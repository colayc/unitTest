package coveragerun

import (
	"errors"
	"fmt"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

var ErrReportBuild = errors.New("coverage report build failed")

type ReportInput struct {
	State      State
	RunID      string
	TestRunID  string
	ReportID   string
	ArtifactID string
	CreatedAt  time.Time
	Summary    coveragedomain.Summary
	Toolchain  coveragedomain.ToolchainSnapshot
}

// BuildReport constructs and validates the domain report only after the
// runner has reached a report-bearing terminal outcome. NewReport remains the
// final domain authority for identity, summary, toolchain, and schema rules.
func BuildReport(input ReportInput) (coveragedomain.Report, error) {
	completeness, err := FinalizeCompleteness(input.State)
	if err != nil {
		return coveragedomain.Report{}, fmt.Errorf("%w: completeness: %w", ErrReportBuild, err)
	}
	report, err := coveragedomain.NewReport(coveragedomain.Report{
		ID: input.ReportID, RunID: input.RunID, TestRunID: input.TestRunID,
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: input.CreatedAt,
		Completeness: completeness, Summary: input.Summary, Toolchain: input.Toolchain,
		ArtifactID: input.ArtifactID,
	})
	if err != nil {
		return coveragedomain.Report{}, fmt.Errorf("%w: %w", ErrReportBuild, err)
	}
	return report, nil
}
