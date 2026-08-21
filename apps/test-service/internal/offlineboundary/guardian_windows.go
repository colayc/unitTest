//go:build windows

package offlineboundary

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	defaultGuardianReadyTimeout   = 5 * time.Second
	defaultGuardianReleaseTimeout = 5 * time.Second
)

type guardianSession interface {
	Receive(context.Context) (guardianFrame, error)
	Send(context.Context, guardianFrame) error
	Wait() error
	Close() error
	Kill() error
}

type guardianOwnerVerifier interface {
	Verify(OwnerIdentity) error
}

type guardianOwnerVerifierFunc func(uint32) (uint64, error)

func (fn guardianOwnerVerifierFunc) Verify(owner OwnerIdentity) error {
	if fn == nil {
		return ErrOwnerIdentityMismatch
	}
	value, err := fn(owner.PID)
	if err != nil {
		return err
	}
	if value == 0 || value != owner.CreationTime {
		return ErrOwnerIdentityMismatch
	}
	return nil
}

type guardianLease struct {
	session        guardianSession
	ready          chan struct{}
	done           chan struct{}
	releaseRequest chan struct{}
	readyTimeout   time.Duration
	releaseTimeout time.Duration

	readyOnce sync.Once
	doneOnce  sync.Once
	closeOnce sync.Once

	mu       sync.Mutex
	finalErr error
}

type guardianReceiveResult struct {
	frame guardianFrame
	err   error
}

type guardianTransport interface {
	Receive(context.Context) (guardianFrame, error)
	Send(context.Context, guardianFrame) error
	Close() error
}

type guardianOwnerWatcher interface {
	Verify(OwnerIdentity) error
	Done() <-chan struct{}
	Close() error
}

type guardianRuntime struct {
	session       guardianTransport
	engineFactory func() (wfpEngine, error)
	leaseIDSource func() []byte
	owner         guardianOwnerWatcher
}

type guardianPipeSession struct {
	cmd       *exec.Cmd
	listener  net.Listener
	connReady chan guardianConnResult

	closeOnce sync.Once
	mu        sync.Mutex
	conn      net.Conn
}

type guardianOwnerMonitor struct {
	handle windows.Handle
	done   chan struct{}
	once   sync.Once
}

type guardianConnResult struct {
	conn net.Conn
	err  error
}

func (boundary *boundary) Start(ctx context.Context, owner OwnerIdentity) (Lease, error) {
	if err := validateOwnerIdentity(owner); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ownerVerifier := boundary.ownerVerifier
	if ownerVerifier == nil {
		ownerVerifier = guardianOwnerVerifierFunc(currentOwnerCreationTime)
	}
	if err := ownerVerifier.Verify(owner); err != nil {
		return nil, ErrOwnerIdentityMismatch
	}

	factory := boundary.guardianFactory
	if factory == nil {
		factory = boundary.startGuardianProcess
	}
	session, err := factory(ctx, owner)
	if err != nil {
		return nil, GuardianStartFailed
	}

	lease := &guardianLease{
		session:        session,
		ready:          make(chan struct{}),
		done:           make(chan struct{}),
		releaseRequest: make(chan struct{}),
		readyTimeout:   boundary.guardianReadyTimeout,
		releaseTimeout: boundary.guardianReleaseTimeout,
	}
	if lease.readyTimeout <= 0 {
		lease.readyTimeout = defaultGuardianReadyTimeout
	}
	if lease.releaseTimeout <= 0 {
		lease.releaseTimeout = defaultGuardianReleaseTimeout
	}
	go lease.run()
	return lease, nil
}

func (lease *guardianLease) Ready() <-chan struct{} { return lease.ready }

func (lease *guardianLease) Close() error {
	lease.closeOnce.Do(func() { close(lease.releaseRequest) })
	return lease.Wait()
}

func (lease *guardianLease) Wait() error {
	<-lease.done
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.finalErr
}

