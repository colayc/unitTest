package processhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/processhost"
)

type targetResult struct {
	code int
	err  error
}

type fakeTarget struct {
	pid, group int
	done       chan targetResult
}

func (t *fakeTarget) PID() int          { return t.pid }
func (t *fakeTarget) ProcessGroup() int { return t.group }
func (t *fakeTarget) Wait() (int, error) {
	result := <-t.done
	return result.code, result.err
}

type fakePlatform struct {
	target       *fakeTarget
	startErr     error
	terminateErr error
	onTerminate  func()
	started      []processcontrol.Spec
	terminations int
	mu           sync.Mutex
}

type batchFakePlatform struct {
	targets      []*fakeTarget
	started      []processcontrol.Spec
	terminations map[int]int
	mu           sync.Mutex
}

func (p *batchFakePlatform) Start(
	spec processcontrol.Spec,
	stdout, stderr io.Writer,
) (processhost.Target, error) {
	p.mu.Lock()
	index := len(p.started)
	p.started = append(p.started, spec)
	p.mu.Unlock()
	if index >= len(p.targets) {
		return nil, errors.New("unexpected target")
	}
	if len(spec.Args) != 0 {
		_, _ = io.WriteString(stdout, "stdout-"+spec.Args[0])
		_, _ = io.WriteString(stderr, "stderr-"+spec.Args[0])
	}
	return p.targets[index], nil
}

func (p *batchFakePlatform) Terminate(
	target processhost.Target,
	_ time.Duration,
) error {
	p.mu.Lock()
	if p.terminations == nil {
		p.terminations = make(map[int]int)
	}
	p.terminations[target.PID()]++
	p.mu.Unlock()
	candidate := target.(*fakeTarget)
	select {
	case candidate.done <- targetResult{}:
	default:
	}
	return nil
}

func (p *batchFakePlatform) terminationCount(pid int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminations[pid]
}

func (p *fakePlatform) Start(spec processcontrol.Spec, stdout, stderr io.Writer) (processhost.Target, error) {
	p.mu.Lock()
	p.started = append(p.started, spec)
	p.mu.Unlock()
	if p.startErr != nil {
		return nil, p.startErr
	}
	return p.target, nil
}

func (p *fakePlatform) Terminate(target processhost.Target, _ time.Duration) error {
	p.mu.Lock()
	p.terminations++
	onTerminate := p.onTerminate
	p.mu.Unlock()
	if onTerminate != nil {
		onTerminate()
	}
	select {
	case p.target.done <- targetResult{}:
	default:
	}
	return p.terminateErr
}

type stagedControl struct {
	first, second *bytes.Reader
	releaseSecond chan struct{}
	secondRead    chan struct{}
	closed        chan struct{}
	readOnce      sync.Once
	closeOnce     sync.Once
}

func newStagedControl(first, second []byte) *stagedControl {
	return &stagedControl{
		first:         bytes.NewReader(first),
		second:        bytes.NewReader(second),
		releaseSecond: make(chan struct{}),
		secondRead:    make(chan struct{}),
		closed:        make(chan struct{}),
	}
}

func (control *stagedControl) Read(buffer []byte) (int, error) {
	if control.first.Len() > 0 {
		return control.first.Read(buffer)
	}
	select {
	case <-control.releaseSecond:
	case <-control.closed:
		return 0, io.ErrClosedPipe
	}
	if control.second.Len() > 0 {
		count, err := control.second.Read(buffer)
		control.readOnce.Do(func() { close(control.secondRead) })
		return count, err
	}
	<-control.closed
	return 0, io.ErrClosedPipe
}

func (control *stagedControl) Close() error {
	control.closeOnce.Do(func() { close(control.closed) })
	return nil
}

type strictCloseControl struct {
	reader *bytes.Reader
	mu     sync.Mutex
	closes int
}

type gatedNonCloser struct {
	readCalled chan struct{}
	release    chan struct{}
	once       sync.Once
}

