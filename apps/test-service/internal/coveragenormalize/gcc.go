package coveragenormalize

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	coverageparsergcovr "unit-test-ide.local/test-service/internal/coverageparser/gcovr"
)

var ErrInvalidGCC = errors.New("invalid normalized GCC coverage input")

type GCCInput struct {
	Export        coverageparsergcovr.Export
	WorkspaceRoot string
	Matcher       *GlobMatcher
	Toolchain     coveragedomain.ToolchainSnapshot
	Completeness  coveragedomain.Completeness
	Limits        Limits
}

// NormalizeGCC binds gcovr's canonical relative paths to verified workspace
// source snapshots. Gcovr never contributes a native source path.
func NormalizeGCC(input GCCInput) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error) {
	fail := func(err error) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error) {
		return coveragemodelv1.CoverageDocumentV1{}, nil, err
	}
	if err := input.Limits.Validate(); err != nil {
		return fail(err)
	}
	if input.Matcher == nil {
		return fail(ErrInvalidGlob)
	}
	if err := validateGCCExport(input.Export, input.Limits); err != nil {
		return fail(err)
	}
	root, err := canonicalWorkspaceRoot(input.WorkspaceRoot)
	if err != nil {
		return fail(err)
	}
	selected := make([]coverageparsergcovr.File, 0, len(input.Export.Files))
	paths := make([]string, 0, len(input.Export.Files))
	for _, file := range input.Export.Files {
		path, relative, err := workspaceRelativeGCovrSource(root, file.RelativePath)
		if err != nil {
			return fail(err)
		}
		if !input.Matcher.Include(relative) {
			continue
		}
		selected, paths = append(selected, file), append(paths, path)
	}
	evidence, err := collectSources(root, paths, input.Matcher, input.Limits)
	if err != nil {
		return fail(err)
	}
	bindings := make([]SourceBinding, len(evidence))
	filesByIdentity := make(map[physicalSourceID]coverageparsergcovr.File, len(evidence))
	for index, item := range evidence {
		bindings[index] = item.binding
		if item.inputIndex < 0 || item.inputIndex >= len(selected) {
			return fail(fmt.Errorf("%w: source binding mismatch", ErrInvalidGCC))
		}
		if _, duplicate := filesByIdentity[item.identity]; duplicate {
			return fail(ErrDuplicateSource)
		}
		filesByIdentity[item.identity] = selected[item.inputIndex]
	}
	document := coveragemodelv1.CoverageDocumentV1{Completeness: completenessV1(input.Completeness), Files: make([]coveragemodelv1.CoverageFileV1, 0, len(bindings)), Provenance: provenanceV1(input.Toolchain), SchemaVersion: coveragemodelv1.The10}
	for index, binding := range bindings {
		file, ok := filesByIdentity[evidence[index].identity]
		if !ok {
			return fail(fmt.Errorf("%w: source binding mismatch", ErrInvalidGCC))
		}
		normalized, err := normalizeGCCFile(file, binding)
		if err != nil {
			return fail(err)
		}
		document.Files = append(document.Files, normalized)
		document.Summary, err = addSummaryV1(document.Summary, normalized.Summary)
		if err != nil {
			return fail(fmt.Errorf("%w: %v", ErrInvalidGCC, err))
		}
	}
	if err := validateGCCToolchain(input.Toolchain); err != nil {
		return fail(err)
	}
	if err := coveragemodelv1.Validate(document); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrInvalidGCC, err))
	}
	return document, append([]SourceBinding(nil), bindings...), nil
}

func validateGCCExport(value coverageparsergcovr.Export, limits Limits) error {
	if value.FormatVersion != "0.14" {
		return ErrInvalidGCC
	}
	if int64(len(value.Files)) > limits.MaxFiles {
		return ErrLimitExceeded
	}
	var lines, branches, functions int64
	seenFiles := make(map[string]struct{}, len(value.Files))
	for _, file := range value.Files {
		if !validRelativeURI(file.RelativePath) || int64(len(file.RelativePath)) > limits.MaxStringBytes || !validGCCMetric(file.Functions) {
			return ErrInvalidGCC
		}
		if _, duplicate := seenFiles[file.RelativePath]; duplicate {
			return ErrInvalidGCC
		}
		seenFiles[file.RelativePath] = struct{}{}
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
			if line.Number < 1 || line.Number > coveragedomain.MaxSafeInteger || line.Count < 0 || line.Count > coveragedomain.MaxSafeInteger || !validGCCMetric(line.Branches) {
				return ErrInvalidGCC
			}
			if _, duplicate := seenLines[line.Number]; duplicate {
				return ErrInvalidGCC
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
func validGCCMetric(value coverageparsergcovr.Metric) bool {
	return value.Covered >= 0 && value.Covered <= value.Total && value.Total <= coveragedomain.MaxSafeInteger
}

func workspaceRelativeGCovrSource(root, relative string) (string, string, error) {
	if !validRelativeURI(relative) {
		return "", "", ErrInvalidSourcePath
	}
	path := filepath.Join(append([]string{root}, splitURI(relative)...)...)
	path, actual, err := workspaceRelativeSource(root, path)
	if err != nil || actual != relative {
		return "", "", ErrInvalidSourcePath
	}
	return path, relative, nil
}
func splitURI(relative string) []string {
	result := make([]string, 0, 4)
	start := 0
	for index := 0; index <= len(relative); index++ {
		if index == len(relative) || relative[index] == '/' {
			result = append(result, relative[start:index])
			start = index + 1
		}
	}
	return result
}

func normalizeGCCFile(file coverageparsergcovr.File, binding SourceBinding) (coveragemodelv1.CoverageFileV1, error) {
	lines := append([]coverageparsergcovr.Line(nil), file.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].Number < lines[j].Number })
	result := coveragemodelv1.CoverageFileV1{Lines: make([]coveragemodelv1.CoverageLineV1, len(lines)), Sha256: binding.SHA256, URI: binding.URI, Summary: coveragemodelv1.CoverageSummaryV1{Functions: coveragemodelv1.CoverageMetricV1{Covered: file.Functions.Covered, Total: file.Functions.Total}}}
	for index, line := range lines {
		normalized := coveragemodelv1.CoverageLineV1{Line: line.Number, Count: line.Count, Branches: coveragemodelv1.CoverageMetricV1{Covered: line.Branches.Covered, Total: line.Branches.Total}}
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
func validateGCCToolchain(value coveragedomain.ToolchainSnapshot) error {
	if value.Platform != coveragedomain.PlatformLinux || value.Architecture != coveragedomain.ArchitectureX64 || value.Compiler.Family != coveragedomain.CompilerFamilyGCC || value.Driver.Name != coveragedomain.DriverGCov || value.Collector.Name != coveragedomain.CollectorGCovr || value.Collector.Version != "8.6" || value.Compiler.Version == "" || value.Compiler.Version != value.Driver.Version {
		return ErrInvalidGCC
	}
	return nil
}
