package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/protocol"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

// GenerationDriver is product-owned. A deployment must supply an implementation
// that resolves snapshots and executes fixed analysis/validation plans; IPC
// cannot supply a driver, process command, candidate or generated file.
type GenerationDriver interface {
	Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error)
	Resolve(context.Context, generationv15.TestGenerationStartRequestV15) (testgendomain.Request, error)
	StageBudget(testgendomain.Run, testgendomain.State) testgencoord.BudgetAmount
	RunStage(context.Context, testgendomain.Run, testgendomain.State, func(context.Context) error) (GenerationStageResult, error)
	// ValidateCandidate rechecks durable Task 10 execution, oracle and coverage
	// evidence. Metadata digests alone never authorize publication.
	ValidateCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) error
	ProjectCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) (generationv15.TestGenerationCandidateV15, error)
	CandidateSet(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, error)
}

// ManagedValidationDriver is deliberately separate from GenerationDriver:
// v1.5 producers need not implement it, and digest-only candidate validation
// cannot stand in for compiling, running and measuring the exact selected
// post-resolution test/CMake file set.
type ManagedValidationDriver interface {
	ValidateManagedSelection(context.Context, testgenpublish.ManagedSelection) ([]byte, error)
}

// managedReviewFinalizer is an internal post-checkpoint hook. A durable review
// must bind the final awaiting-confirmation revision, so it cannot be safely
// committed by an earlier generation stage.
type managedReviewFinalizer interface {
	FinalizeManagedReview(context.Context, testgendomain.Run) error
}

type managedValidationProvider interface {
	ManagedValidationDriver
	ManagedValidationReady() bool
}

type managedCurrentIndexReader interface {
	CurrentCoverageReady() bool
	ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error)
}

type managedReviewReader interface {
	ManagedReviewsReady() bool
	LookupManagedReviewBinding(context.Context, string, string) (managedtest.ReviewBinding, error)
	GetManagedReview(context.Context, managedtest.ReviewGetQuery) (managedtest.ReviewPage, error)
}

type GenerationStageResult struct {
	Next             testgendomain.State
	Candidates       []testgendomain.Candidate
	Artifacts        []task.Artifact
	MinimizedCaseIDs []string
	PreviewSet       *testgenpublish.CandidateSet
}

type generationPublisher interface {
	Recover(context.Context) error
	Plan(context.Context, testgenpublish.CandidateSet) (testgenpublish.PublishPlan, error)
	Receipt(context.Context, testgenpublish.AcceptRequest) (testgenpublish.Receipt, bool, error)
	Accept(context.Context, testgenpublish.AcceptRequest) (testgenpublish.Receipt, error)
}

type GenerationServiceConfig struct {
	Store            *taskstore.Store
	Driver           GenerationDriver
	Publisher        generationPublisher
	Trusted          bool
	CoverageReady    bool
	VerifySnapshot   testgencoord.SnapshotVerifier
	VerifyArtifact   testgencoord.ArtifactVerifier
	VerifyProcess    testgencoord.ProcessOwnerVerifier
	PublishEvent     func(task.Event)
	CurrentIndex     managedCurrentIndexReader
	ManagedReviews   managedReviewReader
	ManagedValidator managedValidationProvider
}

type generationService struct {
	store              *taskstore.Store
	coord              *testgencoord.Coordinator
	driver             GenerationDriver
	publisher          generationPublisher
	verifyProcess      testgencoord.ProcessOwnerVerifier
	publishEvent       func(task.Event)
	currentIndex       managedCurrentIndexReader
	managedReviews     managedReviewReader
	managedValidator   managedValidationProvider
	mu                 sync.Mutex
	acceptMu           sync.Mutex
	managedCancelGuard func(context.Context, string, testgendomain.Run) (testgendomain.Run, error)
	running            map[string]*generationExecution
	cancelPending      map[string]bool
	wg                 sync.WaitGroup
	closeOnce          sync.Once
}

type generationExecution struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func newGenerationService(config GenerationServiceConfig) (*generationService, error) {
	if !config.Trusted || !config.CoverageReady || config.Store == nil || config.Driver == nil || config.Publisher == nil || config.VerifySnapshot == nil || config.VerifyArtifact == nil || config.VerifyProcess == nil {
		return nil, task.ErrStorageUnavailable
	}
	if err := config.Publisher.Recover(context.Background()); err != nil {
		return nil, err
	}
	return &generationService{
		store: config.Store, coord: testgencoord.NewWithProcessVerifier(config.Store, config.VerifySnapshot, config.VerifyArtifact, config.VerifyProcess),
		driver: config.Driver, publisher: config.Publisher, publishEvent: config.PublishEvent, verifyProcess: config.VerifyProcess,
		currentIndex: config.CurrentIndex, managedReviews: config.ManagedReviews, managedValidator: config.ManagedValidator,
		running: make(map[string]*generationExecution), cancelPending: make(map[string]bool),
	}, nil
}

