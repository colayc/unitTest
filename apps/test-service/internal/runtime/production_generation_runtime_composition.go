package runtime

import (
	"os"
	"path/filepath"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

// NewProductionGenerationConfig composes generation, exact selected-output
// validation and managed publication as one fail-closed runtime capability.
// None of the authorities below may be supplied by an IPC client.
func NewProductionGenerationConfig(store *taskstore.Store, runtimeValue *Runtime) (ProductionGenerationConfig, error) {
	if store == nil || runtimeValue == nil || !runtimeValue.trustedWorkspace || runtimeValue.coverageBackend == nil || !runtimeValue.CurrentCoverageReady() {
		return ProductionGenerationConfig{}, task.ErrStorageUnavailable
	}

	composition, err := newProductionGenerationCandidateComposition(store, runtimeValue)
	if err != nil {
		return ProductionGenerationConfig{}, task.ErrStorageUnavailable
	}

	journalRoot := filepath.Join(runtimeValue.controlDataRoot, "test-generation-publisher")
	if !filepath.IsAbs(journalRoot) || filepath.Clean(journalRoot) != journalRoot || os.MkdirAll(journalRoot, 0o700) != nil ||
		os.Chmod(journalRoot, 0o700) != nil || validateDirectDirectory(journalRoot) != nil {
		return ProductionGenerationConfig{}, task.ErrStorageUnavailable
	}
	publisher, err := testgenpublish.New(runtimeValue.workspaceRoot.NativePath, journalRoot, composition.snapshots.VerifyDigest)
	if err != nil {
		return ProductionGenerationConfig{}, task.ErrStorageUnavailable
	}
	fail := func() (ProductionGenerationConfig, error) {
		_ = publisher.Close()
		return ProductionGenerationConfig{}, task.ErrStorageUnavailable
	}

	registry := store.ManagedTestRegistry()
	publisher.ManagedRegistry = registry
	publisher.ManagedSelectionValidator = composition.selected.ValidateManagedSelection
	materializer := &productionManagedMaterializer{
		candidates: store,
		evidence:   composition.validation,
		current:    runtimeValue,
		preimages:  publisher,
		registry:   registry,
		reviews:    store,
	}
	if err := composition.driver.bindManagedMaterializer(materializer); err != nil || !materializer.ready() {
		return fail()
	}

	return ProductionGenerationConfig{
		Base: GenerationServiceConfig{
			Store:          store,
			Driver:         composition.driver,
			Publisher:      publisher,
			Trusted:        true,
			CoverageReady:  true,
			VerifySnapshot: composition.snapshots.VerifyRequest,
			VerifyArtifact: composition.artifacts.Verify,
			VerifyProcess:  composition.processes.Verify,
		},
		Managed: ManagedRuntimeConfig{
			CurrentIndex: runtimeValue,
			Reviews:      store,
			Publisher:    publisher,
			Validator:    composition.selected,
			Baseline:     materializer,
			Receipts:     materializer,
			Driver:       materializer,
		},
	}, nil
}
