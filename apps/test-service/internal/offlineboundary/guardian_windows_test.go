//go:build windows

package offlineboundary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func TestProtocolRejectsOversizedAndUnknownFrames(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		var payload bytes.Buffer
		payload.WriteByte(byte(guardianFrameHello))
		payload.Write(bytes.Repeat([]byte("x"), maxGuardianFramePayloadSize))

		var wire bytes.Buffer
		if err := writeGuardianWireFrame(&wire, payload.Bytes()); err != nil {
			t.Fatalf("writeGuardianWireFrame() error = %v", err)
		}

		if _, err := readGuardianFrame(&wire); !errors.Is(err, errGuardianFrameTooLarge) {
			t.Fatalf("readGuardianFrame() error = %v, want errGuardianFrameTooLarge", err)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		var wire bytes.Buffer
		if err := writeGuardianWireFrame(&wire, []byte{0xff}); err != nil {
			t.Fatalf("writeGuardianWireFrame() error = %v", err)
		}

		if _, err := readGuardianFrame(&wire); !errors.Is(err, errGuardianFrameInvalid) {
			t.Fatalf("readGuardianFrame() error = %v, want errGuardianFrameInvalid", err)
		}
	})

	t.Run("unknown error code", func(t *testing.T) {
		var wire bytes.Buffer
		if err := writeGuardianWireFrame(&wire, []byte{byte(guardianFrameError), 0xff}); err != nil {
			t.Fatalf("writeGuardianWireFrame() error = %v", err)
		}

		if _, err := readGuardianFrame(&wire); !errors.Is(err, errGuardianFrameInvalid) {
			t.Fatalf("readGuardianFrame() error = %v, want errGuardianFrameInvalid", err)
		}
	})
}