func newGatedNonCloser() *gatedNonCloser {
	return &gatedNonCloser{readCalled: make(chan struct{}), release: make(chan struct{})}
}

func (reader *gatedNonCloser) Read([]byte) (int, error) {
	reader.once.Do(func() { close(reader.readCalled) })
	<-reader.release
	return 0, io.EOF
}

func (control *strictCloseControl) Read(buffer []byte) (int, error) {
	return control.reader.Read(buffer)
}

func (control *strictCloseControl) Close() error {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.closes++
	if control.closes > 1 {
		return errors.New("control closed more than once")
	}
	return nil
}

func (control *strictCloseControl) closeCount() int {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.closes
}

func (p *fakePlatform) terminationCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminations
}

type blockingControl struct {
	data   *bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func newBlockingControl(data []byte) *blockingControl {
	return &blockingControl{data: bytes.NewReader(data), closed: make(chan struct{})}
}

func (r *blockingControl) Read(buffer []byte) (int, error) {
	if r.data.Len() > 0 {
		return r.data.Read(buffer)
	}
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingControl) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func commandBytes(t *testing.T, commands ...processcontrol.HostCommand) []byte {
	t.Helper()
	var result bytes.Buffer
	for _, command := range commands {
		if err := json.NewEncoder(&result).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	return result.Bytes()
}

func decodeStatuses(t *testing.T, data []byte) []processcontrol.HostStatus {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var statuses []processcontrol.HostStatus
	for {
		var status processcontrol.HostStatus
		if err := decoder.Decode(&status); errors.Is(err, io.EOF) {
			return statuses
		} else if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
}

func TestRunStartsTargetAndReportsNaturalExit(t *testing.T) {
	target := &fakeTarget{pid: 41, group: 42, done: make(chan targetResult, 1)}
	target.done <- targetResult{code: 17}
	platform := &fakePlatform{target: target}
	spec := processcontrol.Spec{Executable: "fixture", Args: []string{"--task-fixture", "exit-nonzero"}}
	control := newBlockingControl(commandBytes(t, processcontrol.StartCommand(spec)))
	var status bytes.Buffer

	if code := processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard); code != 0 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 2 || statuses[0].Kind != "started" || statuses[0].PID != 41 || statuses[0].ProcessGroup != 42 {
		t.Fatalf("statuses = %#v", statuses)
	}
	if statuses[1].Kind != "exit" || statuses[1].ExitCode != 17 || statuses[1].ErrorCode != "" {
		t.Fatalf("exit status = %#v", statuses[1])
	}
	if platform.terminationCount() != 1 {
		t.Fatalf("terminations = %d, want 1", platform.terminationCount())
	}
	select {
	case <-control.closed:
	default:
		t.Fatal("control reader was not closed after target completion")
	}
}

func TestRunBatchStartsTargetsRoutesOutputAndReportsChildren(t *testing.T) {
	first := &fakeTarget{
		pid: 51, group: 151,
		done: make(chan targetResult, 1),
	}
	second := &fakeTarget{
		pid: 52, group: 152,
		done: make(chan targetResult, 1),
	}
	first.done <- targetResult{code: 3}
	second.done <- targetResult{code: 7}
	platform := &batchFakePlatform{
		targets: []*fakeTarget{first, second},
	}
	spec := processcontrol.Spec{Batch: []processcontrol.BatchItem{
		{
			ID: "first", Executable: "fixture",
			Args: []string{"first"}, Dir: ".",
			TimeoutMS: 1_000,
		},
		{
			ID: "second", Executable: "fixture",
			Args: []string{"second"}, Dir: ".",
			TimeoutMS: 1_000,
		},
	}}
	control := newBlockingControl(commandBytes(
		t,
		processcontrol.StartCommand(spec),
	))
	var status bytes.Buffer

	if code := processhost.Run(
		context.Background(),
		platform,
		control,
		&status,
		io.Discard,
		io.Discard,
	); code != 0 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 6 {
		t.Fatalf("statuses = %#v", statuses)
	}
	if got := statuses[0]; got.Kind != "started" ||
		got.PID != 51 ||
		!slices.Equal(got.TargetProcessGroups, []int{151, 152}) {
		t.Fatalf("started status = %#v", got)
	}
	outputs := make(map[string]string)
	for _, got := range statuses[1 : len(statuses)-1] {
		if got.Kind != "output" {
			t.Fatalf("output status = %#v", got)
		}
		outputs[got.Source+"/"+string(got.Stream)] = string(got.Data)
	}
	wantOutputs := map[string]string{
		"first/stdout":  "stdout-first",
		"first/stderr":  "stderr-first",
		"second/stdout": "stdout-second",
		"second/stderr": "stderr-second",
	}
	if !maps.Equal(outputs, wantOutputs) {
		t.Fatalf("outputs = %#v, want %#v", outputs, wantOutputs)
	}
	exit := statuses[len(statuses)-1]
	if exit.Kind != "exit" || exit.ErrorCode != "" ||
		!reflect.DeepEqual(exit.Children, []processcontrol.HostChildResult{
			{ID: "first", ExitCode: 3},
			{ID: "second", ExitCode: 7},
		}) {
		t.Fatalf("exit status = %#v", exit)
	}
	if platform.terminationCount(51) != 1 ||
		platform.terminationCount(52) != 1 {
		t.Fatalf("terminations = %#v", platform.terminations)
	}
}

func TestRunBatchTerminatesOnlyTimedOutTarget(t *testing.T) {
	timedOut := &fakeTarget{
		pid: 61, group: 161,
		done: make(chan targetResult, 1),
	}
	finished := &fakeTarget{
		pid: 62, group: 162,
		done: make(chan targetResult, 1),
	}
	finished.done <- targetResult{code: 0}
	platform := &batchFakePlatform{
		targets: []*fakeTarget{timedOut, finished},
	}
	spec := processcontrol.Spec{Batch: []processcontrol.BatchItem{
		{
			ID: "slow", Executable: "fixture",
			Dir: ".", TimeoutMS: 10,
		},
		{
			ID: "fast", Executable: "fixture",
			Dir: ".", TimeoutMS: 1_000,
		},
	}}
	control := newBlockingControl(commandBytes(
		t,
		processcontrol.StartCommand(spec),
	))
	var status bytes.Buffer

	if code := processhost.Run(
		context.Background(),
		platform,
		control,
		&status,
		io.Discard,
		io.Discard,
	); code != 0 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	statuses := decodeStatuses(t, status.Bytes())
	exit := statuses[len(statuses)-1]
	if !reflect.DeepEqual(exit.Children, []processcontrol.HostChildResult{
		{ID: "fast"},
		{ID: "slow", TimedOut: true},
	}) {
		t.Fatalf("children = %#v", exit.Children)
	}
	if platform.terminationCount(61) != 1 ||
		platform.terminationCount(62) != 1 {
		t.Fatalf("terminations = %#v", platform.terminations)
	}
}

func TestRunTerminatesTargetOnStopEOFAndContextCancellation(t *testing.T) {
	tests := []struct {
		name    string
		control func(*testing.T, processcontrol.Spec) io.Reader
		ctx     func() (context.Context, context.CancelFunc)
	}{
		{
			name: "stop",
			control: func(t *testing.T, spec processcontrol.Spec) io.Reader {
				return io.NopCloser(bytes.NewReader(commandBytes(t, processcontrol.StartCommand(spec), processcontrol.StopCommand())))
			},
			ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		{
			name: "eof",
			control: func(t *testing.T, spec processcontrol.Spec) io.Reader {
				return io.NopCloser(bytes.NewReader(commandBytes(t, processcontrol.StartCommand(spec))))
			},
			ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		{
			name: "context",
			control: func(t *testing.T, spec processcontrol.Spec) io.Reader {
				return newBlockingControl(commandBytes(t, processcontrol.StartCommand(spec)))
			},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
			platform := &fakePlatform{target: target}
			ctx, cancel := test.ctx()
			defer cancel()
			var status bytes.Buffer
			code := processhost.Run(ctx, platform, test.control(t, processcontrol.Spec{Executable: "fixture"}), &status, io.Discard, io.Discard)
			if code != 0 {
				t.Fatalf("code = %d, status = %q", code, status.String())
			}
			if platform.terminationCount() != 1 {
				t.Fatalf("terminations = %d, want 1", platform.terminationCount())
			}
			statuses := decodeStatuses(t, status.Bytes())
			if len(statuses) != 2 || statuses[1].Kind != "exit" {
				t.Fatalf("statuses = %#v", statuses)
			}
		})
	}
}

func TestRunRejectsInvalidInitialFramesFailClosed(t *testing.T) {
	oversizeSecret := strings.Repeat("SENSITIVE", 470_000)
	tests := []struct {
		name    string
		control string
	}{
		{name: "malformed", control: "{not-json}\n"},
		{name: "wrong kind", control: `{"kind":"stop"}` + "\n"},
		{name: "missing spec", control: `{"kind":"start"}` + "\n"},
		{name: "unknown member", control: `{"kind":"start","spec":{"Executable":"fixture"},"token":"secret"}` + "\n"},
		{name: "oversize", control: `{"kind":"start","spec":{"Executable":"fixture"},"unknown":"` + oversizeSecret + `"}` + "\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			platform := &fakePlatform{}
			var status, stderr bytes.Buffer
			if code := processhost.Run(context.Background(), platform, io.NopCloser(strings.NewReader(test.control)), &status, io.Discard, &stderr); code != 2 {
				t.Fatalf("code = %d, status = %q", code, status.String())
			}
			statuses := decodeStatuses(t, status.Bytes())
			if len(statuses) != 1 || statuses[0].Kind != "error" || statuses[0].ErrorCode != "INVALID_HOST_COMMAND" || statuses[0].Message != "invalid start command" {
				t.Fatalf("statuses = %#v", statuses)
			}
			if len(platform.started) != 0 {
				t.Fatal("platform start was called")
			}
			combined := status.String() + stderr.String()
			if strings.Contains(combined, "secret") || strings.Contains(combined, "SENSITIVE") || strings.Contains(combined, "fixture") {
				t.Fatalf("error reflected input: %q", combined)
			}
		})
	}
}

func TestRunClosesInheritedControlOnInvalidStart(t *testing.T) {
	control := newBlockingControl([]byte("{invalid}\n"))
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), &fakePlatform{}, control, &status, io.Discard, io.Discard); code != 2 {
		t.Fatalf("code = %d", code)
	}
	select {
	case <-control.closed:
	default:
		t.Fatal("control reader was not closed")
	}
}

