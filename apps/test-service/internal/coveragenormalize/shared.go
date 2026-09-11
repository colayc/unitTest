package coveragenormalize

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

var ErrInvalidCoverageMetric = errors.New("invalid normalized coverage metric")

// canonicalWorkspaceRoot and workspaceRelativeSource are shared source-evidence
// guards. Tool-specific parsers can choose their path model before reaching
// these helpers, but no normalizer bypasses source binding verification.
func canonicalWorkspaceRoot(value string) (string, error) {
	if !validPathString(value) || !filepath.IsAbs(value) {
		return "", ErrInvalidSourcePath
	}
	root, err := filepath.Abs(filepath.Clean(value))
	if err != nil || !filepath.IsAbs(root) {
		return "", ErrInvalidSourcePath
	}
	return root, nil
}

func workspaceRelativeSource(root, value string) (string, string, error) {
	if !validPathString(value) || !filepath.IsAbs(value) {
		return "", "", ErrInvalidSourcePath
	}
	path, err := filepath.Abs(filepath.Clean(value))
	if err != nil || !filepath.IsAbs(path) {
		return "", "", ErrInvalidSourcePath
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", ErrInvalidSourcePath
	}
	relative = filepath.ToSlash(relative)
	if !validRelativeURI(relative) {
		return "", "", ErrInvalidSourcePath
	}
	return path, relative, nil
}

func metricV1(covered, total int64) coveragemodelv1.CoverageMetricV1 {
	return coveragemodelv1.CoverageMetricV1{Covered: covered, Total: total}
}

func completenessV1(value coveragedomain.Completeness) coveragemodelv1.CoverageCompletenessV1 {
	reasons := make([]coveragemodelv1.Reason, len(value.Reasons))
	for index, reason := range value.Reasons {
		reasons[index] = coveragemodelv1.Reason(reason)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	return coveragemodelv1.CoverageCompletenessV1{Outcome: coveragemodelv1.Outcome(value.Outcome), Reasons: reasons}
}

func provenanceV1(value coveragedomain.ToolchainSnapshot) coveragemodelv1.CoverageProvenanceV1 {
	return coveragemodelv1.CoverageProvenanceV1{Architecture: coveragemodelv1.Architecture(value.Architecture), Collector: coveragemodelv1.CoverageCollectorV1{Name: coveragemodelv1.CollectorName(value.Collector.Name), Version: value.Collector.Version}, Compiler: coveragemodelv1.CoverageCompilerV1{Family: coveragemodelv1.Family(value.Compiler.Family), Version: value.Compiler.Version}, Driver: coveragemodelv1.CoverageDriverV1{Name: coveragemodelv1.DriverName(value.Driver.Name), Version: value.Driver.Version}, InstrumentationFingerprint: value.InstrumentationFingerprint, NormalizerVersion: value.NormalizerVersion, Platform: coveragemodelv1.Platform(value.Platform)}
}

func addMetricV1(first, second coveragemodelv1.CoverageMetricV1) (coveragemodelv1.CoverageMetricV1, error) {
	if first.Covered < 0 || second.Covered < 0 || first.Total < 0 || second.Total < 0 || first.Covered > coveragedomain.MaxSafeInteger-second.Covered || first.Total > coveragedomain.MaxSafeInteger-second.Total {
		return coveragemodelv1.CoverageMetricV1{}, ErrInvalidCoverageMetric
	}
	return coveragemodelv1.CoverageMetricV1{Covered: first.Covered + second.Covered, Total: first.Total + second.Total}, nil
}
func addSummaryV1(first, second coveragemodelv1.CoverageSummaryV1) (coveragemodelv1.CoverageSummaryV1, error) {
	branches, err := addMetricV1(first.Branches, second.Branches)
	if err != nil {
		return coveragemodelv1.CoverageSummaryV1{}, err
	}
	functions, err := addMetricV1(first.Functions, second.Functions)
	if err != nil {
		return coveragemodelv1.CoverageSummaryV1{}, err
	}
	lines, err := addMetricV1(first.Lines, second.Lines)
	if err != nil {
		return coveragemodelv1.CoverageSummaryV1{}, err
	}
	return coveragemodelv1.CoverageSummaryV1{Branches: branches, Functions: functions, Lines: lines}, nil
}
