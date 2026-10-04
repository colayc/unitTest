package testgenvalidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

type selectedFixtureRunner struct {
	called             []SelectedPhase
	fail               SelectedPhase
	tooMuch            bool
	symlink            bool
	malformed          bool
	wait               bool
	coverage           SelectedCoverage
	discoveredOverride []string
	executedOverride   []string
}

type selectedFixtureExecutor struct {
	runner     *selectedFixtureRunner
	verifyFail bool
}

func (executor *selectedFixtureExecutor) Execute(ctx context.Context, selection testgenpublish.ManagedSelection, phase SelectedPhase, roots Roots) (SelectedStageResult, string, error) {
	result, err := executor.runner.Run(ctx, phase, roots, SelectedCommand{})
	return result, selectedTestSHA([]byte("dynamic-plan:" + string(phase))), err
}

func (executor *selectedFixtureExecutor) VerifyPlan(_ context.Context, _ testgenpublish.ManagedSelection, phases []SelectedPhaseReceipt, digest string) error {
	if executor.verifyFail || len(phases) != len(selectedPhases) || selectedDynamicPlanDigest(phases) != digest {
		return ErrSelectedValidation
	}
	for index, phase := range phases {
		if phase.Phase != selectedPhases[index] || phase.CommandDigest != selectedTestSHA([]byte("dynamic-plan:"+string(phase.Phase))) {
			return ErrSelectedValidation
		}
	}
	return nil
}

func (r *selectedFixtureRunner) Run(ctx context.Context, phase SelectedPhase, roots Roots, command SelectedCommand) (SelectedStageResult, error) {
	r.called = append(r.called, phase)
	if r.wait {
		<-ctx.Done()
		return SelectedStageResult{}, ctx.Err()
	}
	if phase == r.fail {
		return SelectedStageResult{ExitCode: 1}, nil
	}
	if phase == SelectedDiscover {
		ids := []string{"utc_" + strings.Repeat("a", 32)}
		if r.discoveredOverride != nil {
			ids = r.discoveredOverride
		}
		return SelectedStageResult{Output: []byte("ok"), DiscoveredCaseIDs: ids}, nil
	}
	if phase == SelectedRun {
		ids := []string{"utc_" + strings.Repeat("a", 32)}
		if r.executedOverride != nil {
			ids = r.executedOverride
		}
		return SelectedStageResult{Output: []byte("ok"), TestsRun: 1, TestsPassed: 1, ExecutedCaseIDs: ids}, nil
	}
	if phase == SelectedCoveragePhase {
		name := filepath.Join(roots.Artifacts, "selected-coverage.json")
		if r.symlink {
			if err := os.Symlink(filepath.Join(roots.Source, "tests", "CMakeLists.txt"), name); err != nil {
				return SelectedStageResult{}, err
			}
		} else {
			data, _ := json.Marshal(r.coverage)
			if r.malformed {
				data = []byte(`{"project":`)
			}
			if err := os.WriteFile(name, data, 0600); err != nil {
				return SelectedStageResult{}, err
			}
		}
	}
	if r.tooMuch {
		return SelectedStageResult{Output: bytes.Repeat([]byte("x"), maxSelectedOutput+1)}, nil
	}
	if phase == SelectedCoveragePhase {
		return SelectedStageResult{Output: []byte("ok"), CollectorRelativePath: "selected-coverage.json"}, nil
	}
	return SelectedStageResult{Output: []byte("ok")}, nil
}

