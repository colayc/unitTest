package coveragenormalize

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	coverageparserllvm "unit-test-ide.local/test-service/internal/coverageparser/llvm"
)

var ErrInvalidLLVM = errors.New("invalid normalized LLVM coverage input")

type LLVMInput struct {
	Export        coverageparserllvm.Export
	WorkspaceRoot string
	Matcher       *GlobMatcher
	Toolchain     coveragedomain.ToolchainSnapshot
	Completeness  coveragedomain.Completeness
	Limits        Limits
}

type selectedLLVMFile struct {
	file coverageparserllvm.File
}

type physicalSourceID struct {
	device uint64
	file   uint64
}

// NormalizeLLVM binds parsed LLVM evidence to verified workspace source
// snapshots and constructs the only public coverage metric representation.
func NormalizeLLVM(input LLVMInput) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error) {
	fail := func(err error) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error) {
		return coveragemodelv1.CoverageDocumentV1{}, nil, err
	}
	if err := input.Limits.Validate(); err != nil {
		return fail(err)
	}
	if input.Matcher == nil {
		return fail(ErrInvalidGlob)
	}
	if err := validateLLVMExport(input.Export, input.Limits); err != nil {
		return fail(err)
	}
	root, err := canonicalWorkspaceRoot(input.WorkspaceRoot)
	if err != nil {
		return fail(err)
	}

	selected := make([]selectedLLVMFile, 0, len(input.Export.Files))
	paths := make([]string, 0, len(input.Export.Files))
	for _, file := range input.Export.Files {
		path, relative, err := workspaceRelativeSource(root, file.NativePath)
		if err != nil {
			return fail(err)
		}
		if !input.Matcher.Include(relative) {
			continue
		}
		selected = append(selected, selectedLLVMFile{file: file})
		paths = append(paths, path)
	}

	evidence, err := collectSources(root, paths, input.Matcher, input.Limits)
	if err != nil {
		return fail(err)
	}
	bindings := make([]SourceBinding, len(evidence))
	filesByIdentity := make(map[physicalSourceID]coverageparserllvm.File, len(evidence))
	for index := range evidence {
		bindings[index] = evidence[index].binding
		inputIndex := evidence[index].inputIndex
		if inputIndex < 0 || inputIndex >= len(selected) {
			return fail(fmt.Errorf("%w: source binding mismatch", ErrInvalidLLVM))
		}
		if _, duplicate := filesByIdentity[evidence[index].identity]; duplicate {
			return fail(ErrDuplicateSource)
		}
		filesByIdentity[evidence[index].identity] = selected[inputIndex].file
	}

	document := coveragemodelv1.CoverageDocumentV1{
		Completeness:  completenessV1(input.Completeness),
		Files:         make([]coveragemodelv1.CoverageFileV1, 0, len(bindings)),
		Provenance:    provenanceV1(input.Toolchain),
		SchemaVersion: coveragemodelv1.The10,
	}
	for index, binding := range bindings {
		file, exists := filesByIdentity[evidence[index].identity]
		if !exists {
			return fail(fmt.Errorf("%w: source binding mismatch", ErrInvalidLLVM))
		}
		normalized, err := normalizeLLVMFile(file, binding)
		if err != nil {
			return fail(err)
		}
		document.Files = append(document.Files, normalized)
		document.Summary, err = addSummaryV1(document.Summary, normalized.Summary)
		if err != nil {
			return fail(err)
		}
	}
	if err := validateLLVMToolchain(input.Toolchain); err != nil {
		return fail(err)
	}
	if err := coveragemodelv1.Validate(document); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrInvalidLLVM, err))
	}
	return document, append([]SourceBinding(nil), bindings...), nil
}

func validateLLVMExport(value coverageparserllvm.Export, limits Limits) error {
	if !supportedLLVMVersion(value.Version) || int64(len(value.Files)) > limits.MaxFiles {
		if int64(len(value.Files)) > limits.MaxFiles {
			return ErrLimitExceeded
		}
		return ErrInvalidLLVM
	}
	var lines, branches, functions int64
	for _, file := range value.Files {
		if file.NativePath == "" || int64(len(file.NativePath)) > limits.MaxStringBytes || !validMetric(file.Functions) {
			return ErrInvalidLLVM
		}
		if file.Functions.Total > limits.MaxFunctions-functions {
			return ErrLimitExceeded
		}
		functions += file.Functions.Total
		if int64(len(file.Lines)) > limits.MaxLines-lines {
			return ErrLimitExceeded
		}
		lines += int64(len(file.Lines))
		seenLines := make(map[int64]struct{}, len(file.Lines))
		for _, line := range file.Lines {
			if line.Number < 1 || line.Number > coveragedomain.MaxSafeInteger || line.Count < 0 ||
				line.Count > coveragedomain.MaxSafeInteger || !validMetric(line.Branches) {
				return ErrInvalidLLVM
			}
			if _, duplicate := seenLines[line.Number]; duplicate {
				return ErrInvalidLLVM
			}
			seenLines[line.Number] = struct{}{}
			if line.Branches.Total > limits.MaxBranches-branches {
				return ErrLimitExceeded
			}
			branches += line.Branches.Total
		}
	}
	return nil
}

