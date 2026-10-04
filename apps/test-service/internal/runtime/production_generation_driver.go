package runtime

import (
	"context"
	"reflect"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type productionTargetResolver interface {
	Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error)
	ResolveStart(context.Context, generationv15.TestGenerationStartRequestV15) (generationTarget, error)
	ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error)
}

type productionGenerationValidator interface {
	Ready() bool
	Validate(context.Context, testgendomain.Run, generationTarget, productionPipelineResult) (GenerationStageResult, error)
	Minimize(context.Context, testgendomain.Run, generationTarget, productionPipelineResult) (GenerationStageResult, error)
	ValidateCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) error
	CandidateSet(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, error)
}

type cachedProductionPipeline struct {
	request testgendomain.Request
	result  productionPipelineResult
}

type productionGenerationDriver struct {
	resolver  productionTargetResolver
	pipeline  *productionGenerationPipeline
	validator productionGenerationValidator
	finalizer managedReviewFinalizer
	mu        sync.Mutex
	cache     map[string]cachedProductionPipeline
}

func newProductionGenerationDriver(resolver productionTargetResolver, pipeline *productionGenerationPipeline, validator productionGenerationValidator) *productionGenerationDriver {
	return &productionGenerationDriver{resolver: resolver, pipeline: pipeline, validator: validator, cache: make(map[string]cachedProductionPipeline)}
}

func (driver *productionGenerationDriver) setManagedReviewFinalizer(finalizer managedReviewFinalizer) {
	if driver != nil {
		driver.finalizer = finalizer
	}
}

func (driver *productionGenerationDriver) FinalizeManagedReview(ctx context.Context, run testgendomain.Run) error {
	if driver == nil || driver.finalizer == nil {
		return task.ErrStorageUnavailable
	}
	return driver.finalizer.FinalizeManagedReview(ctx, run)
}

func (driver *productionGenerationDriver) Targets(ctx context.Context, input generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	if driver == nil || driver.resolver == nil || ctx == nil {
		return generationv15.TestGenerationTargetListV15{}, task.ErrStorageUnavailable
	}
	return driver.resolver.Targets(ctx, input)
}

func (driver *productionGenerationDriver) Resolve(ctx context.Context, input generationv15.TestGenerationStartRequestV15) (testgendomain.Request, error) {
	if driver == nil || driver.resolver == nil || ctx == nil {
		return testgendomain.Request{}, task.ErrStorageUnavailable
	}
	target, err := driver.resolver.ResolveStart(ctx, input)
	if err != nil {
		return testgendomain.Request{}, err
	}
	if !target.valid() {
		return testgendomain.Request{}, errProductionGenerationUnavailable
	}
	return target.request, nil
}

func (driver *productionGenerationDriver) StageBudget(run testgendomain.Run, next testgendomain.State) testgencoord.BudgetAmount {
	amount := testgencoord.BudgetAmount{MemoryMiB: 1}
	switch next {
	case testgendomain.StateAnalyzing:
		amount.Processes = 1
		amount.MemoryMiB = min(run.Request.Budgets.MemoryMiB, 256)
		amount.OutputBytes = 16 << 20
	case testgendomain.StateSolving, testgendomain.StateRendering:
		amount.MemoryMiB = min(run.Request.Budgets.MemoryMiB, 256)
		amount.OutputBytes = 512 << 10
	case testgendomain.StateValidating:
		amount.Processes = max(1, run.Request.Budgets.Concurrency)
		amount.Candidates = run.Request.Budgets.CandidateCount
		amount.Artifacts = min(run.Request.Budgets.CandidateCount*2, 1000)
		amount.OutputBytes = min(run.Request.Budgets.CandidateCount*(512<<10), 64<<20)
	case testgendomain.StateMinimizing:
		amount.MemoryMiB = min(run.Request.Budgets.MemoryMiB, 256)
	}
	return amount
}