func TestGuardianPipeRejectsSameUserRogueBeforeAcceptingAuthenticatedGuardian(t *testing.T) {
	pipeName := guardianPipeName([]byte(fmt.Sprintf("auth-race-%d", time.Now().UnixNano())))
	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{MessageMode: true})
	if err != nil {
		t.Fatalf("ListenPipe() error = %v", err)
	}
	nonce := bytes.Repeat([]byte{0x5a}, 32)
	identity, err := currentOwnerCreationTime(uint32(os.Getpid()))
	if err != nil {
		_ = listener.Close()
		t.Fatalf("currentOwnerCreationTime() error = %v", err)
	}
	guardian := OwnerIdentity{PID: uint32(os.Getpid()), CreationTime: identity}
	owner := OwnerIdentity{PID: 41, CreationTime: 99}
	session := &guardianPipeSession{
		listener: listener, connReady: make(chan guardianConnResult, 1),
		authNonce: nonce, guardian: guardian, owner: owner,
	}
	defer session.Close() //nolint:errcheck
	go session.accept()

	rogue, err := winio.DialPipeContext(context.Background(), pipeName)
	if err != nil {
		t.Fatalf("rogue DialPipeContext() error = %v", err)
	}
	forged := guardianAuthenticationFrame(nonce, guardian, owner)
	forged.Proof[0] ^= 0xff
	rogueWrite := make(chan error, 1)
	go func() { rogueWrite <- writeGuardianFrame(rogue, forged) }()
	accepted := make(chan guardianConnResult, 1)
	go func() {
		conn, awaitErr := session.awaitConn()
		accepted <- guardianConnResult{conn: conn, err: awaitErr}
	}()
	select {
	case result := <-accepted:
		_ = rogue.Close()
		t.Fatalf("rogue connection was accepted before authentication: %v", result.err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := <-rogueWrite; err != nil {
		t.Fatalf("write forged authentication frame: %v", err)
	}
	_ = rogue.Close()

	real, err := winio.DialPipeContext(context.Background(), pipeName)
	if err != nil {
		t.Fatalf("real DialPipeContext() error = %v", err)
	}
	defer real.Close() //nolint:errcheck
	realWrite := make(chan error, 1)
	go func() {
		if writeErr := writeGuardianFrame(real, guardianAuthenticationFrame(nonce, guardian, owner)); writeErr != nil {
			realWrite <- writeErr
			return
		}
		realWrite <- writeGuardianFrame(real, guardianFrame{Kind: guardianFrameHello})
	}()
	select {
	case result := <-accepted:
		if result.err != nil {
			t.Fatalf("awaitConn() error = %v", result.err)
		}
		frame, readErr := readGuardianFrame(result.conn)
		if readErr != nil || frame.Kind != guardianFrameHello {
			t.Fatalf("first post-auth frame = %#v, error = %v; want Hello", frame, readErr)
		}
		if writeErr := <-realWrite; writeErr != nil {
			t.Fatalf("write authenticated frames: %v", writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("authenticated guardian was not accepted")
	}
}

func TestProcesshostRegistrationCapabilityAcknowledgesExactExecutable(t *testing.T) {
	pipeID := bytes.Repeat([]byte{0x31}, 16)
	nonce := bytes.Repeat([]byte{0x42}, 32)
	server, err := newExecutableRegistrationServer(pipeID, nonce)
	if err != nil {
		t.Fatalf("newExecutableRegistrationServer() error = %v", err)
	}
	defer server.Close() //nolint:errcheck
	t.Setenv(registrationPipeEnvironment, registrationPipeName(pipeID))
	t.Setenv(registrationNonceEnvironment, fmt.Sprintf("%x", nonce))
	want := `C:\fixture\clang-cl.exe`
	received := make(chan string, 1)
	go func() {
		request := <-server.requests
		received <- request.path
		request.result <- nil
	}()
	if err := RegisterExecutableForActiveBoundary(want); err != nil {
		t.Fatalf("RegisterExecutableForActiveBoundary() error = %v", err)
	}
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("registered executable = %q, want exact planned identity", got)
		}
	case <-time.After(time.Second):
		t.Fatal("registration request was not delivered")
	}
}

func TestOwnerIdentityVerifierRejectsPIDReuse(t *testing.T) {
	verifier := guardianOwnerVerifierFunc(func(pid uint32) (uint64, error) {
		if pid != 41 {
			t.Fatalf("pid = %d, want 41", pid)
		}
		return 999, nil
	})

	if err := verifier.Verify(OwnerIdentity{PID: 41, CreationTime: 777}); !errors.Is(err, ErrOwnerIdentityMismatch) {
		t.Fatalf("Verify() error = %v, want ErrOwnerIdentityMismatch", err)
	}
}

func TestGuardianLifecycleWaitsForReadyAndClosesWithReleaseByeAndProcessExit(t *testing.T) {
	session := newScriptedGuardianSession()
	boundary := New(Config{
		ownerVerifier: funcVerifierForOwner(42, 99),
		guardianFactory: func(context.Context, OwnerIdentity) (guardianSession, error) {
			return session, nil
		},
		guardianReadyTimeout:   200 * time.Millisecond,
		guardianReleaseTimeout: 200 * time.Millisecond,
	})

	lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 42, CreationTime: 99})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-lease.Ready():
		t.Fatal("Ready closed before guardian sent ready")
	default:
	}

	session.pushInbound(guardianFrame{Kind: guardianFrameHello})
	select {
	case <-lease.Ready():
		t.Fatal("Ready closed after hello only")
	default:
	}

	session.pushInbound(guardianFrame{Kind: guardianFrameReady})
	select {
	case <-lease.Ready():
	case <-time.After(time.Second):
		t.Fatal("Ready did not close after ready frame")
	}

	closeErr := make(chan error, 1)
	go func() { closeErr <- lease.Close() }()

	frame := session.nextOutbound(t)
	if frame.Kind != guardianFrameRelease {
		t.Fatalf("outbound frame = %#v, want release", frame)
	}

	session.pushInbound(guardianFrame{Kind: guardianFrameBye})
	session.finish(nil)

	if err := <-closeErr; err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := lease.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestGuardianLifecycleRejectsWrongOrderTimeoutAndCrash(t *testing.T) {
	t.Run("wrong order", func(t *testing.T) {
		session := newScriptedGuardianSession()
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   time.Second,
			guardianReleaseTimeout: time.Second,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		session.pushInbound(guardianFrame{Kind: guardianFrameReady})
		session.finish(nil)

		if err := lease.Wait(); !errors.Is(err, GuardianStartFailed) {
			t.Fatalf("Wait() error = %v, want GuardianStartFailed", err)
		}
	})

	t.Run("ready timeout", func(t *testing.T) {
		session := newScriptedGuardianSession()
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   25 * time.Millisecond,
			guardianReleaseTimeout: time.Second,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		if err := lease.Wait(); !errors.Is(err, GuardianTimeout) {
			t.Fatalf("Wait() error = %v, want GuardianTimeout", err)
		}
	})

	t.Run("crash before ready", func(t *testing.T) {
		session := newScriptedGuardianSession()
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   time.Second,
			guardianReleaseTimeout: time.Second,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		session.finish(io.EOF)

		if err := lease.Wait(); !errors.Is(err, GuardianStartFailed) {
			t.Fatalf("Wait() error = %v, want GuardianStartFailed", err)
		}
	})

	t.Run("release timeout returns same canonical timeout result", func(t *testing.T) {
		session := newScriptedGuardianSession()
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   time.Second,
			guardianReleaseTimeout: 25 * time.Millisecond,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		session.pushInbound(guardianFrame{Kind: guardianFrameHello})
		session.pushInbound(guardianFrame{Kind: guardianFrameReady})
		<-lease.Ready()

		err = lease.Close()
		if !errors.Is(err, GuardianTimeout) {
			t.Fatalf("first Close() error = %v, want GuardianTimeout", err)
		}
		err2 := lease.Close()
		if !errors.Is(err2, GuardianTimeout) {
			t.Fatalf("second Close() error = %v, want GuardianTimeout", err2)
		}
		if err2.Error() != err.Error() {
			t.Fatalf("second Close() = %q, want same canonical result as %q", err2.Error(), err.Error())
		}
	})

	t.Run("release send failure is canonical and sanitized", func(t *testing.T) {
		session := newScriptedGuardianSession()
		session.sendErr = errors.New(`write \\.\pipe\offlineboundary-secret: access denied`)
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   time.Second,
			guardianReleaseTimeout: time.Second,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		session.pushInbound(guardianFrame{Kind: guardianFrameHello})
		session.pushInbound(guardianFrame{Kind: guardianFrameReady})
		<-lease.Ready()

		err = lease.Close()
		if !errors.Is(err, SessionCloseFailed) {
			t.Fatalf("first Close() error = %v, want SessionCloseFailed", err)
		}
		if strings.Contains(err.Error(), `\\.\pipe\`) || strings.Contains(err.Error(), `access denied`) {
			t.Fatalf("first Close() leaked transport details: %q", err.Error())
		}
		err2 := lease.Close()
		if !errors.Is(err2, SessionCloseFailed) {
			t.Fatalf("second Close() error = %v, want SessionCloseFailed", err2)
		}
		if err2.Error() != err.Error() {
			t.Fatalf("second Close() = %q, want same canonical result as %q", err2.Error(), err.Error())
		}
	})

	t.Run("bye then process wait failure is canonical and sanitized", func(t *testing.T) {
		session := newScriptedGuardianSession()
		boundary := New(Config{
			ownerVerifier:          funcVerifierForOwner(7, 8),
			guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
			guardianReadyTimeout:   time.Second,
			guardianReleaseTimeout: time.Second,
		})

		lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		session.pushInbound(guardianFrame{Kind: guardianFrameHello})
		session.pushInbound(guardianFrame{Kind: guardianFrameReady})
		<-lease.Ready()

		closeErr := make(chan error, 1)
		go func() { closeErr <- lease.Close() }()

		frame := session.nextOutbound(t)
		if frame.Kind != guardianFrameRelease {
			t.Fatalf("outbound frame = %#v, want release", frame)
		}
		session.pushInbound(guardianFrame{Kind: guardianFrameBye})
		session.finish(errors.New(`process wait for C:\guardian\native-offline-guardian.exe exited with details`))

		err = <-closeErr
		if !errors.Is(err, SessionCloseFailed) {
			t.Fatalf("first Close() error = %v, want SessionCloseFailed", err)
		}
		if strings.Contains(err.Error(), `C:\guardian\`) || strings.Contains(err.Error(), `exited with details`) {
			t.Fatalf("first Close() leaked process details: %q", err.Error())
		}
		err2 := lease.Close()
		if !errors.Is(err2, SessionCloseFailed) {
			t.Fatalf("second Close() error = %v, want SessionCloseFailed", err2)
		}
		if err2.Error() != err.Error() {
			t.Fatalf("second Close() = %q, want same canonical result as %q", err2.Error(), err.Error())
		}
	})
}

func TestGuardianLeaseKillWaitsForProcessExitBeforeClosingSessionOrReturning(t *testing.T) {
	session := newScriptedGuardianSession()
	session.sendErrKind = guardianFrameRelease
	session.sendErr = errors.New("injected release send failure")
	boundary := New(Config{
		ownerVerifier:          funcVerifierForOwner(7, 8),
		guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
		guardianReadyTimeout:   time.Second,
		guardianReleaseTimeout: 200 * time.Millisecond,
	})

	lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 7, CreationTime: 8})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	session.pushInbound(guardianFrame{Kind: guardianFrameHello})
	session.pushInbound(guardianFrame{Kind: guardianFrameReady})
	<-lease.Ready()

	closeResult := make(chan error, 1)
	go func() { closeResult <- lease.Close() }()
	select {
	case <-session.killed:
	case <-time.After(time.Second):
		t.Fatal("guardian was not killed after release send failure")
	}
	select {
	case err := <-closeResult:
		t.Fatalf("Close() returned before the killed guardian exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	select {
	case <-session.closed:
		t.Fatal("transport was closed before the killed guardian exited")
	default:
	}

	session.finish(errors.New("guardian exited after kill"))
	if err := <-closeResult; !errors.Is(err, SessionCloseFailed) {
		t.Fatalf("Close() error = %v, want SessionCloseFailed", err)
	}
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("transport was not closed after guardian exit")
	}
}

func TestGuardianStartupCleanupErrorFrameMapsToSessionCloseFailure(t *testing.T) {
	session := newScriptedGuardianSession()
	boundary := New(Config{
		ownerVerifier:          funcVerifierForOwner(42, 99),
		guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
		guardianReadyTimeout:   time.Second,
		guardianReleaseTimeout: time.Second,
	})
	lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 42, CreationTime: 99})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	session.pushInbound(guardianFrame{Kind: guardianFrameHello})
	session.pushInbound(guardianFrame{Kind: guardianFrameError, Code: guardianErrorSessionCloseFailed})
	if err := lease.Wait(); !errors.Is(err, SessionCloseFailed) {
		t.Fatalf("Wait() error = %v, want SessionCloseFailed", err)
	}
	session.finish(errors.New("guardian exited after cleanup failure"))
}

func TestGuardianRunClosesSessionAndSendsByeWhenOwnerTerminates(t *testing.T) {
	session := newScriptedGuardianSession()
	engine := &fakeWfpEngine{}
	ownerDone := make(chan struct{})
	close(ownerDone)

	err := runGuardianLoop(context.Background(), guardianRuntime{
		session:       session,
		engineFactory: func() (wfpEngine, error) { return engine, nil },
		leaseIDSource: func() ([]byte, error) { return []byte("lease-id"), nil },
		owner:         fakeGuardianOwnerWatcher{done: ownerDone},
	}, OwnerIdentity{PID: 9, CreationTime: 10})
	if err != nil {
		t.Fatalf("runGuardianLoop() error = %v", err)
	}

	if got := session.nextOutbound(t).Kind; got != guardianFrameHello {
		t.Fatalf("first outbound kind = %v, want hello", got)
	}
	if got := session.nextOutbound(t).Kind; got != guardianFrameReady {
		t.Fatalf("second outbound kind = %v, want ready", got)
	}
	if got := session.nextOutbound(t).Kind; got != guardianFrameBye {
		t.Fatalf("third outbound kind = %v, want bye", got)
	}
	if engine.closeCalls != 1 {
		t.Fatalf("engine close calls = %d, want 1", engine.closeCalls)
	}
}

func TestGuardianRunJoinsStartupEngineCloseFailuresAndNeverReportsAccessDeniedSkip(t *testing.T) {
	closeFailure := errors.New("injected engine close failure")
	tests := []struct {
		name          string
		engine        *faultingGuardianEngine
		readySendErr  error
		wantPrimary   error
		wantErrorCode guardianErrorCode
	}{
		{
			name:          "add failure",
			engine:        &faultingGuardianEngine{addErr: WFPAccessDenied, closeErr: closeFailure},
			wantPrimary:   WFPAccessDenied,
			wantErrorCode: guardianErrorSessionCloseFailed,
		},
		{
			name:          "audit failure",
			engine:        &faultingGuardianEngine{auditErr: FilterAuditFailed, closeErr: closeFailure},
			wantPrimary:   FilterAuditFailed,
			wantErrorCode: guardianErrorSessionCloseFailed,
		},
		{
			name:          "ready send failure",
			engine:        &faultingGuardianEngine{closeErr: closeFailure},
			readySendErr:  errors.New("injected ready send failure"),
			wantErrorCode: guardianErrorSessionCloseFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newScriptedGuardianSession()
			session.sendErrKind = guardianFrameReady
			session.sendErr = test.readySendErr
			ownerDone := make(chan struct{})
			err := runGuardianLoop(context.Background(), guardianRuntime{
				session:       session,
				engineFactory: func() (wfpEngine, error) { return test.engine, nil },
				leaseIDSource: func() ([]byte, error) { return []byte("lease-id"), nil },
				owner:         fakeGuardianOwnerWatcher{done: ownerDone},
			}, OwnerIdentity{PID: 9, CreationTime: 10})

			if !errors.Is(err, SessionCloseFailed) {
				t.Fatalf("runGuardianLoop() error = %v, want SessionCloseFailed", err)
			}
			if !errors.Is(err, closeFailure) {
				t.Fatalf("runGuardianLoop() error = %v, want injected close failure", err)
			}
			if test.wantPrimary != nil && !errors.Is(err, test.wantPrimary) {
				t.Fatalf("runGuardianLoop() error = %v, want primary %v", err, test.wantPrimary)
			}
			if test.readySendErr != nil && !errors.Is(err, test.readySendErr) {
				t.Fatalf("runGuardianLoop() error = %v, want ready send failure", err)
			}
			if test.engine.closeCalls != 1 {
				t.Fatalf("engine close calls = %d, want 1", test.engine.closeCalls)
			}
			if got := session.nextOutbound(t).Kind; got != guardianFrameHello {
				t.Fatalf("first outbound kind = %v, want hello", got)
			}
			if test.wantErrorCode != 0 {
				frame := session.nextOutbound(t)
				if frame.Kind != guardianFrameError || frame.Code != test.wantErrorCode {
					t.Fatalf("startup error frame = %#v, want code %v", frame, test.wantErrorCode)
				}
			}
		})
	}
}

func TestGuardianStartSanitizesVerifierAndLauncherFailures(t *testing.T) {
	t.Run("owner verifier failure", func(t *testing.T) {
		_, err := New(Config{
			ownerVerifier: guardianOwnerVerifierFunc(func(uint32) (uint64, error) {
				return 0, errors.New(`C:\secret\owner-verifier\failure.txt`)
			}),
		}).Start(context.Background(), OwnerIdentity{PID: 12, CreationTime: 34})
		if err == nil {
			t.Fatal("Start() error = nil")
		}
		if strings.Contains(err.Error(), `C:\`) {
			t.Fatalf("Start() leaked path in error %q", err)
		}
		if !errors.Is(err, ErrOwnerIdentityMismatch) && !errors.Is(err, GuardianStartFailed) {
			t.Fatalf("Start() error = %v, want canonical sentinel", err)
		}
	})

	t.Run("guardian launcher failure", func(t *testing.T) {
		_, err := New(Config{
			ownerVerifier: funcVerifierForOwner(12, 34),
			guardianFactory: func(context.Context, OwnerIdentity) (guardianSession, error) {
				return nil, errors.New(`CreateProcess C:\very\secret\native-offline-guardian.exe failed`)
			},
		}).Start(context.Background(), OwnerIdentity{PID: 12, CreationTime: 34})
		if err == nil {
			t.Fatal("Start() error = nil")
		}
		if strings.Contains(err.Error(), `C:\`) {
			t.Fatalf("Start() leaked path in error %q", err)
		}
		if !errors.Is(err, GuardianStartFailed) {
			t.Fatalf("Start() error = %v, want GuardianStartFailed", err)
		}
	})
}

func TestGuardianExecutablePathUsesExplicitCallerConfig(t *testing.T) {
	path := `C:\callers\provided\native-offline-guardian.exe`
	boundary := New(Config{GuardianExecutablePath: path}).(*boundary)

	got, err := boundary.resolveGuardianExecutablePath()
	if err != nil {
		t.Fatalf("resolveGuardianExecutablePath() error = %v", err)
	}
	if got != path {
		t.Fatalf("resolveGuardianExecutablePath() = %q, want %q", got, path)
	}
}

type scriptedGuardianSession struct {
	inbound     chan guardianFrame
	outbound    chan guardianFrame
	waitErr     chan error
	sendErr     error
	sendErrKind guardianFrameKind
	inboundOnce sync.Once
	finishOnce  sync.Once
	closeOnce   sync.Once
	killOnce    sync.Once
	killed      chan struct{}
	closed      chan struct{}
}

type fakeGuardianOwnerWatcher struct {
	done      <-chan struct{}
	verifyErr error
}

func newScriptedGuardianSession() *scriptedGuardianSession {
	return &scriptedGuardianSession{
		inbound:  make(chan guardianFrame, 8),
		outbound: make(chan guardianFrame, 8),
		waitErr:  make(chan error, 1),
		killed:   make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (session *scriptedGuardianSession) Receive(context.Context) (guardianFrame, error) {
	frame, ok := <-session.inbound
	if !ok {
		return guardianFrame{}, io.EOF
	}
	return frame, nil
}

func (session *scriptedGuardianSession) Send(_ context.Context, frame guardianFrame) error {
	if session.sendErr != nil && (session.sendErrKind == 0 || session.sendErrKind == frame.Kind) {
		return session.sendErr
	}
	session.outbound <- frame
	return nil
}

type faultingGuardianEngine struct {
	addErr     error
	auditErr   error
	closeErr   error
	closeCalls int
}

func (engine *faultingGuardianEngine) AddOutboundBlockFilters(context.Context, []byte, []string) error {
	return engine.addErr
}

func (engine *faultingGuardianEngine) RegisterExecutable(context.Context, []byte, string) error {
	return nil
}

func (engine *faultingGuardianEngine) AuditOutboundBlockFilters(context.Context, []byte) error {
	return engine.auditErr
}

func (engine *faultingGuardianEngine) Close() error {
	engine.closeCalls++
	return engine.closeErr
}

func (session *scriptedGuardianSession) Wait() error {
	return <-session.waitErr
}

func (session *scriptedGuardianSession) Close() error {
	session.closeOnce.Do(func() {
		session.inboundOnce.Do(func() { close(session.inbound) })
		close(session.closed)
	})
	return nil
}

func (session *scriptedGuardianSession) Kill() error {
	session.killOnce.Do(func() { close(session.killed) })
	return nil
}

func (session *scriptedGuardianSession) pushInbound(frame guardianFrame) {
	session.inbound <- frame
}

func (session *scriptedGuardianSession) finish(err error) {
	session.finishOnce.Do(func() {
		session.inboundOnce.Do(func() { close(session.inbound) })
		session.waitErr <- err
	})
}

func (session *scriptedGuardianSession) nextOutbound(t *testing.T) guardianFrame {
	t.Helper()
	select {
	case frame := <-session.outbound:
		return frame
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbound frame")
		return guardianFrame{}
	}
}

func (watcher fakeGuardianOwnerWatcher) Verify(OwnerIdentity) error { return watcher.verifyErr }

func (watcher fakeGuardianOwnerWatcher) Done() <-chan struct{} { return watcher.done }

func (watcher fakeGuardianOwnerWatcher) Close() error { return nil }

func funcVerifierForOwner(pid uint32, creationTime uint64) guardianOwnerVerifier {
	return guardianOwnerVerifierFunc(func(value uint32) (uint64, error) {
		if value != pid {
			return 0, ErrOwnerIdentityMismatch
		}
		return creationTime, nil
	})
}