func (lease *guardianLease) run() {
	defer lease.finish(nil)

	recv := make(chan guardianReceiveResult, 1)
	go lease.receiveFrames(recv)
	waitCh := make(chan error, 1)
	go func() { waitCh <- lease.session.Wait() }()

	readyTimer := time.NewTimer(lease.readyTimeout)
	defer readyTimer.Stop()

	var releaseTimer *time.Timer
	var releaseTimeout <-chan time.Time
	readySeen := false
	helloSeen := false
	releaseSent := false
	byeSeen := false
	waitErr := error(nil)
	waitDone := false

	sendRelease := func() error {
		if releaseSent {
			return nil
		}
		if err := lease.session.Send(context.Background(), guardianFrame{Kind: guardianFrameRelease}); err != nil {
			return SessionCloseFailed
		}
		releaseSent = true
		releaseTimer = time.NewTimer(lease.releaseTimeout)
		releaseTimeout = releaseTimer.C
		return nil
	}

	for {
		if byeSeen && waitDone {
			if waitErr != nil {
				lease.finish(SessionCloseFailed)
			}
			return
		}

		select {
		case <-readyTimer.C:
			_ = lease.session.Kill()
			lease.finish(GuardianTimeout)
			return
		case <-lease.releaseRequest:
			if readySeen {
				if err := sendRelease(); err != nil {
					_ = lease.session.Kill()
					lease.finish(err)
					return
				}
			}
		case <-releaseTimeout:
			_ = lease.session.Kill()
			lease.finish(GuardianTimeout)
			return
		case result := <-recv:
			if result.err != nil {
				if !readySeen {
					_ = lease.session.Kill()
					lease.finish(GuardianStartFailed)
					return
				}
				if releaseSent {
					_ = lease.session.Kill()
					lease.finish(SessionCloseFailed)
					return
				}
				lease.finish(SessionCloseFailed)
				return
			}
			switch result.frame.Kind {
			case guardianFrameHello:
				if helloSeen || readySeen {
					_ = lease.session.Kill()
					lease.finish(GuardianStartFailed)
					return
				}
				helloSeen = true
			case guardianFrameReady:
				if !helloSeen || readySeen {
					_ = lease.session.Kill()
					lease.finish(GuardianStartFailed)
					return
				}
				readySeen = true
				if !readyTimer.Stop() {
					select {
					case <-readyTimer.C:
					default:
					}
				}
				lease.readyOnce.Do(func() { close(lease.ready) })
				select {
				case <-lease.releaseRequest:
					if err := sendRelease(); err != nil {
						_ = lease.session.Kill()
						lease.finish(err)
						return
					}
				default:
				}
			case guardianFrameError:
				_ = lease.session.Kill()
				if !readySeen {
					lease.finish(GuardianStartFailed)
					return
				}
				lease.finish(SessionCloseFailed)
				return
			case guardianFrameBye:
				if !readySeen {
					_ = lease.session.Kill()
					lease.finish(GuardianStartFailed)
					return
				}
				byeSeen = true
			default:
				_ = lease.session.Kill()
				lease.finish(GuardianStartFailed)
				return
			}
		case err := <-waitCh:
			waitDone = true
			waitErr = err
			if !readySeen {
				if err != nil {
					lease.finish(GuardianStartFailed)
				} else if !byeSeen {
					lease.finish(GuardianStartFailed)
				}
				return
			}
			if byeSeen {
				if releaseTimer != nil {
					releaseTimer.Stop()
				}
				if err != nil {
					lease.finish(SessionCloseFailed)
				}
				return
			}
			if err != nil {
				lease.finish(SessionCloseFailed)
				return
			}
		}
	}
}

func (lease *guardianLease) receiveFrames(out chan<- guardianReceiveResult) {
	for {
		frame, err := lease.session.Receive(context.Background())
		out <- guardianReceiveResult{frame: frame, err: err}
		if err != nil {
			return
		}
	}
}

func (lease *guardianLease) finish(err error) {
	lease.doneOnce.Do(func() {
		lease.mu.Lock()
		lease.finalErr = err
		lease.mu.Unlock()
		_ = lease.session.Close()
		close(lease.done)
	})
}

