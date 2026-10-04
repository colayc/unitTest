package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	"unit-test-ide.local/test-service/internal/testgendomain"
	render "unit-test-ide.local/test-service/internal/testgenrender"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

var errProductionGenerationUnavailable = errors.New("production test generation is unavailable")

// generationTarget contains only service-resolved authority. Native paths and
// compile arguments are created by the trusted resolver, never by IPC.
type generationTarget struct {
	request                                     testgendomain.Request
	projectID, workspaceGeneration              string
	fileID, functionID, gapID, coverageReportID string
	sourceDigest, compileSnapshotDigest         string
	cmakeTargetDigest, toolchainID              string
	framework, frameworkDigest                  string
	ctestName                                   string
	analyzerBundleDigest                        string
	language                                    render.Language
	headerPath                                  string
	linkageName                                 string
	managed                                     bool
	sourceRelativePath                          string
	analysis                                    analysis.AnalysisRequest
	gap                                         solver.CoverageGap
	renderTarget                                render.TargetMetadata
	wallTime                                    time.Duration
	candidateLimit                              int
	memoryBytes                                 int64
	concurrency                                 int
	functions                                   []generationFunctionTarget
}

type generationFunctionTarget struct {
	functionID  string
	gapID       string
	linkageName string
	gap         solver.CoverageGap
}

type productionAnalyzer interface {
	Analyze(context.Context, analysis.AnalysisRequest) (analysis.Program, error)
}

// productionOracle imports only independently attested contracts or the
// isolated execution receipts added by the validation layer. It cannot accept
// assertion text from protocol input.
type productionOracle interface {
	Bind(context.Context, analysis.Program, string, []solver.InputVector) (analysis.Program, []assertion.Observation, error)
}

type productionPipelineResult struct {
	program      analysis.Program
	vectors      []solver.InputVector
	observations []assertion.Observation
	functions    []productionPipelineFunction
	editSet      render.StagedEditSet
	stages       []string
}

type productionPipelineFunction struct {
	functionID, symbolID string
	vectors              []solver.InputVector
	observations         []assertion.Observation
	cases                []render.Case
}

func (result productionPipelineResult) primarySymbolID() string {
	if len(result.functions) == 0 {
		return ""
	}
	return result.functions[0].symbolID
}

type productionGenerationPipeline struct {
	analyzer productionAnalyzer
	oracle   productionOracle
	solver   solver.Solver
}

func newProductionGenerationPipeline(analyzer productionAnalyzer, oracle productionOracle) *productionGenerationPipeline {
	return &productionGenerationPipeline{analyzer: analyzer, oracle: oracle}
}

func validProductionDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validProductionObjectID(value string) bool {
	return len(value) == 32 && validProductionDigest(value+value)
}