func (s *generationService) TestGenerationReady() bool { return s != nil }

// No production driver currently supplies a source-attested current-index
// resolver and durable review lookup in addition to selected-output
// validation. Until all three are wired, v1.6 managed methods stay closed.
func (s *generationService) ManagedTestsReady() bool { return false }

// ManagedReadsReady is deliberately not the v1.6 capability gate: publisher
// preimage reread and apply validation are not yet wired as one operation.
func (s *generationService) ManagedReadsReady() bool {
	return s != nil && s.currentIndex != nil && s.currentIndex.CurrentCoverageReady() &&
		s.managedReviews != nil && s.managedReviews.ManagedReviewsReady() &&
		s.managedValidator != nil && s.managedValidator.ManagedValidationReady()
}

// ResolveManagedStart performs no generation work. Report-bound function,
// file, and gap IDs are resolved only against the exact current coverage
// snapshot; unbound target/workspace requests remain unavailable.
func (s *generationService) ResolveManagedStart(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16) (testgendomain.ManagedTarget, error) {
	if !s.ManagedReadsReady() {
		return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validGenerationOwner(owner) {
		return testgendomain.ManagedTarget{}, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return testgendomain.ManagedTarget{}, err
	}
	if input.CoverageReportID == nil {
		return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
	}
	var scope testgendomain.Scope
	var id string
	switch input.Scope {
	case generationv16.Symbol:
		if input.FunctionID == nil {
			return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
		}
		scope, id = testgendomain.ScopeSymbol, *input.FunctionID
	case generationv16.File:
		if input.FileID == nil {
			return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
		}
		scope, id = testgendomain.ScopeFile, *input.FileID
	case generationv16.TestGenerationScopeV16CoverageGap:
		if input.CoverageGapID == nil {
			return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
		}
		scope, id = testgendomain.ScopeCoverageGap, *input.CoverageGapID
	default:
		return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
	}
	index, err := s.currentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: input.ProjectID, ReportID: *input.CoverageReportID, WorkspaceGeneration: input.WorkspaceGeneration})
	if err != nil {
		return testgendomain.ManagedTarget{}, err
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{ProjectID: input.ProjectID, WorkspaceGeneration: input.WorkspaceGeneration, CoverageReportID: *input.CoverageReportID, Scope: scope, ID: id}, index)
	if err != nil {
		return testgendomain.ManagedTarget{}, err
	}
	if err := ctx.Err(); err != nil {
		return testgendomain.ManagedTarget{}, err
	}
	return target, nil
}

