package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

var ErrManagedOutcomeUncertain = errors.New("managed publication outcome uncertain; re-query review")

// ManagedRuntimeDriver is a product-owned, trusted adapter. It cannot be
// supplied by IPC. PrepareManaged cannot persist a run: this backend validates
// the attested source before calling the coordinator's durable Start.
// CandidateSet reconstructs publication inputs from the durable review.
type ManagedRuntimeDriver interface {
	ManagedDriverReady() bool
	PrepareManaged(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (testgendomain.Request, error)
	ManagedCandidateSet(context.Context, testgendomain.Run, managedtest.ReviewDraft) (testgenpublish.CandidateSet, error)
}

type managedReviewStore interface {
	managedReviewReader
	ReadManagedReviewDraft(context.Context, managedtest.ReviewBinding, string, string) (managedtest.ReviewDraft, error)
}

type managedPublisher interface {
	ManagedPublicationReady() bool
	Recover(context.Context) error
	ReadManagedPreimage(context.Context, string) ([]byte, error)
	PlanManaged(context.Context, testgenpublish.CandidateSet, testgenpublish.ManagedDecision) (testgenpublish.Plan, error)
	PublishManaged(context.Context, testgenpublish.Plan) (testgenpublish.Receipt, error)
	ManagedReceipt(context.Context, testgenpublish.ManagedDecision) (testgenpublish.Receipt, bool, error)
	ManagedRunReceipt(context.Context, string) (testgenpublish.Receipt, bool, error)
}

type managedBaselineProvider interface {
	ManagedBaselineReady() bool
	ValidateManagedBaseline(context.Context, coveragedetail.Index, testgendomain.ManagedTarget) error
}

type managedReceiptProvider interface {
	ManagedReceiptReady() bool
	ValidateManagedEvidence(context.Context, testgendomain.Run, managedtest.ReviewDraft) error
}

type ManagedRuntimeConfig struct {
	Base         *generationService
	CurrentIndex managedCurrentIndexReader
	Reviews      managedReviewStore
	Publisher    managedPublisher
	Validator    managedValidationProvider
	Baseline     managedBaselineProvider
	Receipts     managedReceiptProvider
	Driver       ManagedRuntimeDriver
}

// ManagedRuntimeProvider is additive; the default generationService continues
// to report ManagedTestsReady=false and production does not construct this.
type ManagedRuntimeProvider struct {
	*generationService
	config            ManagedRuntimeConfig
	applyMu           sync.Mutex
	checkpointManaged func(context.Context, int64, testgendomain.Run) (testgendomain.Run, error)
}

func newManagedRuntimeProvider(config ManagedRuntimeConfig) *ManagedRuntimeProvider {
	p := &ManagedRuntimeProvider{generationService: config.Base, config: config}
	if config.Base != nil {
		config.Base.acceptMu.Lock()
		config.Base.managedCancelGuard = p.reconcileManagedCancel
		config.Base.acceptMu.Unlock()
	}
	return p
}

// reconcileManagedCancel runs with generationService.acceptMu held. The
// publisher journal is authoritative if its commit succeeded but the run CAS
// failed; uncertainty keeps the run non-terminal for a later replay.
func (p *ManagedRuntimeProvider) reconcileManagedCancel(ctx context.Context, owner string, run testgendomain.Run) (testgendomain.Run, error) {
	if !p.ManagedTestsReady() {
		return testgendomain.Run{}, ErrManagedOutcomeUncertain
	}
	if err := p.config.Publisher.Recover(ctx); err != nil {
		return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, err)
	}
	receipt, found, err := p.config.Publisher.ManagedRunReceipt(ctx, run.ID)
	if err != nil {
		return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, err)
	}
	if !found {
		return run, nil
	}
	recovered, err := p.confirmManagedReceipt(ctx, managedtest.ApplyRequest{Owner: owner, ReviewID: receipt.ManagedReviewID, ReviewDigest: receipt.ManagedReviewDigest}, receipt)
	if err != nil {
		return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, err)
	}
	return recovered, nil
}

func (p *ManagedRuntimeProvider) ManagedTestsReady() bool {
	if p == nil {
		return false
	}
	c := p.config
	return c.Base != nil && c.Base.store != nil && c.Base.coord != nil && c.Base.TestGenerationReady() &&
		c.CurrentIndex != nil && c.CurrentIndex.CurrentCoverageReady() &&
		c.Reviews != nil && c.Reviews.ManagedReviewsReady() && c.Base.store.ManagedTestsReady() &&
		c.Publisher != nil && c.Publisher.ManagedPublicationReady() &&
		c.Validator != nil && c.Validator.ManagedValidationReady() &&
		c.Baseline != nil && c.Baseline.ManagedBaselineReady() &&
		c.Receipts != nil && c.Receipts.ManagedReceiptReady() &&
		c.Driver != nil && c.Driver.ManagedDriverReady()
}

