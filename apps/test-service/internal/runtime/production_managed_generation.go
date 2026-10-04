package runtime

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

var errProductionManagedUnavailable = errors.New("production managed test generation is unavailable")

// productionManagedReviewInput contains only data re-read from trusted service
// stores. Current is the exact rooted workspace preimage; Set is reconstructed
// from Task 5 evidence rather than accepted from the protocol client.
type productionManagedReviewInput struct {
	Run              testgendomain.Run
	Index            coveragedetail.Index
	Set              testgenpublish.CandidateSet
	SourceArtifactID string
	Current          []byte
	Accepted         []managedtest.Record
	AcceptedBlocks   map[string][]byte
}

type productionManagedCandidateReader interface {
	ListGenerationCandidates(context.Context, string) ([]testgendomain.Candidate, error)
}

type productionManagedEvidenceProvider interface {
	Ready() bool
	CandidateSet(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, error)
	ManagedEvidence(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, []productionManagedCaseEvidence, []byte, string, error)
}

type productionManagedIndexReader interface {
	CurrentCoverageReady() bool
	ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error)
}

type productionManagedPreimageReader interface {
	ReadManagedPreimage(context.Context, string) ([]byte, error)
}

type productionManagedRegistry interface {
	List(context.Context, managedtest.Query) (managedtest.Page, error)
	ReadAcceptedBlock(context.Context, string) ([]byte, error)
}

type productionManagedReviewWriter interface {
	ManagedReviewsReady() bool
	CommitManagedReview(context.Context, managedtest.ReviewDraft) error
	LookupManagedReviewBinding(context.Context, string, string) (managedtest.ReviewBinding, error)
	ReadManagedReviewDraft(context.Context, managedtest.ReviewBinding, string, string) (managedtest.ReviewDraft, error)
}

