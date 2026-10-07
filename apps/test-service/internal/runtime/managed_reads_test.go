package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/protocol"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type managedReadFixture struct {
	binding managedtest.ReviewBinding
	page    managedtest.ReviewPage
	draft   managedtest.ReviewDraft
	index   coveragedetail.Index
	ready   bool
}

func TestManagedStartResolvesOnlyExactCurrentReportBoundID(t *testing.T) {
	project, report, workspace := "core", strings.Repeat("a", 32), strings.Repeat("b", 64)
	fileID, err := coveragedetail.StableFileID(project, "src/a.c")
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "qualified:1:f:signature:")
	if err != nil {
		t.Fatal(err)
	}
	location := coveragedomain.SourceLocation{Line: 4}
	gapID, err := coveragedetail.StableGapID(report, functionID, "line", location, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &managedReadFixture{ready: true, index: coveragedetail.Index{ProjectID: project, ReportID: report, WorkspaceGeneration: workspace,
		Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
		Files:   []coveragedetail.File{{ID: fileID, RelativePath: "src/a.c", SourceSHA256: strings.Repeat("c", 64), Status: coveragedetail.StatusCurrent, Functions: []coveragedetail.Function{{ID: functionID, Name: "f", Status: coveragedetail.StatusCurrent}}}},
		Gaps:    []coveragedetail.Gap{{ID: gapID, FileID: fileID, FunctionID: functionID, Kind: "line", Location: location}}}}
	service := &generationService{currentIndex: f, managedReviews: f, managedValidator: healthyManagedValidator(true)}
	input := generationv16.TestGenerationStartRequestV16{ProjectID: project, WorkspaceGeneration: workspace, Scope: generationv16.TestGenerationScopeV16CoverageGap, CoverageGapID: &gapID, CoverageReportID: &report}
	resolved, err := service.ResolveManagedStart(context.Background(), strings.Repeat("6", 64), input)
	if err != nil || resolved.FileID != fileID || resolved.FunctionID != functionID || resolved.GapID != gapID {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	input = generationv16.TestGenerationStartRequestV16{ProjectID: project, WorkspaceGeneration: workspace, Scope: generationv16.Symbol, FunctionID: &functionID, CoverageReportID: &report}
	resolved, err = service.ResolveManagedStart(context.Background(), strings.Repeat("6", 64), input)
	if err != nil || resolved.FileID != fileID || resolved.FunctionID != functionID || resolved.GapID != "" {
		t.Fatalf("symbol resolved=%+v err=%v", resolved, err)
	}
	input = generationv16.TestGenerationStartRequestV16{ProjectID: project, WorkspaceGeneration: workspace, Scope: generationv16.File, FileID: &fileID, CoverageReportID: &report}
	resolved, err = service.ResolveManagedStart(context.Background(), strings.Repeat("6", 64), input)
	if err != nil || resolved.FileID != fileID || resolved.FunctionID != "" || resolved.GapID != "" {
		t.Fatalf("file resolved=%+v err=%v", resolved, err)
	}
	unknown := strings.Repeat("f", 32)
	input = generationv16.TestGenerationStartRequestV16{ProjectID: project, WorkspaceGeneration: workspace, Scope: generationv16.TestGenerationScopeV16CoverageGap, CoverageGapID: &unknown, CoverageReportID: &report}
	if _, err := service.ResolveManagedStart(context.Background(), strings.Repeat("6", 64), input); err == nil {
		t.Fatal("unknown gap ID resolved")
	}
	input.Scope = generationv16.Symbol
	input.CoverageGapID, input.CoverageReportID, input.FunctionID = nil, nil, &functionID
	if _, err := service.ResolveManagedStart(context.Background(), strings.Repeat("6", 64), input); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("unbound symbol report=%v", err)
	}
}

