//go:build !windows

package toolchain

import (
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

// NewWindowsAdapters is the platform compile boundary. A non-Windows service
// neither inspects Windows installation paths nor invokes a probe runner.
func NewWindowsAdapters(probe.Runner, []workspace.ToolchainConfig) []Adapter {
	return []Adapter{}
}
