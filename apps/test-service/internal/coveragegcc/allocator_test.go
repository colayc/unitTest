package coveragegcc

import (
	"reflect"
	"runtime"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
)

func TestAllocatorClearsOnlyCaseSensitiveHostileGCOVVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC allocator is intentionally unsupported on Windows")
	}
	original := task.ProcessSpec{Executable: "test", Args: []string{"--x"}, Env: []string{"GCOV_PREFIX=bad", "GCOVR_X=bad", "gcovr_x=preserved", "SAFE=ok"}, EnvUnset: []string{"OLD"}, Dir: "/work"}
	allocator := NewAllocator()
	expectation := testrun.ProfileExpectation{InvocationID: "invocation", Iteration: 1, Sequence: 2}
	gotExpectation, decorated, err := allocator.Decorate(expectation, original)
	if err != nil {
		t.Fatal(err)
	}
	if gotExpectation.FileName != "" {
		t.Fatalf("file name = %q, want empty", gotExpectation.FileName)
	}
	if !reflect.DeepEqual(decorated.Env, []string{"gcovr_x=preserved", "SAFE=ok"}) {
		t.Fatalf("env = %#v", decorated.Env)
	}
	if !reflect.DeepEqual(decorated.EnvUnset, []string{"GCOV_PREFIX", "GCOVR_X", "OLD"}) {
		t.Fatalf("unset = %#v", decorated.EnvUnset)
	}
	if err := allocator.Validate(gotExpectation, original, decorated); err != nil {
		t.Fatal(err)
	}
}

func TestAllocatorKeepsSerialSequenceStable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC allocator is intentionally unsupported on Windows")
	}
	allocator := NewAllocator()
	spec := task.ProcessSpec{Executable: "test"}
	a := testrun.ProfileExpectation{InvocationID: "one", Iteration: 1, Sequence: 1}
	b := testrun.ProfileExpectation{InvocationID: "two", Iteration: 1, Sequence: 2}
	for _, want := range []testrun.ProfileExpectation{a, b, a} {
		got, _, err := allocator.Decorate(want, spec)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("expectation = %#v, want %#v", got, want)
		}
	}
}

// This is deliberately a whole-spec comparison: the allocator is authorized
// to remove only hostile variables and to add those exact names to EnvUnset.
func TestAllocatorValidateRejectsAnyUnapprovedProcessTransformation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC allocator is intentionally unsupported on Windows")
	}
	original := task.ProcessSpec{
		Executable: "test", Args: []string{"--x"}, Dir: "/work",
		Env: []string{"SAFE=ok", "GCOV_PREFIX=hostile"}, EnvUnset: []string{"OLD"},
		LaunchPlan: []string{"test", "child"}, LaunchInputs: []cmake.FingerprintFile{{Path: "source", Identity: "identity", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	allocator := NewAllocator()
	want := testrun.ProfileExpectation{InvocationID: "invocation", Iteration: 1, Sequence: 1}
	got, decorated, err := allocator.Decorate(want, original)
	if err != nil {
		t.Fatal(err)
	}
	original.LaunchPlan[0] = "mutated-after-decoration"
	original.LaunchInputs[0].Path = "mutated-after-decoration"
	if decorated.LaunchPlan[0] != "test" || decorated.LaunchInputs[0].Path != "source" {
		t.Fatal("decorated launch state aliases caller input")
	}
	original.LaunchPlan[0] = "test"
	original.LaunchInputs[0].Path = "source"
	for name, tamper := range map[string]func(*task.ProcessSpec){
		"environment value": func(v *task.ProcessSpec) { v.Env[0] = "SAFE=changed" },
		"added environment": func(v *task.ProcessSpec) { v.Env = append(v.Env, "EXTRA=1") },
		"unset change":      func(v *task.ProcessSpec) { v.EnvUnset = append(v.EnvUnset, "EXTRA") },
		"launch plan":       func(v *task.ProcessSpec) { v.LaunchPlan[0] = "other" },
		"launch inputs":     func(v *task.ProcessSpec) { v.LaunchInputs[0].Path = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			value := cloneGCCProcessSpec(decorated)
			tamper(&value)
			if err := allocator.Validate(got, original, value); err == nil {
				t.Fatal("tampered process validated")
			}
		})
	}
}
