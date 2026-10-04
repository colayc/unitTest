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
