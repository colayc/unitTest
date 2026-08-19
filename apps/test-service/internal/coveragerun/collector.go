package coveragerun

import (
	"errors"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

// ErrUnsupportedToolchain is returned when a coverage request does not match
// one of the explicitly supported native collector combinations.
var ErrUnsupportedToolchain = errors.New("unsupported coverage toolchain")

// ProfileFormat identifies the profile evidence emitted by an instrumented
// test binary. It is deliberately descriptive rather than a command line;
// platform collectors own process construction and tool invocation.
type ProfileFormat string

const (
	ProfileFormatGCov    ProfileFormat = "gcov"
	ProfileFormatLLVMRaw ProfileFormat = "llvm-profraw"
)

// MergeStrategy identifies the platform-neutral merge contract. The concrete
// collector decides how to invoke its trusted tool bundle.
type MergeStrategy string

const (
	MergeStrategyGCovr        MergeStrategy = "gcovr"
	MergeStrategyLLVMProfdata MergeStrategy = "llvm-profdata"
)

// CollectorSpec is the closed routing decision for one native toolchain. It
// contains no executable path, argv, environment, or user-controlled script.
type CollectorSpec struct {
	Toolchain     coveragedomain.ToolchainSnapshot
	ProfileFormat ProfileFormat
	MergeStrategy MergeStrategy
}

// ResolveCollector maps the persisted toolchain snapshot to the only native
// collector combinations currently supported by the product:
// Windows clang-cl/llvm-cov, Linux GCC/gcov/gcovr, and Linux Clang/llvm-cov.
func ResolveCollector(toolchain coveragedomain.ToolchainSnapshot) (CollectorSpec, error) {
	if toolchain.Architecture != coveragedomain.ArchitectureX64 ||
		!nonEmpty(toolchain.Compiler.Version) ||
		!nonEmpty(toolchain.Driver.Version) ||
		!nonEmpty(toolchain.Collector.Version) {
		return CollectorSpec{}, ErrUnsupportedToolchain
	}
	if toolchain.Driver.Name == coveragedomain.DriverLLVMCov &&
		(toolchain.Compiler.Version != toolchain.Driver.Version || toolchain.Driver.Version != toolchain.Collector.Version) {
		return CollectorSpec{}, ErrUnsupportedToolchain
	}
	if toolchain.Driver.Name == coveragedomain.DriverGCov && toolchain.Compiler.Version != toolchain.Driver.Version {
		return CollectorSpec{}, ErrUnsupportedToolchain
	}

	spec := CollectorSpec{Toolchain: toolchain}
	switch {
	case toolchain.Platform == coveragedomain.PlatformWindows &&
		toolchain.Compiler.Family == coveragedomain.CompilerFamilyClangCL &&
		toolchain.Driver.Name == coveragedomain.DriverLLVMCov &&
		toolchain.Collector.Name == coveragedomain.CollectorLLVMCov:
		spec.ProfileFormat = ProfileFormatLLVMRaw
		spec.MergeStrategy = MergeStrategyLLVMProfdata
	case toolchain.Platform == coveragedomain.PlatformLinux &&
		toolchain.Compiler.Family == coveragedomain.CompilerFamilyGCC &&
		toolchain.Driver.Name == coveragedomain.DriverGCov &&
		toolchain.Collector.Name == coveragedomain.CollectorGCovr:
		spec.ProfileFormat = ProfileFormatGCov
		spec.MergeStrategy = MergeStrategyGCovr
	case toolchain.Platform == coveragedomain.PlatformLinux &&
		toolchain.Compiler.Family == coveragedomain.CompilerFamilyClang &&
		toolchain.Driver.Name == coveragedomain.DriverLLVMCov &&
		toolchain.Collector.Name == coveragedomain.CollectorLLVMCov:
		spec.ProfileFormat = ProfileFormatLLVMRaw
		spec.MergeStrategy = MergeStrategyLLVMProfdata
	default:
		return CollectorSpec{}, ErrUnsupportedToolchain
	}
	return spec, nil
}

func nonEmpty(value string) bool { return strings.TrimSpace(value) != "" }