func TestRunAcceptsExactLimitStartFrameWithCRLF(t *testing.T) {
	const frameLimit = 64 * 1024
	base, err := json.Marshal(processcontrol.StartCommand(processcontrol.Spec{}))
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", frameLimit-len(base))
	frame, err := json.Marshal(processcontrol.StartCommand(processcontrol.Spec{Executable: padding}))
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != frameLimit {
		t.Fatalf("frame length = %d, want %d", len(frame), frameLimit)
	}
	target := &fakeTarget{pid: 9, group: 10, done: make(chan targetResult, 1)}
	platform := &fakePlatform{target: target}
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(append(frame, '\r', '\n'))), &status, io.Discard, io.Discard); code != 0 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
}

func TestRunRedactsPlatformErrors(t *testing.T) {
	secret := `C:\private\token args=--secret`
	platform := &fakePlatform{startErr: errors.New(secret)}
	var status, stderr bytes.Buffer
	code := processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(commandBytes(t, processcontrol.StartCommand(processcontrol.Spec{Executable: secret})))), &status, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 1 || statuses[0].ErrorCode != "PROCESS_START_FAILED" || statuses[0].Message != "target process could not start" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if strings.Contains(status.String()+stderr.String(), secret) {
		t.Fatalf("error leaked platform details: status=%q stderr=%q", status.String(), stderr.String())
	}
}

