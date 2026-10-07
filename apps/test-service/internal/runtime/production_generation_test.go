package runtime

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func completeProductionGenerationConfig(t *testing.T) ProductionGenerationConfig {
	t.Helper()
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "production-generation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reads := &managedReadFixture{ready: true}
	return ProductionGenerationConfig{
		Base: GenerationServiceConfig{
			Store: store, Driver: &generationDriverFixture{complete: true},
			Publisher: &generationPublisherFixture{complete: true}, Trusted: true, CoverageReady: true,
			VerifySnapshot: func(_ context.Context, request testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
				return request.SnapshotIdentity(), nil
			},
			VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
			VerifyProcess:  func(context.Context, string, string) error { return nil },
		},
		Managed: ManagedRuntimeConfig{
			CurrentIndex: reads, Reviews: reads, Publisher: &managedPublisherFixture{ready: true},
			Validator: healthyManagedValidator(true), Baseline: managedBaselineFixture(true),
			Receipts: managedReceiptFixture(true), Driver: &managedBackendDriverFixture{ready: true},
		},
	}
}

func TestRuntimeExposesOnlyCompleteProductionGeneration(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	bundleParent := filepath.Join(base, "bundles")
	roots := ProductBundleRoots{
		CMake: filepath.Join(bundleParent, "cmake"), Coverage: filepath.Join(bundleParent, "coverage"),
		Testgen: filepath.Join(bundleParent, "testgen"),
	}
	for _, root := range []string{roots.CMake, roots.Coverage, roots.Testgen} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{Executable: os.Args[0], Identity: strings.Repeat("a", 64), Version: "test", Source: cmake.SourceBundle}, nil
	}
	called := 0
	factory := func(*taskstore.Store, *Runtime) (ProductionGenerationConfig, error) {
		called++
		reads := &managedReadFixture{ready: true}
		return ProductionGenerationConfig{
			Base: GenerationServiceConfig{
				Driver: &generationDriverFixture{complete: true}, Publisher: &generationPublisherFixture{complete: true},
				VerifySnapshot: func(_ context.Context, request testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
					return request.SnapshotIdentity(), nil
				},
				VerifyArtifact: func(context.Context, task.Artifact) error { return nil }, VerifyProcess: func(context.Context, string, string) error { return nil },
			},
			Managed: ManagedRuntimeConfig{
				CurrentIndex: reads, Reviews: reads, Publisher: &managedPublisherFixture{ready: true},
				Validator: healthyManagedValidator(true), Baseline: managedBaselineFixture(true),
				Receipts: managedReceiptFixture(true), Driver: &managedBackendDriverFixture{ready: true},
			},
		}, nil
	}
	active, err := Open(Config{
		DataDir: filepath.Join(base, "data"), ServiceExecutable: os.Args[0], WorkspaceRoot: workspaceRoot,
		TrustedWorkspace: true, CoverageBackend: &runtimeCoverageBackend{}, Platform: goruntime.GOOS,
		ProductBundleRoots: roots, ProductionGenerationFactory: factory, dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	backend := active.GenerationBackend()
	managed, ok := backend.(*ManagedRuntimeProvider)
	if !ok || !managed.TestGenerationReady() || !managed.ManagedTestsReady() || called != 1 {
		t.Fatalf("backend=%T base=%v managed=%v calls=%d", backend, managed != nil && managed.TestGenerationReady(), managed != nil && managed.ManagedTestsReady(), called)
	}
}

func TestProductionGenerationCompositionIsAtomic(t *testing.T) {
	complete := completeProductionGenerationConfig(t)
	provider, err := newProductionGenerationBackend(complete)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)
	if !provider.TestGenerationReady() || !provider.ManagedTestsReady() {
		t.Fatal("complete production provider is not atomically ready")
	}

	cases := map[string]func(*ProductionGenerationConfig){
		"store":             func(c *ProductionGenerationConfig) { c.Base.Store = nil },
		"driver":            func(c *ProductionGenerationConfig) { c.Base.Driver = nil },
		"publisher":         func(c *ProductionGenerationConfig) { c.Base.Publisher = nil },
		"snapshot verifier": func(c *ProductionGenerationConfig) { c.Base.VerifySnapshot = nil },
		"artifact verifier": func(c *ProductionGenerationConfig) { c.Base.VerifyArtifact = nil },
		"process verifier":  func(c *ProductionGenerationConfig) { c.Base.VerifyProcess = nil },
		"current index":     func(c *ProductionGenerationConfig) { c.Managed.CurrentIndex = nil },
		"reviews":           func(c *ProductionGenerationConfig) { c.Managed.Reviews = nil },
		"managed publisher": func(c *ProductionGenerationConfig) { c.Managed.Publisher = nil },
		"validator":         func(c *ProductionGenerationConfig) { c.Managed.Validator = nil },
		"baseline":          func(c *ProductionGenerationConfig) { c.Managed.Baseline = nil },
		"receipts":          func(c *ProductionGenerationConfig) { c.Managed.Receipts = nil },
		"managed driver":    func(c *ProductionGenerationConfig) { c.Managed.Driver = nil },
	}
	for name, remove := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := completeProductionGenerationConfig(t)
			remove(&candidate)
			provider, err := newProductionGenerationBackend(candidate)
			if err == nil || provider != nil {
				if provider != nil {
					provider.Close()
				}
				t.Fatalf("provider=%#v err=%v", provider, err)
			}
		})
	}
}
