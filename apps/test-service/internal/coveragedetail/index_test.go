package coveragedetail

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func detailReport() coveragedomain.Report {
	return coveragedomain.Report{ID: strings.Repeat("a", 32), RunID: strings.Repeat("b", 32), TestRunID: strings.Repeat("c", 32), SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: time.Now(), Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, Toolchain: coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15"}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15"}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"}, NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("d", 64)}, ArtifactID: strings.Repeat("e", 32), Sources: []coveragedomain.SourceSnapshot{{URI: "src/a.cpp", SHA256: strings.Repeat("1", 64)}, {URI: "src/b.cpp", SHA256: strings.Repeat("2", 64)}}}
}

func detailInput() BuildInput {
	report := detailReport()
	report.Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 2}, Lines: coveragedomain.Metric{Covered: 1, Total: 3}, Branches: coveragedomain.Metric{Covered: 1, Total: 2}}
	return BuildInput{WorkspaceGeneration: strings.Repeat("f", 64), ProjectID: "demo", Report: report, Sources: report.Sources, Functions: []coveragedomain.FunctionObservation{
		{QualifiedName: "ns::foo", LinkageName: "_ZN2ns3fooEv", File: "src/a.cpp", Start: coveragedomain.SourceLocation{Line: 3, Column: 1}, End: coveragedomain.SourceLocation{Line: 9, Column: 1}, ExecutionCount: 1, Lines: []coveragedomain.LineObservation{{Line: 4, Count: 1}, {Line: 5, Count: 0}}, Branches: []coveragedomain.BranchObservation{{Line: 4, Column: 5, Ordinal: 0, HasOrdinal: true, Count: 1}, {Line: 4, Column: 5, Ordinal: 1, HasOrdinal: true, Count: 0}}},
		{QualifiedName: "ns::foo", LinkageName: "_ZN2ns3fooEv", File: "src/a.cpp", Start: coveragedomain.SourceLocation{Line: 30, Column: 1}, End: coveragedomain.SourceLocation{Line: 39, Column: 1}, ExecutionCount: 1, InstantiationOrdinal: 1, Lines: []coveragedomain.LineObservation{{Line: 4, Count: 1}, {Line: 5, Count: 0}}},
		{QualifiedName: "bar", LinkageName: "_Z3barv", File: "src/b.cpp", ExecutionCount: 0, Lines: []coveragedomain.LineObservation{{Line: 2, Count: 0}}},
	}}
}

func TestBuildMissingDetailCannotClaimExactPercent(t *testing.T) {
	input := detailInput()
	input.Functions = nil
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.Status != StatusIncomplete {
		t.Fatalf("status = %q, want incomplete", got.Project.Status)
	}
	if _, ok := got.Project.Percent("lines"); ok {
		t.Fatal("missing detail produced exact percentage")
	}
}

func TestBuildCrossFunctionLocationCollisionIsIncomplete(t *testing.T) {
	input := detailInput()
	input.Functions[2].File = "src/a.cpp"
	input.Functions[2].Lines[0].Line = 4
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[0].Status != StatusIncomplete || len(got.Files[0].Functions) != 2 || got.Files[0].Functions[0].Status != StatusIncomplete || got.Files[0].Functions[1].Status != StatusIncomplete {
		t.Fatalf("collision attribution = %#v", got.Files[0])
	}
	if got.Files[0].Summary.Lines.Total != 0 {
		t.Fatalf("collision counted exact line: %#v", got.Files[0].Summary)
	}
}

func TestBuildDuplicateSemanticIdentityIsOrderIndependent(t *testing.T) {
	input := detailInput()
	input.Functions[1].QualifiedName = "alternate spelling"
	input.Functions[1].Start = input.Functions[0].Start
	input.Functions[1].End = coveragedomain.SourceLocation{Line: 8, Column: 2}
	first, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Functions[0], input.Functions[1] = input.Functions[1], input.Functions[0]
	second, err := Build(input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("order changed identity/navigation: %v", err)
	}
}

