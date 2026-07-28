package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestGCCProbeUsesFixedArgumentsAndBuildsDescriptor(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	instance, err := adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if instance.ID == "" {
		t.Fatal("Probe() ID is empty")
	}
	instance.ID = "<stable>"
	want := Instance{
		ID:                 "<stable>",
		Family:             FamilyGCC,
		CCompiler:          fixture.gcc,
		CXXCompiler:        fixture.gxx,
		Version:            "13.2.0",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            fixture.sysroot,
		Environment:        []string{},
		Generators:         []string{"Ninja"},
	}
	if !reflect.DeepEqual(instance, want) {
		t.Fatalf("Probe() = %+v, want %+v", instance, want)
	}
	runner.assertCalls(
		probeCall{fixture.gcc, "--version"},
		probeCall{fixture.gcc, "-dumpmachine"},
		probeCall{fixture.gcc, "--print-sysroot"},
		probeCall{fixture.gxx, "--version"},
		probeCall{fixture.gxx, "-dumpmachine"},
		probeCall{fixture.gxx, "--print-sysroot"},
		probeCall{fixture.ninja, "--version"},
	)
}

func TestClangProbeUsesFixedArgumentsAndResourceIdentity(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyClang, nil, "arm64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	instance, err := adapter.Probe(context.Background(), Candidate{
		Family:      FamilyClang,
		CCompiler:   fixture.clang,
		CXXCompiler: fixture.clangxx,
		Make:        fixture.make,
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if instance.Version != "18.1.3" || instance.TargetTriple != "aarch64-linux-gnu" ||
		instance.HostArchitecture != "arm64" || instance.TargetArchitecture != "arm64" ||
		instance.Sysroot != fixture.resource {
		t.Fatalf("Probe() descriptor = %+v", instance)
	}
	if !reflect.DeepEqual(instance.Generators, []string{"Unix Makefiles"}) {
		t.Fatalf("Probe() Generators = %v, want Make fallback", instance.Generators)
	}
	runner.assertCalls(
		probeCall{fixture.clang, "--version"},
		probeCall{fixture.clang, "--print-target-triple"},
		probeCall{fixture.clang, "--print-resource-dir"},
		probeCall{fixture.clangxx, "--version"},
		probeCall{fixture.clangxx, "--print-target-triple"},
		probeCall{fixture.clangxx, "--print-resource-dir"},
		probeCall{fixture.make, "--version"},
	)
}

func TestGNUProbeRejectsMismatchedCompilerPairs(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	runner.outputs[probeKey(fixture.gxx, "-dumpmachine")] = successfulOutput("aarch64-linux-gnu\n")
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	_, err = adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, ErrInvalidToolchain) || !strings.Contains(err.Error(), "target triple") {
		t.Fatalf("Probe() error = %v, want target triple mismatch", err)
	}
}

func TestGNUProbeRejectsWrongFamilyAndMalformedProbeResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*gnuFixture, *gnuFakeRunner)
	}{
		{
			name: "arbitrary gcc banner",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.outputs[probeKey(f.gcc, "--version")] = successfulOutput("Acme compiler 13.2.0\n")
			},
		},
		{
			name: "empty target",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.outputs[probeKey(f.gcc, "-dumpmachine")] = successfulOutput("\n")
			},
		},
		{
			name: "nonzero result",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.outputs[probeKey(f.gcc, "--version")] = probe.Result{ExitCode: 2, Stderr: []byte("failure")}
			},
		},
		{
			name: "oversized output",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.outputs[probeKey(f.gcc, "--version")] = successfulOutput(strings.Repeat("x", maxGNUProbeOutput+1))
			},
		},
		{
			name: "invalid utf8",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.outputs[probeKey(f.gcc, "--version")] = successfulOutput(string([]byte{0xff, 0xfe}))
			},
		},
		{
			name: "runner error",
			change: func(f *gnuFixture, r *gnuFakeRunner) {
				r.errors[probeKey(f.gcc, "--version")] = errors.New("runner failed")
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newGNUFixture(t)
			runner := newGNUFakeRunner(t, fixture)
			test.change(fixture, runner)
			adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
			if err != nil {
				t.Fatalf("newGNUAdapter() error = %v", err)
			}
			_, err = adapter.Probe(context.Background(), Candidate{
				Family:      FamilyGCC,
				CCompiler:   fixture.gcc,
				CXXCompiler: fixture.gxx,
				Ninja:       fixture.ninja,
			})
			if !errors.Is(err, ErrInvalidToolchain) {
				t.Fatalf("Probe() error = %v, want ErrInvalidToolchain", err)
			}
		})
	}
}

