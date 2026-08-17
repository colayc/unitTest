package probe

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestSupervisorKeepsBoundaryAliveAfterTargetExitUntilParentRelease(t *testing.T) {
	controlReader, controlWriter := io.Pipe()
	statusReader, statusWriter := io.Pipe()
	request := supervisorRequest{
		Version: supervisorProtocolVersion,
		Spec: Spec{
			Executable: `C:\fixed\cmake.exe`,
			Args:       []string{"--version=json-v1"},
			Env:        []string{"ONLY=explicit"},
			Dir:        `C:\fixed`,
		},
	}
	target := &fakeSupervisedTarget{pid: 41, exitCode: 7}
	started := make(chan Spec, 1)
	done := make(chan int, 1)
	go func() {
		done <- runSupervisorProtocol(
			controlReader,
			statusWriter,
			io.Discard,
			io.Discard,
			func(spec Spec, _, _ io.Writer) (supervisedTarget, error) {
				started <- spec
				return target, nil
			},
		)
	}()
	if err := writeSupervisorFrame(controlWriter, request); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var startStatus supervisorStatus
	if err := readSupervisorFrame(statusReader, &startStatus); err != nil {
		t.Fatalf("read start status: %v", err)
	}
	if startStatus.Kind != supervisorStatusStarted || startStatus.PID != 41 {
		t.Fatalf("start status = %#v", startStatus)
	}
	var exitStatus supervisorStatus
	if err := readSupervisorFrame(statusReader, &exitStatus); err != nil {
		t.Fatalf("read exit status: %v", err)
	}
	if exitStatus.Kind != supervisorStatusExited || exitStatus.ExitCode != 7 {
		t.Fatalf("exit status = %#v", exitStatus)
	}
	if got := <-started; !reflect.DeepEqual(got, request.Spec) {
		t.Fatalf("target spec = %#v, want %#v", got, request.Spec)
	}

	select {
	case code := <-done:
		t.Fatalf("supervisor exited with %d before parent release", code)
	case <-time.After(50 * time.Millisecond):
	}
	if err := controlWriter.Close(); err != nil {
		t.Fatalf("release supervisor: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("supervisor code = %d, want 0", code)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not observe parent release")
	}
	_ = statusReader.Close()
}

func TestSupervisorProtocolRejectsOversizedRequestBeforeStartingTarget(t *testing.T) {
	frame := bytes.NewBuffer(nil)
	frame.Write([]byte{0, 4, 0, 1})
	called := false
	code := runSupervisorProtocol(
		frame,
		io.Discard,
		io.Discard,
		io.Discard,
		func(Spec, io.Writer, io.Writer) (supervisedTarget, error) {
			called = true
			return nil, errors.New("must not start")
		},
	)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if called {
		t.Fatal("oversized request started a target")
	}
}

func TestSupervisorFrameWriterCompletesShortWrites(t *testing.T) {
	var encoded bytes.Buffer
	writer := shortWriter{destination: &encoded, maximum: 2}
	want := supervisorStatus{
		Version: supervisorProtocolVersion,
		Kind:    supervisorStatusStarted,
		PID:     47,
	}
	if err := writeSupervisorFrame(writer, want); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	var got supervisorStatus
	if err := readSupervisorFrame(&encoded, &got); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
}

func TestSupervisorObservesParentReleaseWhileTargetIsStillRunning(t *testing.T) {
	controlReader, controlWriter := io.Pipe()
	statusReader, statusWriter := io.Pipe()
	targetRelease := make(chan struct{})
	target := &blockingSupervisedTarget{pid: 43, release: targetRelease}
	done := make(chan int, 1)
	go func() {
		done <- runSupervisorProtocol(
			controlReader,
			statusWriter,
			io.Discard,
			io.Discard,
			func(Spec, io.Writer, io.Writer) (supervisedTarget, error) {
				return target, nil
			},
		)
	}()
	if err := writeSupervisorFrame(controlWriter, supervisorRequest{
		Version: supervisorProtocolVersion,
		Spec:    Spec{Executable: `C:\fixed\cmake.exe`, Env: []string{}},
	}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var startStatus supervisorStatus
	if err := readSupervisorFrame(statusReader, &startStatus); err != nil {
		t.Fatalf("read start status: %v", err)
	}
	if startStatus.Kind != supervisorStatusStarted {
		t.Fatalf("start status = %#v", startStatus)
	}
	if err := controlWriter.Close(); err != nil {
		t.Fatalf("release supervisor: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("supervisor code = %d, want 0", code)
		}
		_ = statusReader.Close()
		close(targetRelease)
	case <-time.After(100 * time.Millisecond):
		close(targetRelease)
		var exitStatus supervisorStatus
		_ = readSupervisorFrame(statusReader, &exitStatus)
		<-done
		t.Fatal("supervisor ignored parent release while target was running")
	}
	_ = statusReader.Close()
}

func TestPinnedSupervisorBoundaryPinsBeforeNumericGroupSignal(t *testing.T) {
	var events []string
	err := terminatePinnedSupervisorBoundary(
		func() error {
			events = append(events, "pidfd-stop")
			return nil
		},
		func() error {
			events = append(events, "group-kill")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("terminate boundary: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"pidfd-stop", "group-kill"}) {
		t.Fatalf("events = %#v", events)
	}
}

func TestPinnedSupervisorBoundaryNeverSignalsNumericGroupWhenPinFails(t *testing.T) {
	pinFailure := errors.New("supervisor unavailable")
	groupSignals := 0
	err := terminatePinnedSupervisorBoundary(
		func() error { return pinFailure },
		func() error {
			groupSignals++
			return nil
		},
	)
	if !errors.Is(err, pinFailure) {
		t.Fatalf("error = %v, want pin failure", err)
	}
	if groupSignals != 0 {
		t.Fatalf("numeric group signals = %d, want 0", groupSignals)
	}
}

type fakeSupervisedTarget struct {
	pid      int
	exitCode int
	err      error
}

func (target *fakeSupervisedTarget) PID() int { return target.pid }

func (target *fakeSupervisedTarget) Wait() (int, error) {
	return target.exitCode, target.err
}

type blockingSupervisedTarget struct {
	pid     int
	release <-chan struct{}
}

func (target *blockingSupervisedTarget) PID() int { return target.pid }

func (target *blockingSupervisedTarget) Wait() (int, error) {
	<-target.release
	return 0, nil
}

type shortWriter struct {
	destination io.Writer
	maximum     int
}

func (writer shortWriter) Write(value []byte) (int, error) {
	if len(value) > writer.maximum {
		value = value[:writer.maximum]
	}
	return writer.destination.Write(value)
}
