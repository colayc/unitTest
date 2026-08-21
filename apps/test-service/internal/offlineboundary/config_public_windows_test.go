//go:build windows

package offlineboundary_test

import (
	"testing"

	"unit-test-ide.local/test-service/internal/offlineboundary"
)

func TestConfigExposesGuardianExecutablePathForCallers(t *testing.T) {
	value := offlineboundary.New(offlineboundary.Config{
		GuardianExecutablePath: `C:\callers\provided\native-offline-guardian.exe`,
	})
	if value == nil {
		t.Fatal("New() returned nil")
	}
}