func (p *ManagedRuntimeProvider) ManagedApplyReady() bool { return p.ManagedTestsReady() }

func (p *ManagedRuntimeProvider) readShim() *generationService {
	return &generationService{currentIndex: p.config.CurrentIndex, managedReviews: p.config.Reviews, managedValidator: p.config.Validator}
}

func (p *ManagedRuntimeProvider) ResolveManagedStart(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16) (testgendomain.ManagedTarget, error) {
	if !p.ManagedTestsReady() {
		return testgendomain.ManagedTarget{}, task.ErrStorageUnavailable
	}
	return p.readShim().ResolveManagedStart(ctx, owner, input)
}

func (p *ManagedRuntimeProvider) StartManaged(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16) (generationv16.TestGenerationRunV16, error) {
	target, err := p.ResolveManagedStart(ctx, owner, input)
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	index, err := p.config.CurrentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: input.ProjectID, ReportID: *input.CoverageReportID, WorkspaceGeneration: input.WorkspaceGeneration})
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	if err := p.config.Baseline.ValidateManagedBaseline(ctx, index, target); err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	if err := ctx.Err(); err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	request, err := p.config.Driver.PrepareManaged(ctx, owner, input, target)
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	if request.SourceDigest != target.SourceDigest {
		return generationv16.TestGenerationRunV16{}, testgendomain.ErrStaleSnapshot
	}
	if testgendomain.ValidateRequest(request) != nil || request.SessionOwnerDigest != owner ||
		request.CoverageReportID != *input.CoverageReportID || request.ManagedSelectionID() == "" || request.ManagedSelectionID() != target.SelectionID(request.Scope) ||
		request.IdempotencyKey != input.IdempotencyKey || request.ProjectID != input.ProjectID ||
		request.WorkspaceGeneration != input.WorkspaceGeneration || request.Framework != testgendomain.Framework(input.Framework) ||
		request.Budgets != (testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency}) ||
		request.Goals != (testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent}) {
		return generationv16.TestGenerationRunV16{}, task.ErrConflict
	}
	// Re-attest immediately before persistence; the driver may have spent time
	// preparing, during which the selected source/report could have changed.
	latest, err := p.config.CurrentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: input.ProjectID, ReportID: *input.CoverageReportID, WorkspaceGeneration: input.WorkspaceGeneration})
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	currentTarget, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{ProjectID: input.ProjectID, WorkspaceGeneration: input.WorkspaceGeneration, CoverageReportID: *input.CoverageReportID, Scope: request.Scope, ID: request.ManagedSelectionID()}, latest)
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	if !reflect.DeepEqual(currentTarget, target) || request.SourceDigest != currentTarget.SourceDigest {
		return generationv16.TestGenerationRunV16{}, testgendomain.ErrStaleSnapshot
	}
	if err := ctx.Err(); err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	current, err := p.generationService.coord.Start(ctx, request)
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	count := int64(current.CandidateCount)
	result := generationv16.TestGenerationRunV16{RunID: current.ID, TaskID: current.TaskID, ProjectID: current.Request.ProjectID,
		WorkspaceGeneration: current.Request.WorkspaceGeneration, State: generationv16.TestGenerationStateV16(current.State),
		CreatedAt: current.CreatedAt, FinishedAt: current.FinishedAt, LastSequence: current.LastSequence}
	if current.State == testgendomain.StateAwaitingConfirmation {
		result.CandidateCount = &count
	}
	return result, nil
}

func (p *ManagedRuntimeProvider) GetManagedReviewPage(ctx context.Context, owner, reviewID, cursor string, limit int) (generationv16.ManagedReviewV16, error) {
	if !p.ManagedTestsReady() {
		return generationv16.ManagedReviewV16{}, task.ErrStorageUnavailable
	}
	return p.readShim().GetManagedReviewPage(ctx, owner, reviewID, cursor, limit)
}

func (p *ManagedRuntimeProvider) ListManagedTests(ctx context.Context, query managedtest.Query) (managedtest.Page, error) {
	if !p.ManagedTestsReady() || ctx == nil {
		return managedtest.Page{}, task.ErrStorageUnavailable
	}
	return p.config.Base.store.ManagedTestRegistry().List(ctx, query)
}