func (driver *productionGenerationDriver) resolved(ctx context.Context, run testgendomain.Run) (generationTarget, error) {
	if driver == nil || driver.resolver == nil || ctx == nil {
		return generationTarget{}, task.ErrStorageUnavailable
	}
	target, err := driver.resolver.ResolveRequest(ctx, run.Request)
	if err != nil {
		return generationTarget{}, err
	}
	if !target.valid() || !reflect.DeepEqual(target.request, run.Request) {
		return generationTarget{}, testgendomain.ErrStaleSnapshot
	}
	return target, nil
}

func (driver *productionGenerationDriver) generate(ctx context.Context, run testgendomain.Run, target generationTarget, authorize func(context.Context) error) (productionPipelineResult, error) {
	driver.mu.Lock()
	cached, ok := driver.cache[run.ID]
	driver.mu.Unlock()
	if ok && reflect.DeepEqual(cached.request, run.Request) {
		return cached.result, nil
	}
	if driver.pipeline == nil || authorize == nil {
		return productionPipelineResult{}, task.ErrStorageUnavailable
	}
	if err := authorize(ctx); err != nil {
		return productionPipelineResult{}, err
	}
	result, err := driver.pipeline.Generate(ctx, target)
	if err != nil {
		return productionPipelineResult{}, err
	}
	driver.mu.Lock()
	driver.cache[run.ID] = cachedProductionPipeline{request: run.Request, result: result}
	driver.mu.Unlock()
	return result, nil
}

func (driver *productionGenerationDriver) RunStage(ctx context.Context, run testgendomain.Run, next testgendomain.State, authorize func(context.Context) error) (GenerationStageResult, error) {
	if ctx == nil {
		return GenerationStageResult{}, task.ErrInvalidArgument
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		// Direct focused tests may use Background; production coordinator always
		// supplies the request-bound deadline. Bound direct calls defensively.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(run.Request.Budgets.WallTimeMS)*time.Millisecond)
		defer cancel()
	}
	target, err := driver.resolved(ctx, run)
	if err != nil {
		return GenerationStageResult{}, err
	}
	if next == testgendomain.StateBaseline {
		return GenerationStageResult{Next: next}, nil
	}
	result, err := driver.generate(ctx, run, target, authorize)
	if err != nil {
		return GenerationStageResult{}, err
	}
	switch next {
	case testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering:
		return GenerationStageResult{Next: next}, nil
	case testgendomain.StateValidating:
		if driver.validator == nil || !driver.validator.Ready() || authorize == nil {
			return GenerationStageResult{}, task.ErrStorageUnavailable
		}
		if err := authorize(ctx); err != nil {
			return GenerationStageResult{}, err
		}
		return driver.validator.Validate(ctx, run, target, result)
	case testgendomain.StateMinimizing:
		if driver.validator == nil || !driver.validator.Ready() {
			return GenerationStageResult{}, task.ErrStorageUnavailable
		}
		return driver.validator.Minimize(ctx, run, target, result)
	default:
		return GenerationStageResult{}, task.ErrInvalidArgument
	}
}

func (driver *productionGenerationDriver) ValidateCandidate(ctx context.Context, run testgendomain.Run, candidate testgendomain.Candidate) error {
	if driver == nil || driver.validator == nil || !driver.validator.Ready() {
		return task.ErrStorageUnavailable
	}
	return driver.validator.ValidateCandidate(ctx, run, candidate)
}

func (driver *productionGenerationDriver) CandidateSet(ctx context.Context, run testgendomain.Run, candidates []testgendomain.Candidate) (testgenpublish.CandidateSet, error) {
	if driver == nil || driver.validator == nil || !driver.validator.Ready() {
		return testgenpublish.CandidateSet{}, task.ErrStorageUnavailable
	}
	return driver.validator.CandidateSet(ctx, run, candidates)
}

