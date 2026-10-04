package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	"unit-test-ide.local/test-service/internal/testgendomain"
	render "unit-test-ide.local/test-service/internal/testgenrender"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

type pipelineAnalyzerFixture struct {
	program analysis.Program
	calls   int
}

func (fixture *pipelineAnalyzerFixture) Analyze(context.Context, analysis.AnalysisRequest) (analysis.Program, error) {
	fixture.calls++
	return fixture.program, nil
}

type pipelineOracleFixture struct{ calls int }

func (fixture *pipelineOracleFixture) Bind(_ context.Context, program analysis.Program, symbolID string, vectors []solver.InputVector) (analysis.Program, []assertion.Observation, error) {
	fixture.calls++
	observations := make([]assertion.Observation, 0, len(vectors))
	for _, vector := range vectors {
		expected := solver.Value{Kind: analysis.TypeInteger, Integer: "7"}
		evidence := assertion.Evidence{
			Kind: assertion.EvidenceReturnContract, Target: assertion.TargetReturn,
			TargetDigest: symbolID, SourceDigest: program.Functions[0].Excerpt.Digest,
			Expected: expected, Rule: assertion.RuleEqual, Stability: assertion.StabilityDeterministic,
		}
		program.Functions[0].OracleProofs = append(program.Functions[0].OracleProofs, analysis.OracleProof{
			Kind: string(evidence.Kind), CandidateID: vector.ID,
			InputDigest: assertion.InputDigest(vector.Inputs), TargetDigest: evidence.TargetDigest,
			SourceDigest: evidence.SourceDigest, ExpectedDigest: assertion.ValueDigest(expected), Rule: string(evidence.Rule),
		})
		observations = append(observations, assertion.Observation{SymbolID: symbolID, CandidateID: vector.ID, Evidence: []assertion.Evidence{evidence}})
	}
	return program, observations, nil
}

func productionPipelineFixture(t *testing.T) (*productionGenerationPipeline, generationTarget, *pipelineAnalyzerFixture, *pipelineOracleFixture) {
	t.Helper()
	program := analysis.Program{Version: analysis.IRVersion, Digest: strings.Repeat("a", 64), Functions: []analysis.Function{{
		SymbolID: strings.Repeat("c", 64), Name: "classify",
		ReturnType: analysis.Type{Kind: analysis.TypeInteger, Spelling: "int", BitWidth: 32, Signed: true},
		Parameters: []analysis.Parameter{{Name: "x", Type: analysis.Type{Kind: analysis.TypeInteger, Spelling: "int", BitWidth: 32, Signed: true}}},
		Branches:   []analysis.Branch{{Kind: analysis.BranchIf, Predicate: analysis.Predicate{Operator: "<", Left: "x", Right: "3"}, PathVerified: true, LocationDigest: strings.Repeat("b", 64)}},
		Excerpt:    analysis.SourceExcerpt{Digest: strings.Repeat("d", 64)},
		Decision:   analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone},
	}}}
	analyzer := &pipelineAnalyzerFixture{program: program}
	oracle := &pipelineOracleFixture{}
	target := generationTarget{
		projectID: "core", workspaceGeneration: strings.Repeat("1", 64),
		fileID: strings.Repeat("2", 32), functionID: strings.Repeat("3", 32), gapID: strings.Repeat("4", 32),
		coverageReportID: strings.Repeat("5", 32), sourceDigest: strings.Repeat("d", 64),
		compileSnapshotDigest: strings.Repeat("a", 64), toolchainID: strings.Repeat("6", 64),
		framework: "cpputest", frameworkDigest: strings.Repeat("7", 64), analyzerBundleDigest: strings.Repeat("8", 64),
		language: render.LanguageCPP, headerPath: "include/classify.h",
		analysis:     analysis.AnalysisRequest{WorkspaceRoot: t.TempDir(), SourceRelative: "src/classify.cpp", SourceDigest: strings.Repeat("d", 64), CompileSnapshotDigest: strings.Repeat("a", 64), Timeout: time.Second},
		gap:          solver.CoverageGap{Kind: solver.GapBranch, SymbolID: strings.Repeat("c", 64), BranchID: strings.Repeat("b", 64), Outcome: solver.OutcomeTrue, CompileSnapshot: strings.Repeat("a", 64), AnalyzerVersion: "clang-ir-v1"},
		renderTarget: render.TargetMetadata{TestTarget: "unit_tests", ProductionTarget: "core", FrameworkTarget: "CppUTest", CMakePath: "tests/CMakeLists.txt", TestPath: "tests/generated/classify_test.cpp", ExistingCMake: "add_executable(unit_tests existing.cpp)\ntarget_link_libraries(unit_tests PRIVATE core CppUTest)\n"},
		wallTime:     time.Second, candidateLimit: 16, memoryBytes: 1 << 20, concurrency: 1,
	}
	target.request = testgendomain.Request{
		IdempotencyKey: strings.Repeat("9", 32), WorkspaceGeneration: target.workspaceGeneration,
		ProjectID: target.projectID, Scope: testgendomain.ScopeCoverageGap,
		CoverageReportID: target.coverageReportID, ManagedGapID: target.gapID,
		Framework:             testgendomain.FrameworkCppUTest,
		Goals:                 testgendomain.Goals{FunctionPercent: 100, LinePercent: 100, BranchPercent: 100},
		Budgets:               testgendomain.Budgets{WallTimeMS: 1000, CandidateCount: int64(target.candidateLimit), MemoryMiB: 64, Concurrency: int64(target.concurrency)},
		CompileSnapshotDigest: target.compileSnapshotDigest, CoverageSnapshotDigest: strings.Repeat("e", 64),
		SourceDigest: target.sourceDigest, CMakeTargetDigest: target.toolchainID,
		FrameworkBundleDigest: target.frameworkDigest, AnalyzerBundleDigest: target.analyzerBundleDigest,
		BaselineReportDigest: strings.Repeat("f", 64), ProcessOwnerDigest: strings.Repeat("0", 64),
	}
	return newProductionGenerationPipeline(analyzer, oracle), target, analyzer, oracle
}

