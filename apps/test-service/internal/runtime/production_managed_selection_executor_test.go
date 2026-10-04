package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionSelectedBaselineFixture struct{ data []byte }

func (fixture productionSelectedBaselineFixture) ReadCoverageBaseline(context.Context, string) ([]byte, error) {
	return append([]byte(nil), fixture.data...), nil
}

type productionSelectedBuildFixture struct{ value productionBuildSnapshot }

func (fixture productionSelectedBuildFixture) ResolveProductionBuild(context.Context, generationTarget) (productionBuildSnapshot, error) {
	return fixture.value, nil
}

type productionSelectedPlanFixture struct {
	registration productionValidationPlanRegistration
	released     bool
}

func (fixture *productionSelectedPlanFixture) RegisterValidationPlan(value productionValidationPlanRegistration) error {
	fixture.registration = cloneProductionValidationPlanRegistration(value)
	return nil
}

func (fixture *productionSelectedPlanFixture) ResolveValidationProcessPlan(_ context.Context, candidateID, taskID string, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (productionValidationProcessPlan, error) {
	if candidateID != fixture.registration.candidateID || taskID != fixture.registration.processTaskID || roots.CandidateID != candidateID || roots.ProcessTaskID != taskID {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	return productionValidationProcessPlan{
		taskID: taskID, serviceInstanceID: strings.Repeat("9", 32),
		tools:        map[string]string{"trusted-tool": strings.Repeat("a", 64)},
		specs:        map[testgenvalidate.Stage]processcontrol.Spec{stage: {Executable: "trusted-tool", Dir: roots.Build, Args: []string{string(stage)}}},
		recordLease:  func(context.Context, task.ProcessLease) error { return nil },
		releaseLease: func(context.Context, task.ProcessLease) error { return nil },
	}, nil
}

func (fixture *productionSelectedPlanFixture) ReleaseValidationPlan(candidateID, taskID string) {
	fixture.released = candidateID == fixture.registration.candidateID && taskID == fixture.registration.processTaskID
}

type productionSelectedStageFixture struct{ stages []testgenvalidate.Stage }

func (fixture *productionSelectedStageFixture) Execute(_ context.Context, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (testgenvalidate.StageEvidence, error) {
	fixture.stages = append(fixture.stages, stage)
	if stage == testgenvalidate.StageDiscover {
		return testgenvalidate.StageEvidence{DiscoveredCaseIDs: []string{roots.CandidateID}}, nil
	}
	return testgenvalidate.StageEvidence{}, nil
}

func TestProductionManagedSelectionExecutorRunsExactNativeSequenceAndAttestsPlan(t *testing.T) {
	run, index, set, _, target, _ := productionManagedFixture(t)
	metric := coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 1}, Lines: coveragedomain.Metric{Covered: 1, Total: 1}}
	index.Project.Summary, index.Files[0].Summary, index.Files[0].Functions[0].Summary = metric, metric, metric
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "src", "classify.cpp"), []byte("int classify(int value) { return value; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
		root: root, store: productionManagedSelectionStoreFixture{run: run},
		resolver: productionManagedSelectionResolverFixture{target: target}, current: productionManagedIndexFixture{index: index},
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, stages := &productionSelectedPlanFixture{}, &productionSelectedStageFixture{}
	executor, err := newProductionManagedSelectionExecutor(productionManagedSelectionExecutorConfig{
		authority: authority, baseline: productionSelectedBaselineFixture{data: productionValidationCoverage(0)},
		builds: productionSelectedBuildFixture{}, plans: plans, stages: stages,
		coverage: func(binding productionManagedSelectionBinding, _ testgenvalidate.StageEvidence) (testgenvalidate.SelectedCoverage, error) {
			return productionSelectedCoverage(binding.index)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := productionSelectedSelection(t, run, index.ToolchainID, set.Files)
	base := t.TempDir()
	roots := testgenvalidate.Roots{
		Source: filepath.Join(base, "source"), Build: filepath.Join(base, "build"), Artifacts: filepath.Join(base, "artifacts"),
		SnapshotDigest: strings.Repeat("8", 64),
	}
	for _, path := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executor.Execute(context.Background(), selection, testgenvalidate.SelectedBuild, roots); err == nil {
		t.Fatal("out-of-order selected build was accepted")
	}
	phases := []testgenvalidate.SelectedPhase{
		testgenvalidate.SelectedConfigure, testgenvalidate.SelectedBuild, testgenvalidate.SelectedDiscover,
		testgenvalidate.SelectedRun, testgenvalidate.SelectedCoveragePhase,
	}
	receipts := make([]testgenvalidate.SelectedPhaseReceipt, 0, len(phases))
	for _, phase := range phases {
		result, digest, err := executor.Execute(context.Background(), selection, phase, roots)
		if err != nil {
			t.Fatalf("phase %s: %v", phase, err)
		}
		if phase == testgenvalidate.SelectedDiscover && len(result.DiscoveredCaseIDs) == 0 ||
			phase == testgenvalidate.SelectedRun && (result.TestsRun == 0 || result.TestsPassed != result.TestsRun) ||
			phase == testgenvalidate.SelectedCoveragePhase && result.CollectorRelativePath != "selected-coverage.json" {
			t.Fatalf("phase %s result=%+v", phase, result)
		}
		receipts = append(receipts, testgenvalidate.SelectedPhaseReceipt{Phase: phase, CommandDigest: digest})
	}
	wantStages := []testgenvalidate.Stage{
		testgenvalidate.StageConfigure, testgenvalidate.StageCompile, testgenvalidate.StageDiscover,
		testgenvalidate.StageCandidate, testgenvalidate.StageSuite, testgenvalidate.StageCoverage,
	}
	if strings.Join(productionStageStrings(stages.stages), ",") != strings.Join(productionStageStrings(wantStages), ",") {
		t.Fatalf("stages=%v", stages.stages)
	}
	planDigest, err := productionSelectedPlanDigest(receipts)
	if err != nil || executor.VerifyPlan(context.Background(), selection, receipts, planDigest) != nil {
		t.Fatalf("plan digest=%s error=%v", planDigest, err)
	}
	executor.Release(selection, roots)
	if !plans.released {
		t.Fatal("native validation plan was not released")
	}
	if err := executor.VerifyPlan(context.Background(), selection, receipts, planDigest); err != nil {
		t.Fatalf("released attestation could not be reverified: %v", err)
	}
	tampered := append([]testgenvalidate.SelectedPhaseReceipt(nil), receipts...)
	tampered[0].CommandDigest = strings.Repeat("f", 64)
	if err := executor.VerifyPlan(context.Background(), selection, tampered, planDigest); err == nil {
		t.Fatal("tampered selected plan was accepted")
	}
}

func TestProductionSelectedCoverageRebuildsStableFileAndFunctionIdentities(t *testing.T) {
	run, index, _, _, target, _ := productionManagedFixture(t)
	metric := coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}, Lines: coveragedomain.Metric{Covered: 1, Total: 1}}
	index.Project.Summary, index.Files[0].Summary, index.Files[0].Functions[0].Summary = metric, metric, metric
	index.Toolchain = coveragedomain.ToolchainSnapshot{
		Platform: coveragedomain.PlatformWindows, Architecture: coveragedomain.ArchitectureX64,
		Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClangCL, Version: "22.1.8"},
		Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "22.1.8"},
		Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "22.1.8"},
		NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("a", 64),
	}
	document := coveragemodelv1.CoverageDocumentV1{
		SchemaVersion: coveragemodelv1.The10,
		Completeness:  coveragemodelv1.CoverageCompletenessV1{Outcome: coveragemodelv1.Available, Reasons: []coveragemodelv1.Reason{}},
		Provenance: coveragemodelv1.CoverageProvenanceV1{
			Platform: coveragemodelv1.Windows, Architecture: coveragemodelv1.X64,
			Compiler:          coveragemodelv1.CoverageCompilerV1{Family: coveragemodelv1.ClangCl, Version: "22.1.8"},
			Driver:            coveragemodelv1.CoverageDriverV1{Name: coveragemodelv1.FluffyLlvmCov, Version: "22.1.8"},
			Collector:         coveragemodelv1.CoverageCollectorV1{Name: coveragemodelv1.PurpleLlvmCov, Version: "22.1.8"},
			NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("a", 64),
		},
		Summary: coveragemodelv1.CoverageSummaryV1{Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}, Lines: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}},
		Files: []coveragemodelv1.CoverageFileV1{{
			URI: target.sourceRelativePath, Sha256: target.sourceDigest,
			Summary: coveragemodelv1.CoverageSummaryV1{Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}, Lines: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}},
			Lines:   []coveragemodelv1.CoverageLineV1{{Line: 1, Count: 1, Branches: coveragemodelv1.CoverageMetricV1{}}},
		}},
	}
	coverageJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	observation := coveragedomain.FunctionObservation{
		QualifiedName: "classify", LinkageName: index.Files[0].Functions[0].LinkageName, File: target.sourceRelativePath,
		Start: coveragedomain.SourceLocation{Line: 1, Column: 1}, End: coveragedomain.SourceLocation{Line: 1, Column: 10},
		ExecutionCount: 1, Lines: []coveragedomain.LineObservation{{Line: 1, Count: 1}},
	}
	index.Files[0].Functions[0].ID, err = coveragedetail.StableFunctionID(index.Files[0].ID, fmt.Sprintf("linkage:%d:%s:signature:", len(observation.LinkageName), observation.LinkageName))
	if err != nil {
		t.Fatal(err)
	}
	detailJSON, err := json.Marshal(productionCoverageDetailEvidence{Version: 1, Observations: []coveragedomain.FunctionObservation{observation}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := productionSelectedCoverageFromEvidence(productionManagedSelectionBinding{run: run, target: target, index: index}, testgenvalidate.StageEvidence{
		CoverageJSON: coverageJSON, CoverageDetailJSON: detailJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 1 || selected.Files[0].ID != index.Files[0].ID || len(selected.Functions) != 1 ||
		selected.Functions[0].ID != index.Files[0].Functions[0].ID || selected.Functions[0].Summary != metric {
		t.Fatalf("selected coverage=%+v", selected)
	}
}

func productionSelectedSelection(t *testing.T, run testgendomain.Run, toolchainID string, files []testgenrender.StagedFile) testgenpublish.ManagedSelection {
	t.Helper()
	outputs := make([]testgenpublish.SelectedOutput, len(files))
	for index, file := range files {
		outputs[index] = testgenpublish.SelectedOutput{Path: file.Path, Digest: file.AfterDigest}
	}
	sort.Slice(outputs, func(left, right int) bool { return outputs[left].Path < outputs[right].Path })
	encoded, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	return testgenpublish.ManagedSelection{
		RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, ToolchainID: toolchainID,
		SelectedOutputDigest: productionBytesDigest(append([]byte("managed-selected-v1\x00"), encoded...)),
		Files:                append([]testgenrender.StagedFile(nil), files...),
	}
}

func productionStageStrings(values []testgenvalidate.Stage) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