func TestRunRejectsUnknownCommandAfterStartAndTerminatesTarget(t *testing.T) {
	target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
	platform := &fakePlatform{target: target}
	commands := commandBytes(t,
		processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
		processcontrol.HostCommand{Kind: "start", Spec: &processcontrol.Spec{Executable: `C:\private\second.exe`}},
	)
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(commands)), &status, io.Discard, io.Discard); code != 2 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 2 || statuses[0].Kind != "started" || statuses[1].Kind != "error" || statuses[1].ErrorCode != "INVALID_HOST_COMMAND" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if strings.Contains(status.String(), "private") {
		t.Fatalf("status leaked command: %q", status.String())
	}
	if platform.terminationCount() != 1 {
		t.Fatalf("terminations = %d, want 1", platform.terminationCount())
	}
}

func TestRunRejectsInvalidCommandWhenTargetCompletionWins(t *testing.T) {
	start := commandBytes(t, processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}))
	invalid := commandBytes(t, processcontrol.HostCommand{Kind: "unknown"})
	control := newStagedControl(start, invalid)
	target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
	target.done <- targetResult{code: 0}
	platform := &fakePlatform{target: target}
	platform.onTerminate = func() {
		close(control.releaseSecond)
		<-control.secondRead
	}
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard); code != 2 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 2 || statuses[1].Kind != "error" || statuses[1].ErrorCode != "INVALID_HOST_COMMAND" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestRunRejectsInvalidCommandWhenCommandWins(t *testing.T) {
	target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
	platform := &fakePlatform{target: target}
	commands := commandBytes(t,
		processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
		processcontrol.HostCommand{Kind: "unknown"},
	)
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(commands)), &status, io.Discard, io.Discard); code != 2 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
}

