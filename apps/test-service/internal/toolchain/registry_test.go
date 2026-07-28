package toolchain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRegistrySortsDeduplicatesAndDefensivelyCopiesResults(t *testing.T) {
	t.Parallel()

	sharedEnvironment := []string{"LANG=C"}
	sharedGenerators := []string{"Ninja"}
	sharedCoverage := CoverageCapability{GCov: "/usr/bin/gcov"}
	gcc := Instance{
		ID:                 "gcc-id",
		Family:             FamilyGCC,
		CCompiler:          "/usr/bin/gcc",
		CXXCompiler:        "/usr/bin/g++",
		Version:            "13.2.0",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            "/",
		Environment:        sharedEnvironment,
		Generators:         sharedGenerators,
		Coverage:           sharedCoverage,
	}
	clang := Instance{
		ID:                 "clang-id",
		Family:             FamilyClang,
		CCompiler:          "/usr/bin/clang",
		CXXCompiler:        "/usr/bin/clang++",
		Version:            "18.1.3",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            "/usr/lib/llvm-18/lib/clang/18",
		Generators:         []string{"Unix Makefiles"},
	}

	first := &staticAdapter{instances: []Instance{gcc, clang}}
	second := &staticAdapter{instances: []Instance{gcc}}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	instances, issues := registry.Discover(context.Background())
	if len(issues) != 0 {
		t.Fatalf("Discover() issues = %+v, want none", issues)
	}
	if got := instanceIDs(instances); !reflect.DeepEqual(got, []string{"clang-id", "gcc-id"}) {
		t.Fatalf("Discover() ids = %v, want [clang-id gcc-id]", got)
	}

	instances[1].Environment[0] = "CALLER=mutated"
	instances[1].Generators[0] = "Caller Mutated"
	if got := first.instances[0].Environment; !reflect.DeepEqual(got, []string{"LANG=C"}) {
		t.Fatalf("adapter Environment = %v after caller mutation, want isolated storage", got)
	}
	if got := first.instances[0].Generators; !reflect.DeepEqual(got, []string{"Ninja"}) {
		t.Fatalf("adapter Generators = %v after caller mutation, want isolated storage", got)
	}

	again, againIssues := registry.Discover(context.Background())
	if len(againIssues) != 0 {
		t.Fatalf("second Discover() issues = %+v, want none", againIssues)
	}
	if got := again[1].Environment; !reflect.DeepEqual(got, []string{"LANG=C"}) {
		t.Fatalf("second Discover() Environment = %v, want defensive copy", got)
	}
	if got := again[1].Generators; !reflect.DeepEqual(got, []string{"Ninja"}) {
		t.Fatalf("second Discover() Generators = %v, want defensive copy", got)
	}
	if again[1].Coverage != sharedCoverage {
		t.Fatalf("second Discover() Coverage = %+v, want %+v", again[1].Coverage, sharedCoverage)
	}
}

func TestRegistryKeepsSuccessesAndReportsStableAdapterFailures(t *testing.T) {
	t.Parallel()

	success := Instance{
		ID:                 "gcc-id",
		Family:             FamilyGCC,
		CCompiler:          "/tools/gcc",
		CXXCompiler:        "/tools/g++",
		Version:            "13.2.0",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            "/",
		Generators:         []string{"Ninja"},
	}
	registry, err := NewRegistry(
		&staticAdapter{err: errors.New("contains /secret/path and TOKEN=value")},
		&staticAdapter{instances: []Instance{success}},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	instances, issues := registry.Discover(context.Background())
	if got := instanceIDs(instances); !reflect.DeepEqual(got, []string{"gcc-id"}) {
		t.Fatalf("Discover() ids = %v, want [gcc-id]", got)
	}
	wantIssues := []Issue{{
		Code:     "TOOLCHAIN_DISCOVERY_FAILED",
		Message:  "toolchain adapter 0 failed",
		Blocking: false,
	}}
	if !reflect.DeepEqual(issues, wantIssues) {
		t.Fatalf("Discover() issues = %+v, want %+v", issues, wantIssues)
	}
}

func TestRegistryReportsConflictingStableIDs(t *testing.T) {
	t.Parallel()

	base := Instance{
		ID:                 "same-id",
		Family:             FamilyGCC,
		CCompiler:          "/tools/gcc",
		CXXCompiler:        "/tools/g++",
		Version:            "13.2.0",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            "/",
		Generators:         []string{"Ninja"},
	}
	conflict := base
	conflict.Version = "14.1.0"

	registry, err := NewRegistry(
		&staticAdapter{instances: []Instance{base}},
		&staticAdapter{instances: []Instance{conflict}},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	instances, issues := registry.Discover(context.Background())
	if len(instances) != 1 || instances[0].Version != "13.2.0" {
		t.Fatalf("Discover() instances = %+v, want deterministic first descriptor", instances)
	}
	want := []Issue{{
		Code:     "TOOLCHAIN_ID_CONFLICT",
		Message:  `toolchain id "same-id" has conflicting descriptors`,
		Blocking: true,
	}}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("Discover() issues = %+v, want %+v", issues, want)
	}
}

func TestRegistryCancellationReturnsPromptlyWithoutOrdinaryIssue(t *testing.T) {
	t.Parallel()

	blocked := &staticAdapter{discover: func(ctx context.Context) ([]Instance, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	registry, err := NewRegistry(blocked)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	instances, issues := registry.Discover(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Discover() took %v after cancellation", elapsed)
	}
	if instances != nil || issues != nil {
		t.Fatalf("Discover() = (%+v, %+v), want (nil, nil) for cancellation", instances, issues)
	}
}

func TestNewRegistryRejectsNilDuplicateAndExcessAdapters(t *testing.T) {
	t.Parallel()

	var typedNil *staticAdapter
	if _, err := NewRegistry(typedNil); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("NewRegistry(typed nil) error = %v, want ErrInvalidRegistry", err)
	}
	adapter := &staticAdapter{}
	if _, err := NewRegistry(adapter, adapter); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("NewRegistry(duplicate) error = %v, want ErrInvalidRegistry", err)
	}
	if _, err := NewRegistry(valueAdapter{}, valueAdapter{}); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("NewRegistry(duplicate values) error = %v, want ErrInvalidRegistry", err)
	}
	tooMany := make([]Adapter, maxRegistryAdapters+1)
	for index := range tooMany {
		tooMany[index] = &staticAdapter{}
	}
	if _, err := NewRegistry(tooMany...); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("NewRegistry(excess) error = %v, want ErrInvalidRegistry", err)
	}
}

type valueAdapter struct{}

func (valueAdapter) Discover(context.Context) ([]Instance, error) {
	return nil, nil
}

func (valueAdapter) Probe(context.Context, Candidate) (Instance, error) {
	return Instance{}, nil
}

type staticAdapter struct {
	instances []Instance
	err       error
	discover  func(context.Context) ([]Instance, error)
}

func (adapter *staticAdapter) Discover(ctx context.Context) ([]Instance, error) {
	if adapter.discover != nil {
		return adapter.discover(ctx)
	}
	return adapter.instances, adapter.err
}

func (*staticAdapter) Probe(context.Context, Candidate) (Instance, error) {
	return Instance{}, errors.New("not implemented")
}

func instanceIDs(instances []Instance) []string {
	ids := make([]string, len(instances))
	for index := range instances {
		ids[index] = instances[index].ID
	}
	return ids
}
