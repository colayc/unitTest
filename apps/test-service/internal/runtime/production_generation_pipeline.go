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
	toolchainID, framework, frameworkDigest     string
	analyzerBundleDigest                        string
	language                                    render.Language
	headerPath                                  string
	managed                                     bool
	sourceRelativePath                          string
	analysis                                    analysis.AnalysisRequest
	gap                                         solver.CoverageGap
	renderTarget                                render.TargetMetadata
	wallTime                                    time.Duration
	candidateLimit                              int
	memoryBytes                                 int64
	concurrency                                 int
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
	editSet      render.StagedEditSet
	stages       []string
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

func (target generationTarget) validResolvedScope() bool {
	if !target.managed {
		return validProductionObjectID(target.functionID) && validProductionObjectID(target.gapID)
	}
	switch target.request.Scope {
	case testgendomain.ScopeSymbol:
		return validProductionObjectID(target.functionID) && target.gapID == "" &&
			target.request.ManagedTargetID == target.functionID && target.gap.Kind == solver.GapFunction
	case testgendomain.ScopeCoverageGap:
		return validProductionObjectID(target.functionID) && validProductionObjectID(target.gapID) &&
			target.request.ManagedGapID == target.gapID
	default:
		// File generation needs a bounded multi-function execution plan. It must
		// stay unavailable until that plan is implemented atomically.
		return false
	}
}

func (target generationTarget) valid() bool {
	if testgendomain.ValidateRequest(target.request) != nil ||
		target.request.ProjectID != target.projectID || target.request.WorkspaceGeneration != target.workspaceGeneration ||
		target.request.SourceDigest != target.sourceDigest || target.request.CompileSnapshotDigest != target.compileSnapshotDigest ||
		target.request.CMakeTargetDigest != target.toolchainID || target.request.FrameworkBundleDigest != target.frameworkDigest ||
		target.request.AnalyzerBundleDigest != target.analyzerBundleDigest ||
		target.projectID == "" || len(target.projectID) > 128 ||
		!validProductionDigest(target.workspaceGeneration) || !validProductionObjectID(target.fileID) || !target.validResolvedScope() ||
		!validProductionObjectID(target.coverageReportID) || !validProductionDigest(target.sourceDigest) ||
		!validProductionDigest(target.compileSnapshotDigest) || !validProductionDigest(target.toolchainID) ||
		!validProductionDigest(target.frameworkDigest) || !validProductionDigest(target.analyzerBundleDigest) ||
		target.analysis.SourceDigest != target.sourceDigest || target.analysis.CompileSnapshotDigest != target.compileSnapshotDigest ||
		target.gap.CompileSnapshot != target.compileSnapshotDigest || target.gap.SymbolID == "" ||
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
	vectors, diagnostics, err := pipeline.solver.Solve(ctx, program, target.gap, solver.Budget{
		WallTime: target.wallTime, CandidateLimit: target.candidateLimit,
		MemoryBytes: target.memoryBytes, Concurrency: target.concurrency,
	})
	if err != nil {
		return productionPipelineResult{}, err
	}
	if len(vectors) == 0 || len(diagnostics) != 0 {
		return productionPipelineResult{}, errProductionGenerationUnavailable
	}
	sort.Slice(vectors, func(left, right int) bool { return vectors[left].ID < vectors[right].ID })
	result.vectors = vectors
	result.stages = append(result.stages, "solve")
	program, observations, err := pipeline.oracle.Bind(ctx, program, target.gap.SymbolID, vectors)
	if err != nil || len(observations) != len(vectors) {
		return productionPipelineResult{}, errProductionGenerationUnavailable
	}
	byCandidate := make(map[string]assertion.Observation, len(observations))
	for _, observation := range observations {
		if _, duplicate := byCandidate[observation.CandidateID]; duplicate {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
		byCandidate[observation.CandidateID] = observation
	}
	cases := make([]render.Case, 0, len(vectors))
	orderedObservations := make([]assertion.Observation, 0, len(vectors))
	for _, vector := range vectors {
		observation, ok := byCandidate[vector.ID]
		if !ok {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
		if _, _, err := assertion.Derive(program, vector, observation); err != nil {
			return productionPipelineResult{}, errProductionGenerationUnavailable
		}
		orderedObservations = append(orderedObservations, observation)
		cases = append(cases, render.Case{Vector: vector, Observation: observation})
	}
	result.program, result.observations = program, orderedObservations
	result.stages = append(result.stages, "assert")
	editSet, err := render.Render(render.RenderRequest{
		Program: program, SymbolID: target.gap.SymbolID, Language: target.language,
		HeaderPath: target.headerPath, Target: target.renderTarget, Cases: cases,
	})
	if err == nil && target.managed {
		managed, managedErr := render.RenderManagedFile(render.ManagedRenderInput{
			Program: program, ProjectID: target.projectID, SourceRelativePath: target.sourceRelativePath, SourceFileID: target.fileID,
			Framework: target.framework, GeneratorVersion: "unit-test-service-v1", Language: target.language,
			HeaderPath: target.headerPath, Target: target.renderTarget,
			Functions: []render.ManagedFunctionInput{{FunctionID: target.functionID, SymbolID: target.gap.SymbolID, Cases: cases}},
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
