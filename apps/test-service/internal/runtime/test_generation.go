package runtime

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
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
	Store          *taskstore.Store
	Driver         GenerationDriver
	Publisher      generationPublisher
	Trusted        bool
	CoverageReady  bool
	VerifySnapshot testgencoord.SnapshotVerifier
	VerifyArtifact testgencoord.ArtifactVerifier
	VerifyProcess  testgencoord.ProcessOwnerVerifier
	PublishEvent   func(task.Event)
}

type generationService struct {
	store        *taskstore.Store
	coord        *testgencoord.Coordinator
	driver       GenerationDriver
	publisher    generationPublisher
	publishEvent func(task.Event)
	mu           sync.Mutex
	acceptMu     sync.Mutex
	running      map[string]context.CancelFunc
	wg           sync.WaitGroup
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
		driver: config.Driver, publisher: config.Publisher, publishEvent: config.PublishEvent,
		running: make(map[string]context.CancelFunc),
	}, nil
}

func (s *generationService) TestGenerationReady() bool { return s != nil }

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
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	run, err := s.owned(ctx, owner, runID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	if testgendomain.IsTerminal(run.State) {
		return generationRunV15(run), nil
	}
	s.mu.Lock()
	stop := s.running[run.ID]
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	cancelled, err := s.coord.Cancel(ctx, run.TaskID)
	if err != nil {
		return generationv15.TestGenerationRunV15{}, err
	}
	s.publishNewEvents(ctx, run, cancelled)
	return generationRunV15(cancelled), nil
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
		len(projected.PlannedEdits) != len(candidate.PlannedEdits) {
		return false
	}
	for i, edit := range candidate.PlannedEdits {
		got := projected.PlannedEdits[i]
		if got.Path != edit.Path || got.Operation != generationv15.Operation(edit.Operation) || got.AfterDigest != edit.AfterDigest ||
			(edit.BeforeDigest == "" && got.BeforeDigest != nil) || (edit.BeforeDigest != "" && (got.BeforeDigest == nil || *got.BeforeDigest != edit.BeforeDigest)) {
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
	if run.State != testgendomain.StateAwaitingConfirmation || run.Record.Preview == nil || input.ConfirmationDigest != run.Record.Preview.ConfirmationDigest {
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
		if plan.CandidateSetDigest != preview.CandidateSetDigest || plan.DiffDigest != preview.DiffDigest || plan.ConfirmationDigest != preview.ConfirmationDigest || plan.CharacterizationDigest != preview.CharacterizationDigest {
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
		if preview.CharacterizationDigest != "" {
			result.Preview.CharacterizationDigest = &preview.CharacterizationDigest
		}
	}
	return result
}

func (s *generationService) launch(runID string) {
	s.mu.Lock()
	if _, exists := s.running[runID]; exists {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running[runID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.running, runID); s.mu.Unlock() }()
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
			s.fail(run)
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
			s.fail(run)
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
			record.Preview = &testgendomain.PreviewIdentity{
				CandidateSetDigest: plan.CandidateSetDigest, DiffDigest: plan.DiffDigest,
				ConfirmationDigest: plan.ConfirmationDigest, CharacterizationDigest: plan.CharacterizationDigest,
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
	if run.State == testgendomain.StateCancelled || testgendomain.IsTerminal(run.State) {
		return
	}
	_, _ = s.coord.Fail(context.Background(), run.TaskID)
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
			if testgendomain.IsTerminal(run.State) || run.State == testgendomain.StateAwaitingConfirmation {
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
	s.mu.Lock()
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

var _ session.GenerationBackend = (*generationService)(nil)