func TestGNUDiscoverPrefersManualIDAndDeduplicatesEquivalentCandidates(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, []Candidate{
		{
			Family:      FamilyGCC,
			CCompiler:   fixture.gcc,
			CXXCompiler: fixture.gxx,
			Ninja:       fixture.ninja,
		},
		{
			ID:          "manual-linux-gcc",
			Family:      FamilyGCC,
			CCompiler:   fixture.gcc,
			CXXCompiler: fixture.gxx,
			Manual:      true,
			Ninja:       fixture.ninja,
		},
	}, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	instances, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got := instanceIDs(instances); !reflect.DeepEqual(got, []string{"manual-linux-gcc"}) {
		t.Fatalf("Discover() ids = %v, want manual ID", got)
	}
}

func TestGNUDiscoverKeepsSuccessesAndReturnsCandidateIssues(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, []Candidate{
		{
			Family:      FamilyGCC,
			CCompiler:   fixture.gcc,
			CXXCompiler: fixture.gxx,
		},
		{
			Family:      FamilyGCC,
			CCompiler:   fixture.gcc2,
			CXXCompiler: fixture.gxx2,
			Ninja:       fixture.ninja,
		},
	}, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	instances, discoverErr := adapter.Discover(context.Background())
	if len(instances) != 1 {
		t.Fatalf("Discover() instances = %+v, want one successful candidate", instances)
	}
	var carrier issueCarrier
	if !asIssueCarrier(discoverErr, &carrier) {
		t.Fatalf("Discover() error = %v, want issue carrier", discoverErr)
	}
	want := []Issue{{
		Code:     "BUILD_TOOL_NOT_FOUND",
		Message:  "gcc candidate has no verified Ninja or Make build tool",
		Blocking: false,
	}}
	if got := carrier.ToolchainIssues(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolchainIssues() = %+v, want %+v", got, want)
	}
}

func TestGNUAutomaticIDsAreStableAcrossCandidateOrder(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	firstRunner := newGNUFakeRunner(t, fixture)
	secondRunner := newGNUFakeRunner(t, fixture)
	firstCandidates := []Candidate{
		{Family: FamilyGCC, CCompiler: fixture.gcc, CXXCompiler: fixture.gxx, Ninja: fixture.ninja},
		{Family: FamilyGCC, CCompiler: fixture.gcc2, CXXCompiler: fixture.gxx2, Ninja: fixture.ninja},
	}
	secondCandidates := []Candidate{firstCandidates[1], firstCandidates[0]}
	first, _ := newGNUAdapter(firstRunner, FamilyGCC, firstCandidates, "x64")
	second, _ := newGNUAdapter(secondRunner, FamilyGCC, secondCandidates, "x64")

	left, leftErr := first.Discover(context.Background())
	right, rightErr := second.Discover(context.Background())
	if leftErr != nil || rightErr != nil {
		t.Fatalf("Discover() errors = (%v, %v)", leftErr, rightErr)
	}
	if !reflect.DeepEqual(instanceIDs(left), instanceIDs(right)) {
		t.Fatalf("candidate order changed IDs: %v != %v", instanceIDs(left), instanceIDs(right))
	}
}

func TestGNUProbeRejectsExecutableMutation(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	runner.afterCall = func(call probeCall) {
		if call == (probeCall{fixture.gcc, "--version"}) {
			if err := os.WriteFile(fixture.gcc, []byte("mutated"), 0o755); err != nil {
				t.Fatalf("mutate compiler: %v", err)
			}
		}
	}
	adapter, _ := newGNUAdapter(runner, FamilyGCC, nil, "x64")

	_, err := adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, ErrInvalidToolchain) || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("Probe() error = %v, want executable change rejection", err)
	}
}

func TestGNUProbeRejectsExecutableTailMutation(t *testing.T) {
	fixture := newGNUFixture(t)
	const tailOffset = int64(17 * 1024 * 1024)
	if err := os.Truncate(fixture.gcc, tailOffset+1); err != nil {
		t.Fatalf("Truncate(gcc): %v", err)
	}
	runner := newGNUFakeRunner(t, fixture)
	runner.afterCall = func(call probeCall) {
		if call != (probeCall{fixture.gcc, "--version"}) {
			return
		}
		file, err := os.OpenFile(fixture.gcc, os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile(gcc): %v", err)
		}
		if _, err := file.WriteAt([]byte{1}, tailOffset); err != nil {
			_ = file.Close()
			t.Fatalf("WriteAt(gcc tail): %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(gcc): %v", err)
		}
	}
	adapter, _ := newGNUAdapter(runner, FamilyGCC, nil, "x64")

	_, err := adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, ErrInvalidToolchain) || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("Probe() error = %v, want executable tail change rejection", err)
	}
}

type gnuFixture struct {
	root     string
	gcc      string
	gxx      string
	gcc2     string
	gxx2     string
	clang    string
	clangxx  string
	ninja    string
	make     string
	sysroot  string
	resource string
}

