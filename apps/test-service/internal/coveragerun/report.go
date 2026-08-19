package coveragerun

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
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
	Sources    []coveragenormalize.SourceBinding
}

// BuildReport constructs and validates the domain report only after the
// runner has reached a report-bearing terminal outcome. NewReport remains the
// final domain authority for identity, summary, toolchain, and schema rules.
func BuildReport(input ReportInput) (coveragedomain.Report, error) {
	completeness, err := FinalizeCompleteness(input.State)
	if err != nil {
		return coveragedomain.Report{}, fmt.Errorf("%w: completeness: %w", ErrReportBuild, err)
	}
	sources, err := publicReportSources(input.Sources)
	if err != nil {
		return coveragedomain.Report{}, fmt.Errorf("%w: sources: %w", ErrReportBuild, err)
	}
	report, err := coveragedomain.NewReport(coveragedomain.Report{
		ID: input.ReportID, RunID: input.RunID, TestRunID: input.TestRunID,
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: input.CreatedAt,
		Completeness: completeness, Summary: input.Summary, Toolchain: input.Toolchain, Sources: sources,
		ArtifactID: input.ArtifactID,
	})
	if err != nil {
		return coveragedomain.Report{}, fmt.Errorf("%w: %w", ErrReportBuild, err)
	}
	return report, nil
}

func publicReportSources(values []coveragenormalize.SourceBinding) ([]coveragedomain.SourceSnapshot, error) {
	if len(values) > 100_000 {
		return nil, errors.New("too many sources")
	}
	result := make([]coveragedomain.SourceSnapshot, len(values))
	for index, source := range values {
		public := source.Public()
		result[index] = coveragedomain.SourceSnapshot{URI: public.URI, SHA256: public.SHA256}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })
	for index := 1; index < len(result); index++ {
		if result[index-1].URI == result[index].URI {
			return nil, errors.New("duplicate source URI")
		}
	}
	return result, nil
}
