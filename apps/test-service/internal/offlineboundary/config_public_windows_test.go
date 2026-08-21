//go:build windows

package offlineboundary_test

import (
	"testing"

	"unit-test-ide.local/test-service/internal/offlineboundary"
)

func TestConfigExposesGuardianExecutablePathForCallers(t *testing.T) {
	t.Run("non-empty override wins", func(t *testing.T) {
		override := `C:\callers\provided\native-offline-guardian.exe`
		got, err := offlineboundary.ResolveGuardianExecutablePath(
			offlineboundary.Config{GuardianExecutablePath: override},
			func() (string, error) {
				t.Fatal("resolver should not be called when override is set")
				return "", nil
			},
		)
		if err != nil {
			t.Fatalf("ResolveGuardianExecutablePath() error = %v", err)
		}
		if got != override {
			t.Fatalf("ResolveGuardianExecutablePath() = %q, want %q", got, override)
		}
	})

	t.Run("empty value uses sibling guardian discovery", func(t *testing.T) {
		got, err := offlineboundary.ResolveGuardianExecutablePath(
			offlineboundary.Config{},
			func() (string, error) { return `C:\apps\unit-test-service.exe`, nil },
		)
		if err != nil {
			t.Fatalf("ResolveGuardianExecutablePath() error = %v", err)
		}
		want := `C:\apps\native-offline-guardian.exe`
		if got != want {
			t.Fatalf("ResolveGuardianExecutablePath() = %q, want %q", got, want)
		}
	})

	if value := offlineboundary.New(offlineboundary.Config{
		GuardianExecutablePath: `C:\callers\provided\native-offline-guardian.exe`,
	}); value == nil {
		t.Fatal("New() returned nil")
	}
}