func (p *ManagedRuntimeProvider) ListManagedTestRecords(ctx context.Context, owner string, input generationv16.ManagedRecordsRequestV16) (generationv16.ManagedTestRecordPageV16, error) {
	if !p.ManagedTestsReady() {
		return generationv16.ManagedTestRecordPageV16{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validGenerationOwner(owner) || !validGenerationRunID(input.CoverageReportID) ||
		!validGenerationDigest(input.WorkspaceGeneration) || input.FileID != nil && !validGenerationRunID(*input.FileID) ||
		input.Cursor != nil && (len(*input.Cursor) == 0 || len(*input.Cursor) > 4096) ||
		input.Limit != nil && (*input.Limit < 1 || *input.Limit > 200) ||
		input.Status != nil && !managedtest.ValidStatus(managedtest.Status(*input.Status)) {
		return generationv16.ManagedTestRecordPageV16{}, task.ErrInvalidArgument
	}
	index, err := p.config.CurrentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: input.ProjectID, ReportID: input.CoverageReportID, WorkspaceGeneration: input.WorkspaceGeneration})
	if err != nil {
		return generationv16.ManagedTestRecordPageV16{}, err
	}
	if index.ProjectID != input.ProjectID || index.ReportID != input.CoverageReportID || index.WorkspaceGeneration != input.WorkspaceGeneration || index.Project.Status != coveragedetail.StatusCurrent {
		return generationv16.ManagedTestRecordPageV16{}, task.ErrStorageUnavailable
	}
	query := managedtest.Query{ProjectID: input.ProjectID, Limit: 200}
	if input.FileID != nil {
		query.SourceFileID = *input.FileID
	}
	if input.Status != nil {
		query.Status = managedtest.Status(*input.Status)
	}
	if input.Cursor != nil {
		query.Cursor = *input.Cursor
	}
	if input.Limit != nil {
		query.Limit = int(*input.Limit)
	}
	page, err := p.config.Base.store.ManagedTestRegistry().List(ctx, query)
	if err != nil {
		return generationv16.ManagedTestRecordPageV16{}, err
	}
	result := generationv16.ManagedTestRecordPageV16{CoverageReportID: input.CoverageReportID, WorkspaceGeneration: input.WorkspaceGeneration,
		Items: make([]generationv16.ManagedTestRecordV16, 0, len(page.Items))}
	for _, record := range page.Items {
		if !managedtest.ValidRecord(record) || record.ProjectID != input.ProjectID {
			return generationv16.ManagedTestRecordPageV16{}, task.ErrStorageUnavailable
		}
		current, readErr := p.config.Publisher.ReadManagedPreimage(ctx, record.TestRelativePath)
		if readErr != nil {
			return generationv16.ManagedTestRecordPageV16{}, readErr
		}
		if record.Status == managedtest.StatusCurrent {
			matchedSource := false
			for _, file := range index.Files {
				if file.ID == record.SourceFileID && file.RelativePath == record.SourceRelativePath && file.SourceSHA256 == record.SourceDigest && file.Status == coveragedetail.StatusCurrent {
					matchedSource = true
					break
				}
			}
			if !matchedSource {
				return generationv16.ManagedTestRecordPageV16{}, testgendomain.ErrStaleSnapshot
			}
			doc, parseErr := managedtest.ParseDocument(current, 4<<20, 4096)
			if parseErr != nil {
				return generationv16.ManagedTestRecordPageV16{}, testgenpublish.ErrConflict
			}
			found := false
			for _, block := range doc.Blocks {
				if block.CaseID == record.CaseID && block.FunctionID == record.FunctionID && block.Digest == record.AcceptedBlockDigest {
					found = true
					break
				}
			}
			if !found {
				return generationv16.ManagedTestRecordPageV16{}, testgenpublish.ErrConflict
			}
		}
		sum := sha256.Sum256(current)
		result.Items = append(result.Items, generationv16.ManagedTestRecordV16{CaseID: record.CaseID, FileID: record.SourceFileID, FunctionID: record.FunctionID,
			Status: generationv16.ManagedTestStatusV16(record.Status), AcceptedDigest: record.AcceptedBlockDigest, CurrentDigest: hex.EncodeToString(sum[:])})
	}
	if page.NextCursor != "" {
		result.NextCursor = &page.NextCursor
	}
	return result, nil
}