func selectedTestSHA(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func selectedFixture(t *testing.T) (*SelectedValidator, testgenpublish.ManagedSelection, *selectedFixtureRunner, *SelectionContext) {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.cpp"), []byte("int main() { return 1; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, sourceDigest, err := sourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(t.TempDir(), "trusted-tool")
	if err := os.WriteFile(tool, []byte("fixture tool"), 0600); err != nil {
		t.Fatal(err)
	}
	metric := coveragedomain.Metric{Covered: 2, Total: 3}
	summary := coveragedomain.Summary{Functions: metric, Lines: metric, Branches: metric}
	fileID, err := coveragedetail.StableFileID("project", "main.cpp")
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "main()")
	if err != nil {
		t.Fatal(err)
	}
	baseline := SelectedCoverage{Project: summary, Files: []SelectedFileCoverage{{ID: fileID, Summary: summary}}, Functions: []SelectedFunctionCoverage{{ID: functionID, FileID: fileID, Summary: summary}}}
	trusted := &SelectionContext{SnapshotDigest: strings.Repeat("1", 64), ToolchainID: "trusted-toolchain", SourceDigest: sourceDigest, Baseline: baseline}
	runner := &selectedFixtureRunner{coverage: baseline}
	commands := map[SelectedPhase]SelectedCommand{}
	for _, phase := range selectedPhases {
		commands[phase] = SelectedCommand{Executable: tool, Args: []string{string(phase)}, Dir: SelectedBuildDir}
	}
	v, err := NewSelectedValidator(SelectedConfig{SourceRoot: source, TempRoot: t.TempDir(), Resolve: func(context.Context, string) (SelectionContext, error) { return *trusted, nil }, Runner: runner, Commands: commands, ToolSHA256: map[string]string{tool: selectedTestSHA([]byte("fixture tool"))}, MACKey: bytes.Repeat([]byte{0x37}, 32), PhaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	files := []testgenrender.StagedFile{{Path: "tests/generated/main_test.cpp", Content: []byte("// unit-test-ide:managed-begin case=utc_" + strings.Repeat("a", 32) + " function=" + functionID + " symbol=main\nvoid test_main() {}\n// unit-test-ide:managed-end case=utc_" + strings.Repeat("a", 32) + "\n")}, {Path: "tests/CMakeLists.txt", Content: []byte("add_executable(test_main main_test.cpp)\n")}}
	for i := range files {
		files[i].AfterDigest = selectedTestSHA(files[i].Content)
	}
	selection := testgenpublish.ManagedSelection{RunID: strings.Repeat("c", 32), SnapshotDigest: trusted.SnapshotDigest, ToolchainID: trusted.ToolchainID, Files: files}
	selection.SelectedOutputDigest = selectedDigestForTest(files)
	return v, selection, runner, trusted
}

func selectedDigestForTest(files []testgenrender.StagedFile) string {
	outputs := []testgenpublish.SelectedOutput{{Path: "tests/CMakeLists.txt", Digest: files[1].AfterDigest}, {Path: "tests/generated/main_test.cpp", Digest: files[0].AfterDigest}}
	encoded, _ := json.Marshal(outputs)
	return selectedTestSHA(append([]byte("managed-selected-v1\x00"), encoded...))
}

func TestSelectedValidatorRunsIsolatedFixedPhasesAndBindsReceipt(t *testing.T) {
	v, selection, runner, _ := selectedFixture(t)
	receipt, err := v.Validate(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.called) != len(selectedPhases) || len(receipt) == 0 || len(receipt) > 1<<20 {
		t.Fatalf("not all phases ran: %v", runner.called)
	}
	if err := v.Verify(context.Background(), selection, receipt); err != nil {
		t.Fatal(err)
	}
	var body SelectedReceipt
	if err := json.Unmarshal(receipt, &body); err != nil {
		t.Fatal(err)
	}
	if body.SelectedOutputDigest != selection.SelectedOutputDigest || body.Coverage.Project.Lines.Covered != 2 || len(body.Phases) != len(selectedPhases) {
		t.Fatalf("receipt evidence missing: %+v", body)
	}
	for _, phase := range body.Phases {
		if phase.Status != "passed" || phase.StartedAt.IsZero() || phase.FinishedAt.Before(phase.StartedAt) || phase.CommandDigest == "" {
			t.Fatalf("phase not canonical: %+v", phase)
		}
	}
}

func TestSelectedValidatorRunsDynamicAttestedPlans(t *testing.T) {
	legacy, selection, runner, _ := selectedFixture(t)
	executor := &selectedFixtureExecutor{runner: runner}
	config := legacy.config
	config.Runner, config.Commands, config.ToolSHA256 = nil, nil, nil
	config.Executor = executor
	validator, err := NewSelectedValidator(config)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := validator.Validate(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Verify(context.Background(), selection, receipt); err != nil {
		t.Fatal(err)
	}
	var body SelectedReceipt
	if err := json.Unmarshal(receipt, &body); err != nil || body.CommandSetDigest != selectedDynamicPlanDigest(body.Phases) {
		t.Fatalf("receipt=%+v error=%v", body, err)
	}
	executor.verifyFail = true
	if receipt, err := validator.Validate(context.Background(), selection); err == nil || len(receipt) != 0 {
		t.Fatal("unattested dynamic plan accepted")
	}
}

func TestSelectedValidatorRejectsInputAndStateDriftBeforeRunning(t *testing.T) {
	for name, mutate := range map[string]func(*testgenpublish.ManagedSelection, *SelectionContext){
		"file digest": func(s *testgenpublish.ManagedSelection, _ *SelectionContext) {
			s.Files[0].AfterDigest = strings.Repeat("f", 64)
		},
		"selection digest": func(s *testgenpublish.ManagedSelection, _ *SelectionContext) {
			s.SelectedOutputDigest = strings.Repeat("f", 64)
		},
		"path traversal": func(s *testgenpublish.ManagedSelection, _ *SelectionContext) {
			s.Files[0].Path = "tests/generated/../main_test.cpp"
		},
		"duplicate path": func(s *testgenpublish.ManagedSelection, _ *SelectionContext) { s.Files[1].Path = s.Files[0].Path },
		"source drift": func(_ *testgenpublish.ManagedSelection, c *SelectionContext) {
			c.SourceDigest = strings.Repeat("2", 64)
		},
		"toolchain drift":  func(_ *testgenpublish.ManagedSelection, c *SelectionContext) { c.ToolchainID = "other-toolchain" },
		"malformed run id": func(s *testgenpublish.ManagedSelection, _ *SelectionContext) { s.RunID = strings.Repeat("z", 32) },
		"path-like toolchain id": func(s *testgenpublish.ManagedSelection, c *SelectionContext) {
			s.ToolchainID = "C:/private/path"
			c.ToolchainID = s.ToolchainID
		},
	} {
		t.Run(name, func(t *testing.T) {
			v, selection, runner, resolved := selectedFixture(t)
			mutate(&selection, resolved)
			if receipt, err := v.Validate(context.Background(), selection); err == nil || len(receipt) != 0 || len(runner.called) != 0 {
				t.Fatalf("unsafe selection ran: err=%v calls=%v", err, runner.called)
			}
		})
	}
}

func TestSelectedValidatorRejectsPhaseAndCoverageFailures(t *testing.T) {
	for name, mutate := range map[string]func(*selectedFixtureRunner){
		"nonzero":            func(r *selectedFixtureRunner) { r.fail = SelectedDiscover },
		"oversized output":   func(r *selectedFixtureRunner) { r.tooMuch = true },
		"malformed coverage": func(r *selectedFixtureRunner) { r.malformed = true },
		"symlink coverage":   func(r *selectedFixtureRunner) { r.symlink = true },
		"regression": func(r *selectedFixtureRunner) {
			r.coverage.Project.Lines.Covered = 1
			r.coverage.Files[0].Summary.Lines.Covered = 1
			r.coverage.Functions[0].Summary.Lines.Covered = 1
		},
		"invalid metric": func(r *selectedFixtureRunner) { r.coverage.Project.Lines.Covered = 4 },
	} {
		t.Run(name, func(t *testing.T) {
			v, selection, runner, _ := selectedFixture(t)
			mutate(runner)
			if receipt, err := v.Validate(context.Background(), selection); err == nil || len(receipt) != 0 {
				t.Fatalf("bad phase accepted: %v", err)
			}
		})
	}
}

func TestSelectedValidatorCancelTimeoutAndAuthenticatedNoop(t *testing.T) {
	v, selection, runner, resolved := selectedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Validate(ctx, selection); !errors.Is(err, context.Canceled) || len(runner.called) != 0 {
		t.Fatalf("cancel: %v calls=%v", err, runner.called)
	}
	runner.wait = true
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := v.Validate(ctx, selection); err == nil {
		t.Fatal("timeout accepted")
	}
	runner.wait = false
	runner.called = nil
	first, err := v.Validate(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	runner.called = nil
	resolved.PreviousReceipt = first
	second, err := v.Validate(context.Background(), selection)
	if err != nil || !bytes.Equal(first, second) || len(runner.called) != 0 {
		t.Fatalf("authenticated no-op failed: %v calls=%v", err, runner.called)
	}
	forged := bytes.Clone(first)
	forged[len(forged)-5] ^= 1
	if v.Verify(context.Background(), selection, forged) == nil {
		t.Fatal("tampered receipt verified")
	}
	resolved.PreviousReceipt = forged
	if _, err := v.Validate(context.Background(), selection); err == nil {
		t.Fatal("forged no-op accepted")
	}
	resolved.PreviousReceipt = first
	resolved.SourceDigest = strings.Repeat("9", 64)
	if v.Verify(context.Background(), selection, first) == nil {
		t.Fatal("source-drifted receipt verified")
	}
}

func TestSelectedValidatorRejectsAmbiguousCommand(t *testing.T) {
	v, _, _, _ := selectedFixture(t)
	config := v.config
	config.Commands[SelectedBuild] = SelectedCommand{Executable: config.Commands[SelectedBuild].Executable, Args: []string{"&&", "evil"}, Dir: SelectedBuildDir}
	if _, err := NewSelectedValidator(config); err == nil {
		t.Fatal("shell-like command accepted")
	}
}

func TestSelectedValidatorRejectsAllowlistedShellExecutable(t *testing.T) {
	v, _, _, _ := selectedFixture(t)
	tool := filepath.Join(t.TempDir(), "cmd.exe")
	if err := os.WriteFile(tool, []byte("fixture shell"), 0600); err != nil {
		t.Fatal(err)
	}
	config := v.config
	config.ToolSHA256 = map[string]string{tool: selectedTestSHA([]byte("fixture shell"))}
	config.Commands = make(map[SelectedPhase]SelectedCommand)
	for _, phase := range selectedPhases {
		config.Commands[phase] = SelectedCommand{Executable: tool, Args: []string{string(phase)}, Dir: SelectedBuildDir}
	}
	if _, err := NewSelectedValidator(config); err == nil {
		t.Fatal("shell executable accepted")
	}
}

func TestSelectedValidatorRejectsUnselectedDiscoveryAndEmptyFileSummary(t *testing.T) {
	v, selection, runner, _ := selectedFixture(t)
	runner.discoveredOverride = []string{"utc_" + strings.Repeat("d", 32)}
	if _, err := v.Validate(context.Background(), selection); err == nil {
		t.Fatal("unselected case was executed")
	}
	v, selection, runner, trusted := selectedFixture(t)
	trusted.Baseline.Project = coveragedomain.Summary{}
	trusted.Baseline.Files[0].Summary = coveragedomain.Summary{}
	trusted.Baseline.Functions[0].Summary = coveragedomain.Summary{}
	runner.coverage = trusted.Baseline
	if _, err := v.Validate(context.Background(), selection); err != nil {
		t.Fatalf("valid zero baseline rejected: %v", err)
	}
}

func TestSelectedValidatorRejectsDifferentCaseWithSameRunCount(t *testing.T) {
	v, selection, runner, _ := selectedFixture(t)
	runner.executedOverride = []string{"utc_" + strings.Repeat("d", 32)}
	if _, err := v.Validate(context.Background(), selection); err == nil {
		t.Fatal("unselected case satisfied run count")
	}
}

func TestSelectedValidatorRejectsFunctionSummaryOutsideFile(t *testing.T) {
	v, selection, runner, trusted := selectedFixture(t)
	trusted.Baseline.Functions[0].Summary.Lines.Total = 4
	runner.coverage = trusted.Baseline
	if _, err := v.Validate(context.Background(), selection); err == nil || len(runner.called) != 0 {
		t.Fatalf("inconsistent baseline accepted: err=%v calls=%v", err, runner.called)
	}
}

func TestSelectedValidatorAcceptsStableDetailIDsAndRejectsDigestShapedIDs(t *testing.T) {
	v, selection, runner, trusted := selectedFixture(t)
	receipt, err := v.Validate(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	var body SelectedReceipt
	if err := json.Unmarshal(receipt, &body); err != nil {
		t.Fatal(err)
	}
	fileID, _ := coveragedetail.StableFileID("project", "main.cpp")
	functionID, _ := coveragedetail.StableFunctionID(fileID, "main()")
	if body.Coverage.Files[0].ID != fileID || body.Coverage.Functions[0].ID != functionID {
		t.Fatalf("coverage IDs are not stable detail IDs: %+v", body.Coverage)
	}
	v, selection, runner, trusted = selectedFixture(t)
	trusted.Baseline.Files[0].ID = strings.Repeat("a", 64)
	trusted.Baseline.Functions[0].FileID = trusted.Baseline.Files[0].ID
	trusted.Baseline.Functions[0].ID = strings.Repeat("b", 64)
	runner.coverage = trusted.Baseline
	if _, err := v.Validate(context.Background(), selection); err == nil || len(runner.called) != 0 {
		t.Fatalf("64-hex pseudo-IDs accepted: err=%v calls=%v", err, runner.called)
	}
}

func TestPreparedSelectedRunnerUsesClosedPreparedProcessBoundary(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "fixed-tool")
	if err := os.WriteFile(tool, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	roots := Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}
	for _, dir := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1)}
	process.done <- processcontrol.Result{}
	close(process.done)
	close(process.output)
	native := &fakeRunner{process: process}
	runner := PreparedSelectedRunner{Runner: native, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{tool: selectedTestSHA([]byte("fixed-tool"))}, RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil }, Interpret: func(SelectedPhase, []byte) (SelectedStageResult, error) { return SelectedStageResult{}, nil }}
	_, err := runner.Run(context.Background(), SelectedBuild, roots, SelectedCommand{Executable: tool, Args: []string{"--build"}, Dir: SelectedBuildDir})
	if err != nil || native.prepared != 1 || !native.lastSpec.ClosedEnvironment || native.lastSpec.Executable != tool || native.lastSpec.Dir != roots.Build || len(native.lastSpec.Args) != 1 || native.lastSpec.Args[0] != "--build" {
		t.Fatalf("unsafe process adapter: err=%v spec=%+v", err, native.lastSpec)
	}
}
