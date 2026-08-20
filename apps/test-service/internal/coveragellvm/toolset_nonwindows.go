//go:build !windows

package coveragellvm

import (
	"os"

	"unit-test-ide.local/test-service/internal/toolchain"
)

type nativeFileIdentity struct{}

func PinToolset(toolchain.Instance) (*Toolset, error) {
	return nil, ErrUnsupportedPlatform
}

func verifyPinnedTool(*pinnedTool) error { return ErrUnsupportedPlatform }

func verifyPinnedDirectory(string, *os.File, os.FileInfo, nativeFileIdentity) error {
	return ErrUnsupportedPlatform
}
