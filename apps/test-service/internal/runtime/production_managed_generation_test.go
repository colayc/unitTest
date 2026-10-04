package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

func productionManagedFixture(t *testing.T) (testgendomain.Run, coveragedetail.Index, testgenpublish.CandidateSet, []byte, generationTarget, productionPipelineResult) {
	t.Helper()
	pipeline, target, _, _ := productionPipelineFixture(t)
	target.managed = true
	target.sourceRelativePath = "src/classify.cpp"
	target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
	fileID, err := coveragedetail.StableFileID(target.projectID, target.sourceRelativePath)
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "linkage:_Z8classifyi")
	if err != nil {
		t.Fatal(err)
	}
	gapID, err := coveragedetail.StableGapID(target.coverageReportID, functionID, "branch", coveragedomain.SourceLocation{Line: 4, Column: 7}, 0)
	if err != nil {
		t.Fatal(err)
	}
	target.fileID, target.functionID, target.gapID = fileID, functionID, gapID
	target.request.ManagedGapID = gapID
	target.request.SessionOwnerDigest = strings.Repeat("a", 64)
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	generated, ok := exactGeneratedSource(result.editSet.Files)
	if !ok {
		t.Fatal("missing generated managed source")
	}
	groupID := strings.Repeat("b", 32)
	run := testgendomain.Run{
		ID: groupID, TaskID: strings.Repeat("c", 32), Request: target.request,
		State: testgendomain.StateAwaitingConfirmation, Revision: 8,
		CreatedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), CandidateCount: 1,
		ArtifactDigests: []testgendomain.ArtifactRef{{ID: strings.Repeat("d", 32), Digest: productionBytesDigest(generated)}},
		Record:          testgendomain.NewGenerationRecord(target.request),
	}
	run.Record.MinimizedCaseIDs = []string{groupID}
	index := coveragedetail.Index{
		WorkspaceGeneration: target.workspaceGeneration, ProjectID: target.projectID,
		ReportID: target.coverageReportID, RunID: strings.Repeat("e", 32), ToolchainID: target.toolchainID,
		Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
		Files: []coveragedetail.File{{
			ID: fileID, RelativePath: target.sourceRelativePath, SourceSHA256: target.sourceDigest,
			Status:    coveragedetail.StatusCurrent,
			Functions: []coveragedetail.Function{{ID: functionID, Name: "classify", LinkageName: "_Z8classifyi", Status: coveragedetail.StatusCurrent}},
		}},
		Gaps: []coveragedetail.Gap{{ID: gapID, FileID: fileID, FunctionID: functionID, Kind: "branch", Location: coveragedomain.SourceLocation{Line: 4, Column: 7}}},
	}
	set := testgenpublish.CandidateSet{
		RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: []string{groupID},
		TestTarget: target.renderTarget.TestTarget, ProductionTarget: target.renderTarget.ProductionTarget,
		FrameworkTarget: target.renderTarget.FrameworkTarget, SymbolID: target.gap.SymbolID,
		Files: result.editSet.Files, Diff: result.editSet.Diff,
	}
	return run, index, set, generated, target, result
}

type productionManagedEvidenceFixture struct {
	set     testgenpublish.CandidateSet
	cases   []productionManagedCaseEvidence
	receipt []byte
}

func (fixture productionManagedEvidenceFixture) Ready() bool { return true }
func (fixture productionManagedEvidenceFixture) CandidateSet(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, error) {
	return cloneProductionCandidateSet(fixture.set), nil
}
func (fixture productionManagedEvidenceFixture) ManagedEvidence(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, []productionManagedCaseEvidence, []byte, string, error) {
	return cloneProductionCandidateSet(fixture.set), append([]productionManagedCaseEvidence(nil), fixture.cases...), cloneProductionBytes(fixture.receipt), productionBytesDigest(fixture.receipt), nil
}

type productionManagedCandidateFixture struct{ values []testgendomain.Candidate }