func validMetric(value coverageparserllvm.Metric) bool {
	return value.Covered >= 0 && value.Covered <= value.Total && value.Total <= coveragedomain.MaxSafeInteger
}

func supportedLLVMVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || (parts[0] != "2" && parts[0] != "3") {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 31); err != nil {
			return false
		}
	}
	return true
}

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
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", ErrInvalidSourcePath
	}
	relative = filepath.ToSlash(relative)
	if !validRelativeURI(relative) {
		return "", "", ErrInvalidSourcePath
	}
	return path, relative, nil
}

func normalizeLLVMFile(file coverageparserllvm.File, binding SourceBinding) (coveragemodelv1.CoverageFileV1, error) {
	lines := append([]coverageparserllvm.Line(nil), file.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].Number < lines[j].Number })
	result := coveragemodelv1.CoverageFileV1{
		Lines:  make([]coveragemodelv1.CoverageLineV1, len(lines)),
		Sha256: binding.SHA256,
		URI:    binding.URI,
		Summary: coveragemodelv1.CoverageSummaryV1{
			Functions: metricV1(file.Functions),
		},
	}
	for index, line := range lines {
		normalized := coveragemodelv1.CoverageLineV1{
			Branches: metricV1(line.Branches),
			Count:    line.Count,
			Line:     line.Number,
		}
		result.Lines[index] = normalized
		result.Summary.Lines.Total++
		if line.Count > 0 {
			result.Summary.Lines.Covered++
		}
		var err error
		result.Summary.Branches, err = addMetricV1(result.Summary.Branches, normalized.Branches)
		if err != nil {
			return coveragemodelv1.CoverageFileV1{}, err
		}
	}
	return result, nil
}

func metricV1(value coverageparserllvm.Metric) coveragemodelv1.CoverageMetricV1 {
	return coveragemodelv1.CoverageMetricV1{Covered: value.Covered, Total: value.Total}
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
	return coveragemodelv1.CoverageProvenanceV1{
		Architecture: coveragemodelv1.Architecture(value.Architecture),
		Collector: coveragemodelv1.CoverageCollectorV1{
			Name: coveragemodelv1.CollectorName(value.Collector.Name), Version: value.Collector.Version,
		},
		Compiler: coveragemodelv1.CoverageCompilerV1{
			Family: coveragemodelv1.Family(value.Compiler.Family), Version: value.Compiler.Version,
		},
		Driver: coveragemodelv1.CoverageDriverV1{
			Name: coveragemodelv1.DriverName(value.Driver.Name), Version: value.Driver.Version,
		},
		InstrumentationFingerprint: value.InstrumentationFingerprint,
		NormalizerVersion:          value.NormalizerVersion,
		Platform:                   coveragemodelv1.Platform(value.Platform),
	}
}

func validateLLVMToolchain(value coveragedomain.ToolchainSnapshot) error {
	if value.Driver.Name != coveragedomain.DriverLLVMCov || value.Collector.Name != coveragedomain.CollectorLLVMCov ||
		value.Compiler.Version == "" || value.Compiler.Version != value.Driver.Version ||
		value.Driver.Version != value.Collector.Version {
		return ErrInvalidLLVM
	}
	validPair := value.Platform == coveragedomain.PlatformWindows && value.Compiler.Family == coveragedomain.CompilerFamilyClangCL ||
		value.Platform == coveragedomain.PlatformLinux && value.Compiler.Family == coveragedomain.CompilerFamilyClang
	if !validPair {
		return ErrInvalidLLVM
	}
	return nil
}

func addMetricV1(first, second coveragemodelv1.CoverageMetricV1) (coveragemodelv1.CoverageMetricV1, error) {
	if first.Covered < 0 || second.Covered < 0 || first.Total < 0 || second.Total < 0 ||
		first.Covered > coveragedomain.MaxSafeInteger-second.Covered ||
		first.Total > coveragedomain.MaxSafeInteger-second.Total {
		return coveragemodelv1.CoverageMetricV1{}, ErrInvalidLLVM
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