type productionManagedMaterializer struct {
	candidates productionManagedCandidateReader
	evidence   productionManagedEvidenceProvider
	current    productionManagedIndexReader
	preimages  productionManagedPreimageReader
	registry   productionManagedRegistry
	reviews    productionManagedReviewWriter
	prepare    func(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (testgendomain.Request, error)
}

func productionManagedSource(set testgenpublish.CandidateSet) (string, []byte, bool) {
	path := ""
	var content []byte
	for _, file := range set.Files {
		if !strings.HasSuffix(file.Path, "_test.c") && !strings.HasSuffix(file.Path, "_test.cpp") {
			continue
		}
		if path != "" || !managedtest.ValidTestPath(file.Path) || len(file.Content) == 0 || productionBytesDigest(file.Content) != file.AfterDigest {
			return "", nil, false
		}
		path = file.Path
		content = append([]byte(nil), file.Content...)
	}
	return path, content, path != ""
}

func cloneAcceptedBlocks(input map[string][]byte) map[string][]byte {
	if input == nil {
		return nil
	}
	result := make(map[string][]byte, len(input))
	for id, block := range input {
		result[id] = append([]byte(nil), block...)
	}
	return result
}

func cloneProductionBytes(input []byte) []byte {
	result := make([]byte, len(input))
	copy(result, input)
	return result
}

func managedReviewStatus(operation managedtest.Operation) (managedtest.Status, bool) {
	switch operation.Kind {
	case managedtest.OperationAdd, managedtest.OperationUpdate, managedtest.OperationUnchanged:
		return managedtest.StatusCurrent, !operation.Conflict
	case managedtest.OperationConflict:
		return managedtest.StatusConflicted, operation.Conflict
	case managedtest.OperationOrphan:
		return managedtest.StatusOrphaned, operation.Conflict
	default:
		return managedtest.StatusInvalid, false
	}
}

// buildProductionManagedReview turns the exact validated managed source into a
// durable review. It never writes the workspace. The same ReconcileInput is
// retained for the later selected-output publication plan.
func buildProductionManagedReview(input productionManagedReviewInput) (managedtest.ReviewDraft, []managedtest.ReconcileInput, error) {
	run := input.Run
	if testgendomain.ValidateRun(run) != nil || run.State != testgendomain.StateAwaitingConfirmation || run.Request.Scope != testgendomain.ScopeCoverageGap || run.Request.ManagedGapID == "" ||
		input.Set.RunID != run.ID || input.Set.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest || input.Set.Managed != nil || len(input.Set.CaseIDs) != 1 ||
		!validProductionObjectID(input.SourceArtifactID) || input.Index.ToolchainID == "" {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		CoverageReportID: run.Request.CoverageReportID, Scope: run.Request.Scope, ID: run.Request.ManagedGapID,
	}, input.Index)
	if err != nil || target.SourceDigest != run.Request.SourceDigest || target.FunctionID == "" || target.GapID != run.Request.ManagedGapID {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	path, generated, ok := productionManagedSource(input.Set)
	if !ok || !strings.HasPrefix(path, "tests/generated/"+target.File+"_test.") {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	artifactBound := false
	for _, artifact := range run.ArtifactDigests {
		if artifact.ID == input.SourceArtifactID && artifact.Digest == productionBytesDigest(generated) {
			artifactBound = true
			break
		}
	}
	if !artifactBound {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	generatedDocument, err := managedtest.ParseDocument(generated, managedtest.MaxReviewBytes, 200)
	if err != nil || len(generatedDocument.Blocks) == 0 {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	for _, block := range generatedDocument.Blocks {
		if block.FunctionID != target.FunctionID {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
	}
	currentDocument, err := managedtest.ParseDocument(input.Current, managedtest.MaxReviewBytes, 200)
	if err != nil {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	accepted := append([]managedtest.Record(nil), input.Accepted...)
	sort.Slice(accepted, func(left, right int) bool { return accepted[left].CaseID < accepted[right].CaseID })
	for _, record := range accepted {
		if !managedtest.ValidRecord(record) || record.ProjectID != run.Request.ProjectID || record.SourceFileID != target.FileID ||
			record.FunctionID != target.FunctionID || record.SourceRelativePath != target.File || record.TestRelativePath != path ||
			record.ToolchainID != input.Index.ToolchainID || record.SourceDigest != run.Request.SourceDigest || record.Status == managedtest.StatusInvalid {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
	}
	reconcileInput := managedtest.ReconcileInput{
		Accepted: accepted, AcceptedBlocks: cloneAcceptedBlocks(input.AcceptedBlocks), Current: currentDocument,
		Generated: managedtest.ManagedFile{Path: path, Content: cloneProductionBytes(generated)},
	}
	review, err := managedtest.Reconcile(reconcileInput)
	if err != nil || review.Path != path || review.PreimageDigest != productionBytesDigest(input.Current) || review.GeneratedDigest != productionBytesDigest(generated) {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	candidates := make([]managedtest.ReviewCandidate, 0, len(review.Operations))
	currentDigest, generatedDigest := productionBytesDigest(input.Current), productionBytesDigest(generated)
	for _, operation := range review.Operations {
		status, valid := managedReviewStatus(operation)
		if !valid {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
		candidates = append(candidates, managedtest.ReviewCandidate{
			CandidateID: operation.CaseID, TestRelativePath: path, Status: status, AcceptedDigest: operation.AcceptedDigest,
			CurrentDigest: currentDigest, GeneratedDigest: generatedDigest,
			CurrentBytes: cloneProductionBytes(input.Current), GeneratedBytes: cloneProductionBytes(generated),
		})
	}
	if len(candidates) == 0 {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	manifest := managedtest.ReviewManifest{
		ReviewID: run.ID, OwnerDigest: run.Request.SessionOwnerDigest, RunID: run.ID, RunRevision: run.Revision,
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		ReportID: run.Request.CoverageReportID, ToolchainID: input.Index.ToolchainID, SourceDigest: run.Request.SourceDigest,
		ArtifactRef: "artifact:" + input.SourceArtifactID, CreatedAt: run.CreatedAt,
	}
	manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(candidates)
	manifest.CurrentPreimageDigest, manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(candidates)
	draft := managedtest.ReviewDraft{Manifest: manifest, Candidates: candidates}
	if !managedtest.ValidReviewDraft(draft) || !bytes.Equal(reconcileInput.Current.Bytes, input.Current) || !bytes.Equal(reconcileInput.Generated.Content, generated) {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	return draft, []managedtest.ReconcileInput{reconcileInput}, nil
}

func (materializer *productionManagedMaterializer) ready() bool {
	return materializer != nil && materializer.candidates != nil && materializer.evidence != nil && materializer.evidence.Ready() &&
		materializer.current != nil && materializer.current.CurrentCoverageReady() && materializer.preimages != nil &&
		materializer.registry != nil && materializer.reviews != nil && materializer.reviews.ManagedReviewsReady()
}

func (materializer *productionManagedMaterializer) listAccepted(ctx context.Context, projectID, fileID string) ([]managedtest.Record, error) {
	accepted := []managedtest.Record{}
	cursor := ""
	for {
		page, err := materializer.registry.List(ctx, managedtest.Query{ProjectID: projectID, SourceFileID: fileID, Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		accepted = append(accepted, page.Items...)
		if len(accepted) > 200 {
			return nil, errProductionManagedUnavailable
		}
		if page.NextCursor == "" {
			return accepted, nil
		}
		if page.NextCursor == cursor {
			return nil, errProductionManagedUnavailable
		}
		cursor = page.NextCursor
	}
}

func (materializer *productionManagedMaterializer) acceptedAncestors(ctx context.Context, current []byte, records []managedtest.Record) (map[string][]byte, error) {
	document, err := managedtest.ParseDocument(current, managedtest.MaxReviewBytes, 200)
	if err != nil {
		return nil, err
	}
	blocks := make(map[string]managedtest.Block, len(document.Blocks))
	for _, block := range document.Blocks {
		blocks[block.CaseID] = block
	}
	result := make(map[string][]byte, len(records))
	for _, record := range records {
		if block, ok := blocks[record.CaseID]; ok && block.FunctionID == record.FunctionID && block.Digest == record.AcceptedBlockDigest {
			result[record.CaseID] = cloneProductionBytes(document.Bytes[block.StartByte:block.EndByte])
			continue
		}
		ancestor, err := materializer.registry.ReadAcceptedBlock(ctx, record.CaseID)
		if err != nil {
			return nil, err
		}
		result[record.CaseID] = ancestor
	}
	return result, nil
}

// FinalizeManagedReview runs only after the awaiting-confirmation checkpoint.
// Replaying it after a crash verifies and accepts the exact existing draft.
func (materializer *productionManagedMaterializer) FinalizeManagedReview(ctx context.Context, run testgendomain.Run) error {
	if ctx == nil || !materializer.ready() || testgendomain.ValidateRun(run) != nil || run.State != testgendomain.StateAwaitingConfirmation || run.Request.ManagedGapID == "" {
		return errProductionManagedUnavailable
	}
	candidates, err := materializer.candidates.ListGenerationCandidates(ctx, run.ID)
	if err != nil || len(candidates) != 1 {
		return errProductionManagedUnavailable
	}
	set, err := materializer.evidence.CandidateSet(ctx, run, candidates)
	if err != nil {
		return err
	}
	path, _, ok := productionManagedSource(set)
	if !ok {
		return errProductionManagedUnavailable
	}
	index, err := materializer.current.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{
		ProjectID: run.Request.ProjectID, ReportID: run.Request.CoverageReportID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
	})
	if err != nil {
		return err
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		CoverageReportID: run.Request.CoverageReportID, Scope: run.Request.Scope, ID: run.Request.ManagedGapID,
	}, index)
	if err != nil {
		return err
	}
	current, err := materializer.preimages.ReadManagedPreimage(ctx, path)
	if err != nil {
		return err
	}
	accepted, err := materializer.listAccepted(ctx, run.Request.ProjectID, target.FileID)
	if err != nil {
		return err
	}
	ancestors, err := materializer.acceptedAncestors(ctx, current, accepted)
	if err != nil {
		return err
	}
	draft, _, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: candidates[0].StagedSourceArtifact.ID,
		Current: current, Accepted: accepted, AcceptedBlocks: ancestors,
	})
	if err != nil {
		return err
	}
	if err := materializer.reviews.CommitManagedReview(ctx, draft); err == nil {
		return nil
	} else if !errors.Is(err, task.ErrConflict) {
		return err
	}
	binding, err := materializer.reviews.LookupManagedReviewBinding(ctx, run.Request.SessionOwnerDigest, draft.Manifest.ReviewID)
	if err != nil || binding != draft.Manifest.Binding() {
		return task.ErrConflict
	}
	existing, err := materializer.reviews.ReadManagedReviewDraft(ctx, binding, draft.Manifest.ReviewID, draft.Manifest.Digest())
	if err != nil || !reflect.DeepEqual(existing, draft) {
		return task.ErrConflict
	}
	return nil
}

func (materializer *productionManagedMaterializer) ManagedDriverReady() bool {
	return materializer.ready() && materializer.prepare != nil
}

func (materializer *productionManagedMaterializer) PrepareManaged(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
	if !materializer.ManagedDriverReady() || ctx == nil {
		return testgendomain.Request{}, task.ErrStorageUnavailable
	}
	request, err := materializer.prepare(ctx, owner, input, target)
	if err != nil {
		return testgendomain.Request{}, err
	}
	if testgendomain.ValidateRequest(request) != nil || request.SessionOwnerDigest != owner || request.SourceDigest != target.SourceDigest || request.ManagedGapID != target.GapID {
		return testgendomain.Request{}, errProductionManagedUnavailable
	}
	return request, nil
}

func managedCMakePath(set testgenpublish.CandidateSet) (string, bool) {
	result := ""
	for _, file := range set.Files {
		if strings.HasSuffix(file.Path, "CMakeLists.txt") {
			if result != "" {
				return "", false
			}
			result = file.Path
		}
	}
	return result, result != ""
}

func (materializer *productionManagedMaterializer) ManagedCandidateSet(ctx context.Context, run testgendomain.Run, draft managedtest.ReviewDraft) (testgenpublish.CandidateSet, error) {
	if ctx == nil || !materializer.ready() || !managedtest.ValidReviewDraft(draft) || draft.Manifest.RunID != run.ID || draft.Manifest.RunRevision != run.Revision {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	candidates, err := materializer.candidates.ListGenerationCandidates(ctx, run.ID)
	if err != nil || len(candidates) != 1 {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	set, cases, receipt, receiptDigest, err := materializer.evidence.ManagedEvidence(ctx, run, candidates)
	if err != nil || len(cases) == 0 || len(receipt) == 0 || productionBytesDigest(receipt) != receiptDigest {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	path, generated, ok := productionManagedSource(set)
	if !ok {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	index, err := materializer.current.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{
		ProjectID: run.Request.ProjectID, ReportID: run.Request.CoverageReportID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
	})
	if err != nil || index.ToolchainID != draft.Manifest.ToolchainID {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		CoverageReportID: run.Request.CoverageReportID, Scope: run.Request.Scope, ID: run.Request.ManagedGapID,
	}, index)
	if err != nil {
		return testgenpublish.CandidateSet{}, err
	}
	current, err := materializer.preimages.ReadManagedPreimage(ctx, path)
	if err != nil {
		return testgenpublish.CandidateSet{}, err
	}
	accepted, err := materializer.listAccepted(ctx, run.Request.ProjectID, target.FileID)
	if err != nil {
		return testgenpublish.CandidateSet{}, err
	}
	ancestors, err := materializer.acceptedAncestors(ctx, current, accepted)
	if err != nil {
		return testgenpublish.CandidateSet{}, err
	}
	rebuilt, inputs, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: candidates[0].StagedSourceArtifact.ID,
		Current: current, Accepted: accepted, AcceptedBlocks: ancestors,
	})
	if err != nil || !reflect.DeepEqual(rebuilt, draft) || len(inputs) != 1 {
		return testgenpublish.CandidateSet{}, task.ErrConflict
	}
	document, err := managedtest.ParseDocument(generated, managedtest.MaxReviewBytes, 200)
	if err != nil || len(document.Blocks) != len(cases) {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	evidenceByID := make(map[string]productionManagedCaseEvidence, len(cases))
	for _, item := range cases {
		evidenceByID[item.CaseID] = item
	}
	records := make([]managedtest.Record, 0, len(document.Blocks))
	caseIDs := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		item, exists := evidenceByID[block.CaseID]
		if !exists || item.FunctionID != block.FunctionID || item.SourceFileID != target.FileID || item.SourceDigest != target.SourceDigest || item.ToolchainID != index.ToolchainID {
			return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
		}
		record := managedtest.Record{
			CaseID: item.CaseID, ProjectID: item.ProjectID, SourceFileID: item.SourceFileID, FunctionID: item.FunctionID,
			SourceRelativePath: item.SourceRelativePath, ScenarioID: item.ScenarioID, TestRelativePath: item.TestRelativePath,
			AcceptedBlockDigest: block.Digest, GeneratorVersion: item.GeneratorVersion, Framework: item.Framework,
			ToolchainID: item.ToolchainID, SourceDigest: item.SourceDigest, ValidationReceiptDigest: receiptDigest,
			Status: managedtest.StatusCurrent, LastVerifiedAt: run.CreatedAt,
		}
		if !managedtest.ValidRecord(record) {
			return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
		}
		records = append(records, record)
		caseIDs = append(caseIDs, strings.TrimPrefix(block.CaseID, "utc_"))
	}
	cmake, ok := managedCMakePath(set)
	if !ok {
		return testgenpublish.CandidateSet{}, errProductionManagedUnavailable
	}
	review, err := managedtest.Reconcile(inputs[0])
	if err != nil {
		return testgenpublish.CandidateSet{}, err
	}
	set.CaseIDs = caseIDs
	set.CharacterizationIDs = nil
	if candidates[0].Kind == testgendomain.KindCharacterization {
		set.CharacterizationIDs = append([]string(nil), caseIDs...)
	}
	set.Managed = &testgenpublish.ManagedCandidateSet{
		ReviewID: draft.Manifest.ReviewID, ReviewArtifactDigest: review.Digest(), ToolchainID: index.ToolchainID,
		ValidationReceipt: cloneProductionBytes(receipt), ValidationReceiptDigest: receiptDigest,
		CMakePath: cmake, Inputs: inputs, Records: records,
	}
	return set, nil
}

func (materializer *productionManagedMaterializer) ManagedBaselineReady() bool {
	return materializer != nil && materializer.current != nil && materializer.current.CurrentCoverageReady()
}

func (materializer *productionManagedMaterializer) ValidateManagedBaseline(_ context.Context, index coveragedetail.Index, target testgendomain.ManagedTarget) error {
	resolved, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: index.ProjectID, WorkspaceGeneration: index.WorkspaceGeneration, CoverageReportID: index.ReportID,
		Scope: testgendomain.ScopeCoverageGap, ID: target.GapID,
	}, index)
	if err != nil || !reflect.DeepEqual(resolved, target) || index.ToolchainID == "" {
		return testgendomain.ErrStaleSnapshot
	}
	return nil
}

func (materializer *productionManagedMaterializer) ManagedReceiptReady() bool {
	return materializer.ready()
}

func (materializer *productionManagedMaterializer) ValidateManagedEvidence(ctx context.Context, run testgendomain.Run, draft managedtest.ReviewDraft) error {
	_, err := materializer.ManagedCandidateSet(ctx, run, draft)
	return err
}

var _ ManagedRuntimeDriver = (*productionManagedMaterializer)(nil)
var _ managedBaselineProvider = (*productionManagedMaterializer)(nil)
var _ managedReceiptProvider = (*productionManagedMaterializer)(nil)

type productionManagedSelectionValidator struct {
	validator *testgenvalidate.SelectedValidator
}

func (adapter productionManagedSelectionValidator) ManagedValidationReady() bool {
	return adapter.validator != nil
}

func (adapter productionManagedSelectionValidator) ValidateManagedSelection(ctx context.Context, selection testgenpublish.ManagedSelection) ([]byte, error) {
	if adapter.validator == nil || ctx == nil {
		return nil, task.ErrStorageUnavailable
	}
	return adapter.validator.Validate(ctx, selection)
}

var _ managedValidationProvider = productionManagedSelectionValidator{}