func (driver *productionGenerationDriver) ProjectCandidate(_ context.Context, _ testgendomain.Run, candidate testgendomain.Candidate) (generationv15.TestGenerationCandidateV15, error) {
	if testgendomain.ValidateCandidate(candidate) != nil || len(candidate.Assertions) != 1 {
		return generationv15.TestGenerationCandidateV15{}, task.ErrInvalidArgument
	}
	edits := make([]generationv15.TestGenerationPlannedEditV15, 0, len(candidate.PlannedEdits))
	for _, edit := range candidate.PlannedEdits {
		item := generationv15.TestGenerationPlannedEditV15{Path: edit.Path, Operation: generationv15.Operation(edit.Operation), AfterDigest: edit.AfterDigest}
		if edit.BeforeDigest != "" {
			item.BeforeDigest = &edit.BeforeDigest
		}
		edits = append(edits, item)
	}
	diagnostics := make([]generationv15.TestGenerationDiagnosticV15, 0, len(candidate.Diagnostics))
	for _, diagnostic := range candidate.Diagnostics {
		item := generationv15.TestGenerationDiagnosticV15{Code: generationv15.TestGenerationDiagnosticCodeV15(diagnostic.Code), Severity: generationv15.Severity(diagnostic.Severity)}
		if diagnostic.Reason != "" {
			reason := generationv15.TestGenerationDiagnosticReasonV15(diagnostic.Reason)
			item.Reason = &reason
		}
		diagnostics = append(diagnostics, item)
	}
	return generationv15.TestGenerationCandidateV15{
		CandidateID: candidate.CaseID, Kind: generationv15.TestGenerationCandidateKindV15(candidate.Kind),
		CodeDigest: candidate.CodeDigest, ArtifactDigest: candidate.StagedSourceArtifact.Digest,
		AssertionProvenance: generationv15.TestGenerationAssertionProvenanceV15{Kind: generationv15.Kind(candidate.Assertions[0].Kind), EvidenceDigest: candidate.Assertions[0].EvidenceDigest},
		BaselineCoverage:    generationv15.TestGenerationCoverageV15{FunctionPercent: candidate.BaselineCoverage.FunctionPercent, LinePercent: candidate.BaselineCoverage.LinePercent, BranchPercent: candidate.BaselineCoverage.BranchPercent},
		DeltaCoverage:       generationv15.TestGenerationCoverageV15{FunctionPercent: candidate.DeltaCoveragePercent.FunctionPercent, LinePercent: candidate.DeltaCoveragePercent.LinePercent, BranchPercent: candidate.DeltaCoveragePercent.BranchPercent},
		PlannedEdits:        edits, Diagnostics: diagnostics,
	}, nil
}

func (driver *productionGenerationDriver) managedDelegate() (ManagedRuntimeDriver, bool) {
	if driver == nil || driver.validator == nil {
		return nil, false
	}
	delegate, ok := driver.validator.(ManagedRuntimeDriver)
	return delegate, ok
}

func (driver *productionGenerationDriver) ManagedDriverReady() bool {
	delegate, ok := driver.managedDelegate()
	return ok && driver.validator.Ready() && delegate.ManagedDriverReady()
}

func (driver *productionGenerationDriver) PrepareManaged(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
	delegate, ok := driver.managedDelegate()
	if !ok || !driver.ManagedDriverReady() {
		return testgendomain.Request{}, task.ErrStorageUnavailable
	}
	return delegate.PrepareManaged(ctx, owner, input, target)
}

func (driver *productionGenerationDriver) ManagedCandidateSet(ctx context.Context, run testgendomain.Run, review managedtest.ReviewDraft) (testgenpublish.CandidateSet, error) {
	delegate, ok := driver.managedDelegate()
	if !ok || !driver.ManagedDriverReady() {
		return testgenpublish.CandidateSet{}, task.ErrStorageUnavailable
	}
	return delegate.ManagedCandidateSet(ctx, run, review)
}

var _ GenerationDriver = (*productionGenerationDriver)(nil)
var _ ManagedRuntimeDriver = (*productionGenerationDriver)(nil)
