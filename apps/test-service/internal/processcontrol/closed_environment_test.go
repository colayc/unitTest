package processcontrol

import (
	"reflect"
	"testing"
)

func TestClosedEnvironmentNeverInheritsHostSettings(t *testing.T) {
	original := environSnapshot
	environSnapshot = func() []string {
		return []string{"LD_PRELOAD=/host/inject.so", "CMAKE_PREFIX_PATH=/host/other", "PATH=/host/bin"}
	}
	t.Cleanup(func() { environSnapshot = original })
	got := ClosedEnvironment([]string{"TEMP=/owned/temp", "LLVM_PROFILE_FILE=/owned/profile.profraw"})
	want := []string{"LLVM_PROFILE_FILE=/owned/profile.profraw", "TEMP=/owned/temp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("closed environment inherited host values: %v", got)
	}
}
