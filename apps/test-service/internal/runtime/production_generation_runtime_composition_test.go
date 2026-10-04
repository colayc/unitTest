package runtime

import "testing"

func TestNewProductionGenerationConfigBuildsAtomicManagedProvider(t *testing.T) {
	runtimeValue, store, closeAll := productionCandidateCompositionFixture(t)
	defer closeAll()
	runtimeValue.trustedWorkspace = true
	runtimeValue.coverageBackend = &runtimeCoverageBackend{}

	config, err := NewProductionGenerationConfig(store, runtimeValue)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newProductionGenerationBackend(config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if !provider.TestGenerationReady() || !provider.ManagedTestsReady() {
		t.Fatalf("provider base=%v managed=%v", provider.TestGenerationReady(), provider.ManagedTestsReady())
	}
	if config.Base.Driver == nil || config.Base.Publisher == nil || config.Base.VerifySnapshot == nil || config.Base.VerifyArtifact == nil || config.Base.VerifyProcess == nil ||
		config.Managed.CurrentIndex == nil || config.Managed.Reviews == nil || config.Managed.Publisher == nil || config.Managed.Validator == nil ||
		config.Managed.Baseline == nil || config.Managed.Receipts == nil || config.Managed.Driver == nil {
		t.Fatalf("incomplete config=%+v", config)
	}
}

func TestNewProductionGenerationConfigFailsClosedOutsideReadyRuntime(t *testing.T) {
	tests := map[string]func(*Runtime){
		"untrusted":        func(value *Runtime) { value.trustedWorkspace = false },
		"coverage backend": func(value *Runtime) { value.coverageBackend = nil },
		"coverage detail":  func(value *Runtime) { value.detailFailed.Store(true) },
	}
	for name, remove := range tests {
		t.Run(name, func(t *testing.T) {
			runtimeValue, store, closeAll := productionCandidateCompositionFixture(t)
			defer closeAll()
			runtimeValue.trustedWorkspace = true
			runtimeValue.coverageBackend = &runtimeCoverageBackend{}
			remove(runtimeValue)
			if config, err := NewProductionGenerationConfig(store, runtimeValue); err == nil || config.Base.Driver != nil {
				t.Fatalf("config=%+v error=%v", config, err)
			}
		})
	}
}