func TestRunRejectsFramesAfterStop(t *testing.T) {
	oversize := `{"kind":"unknown","padding":"` + strings.Repeat("x", 64*1024) + `"}` + "\n"
	tests := []struct {
		name     string
		trailing []byte
	}{
		{name: "duplicate stop", trailing: commandBytes(t, processcontrol.StopCommand())},
		{name: "unknown", trailing: commandBytes(t, processcontrol.HostCommand{Kind: "unknown"})},
		{name: "malformed", trailing: []byte("{malformed}\n")},
		{name: "oversize", trailing: []byte(oversize)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
			platform := &fakePlatform{target: target}
			control := commandBytes(t,
				processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
				processcontrol.StopCommand(),
			)
			control = append(control, test.trailing...)
			var status bytes.Buffer
			if code := processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(control)), &status, io.Discard, io.Discard); code != 2 {
				t.Fatalf("code = %d, status = %q", code, status.String())
			}
			statuses := decodeStatuses(t, status.Bytes())
			if len(statuses) != 2 || statuses[0].Kind != "started" || statuses[1].Kind != "error" || statuses[1].ErrorCode != "INVALID_HOST_COMMAND" {
				t.Fatalf("statuses = %#v", statuses)
			}
			if platform.terminationCount() != 1 {
				t.Fatalf("terminations = %d, want 1", platform.terminationCount())
			}
		})
	}
}

func TestRunRejectsUnterminatedFragmentAfterStopOnOwnedClose(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
	}{
		{name: "complete unknown JSON without newline", fragment: `{"kind":"unknown"}`},
		{name: "malformed partial JSON", fragment: `{"kind":`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
			platform := &fakePlatform{target: target}
			data := commandBytes(t,
				processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
				processcontrol.StopCommand(),
			)
			control := newBlockingControl(append(data, test.fragment...))
			var status bytes.Buffer
			if code := processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard); code != 2 {
				t.Fatalf("code = %d, status = %q", code, status.String())
			}
			statuses := decodeStatuses(t, status.Bytes())
			if len(statuses) != 2 || statuses[0].Kind != "started" || statuses[1].Kind != "error" || statuses[1].ErrorCode != "INVALID_HOST_COMMAND" {
				t.Fatalf("statuses = %#v", statuses)
			}
			if platform.terminationCount() != 1 {
				t.Fatalf("terminations = %d, want 1", platform.terminationCount())
			}
		})
	}
}