func (boundary *boundary) startGuardianProcess(ctx context.Context, owner OwnerIdentity) (guardianSession, error) {
	executable, err := boundary.resolveGuardianExecutablePath()
	if err != nil {
		return nil, GuardianStartFailed
	}
	pipeName := guardianPipeName(newLeaseID())
	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		MessageMode:      true,
		InputBufferSize:  4096,
		OutputBufferSize: 4096,
	})
	if err != nil {
		return nil, GuardianStartFailed
	}

	args := []string{
		"--owner-pid=" + strconv.FormatUint(uint64(owner.PID), 10),
		"--owner-creation-time=" + strconv.FormatUint(owner.CreationTime, 10),
		"--ipc-address=" + pipeName,
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		_ = listener.Close()
		return nil, GuardianStartFailed
	}
	session := &guardianPipeSession{
		cmd:       cmd,
		listener:  listener,
		connReady: make(chan guardianConnResult, 1),
	}
	go session.accept()
	return session, nil
}

func (boundary *boundary) resolveGuardianExecutablePath() (string, error) {
	if boundary.guardianExecutablePath != "" {
		return boundary.guardianExecutablePath, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "native-offline-guardian.exe"), nil
}

func guardianPipeName(leaseID []byte) string {
	return fmt.Sprintf(`\\.\pipe\offlineboundary-%x`, leaseID)
}

func (session *guardianPipeSession) Receive(context.Context) (guardianFrame, error) {
	conn, err := session.awaitConn()
	if err != nil {
		return guardianFrame{}, err
	}
	return readGuardianFrame(conn)
}

func (session *guardianPipeSession) Send(_ context.Context, frame guardianFrame) error {
	conn, err := session.awaitConn()
	if err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return writeGuardianFrame(conn, frame)
}

func (session *guardianPipeSession) Wait() error {
	if session.cmd == nil {
		return nil
	}
	return session.cmd.Wait()
}

func (session *guardianPipeSession) Close() error {
	var closeErr error
	session.closeOnce.Do(func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.conn != nil {
			closeErr = session.conn.Close()
		}
		if session.listener != nil {
			if err := session.listener.Close(); closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func (session *guardianPipeSession) Kill() error {
	if session.cmd == nil || session.cmd.Process == nil {
		return nil
	}
	return session.cmd.Process.Kill()
}

func (session *guardianPipeSession) accept() {
	conn, err := session.listener.Accept()
	session.connReady <- guardianConnResult{conn: conn, err: err}
}

func (session *guardianPipeSession) awaitConn() (net.Conn, error) {
	session.mu.Lock()
	if session.conn != nil {
		conn := session.conn
		session.mu.Unlock()
		return conn, nil
	}
	session.mu.Unlock()

	result := <-session.connReady
	if result.err != nil {
		return nil, result.err
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.conn == nil {
		session.conn = result.conn
	}
	return session.conn, nil
}

func RunNativeGuardian(owner OwnerIdentity, ipcAddress string) error {
	return sanitizeGuardianChildError(runNativeGuardian(owner, ipcAddress))
}

func runNativeGuardian(owner OwnerIdentity, ipcAddress string) error {
	if ipcAddress == "" {
		return GuardianStartFailed
	}
	conn, err := winio.DialPipeContext(context.Background(), ipcAddress)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck

	ownerMonitor, err := newGuardianOwnerMonitor(owner)
	if err != nil {
		_ = writeGuardianFrame(conn, guardianFrame{Kind: guardianFrameError, Code: guardianErrorStartup})
		return err
	}
	defer ownerMonitor.Close() //nolint:errcheck

	runtime := guardianRuntime{
		session:       guardianConnTransport{conn: conn},
		engineFactory: defaultWFPEngineFactory,
		leaseIDSource: newLeaseID,
		owner:         ownerMonitor,
	}
	return runGuardianLoop(context.Background(), runtime, owner)
}

type guardianConnTransport struct {
	conn net.Conn
}

func (transport guardianConnTransport) Receive(context.Context) (guardianFrame, error) {
	return readGuardianFrame(transport.conn)
}

func (transport guardianConnTransport) Send(_ context.Context, frame guardianFrame) error {
	return writeGuardianFrame(transport.conn, frame)
}

func (transport guardianConnTransport) Close() error {
	if transport.conn != nil {
		return transport.conn.Close()
	}
	return nil
}

func runGuardianLoop(ctx context.Context, runtime guardianRuntime, owner OwnerIdentity) error {
	if err := validateOwnerIdentity(owner); err != nil {
		return err
	}
	if err := runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameHello}); err != nil {
		return err
	}
	if err := runtime.owner.Verify(owner); err != nil {
		_ = runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameError, Code: guardianErrorStartup})
		return err
	}
	engineFactory := runtime.engineFactory
	if engineFactory == nil {
		engineFactory = defaultWFPEngineFactory
	}
	engine, err := engineFactory()
	if err != nil {
		_ = runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameError, Code: guardianErrorStartup})
		return err
	}
	leaseIDSource := runtime.leaseIDSource
	if leaseIDSource == nil {
		leaseIDSource = newLeaseID
	}
	leaseID := leaseIDSource()
	if err := engine.AddOutboundBlockFilters(ctx, leaseID); err != nil {
		_ = engine.Close()
		_ = runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameError, Code: guardianErrorStartup})
		return err
	}
	if err := engine.AuditOutboundBlockFilters(ctx, leaseID); err != nil {
		_ = engine.Close()
		_ = runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameError, Code: guardianErrorStartup})
		return err
	}
	if err := runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameReady}); err != nil {
		_ = engine.Close()
		return err
	}

	release := make(chan guardianReceiveResult, 1)
	go func() {
		frame, recvErr := runtime.session.Receive(ctx)
		release <- guardianReceiveResult{frame: frame, err: recvErr}
	}()

	select {
	case <-ctx.Done():
	case <-runtime.owner.Done():
	case result := <-release:
		if result.err != nil {
			_ = engine.Close()
			return result.err
		}
		if result.frame.Kind != guardianFrameRelease {
			_ = engine.Close()
			return errGuardianFrameInvalid
		}
	}
	closeErr := engine.Close()
	if err := runtime.session.Send(ctx, guardianFrame{Kind: guardianFrameBye}); err != nil {
		if closeErr != nil {
			return errors.Join(closeErr, err)
		}
		return err
	}
	return closeErr
}

