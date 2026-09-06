package coveragegcc

import (
	"reflect"
	"runtime"
	"testing"

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