func TestRunRejectsBlockingNonCloserBeforeReadOrPlatformStart(t *testing.T) {
	control := newGatedNonCloser()
	platform := &fakePlatform{}
	var status bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard)
	}()

	select {
	case code := <-done:
		if code != 2 {
			t.Fatalf("code = %d, status = %q", code, status.String())
		}
	case <-control.readCalled:
		close(control.release)
		<-done
		t.Fatal("Run read from a non-closable control")
	case <-time.After(2 * time.Second):
		close(control.release)
		<-done
		t.Fatal("Run blocked on a non-closable control")
	}

	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 1 || statuses[0].Kind != "error" || statuses[0].ErrorCode != "INVALID_HOST_CONTROL" || statuses[0].Message != "invalid host control" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if len(platform.started) != 0 {
		t.Fatal("platform start was called")
	}
}

func TestRunClosesControlExactlyOnce(t *testing.T) {
	target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
	platform := &fakePlatform{target: target}
	control := &strictCloseControl{reader: bytes.NewReader(commandBytes(t,
		processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
		processcontrol.StopCommand(),
	))}
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard); code != 0 {
		t.Fatalf("code = %d, status = %q", code, status.String())
	}
	if count := control.closeCount(); count != 1 {
		t.Fatalf("control close count = %d, want 1", count)
	}
}

func TestRunTerminateErrorStillReleasesWaitAndReturnsRedactedFailure(t *testing.T) {
	target := &fakeTarget{pid: 5, group: 6, done: make(chan targetResult, 1)}
	platform := &fakePlatform{target: target, terminateErr: errors.New(`C:\private\terminate failure`)}
	control := commandBytes(t,
		processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"}),
		processcontrol.StopCommand(),
	)
	var status bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- processhost.Run(context.Background(), platform, io.NopCloser(bytes.NewReader(control)), &status, io.Discard, io.Discard)
	}()
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("code = %d, status = %q", code, status.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host did not return after Terminate released Wait with an error")
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 2 || statuses[1].Kind != "exit" || statuses[1].ErrorCode != "PROCESS_WAIT_FAILED" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if strings.Contains(status.String(), "private") {
		t.Fatalf("status leaked terminate error: %q", status.String())
	}
}

func TestRunReportsRedactedWaitFailure(t *testing.T) {
	target := &fakeTarget{pid: 7, group: 8, done: make(chan targetResult, 1)}
	target.done <- targetResult{code: 9, err: errors.New(`C:\private\failure`)}
	platform := &fakePlatform{target: target}
	control := newBlockingControl(commandBytes(t, processcontrol.StartCommand(processcontrol.Spec{Executable: "fixture"})))
	var status bytes.Buffer
	if code := processhost.Run(context.Background(), platform, control, &status, io.Discard, io.Discard); code != 1 {
		t.Fatalf("code = %d", code)
	}
	statuses := decodeStatuses(t, status.Bytes())
	if len(statuses) != 2 || statuses[1].Kind != "exit" || statuses[1].ExitCode != 9 || statuses[1].ErrorCode != "PROCESS_WAIT_FAILED" || statuses[1].Message != "" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if strings.Contains(status.String(), "private") {
		t.Fatalf("status leaked wait error: %q", status.String())
	}
}

func TestHostCommandConstructorsProduceClosedProtocolShapes(t *testing.T) {
	spec := processcontrol.Spec{Executable: "fixture"}
	start := processcontrol.StartCommand(spec)
	if start.Kind != "start" || start.Spec == nil || start.Spec.Executable != "fixture" {
		t.Fatalf("start = %#v", start)
	}
	stop := processcontrol.StopCommand()
	if stop.Kind != "stop" || stop.Spec != nil {
		t.Fatalf("stop = %#v", stop)
	}
}
