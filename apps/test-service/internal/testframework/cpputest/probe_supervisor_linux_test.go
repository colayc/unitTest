//go:build linux

package cpputest

import (
	"os"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

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
