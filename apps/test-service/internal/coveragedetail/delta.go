package coveragedetail

import (
	"fmt"
	"reflect"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func applyDelta(current *Index, baseline Index) error {
	if !validHex(baseline.ReportID, 32) || !validHex(baseline.RunID, 32) || baseline.ProjectID != current.ProjectID || baseline.WorkspaceGeneration != current.WorkspaceGeneration || !reflect.DeepEqual(baseline.Toolchain, current.Toolchain) || baseline.Project.Status != StatusCurrent || current.Project.Status != StatusCurrent {
		return fmt.Errorf("%w: incompatible baseline", ErrInvalidDetail)
	}
	beforeFiles := make(map[string]File, len(baseline.Files))
	for _, file := range baseline.Files {
		beforeFiles[file.ID] = file
	}
	for i := range current.Files {
		file := &current.Files[i]
		before, ok := beforeFiles[file.ID]
		if !ok || before.SourceSHA256 != file.SourceSHA256 || before.RelativePath != file.RelativePath || before.Status != StatusCurrent || file.Status != StatusCurrent {
			return fmt.Errorf("%w: incompatible source baseline", ErrInvalidDetail)
		}
		file.Delta = summaryDelta(file.Summary, before.Summary)
		beforeFunctions := make(map[string]Function, len(before.Functions))
		for _, fn := range before.Functions {
			beforeFunctions[fn.ID] = fn
		}
		for j := range file.Functions {
			fn := &file.Functions[j]
			if old, ok := beforeFunctions[fn.ID]; ok {
				fn.Delta = summaryDelta(fn.Summary, old.Summary)
			} else {
				fn.Delta = summaryDelta(fn.Summary, coveragedomain.Summary{})
			}
		}
		delete(beforeFiles, file.ID)
	}
	if len(beforeFiles) > 0 {
		return fmt.Errorf("%w: baseline source set changed", ErrInvalidDetail)
	}
	current.Project.Delta = summaryDelta(current.Project.Summary, baseline.Project.Summary)
	current.Project.BaselineReportID = baseline.ReportID
	return nil
}

func summaryDelta(current, baseline coveragedomain.Summary) DeltaSummary {
	return DeltaSummary{Functions: metricDelta(current.Functions, baseline.Functions), Lines: metricDelta(current.Lines, baseline.Lines), Branches: metricDelta(current.Branches, baseline.Branches)}
}

func metricDelta(current, baseline coveragedomain.Metric) DeltaMetric {
	return DeltaMetric{Covered: current.Covered - baseline.Covered, Total: current.Total - baseline.Total}
}
