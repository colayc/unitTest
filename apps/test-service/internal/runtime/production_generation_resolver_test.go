package runtime

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/discovery"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionResolverAuthorityFixture struct {
	context productionResolverContext
}

func (fixture *productionResolverAuthorityFixture) ResolveProductionContext(_ context.Context, projectID, generation, reportID string) (productionResolverContext, error) {
	if projectID != fixture.context.project.ID || generation != fixture.context.snapshot.Generation || reportID != fixture.context.index.ReportID {
		return productionResolverContext{}, errProductionGenerationUnavailable
	}
	result := fixture.context
	result.targets = cmake.CloneTargets(result.targets)
	return result, nil
}

type productionLayoutAuthorityFixture struct {
	layout productionTestLayout
}

func (fixture productionLayoutAuthorityFixture) ResolveProductionTestLayout(_ context.Context, _ productionResolverContext, binding productionCompileBinding, requested testgendomain.Framework) (productionTestLayout, error) {
	if binding.target.Name != "core" || requested != testgendomain.FrameworkAuto && requested != fixture.layout.framework {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	return fixture.layout, nil
}

func TestProductionGenerationResolverBuildsReportBoundFileTarget(t *testing.T) {
	resolver, authority, input, selected := productionResolverFixture(t)

	target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
	if err != nil {
		t.Fatalf("ResolveManagedStart() error = %v", err)
	}
	if !target.valid() || target.request.Scope != testgendomain.ScopeFile || target.request.ManagedTargetID != selected.FileID ||
		target.cmakeTargetDigest == target.toolchainID || target.cmakeTargetDigest != strings.Repeat("c", 64) ||
		target.toolchainID != authority.context.index.ToolchainID || target.headerPath != "include/choose.h" ||
		target.renderTarget.TestPath != "tests/generated/src/choose.c_test.c" || len(target.functions) != 2 {
		t.Fatalf("resolved target = %+v", target)
	}
	if got, want := target.analysis.Arguments, []string{"-std=c17", "-DFEATURE=1", "-Iinclude"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("analysis arguments = %#v, want %#v", got, want)
	}
	resolvedAgain, err := resolver.ResolveRequest(context.Background(), target.request)
	if err != nil || !reflect.DeepEqual(resolvedAgain, target) {
		t.Fatalf("ResolveRequest() = %+v, %v", resolvedAgain, err)
	}
}

func TestProductionGenerationResolverBuildsExactFunctionTarget(t *testing.T) {
	resolver, authority, input, selected := productionResolverFixture(t)
	function := authority.context.index.Files[0].Functions[1]
	input.Scope, input.FileID, input.FunctionID = generationv16.Symbol, nil, &function.ID
	selected.FunctionID, selected.FunctionName = function.ID, function.Name

	target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
	if err != nil {
		t.Fatalf("ResolveManagedStart() error = %v", err)
	}
	if target.request.Scope != testgendomain.ScopeSymbol || target.functionID != function.ID || target.linkageName != function.LinkageName || len(target.functions) != 0 {
		t.Fatalf("resolved function target = %+v", target)
	}
}

func TestProductionGenerationResolverRejectsChangedOrUnsupportedAuthority(t *testing.T) {
	resolver, authority, input, selected := productionResolverFixture(t)
	target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
	if err != nil {
		t.Fatal(err)
	}
	authority.context.index.Files[0].SourceSHA256 = strings.Repeat("0", 64)
	if _, err := resolver.ResolveRequest(context.Background(), target.request); err == nil {
		t.Fatal("ResolveRequest(changed source) error = nil")
	}

	resolver, authority, input, selected = productionResolverFixture(t)
	authority.context.index.Files[0].Functions[0].LinkageName = ""
	if _, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected); err == nil {
		t.Fatal("ResolveManagedStart(missing linkage) error = nil")
	}

	resolver, _, input, selected = productionResolverFixture(t)
	gap := strings.Repeat("8", 32)
	input.Scope, input.FileID, input.CoverageGapID = generationv16.TestGenerationScopeV16CoverageGap, nil, &gap
	selected.FunctionID, selected.FunctionName, selected.GapID = strings.Repeat("2", 32), "choose", gap
	if _, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected); err == nil {
		t.Fatal("ResolveManagedStart(branch gap) error = nil, want current fail-closed behavior")
	}
}

func productionResolverFixture(t *testing.T) (*productionGenerationTargetResolver, *productionResolverAuthorityFixture, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) {
	t.Helper()
	root, target := productionCompileBindingFixture(t)
	source, err := os.ReadFile(target.CompileUnits[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	generation := strings.Repeat("a", 64)
	reportID := strings.Repeat("b", 32)
	fileID, err := coveragedetail.StableFileID("core", "src/choose.c")
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := productionBytesDigest(source)
	functions := []coveragedetail.Function{
		{ID: strings.Repeat("2", 32), Name: "choose", LinkageName: "choose", Status: coveragedetail.StatusCurrent},
		{ID: strings.Repeat("3", 32), Name: "other", LinkageName: "other", Status: coveragedetail.StatusCurrent},
	}
	profile := cmake.BuildProfile{ID: target.ProfileID, ProjectID: "core", BinaryDir: root.NativePath, Configuration: "Debug"}
	authority := &productionResolverAuthorityFixture{context: productionResolverContext{
		snapshot: discovery.Snapshot{Generation: generation},
		project:  workspace.ProjectConfig{ID: "core", SourceDir: "."},
		profile:  profile, targets: []cmake.Target{target},
		index: coveragedetail.Index{
			WorkspaceGeneration: generation, ProjectID: "core", ReportID: reportID, RunID: strings.Repeat("4", 32),
			ToolchainID: strings.Repeat("5", 64), Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
			Files: []coveragedetail.File{{ID: fileID, RelativePath: "src/choose.c", SourceSHA256: sourceDigest, Status: coveragedetail.StatusCurrent, Functions: functions}},
		},
		baselineReportDigest: strings.Repeat("6", 64),
	}}
	layout := productionTestLayout{
		framework: testgendomain.FrameworkUnity, frameworkDigest: strings.Repeat("7", 64),
		renderTarget: testgenrender.TargetMetadata{
			TestTarget: "unit_tests", ProductionTarget: "core", FrameworkTarget: "unity",
			CMakePath: "tests/CMakeLists.txt", TestPath: "tests/generated/src/choose.c_test.c",
			ExistingCMake: "add_executable(unit_tests)\ntarget_link_libraries(unit_tests PRIVATE core unity)\n",
		},
	}
	resolver, err := newProductionGenerationTargetResolver(productionTargetResolverConfig{
		root: root, authority: authority, layout: productionLayoutAuthorityFixture{layout: layout},
		analyzerBundleDigest: strings.Repeat("8", 64), analyzerVersion: "fixed-clang-v1", processOwnerDigest: strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	file := fileID
	report := reportID
	input := generationv16.TestGenerationStartRequestV16{
		IdempotencyKey: strings.Repeat("e", 32), ProjectID: "core", WorkspaceGeneration: generation,
		Scope: generationv16.File, FileID: &file, CoverageReportID: &report, Framework: generationv16.Auto,
		Goals:   generationv16.TestGenerationGoalsV16{FunctionPercent: 100, LinePercent: 100, BranchPercent: 100},
		Budgets: generationv16.TestGenerationBudgetsV16{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	}
	selected := testgendomain.ManagedTarget{FileID: fileID, File: "src/choose.c", SourceDigest: sourceDigest}
	return resolver, authority, input, selected
}