// The v1.6 route exposes paged, bounded review data. This older whole-review
// interface remains closed so raw source bytes are never serialized by it.
func (p *ManagedRuntimeProvider) GetManagedReview(context.Context, string, string) (managedtest.Review, error) {
	return managedtest.Review{}, task.ErrStorageUnavailable
}

func (p *ManagedRuntimeProvider) ApplyManagedReview(ctx context.Context, request managedtest.ApplyRequest) (testgendomain.Run, error) {
	if !p.ManagedTestsReady() {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validGenerationOwner(request.Owner) || !validGenerationRunID(request.ReviewID) || !validGenerationDigest(request.ReviewDigest) || len(request.Resolutions) > 200 {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return testgendomain.Run{}, err
	}
	for key, choice := range request.Resolutions {
		if !managedtest.ValidConflictChoice(choice) || !(strings.HasPrefix(key, "utc_") && len(key) == 36 && validGenerationHex(key[4:]) || strings.HasPrefix(key, "scaffold:") && managedtest.ValidTestPath(strings.TrimPrefix(key, "scaffold:"))) {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
	}
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	// Share the terminal-state gate with v1.5 Accept and Cancel. Keep it
	// through publication and the accepted checkpoint: cancellation must not
	// commit between the final run reread and the publisher journal dispatch.
	p.generationService.acceptMu.Lock()
	defer p.generationService.acceptMu.Unlock()
	decision := testgenpublish.ManagedDecision{ReviewID: request.ReviewID, ReviewDigest: request.ReviewDigest, Resolutions: request.Resolutions}
	if err := p.config.Publisher.Recover(ctx); err != nil {
		return testgendomain.Run{}, err
	}
	if receipt, found, err := p.config.Publisher.ManagedReceipt(ctx, decision); err != nil {
		return testgendomain.Run{}, err
	} else if found {
		return p.confirmManagedReceipt(ctx, request, receipt)
	}
	binding, err := p.config.Reviews.LookupManagedReviewBinding(ctx, request.Owner, request.ReviewID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if binding.OwnerDigest != request.Owner || !binding.Valid() {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	draft, err := p.config.Reviews.ReadManagedReviewDraft(ctx, binding, request.ReviewID, request.ReviewDigest)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if !managedtest.ValidReviewDraft(draft) || draft.Manifest.Binding() != binding || draft.Manifest.Digest() != request.ReviewDigest {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	run, err := p.generationService.owned(ctx, request.Owner, binding.RunID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if run.Revision != binding.RunRevision || run.State != testgendomain.StateAwaitingConfirmation ||
		run.Request.CoverageReportID != binding.ReportID || run.Request.ManagedSelectionID() == "" || run.Request.ProjectID != binding.ProjectID ||
		run.Request.WorkspaceGeneration != binding.WorkspaceGeneration || run.Request.SourceDigest != draft.Manifest.SourceDigest {
		return testgendomain.Run{}, task.ErrConflict
	}
	index, err := p.config.CurrentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: binding.ProjectID, ReportID: binding.ReportID, WorkspaceGeneration: binding.WorkspaceGeneration})
	if err != nil {
		return testgendomain.Run{}, err
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{ProjectID: binding.ProjectID, WorkspaceGeneration: binding.WorkspaceGeneration, CoverageReportID: binding.ReportID, Scope: run.Request.Scope, ID: run.Request.ManagedSelectionID()}, index)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if run.Request.SourceDigest != target.SourceDigest {
		return testgendomain.Run{}, testgendomain.ErrStaleSnapshot
	}
	if err := p.config.Baseline.ValidateManagedBaseline(ctx, index, target); err != nil {
		return testgendomain.Run{}, err
	}
	if err := p.config.Receipts.ValidateManagedEvidence(ctx, run, draft); err != nil {
		return testgendomain.Run{}, err
	}
	for _, candidate := range draft.Candidates {
		current, readErr := p.config.Publisher.ReadManagedPreimage(ctx, candidate.TestRelativePath)
		if readErr != nil || !bytes.Equal(current, candidate.CurrentBytes) {
			return testgendomain.Run{}, testgenpublish.ErrConflict
		}
		if candidate.Status == managedtest.StatusConflicted && !managedtest.ValidConflictChoice(request.Resolutions[candidate.CandidateID]) {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
	}
	set, err := p.config.Driver.ManagedCandidateSet(ctx, run, draft)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if !managedCandidateSetMatchesDraft(set, run, draft) {
		return testgendomain.Run{}, task.ErrConflict
	}
	current, err := p.generationService.owned(ctx, request.Owner, run.ID)
	if err != nil || current.Revision != run.Revision || current.State != run.State {
		return testgendomain.Run{}, task.ErrConflict
	}
	plan, err := p.config.Publisher.PlanManaged(ctx, set, decision)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if plan.RunID != run.ID || plan.SnapshotDigest != run.Record.SnapshotDigest || plan.ManagedReviewID != request.ReviewID || plan.ManagedReviewDigest != request.ReviewDigest {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	if err := ctx.Err(); err != nil {
		return testgendomain.Run{}, err
	}
	// From this point the journal may commit despite client cancellation. Run
	// to a bounded confirmation, never report the canceled RPC as a rollback.
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	receipt, publishErr := p.config.Publisher.PublishManaged(commitCtx, plan)
	if publishErr != nil {
		if recoverErr := p.config.Publisher.Recover(commitCtx); recoverErr != nil {
			return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, publishErr, recoverErr)
		}
		confirmed, found, probeErr := p.config.Publisher.ManagedReceipt(commitCtx, decision)
		if probeErr != nil || !found {
			return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, publishErr, probeErr)
		}
		receipt = confirmed
	}
	return p.confirmManagedReceipt(commitCtx, request, receipt)
}

func managedCandidateSetMatchesDraft(set testgenpublish.CandidateSet, run testgendomain.Run, draft managedtest.ReviewDraft) bool {
	if set.RunID != run.ID || set.SnapshotDigest != run.Record.SnapshotDigest || set.Managed == nil ||
		set.Managed.ReviewID != draft.Manifest.ReviewID || set.Managed.ToolchainID != draft.Manifest.ToolchainID ||
		!validGenerationDigest(set.Managed.ReviewArtifactDigest) || len(set.CaseIDs) != len(draft.Candidates) {
		return false
	}
	ids := append([]string(nil), set.CaseIDs...)
	sort.Strings(ids)
	want := make([]string, len(draft.Candidates))
	generated := make(map[string][]byte)
	for i, candidate := range draft.Candidates {
		want[i] = strings.TrimPrefix(candidate.CandidateID, "utc_")
		if prior, exists := generated[candidate.TestRelativePath]; exists && !bytes.Equal(prior, candidate.GeneratedBytes) {
			return false
		}
		generated[candidate.TestRelativePath] = candidate.GeneratedBytes
	}
	sort.Strings(want)
	if !slices.Equal(ids, want) || len(set.Managed.Inputs) != len(generated) {
		return false
	}
	for _, input := range set.Managed.Inputs {
		wantBytes, exists := generated[input.Generated.Path]
		if !exists || !bytes.Equal(wantBytes, input.Generated.Content) {
			return false
		}
		delete(generated, input.Generated.Path)
	}
	return len(generated) == 0
}

func (p *ManagedRuntimeProvider) confirmManagedReceipt(ctx context.Context, request managedtest.ApplyRequest, receipt testgenpublish.Receipt) (testgendomain.Run, error) {
	if receipt.ManagedReviewID != request.ReviewID || receipt.ManagedReviewDigest != request.ReviewDigest || !validGenerationRunID(receipt.RunID) {
		return testgendomain.Run{}, task.ErrConflict
	}
	run, err := p.generationService.owned(ctx, request.Owner, receipt.RunID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if receipt.SnapshotDigest != run.Record.SnapshotDigest {
		return testgendomain.Run{}, task.ErrConflict
	}
	if run.State == testgendomain.StateAccepted {
		return run, nil
	}
	if run.State != testgendomain.StateAwaitingConfirmation || run.Request.Scope != testgendomain.ScopeCoverageGap {
		return testgendomain.Run{}, task.ErrConflict
	}
	next := testgendomain.CloneRun(run)
	next.State = testgendomain.StateAccepted
	next.Revision++
	now := time.Now().UTC()
	next.FinishedAt = &now
	checkpoint := p.checkpointManaged
	if checkpoint == nil {
		checkpoint = func(ctx context.Context, expected int64, next testgendomain.Run) (testgendomain.Run, error) {
			return p.config.Base.store.CheckpointGeneration(ctx, expected, next, nil, nil)
		}
	}
	committed, err := checkpoint(ctx, run.Revision, next)
	if err != nil {
		return testgendomain.Run{}, errors.Join(ErrManagedOutcomeUncertain, err)
	}
	return committed, nil
}
