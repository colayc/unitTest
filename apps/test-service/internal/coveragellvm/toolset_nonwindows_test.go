//go:build !windows

package coveragellvm

import (
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestLLVMToolsetReportsUnsupportedPlatform(t *testing.T) {
	toolset, err := PinToolset(toolchain.Instance{})
	if toolset != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("PinToolset() = %#v, %v, want nil ErrUnsupportedPlatform", toolset, err)
	}
}
