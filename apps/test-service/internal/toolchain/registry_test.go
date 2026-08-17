package toolchain

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func TestRegistrySanitizesCarrierIssues(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(&staticAdapter{err: carrierTestError{issues: []Issue{
		{
			Code:     "BUILD_TOOL_NOT_FOUND",
			Message:  "failed under /secret/customer/TOKEN=abcdef with full probe output",
			Blocking: true,
		},
		{
			Code:     "INVALID/CODE/TOKEN",
			Message:  strings.Repeat("secret-output-", 1024),
			Blocking: true,
		},
	}}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, issues := registry.Discover(context.Background())
	want := []Issue{
		{
			Code:     "BUILD_TOOL_NOT_FOUND",
			Message:  "toolchain candidate has no verified Ninja or Make build tool",
			Blocking: false,
		},
		{
			Code:     "TOOLCHAIN_DISCOVERY_FAILED",
			Message:  "toolchain adapter 0 returned an invalid issue",
			Blocking: false,
		},
	}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("Discover() issues = %+v, want %+v", issues, want)
	}
	for _, issue := range issues {
		for _, secret := range []string{"/secret/customer", "TOKEN=", "abcdef", "secret-output"} {
			if strings.Contains(issue.Code, secret) || strings.Contains(issue.Message, secret) {
				t.Fatalf("Discover() issue %+v exposes %q", issue, secret)
			}
		}
	}
}

func TestRegistrySanitizesWindowsCarrierIssuesWithFixedMessages(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(&staticAdapter{err: carrierTestError{issues: []Issue{
		{
			Code:     "TOOLCHAIN_ENVIRONMENT_INVALID",
			Message:  "environment contained TOKEN=super-secret under C:\\customer",
			Blocking: true,
		},
		{
			Code:     "TOOLCHAIN_MANUAL_SELECTION_FAILED",
			Message:  "installationId=secret-customer-installation was absent",
			Blocking: true,
		},
		{
			Code:     "WINDOWS_BUILD_TOOL_NOT_FOUND",
			Message:  "Ninja failed under C:\\customer\\TOKEN=secret",
			Blocking: true,
		},
	}}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	_, issues := registry.Discover(context.Background())
	want := []Issue{
		{
			Code:     "TOOLCHAIN_ENVIRONMENT_INVALID",
			Message:  "Windows toolchain environment is invalid",
			Blocking: false,
		},
		{
			Code:     "TOOLCHAIN_MANUAL_SELECTION_FAILED",
			Message:  "manual Windows toolchain selection was not found",
			Blocking: false,
		},
		{
			Code:     "WINDOWS_BUILD_TOOL_NOT_FOUND",
			Message:  "Windows toolchain has no verified build generator",
			Blocking: false,
		},
	}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("Discover() issues = %+v, want %+v", issues, want)
	}
	for _, issue := range issues {
		for _, secret := range []string{"TOKEN=", "super-secret", "C:\\customer", "secret-customer"} {
			if strings.Contains(issue.Message, secret) {
				t.Fatalf("Discover() issue %+v exposes %q", issue, secret)
			}
		}
	}
}

func TestRegistryAcceptsExactInstanceFieldBudgets(t *testing.T) {
	t.Parallel()

	instance := boundedRegistryInstance(0)
	instance.ID = "i" + strings.Repeat("a", maxRegistryIDBytes-1)
	instance.CCompiler = "/" + strings.Repeat("c", maxRegistryPathBytes-1)
	instance.CXXCompiler = "/" + strings.Repeat("x", maxRegistryPathBytes-1)
	instance.Version = strings.Repeat("1", maxRegistryVersionBytes)
	instance.TargetTriple = strings.Repeat("t", maxRegistryTripleBytes)
	instance.Sysroot = "/" + strings.Repeat("s", maxRegistryPathBytes-1)
	instance.Coverage = CoverageCapability{
		LLVMProfdata: "/" + strings.Repeat("p", maxRegistryPathBytes-1),
		LLVMCov:      "/" + strings.Repeat("l", maxRegistryPathBytes-1),
		GCov:         "/" + strings.Repeat("g", maxRegistryPathBytes-1),
	}
	instance.Environment = environmentWithExactCountAndTotal(
		maxRegistryEnvironmentEntries,
		maxRegistryEnvironmentTotalBytes,
	)
	instance.Generators = []string{
		"NMake Makefiles",
		"Ninja",
		"Unix Makefiles",
		"Visual Studio 17 2022",
	}
	registry, err := NewRegistry(&staticAdapter{instances: []Instance{instance}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	instances, issues := registry.Discover(context.Background())
	if len(instances) != 1 || len(issues) != 0 {
		t.Fatalf("Discover() = (%d instances, %+v), want one instance and no issues", len(instances), issues)
	}
}

func TestRegistryAcceptsOnlyPinnedVisualStudioGeneratorNames(t *testing.T) {
	t.Parallel()

	for _, generator := range []string{
		"Visual Studio 17 2022",
		"Visual Studio 18 2026",
	} {
		t.Run(generator, func(t *testing.T) {
			instance := boundedRegistryInstance(0)
			instance.Generators = []string{generator}
			registry, err := NewRegistry(&staticAdapter{instances: []Instance{instance}})
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}
			instances, issues := registry.Discover(context.Background())
			if len(instances) != 1 || len(issues) != 0 {
				t.Fatalf("Discover() = %#v, %#v", instances, issues)
			}
		})
	}

	instance := boundedRegistryInstance(1)
	instance.Generators = []string{"Visual Studio arbitrary"}
	registry, err := NewRegistry(&staticAdapter{instances: []Instance{instance}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	instances, issues := registry.Discover(context.Background())
	if len(instances) != 0 || len(issues) != 1 || issues[0].Code != "TOOLCHAIN_INVALID" {
		t.Fatalf("Discover(arbitrary generator) = %#v, %#v", instances, issues)
	}
}

func TestRegistryRejectsInstanceFieldBudgetPlusOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*Instance)
	}{
		{
			name: "id bytes",
			change: func(instance *Instance) {
				instance.ID = "i" + strings.Repeat("a", maxRegistryIDBytes)
			},
		},
		{
			name: "path bytes",
			change: func(instance *Instance) {
				instance.CCompiler = "/" + strings.Repeat("c", maxRegistryPathBytes)
			},
		},
		{
			name: "version bytes",
			change: func(instance *Instance) {
				instance.Version = strings.Repeat("1", maxRegistryVersionBytes+1)
			},
		},
		{
			name: "triple bytes",
			change: func(instance *Instance) {
				instance.TargetTriple = strings.Repeat("t", maxRegistryTripleBytes+1)
			},
		},
		{
			name: "environment entries",
			change: func(instance *Instance) {
				instance.Environment = make([]string, maxRegistryEnvironmentEntries+1)
				for index := range instance.Environment {
					instance.Environment[index] = fmt.Sprintf("K%d=v", index)
				}
			},
		},
		{
			name: "environment entry bytes",
			change: func(instance *Instance) {
				instance.Environment = []string{"A=" + strings.Repeat("v", maxRegistryEnvironmentEntryBytes-1)}
			},
		},
		{
			name: "environment total bytes",
			change: func(instance *Instance) {
				instance.Environment = environmentWithTotalBytes(maxRegistryEnvironmentTotalBytes + 1)
			},
		},
		{
			name: "generator entries",
			change: func(instance *Instance) {
				instance.Generators = []string{"Ninja", "Ninja", "Ninja", "Ninja", "Ninja"}
			},
		},
		{
			name: "coverage path bytes",
			change: func(instance *Instance) {
				instance.Coverage.LLVMProfdata = "/" + strings.Repeat("p", maxRegistryPathBytes)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			instance := boundedRegistryInstance(0)
			test.change(&instance)
			registry, err := NewRegistry(&staticAdapter{instances: []Instance{instance}})
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}

			instances, issues := registry.Discover(context.Background())
			if len(instances) != 0 {
				t.Fatalf("Discover() instances = %+v, want invalid instance rejected", instances)
			}
			if len(issues) != 1 || issues[0].Code != "TOOLCHAIN_INVALID" {
				t.Fatalf("Discover() issues = %+v, want TOOLCHAIN_INVALID", issues)
			}
		})
	}
}

func TestRegistryBoundsPerAdapterResultsBeforeNormalization(t *testing.T) {
	t.Parallel()

	exact := make([]Instance, maxRegistryAdapterResults)
	for index := range exact {
		exact[index] = boundedRegistryInstance(index)
	}
	registry, _ := NewRegistry(&staticAdapter{instances: exact})
	instances, issues := registry.Discover(context.Background())
	if len(instances) != maxRegistryAdapterResults || len(issues) != 0 {
		t.Fatalf("exact Discover() = (%d instances, %+v)", len(instances), issues)
	}

	excess := append(append([]Instance(nil), exact...), boundedRegistryInstance(maxRegistryAdapterResults))
	excess[len(excess)-1].Environment = []string{"TOKEN=" + strings.Repeat("secret", 4096)}
	registry, _ = NewRegistry(&staticAdapter{instances: excess})
	instances, issues = registry.Discover(context.Background())
	if len(instances) != maxRegistryAdapterResults {
		t.Fatalf("excess Discover() instances = %d, want %d", len(instances), maxRegistryAdapterResults)
	}
	want := []Issue{{
		Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
		Message:  "toolchain adapter 0 result limit exceeded",
		Blocking: true,
	}}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("excess Discover() issues = %+v, want %+v", issues, want)
	}
}

