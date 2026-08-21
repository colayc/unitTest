//go:build windows

package offlineboundary

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
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
			ownerVerifier: funcVerifierForOwner(7, 8),
			guardianFactory: func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
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
			ownerVerifier: funcVerifierForOwner(7, 8),
			guardianFactory: func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
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
			ownerVerifier: funcVerifierForOwner(7, 8),
			guardianFactory: func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
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
}

func TestGuardianRunClosesSessionAndSendsByeWhenOwnerTerminates(t *testing.T) {
	session := newScriptedGuardianSession()
	engine := &fakeWfpEngine{}
	ownerDone := make(chan struct{})
	close(ownerDone)

	err := runGuardianLoop(context.Background(), guardianRuntime{
		session:       session,
		engineFactory: func() (wfpEngine, error) { return engine, nil },
		leaseIDSource: func() []byte { return []byte("lease-id") },
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

type scriptedGuardianSession struct {
	inbound  chan guardianFrame
	outbound chan guardianFrame
	waitErr  chan error
	closeOnce sync.Once
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
	session.outbound <- frame
	return nil
}

func (session *scriptedGuardianSession) Wait() error {
	return <-session.waitErr
}

func (session *scriptedGuardianSession) Close() error {
	session.closeOnce.Do(func() { close(session.inbound) })
	return nil
}

func (session *scriptedGuardianSession) Kill() error { return nil }

func (session *scriptedGuardianSession) pushInbound(frame guardianFrame) {
	session.inbound <- frame
}

func (session *scriptedGuardianSession) finish(err error) {
	session.closeOnce.Do(func() { close(session.inbound) })
	session.waitErr <- err
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
