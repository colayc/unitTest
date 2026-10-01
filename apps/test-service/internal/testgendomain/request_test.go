package testgendomain

import (
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestManagedGapIdentityPersistsWithoutChangingLegacyGapRequests(t *testing.T) {
	hash := strings.Repeat("a", 64)
	req := Request{IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: hash, ProjectID: "core", Scope: ScopeCoverageGap,
		CoverageReportID: strings.Repeat("2", 32), Framework: FrameworkAuto,
		Goals: Goals{}, Budgets: Budgets{WallTimeMS: 1000, CandidateCount: 1, MemoryMiB: 64, Concurrency: 1},
		CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash, SourceDigest: hash, CMakeTargetDigest: hash,
		FrameworkBundleDigest: hash, AnalyzerBundleDigest: hash, BaselineReportDigest: hash, ProcessOwnerDigest: hash}
	if err := ValidateRequest(req); err != nil {
		t.Fatalf("legacy gap request rejected: %v", err)
	}
	req.ManagedGapID = strings.Repeat("3", 32)
	if err := ValidateRequest(req); err != nil {
		t.Fatalf("bound managed gap rejected: %v", err)
	}
	req.ManagedGapID = "forged"
	if err := ValidateRequest(req); err == nil {
		t.Fatal("malformed managed gap accepted")
	}
	req.Scope = ScopeWorkspace
	req.CoverageReportID = ""
	req.ManagedGapID = strings.Repeat("3", 32)
	if err := ValidateRequest(req); err == nil {
		t.Fatal("managed gap accepted outside report scope")
	}
}

func TestManagedTargetResolvesOnlyExactCurrentIndexIDs(t *testing.T) {
	report := strings.Repeat("a", 32)
	generation := strings.Repeat("b", 64)
	fileID, err := coveragedetail.StableFileID("project", "src/classify.c")
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "classify(int)")
	if err != nil {
		t.Fatal(err)
	}
	gapID, err := coveragedetail.StableGapID(report, functionID, "line", coveragedomain.SourceLocation{Line: 12}, 0)
	if err != nil {
		t.Fatal(err)
	}
	index := coveragedetail.Index{WorkspaceGeneration: generation, ProjectID: "project", ReportID: report,
		Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
		Files: []coveragedetail.File{{ID: fileID, RelativePath: "src/classify.c", SourceSHA256: strings.Repeat("c", 64), Status: coveragedetail.StatusCurrent,
			Functions: []coveragedetail.Function{{ID: functionID, Name: "classify", Status: coveragedetail.StatusCurrent}}}},
		Gaps: []coveragedetail.Gap{{ID: gapID, FileID: fileID, FunctionID: functionID, Kind: "line", Location: coveragedomain.SourceLocation{Line: 12}}},
	}
	for _, tc := range []struct {
		scope Scope
		id    string
	}{{ScopeSymbol, functionID}, {ScopeFile, fileID}, {ScopeCoverageGap, gapID}} {
		got, err := ResolveManagedTarget(ManagedSelector{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: tc.scope, ID: tc.id}, index)
		if err != nil || got.FileID != fileID || got.File != "src/classify.c" || (tc.scope != ScopeFile && got.FunctionID != functionID) {
			t.Fatalf("%s resolution = %#v, %v", tc.scope, got, err)
		}
	}
	for _, bad := range []ManagedSelector{
		{ProjectID: "project", WorkspaceGeneration: strings.Repeat("d", 64), CoverageReportID: report, Scope: ScopeSymbol, ID: functionID},
		{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: strings.Repeat("e", 32), Scope: ScopeSymbol, ID: functionID},
		{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: ScopeSymbol, ID: strings.Repeat("f", 32)},
		{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: ScopeSymbol, ID: functionID, SymbolText: "fn:forged"},
		{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: ScopeFile, ID: fileID, FilePath: "src/forged.c"},
		{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: ScopeCoverageGap, ID: gapID, GapLine: 12},
	} {
		if got, err := ResolveManagedTarget(bad, index); err == nil {
			t.Fatalf("unsafe selector resolved: %#v", got)
		}
	}
	index.Files[0].Status = coveragedetail.StatusStale
	if got, err := ResolveManagedTarget(ManagedSelector{ProjectID: "project", WorkspaceGeneration: generation, CoverageReportID: report, Scope: ScopeSymbol, ID: functionID}, index); err == nil {
		t.Fatalf("stale source resolved: %#v", got)
	}
}

