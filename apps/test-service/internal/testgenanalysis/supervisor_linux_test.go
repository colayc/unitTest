//go:build linux

package testgenanalysis

import (
	"os"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

// The analyzer's native test starts the current test binary as the production
// probe supervisor. Each Go test package owns its own test binary, so the
// supervisor entry must be present in this package as well as in probe's tests.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--probe-supervisor" {
		status := os.NewFile(3, "probe-supervisor-status")
		if status == nil {
			os.Exit(2)
		}
		os.Exit(probe.RunSupervisor(os.Stdin, status, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
