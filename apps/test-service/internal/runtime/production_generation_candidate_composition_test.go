package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionCompositionProbeRunner struct{}

func (productionCompositionProbeRunner) Run(context.Context, probe.Spec) (probe.Result, error) {
	return probe.Result{}, nil
}

func TestProductionGenerationCandidateCompositionBindsRealClosedComponents(t *testing.T) {
	runtimeValue, store, closeAll := productionCandidateCompositionFixture(t)
	defer closeAll()

	composition, err := newProductionGenerationCandidateComposition(store, runtimeValue)
	if err != nil {
		t.Fatalf("newProductionGenerationCandidateComposition() error=%v", err)
	}
	if composition.driver == nil || composition.resolver == nil || composition.validation == nil || !composition.validation.Ready() ||
		composition.snapshots == nil || composition.artifacts == nil || composition.processes == nil ||
		composition.processes.OwnerDigest() != productionBytesDigest([]byte(runtimeValue.serviceInstanceID)) {
		t.Fatalf("composition=%+v", composition)
	}
	if composition.validation.builds == nil || composition.validation.plans == nil || composition.validation.validator.Config.Planner == nil ||
		composition.validation.validator.Config.ResolveCandidate == nil || composition.validation.validator.Config.VerifyEvidence == nil {
		t.Fatal("native validation authorities were not bound")
	}
	if composition.driver.pipeline == nil || composition.driver.pipeline.analyzer == nil || composition.driver.pipeline.oracle == nil {
		t.Fatal("offline analyzer/solver/oracle pipeline was not bound")
	}
}

func TestProductionGenerationCandidateCompositionFailsClosedWhenDependencyMissing(t *testing.T) {
	tests := map[string]func(*Runtime){
		"product bundles": func(value *Runtime) { value.productBundles = nil },
		"build authority": func(value *Runtime) { value.productionBuilds = nil },
		"process runner":  func(value *Runtime) { value.runner = nil },
		"probe runner":    func(value *Runtime) { value.probeRunner = nil },
		"artifacts":       func(value *Runtime) { value.artifacts = nil },
		"workspace":       func(value *Runtime) { value.workspaceRoot = workspace.Root{} },
		"control root":    func(value *Runtime) { value.controlDataRoot = "" },
	}
	for name, remove := range tests {
		t.Run(name, func(t *testing.T) {
			runtimeValue, store, closeAll := productionCandidateCompositionFixture(t)
			defer closeAll()
			remove(runtimeValue)
			if composition, err := newProductionGenerationCandidateComposition(store, runtimeValue); err == nil || composition != nil {
				t.Fatalf("composition=%#v error=%v", composition, err)
			}
		})
	}
}

func productionCandidateCompositionFixture(t *testing.T) (*Runtime, *taskstore.Store, func()) {
	t.Helper()
	base := t.TempDir()
	workspacePath := filepath.Join(base, "workspace")
	controlRoot := filepath.Join(base, "controls")
	artifactRoot := filepath.Join(base, "artifacts")
	for _, path := range []string{workspacePath, controlRoot, artifactRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := workspace.OpenRoot(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := taskstore.Open(filepath.Join(base, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactstore.New(artifactRoot)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	tools := filepath.Join(base, "tools")
	if err := os.MkdirAll(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	cmakePath, ctestPath := filepath.Join(tools, "cmake.exe"), filepath.Join(tools, "ctest.exe")
	for _, path := range []string{cmakePath, ctestPath} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimeValue := &Runtime{
		store: store, artifacts: artifacts, runner: &recordingRunner{}, probeRunner: productionCompositionProbeRunner{},
		installation:      cmake.Installation{Executable: cmakePath, CTestExecutable: ctestPath, Version: "4.3.0", Identity: strings.Repeat("4", 64)},
		serviceInstanceID: strings.Repeat("5", 32), controlDataRoot: controlRoot, platform: platformForTest(), workspaceRoot: root,
		productBundles:   &ProductBundles{testgen: fakeVerifiedTestgenBundle{}},
		productionBuilds: &productionBuildPreparerFixture{},
	}
	return runtimeValue, store, func() {
		_ = artifacts.Close()
		_ = store.Close()
	}
}
