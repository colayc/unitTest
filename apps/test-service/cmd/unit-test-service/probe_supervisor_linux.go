//go:build linux

package main

import (
	"io"
	"os"

	"unit-test-ide.local/test-service/internal/probe"
)

func init() {
	probeSupervisorEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
		status := os.NewFile(3, "probe-supervisor-status")
		if status == nil {
			return 2
		}
		defer status.Close()
		return probe.RunSupervisor(stdin, status, stdout, stderr)
	}
}
