package build

import (
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestCoverageToolsetIdentityAcceptsCompleteGCCCapability(t *testing.T) {
	paths := []string{"/usr/bin/gcc", "/usr/bin/g++", "/usr/bin/gcov"}
	evidence := []toolchain.ExecutableEvidence{
		{FileIdentity: "unix:1:10", SHA256: strings.Repeat("a", 64)},
		{FileIdentity: "unix:1:11", SHA256: strings.Repeat("b", 64)},
		{FileIdentity: "unix:1:12", SHA256: strings.Repeat("c", 64)},
	}
	identity := toolchain.GCCToolsetIdentity("14.2.0", paths, evidence)
	instance := toolchain.Instance{
		Family: toolchain.FamilyGCC, CCompiler: paths[0], CXXCompiler: paths[1], Version: "14.2.0",
		Coverage: toolchain.CoverageCapability{
			GCov: paths[2], GCovVersion: "14.2.0", ToolsetIdentity: identity,
			CompilerEvidence: evidence[0], CXXCompilerEvidence: evidence[1], GCovEvidence: evidence[2],
		},
	}
	got, err := coverageToolsetIdentity(instance, true)
	if err != nil || got != identity {
		t.Fatalf("complete GCC coverage capability = %q, %v; want %q", got, err, identity)
	}
}

func TestCoverageToolsetIdentityRejectsIncompleteGCCCapability(t *testing.T) {
	instance := toolchain.Instance{Family: toolchain.FamilyGCC, Version: "14.2.0"}
	if _, err := coverageToolsetIdentity(instance, true); err == nil {
		t.Fatal("incomplete GCC coverage capability was accepted")
	}
}
