//go:build windows

package coveragegcc

import "unit-test-ide.local/test-service/internal/toolchain"

type nativeFileIdentity struct{}

func PinToolset(toolchain.Instance) (*Toolset, error) { return nil, ErrUnsupportedPlatform }

func verifyPinnedTool(*pinnedTool) error { return ErrUnsupportedPlatform }
