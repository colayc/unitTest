//go:build windows

package toolchain

import (
	"fmt"
	"os"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

// NewUnixAdapters is a compile boundary for callers shared by both platforms.
// Unix discovery is intentionally unavailable in a Windows service process.
func NewUnixAdapters(probe.Runner, []workspace.ToolchainConfig) []Adapter {
	return []Adapter{}
}

func directoryOSIdentity(info os.FileInfo) (string, error) {
	if info == nil || !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	return fmt.Sprintf(
		"windows-compat:%s:%d:%d",
		info.Name(),
		info.ModTime().UnixNano(),
		info.Mode(),
	), nil
}
