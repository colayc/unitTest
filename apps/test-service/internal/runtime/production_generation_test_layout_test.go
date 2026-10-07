package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionCTestSnapshotFixture struct{ snapshot ctest.Snapshot }

func (fixture productionCTestSnapshotFixture) ReadProductionCTest(context.Context, cmake.BuildProfile) (ctest.Snapshot, error) {
	return fixture.snapshot, nil
}

func TestCTestProductionTestLayoutResolvesConfiguredExecutableAndCMake(t *testing.T) {
	authority, resolved, binding := productionTestLayoutFixture(t)

	layout, err := authority.ResolveProductionTestLayout(context.Background(), resolved, binding, testgendomain.FrameworkAuto)
	if err != nil {
		t.Fatalf("ResolveProductionTestLayout() error = %v", err)
	}
	if layout.framework != testgendomain.FrameworkUnity || layout.frameworkDigest != strings.Repeat("7", 64) || layout.ctestName != "unit" ||
		layout.testTargetID != strings.Repeat("1", 64) ||
		layout.renderTarget.TestTarget != "unit_tests" || layout.renderTarget.ProductionTarget != "core" ||
		layout.renderTarget.FrameworkTarget != "unity" || layout.renderTarget.CMakePath != "tests/CMakeLists.txt" ||
		layout.renderTarget.TestPath != "tests/generated/src/choose.c_test.c" || layout.renderTarget.ExistingCMake == "" {
		t.Fatalf("layout = %+v", layout)
	}
}

func TestCTestProductionTestLayoutRejectsAmbiguousOrMismatchedContainers(t *testing.T) {
	t.Run("framework mismatch", func(t *testing.T) {
		authority, resolved, binding := productionTestLayoutFixture(t)
		if _, err := authority.ResolveProductionTestLayout(context.Background(), resolved, binding, testgendomain.FrameworkCppUTest); err == nil {
			t.Fatal("framework mismatch was accepted")
		}
	})
	t.Run("duplicate configured container", func(t *testing.T) {
		authority, resolved, binding := productionTestLayoutFixture(t)
		resolved.project.Tests.Containers = append(resolved.project.Tests.Containers, resolved.project.Tests.Containers[0])
		if _, err := authority.ResolveProductionTestLayout(context.Background(), resolved, binding, testgendomain.FrameworkAuto); err == nil {
			t.Fatal("duplicate container was accepted")
		}
	})
	t.Run("missing framework target", func(t *testing.T) {
		authority, resolved, binding := productionTestLayoutFixture(t)
		resolved.targets = resolved.targets[:2]
		if _, err := authority.ResolveProductionTestLayout(context.Background(), resolved, binding, testgendomain.FrameworkAuto); err == nil {
			t.Fatal("missing framework target was accepted")
		}
	})
}

func productionTestLayoutFixture(t *testing.T) (*ctestProductionTestLayoutAuthority, productionResolverContext, productionCompileBinding) {
	t.Helper()
	root, production := productionCompileBindingFixture(t)
	testsDirectory := filepath.Join(root.NativePath, "tests")
	testExecutable := filepath.Join(root.NativePath, "build", "unit_tests.exe")
	if err := os.MkdirAll(filepath.Dir(testExecutable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testExecutable, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	testTarget := cmake.Target{
		ID: strings.Repeat("1", 64), Name: "unit_tests", Type: "EXECUTABLE",
		ProjectID: "core", ProfileID: production.ProfileID, Configuration: "Debug",
		SourceDir: testsDirectory, BuildDir: filepath.Dir(testExecutable),
		ProjectSourceDir: root.NativePath, ProjectBuildDir: root.NativePath,
		Artifacts: []string{testExecutable},
	}
	frameworkTarget := cmake.Target{ID: strings.Repeat("2", 64), Name: "unity", Type: "STATIC_LIBRARY", ProjectID: "core", ProfileID: production.ProfileID}
	profile := cmake.BuildProfile{ID: production.ProfileID, ProjectID: "core", BinaryDir: root.NativePath, Configuration: "Debug"}
	raw := ctest.RawTest{
		Name: "unit", Config: "Debug", Command: []string{testExecutable},
		Properties: []ctest.Property{{
			Name:  "WORKING_DIRECTORY",
			Value: ctest.PropertyValue{Kind: ctest.PropertyString, String: root.NativePath},
		}},
	}
	resolved := productionResolverContext{
		project: workspace.ProjectConfig{ID: "core", Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{{CTestName: "unit", Framework: workspace.FrameworkUnity}}}},
		profile: profile, targets: []cmake.Target{production, testTarget, frameworkTarget},
	}
	binding, err := resolveProductionCompileBinding(root, "src/choose.c", resolved.targets, strings.Repeat("5", 64))
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newCTestProductionTestLayoutAuthority(productionTestLayoutConfig{
		root: root, ctest: productionCTestSnapshotFixture{snapshot: ctest.Snapshot{Tests: []ctest.RawTest{raw}}},
		frameworkDigests: map[testgendomain.Framework]string{
			testgendomain.FrameworkUnity: strings.Repeat("7", 64), testgendomain.FrameworkCppUTest: strings.Repeat("8", 64),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority, resolved, binding
}