func TestRegistryStopsAtIssueBudgetWithStableLimitIssue(t *testing.T) {
	t.Parallel()

	carried := make([]Issue, maxRegistryIssues+10)
	for index := range carried {
		carried[index] = Issue{
			Code:    "TOOLCHAIN_PROBE_FAILED",
			Message: strings.Repeat("raw-secret-payload", 32),
		}
	}
	registry, _ := NewRegistry(&staticAdapter{err: carrierTestError{issues: carried}})

	_, issues := registry.Discover(context.Background())
	if len(issues) != maxRegistryIssues {
		t.Fatalf("Discover() issues = %d, want exact budget %d", len(issues), maxRegistryIssues)
	}
	limitCount := 0
	for _, issue := range issues {
		if issue.Code == "TOOLCHAIN_LIMIT_EXCEEDED" &&
			issue.Message == "toolchain issue limit exceeded" &&
			issue.Blocking {
			limitCount++
		}
		if strings.Contains(issue.Message, "raw-secret") {
			t.Fatalf("Discover() leaked carrier payload in %+v", issue)
		}
	}
	if limitCount != 1 {
		t.Fatalf("Discover() limit issue count = %d, want 1", limitCount)
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

func TestNormalizeRegistryResultsTreatsContextErrorsAsGlobalCancellation(t *testing.T) {
	t.Parallel()

	for _, contextErr := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		fmt.Errorf("wrapped cancellation: %w", context.Canceled),
		fmt.Errorf("wrapped deadline: %w", context.DeadlineExceeded),
	} {
		instances, issues := normalizeRegistryResults([]adapterResult{{
			index: 0,
			err:   contextErr,
		}})
		if instances != nil || issues != nil {
			t.Fatalf(
				"normalizeRegistryResults(%v) = (%+v, %+v), want (nil, nil)",
				contextErr,
				instances,
				issues,
			)
		}
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

type carrierTestError struct {
	issues []Issue
}

func (carrier carrierTestError) Error() string {
	return "carrier error"
}

func (carrier carrierTestError) ToolchainIssues() []Issue {
	return carrier.issues
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

func boundedRegistryInstance(index int) Instance {
	return Instance{
		ID:                 fmt.Sprintf("gcc-%03d", index),
		Family:             FamilyGCC,
		CCompiler:          fmt.Sprintf("/tools/gcc-%03d", index),
		CXXCompiler:        fmt.Sprintf("/tools/g++-%03d", index),
		Version:            "13.2.0",
		TargetTriple:       "x86_64-linux-gnu",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
		Sysroot:            "/",
		Environment:        []string{"LANG=C"},
		Generators:         []string{"Ninja"},
	}
}

func environmentWithTotalBytes(total int) []string {
	const maximumValueBytes = maxRegistryEnvironmentEntryBytes - len("KEY_000=")
	var result []string
	for total > 0 {
		index := len(result)
		prefix := fmt.Sprintf("KEY_%03d=", index)
		valueBytes := min(total-len(prefix), maximumValueBytes)
		if valueBytes < 0 {
			valueBytes = 0
		}
		entry := prefix + strings.Repeat("v", valueBytes)
		result = append(result, entry)
		total -= len(entry)
	}
	return result
}

func environmentWithExactCountAndTotal(count, total int) []string {
	result := make([]string, count)
	used := 0
	for index := range result {
		result[index] = fmt.Sprintf("KEY_%03d=", index)
		used += len(result[index])
	}
	remaining := total - used
	for index := range result {
		capacity := maxRegistryEnvironmentEntryBytes - len(result[index])
		add := min(remaining, capacity)
		result[index] += strings.Repeat("v", add)
		remaining -= add
	}
	if remaining != 0 {
		panic("test environment budget cannot be represented")
	}
	return result
}
