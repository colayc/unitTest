package coveragerun

import "unit-test-ide.local/test-service/internal/coveragedomain"

// StepKind is a platform-neutral stage in the trusted collector pipeline.
// The actual process specs are produced by platform collectors later.
type StepKind string

const (
	StepCollectProfiles StepKind = "collect_profiles"
	StepMergeProfiles   StepKind = "merge_profiles"
	StepGenerateReport  StepKind = "generate_report"
)

// Plan is the closed set of evidence operations allowed for a resolved
// collector. It contains no command line, environment, or filesystem path.
type Plan struct {
	Collector CollectorSpec
	Steps     []StepKind
}

// BuildPlan resolves the toolchain and returns the fixed stage sequence. GCC
// uses gcovr's combined collection/report operation; LLVM requires a separate
// profdata merge before export/report generation.
func BuildPlan(toolchain coveragedomain.ToolchainSnapshot) (Plan, error) {
	spec, err := ResolveCollector(toolchain)
	if err != nil {
		return Plan{}, err
	}
	steps := []StepKind{StepCollectProfiles, StepGenerateReport}
	if spec.MergeStrategy == MergeStrategyLLVMProfdata {
		steps = []StepKind{StepCollectProfiles, StepMergeProfiles, StepGenerateReport}
	}
	return Plan{Collector: spec, Steps: steps}, nil
}