func newGuardianOwnerMonitor(owner OwnerIdentity) (*guardianOwnerMonitor, error) {
	if err := validateOwnerIdentity(owner); err != nil {
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, owner.PID)
	if err != nil {
		return nil, err
	}
	monitor := &guardianOwnerMonitor{
		handle: handle,
		done:   make(chan struct{}),
	}
	if err := monitor.Verify(owner); err != nil {
		monitor.Close() //nolint:errcheck
		return nil, err
	}
	go func() {
		_, _ = windows.WaitForSingleObject(monitor.handle, windows.INFINITE)
		monitor.once.Do(func() { close(monitor.done) })
	}()
	return monitor, nil
}

func (monitor *guardianOwnerMonitor) Verify(owner OwnerIdentity) error {
	current, err := ownerCreationTimeFromHandle(monitor.handle)
	if err != nil {
		return err
	}
	if current != owner.CreationTime {
		return ErrOwnerIdentityMismatch
	}
	return nil
}

func (monitor *guardianOwnerMonitor) Done() <-chan struct{} { return monitor.done }

func (monitor *guardianOwnerMonitor) Close() error {
	if monitor == nil || monitor.handle == 0 || monitor.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(monitor.handle)
	monitor.handle = 0
	return err
}

func currentOwnerCreationTime(pid uint32) (uint64, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle) //nolint:errcheck
	return ownerCreationTimeFromHandle(handle)
}

func ownerCreationTimeFromHandle(handle windows.Handle) (uint64, error) {
	if handle == 0 || handle == windows.InvalidHandle {
		return 0, ErrOwnerIdentityMismatch
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	value := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if value == 0 {
		return 0, ErrOwnerIdentityMismatch
	}
	return value, nil
}

func sanitizeGuardianChildError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrOwnerIdentityMismatch) {
		return ErrOwnerIdentityMismatch
	}
	return GuardianStartFailed
}
