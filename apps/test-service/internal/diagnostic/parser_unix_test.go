//go:build !windows

package diagnostic

import (
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestPOSIXDiagnosticIdentityRemainsCaseSensitive(t *testing.T) {
	lower := &parser{
		options:     Options{Root: workspace.Root{URI: "file:///tmp/workspace"}},
		occurrences: make(map[string]int),
	}
	upper := &parser{
		options:     Options{Root: workspace.Root{URI: "file:///tmp/workspace"}},
		occurrences: make(map[string]int),
	}
	lowerValue := Diagnostic{
		Source: "compiler", Severity: "error", Code: "COMPILER_ERROR",
		Message: "broken", FileURI: "file:///tmp/workspace/src/main.cpp",
	}
	upperValue := lowerValue
	upperValue.FileURI = "file:///tmp/workspace/src/Main.cpp"

	if lower.diagnosticID(lowerValue) == upper.diagnosticID(upperValue) {
		t.Fatal("POSIX filename casing collapsed to one diagnostic identity")
	}
}
