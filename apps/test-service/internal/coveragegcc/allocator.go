package coveragegcc

import (
	"reflect"
	"runtime"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
)

// Allocator protects GCC executions from inherited gcov/gcovr routing while
// preserving the executable, arguments, directory and batch target exactly.
type Allocator struct {
	mu        sync.Mutex
	allocated map[string]testrun.ProfileExpectation
	closed    bool
}

func NewAllocator() *Allocator {
	return &Allocator{allocated: make(map[string]testrun.ProfileExpectation)}
}

func (a *Allocator) Decorate(expectation testrun.ProfileExpectation, original task.ProcessSpec) (testrun.ProfileExpectation, task.ProcessSpec, error) {
	if runtime.GOOS == "windows" || a == nil || !validAllocation(expectation) || len(original.Batch) != 0 {
		return testrun.ProfileExpectation{}, task.ProcessSpec{}, ErrInvalidToolset
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return testrun.ProfileExpectation{}, task.ProcessSpec{}, ErrInvalidToolset
	}
	key := allocationKey(expectation)
	if existing, ok := a.allocated[key]; ok && existing != expectation {
		return testrun.ProfileExpectation{}, task.ProcessSpec{}, ErrInvalidToolset
	}
	result, err := decoratedGCCProcessSpec(original)
	if err != nil {
		return testrun.ProfileExpectation{}, task.ProcessSpec{}, err
	}
	a.allocated[key] = expectation
	return expectation, result, nil
}

func (a *Allocator) Validate(expectation testrun.ProfileExpectation, original, decorated task.ProcessSpec) error {
	if runtime.GOOS == "windows" || a == nil || !validAllocation(expectation) || !sameGCCProcessTarget(original, decorated) {
		return ErrInvalidToolset
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.allocated[allocationKey(expectation)] != expectation {
		return ErrInvalidToolset
	}
	want, err := decoratedGCCProcessSpec(original)
	if err != nil || !reflect.DeepEqual(want.Env, decorated.Env) || !reflect.DeepEqual(want.EnvUnset, decorated.EnvUnset) {
		return ErrInvalidToolset
	}
	return nil
}
func (a *Allocator) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return nil
}
func allocationKey(v testrun.ProfileExpectation) string {
	return v.InvocationID + "\x00" + string(rune(v.Iteration))
}
func validAllocation(v testrun.ProfileExpectation) bool {
	return v.InvocationID != "" && v.Iteration > 0 && v.Sequence > 0 && v.Sequence <= testrun.MaxProfileCount && v.FileName == ""
}
func cloneGCCProcessSpec(v task.ProcessSpec) task.ProcessSpec {
	r := v
	r.LaunchPlan = append([]string(nil), v.LaunchPlan...)
	r.LaunchInputs = append([]cmake.FingerprintFile(nil), v.LaunchInputs...)
	r.Args = append([]string(nil), v.Args...)
	r.Env = append([]string(nil), v.Env...)
	r.EnvUnset = append([]string(nil), v.EnvUnset...)
	r.Batch = append([]task.ProcessBatchItem(nil), v.Batch...)
	return r
}
func sameGCCProcessTarget(a, b task.ProcessSpec) bool {
	return a.Executable == b.Executable && a.Dir == b.Dir &&
		reflect.DeepEqual(a.Args, b.Args) && reflect.DeepEqual(a.LaunchPlan, b.LaunchPlan) &&
		reflect.DeepEqual(a.LaunchInputs, b.LaunchInputs) && len(a.Batch) == 0 && len(b.Batch) == 0
}

func decoratedGCCProcessSpec(original task.ProcessSpec) (task.ProcessSpec, error) {
	if len(original.Batch) != 0 {
		return task.ProcessSpec{}, ErrInvalidToolset
	}
	result := cloneGCCProcessSpec(original)
	result.Env = result.Env[:0]
	removed := make([]string, 0, len(original.Env))
	for _, value := range original.Env {
		name, _, found := strings.Cut(value, "=")
		if !found || !validGCCEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return task.ProcessSpec{}, ErrInvalidToolset
		}
		if hostileGCCEnvironmentName(name) {
			removed = append(removed, name)
			continue
		}
		result.Env = append(result.Env, value)
	}
	for _, value := range original.EnvUnset {
		if !validGCCEnvironmentName(value) {
			return task.ProcessSpec{}, ErrInvalidToolset
		}
	}
	// EnvUnset is part of the caller's launch contract. Preserve it byte-for-
	// byte (including order and duplicates), then append exactly the hostile
	// inherited variables removed from Env.
	result.EnvUnset = append(append([]string(nil), original.EnvUnset...), removed...)
	return result, nil
}
func hostileGCCEnvironmentName(name string) bool {
	return strings.HasPrefix(name, "GCOV_") || strings.HasPrefix(name, "GCOVR_")
}
func validGCCEnvironmentName(v string) bool {
	if v == "" {
		return false
	}
	for i, c := range []byte(v) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

var _ testrun.ProfileAllocator = (*Allocator)(nil)
