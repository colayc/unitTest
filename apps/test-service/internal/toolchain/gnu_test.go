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

func TestParseCompilerVersionAcceptsUbuntuGCCAndGXXBanners(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		banner string
		want   string
	}{
		"gcc": {
			banner: "gcc (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0",
			want:   "13.3.0",
		},
		"g++": {
			banner: "g++ (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0",
			want:   "13.3.0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseCompilerVersion(FamilyGCC, []byte(test.banner+"\n"))
			if err != nil || got != test.want {
				t.Fatalf("parseCompilerVersion() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestGCCProbeAcceptsDefaultEmptySysroot(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	runner.outputs[probeKey(fixture.gcc, "--print-sysroot")] = successfulOutput(" \r\n\t")
	runner.outputs[probeKey(fixture.gxx, "--print-sysroot")] = successfulOutput("\n")
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
	if instance.Sysroot != "" {
		t.Fatalf("Probe() Sysroot = %q, want empty default sysroot", instance.Sysroot)
	}
	if instance.ID == "" {
		t.Fatal("Probe() automatic ID is empty")
	}
}

func TestGCCProbeRejectsDefaultAndExplicitSysrootPair(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	runner.outputs[probeKey(fixture.gcc, "--print-sysroot")] = successfulOutput("\n")
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
	if !errors.Is(err, ErrInvalidToolchain) || !strings.Contains(err.Error(), "SDK identity") {
		t.Fatalf("Probe() error = %v, want default/explicit SDK mismatch", err)
	}
}

func TestGCCAutomaticIDDistinguishesDefaultAndExplicitSysroot(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	defaultRunner := newGNUFakeRunner(t, fixture)
	defaultRunner.outputs[probeKey(fixture.gcc, "--print-sysroot")] = successfulOutput("\n")
	defaultRunner.outputs[probeKey(fixture.gxx, "--print-sysroot")] = successfulOutput("\n")
	explicitRunner := newGNUFakeRunner(t, fixture)
	defaultAdapter, _ := newGNUAdapter(defaultRunner, FamilyGCC, nil, "x64")
	explicitAdapter, _ := newGNUAdapter(explicitRunner, FamilyGCC, nil, "x64")
	candidate := Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	}

	defaultInstance, err := defaultAdapter.Probe(context.Background(), candidate)
	if err != nil {
		t.Fatalf("default Probe() error = %v", err)
	}
	explicitInstance, err := explicitAdapter.Probe(context.Background(), candidate)
	if err != nil {
		t.Fatalf("explicit Probe() error = %v", err)
	}
	if defaultInstance.ID == explicitInstance.ID {
		t.Fatalf("default and explicit sysroot share automatic ID %q", defaultInstance.ID)
	}
}

func TestAutomaticToolchainIDFitsProtocolToolchainIDBound(t *testing.T) {
	got, err := automaticToolchainID(Instance{Family: FamilyClangCL, Version: "22.1.8", TargetTriple: "x86_64-pc-windows-msvc", HostArchitecture: "x64", TargetArchitecture: "x64"}, "c", "cxx", "sdk")
	if err != nil {
		t.Fatalf("automaticToolchainID() error = %v", err)
	}
	if len(got) != 64 || !strings.HasPrefix(got, "clang-cl-") {
		t.Fatalf("automaticToolchainID() = %q, want a 64-character clang-cl identity", got)
	}
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

func TestGNUProbeErrorDoesNotExposeInvalidExecutablePath(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}
	sensitivePath := filepath.Join(t.TempDir(), "customer-secret-TOKEN=abcdef-gcc")

	_, err = adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   sensitivePath,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, ErrInvalidToolchain) {
		t.Fatalf("Probe() error = %v, want ErrInvalidToolchain", err)
	}
	for _, secret := range []string{sensitivePath, filepath.Dir(sensitivePath), "customer-secret", "TOKEN=", "abcdef"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Probe() error %q exposes %q", err, secret)
		}
	}
	if got, want := err.Error(), "invalid toolchain: C compiler executable is invalid"; got != want {
		t.Fatalf("Probe() error = %q, want stable %q", got, want)
	}
}

func TestGNUProbeCancellationDuringSnapshotVerificationPropagatesContext(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	runner.afterCall = func(call probeCall) {
		if call == (probeCall{fixture.gcc, "--version"}) {
			cancel()
		}
	}
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	_, err = adapter.Probe(ctx, Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want context.Canceled", err)
	}
}

func TestGNUProbePreservesCCompilerInitialSnapshotHashCancellation(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}
	ctx := &errOnContextCheck{
		Context: context.Background(),
		trigger: 4,
		err:     context.Canceled,
	}

	_, err = adapter.Probe(ctx, Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want C snapshot context.Canceled", err)
	}
}

func TestGNUProbePreservesCXXCompilerInitialSnapshotHashCancellation(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}
	ctx := &errOnContextCheck{
		Context: context.Background(),
		trigger: 14,
		err:     context.Canceled,
	}

	_, err = adapter.Probe(ctx, Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want C++ snapshot context.Canceled", err)
	}
}