func (f *managedReadFixture) ManagedReviewsReady() bool  { return f.ready }
func (f *managedReadFixture) CurrentCoverageReady() bool { return f.ready }
func (f *managedReadFixture) LookupManagedReviewBinding(_ context.Context, owner, _ string) (managedtest.ReviewBinding, error) {
	if owner != f.binding.OwnerDigest {
		return managedtest.ReviewBinding{}, task.ErrNotFound
	}
	return f.binding, nil
}
func (f *managedReadFixture) GetManagedReview(_ context.Context, q managedtest.ReviewGetQuery) (managedtest.ReviewPage, error) {
	if q.Binding != f.binding {
		return managedtest.ReviewPage{}, task.ErrConflict
	}
	return f.page, nil
}
func (f *managedReadFixture) ReadManagedReviewDraft(_ context.Context, binding managedtest.ReviewBinding, reviewID, digest string) (managedtest.ReviewDraft, error) {
	if binding != f.binding || reviewID != f.draft.Manifest.ReviewID || digest != f.draft.Manifest.Digest() {
		return managedtest.ReviewDraft{}, task.ErrConflict
	}
	return f.draft, nil
}
func (f *managedReadFixture) ReadCurrentCoverageIndex(_ context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	if q.ProjectID != f.index.ProjectID || q.ReportID != f.index.ReportID || q.WorkspaceGeneration != f.index.WorkspaceGeneration {
		return coveragedetail.Index{}, task.ErrConflict
	}
	return f.index, nil
}

type healthyManagedValidator bool

func (v healthyManagedValidator) ManagedValidationReady() bool { return bool(v) }
func (v healthyManagedValidator) ValidateManagedSelection(context.Context, testgenpublish.ManagedSelection) ([]byte, error) {
	return nil, task.ErrStorageUnavailable
}

func TestManagedReviewReadRequiresAttestedProvidersAndNeverEnablesApply(t *testing.T) {
	owner := strings.Repeat("6", 64)
	reviewID := strings.Repeat("8", 32)
	reportID := strings.Repeat("a", 32)
	workspace := strings.Repeat("b", 64)
	digest := strings.Repeat("c", 64)
	caseID := "utc_" + strings.Repeat("d", 32)
	f := &managedReadFixture{ready: true, binding: managedtest.ReviewBinding{OwnerDigest: owner, RunID: strings.Repeat("e", 32), RunRevision: 1, ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, ToolchainID: "toolchain"},
		page:  managedtest.ReviewPage{ReviewID: reviewID, ReviewDigest: digest, WorkspaceGeneration: workspace, ReportID: reportID, Cases: []managedtest.ReviewCase{{CandidateID: caseID, Status: managedtest.StatusCurrent, AcceptedDigest: digest, CurrentDigest: digest, GeneratedDigest: digest}}},
		index: coveragedetail.Index{ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}, Files: []coveragedetail.File{{ID: strings.Repeat("f", 32), Status: coveragedetail.StatusCurrent}}}}
	missing := &generationService{managedReviews: f, currentIndex: f}
	if missing.ManagedReadsReady() {
		t.Fatal("missing validator reported read-ready")
	}
	if _, err := missing.GetManagedReviewPage(context.Background(), owner, reviewID, "", 1); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("missing validator read=%v", err)
	}
	service := &generationService{managedReviews: f, currentIndex: f, managedValidator: healthyManagedValidator(true)}
	if !service.ManagedReadsReady() || service.ManagedTestsReady() {
		t.Fatal("read provider readiness must not advertise apply")
	}
	page, err := service.GetManagedReviewPage(context.Background(), owner, reviewID, "", 1)
	if err != nil || len(page.Cases) != 1 || page.Cases[0].CaseID != caseID || page.ReviewDigest != digest {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := service.GetManagedReviewPage(context.Background(), strings.Repeat("7", 64), reviewID, "", 1); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross-owner read=%v", err)
	}
	f.index.WorkspaceGeneration = strings.Repeat("9", 64)
	if _, err := service.GetManagedReviewPage(context.Background(), owner, reviewID, "", 1); err == nil {
		t.Fatal("stale coverage index yielded review")
	}
}

