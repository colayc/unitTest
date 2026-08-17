package testdomain

import (
	"errors"
	"strings"
	"testing"
)

func TestStableIDsIgnoreRuntimePathsProfilesAndToolchains(t *testing.T) {
	base := CaseIdentity{
		ProjectID:   "core",
		CTestName:   "core.cpputest",
		Framework:   FrameworkCppUTest,
		Group:       "Math",
		Suite:       "Addition",
		Name:        "adds_numbers",
		SourcePath:  `C:\work\core\math_test.cpp`,
		ProfileID:   strings.Repeat("1", 64),
		ToolchainID: "windows-msvc",
	}
	windowsID, err := CaseID(base)
	if err != nil {
		t.Fatal(err)
	}
	base.SourcePath = "/home/runner/work/core/math_test.cpp"
	base.ProfileID = strings.Repeat("2", 64)
	base.ToolchainID = "linux-clang"
	linuxID, err := CaseID(base)
	if err != nil {
		t.Fatal(err)
	}
	if windowsID != linuxID {
		t.Fatalf("runtime-only fields changed ID: %q != %q", windowsID, linuxID)
	}
}

func TestStableIDTupleBoundariesDoNotCollide(t *testing.T) {
	first, err := ContainerID("ab", "c")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ContainerID("a", "bc")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("length-prefixed tuples collided: %q", first)
	}
}

func TestStableIDsNormalizeNFC(t *testing.T) {
	decomposed, err := GroupID("core", "unicode", FrameworkCppUTest, "Cafe\u0301")
	if err != nil {
		t.Fatal(err)
	}
	composed, err := GroupID("core", "unicode", FrameworkCppUTest, "Café")
	if err != nil {
		t.Fatal(err)
	}
	if decomposed != composed {
		t.Fatalf("NFC-equivalent values produced different IDs: %q != %q", decomposed, composed)
	}
}

func TestCaseRenameAndFrameworkChangeProduceNewIDs(t *testing.T) {
	identity := CaseIdentity{
		ProjectID: "core",
		CTestName: "core.tests",
		Framework: FrameworkCppUTest,
		Group:     "Math",
		Name:      "adds",
	}
	original, err := CaseID(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.Name = "adds_numbers"
	renamed, err := CaseID(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.Name = "adds"
	identity.Framework = FrameworkUnity
	changedFramework, err := CaseID(identity)
	if err != nil {
		t.Fatal(err)
	}
	if original == renamed || original == changedFramework {
		t.Fatalf("semantic identity change did not change ID: %q %q %q", original, renamed, changedFramework)
	}
}

func TestContainerIDDoesNotIncludeFramework(t *testing.T) {
	for _, framework := range []Framework{FrameworkCppUTest, FrameworkUnity, FrameworkOpaqueCTest} {
		_ = framework
		got, err := ContainerID("core", "core.tests")
		if err != nil {
			t.Fatal(err)
		}
		if got != mustContainerID(t, "core", "core.tests") {
			t.Fatalf("framework selection changed container ID: %q", got)
		}
	}
}

func TestStableIDValidationIsExplicit(t *testing.T) {
	if _, err := ContainerID("", "tests"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("empty project error = %v", err)
	}
	if _, err := GroupID("core", "tests", Framework("other"), "group"); !errors.Is(err, ErrInvalidFramework) {
		t.Fatalf("unknown framework error = %v", err)
	}
	if _, err := CaseID(CaseIdentity{ProjectID: "core", CTestName: "tests", Framework: FrameworkUnity}); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("empty case error = %v", err)
	}
	if !ValidID(mustContainerID(t, "core", "tests")) ||
		ValidID("bad") ||
		ValidID(ID("utid-v1-"+strings.Repeat("A", 64))) {
		t.Fatal("ValidID accepted an invalid value or rejected a generated ID")
	}
}

func mustContainerID(t *testing.T, projectID, ctestName string) ID {
	t.Helper()
	id, err := ContainerID(projectID, ctestName)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