func newGNUFixture(t *testing.T) *gnuFixture {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("Mkdir(bin): %v", err)
	}
	fixture := &gnuFixture{
		root:     root,
		gcc:      filepath.Join(bin, executableName("gcc")),
		gxx:      filepath.Join(bin, executableName("g++")),
		gcc2:     filepath.Join(bin, executableName("gcc-13")),
		gxx2:     filepath.Join(bin, executableName("g++-13")),
		clang:    filepath.Join(bin, executableName("clang")),
		clangxx:  filepath.Join(bin, executableName("clang++")),
		ninja:    filepath.Join(bin, executableName("ninja")),
		make:     filepath.Join(bin, executableName("make")),
		sysroot:  filepath.Join(root, "sysroot"),
		resource: filepath.Join(root, "lib", "clang", "18"),
	}
	for _, directory := range []string{fixture.sysroot, fixture.resource} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", directory, err)
		}
	}
	for _, executable := range []string{
		fixture.gcc, fixture.gxx, fixture.gcc2, fixture.gxx2,
		fixture.clang, fixture.clangxx, fixture.ninja, fixture.make,
	} {
		if err := os.WriteFile(executable, []byte(filepath.Base(executable)), 0o755); err != nil {
			t.Fatalf("WriteFile(%q): %v", executable, err)
		}
	}
	return fixture
}

func executableName(name string) string {
	return name
}

type probeCall struct {
	executable string
	argument   string
}

type gnuFakeRunner struct {
	t         *testing.T
	mu        sync.Mutex
	outputs   map[string]probe.Result
	errors    map[string]error
	calls     []probeCall
	afterCall func(probeCall)
}

func newGNUFakeRunner(t *testing.T, fixture *gnuFixture) *gnuFakeRunner {
	t.Helper()
	outputs := map[string]probe.Result{}
	for _, executable := range []string{fixture.gcc, fixture.gxx, fixture.gcc2, fixture.gxx2} {
		outputs[probeKey(executable, "--version")] = successfulOutput("gcc (GCC) 13.2.0\nCopyright (C) Free Software Foundation\n")
		outputs[probeKey(executable, "-dumpmachine")] = successfulOutput("x86_64-linux-gnu\n")
		outputs[probeKey(executable, "--print-sysroot")] = successfulOutput(fixture.sysroot + "\n")
	}
	for _, executable := range []string{fixture.clang, fixture.clangxx} {
		outputs[probeKey(executable, "--version")] = successfulOutput("Ubuntu clang version 18.1.3 (build)\n")
		outputs[probeKey(executable, "--print-target-triple")] = successfulOutput("aarch64-linux-gnu\n")
		outputs[probeKey(executable, "--print-resource-dir")] = successfulOutput(fixture.resource + "\n")
	}
	outputs[probeKey(fixture.ninja, "--version")] = successfulOutput("1.12.1\n")
	outputs[probeKey(fixture.make, "--version")] = successfulOutput("GNU Make 4.4.1\n")
	return &gnuFakeRunner{
		t:       t,
		outputs: outputs,
		errors:  make(map[string]error),
	}
}

func (runner *gnuFakeRunner) Run(ctx context.Context, spec probe.Spec) (probe.Result, error) {
	runner.t.Helper()
	if err := ctx.Err(); err != nil {
		return probe.Result{}, err
	}
	if !filepath.IsAbs(spec.Executable) {
		runner.t.Fatalf("probe executable %q is not absolute", spec.Executable)
	}
	if len(spec.Args) != 1 {
		runner.t.Fatalf("probe args = %v, want exactly one fixed argument", spec.Args)
	}
	if spec.Env == nil || len(spec.Env) != 0 {
		runner.t.Fatalf("probe Env = %v, want explicit empty environment", spec.Env)
	}
	if spec.Dir != "" {
		runner.t.Fatalf("probe Dir = %q, want empty", spec.Dir)
	}
	if spec.Timeout != gnuProbeTimeout || spec.MaxOutput != maxGNUProbeOutput {
		runner.t.Fatalf("probe bounds = (%v, %d), want (%v, %d)",
			spec.Timeout, spec.MaxOutput, gnuProbeTimeout, maxGNUProbeOutput)
	}
	call := probeCall{spec.Executable, spec.Args[0]}
	key := probeKey(call.executable, call.argument)
	runner.mu.Lock()
	runner.calls = append(runner.calls, call)
	result, present := runner.outputs[key]
	err := runner.errors[key]
	after := runner.afterCall
	runner.mu.Unlock()
	if after != nil {
		after(call)
	}
	if err != nil {
		return probe.Result{}, err
	}
	if !present {
		return probe.Result{}, fmt.Errorf("unexpected probe %s %s", spec.Executable, spec.Args[0])
	}
	return result, nil
}

func (runner *gnuFakeRunner) assertCalls(want ...probeCall) {
	runner.t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !reflect.DeepEqual(runner.calls, want) {
		runner.t.Fatalf("probe calls = %+v, want %+v", runner.calls, want)
	}
}

func probeKey(executable, argument string) string {
	return executable + "\x00" + argument
}

func successfulOutput(stdout string) probe.Result {
	return probe.Result{ExitCode: 0, Stdout: []byte(stdout)}
}
