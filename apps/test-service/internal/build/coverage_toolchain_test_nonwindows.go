//go:build !windows

package build

import (
	"testing"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func testCoverageToolchainEvidence(_ *testing.T, instance toolchain.Instance) toolchain.Instance {
	return instance
}
