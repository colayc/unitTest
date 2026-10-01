package coveragedetail

import (
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestBuildDeltaRequiresCompatibleBaseline(t *testing.T) {
	input := detailInput()
	baseline, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Project.BaselineReportID != "" {
		t.Fatalf("unbased index has provenance %q", baseline.Project.BaselineReportID)
	}
	input.Baseline = &baseline
	input.Report.ID = strings.Repeat("9", 32)
	input.Functions[2].ExecutionCount = 1
	input.Report.Summary.Functions.Covered = 2
	current, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if current.Project.Delta.Functions.Covered != 1 || current.Project.Delta.Functions.Total != 0 {
		t.Fatalf("delta = %#v", current.Project.Delta)
	}
	if current.Project.BaselineReportID != baseline.ReportID {
		t.Fatalf("baseline provenance = %q, want %q", current.Project.BaselineReportID, baseline.ReportID)
	}
	input.Report.Toolchain.Compiler.Version = "16"
	if _, err := Build(input); err == nil {
		t.Fatal("accepted incompatible toolchain baseline")
	}
	input.Report.Toolchain.Compiler.Version = "15"
	input.WorkspaceGeneration = strings.Repeat("8", 64)
	if _, err := Build(input); err == nil {
		t.Fatal("accepted incompatible workspace baseline")
	}
	input.WorkspaceGeneration = baseline.WorkspaceGeneration
	input.Report.ID = "another-report"
	if _, err := Build(input); err == nil {
		t.Fatal("accepted invalid report identity")
	}
}

func TestBuildDeltaRejectsSourceAndCompletenessIncompatibility(t *testing.T) {
	baseInput := detailInput()
	baseline, err := Build(baseInput)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BuildInput){
		"digest changed": func(v *BuildInput) {
			v.Report.Sources[0].SHA256 = strings.Repeat("3", 64)
			v.Sources = append([]coveragedomain.SourceSnapshot(nil), v.Report.Sources...)
		},
		"source set changed": func(v *BuildInput) {
			v.Report.Sources = v.Report.Sources[:1]
			v.Sources = append([]coveragedomain.SourceSnapshot(nil), v.Report.Sources...)
			v.Functions = v.Functions[:2]
			v.Report.Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}, Lines: coveragedomain.Metric{Covered: 1, Total: 2}, Branches: coveragedomain.Metric{Covered: 1, Total: 2}}
		},
		"current incomplete": func(v *BuildInput) { v.Functions[0].IncompleteReason = coveragedomain.ObservationIncompleteLimit },
	} {
		t.Run(name, func(t *testing.T) {
			input := detailInput()
			input.Report.ID = strings.Repeat("9", 32)
			mutate(&input)
			withoutBaseline, err := Build(input)
			if err != nil {
				t.Fatalf("candidate invalid before baseline comparison: %v", err)
			}
			if name != "current incomplete" && withoutBaseline.Project.Status != StatusCurrent {
				t.Fatalf("candidate status = %q, want current", withoutBaseline.Project.Status)
			}
			input.Baseline = &baseline
			if _, err := Build(input); err == nil {
				t.Fatal("accepted incompatible baseline")
			}
		})
	}
	input := detailInput()
	incomplete := baseline
	incomplete.Project.Status = StatusIncomplete
	input.Baseline = &incomplete
	input.Report.ID = strings.Repeat("9", 32)
	if _, err := Build(input); err == nil {
		t.Fatal("accepted incomplete baseline")
	}
}