func validRequest() Request {
	return Request{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("a", 64),
		ProjectID: "project", Scope: ScopeSymbol, SymbolID: "fn:classify", Framework: FrameworkAuto,
		Goals:                 Goals{FunctionPercent: 90, LinePercent: 80, BranchPercent: 70},
		Budgets:               Budgets{WallTimeMS: 1000, CandidateCount: 4, MemoryMiB: 256, Concurrency: 1},
		CompileSnapshotDigest: strings.Repeat("b", 64), CoverageSnapshotDigest: strings.Repeat("c", 64),
		SourceDigest: strings.Repeat("1", 64), CMakeTargetDigest: strings.Repeat("2", 64), FrameworkBundleDigest: strings.Repeat("3", 64), AnalyzerBundleDigest: strings.Repeat("4", 64), BaselineReportDigest: strings.Repeat("5", 64), ProcessOwnerDigest: strings.Repeat("d", 64),
	}
}

func TestValidateRequestRejectsInvalidClosedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"scope", func(r *Request) { r.Scope = "all" }},
		{"framework", func(r *Request) { r.Framework = "gtest" }},
		{"missing symbol", func(r *Request) { r.SymbolID = "" }},
		{"extra target", func(r *Request) { r.TargetID = strings.Repeat("d", 64) }},
		{"absolute file", func(r *Request) { r.Scope = ScopeFile; r.SymbolID = ""; r.File = "/tmp/a.c" }},
		{"zero wall time", func(r *Request) { r.Budgets.WallTimeMS = 0 }},
		{"overflow candidates", func(r *Request) { r.Budgets.CandidateCount = 1001 }},
		{"zero concurrency", func(r *Request) { r.Budgets.Concurrency = 0 }},
		{"invalid memory", func(r *Request) { r.Budgets.MemoryMiB = 63 }},
		{"invalid goal", func(r *Request) { r.Goals.BranchPercent = 101 }},
		{"invalid workspace identity", func(r *Request) { r.WorkspaceGeneration = "untrusted" }},
		{"invalid compile snapshot", func(r *Request) { r.CompileSnapshotDigest = "stale" }},
		{"invalid coverage snapshot", func(r *Request) { r.CoverageSnapshotDigest = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.change(&r)
			if ValidateRequest(r) == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	if err := ValidateRequest(validRequest()); err != nil {
		t.Fatalf("valid request: %v", err)
	}
}

func TestRequestSnapshotIdentityMustMatch(t *testing.T) {
	r := validRequest()
	if err := r.SnapshotMatches(r.SnapshotIdentity()); err != nil {
		t.Fatal(err)
	}
	changed := r.SnapshotIdentity()
	changed.CompileSnapshotDigest = strings.Repeat("f", 64)
	if err := r.SnapshotMatches(changed); err == nil {
		t.Fatal("accepted stale compile snapshot")
	}
}

func TestRequestRejectsMalformedPersistedSessionOwner(t *testing.T) {
	r := validRequest()
	r.SessionOwnerDigest = "foreign-session"
	if ValidateRequest(r) == nil {
		t.Fatal("accepted malformed session owner digest")
	}
	r.SessionOwnerDigest = strings.Repeat("e", 64)
	if err := ValidateRequest(r); err != nil {
		t.Fatalf("valid session owner rejected: %v", err)
	}
}