func (s *generationService) GetManagedReviewPage(ctx context.Context, owner, reviewID, cursor string, limit int) (generationv16.ManagedReviewV16, error) {
	if !s.ManagedReadsReady() {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validGenerationOwner(owner) || !validGenerationRunID(reviewID) || limit < 1 || limit > protocol.MaxManagedReviewPageItemsV16 || len(cursor) > 4096 {
		return generationv16.ManagedReviewV16{}, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return generationv16.ManagedReviewV16{}, err
	}
	binding, err := s.managedReviews.LookupManagedReviewBinding(ctx, owner, reviewID)
	if err != nil {
		return generationv16.ManagedReviewV16{}, err
	}
	if binding.OwnerDigest != owner || !binding.Valid() {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	index, err := s.currentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: binding.ProjectID, ReportID: binding.ReportID, WorkspaceGeneration: binding.WorkspaceGeneration})
	if err != nil {
		return generationv16.ManagedReviewV16{}, err
	}
	if index.ProjectID != binding.ProjectID || index.ReportID != binding.ReportID || index.WorkspaceGeneration != binding.WorkspaceGeneration || index.Project.Status != coveragedetail.StatusCurrent || len(index.Files) == 0 {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	page, err := s.managedReviews.GetManagedReview(ctx, managedtest.ReviewGetQuery{Binding: binding, ReviewID: reviewID, Cursor: cursor, Limit: limit})
	if err != nil {
		return generationv16.ManagedReviewV16{}, err
	}
	if page.ReviewID != reviewID || !validGenerationDigest(page.ReviewDigest) || page.ReportID != binding.ReportID || page.WorkspaceGeneration != binding.WorkspaceGeneration || len(page.Cases) > limit {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	result := generationv16.ManagedReviewV16{ReviewID: page.ReviewID, ReviewDigest: page.ReviewDigest, WorkspaceGeneration: page.WorkspaceGeneration, CoverageReportID: page.ReportID, Cases: make([]generationv16.ManagedReviewCaseV16, 0, len(page.Cases))}
	for _, c := range page.Cases {
		if len(c.CandidateID) != 36 || c.CandidateID[:4] != "utc_" || !validGenerationHex(c.CandidateID[4:]) || !validGenerationDigest(c.CurrentDigest) || !validGenerationDigest(c.GeneratedDigest) || c.AcceptedDigest != "" && !validGenerationDigest(c.AcceptedDigest) || !managedtest.ValidStatus(c.Status) {
			return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
		}
		accepted, acceptedAbsent, acceptedErr := protocol.EncodeManagedBlockDigestV16(c.AcceptedDigest)
		current, currentAbsent, currentErr := protocol.EncodeManagedBlockDigestV16(c.CurrentDigest)
		generated, generatedAbsent, generatedErr := protocol.EncodeManagedBlockDigestV16(c.GeneratedDigest)
		if acceptedErr != nil || currentErr != nil || generatedErr != nil {
			return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
		}
		wire := generationv16.ManagedReviewCaseV16{CaseID: c.CandidateID, Status: generationv16.ManagedTestStatusV16(c.Status), AcceptedDigest: accepted, CurrentDigest: current, GeneratedDigest: generated}
		if acceptedAbsent {
			wire.AbsentSides = append(wire.AbsentSides, "accepted")
		}
		if currentAbsent {
			wire.AbsentSides = append(wire.AbsentSides, "current")
		}
		if generatedAbsent {
			wire.AbsentSides = append(wire.AbsentSides, "generated")
		}
		result.Cases = append(result.Cases, wire)
	}
	if page.NextCursor != "" {
		result.NextCursor = &page.NextCursor
	}
	if !protocol.ValidManagedReviewPageV16(result) {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	if err := ctx.Err(); err != nil {
		return generationv16.ManagedReviewV16{}, err
	}
	return result, nil
}

func (s *generationService) ListManagedTests(context.Context, managedtest.Query) (managedtest.Page, error) {
	return managedtest.Page{}, task.ErrStorageUnavailable
}
func (s *generationService) GetManagedReview(context.Context, string, string) (managedtest.Review, error) {
	return managedtest.Review{}, task.ErrStorageUnavailable
}
func (s *generationService) ApplyManagedReview(context.Context, managedtest.ApplyRequest) (testgendomain.Run, error) {
	return testgendomain.Run{}, task.ErrStorageUnavailable
}

func (s *generationService) ListTestGenerationTargets(ctx context.Context, owner string, input generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	if s == nil || !validGenerationOwner(owner) || ctx == nil {
		return generationv15.TestGenerationTargetListV15{}, task.ErrInvalidArgument
	}
	return s.driver.Targets(ctx, input)
}

func (s *generationService) StartTestGeneration(ctx context.Context, owner string, input generationv15.TestGenerationStartRequestV15) (generationv15.TestGenerationRunV15, error) {
	if s == nil || !validGenerationOwner(owner) || ctx == nil {
		return generationv15.TestGenerationRunV15{}, task.ErrInvalidArgument
	}
	request, err := s.driver.Resolve(ctx, input)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	// The authenticated realm is authoritative even if a driver accidentally
	// returns a different owner; the client cannot supply this field.
	request.SessionOwnerDigest = owner
	if testgendomain.ValidateRequest(request) != nil || request.IdempotencyKey != input.IdempotencyKey || request.WorkspaceGeneration != input.WorkspaceGeneration || request.ProjectID != input.ProjectID {
		return generationv15.TestGenerationRunV15{}, task.ErrInvalidArgument
	}
	run, err := s.coord.Start(ctx, request)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if run.Request.SessionOwnerDigest != owner {
		return generationv15.TestGenerationRunV15{}, task.ErrNotFound
	}
	s.launch(run.ID)
	return generationRunV15(run), nil
}

func (s *generationService) GetTestGenerationRun(ctx context.Context, owner, runID string) (generationv15.TestGenerationRunV15, error) {
	run, err := s.owned(ctx, owner, runID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	return generationRunV15(run), nil
}

func (s *generationService) CancelTestGeneration(ctx context.Context, owner, runID string) (generationv15.TestGenerationRunV15, error) {
	if s == nil || ctx == nil {
		return generationv15.TestGenerationRunV15{}, task.ErrInvalidArgument
	}
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	run, err := s.owned(ctx, owner, runID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if testgendomain.IsTerminal(run.State) {
		if run.State == testgendomain.StateCancelled && s.attestProcessGone(ctx, run.TaskID, run.Request.ProcessOwnerDigest) != nil {
			return generationv15.TestGenerationRunV15{}, task.ErrConflict
		}
		return generationRunV15(run), nil
	}
	run, err = s.reconcileManagedBeforeCancel(ctx, owner, run)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if testgendomain.IsTerminal(run.State) {
		return generationRunV15(run), nil
	}
	s.mu.Lock()
	s.cancelPending[run.ID] = true
	execution := s.running[run.ID]
	s.mu.Unlock()
	if execution != nil {
		execution.cancel()
		waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
		defer waitCancel()
		select {
		case <-execution.done:
		case <-waitCtx.Done():
			return generationv15.TestGenerationRunV15{}, task.ErrConflict
		}
	}
	if err := s.attestProcessGone(ctx, run.TaskID, run.Request.ProcessOwnerDigest); err != nil {
		return generationv15.TestGenerationRunV15{}, task.ErrConflict
	}
	run, err = s.owned(ctx, owner, runID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	run, err = s.reconcileManagedBeforeCancel(ctx, owner, run)
	if err != nil {
		s.mu.Lock()
		delete(s.cancelPending, runID)
		s.mu.Unlock()
		return generationv15.TestGenerationRunV15{}, err
	}
	if testgendomain.IsTerminal(run.State) {
		s.mu.Lock()
		delete(s.cancelPending, runID)
		s.mu.Unlock()
		return generationRunV15(run), nil
	}
	cancelled, err := s.coord.Cancel(ctx, run.TaskID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	s.mu.Lock()
	delete(s.cancelPending, run.ID)
	s.mu.Unlock()
	s.publishNewEvents(ctx, run, cancelled)
	return generationRunV15(cancelled), nil
}

func (s *generationService) reconcileManagedBeforeCancel(ctx context.Context, owner string, run testgendomain.Run) (testgendomain.Run, error) {
	if run.Request.ManagedSelectionID() == "" || testgendomain.IsTerminal(run.State) {
		return run, nil
	}
	// A managed receipt can outlive the run's accepted checkpoint. Without a
	// healthy recovery hook, cancellation must not make that split permanent.
	if s.managedCancelGuard == nil {
		return testgendomain.Run{}, task.ErrConflict
	}
	recovered, err := s.managedCancelGuard(ctx, owner, run)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if recovered.ID != run.ID || recovered.Request.SessionOwnerDigest != owner || recovered.Revision < run.Revision {
		return testgendomain.Run{}, task.ErrConflict
	}
	return recovered, nil
}

func (s *generationService) attestProcessGone(ctx context.Context, taskID, ownerDigest string) error {
	if s == nil || s.verifyProcess == nil || ctx == nil {
		return task.ErrConflict
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.verifyProcess(verifyCtx, taskID, ownerDigest) }()
	select {
	case err := <-result:
		if err != nil {
			return task.ErrConflict
		}
		return nil
	case <-verifyCtx.Done():
		return task.ErrConflict
	}
}

func (s *generationService) ReplayTestGenerationEvents(ctx context.Context, owner string, input generationv15.TestGenerationEventReplayRequestV15) (generationv15.TestGenerationEventPageV15, error) {
	run, err := s.owned(ctx, owner, input.RunID)
	if err != nil {
		return generationv15.TestGenerationEventPageV15{}, err
	}
	limit := 100
	if input.Limit != nil {
		limit = int(*input.Limit)
	}
	if input.AfterSequence < 0 || input.AfterSequence > run.LastSequence || limit < 1 || limit > 200 {
		return generationv15.TestGenerationEventPageV15{}, task.ErrInvalidArgument
	}
	events, err := s.store.ReplayGenerationEvents(ctx, run.ID, input.AfterSequence, limit)
	if err != nil {
		return generationv15.TestGenerationEventPageV15{}, err
	}
	page := generationv15.TestGenerationEventPageV15{Items: make([]generationv15.TestGenerationProgressEventV15, 0, len(events)), NextAfterSequence: input.AfterSequence}
	for _, event := range events {
		state, projectErr := projectGenerationEvent(run, event)
		if projectErr != nil {
			return generationv15.TestGenerationEventPageV15{}, projectErr
		}
		page.Items = append(page.Items, generationv15.TestGenerationProgressEventV15{Sequence: event.Sequence, State: generationv15.TestGenerationStateV15(state), OccurredAt: event.At})
		page.NextAfterSequence = event.Sequence
	}
	return page, nil
}

func projectGenerationEvent(run testgendomain.Run, event task.Event) (testgendomain.State, error) {
	switch event.Type {
	case task.EventTaskCreated:
		return testgendomain.StateQueued, nil
	case task.EventTaskStarted:
		return testgendomain.StateBaseline, nil
	case task.EventTaskFinished:
		if testgendomain.IsTerminal(run.State) {
			return run.State, nil
		}
	case task.EventTestGenerationStateChanged:
		var value struct {
			RunID string              `json:"runId"`
			From  testgendomain.State `json:"from"`
			To    testgendomain.State `json:"to"`
		}
		if json.Unmarshal(event.Payload, &value) == nil && value.RunID == run.ID && testgendomain.ValidTransition(value.From, value.To) {
			return value.To, nil
		}
	}
	return "", task.ErrConflict
}

func (s *generationService) ListTestGenerationCandidates(ctx context.Context, owner string, input generationv15.TestGenerationCandidateListRequestV15) (generationv15.TestGenerationCandidatePageV15, error) {
	run, err := s.owned(ctx, owner, input.RunID)
	if err != nil {
		return generationv15.TestGenerationCandidatePageV15{}, err
	}
	candidates, err := s.coord.ListCandidates(ctx, run.ID)
	if err != nil {
		return generationv15.TestGenerationCandidatePageV15{}, err
	}
	limit := 100
	if input.Limit != nil {
		limit = int(*input.Limit)
	}
	if limit < 1 || limit > 200 {
		return generationv15.TestGenerationCandidatePageV15{}, task.ErrInvalidArgument
	}
	cursor := ""
	if input.Cursor != nil {
		cursor = *input.Cursor
	}
	page := generationv15.TestGenerationCandidatePageV15{Items: []generationv15.TestGenerationCandidateV15{}}
	for _, candidate := range candidates {
		if candidate.CaseID <= cursor {
			continue
		}
		if len(page.Items) == limit {
			next := page.Items[len(page.Items)-1].CandidateID
			page.NextCursor = &next
			break
		}
		if err := s.driver.ValidateCandidate(ctx, run, candidate); err != nil {
			return generationv15.TestGenerationCandidatePageV15{}, task.ErrConflict
		}
		projected, projectErr := s.driver.ProjectCandidate(ctx, run, candidate)
		if projectErr != nil || !matchesDurableReview(candidate, projected) {
			return generationv15.TestGenerationCandidatePageV15{}, task.ErrStorageUnavailable
		}
		page.Items = append(page.Items, projected)
	}
	return page, nil
}

func matchesDurableReview(candidate testgendomain.Candidate, projected generationv15.TestGenerationCandidateV15) bool {
	if projected.CandidateID != candidate.CaseID || projected.Kind != generationv15.TestGenerationCandidateKindV15(candidate.Kind) ||
		projected.CodeDigest != candidate.CodeDigest || projected.ArtifactDigest != candidate.StagedSourceArtifact.Digest ||
		len(projected.PlannedEdits) != len(candidate.PlannedEdits) || len(projected.Diagnostics) != len(candidate.Diagnostics) ||
		projected.CharacterizationConfirmed ||
		projected.BaselineCoverage.FunctionPercent != candidate.BaselineCoverage.FunctionPercent || projected.BaselineCoverage.LinePercent != candidate.BaselineCoverage.LinePercent || projected.BaselineCoverage.BranchPercent != candidate.BaselineCoverage.BranchPercent ||
		projected.DeltaCoverage.FunctionPercent != candidate.DeltaCoveragePercent.FunctionPercent || projected.DeltaCoverage.LinePercent != candidate.DeltaCoveragePercent.LinePercent || projected.DeltaCoverage.BranchPercent != candidate.DeltaCoveragePercent.BranchPercent {
		return false
	}
	matchedAssertion := false
	for _, assertion := range candidate.Assertions {
		if projected.AssertionProvenance.Kind == generationv15.Kind(assertion.Kind) && projected.AssertionProvenance.EvidenceDigest == assertion.EvidenceDigest {
			matchedAssertion = true
			break
		}
	}
	if !matchedAssertion {
		return false
	}
	for i, edit := range candidate.PlannedEdits {
		got := projected.PlannedEdits[i]
		if got.Path != edit.Path || got.Operation != generationv15.Operation(edit.Operation) || got.AfterDigest != edit.AfterDigest ||
			(edit.BeforeDigest == "" && got.BeforeDigest != nil) || (edit.BeforeDigest != "" && (got.BeforeDigest == nil || *got.BeforeDigest != edit.BeforeDigest)) {
			return false
		}
	}
	for i, diagnostic := range candidate.Diagnostics {
		got := projected.Diagnostics[i]
		if got.Code != generationv15.TestGenerationDiagnosticCodeV15(diagnostic.Code) || got.Severity != generationv15.Severity(diagnostic.Severity) ||
			(diagnostic.Reason == "" && got.Reason != nil) || (diagnostic.Reason != "" && (got.Reason == nil || string(*got.Reason) != diagnostic.Reason)) {
			return false
		}
	}
	return true
}

func (s *generationService) AcceptTestGeneration(ctx context.Context, owner string, input generationv15.TestGenerationAcceptRequestV15) (generationv15.TestGenerationRunV15, error) {
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	run, err := s.owned(ctx, owner, input.RunID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if run.State == testgendomain.StateAccepted {
		if run.Record.Preview == nil || input.ConfirmationDigest != run.Record.Preview.ConfirmationDigest {
			return generationv15.TestGenerationRunV15{}, testgendomain.ErrStaleSnapshot
		}
		if err := s.publisher.Recover(ctx); err != nil {
			return generationv15.TestGenerationRunV15{}, err
		}
		// Accepted is only replayable while Task 12's durable receipt and
		// published files still attest to the committed preview. Never re-plan
		// here: the publisher has already changed the workspace.
		_, published, err := s.publisher.Receipt(ctx, publicationForRun(run))
		if err != nil {
			return generationv15.TestGenerationRunV15{}, err
		}
		if !published {
			return generationv15.TestGenerationRunV15{}, testgenpublish.ErrConflict
		}
		return generationRunV15(run), nil
	}
	if run.State != testgendomain.StateAwaitingConfirmation || run.Record.Preview == nil || run.Record.Preview.Diff == "" || input.ConfirmationDigest != run.Record.Preview.ConfirmationDigest {
		return generationv15.TestGenerationRunV15{}, testgendomain.ErrStaleSnapshot
	}
	if err := s.publisher.Recover(ctx); err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if _, err := s.coord.Resume(ctx, run.TaskID); err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	set, selected, err := s.authoritativeSet(ctx, run, run.Record.MinimizedCaseIDs)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if len(selected) != len(run.Record.MinimizedCaseIDs) || len(selected) == 0 || !slices.Contains(run.Record.MinimizedCaseIDs, input.CandidateID) {
		return generationv15.TestGenerationRunV15{}, task.ErrNotFound
	}
	for _, candidate := range selected {
		if candidate.Kind == testgendomain.KindCharacterization && !input.ConfirmCharacterization {
			return generationv15.TestGenerationRunV15{}, session.ErrCharacterizationConfirmationRequired
		}
	}
	preview := run.Record.Preview
	publication := publicationForRun(run)
	_, published, err := s.publisher.Receipt(ctx, publication)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if !published {
		plan, planErr := s.publisher.Plan(ctx, set)
		if planErr != nil {
			return generationv15.TestGenerationRunV15{}, planErr
		}
		if plan.CandidateSetDigest != preview.CandidateSetDigest || plan.DiffDigest != preview.DiffDigest || plan.Diff != preview.Diff || plan.ConfirmationDigest != preview.ConfirmationDigest || plan.CharacterizationDigest != preview.CharacterizationDigest {
			return generationv15.TestGenerationRunV15{}, testgendomain.ErrStaleSnapshot
		}
	}
	current, err := s.owned(ctx, owner, run.ID)
	if err != nil || current.Revision != run.Revision || current.State != run.State || current.Record.Preview == nil || *current.Record.Preview != *preview {
		return generationv15.TestGenerationRunV15{}, task.ErrConflict
	}
	if _, err := s.publisher.Accept(ctx, publication); err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	// The publisher itself changes generated tests/CMake. A post-publication
	// workspace snapshot recheck would reject our own edits; the durable receipt
	// and CAS over the already-confirmed preview are authoritative here.
	next := testgendomain.CloneRun(run)
	next.State = testgendomain.StateAccepted
	next.Revision++
	now := time.Now().UTC()
	next.FinishedAt = &now
	committed, err := s.store.CheckpointGeneration(ctx, run.Revision, next, nil, nil)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	s.publishNewEvents(ctx, run, committed)
	return generationRunV15(committed), nil
}

func (s *generationService) authoritativeSet(ctx context.Context, run testgendomain.Run, ids []string) (testgenpublish.CandidateSet, []testgendomain.Candidate, error) {
	all, err := s.coord.ListCandidates(ctx, run.ID)
	if err != nil {
		return testgenpublish.CandidateSet{}, nil, err
	}
	selected := make([]testgendomain.Candidate, 0, len(ids))
	for _, candidate := range all {
		if slices.Contains(ids, candidate.CaseID) {
			if err := s.driver.ValidateCandidate(ctx, run, candidate); err != nil {
				return testgenpublish.CandidateSet{}, nil, err
			}
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 || len(selected) != len(ids) {
		return testgenpublish.CandidateSet{}, nil, task.ErrConflict
	}
	set, err := s.driver.CandidateSet(ctx, run, selected)
	if err != nil {
		return testgenpublish.CandidateSet{}, nil, err
	}
	actual := append([]string(nil), set.CaseIDs...)
	sort.Strings(actual)
	if set.RunID != run.ID || set.SnapshotDigest != run.Record.SnapshotDigest || !slices.Equal(actual, ids) {
		return testgenpublish.CandidateSet{}, nil, task.ErrConflict
	}
	return set, selected, nil
}

func publicationForRun(run testgendomain.Run) testgenpublish.AcceptRequest {
	preview := run.Record.Preview
	return testgenpublish.AcceptRequest{
		RunID: run.ID, CandidateSetDigest: preview.CandidateSetDigest,
		SnapshotDigest: run.Record.SnapshotDigest, DiffDigest: preview.DiffDigest,
		ConfirmationDigest: preview.ConfirmationDigest, CharacterizationDigest: preview.CharacterizationDigest,
	}
}

func (s *generationService) owned(ctx context.Context, owner, runID string) (testgendomain.Run, error) {
	if s == nil || ctx == nil || !validGenerationOwner(owner) || !validGenerationRunID(runID) {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	run, err := s.coord.Get(ctx, runID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if run.Request.SessionOwnerDigest == "" || run.Request.SessionOwnerDigest != owner {
		return testgendomain.Run{}, task.ErrNotFound
	}
	return run, nil
}

func validGenerationOwner(value string) bool  { return validGenerationDigest(value) }
func validGenerationRunID(value string) bool  { return len(value) == 32 && validGenerationHex(value) }
func validGenerationDigest(value string) bool { return len(value) == 64 && validGenerationHex(value) }
func validGenerationHex(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func generationRunV15(run testgendomain.Run) generationv15.TestGenerationRunV15 {
	result := generationv15.TestGenerationRunV15{
		RunID: run.ID, TaskID: run.TaskID, ProjectID: run.Request.ProjectID,
		WorkspaceGeneration: run.Request.WorkspaceGeneration, State: generationv15.TestGenerationStateV15(run.State),
		CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt, LastSequence: run.LastSequence,
	}
	if run.State == testgendomain.StateAwaitingConfirmation || testgendomain.IsTerminal(run.State) {
		count := int64(run.CandidateCount)
		result.CandidateCount = &count
	}
	if run.Record.Preview != nil && (run.State == testgendomain.StateAwaitingConfirmation || run.State == testgendomain.StateAccepted) {
		preview := run.Record.Preview
		result.Preview = &generationv15.TestGenerationPreviewV15{
			CandidateSetDigest: preview.CandidateSetDigest,
			DiffDigest:         preview.DiffDigest,
			ConfirmationDigest: preview.ConfirmationDigest,
		}
		if preview.Diff != "" {
			result.Preview.Diff = &preview.Diff
		}
		if preview.CharacterizationDigest != "" {
			result.Preview.CharacterizationDigest = &preview.CharacterizationDigest
		}
	}
	return result
}

func (s *generationService) launch(runID string) {
	s.mu.Lock()
	if _, exists := s.running[runID]; exists || s.cancelPending[runID] {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	execution := &generationExecution{cancel: cancel, done: make(chan struct{})}
	s.running[runID] = execution
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			if s.running[runID] == execution {
				delete(s.running, runID)
			}
			close(execution.done)
			s.mu.Unlock()
		}()
		s.run(ctx, runID)
	}()
}

func (s *generationService) run(ctx context.Context, runID string) {
	for ctx.Err() == nil {
		run, err := s.coord.Get(ctx, runID)
		if err != nil || testgendomain.IsTerminal(run.State) || run.State == testgendomain.StateAwaitingConfirmation {
			return
		}
		next, err := testgencoord.NextStage(run.State)
		if err != nil {
			return
		}
		ledger, err := generationLedger(run)
		if err != nil {
			s.fail(run)
			return
		}
		amount := s.driver.StageBudget(run, next)
		amount.Events = 1
		if next == testgendomain.StateBaseline {
			amount.Events = 2
		}
		reservation, err := ledger.Reserve(ctx, amount)
		if err != nil {
			if ctx.Err() == nil {
				s.fail(run)
			}
			return
		}
		stageCtx, cancel := reservation.StageContext(ctx)
		result, stageErr := s.driver.RunStage(stageCtx, run, next, func(processCtx context.Context) error {
			return s.coord.AuthorizeProcessLaunch(processCtx, runID)
		})
		stageContextErr := stageCtx.Err()
		if stageErr != nil || stageContextErr != nil {
			cancel()
			reservation.Release()
			if ctx.Err() == nil {
				s.fail(run)
			}
			return
		}
		if result.Next == "" {
			result.Next = next
		}
		if !testgendomain.ValidTransition(run.State, result.Next) {
			cancel()
			reservation.Release()
			s.fail(run)
			return
		}
		record := run.Record
		record.MinimizedCaseIDs = append([]string(nil), record.MinimizedCaseIDs...)
		if result.MinimizedCaseIDs != nil {
			record.MinimizedCaseIDs = append([]string(nil), result.MinimizedCaseIDs...)
			sort.Strings(record.MinimizedCaseIDs)
		}
		record.BudgetUsed.Candidates += amount.Candidates
		record.BudgetUsed.OutputBytes += amount.OutputBytes
		record.BudgetUsed.Events += amount.Events
		record.BudgetUsed.Artifacts += amount.Artifacts
		if result.Next == testgendomain.StateAwaitingConfirmation {
			if result.PreviewSet == nil {
				cancel()
				reservation.Release()
				s.fail(run)
				return
			}
			previewRun := testgendomain.CloneRun(run)
			previewRun.Record = record
			set, _, setErr := s.authoritativeSet(stageCtx, previewRun, record.MinimizedCaseIDs)
			if setErr != nil {
				cancel()
				reservation.Release()
				s.fail(run)
				return
			}
			plan, planErr := s.publisher.Plan(stageCtx, set)
			if planErr != nil {
				cancel()
				reservation.Release()
				s.fail(run)
				return
			}
			planDiffHash := sha256.Sum256([]byte(plan.Diff))
			if len(plan.Diff) == 0 || len(plan.Diff) > 262144 || hex.EncodeToString(planDiffHash[:]) != plan.DiffDigest {
				cancel()
				reservation.Release()
				s.fail(run)
				return
			}
			record.Preview = &testgendomain.PreviewIdentity{
				CandidateSetDigest: plan.CandidateSetDigest, DiffDigest: plan.DiffDigest,
				Diff: plan.Diff, ConfirmationDigest: plan.ConfirmationDigest, CharacterizationDigest: plan.CharacterizationDigest,
			}
		}
		cancel()
		if err := reservation.Commit(); err != nil {
			s.fail(run)
			return
		}
		committed, err := s.coord.CheckpointWithRecord(ctx, run.ID, run.Revision, result.Next, result.Candidates, result.Artifacts, record)
		if err != nil {
			// The ledger is reconstructed from the committed record on the next
			// attempt; a failed CAS never carries in-memory usage forward.
			return
		}
		if committed.State == testgendomain.StateAwaitingConfirmation && committed.Request.ManagedSelectionID() != "" {
			if finalizer, ok := s.driver.(managedReviewFinalizer); ok {
				if err := finalizer.FinalizeManagedReview(ctx, committed); err != nil {
					s.fail(committed)
					return
				}
			}
		}
		s.publishNewEvents(ctx, run, committed)
		if committed.State == testgendomain.StateAwaitingConfirmation || testgendomain.IsTerminal(committed.State) {
			return
		}
	}
}

func generationLedger(run testgendomain.Run) (*testgencoord.BudgetLedger, error) {
	used := testgencoord.BudgetAmount{
		Candidates: run.Record.BudgetUsed.Candidates, OutputBytes: run.Record.BudgetUsed.OutputBytes,
		Events: run.Record.BudgetUsed.Events, Artifacts: run.Record.BudgetUsed.Artifacts,
	}
	limits := testgencoord.BudgetLimits{
		WallTime:   time.Duration(run.Request.Budgets.WallTimeMS) * time.Millisecond,
		Candidates: run.Request.Budgets.CandidateCount, MemoryMiB: run.Request.Budgets.MemoryMiB,
		Processes: run.Request.Budgets.Concurrency, OutputBytes: 1 << 30, Events: 10000, Artifacts: 1000,
	}
	return testgencoord.NewBudgetLedgerFromUsage(limits, run.CreatedAt, time.Now, used)
}

func (s *generationService) fail(run testgendomain.Run) {
	s.mu.Lock()
	cancelling := s.cancelPending[run.ID]
	s.mu.Unlock()
	if cancelling {
		return
	}
	if run.State == testgendomain.StateCancelled || testgendomain.IsTerminal(run.State) {
		return
	}
	failed, err := s.coord.Fail(context.Background(), run.TaskID)
	if err == nil {
		s.publishNewEvents(context.Background(), run, failed)
	}
}

func (s *generationService) publishNewEvents(ctx context.Context, before, after testgendomain.Run) {
	if s.publishEvent == nil {
		return
	}
	events, err := s.store.ReplayGenerationEvents(ctx, after.ID, before.LastSequence, 200)
	if err != nil {
		return
	}
	for _, event := range events {
		s.publishEvent(event)
	}
}

func (s *generationService) ResumeAll(ctx context.Context) error {
	if s == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	cursor := ""
	for {
		page, err := s.store.List(ctx, cursor, 200, task.KindTestGeneration)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			run, getErr := s.store.GetGenerationByTask(ctx, item.ID)
			if getErr != nil {
				return getErr
			}
			if testgendomain.IsTerminal(run.State) {
				if err := s.attestProcessGone(ctx, run.TaskID, run.Request.ProcessOwnerDigest); err != nil {
					return task.ErrConflict
				}
				continue
			}
			if run.State == testgendomain.StateAwaitingConfirmation {
				if run.Request.ManagedSelectionID() != "" {
					if finalizer, ok := s.driver.(managedReviewFinalizer); ok {
						if err := finalizer.FinalizeManagedReview(ctx, run); err != nil {
							s.fail(run)
						}
					}
				}
				continue
			}
			if _, resumeErr := s.coord.Resume(ctx, run.TaskID); resumeErr != nil {
				s.fail(run)
				continue
			}
			s.launch(run.ID)
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

func (s *generationService) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		for _, execution := range s.running {
			execution.cancel()
		}
		s.mu.Unlock()
		s.wg.Wait()
		if closer, ok := s.publisher.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
}

var _ session.GenerationBackend = (*generationService)(nil)
