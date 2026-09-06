//go:build windows

package coveragegcc

import (
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestGCCPinToolsetWindowsHasNoFilesystemFallback(t *testing.T) {
	if toolset, err := PinToolset(toolchain.Instance{Family: toolchain.FamilyGCC, CCompiler: `Z:\missing\gcc`}); toolset != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("PinToolset() = %#v, %v; want nil ErrUnsupportedPlatform", toolset, err)
	}
}