func (fixture productionManagedCandidateFixture) ListGenerationCandidates(context.Context, string) ([]testgendomain.Candidate, error) {
	return append([]testgendomain.Candidate(nil), fixture.values...), nil
}

type productionManagedIndexFixture struct{ index coveragedetail.Index }

func (productionManagedIndexFixture) CurrentCoverageReady() bool { return true }
func (fixture productionManagedIndexFixture) ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	return fixture.index, nil
}

type productionManagedRegistryFixture struct {
	records []managedtest.Record
	blocks  map[string][]byte
}

func (fixture productionManagedRegistryFixture) List(context.Context, managedtest.Query) (managedtest.Page, error) {
	return managedtest.Page{Items: append([]managedtest.Record(nil), fixture.records...)}, nil
}
func (fixture productionManagedRegistryFixture) ReadAcceptedBlock(_ context.Context, caseID string) ([]byte, error) {
	value, ok := fixture.blocks[caseID]
	if !ok {
		return nil, task.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

type productionManagedReviewFixture struct {
	draft managedtest.ReviewDraft
	write int
}

func (*productionManagedReviewFixture) ManagedReviewsReady() bool { return true }
func (fixture *productionManagedReviewFixture) CommitManagedReview(_ context.Context, draft managedtest.ReviewDraft) error {
	fixture.write++
	if fixture.write > 1 {
		return task.ErrConflict
	}
	fixture.draft = draft
	return nil
}
func (fixture *productionManagedReviewFixture) LookupManagedReviewBinding(context.Context, string, string) (managedtest.ReviewBinding, error) {
	return fixture.draft.Manifest.Binding(), nil
}
func (fixture *productionManagedReviewFixture) ReadManagedReviewDraft(context.Context, managedtest.ReviewBinding, string, string) (managedtest.ReviewDraft, error) {
	return fixture.draft, nil
}

func TestProductionManagedMaterializerPersistsReviewIdempotently(t *testing.T) {
	run, index, set, _, _, _ := productionManagedFixture(t)
	candidate := testgendomain.Candidate{CaseID: set.CaseIDs[0], StagedSourceArtifact: run.ArtifactDigests[0]}
	reviews := &productionManagedReviewFixture{}
	materializer := &productionManagedMaterializer{
		candidates: productionManagedCandidateFixture{values: []testgendomain.Candidate{candidate}},
		evidence:   productionManagedEvidenceFixture{set: set}, current: productionManagedIndexFixture{index: index},
		preimages: &managedPublisherFixture{ready: true, preimages: map[string][]byte{set.Files[0].Path: []byte{}}},
		registry:  productionManagedRegistryFixture{}, reviews: reviews,
	}
	if err := materializer.FinalizeManagedReview(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if reviews.write != 1 || !managedtest.ValidReviewDraft(reviews.draft) || reviews.draft.Manifest.RunRevision != run.Revision {
		t.Fatalf("review not persisted: writes=%d draft=%+v", reviews.write, reviews.draft)
	}
	if err := materializer.FinalizeManagedReview(context.Background(), run); err != nil {
		t.Fatalf("idempotent finalization: %v", err)
	}
	if reviews.write != 2 {
		t.Fatalf("idempotent retry did not verify existing review: %d", reviews.write)
	}
}

func TestProductionManagedReviewAcceptsReportBoundFunctionRun(t *testing.T) {
	run, index, set, _, target, _ := productionManagedFixture(t)
	run.Request.Scope = testgendomain.ScopeSymbol
	run.Request.ManagedTargetID = target.functionID
	run.Request.ManagedGapID = ""
	run.Record = testgendomain.NewGenerationRecord(run.Request)
	run.Record.MinimizedCaseIDs = []string{run.ID}
	set.SnapshotDigest = run.Record.SnapshotDigest
	draft, _, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: run.ArtifactDigests[0].ID, Current: []byte{},
	})
	if err != nil || !managedtest.ValidReviewDraft(draft) || draft.Manifest.RunID != run.ID {
		t.Fatalf("function review=%+v err=%v", draft, err)
	}
}

func TestProductionManagedBaselineAcceptsExactFileFunctionAndGapTargets(t *testing.T) {
	_, index, _, _, target, _ := productionManagedFixture(t)
	materializer := &productionManagedMaterializer{current: productionManagedIndexFixture{index: index}}
	gap := testgendomain.ManagedTarget{FileID: target.fileID, FunctionID: target.functionID, GapID: target.gapID, File: target.sourceRelativePath, FunctionName: "classify", SourceDigest: target.sourceDigest}
	function := gap
	function.GapID = ""
	file := function
	file.FunctionID, file.FunctionName = "", ""
	for name, candidate := range map[string]testgendomain.ManagedTarget{"file": file, "function": function, "gap": gap} {
		if err := materializer.ValidateManagedBaseline(context.Background(), index, candidate); err != nil {
			t.Fatalf("%s baseline: %v", name, err)
		}
	}
}

func TestProductionManagedCandidateSetUsesExecutableCaseIDsAndEvidence(t *testing.T) {
	run, index, set, generated, target, result := productionManagedFixture(t)
	candidate := testgendomain.Candidate{CaseID: set.CaseIDs[0], Kind: testgendomain.KindVerified, StagedSourceArtifact: run.ArtifactDigests[0]}
	document, err := managedtest.ParseDocument(generated, int64(len(generated)), 200)
	if err != nil {
		t.Fatal(err)
	}
	cases := make([]productionManagedCaseEvidence, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		for _, vector := range result.vectors {
			caseID, caseErr := managedtest.StableCaseID(index.ProjectID, target.sourceRelativePath, target.functionID, vector.ID)
			if caseErr == nil && caseID == block.CaseID {
				cases = append(cases, productionManagedCaseEvidence{
					CaseID: caseID, FunctionID: target.functionID, ScenarioID: vector.ID, ProjectID: index.ProjectID,
					SourceFileID: target.fileID, SourceRelativePath: target.sourceRelativePath, TestRelativePath: set.Files[0].Path,
					GeneratorVersion: "unit-test-service-v1", Framework: target.framework, ToolchainID: index.ToolchainID, SourceDigest: target.sourceDigest,
				})
			}
		}
	}
	draft, _, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: candidate.StagedSourceArtifact.ID, Current: []byte{},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := []byte(`{"version":1,"validated":true}`)
	materializer := &productionManagedMaterializer{
		candidates: productionManagedCandidateFixture{values: []testgendomain.Candidate{candidate}},
		evidence:   productionManagedEvidenceFixture{set: set, cases: cases, receipt: receipt}, current: productionManagedIndexFixture{index: index},
		preimages: &managedPublisherFixture{ready: true, preimages: map[string][]byte{set.Files[0].Path: []byte{}}},
		registry:  productionManagedRegistryFixture{}, reviews: &productionManagedReviewFixture{},
	}
	managed, err := materializer.ManagedCandidateSet(context.Background(), run, draft)
	if err != nil {
		t.Fatal(err)
	}
	if managed.Managed == nil || len(managed.CaseIDs) != len(document.Blocks) || len(managed.Managed.Records) != len(document.Blocks) ||
		managed.Managed.ValidationReceiptDigest != productionBytesDigest(receipt) || !bytes.Equal(managed.Managed.ValidationReceipt, receipt) {
		t.Fatalf("invalid managed candidate set: %+v", managed)
	}
	for position, record := range managed.Managed.Records {
		if !managedtest.ValidRecord(record) || managed.CaseIDs[position] != strings.TrimPrefix(record.CaseID, "utc_") || record.AcceptedBlockDigest != document.Blocks[position].Digest {
			t.Fatalf("record %d = %+v", position, record)
		}
	}
}

func TestBuildProductionManagedReviewCreatesMaintainableCases(t *testing.T) {
	run, index, set, generated, _, result := productionManagedFixture(t)
	if err := testgendomain.ValidateRun(run); err != nil {
		t.Fatalf("invalid run fixture: %v: %+v", err, run)
	}
	draft, inputs, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: run.ArtifactDigests[0].ID,
		Current: []byte{},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := managedtest.ParseDocument(generated, int64(len(generated)), 200)
	if err != nil {
		t.Fatal(err)
	}
	if !managedtest.ValidReviewDraft(draft) || draft.Manifest.ReviewID != run.ID || draft.Manifest.ToolchainID != index.ToolchainID ||
		draft.Manifest.ArtifactRef != "artifact:"+run.ArtifactDigests[0].ID || len(draft.Candidates) != len(result.vectors) || len(inputs) != 1 {
		t.Fatalf("invalid production managed review: draft=%+v inputs=%d", draft, len(inputs))
	}
	for position, candidate := range draft.Candidates {
		if candidate.CandidateID != doc.Blocks[position].CaseID || candidate.Status != managedtest.StatusCurrent ||
			len(candidate.CurrentBytes) != 0 || !bytes.Equal(candidate.GeneratedBytes, generated) {
			t.Fatalf("candidate %d = %+v", position, candidate)
		}
	}
}

func TestBuildProductionManagedReviewReportsEditedBlockConflict(t *testing.T) {
	run, index, set, generated, target, result := productionManagedFixture(t)
	doc, err := managedtest.ParseDocument(generated, int64(len(generated)), 200)
	if err != nil {
		t.Fatal(err)
	}
	current := bytes.Replace(generated, []byte("// Arrange\n"), []byte("// Arrange - maintained by user\n"), 1)
	accepted := make([]managedtest.Record, 0, len(doc.Blocks))
	acceptedBlocks := make(map[string][]byte, len(doc.Blocks))
	for _, block := range doc.Blocks {
		var scenario string
		for _, vector := range result.vectors {
			candidateID, candidateErr := managedtest.StableCaseID(index.ProjectID, target.sourceRelativePath, target.functionID, vector.ID)
			if candidateErr == nil && candidateID == block.CaseID {
				scenario = vector.ID
				break
			}
		}
		if scenario == "" {
			t.Fatalf("scenario missing for %s", block.CaseID)
		}
		accepted = append(accepted, managedtest.Record{
			CaseID: block.CaseID, ProjectID: index.ProjectID, SourceFileID: target.fileID, FunctionID: target.functionID,
			SourceRelativePath: target.sourceRelativePath, ScenarioID: scenario, TestRelativePath: set.Files[0].Path,
			AcceptedBlockDigest: block.Digest, GeneratorVersion: "unit-test-service-v1", Framework: target.framework,
			ToolchainID: index.ToolchainID, SourceDigest: target.sourceDigest, ValidationReceiptDigest: strings.Repeat("f", 64),
			Status: managedtest.StatusCurrent, LastVerifiedAt: run.CreatedAt,
		})
		acceptedBlocks[block.CaseID] = append([]byte(nil), generated[block.StartByte:block.EndByte]...)
	}
	draft, _, err := buildProductionManagedReview(productionManagedReviewInput{
		Run: run, Index: index, Set: set, SourceArtifactID: run.ArtifactDigests[0].ID,
		Current: current, Accepted: accepted, AcceptedBlocks: acceptedBlocks,
	})
	if err != nil {
		t.Fatal(err)
	}
	conflicts := 0
	for _, candidate := range draft.Candidates {
		if candidate.Status == managedtest.StatusConflicted {
			conflicts++
		}
	}
	if conflicts != 1 || !managedtest.ValidReviewDraft(draft) {
		t.Fatalf("conflicts=%d valid=%v draft=%+v", conflicts, managedtest.ValidReviewDraft(draft), draft)
	}
}
