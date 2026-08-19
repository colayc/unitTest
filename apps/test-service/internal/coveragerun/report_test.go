package coveragerun

import (
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
)

func TestBuildReportUsesFinalizedCompletenessAndStableIdentity(t *testing.T) {
	input := ReportInput{
		State: State{Terminal: true, Outcome: coveragedomain.OutcomePartial, PartialReasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed}},
		RunID: strings.Repeat("a", 32), TestRunID: strings.Repeat("b", 32), ReportID: strings.Repeat("c", 32), ArtifactID: strings.Repeat("d", 32),
		CreatedAt: time.Unix(10, 0), Summary: validCoverageSummary(), Toolchain: validCoverageToolchain(),
	}
	report, err := BuildReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != coveragedomain.SchemaVersion10 || report.RunID != input.RunID || report.TestRunID != input.TestRunID || report.Completeness.Outcome != coveragedomain.OutcomePartial {
		t.Fatalf("report = %#v", report)
	}
	input.State.PartialReasons[0] = coveragedomain.CompletenessReasonTestTimedOut
	if report.Completeness.Reasons[0] != coveragedomain.CompletenessReasonTestCrashed {
		t.Fatal("report completeness aliases input state")
	}
}

func TestBuildReportRejectsUnavailableStateAndInvalidDomainMetadata(t *testing.T) {
	base := ReportInput{State: State{Terminal: true, Outcome: coveragedomain.OutcomeUnavailable}, RunID: strings.Repeat("a", 32), TestRunID: strings.Repeat("b", 32), ReportID: strings.Repeat("c", 32), ArtifactID: strings.Repeat("d", 32), CreatedAt: time.Unix(10, 0), Summary: validCoverageSummary(), Toolchain: validCoverageToolchain()}
	if _, err := BuildReport(base); !errors.Is(err, ErrReportBuild) {
		t.Fatalf("unavailable error = %v", err)
	}
	base.State = State{Terminal: true, Outcome: coveragedomain.OutcomeAvailable}
	base.RunID = "bad"
	if _, err := BuildReport(base); !errors.Is(err, ErrReportBuild) {
		t.Fatalf("invalid identity error = %v", err)
	}
}

func TestBuildReportPublishesSortedSourceDigestsWithoutNativePaths(t *testing.T) {
	input := ReportInput{
		State: State{Terminal: true, Outcome: coveragedomain.OutcomeAvailable},
		RunID: strings.Repeat("a", 32), TestRunID: strings.Repeat("b", 32), ReportID: strings.Repeat("c", 32), ArtifactID: strings.Repeat("d", 32),
		CreatedAt: time.Unix(10, 0), Summary: validCoverageSummary(), Toolchain: validCoverageToolchain(),
		Sources: []coveragenormalize.SourceBinding{
			{URI: "src/z.cpp", SHA256: strings.Repeat("b", 64), NativePath: `C:\secret\z.cpp`},
			{URI: "src/a.cpp", SHA256: strings.Repeat("a", 64), NativePath: `/secret/a.cpp`},
		},
	}
	report, err := BuildReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sources) != 2 || report.Sources[0].URI != "src/a.cpp" || report.Sources[1].URI != "src/z.cpp" {
		t.Fatalf("report sources = %#v, want sorted sources", report.Sources)
	}
	if report.Sources[0].SHA256 != strings.Repeat("a", 64) || report.Sources[1].SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("report source digests = %#v", report.Sources)
	}
}

func TestBuildReportRejectsDuplicateSourceURI(t *testing.T) {
	input := ReportInput{
		State: State{Terminal: true, Outcome: coveragedomain.OutcomeAvailable},
		RunID: strings.Repeat("a", 32), TestRunID: strings.Repeat("b", 32), ReportID: strings.Repeat("c", 32), ArtifactID: strings.Repeat("d", 32),
		CreatedAt: time.Unix(10, 0), Summary: validCoverageSummary(), Toolchain: validCoverageToolchain(),
		Sources: []coveragenormalize.SourceBinding{
			{URI: "src/a.cpp", SHA256: strings.Repeat("a", 64)},
			{URI: "src/a.cpp", SHA256: strings.Repeat("b", 64)},
		},
	}
	if _, err := BuildReport(input); !errors.Is(err, ErrReportBuild) {
		t.Fatalf("duplicate source error = %v, want ErrReportBuild", err)
	}
}

func validCoverageSummary() coveragedomain.Summary {
	return coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: 1, Total: 2}, Branches: coveragedomain.Metric{Covered: 1, Total: 1}, Functions: coveragedomain.Metric{Covered: 1, Total: 1}}
}

func validCoverageToolchain() coveragedomain.ToolchainSnapshot {
	return coveragedomain.ToolchainSnapshot{
		Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
		Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
		Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
		Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
		NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("e", 64),
	}
}