func TestGNUProbePreservesInitialSnapshotHashDeadline(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}
	ctx := &errOnContextCheck{
		Context: context.Background(),
		trigger: 4,
		err:     context.DeadlineExceeded,
	}

	_, err = adapter.Probe(ctx, Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe() error = %v, want snapshot context.DeadlineExceeded", err)
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
	if !errors.Is(err, ErrInvalidToolchain) ||
		err.Error() != "invalid toolchain: probe executable verification failed" {
		t.Fatalf("Probe() error = %v, want stable executable verification rejection", err)
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
	if !errors.Is(err, ErrInvalidToolchain) ||
		err.Error() != "invalid toolchain: probe executable verification failed" {
		t.Fatalf("Probe() error = %v, want stable executable tail verification rejection", err)
	}
}

func TestExecutableSnapshotAcceptsExactLimitAndRejectsLimitPlusOne(t *testing.T) {
	t.Parallel()

	const limit = int64(4096)
	exact := filepath.Join(t.TempDir(), "exact-compiler")
	if err := os.WriteFile(exact, make([]byte, limit), 0o755); err != nil {
		t.Fatalf("WriteFile(exact): %v", err)
	}
	snapshot, err := openExecutableSnapshotWithLimit(context.Background(), exact, limit)
	if err != nil {
		t.Fatalf("openExecutableSnapshotWithLimit(exact) error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close(exact): %v", err)
	}

	excess := filepath.Join(t.TempDir(), "excess-compiler")
	if err := os.WriteFile(excess, make([]byte, limit+1), 0o755); err != nil {
		t.Fatalf("WriteFile(excess): %v", err)
	}
	if _, err := openExecutableSnapshotWithLimit(context.Background(), excess, limit); !errors.Is(err, errExecutableTooLarge) {
		t.Fatalf("openExecutableSnapshotWithLimit(limit+1) error = %v, want errExecutableTooLarge", err)
	}
}

func TestExecutableSnapshotHonorsCancellationDuringOpenAndVerify(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "compiler")
	if err := os.WriteFile(path, []byte("compiler"), 0o755); err != nil {
		t.Fatalf("WriteFile(compiler): %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openExecutableSnapshotWithLimit(cancelled, path, 4096); !errors.Is(err, context.Canceled) {
		t.Fatalf("openExecutableSnapshotWithLimit(cancelled) error = %v, want context.Canceled", err)
	}

	snapshot, err := openExecutableSnapshotWithLimit(context.Background(), path, 4096)
	if err != nil {
		t.Fatalf("openExecutableSnapshotWithLimit() error = %v", err)
	}
	defer snapshot.Close()
	if err := snapshot.Verify(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestExecutableDigestStopsBetweenChunksAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstChunkReader{cancel: cancel}
	_, _, err := digestBounded(ctx, reader, 4096)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("digestBounded() error = %v, want context.Canceled", err)
	}
	if reader.reads != 1 {
		t.Fatalf("digestBounded() reads = %d, want stop after first chunk", reader.reads)
	}
}

func TestExecutableSnapshotRejectsGrowthBetweenHandleCheckAndDigest(t *testing.T) {
	t.Parallel()

	const limit = int64(4096)
	path := filepath.Join(t.TempDir(), "growing-compiler")
	if err := os.WriteFile(path, make([]byte, limit), 0o755); err != nil {
		t.Fatalf("WriteFile(compiler): %v", err)
	}
	grow := func() {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile(compiler): %v", err)
		}
		if _, err := file.Write([]byte{1}); err != nil {
			_ = file.Close()
			t.Fatalf("append compiler: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(compiler): %v", err)
		}
	}

	if _, err := openExecutableSnapshotWithLimitAndHook(
		context.Background(),
		path,
		limit,
		grow,
	); !errors.Is(err, errExecutableTooLarge) {
		t.Fatalf("openExecutableSnapshotWithLimitAndHook() error = %v, want errExecutableTooLarge", err)
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
	for _, executable := range []string{fixture.gcc, fixture.gcc2} {
		outputs[probeKey(executable, "--version")] = successfulOutput("gcc (Ubuntu 13.2.0-1ubuntu1) 13.2.0\n")
		outputs[probeKey(executable, "-dumpmachine")] = successfulOutput("x86_64-linux-gnu\n")
		outputs[probeKey(executable, "--print-sysroot")] = successfulOutput(fixture.sysroot + "\n")
	}
	for _, executable := range []string{fixture.gxx, fixture.gxx2} {
		outputs[probeKey(executable, "--version")] = successfulOutput("g++ (Ubuntu 13.2.0-1ubuntu1) 13.2.0\n")
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

type cancelAfterFirstChunkReader struct {
	cancel context.CancelFunc
	reads  int
}

type errOnContextCheck struct {
	context.Context
	trigger int
	calls   int
	err     error
}

func (ctx *errOnContextCheck) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.trigger {
		return ctx.err
	}
	return nil
}

func (reader *cancelAfterFirstChunkReader) Read(buffer []byte) (int, error) {
	reader.reads++
	if reader.reads == 1 {
		for index := range min(len(buffer), 1024) {
			buffer[index] = byte(index)
		}
		reader.cancel()
		return min(len(buffer), 1024), nil
	}
	return 0, errors.New("digest read after cancellation")
}