func TestProductionGenerationPipelineIsDeterministicAndUsesExistingModules(t *testing.T) {
	pipeline, target, analyzer, oracle := productionPipelineFixture(t)
	first, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic pipeline\nfirst=%+v\nsecond=%+v", first, second)
	}
	if analyzer.calls != 2 || oracle.calls != 2 || len(first.vectors) == 0 || len(first.editSet.Files) != 2 {
		t.Fatalf("analyze=%d oracle=%d vectors=%d files=%d", analyzer.calls, oracle.calls, len(first.vectors), len(first.editSet.Files))
	}
	if !reflect.DeepEqual(first.stages, []string{"analyze", "solve", "assert", "render"}) {
		t.Fatalf("stages=%v", first.stages)
	}
	for _, forbidden := range []string{"mock", "stub", "cmock", "cppumock"} {
		if strings.Contains(strings.ToLower(first.editSet.Diff), forbidden) {
			t.Fatalf("generated diff contains forbidden %q", forbidden)
		}
	}
}

func TestProductionGenerationPipelineRejectsInvalidTargetBeforeAnalysis(t *testing.T) {
	pipeline, target, analyzer, _ := productionPipelineFixture(t)
	target.framework = "mock"
	if _, err := pipeline.Generate(context.Background(), target); err == nil {
		t.Fatal("unsafe framework accepted")
	}
	if analyzer.calls != 0 {
		t.Fatalf("analyzer calls=%d", analyzer.calls)
	}
}

func TestProductionGenerationPipelineRendersMaintainableManagedCases(t *testing.T) {
	pipeline, target, _, _ := productionPipelineFixture(t)
	target.managed = true
	target.sourceRelativePath = "src/classify.cpp"
	target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := exactGeneratedSource(result.editSet.Files)
	if !ok {
		t.Fatal("managed source missing")
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 4096)
	if err != nil || len(document.Blocks) != len(result.vectors) {
		t.Fatalf("managed document blocks=%d vectors=%d err=%v\n%s", len(document.Blocks), len(result.vectors), err, source)
	}
	for _, block := range document.Blocks {
		if block.FunctionID != target.functionID || !strings.HasPrefix(block.CaseID, "utc_") {
			t.Fatalf("unmaintainable managed block: %+v", block)
		}
	}
}

func TestProductionGenerationPipelineSupportsReportBoundFunctionGeneration(t *testing.T) {
	pipeline, target, _, _ := productionPipelineFixture(t)
	target.managed = true
	target.sourceRelativePath = "src/classify.cpp"
	target.request.Scope = testgendomain.ScopeSymbol
	target.request.ManagedTargetID = target.functionID
	target.request.ManagedGapID = ""
	target.gapID = ""
	target.gap.Kind = solver.GapFunction
	target.gap.BranchID = ""
	target.gap.Outcome = ""
	target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := exactGeneratedSource(result.editSet.Files)
	if !ok {
		t.Fatal("function generation source missing")
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 4096)
	if err != nil || len(document.Blocks) == 0 {
		t.Fatalf("function generation did not render maintainable cases: blocks=%d err=%v", len(document.Blocks), err)
	}
}
