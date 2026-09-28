package coveragedetail

import (
	"strings"
	"testing"
)

func TestBuildDeltaRequiresCompatibleBaseline(t *testing.T) {
	input := detailInput()
	baseline, err := Build(input)
	if err != nil {
		t.Fatal(err)
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
