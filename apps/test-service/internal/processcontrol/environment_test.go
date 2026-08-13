package processcontrol

import (
	"reflect"
	"testing"
)

func TestSanitizeEnvironmentRemovesHostileFamiliesAtSnapshotBarrier(t *testing.T) {
	previous := environSnapshot
	defer func() { environSnapshot = previous }()
	called := false
	environSnapshot = func() []string {
		called = true
		return []string{"PYTHONPATH=hostile", "pYtHoNpAtH=variant", "SAFE=host", "UNIT_TEST_IDE_STATUS_HANDLE=7"}
	}
	got := SanitizeEnvironment(nil, nil)
	if !called {
		t.Fatal("environment snapshot barrier was not invoked")
	}
	want := []string{"SAFE=host"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SanitizeEnvironment() = %#v, want %#v", got, want)
	}
}