func TestBuildQualifiedDisplayNameIsOrderIndependent(t *testing.T) {
	input := detailInput()
	input.Functions[0].QualifiedName = ""
	input.Functions[1].QualifiedName = "zz::foo"
	first, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Functions[0], input.Functions[1] = input.Functions[1], input.Functions[0]
	second, err := Build(input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("display name changed with emission order: %v", err)
	}
	if first.Files[0].Functions[0].Name != "zz::foo" {
		t.Fatalf("display name = %q", first.Files[0].Functions[0].Name)
	}
}

func TestBuildDedupesFunctionsAndLocationsAndAggregatesUniqueFiles(t *testing.T) {
	input := detailInput()
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Project.Summary != (coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 2}, Lines: coveragedomain.Metric{Covered: 1, Total: 3}, Branches: coveragedomain.Metric{Covered: 1, Total: 2}}) {
		t.Fatalf("index = %#v", got)
	}
	if len(got.Files[0].Functions) != 1 || len(got.Files[0].Lines) != 2 || len(got.Files[0].Functions[0].Branches) != 2 {
		t.Fatalf("file a = %#v", got.Files[0])
	}
	if got.Files[0].Functions[0].ID == "" || got.Files[0].Functions[0].Name != "ns::foo" {
		t.Fatalf("function = %#v", got.Files[0].Functions[0])
	}
	if len(got.Gaps) != 3 {
		t.Fatalf("gaps = %#v", got.Gaps)
	}
	input.Functions[0], input.Functions[2] = input.Functions[2], input.Functions[0]
	reordered, err := Build(input)
	if err != nil || !reflect.DeepEqual(got, reordered) {
		t.Fatalf("order changed index: %v\n%#v\n%#v", err, got, reordered)
	}
}

func TestBuildAmbiguousEvidenceIsIncompleteAndExcludedFromExactCounts(t *testing.T) {
	input := detailInput()
	input.Functions[0].IncompleteReason = coveragedomain.ObservationIncompleteAttributionAmbiguous
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.Status != StatusIncomplete || got.Files[0].Status != StatusIncomplete || got.Files[0].Functions[0].Status != StatusIncomplete {
		t.Fatalf("incomplete not propagated: %#v", got)
	}
	if got.Files[0].Functions[0].Summary != (coveragedomain.Summary{}) {
		t.Fatalf("ambiguous metrics became exact: %#v", got.Files[0].Functions[0].Summary)
	}
	if _, ok := got.Project.Percent("functions"); ok {
		t.Fatal("incomplete project has exact percent")
	}
}

func TestBuildRejectsUnknownIncompleteReason(t *testing.T) {
	input := detailInput()
	input.Functions[0].IncompleteReason = "invented_reason"
	if _, err := Build(input); err == nil {
		t.Fatal("accepted unrecognized incomplete reason")
	}
}

func TestBuildRejectsUnsafeNavigationCoordinates(t *testing.T) {
	input := detailInput()
	input.Functions[0].Start.Line = -1
	if _, err := Build(input); err == nil {
		t.Fatal("accepted negative source range")
	}
	input = detailInput()
	input.Functions[0].End.Line = coveragedomain.MaxSafeInteger + 1
	if _, err := Build(input); err == nil {
		t.Fatal("accepted unsafe source range")
	}
}

func TestBuildPartialReportSuppressesAllExactPercentages(t *testing.T) {
	input := detailInput()
	input.Report.Completeness = coveragedomain.Completeness{Outcome: coveragedomain.OutcomePartial, Reasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestTimedOut}}
	got, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.Status != StatusIncomplete || got.Files[0].Status != StatusIncomplete || got.Files[0].Functions[0].Status != StatusIncomplete {
		t.Fatalf("partial report status = %#v", got)
	}
	if _, ok := got.Files[0].Functions[0].Percent("lines"); ok {
		t.Fatal("partial function has exact percent")
	}
}