func validProductionLinkage(value string) bool {
	if value == "" || len(value) > 8192 {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func validProductionCTestName(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && !strings.ContainsRune(value, '\x00')
}

func (target generationTarget) validResolvedScope() bool {
	if !target.managed {
		return validProductionObjectID(target.functionID) && validProductionObjectID(target.gapID) && validProductionGap(target.gap, target.linkageName, target.compileSnapshotDigest)
	}
	switch target.request.Scope {
	case testgendomain.ScopeSymbol:
		return validProductionObjectID(target.functionID) && target.gapID == "" &&
			target.request.ManagedTargetID == target.functionID && target.gap.Kind == solver.GapFunction &&
			validProductionGap(target.gap, target.linkageName, target.compileSnapshotDigest) && len(target.functions) == 0
	case testgendomain.ScopeFile:
		if target.functionID != "" || target.gapID != "" || target.request.ManagedTargetID != target.fileID ||
			len(target.functions) == 0 || len(target.functions) > target.candidateLimit {
			return false
		}
		previous := ""
		for _, function := range target.functions {
			if !validProductionObjectID(function.functionID) || function.functionID <= previous || function.gapID != "" ||
				function.gap.Kind != solver.GapFunction || !validProductionGap(function.gap, function.linkageName, target.compileSnapshotDigest) {
				return false
			}
			previous = function.functionID
		}
		return true
	case testgendomain.ScopeCoverageGap:
		return validProductionObjectID(target.functionID) && validProductionObjectID(target.gapID) &&
			target.request.ManagedGapID == target.gapID && validProductionGap(target.gap, target.linkageName, target.compileSnapshotDigest) && len(target.functions) == 0
	default:
		return false
	}
}

func validProductionGap(gap solver.CoverageGap, linkageName, compileSnapshot string) bool {
	return gap.CompileSnapshot == compileSnapshot && (validProductionDigest(gap.SymbolID) || validProductionLinkage(linkageName)) && gap.AnalyzerVersion != ""
}

func (target generationTarget) selectedFunctions() []generationFunctionTarget {
	if target.request.Scope == testgendomain.ScopeFile {
		return append([]generationFunctionTarget(nil), target.functions...)
	}
	return []generationFunctionTarget{{functionID: target.functionID, gapID: target.gapID, linkageName: target.linkageName, gap: target.gap}}
}

func (target generationTarget) primarySymbolID() string {
	selected := target.selectedFunctions()
	if len(selected) == 0 {
		return ""
	}
	return selected[0].gap.SymbolID
}

func bindProductionFunctions(program analysis.Program, selected []generationFunctionTarget) ([]generationFunctionTarget, error) {
	result := append([]generationFunctionTarget(nil), selected...)
	seen := map[string]bool{}
	for index := range result {
		match := -1
		for functionIndex, function := range program.Functions {
			if result[index].gap.SymbolID != "" && function.SymbolID != result[index].gap.SymbolID ||
				result[index].linkageName != "" && function.LinkageName != result[index].linkageName {
				continue
			}
			if match != -1 {
				return nil, errProductionGenerationUnavailable
			}
			match = functionIndex
		}
		if match == -1 || !validProductionDigest(program.Functions[match].SymbolID) ||
			program.Functions[match].Decision.Kind != analysis.DecisionSupported || seen[program.Functions[match].SymbolID] {
			return nil, errProductionGenerationUnavailable
		}
		seen[program.Functions[match].SymbolID] = true
		result[index].gap.SymbolID = program.Functions[match].SymbolID
	}
	return result, nil
}

func (target generationTarget) valid() bool {
	if testgendomain.ValidateRequest(target.request) != nil ||
		target.request.ProjectID != target.projectID || target.request.WorkspaceGeneration != target.workspaceGeneration ||
		target.request.SourceDigest != target.sourceDigest || target.request.CompileSnapshotDigest != target.compileSnapshotDigest ||
		target.request.CMakeTargetDigest != target.cmakeTargetDigest || target.request.FrameworkBundleDigest != target.frameworkDigest ||
		target.request.AnalyzerBundleDigest != target.analyzerBundleDigest ||
		target.projectID == "" || len(target.projectID) > 128 ||
		!validProductionDigest(target.workspaceGeneration) || !validProductionObjectID(target.fileID) || !target.validResolvedScope() ||
		!validProductionObjectID(target.coverageReportID) || !validProductionDigest(target.sourceDigest) ||
		!validProductionDigest(target.compileSnapshotDigest) || !validProductionDigest(target.cmakeTargetDigest) || !validProductionDigest(target.toolchainID) ||
		!validProductionDigest(target.frameworkDigest) || !validProductionDigest(target.analyzerBundleDigest) ||
		!validProductionCTestName(target.ctestName) ||
		target.analysis.SourceDigest != target.sourceDigest || target.analysis.CompileSnapshotDigest != target.compileSnapshotDigest ||
		target.managed && target.sourceRelativePath == "" ||
		target.wallTime <= 0 || target.wallTime > 24*time.Hour || target.candidateLimit < 1 || target.candidateLimit > 1000 ||
		target.memoryBytes < 1024 || target.memoryBytes > 1<<30 || target.concurrency < 1 || target.concurrency > 16 {
		return false
	}
	switch target.framework {
	case "cpputest":
		return target.language == render.LanguageCPP
	case "unity":
		return target.language == render.LanguageC
	default:
		return false
	}
}

func productionUnified(name, before, after string) string {
	if before == after {
		return ""
	}
	oldLines := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	if before == "" {
		oldLines = nil
	}
	newLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if after == "" {
		newLines = nil
	}
	var builder strings.Builder
	start := 0
	if len(oldLines) > 0 {
		start = 1
	}
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n@@ -%d,%d +1,%d @@\n", name, name, start, len(oldLines), len(newLines))
	for _, line := range oldLines {
		builder.WriteString("-" + line + "\n")
	}
	for _, line := range newLines {
		builder.WriteString("+" + line + "\n")
	}
	return builder.String()
}

func forbiddenGeneratedText(value string) bool {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"mock", "stub", "cmock", "cppumock"} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

func (pipeline *productionGenerationPipeline) solveFunction(ctx context.Context, program analysis.Program, selected generationFunctionTarget, budget solver.Budget) (analysis.Program, productionPipelineFunction, error) {
	vectors, diagnostics, err := pipeline.solver.Solve(ctx, program, selected.gap, budget)
	if err != nil || len(vectors) == 0 || len(diagnostics) != 0 {
		return analysis.Program{}, productionPipelineFunction{}, errProductionGenerationUnavailable
	}
	sort.Slice(vectors, func(left, right int) bool { return vectors[left].ID < vectors[right].ID })
	program, observations, err := pipeline.oracle.Bind(ctx, program, selected.gap.SymbolID, vectors)
	if err != nil || len(observations) != len(vectors) {
		return analysis.Program{}, productionPipelineFunction{}, errProductionGenerationUnavailable
	}
	byCandidate := make(map[string]assertion.Observation, len(observations))
	for _, observation := range observations {
		if _, duplicate := byCandidate[observation.CandidateID]; duplicate {
			return analysis.Program{}, productionPipelineFunction{}, errProductionGenerationUnavailable
		}
		byCandidate[observation.CandidateID] = observation
	}
	function := productionPipelineFunction{functionID: selected.functionID, symbolID: selected.gap.SymbolID, vectors: vectors}
	for _, vector := range vectors {
		observation, ok := byCandidate[vector.ID]
		if !ok {
			return analysis.Program{}, productionPipelineFunction{}, errProductionGenerationUnavailable
		}
		if _, _, err := assertion.Derive(program, vector, observation); err != nil {
			return analysis.Program{}, productionPipelineFunction{}, errProductionGenerationUnavailable
		}
		function.observations = append(function.observations, observation)
		function.cases = append(function.cases, render.Case{Vector: vector, Observation: observation})
	}
	return program, function, nil
}

func (pipeline *productionGenerationPipeline) Generate(ctx context.Context, target generationTarget) (productionPipelineResult, error) {
	if ctx == nil || pipeline == nil || pipeline.analyzer == nil || pipeline.oracle == nil || !target.valid() {
		return productionPipelineResult{}, errProductionGenerationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return productionPipelineResult{}, err
	}
	program, err := pipeline.analyzer.Analyze(ctx, target.analysis)
	if err != nil {
		return productionPipelineResult{}, err
	}
	result := productionPipelineResult{program: program, stages: []string{"analyze"}}
	selected, err := bindProductionFunctions(program, target.selectedFunctions())
	if err != nil {
		return productionPipelineResult{}, err
	}
	generationContext, cancel := context.WithTimeout(ctx, target.wallTime)
	defer cancel()
	remainingCandidates := target.candidateLimit
	seenVectors := map[string]bool{}
	for index, functionTarget := range selected {
		remainingFunctions := len(selected) - index
		limit := remainingCandidates / remainingFunctions
		if limit < 1 {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
		remainingWall := target.wallTime
		if deadline, ok := generationContext.Deadline(); ok {
			remainingWall = time.Until(deadline)
		}
		if remainingWall <= 0 {
			return productionPipelineResult{}, context.DeadlineExceeded
		}
		var function productionPipelineFunction
		program, function, err = pipeline.solveFunction(generationContext, program, functionTarget, solver.Budget{
			WallTime: remainingWall, CandidateLimit: limit, MemoryBytes: target.memoryBytes, Concurrency: target.concurrency,
		})
		if err != nil {
			return productionPipelineResult{}, err
		}
		for _, vector := range function.vectors {
			if seenVectors[vector.ID] {
				return productionPipelineResult{}, errProductionGenerationUnavailable
			}
			seenVectors[vector.ID] = true
		}
		remainingCandidates -= len(function.vectors)
		result.functions = append(result.functions, function)
		result.vectors = append(result.vectors, function.vectors...)
		result.observations = append(result.observations, function.observations...)
	}
	result.program = program
	result.stages = append(result.stages, "solve", "assert")
	primary := result.functions[0]
	editSet, err := render.Render(render.RenderRequest{
		Program: program, SymbolID: primary.symbolID, Language: target.language,
		HeaderPath: target.headerPath, Target: target.renderTarget, Cases: primary.cases,
	})
	if err == nil && target.managed {
		managedFunctions := make([]render.ManagedFunctionInput, 0, len(result.functions))
		for _, function := range result.functions {
			managedFunctions = append(managedFunctions, render.ManagedFunctionInput{FunctionID: function.functionID, SymbolID: function.symbolID, Cases: function.cases})
		}
		managed, managedErr := render.RenderManagedFile(render.ManagedRenderInput{
			Program: program, ProjectID: target.projectID, SourceRelativePath: target.sourceRelativePath, SourceFileID: target.fileID,
			Framework: target.framework, GeneratorVersion: "unit-test-service-v1", Language: target.language,
			HeaderPath: target.headerPath, Target: target.renderTarget,
			Functions: managedFunctions,
		})
		if managedErr != nil || len(editSet.Files) != 2 || editSet.Files[0].Path != managed.Path {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
		editSet.Files[0].Content = append([]byte(nil), managed.Content...)
		editSet.Files[0].AfterDigest = productionBytesDigest(managed.Content)
		editSet.Diff = productionUnified(managed.Path, "", string(managed.Content)) +
			productionUnified(editSet.Files[1].Path, target.renderTarget.ExistingCMake, string(editSet.Files[1].Content))
	}
	if err != nil || forbiddenGeneratedText(editSet.Diff) {
		return productionPipelineResult{}, errProductionGenerationUnavailable
	}
	for _, file := range editSet.Files {
		if forbiddenGeneratedText(file.Path) || forbiddenGeneratedText(string(file.Content)) {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
	}
	result.editSet = editSet
	result.stages = append(result.stages, "render")
	return result, nil
}
