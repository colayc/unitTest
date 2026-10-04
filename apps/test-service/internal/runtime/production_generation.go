package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/task"
)

// ProductionGenerationConfig binds the existing v1.5 coordinator and v1.6
// managed provider into one readiness unit. It contains no IPC-derived
// process command or native path.
type ProductionGenerationConfig struct {
	Base    GenerationServiceConfig
	Managed ManagedRuntimeConfig
}

func newProductionGenerationBackend(config ProductionGenerationConfig) (*ManagedRuntimeProvider, error) {
	config.Base.CurrentIndex = config.Managed.CurrentIndex
	config.Base.ManagedReviews = config.Managed.Reviews
	config.Base.ManagedValidator = config.Managed.Validator
	base, err := newGenerationService(config.Base)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*ManagedRuntimeProvider, error) {
		base.Close()
		return nil, cause
	}
	if config.Managed.Publisher == nil {
		return fail(task.ErrStorageUnavailable)
	}
	if err := config.Managed.Publisher.Recover(context.Background()); err != nil {
		return fail(err)
	}
	config.Managed.Base = base
	provider := newManagedRuntimeProvider(config.Managed)
	if !provider.TestGenerationReady() || !provider.ManagedTestsReady() {
		return fail(task.ErrStorageUnavailable)
	}
	return provider, nil
}
