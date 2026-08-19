package session

import (
	"fmt"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragev14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/coverage"
	testv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/test"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func toProtocolCoverageRun(value coveragedomain.Run) (coveragev14.CoverageRun, error) {
	validated, err := coveragedomain.NewRun(value)
	if err != nil {
		return coveragev14.CoverageRun{}, fmt.Errorf("invalid coverage run projection: %w", err)
	}
	result := coveragev14.CoverageRun{
		CoverageRunID:       validated.ID,
		TaskID:              validated.TaskID,
		TestRunID:           validated.TestRunID,
		WorkspaceGeneration: validated.Request.WorkspaceGeneration,
		ProjectID:           validated.Request.ProjectID,
		CoverageProfileID:   validated.Request.CoverageProfileID,
		CatalogRevision:     validated.Request.CatalogRevision,
		SelectionSnapshot:   toProtocolCoverageSelection(validated.SelectionSnapshot),
		RepeatCount:         validated.Request.RepeatCount,
		TimeoutMS:           validated.Request.Timeout.Milliseconds(),
		Status:              coveragev14.CoverageRunStatusV14(validated.Status),
		CreatedAt:           validated.CreatedAt,
		StartedAt:           validated.StartedAt,
		FinishedAt:          validated.FinishedAt,
		LastSequence:        validated.LastSequence,
	}
	if validated.Outcome != "" {
		outcome := coveragev14.CoverageRunOutcomeV14(validated.Outcome)
		result.Outcome = &outcome
	}
	if validated.Reason != "" {
		reason := coveragev14.CoverageRunReasonV14(validated.Reason)
		result.Reason = &reason
	}
	if validated.ReportID != "" {
		reportID := validated.ReportID
		result.ReportID = &reportID
	}
	return result, nil
}

func toProtocolCoverageRunPage(value coveragedomain.RunPage) (coveragev14.CoverageRunPage, error) {
	result := coveragev14.CoverageRunPage{Items: make([]coveragev14.CoverageRun, len(value.Items))}
	for index, run := range value.Items {
		projected, err := toProtocolCoverageRun(run)
		if err != nil {
			return coveragev14.CoverageRunPage{}, err
		}
		result.Items[index] = projected
	}
	if value.NextCursor != "" {
		cursor := value.NextCursor
		result.NextCursor = &cursor
	}
	return result, nil
}

func toProtocolCoverageReport(value coveragedomain.Report) (coveragev14.CoverageReport, error) {
	validated, err := coveragedomain.NewReport(value)
	if err != nil {
		return coveragev14.CoverageReport{}, fmt.Errorf("invalid coverage report projection: %w", err)
	}
	return coveragev14.CoverageReport{
		ReportID:       validated.ID,
		CoverageRunID:  validated.RunID,
		TestRunID:      validated.TestRunID,
		SchemaVersion:  coveragev14.CoverageSchemaVersionV14(validated.SchemaVersion),
		CreatedAt:      validated.CreatedAt,
		Completeness:   toProtocolCoverageCompleteness(validated.Completeness),
		Summary:        toProtocolCoverageSummary(validated.Summary),
		ToolProvenance: toProtocolCoverageToolchain(validated.Toolchain),
		ArtifactID:     validated.ArtifactID,
	}, nil
}

func toProtocolCoverageSelection(value testdomain.SelectionSnapshot) testv14.TestSelectionSnapshotV14 {
	return testv14.TestSelectionSnapshotV14{
		Mode:         testv14.TestSelectionModeV14(value.Mode),
		ContainerIDS: append([]string(nil), idsToStrings(value.ContainerIDs)...),
		ItemIDS:      append([]string(nil), idsToStrings(value.ItemIDs)...),
	}
}

func idsToStrings(values []testdomain.ID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func toProtocolCoverageCompleteness(value coveragedomain.Completeness) coveragev14.CoverageCompletenessV14 {
	reasons := make([]coveragev14.CoverageIncompleteReasonV14, len(value.Reasons))
	for index, reason := range value.Reasons {
		reasons[index] = coveragev14.CoverageIncompleteReasonV14(reason)
	}
	return coveragev14.CoverageCompletenessV14{
		Outcome: coveragev14.CoverageCompletenessOutcomeV14(value.Outcome),
		Reasons: reasons,
	}
}

func toProtocolCoverageSummary(value coveragedomain.Summary) coveragev14.CoverageSummaryV14 {
	return coveragev14.CoverageSummaryV14{
		Lines:     toProtocolCoverageMetric(value.Lines),
		Branches:  toProtocolCoverageMetric(value.Branches),
		Functions: toProtocolCoverageMetric(value.Functions),
	}
}

func toProtocolCoverageMetric(value coveragedomain.Metric) coveragev14.CoverageMetricV14 {
	return coveragev14.CoverageMetricV14{Covered: value.Covered, Total: value.Total}
}

func toProtocolCoverageToolchain(value coveragedomain.ToolchainSnapshot) coveragev14.CoverageToolProvenanceV14 {
	return coveragev14.CoverageToolProvenanceV14{
		Platform:                   coveragev14.CoveragePlatformV14(value.Platform),
		Architecture:               coveragev14.CoverageArchitectureV14(value.Architecture),
		Compiler:                   coveragev14.CoverageCompilerV14{Family: coveragev14.CoverageCompilerFamilyV14(value.Compiler.Family), Version: value.Compiler.Version},
		Driver:                     coveragev14.CoverageDriverV14{Name: coveragev14.CoverageDriverNameV14(value.Driver.Name), Version: value.Driver.Version},
		Collector:                  coveragev14.CoverageCollectorV14{Name: coveragev14.CoverageCollectorNameV14(value.Collector.Name), Version: value.Collector.Version},
		NormalizerVersion:          value.NormalizerVersion,
		InstrumentationFingerprint: value.InstrumentationFingerprint,
	}
}