func TestManagedReviewReadRejectsOversizedPageAndCancellation(t *testing.T) {
	owner := strings.Repeat("6", 64)
	reviewID := strings.Repeat("8", 32)
	reportID := strings.Repeat("a", 32)
	workspace := strings.Repeat("b", 64)
	f := &managedReadFixture{ready: true, binding: managedtest.ReviewBinding{OwnerDigest: owner, RunID: strings.Repeat("e", 32), RunRevision: 1, ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, ToolchainID: "toolchain"},
		page:  managedtest.ReviewPage{ReviewID: reviewID, ReviewDigest: strings.Repeat("c", 64), WorkspaceGeneration: workspace, ReportID: reportID, Cases: make([]managedtest.ReviewCase, 33)},
		index: coveragedetail.Index{ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}, Files: []coveragedetail.File{{ID: strings.Repeat("f", 32), Status: coveragedetail.StatusCurrent}}}}
	service := &generationService{managedReviews: f, currentIndex: f, managedValidator: healthyManagedValidator(true)}
	if _, err := service.GetManagedReviewPage(context.Background(), owner, reviewID, "", 32); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("oversized page=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.GetManagedReviewPage(ctx, owner, reviewID, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read=%v", err)
	}
}

func TestManagedReviewReadEncodesAbsentAcceptedDigestWithoutChangingStoredCandidate(t *testing.T) {
	owner, reviewID, reportID := strings.Repeat("6", 64), strings.Repeat("8", 32), strings.Repeat("a", 32)
	workspace, digest := strings.Repeat("b", 64), strings.Repeat("c", 64)
	caseID := "utc_" + strings.Repeat("d", 32)
	f := &managedReadFixture{ready: true,
		binding: managedtest.ReviewBinding{OwnerDigest: owner, RunID: strings.Repeat("e", 32), RunRevision: 1, ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, ToolchainID: "toolchain"},
		page: managedtest.ReviewPage{ReviewID: reviewID, ReviewDigest: digest, WorkspaceGeneration: workspace, ReportID: reportID,
			Cases: []managedtest.ReviewCase{{CandidateID: caseID, Status: managedtest.StatusConflicted, AcceptedDigest: "", CurrentDigest: digest, GeneratedDigest: digest}}},
		index: coveragedetail.Index{ProjectID: "core", WorkspaceGeneration: workspace, ReportID: reportID, Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}, Files: []coveragedetail.File{{ID: strings.Repeat("f", 32), Status: coveragedetail.StatusCurrent}}}}
	service := &generationService{managedReviews: f, currentIndex: f, managedValidator: healthyManagedValidator(true)}
	page, err := service.GetManagedReviewPage(context.Background(), owner, reviewID, "", 1)
	if err != nil || len(page.Cases) != 1 {
		t.Fatalf("first-time review page=%+v err=%v", page, err)
	}
	if page.Cases[0].AcceptedDigest != protocol.AbsentBlockDigestV16 || len(page.Cases[0].AbsentSides) != 1 || page.Cases[0].AbsentSides[0] != "accepted" || f.page.Cases[0].AcceptedDigest != "" {
		t.Fatalf("absent ancestor not round-tripped: wire=%+v internal=%+v", page.Cases[0], f.page.Cases[0])
	}
	f.page.Cases[0].AcceptedDigest = protocol.AbsentBlockDigestV16 // A real empty accepted block is present.
	page, err = service.GetManagedReviewPage(context.Background(), owner, reviewID, "", 1)
	if err != nil || len(page.Cases[0].AbsentSides) != 0 || page.Cases[0].AcceptedDigest != protocol.AbsentBlockDigestV16 {
		t.Fatalf("real empty block mislabelled absent: page=%+v err=%v", page, err)
	}
}
