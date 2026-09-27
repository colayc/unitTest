package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/probe"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type generationDriverFixture struct {
	mu                   sync.Mutex
	stages               []testgendomain.State
	launches             int
	complete             bool
	candidateSetMismatch bool
	projectionMismatch   string
	block                <-chan struct{}
	ignoreCancel         bool
}

const fixtureGenerationDiff = "--- a/tests/generated/classify_test.cpp\n+++ b/tests/generated/classify_test.cpp\n@@ -0,0 +1 @@\n+TEST(classify, generated) {}\n--- a/CMakeLists.txt\n+++ b/CMakeLists.txt\n@@ -1 +1,2 @@\n add_executable(tests)\n+target_sources(tests PRIVATE tests/generated/classify_test.cpp)\n"

func TestGenerationFactoryOnlyRunsForTrustedReadyRuntime(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{Executable: os.Args[0], Identity: strings.Repeat("a", 64), Version: "test", Source: cmake.SourceDev}, nil
	}
	called := 0
	factory := func(*taskstore.Store, *Runtime) (GenerationServiceConfig, error) {
		called++
		return GenerationServiceConfig{
			Driver: &generationDriverFixture{complete: true}, Publisher: &generationPublisherFixture{complete: true},
			VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
				return r.SnapshotIdentity(), nil
			},
			VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
			VerifyProcess:  func(context.Context, string, string) error { return nil },
		}, nil
	}
	untrusted, err := Open(Config{DataDir: filepath.Join(base, "untrusted"), ServiceExecutable: os.Args[0], WorkspaceRoot: workspaceRoot, Platform: goruntime.GOOS, GenerationFactory: factory, dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	if untrusted.GenerationBackend() != nil || called != 0 {
		t.Fatal("untrusted runtime created generation provider")
	}
	if err := untrusted.Close(); err != nil {
		t.Fatal(err)
	}
	trusted, err := Open(Config{DataDir: filepath.Join(base, "trusted"), ServiceExecutable: os.Args[0], WorkspaceRoot: workspaceRoot, TrustedWorkspace: true, CoverageBackend: &runtimeCoverageBackend{}, Platform: goruntime.GOOS, GenerationFactory: factory, dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	defer trusted.Close()
	if trusted.GenerationBackend() == nil || !trusted.GenerationBackend().TestGenerationReady() || called != 1 {
		t.Fatal("trusted runtime did not wire ready generation provider")
	}
}

func (d *generationDriverFixture) Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	return generationv15.TestGenerationTargetListV15{Items: []generationv15.TestGenerationTargetV15{}}, nil
}
func (d *generationDriverFixture) Resolve(_ context.Context, input generationv15.TestGenerationStartRequestV15) (testgendomain.Request, error) {
	digest := strings.Repeat("a", 64)
	return testgendomain.Request{
		IdempotencyKey: input.IdempotencyKey, WorkspaceGeneration: input.WorkspaceGeneration,
		ProjectID: input.ProjectID, Scope: testgendomain.Scope(input.Scope), Framework: testgendomain.Framework(input.Framework),
		Goals:                 testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent},
		Budgets:               testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency},
		CompileSnapshotDigest: digest, CoverageSnapshotDigest: digest, SourceDigest: digest,
		CMakeTargetDigest: digest, FrameworkBundleDigest: digest, AnalyzerBundleDigest: digest,
		BaselineReportDigest: digest, ProcessOwnerDigest: digest,
	}, nil
}
func (d *generationDriverFixture) StageBudget(_ testgendomain.Run, next testgendomain.State) testgencoord.BudgetAmount {
	amount := testgencoord.BudgetAmount{MemoryMiB: 64, Processes: 1}
	if next == testgendomain.StateValidating && d.complete {
		amount.Candidates, amount.Artifacts = 1, 1
	}
	return amount
}
func (d *generationDriverFixture) RunStage(ctx context.Context, run testgendomain.Run, next testgendomain.State, authorize func(context.Context) error) (GenerationStageResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		return GenerationStageResult{}, errors.New("stage lacks absolute deadline")
	}
	if err := authorize(ctx); err != nil {
		return GenerationStageResult{}, err
	}
	d.mu.Lock()
	d.stages = append(d.stages, next)
	d.launches++
	d.mu.Unlock()
	if next == testgendomain.StateBaseline && d.block != nil {
		if d.ignoreCancel {
			<-d.block
			return GenerationStageResult{}, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return GenerationStageResult{}, ctx.Err()
		case <-d.block:
		}
	}
	if next == testgendomain.StateMinimizing && !d.complete {
		return GenerationStageResult{Next: testgendomain.StateRejected}, nil
	}
	if next == testgendomain.StateValidating && d.complete {
		artifact := task.Artifact{ID: strings.Repeat("4", 32), TaskID: run.TaskID, Kind: "test-generation-source", RelativePath: "tasks/" + run.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 12, SHA256: strings.Repeat("e", 64), CreatedAt: run.CreatedAt}
		candidate := testgendomain.Candidate{CaseID: strings.Repeat("5", 32), Kind: testgendomain.KindVerified, TargetSymbol: "fn:classify", Assertions: []testgendomain.Assertion{{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: strings.Repeat("6", 64)}}, StagedSourceArtifact: testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256}, CodeDigest: strings.Repeat("7", 64), BaselineCoverage: testgendomain.CoveragePercent{FunctionPercent: 20, LinePercent: 30, BranchPercent: 10}, DeltaCoveragePercent: testgendomain.CoveragePercent{FunctionPercent: 5, LinePercent: 4, BranchPercent: 3}, Diagnostics: []testgendomain.Diagnostic{{Code: testgendomain.DiagnosticCoverageGap, Severity: testgendomain.SeverityWarning, Reason: "uncovered-branch"}}, PlannedEdits: []testgendomain.PlannedEdit{{Path: "tests/generated/classify_test.cpp", Operation: testgendomain.EditCreate, AfterDigest: strings.Repeat("8", 64)}, {Path: "CMakeLists.txt", Operation: testgendomain.EditModify, BeforeDigest: strings.Repeat("9", 64), AfterDigest: strings.Repeat("a", 64)}}}
		return GenerationStageResult{Next: next, Candidates: []testgendomain.Candidate{candidate}, Artifacts: []task.Artifact{artifact}}, nil
	}
	if next == testgendomain.StateAwaitingConfirmation && d.complete {
		set := testgenpublish.CandidateSet{RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: []string{strings.Repeat("5", 32)}}
		return GenerationStageResult{Next: next, MinimizedCaseIDs: []string{strings.Repeat("5", 32)}, PreviewSet: &set}, nil
	}
	return GenerationStageResult{Next: next}, nil
}
func (d *generationDriverFixture) ProjectCandidate(_ context.Context, _ testgendomain.Run, candidate testgendomain.Candidate) (generationv15.TestGenerationCandidateV15, error) {
	edits := make([]generationv15.TestGenerationPlannedEditV15, 0, len(candidate.PlannedEdits))
	for _, edit := range candidate.PlannedEdits {
		item := generationv15.TestGenerationPlannedEditV15{Path: edit.Path, Operation: generationv15.Operation(edit.Operation), AfterDigest: edit.AfterDigest}
		if edit.BeforeDigest != "" {
			item.BeforeDigest = &edit.BeforeDigest
		}
		edits = append(edits, item)
	}
	diagnostics := make([]generationv15.TestGenerationDiagnosticV15, 0, len(candidate.Diagnostics))
	for _, diagnostic := range candidate.Diagnostics {
		item := generationv15.TestGenerationDiagnosticV15{Code: generationv15.TestGenerationDiagnosticCodeV15(diagnostic.Code), Severity: generationv15.Severity(diagnostic.Severity)}
		if diagnostic.Reason != "" {
			reason := generationv15.TestGenerationDiagnosticReasonV15(diagnostic.Reason)
			item.Reason = &reason
		}
		diagnostics = append(diagnostics, item)
	}
	projected := generationv15.TestGenerationCandidateV15{
		CandidateID: candidate.CaseID, Kind: generationv15.TestGenerationCandidateKindV15(candidate.Kind),
		CodeDigest: candidate.CodeDigest, ArtifactDigest: candidate.StagedSourceArtifact.Digest,
		AssertionProvenance: generationv15.TestGenerationAssertionProvenanceV15{Kind: generationv15.Kind(candidate.Assertions[0].Kind), EvidenceDigest: candidate.Assertions[0].EvidenceDigest},
		BaselineCoverage:    generationv15.TestGenerationCoverageV15{FunctionPercent: candidate.BaselineCoverage.FunctionPercent, LinePercent: candidate.BaselineCoverage.LinePercent, BranchPercent: candidate.BaselineCoverage.BranchPercent},
		DeltaCoverage:       generationv15.TestGenerationCoverageV15{FunctionPercent: candidate.DeltaCoveragePercent.FunctionPercent, LinePercent: candidate.DeltaCoveragePercent.LinePercent, BranchPercent: candidate.DeltaCoveragePercent.BranchPercent},
		PlannedEdits:        edits, Diagnostics: diagnostics,
	}
	d.mu.Lock()
	mismatch := d.projectionMismatch
	d.mu.Unlock()
	switch mismatch {
	case "oracle":
		projected.AssertionProvenance.EvidenceDigest = strings.Repeat("0", 64)
	case "coverage":
		projected.DeltaCoverage.BranchPercent++
	case "diagnostic":
		projected.Diagnostics[0].Severity = generationv15.Error
	}
	return projected, nil
}
func (d *generationDriverFixture) ValidateCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) error {
	return nil
}
func (d *generationDriverFixture) CandidateSet(_ context.Context, run testgendomain.Run, candidates []testgendomain.Candidate) (testgenpublish.CandidateSet, error) {
	if !d.complete || len(candidates) != 1 {
		return testgenpublish.CandidateSet{}, task.ErrInvalidArgument
	}
	ids := []string{candidates[0].CaseID}
	if d.candidateSetMismatch {
		ids = []string{strings.Repeat("9", 32)}
	}
	return testgenpublish.CandidateSet{RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: ids}, nil
}

func TestWarmAcceptRechecksAuthoritativeCandidatesBeforePublisherWrite(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	driver := &generationDriverFixture{complete: true}
	publisher := &generationPublisherFixture{complete: true}
	service, err := newGenerationService(GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: publisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	owner := strings.Repeat("e", 64)
	started, err := service.StartTestGeneration(context.Background(), owner, generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64), ProjectID: "core",
		Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := service.GetTestGenerationRun(context.Background(), owner, started.RunID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if run.State == generationv15.AwaitingConfirmation {
			break
		}
		if run.State == generationv15.Failed {
			t.Fatal("generation failed")
		}
		time.Sleep(time.Millisecond)
	}
	driver.mu.Lock()
	driver.candidateSetMismatch = true
	driver.mu.Unlock()
	_, err = service.AcceptTestGeneration(context.Background(), owner, generationv15.TestGenerationAcceptRequestV15{
		RunID: started.RunID, CandidateID: strings.Repeat("5", 32), ConfirmationDigest: strings.Repeat("d", 64),
	})
	if !errors.Is(err, task.ErrConflict) {
		t.Fatalf("warm accept = %v, want conflict", err)
	}
	publisher.mu.Lock()
	accepts := publisher.accepts
	publisher.mu.Unlock()
	if accepts != 0 {
		t.Fatalf("publisher wrote %d times before revalidation", accepts)
	}
}

type generationPublisherFixture struct {
	mu                         sync.Mutex
	recoveries, plans, accepts int
	complete                   bool
	diff                       string
	acceptErr                  error
}

func (p *generationPublisherFixture) Recover(context.Context) error {
	p.mu.Lock()
	p.recoveries++
	p.mu.Unlock()
	return nil
}
func (p *generationPublisherFixture) Plan(ctx context.Context, set testgenpublish.CandidateSet) (testgenpublish.PublishPlan, error) {
	p.mu.Lock()
	p.plans++
	p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return testgenpublish.PublishPlan{}, err
	}
	if p.complete {
		diff := p.diff
		if diff == "" {
			diff = fixtureGenerationDiff
		}
		sum := sha256.Sum256([]byte(diff))
		return testgenpublish.PublishPlan{RunID: set.RunID, SnapshotDigest: set.SnapshotDigest, CandidateSetDigest: strings.Repeat("b", 64), Diff: diff, DiffDigest: hex.EncodeToString(sum[:]), ConfirmationDigest: strings.Repeat("d", 64)}, nil
	}
	return testgenpublish.PublishPlan{}, task.ErrInvalidArgument
}
func (p *generationPublisherFixture) Receipt(context.Context, testgenpublish.AcceptRequest) (testgenpublish.Receipt, bool, error) {
	return testgenpublish.Receipt{}, false, nil
}
func (p *generationPublisherFixture) Accept(_ context.Context, request testgenpublish.AcceptRequest) (testgenpublish.Receipt, error) {
	p.mu.Lock()
	p.accepts++
	planned := p.plans > 0
	forced := p.acceptErr
	p.mu.Unlock()
	if forced != nil {
		return testgenpublish.Receipt{}, forced
	}
	if p.complete && !planned {
		return testgenpublish.Receipt{}, testgenpublish.ErrConflict
	}
	if p.complete {
		return testgenpublish.Receipt{RunID: request.RunID, SnapshotDigest: request.SnapshotDigest, CandidateSetDigest: request.CandidateSetDigest, DiffDigest: request.DiffDigest, ConfirmationDigest: request.ConfirmationDigest}, nil
	}
	return testgenpublish.Receipt{}, task.ErrInvalidArgument
}

func TestAcceptedGenerationRequiresDurableReceiptOnReplay(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	driver := &generationDriverFixture{complete: true}
	publisher := &generationPublisherFixture{complete: true}
	config := GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: publisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	}
	service, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("e", 64)
	input := generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64), ProjectID: "core",
		Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	}
	started, err := service.StartTestGeneration(context.Background(), owner, input)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := service.GetTestGenerationRun(context.Background(), owner, started.RunID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if run.State == generationv15.AwaitingConfirmation {
			break
		}
		if run.State == generationv15.Failed {
			t.Fatal("generation failed")
		}
		time.Sleep(time.Millisecond)
	}
	accept := generationv15.TestGenerationAcceptRequestV15{RunID: started.RunID, CandidateID: strings.Repeat("5", 32), ConfirmationDigest: strings.Repeat("d", 64)}
	if _, err := service.AcceptTestGeneration(context.Background(), owner, accept); err != nil {
		t.Fatal(err)
	}
	service.Close()
	broken := &generationPublisherFixture{complete: true, acceptErr: testgenpublish.ErrConflict}
	config.Publisher = broken
	restarted, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.AcceptTestGeneration(context.Background(), owner, accept); !errors.Is(err, testgenpublish.ErrConflict) {
		t.Fatalf("accepted replay without receipt = %v, want conflict", err)
	}
}

func TestGenerationRuntimeStagesAreDurableOwnedAndNeverPublishBeforeAccept(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	driver := &generationDriverFixture{}
	publisher := &generationPublisherFixture{}
	service, err := newGenerationService(GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: publisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !service.TestGenerationReady() {
		t.Fatal("complete provider was not ready")
	}
	owner := strings.Repeat("e", 64)
	input := generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64),
		ProjectID: "core", Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	}
	started, err := service.StartTestGeneration(context.Background(), owner, input)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var got generationv15.TestGenerationRunV15
	for time.Now().Before(deadline) {
		got, err = service.GetTestGenerationRun(context.Background(), owner, started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == generationv15.Rejected {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got.State != generationv15.Rejected {
		stored, _ := store.GetGeneration(context.Background(), started.RunID)
		events, _ := store.ReplayGenerationEvents(context.Background(), started.RunID, 0, 200)
		driver.mu.Lock()
		stages := append([]testgendomain.State(nil), driver.stages...)
		driver.mu.Unlock()
		t.Fatalf("run state = %q, durable=%+v, stages=%v, events=%+v", got.State, stored, stages, events)
	}
	if _, err := service.GetTestGenerationRun(context.Background(), strings.Repeat("f", 64), started.RunID); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("foreign owner get = %v", err)
	}
	stored, err := store.GetGeneration(context.Background(), started.RunID)
	if err != nil || stored.Request.SessionOwnerDigest != owner || stored.Revision != 7 {
		t.Fatalf("durable run = %+v, %v", stored, err)
	}
	events, err := store.ReplayGenerationEvents(context.Background(), started.RunID, 0, 200)
	if err != nil || len(events) < 7 {
		t.Fatalf("durable events = %d, %v", len(events), err)
	}
	driver.mu.Lock()
	stages := append([]testgendomain.State(nil), driver.stages...)
	launches := driver.launches
	driver.mu.Unlock()
	if strings.Join(statesAsStrings(stages), ",") != "baseline,analyzing,solving,rendering,validating,minimizing" || launches != 6 {
		t.Fatalf("stage order=%v, launch guards=%d", stages, launches)
	}
	publisher.mu.Lock()
	plans, accepts, recoveries := publisher.plans, publisher.accepts, publisher.recoveries
	publisher.mu.Unlock()
	if plans != 0 || accepts != 0 || recoveries != 1 {
		t.Fatalf("publisher calls plan=%d accept=%d recover=%d", plans, accepts, recoveries)
	}
	if err := service.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	resumedStages := len(driver.stages)
	driver.mu.Unlock()
	if resumedStages != len(stages) {
		t.Fatal("terminal run relaunched after restart")
	}
}

func statesAsStrings(values []testgendomain.State) []string {
	result := make([]string, len(values))
	for i, state := range values {
		result[i] = string(state)
	}
	return result
}

func TestGenerationCancelAndPrivateProgressReplayAreOwnerScoped(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	driver := &generationDriverFixture{block: make(chan struct{})}
	service, err := newGenerationService(GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: &generationPublisherFixture{}, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	owner, foreign := strings.Repeat("e", 64), strings.Repeat("f", 64)
	started, err := service.StartTestGeneration(context.Background(), owner, generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64), ProjectID: "core",
		Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelTestGeneration(context.Background(), foreign, started.RunID); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("foreign cancel = %v", err)
	}
	cancelled, err := service.CancelTestGeneration(context.Background(), owner, started.RunID)
	if err != nil || cancelled.State != generationv15.Cancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if _, err := service.ReplayTestGenerationEvents(context.Background(), foreign, generationv15.TestGenerationEventReplayRequestV15{RunID: started.RunID}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("foreign replay = %v", err)
	}
	page, err := service.ReplayTestGenerationEvents(context.Background(), owner, generationv15.TestGenerationEventReplayRequestV15{RunID: started.RunID})
	if err != nil || len(page.Items) < 3 || page.Items[len(page.Items)-1].State != generationv15.Cancelled {
		t.Fatalf("private replay = %+v, %v", page, err)
	}
	for i, item := range page.Items {
		if item.Sequence != int64(i+1) {
			t.Fatalf("sequence gap at %d: %+v", i, page.Items)
		}
	}
	if err := service.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := service.GetTestGenerationRun(context.Background(), owner, started.RunID)
	if err != nil || reloaded.State != generationv15.Cancelled {
		t.Fatalf("restart cancellation = %+v, %v", reloaded, err)
	}
}

func TestGenerationCancelWaitsForStageExitAndProcessAttestation(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	release := make(chan struct{})
	driver := &generationDriverFixture{block: release, ignoreCancel: true}
	var processLive atomic.Bool
	service, err := newGenerationService(GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: &generationPublisherFixture{}, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess: func(context.Context, string, string) error {
			if processLive.Load() {
				return errors.New("live child")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	owner := strings.Repeat("e", 64)
	started, err := service.StartTestGeneration(context.Background(), owner, generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64), ProjectID: "core",
		Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		driver.mu.Lock()
		entered := len(driver.stages) > 0
		driver.mu.Unlock()
		if entered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	driver.mu.Lock()
	entered := len(driver.stages) > 0
	driver.mu.Unlock()
	if !entered {
		t.Fatal("stage did not enter")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	if _, err := service.CancelTestGeneration(ctx, owner, started.RunID); err == nil {
		t.Fatal("cancel completed while stage ignored cancellation")
	}
	run, err := service.GetTestGenerationRun(context.Background(), owner, started.RunID)
	if err != nil || testgendomain.IsTerminal(testgendomain.State(run.State)) {
		t.Fatalf("premature terminal run = %+v, %v", run, err)
	}
	processLive.Store(true)
	close(release)
	released = true
	if _, err := service.CancelTestGeneration(context.Background(), owner, started.RunID); err == nil {
		t.Fatal("cancel completed with live process owner")
	}
	processLive.Store(false)
	finished, err := service.CancelTestGeneration(context.Background(), owner, started.RunID)
	if err != nil || finished.State != generationv15.Cancelled {
		t.Fatalf("attested cancel = %+v, %v", finished, err)
	}
	processLive.Store(true)
	if err := service.ResumeAll(context.Background()); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("restart adopted live cancelled process: %v", err)
	}
	if _, err := service.CancelTestGeneration(context.Background(), owner, started.RunID); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("idempotent cancel ignored live process: %v", err)
	}
	processLive.Store(false)
}

func TestGenerationProcessAttestationDeadlineIsBoundedEvenForBrokenVerifier(t *testing.T) {
	release := make(chan struct{})
	service := &generationService{verifyProcess: func(context.Context, string, string) error { <-release; return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.attestProcessGone(ctx, strings.Repeat("1", 32), strings.Repeat("a", 64)); !errors.Is(err, task.ErrConflict) {
		close(release)
		t.Fatalf("unbounded verifier result = %v", err)
	}
	close(release)
}

func TestGenerationAcceptRehydratesDurablePreviewAfterRestart(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	driver := &generationDriverFixture{complete: true}
	firstPublisher := &generationPublisherFixture{complete: true}
	config := GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: firstPublisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	}
	service, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("e", 64)
	input := generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64),
		ProjectID: "core", Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	}
	started, err := service.StartTestGeneration(context.Background(), owner, input)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var run generationv15.TestGenerationRunV15
	for time.Now().Before(deadline) {
		run, err = service.GetTestGenerationRun(context.Background(), owner, started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State == generationv15.AwaitingConfirmation || run.State == generationv15.Failed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if run.State != generationv15.AwaitingConfirmation {
		t.Fatalf("preview state = %q", run.State)
	}
	if run.Preview == nil || run.Preview.ConfirmationDigest != strings.Repeat("d", 64) {
		t.Fatalf("owner-scoped preview is missing: %+v", run.Preview)
	}
	if run.Preview.Diff == nil || *run.Preview.Diff != fixtureGenerationDiff {
		t.Fatalf("exact generated-test and CMake review diff missing: %+v", run.Preview)
	}
	persisted, err := store.GetGeneration(context.Background(), run.RunID)
	if err != nil || persisted.Record.Preview == nil || persisted.Record.Preview.ConfirmationDigest != strings.Repeat("d", 64) {
		t.Fatalf("durable preview = %+v, %v", persisted.Record, err)
	}
	firstPublisher.mu.Lock()
	firstPlans, firstAccepts := firstPublisher.plans, firstPublisher.accepts
	firstPublisher.mu.Unlock()
	if firstPlans != 1 || firstAccepts != 0 {
		t.Fatalf("published before accept: plan=%d accept=%d", firstPlans, firstAccepts)
	}
	service.Close()
	restartedPublisher := &generationPublisherFixture{complete: true}
	config.Publisher = restartedPublisher
	restarted, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.GetTestGenerationRun(context.Background(), owner, run.RunID)
	if err != nil || resumed.Preview == nil || resumed.Preview.Diff == nil || *resumed.Preview.Diff != fixtureGenerationDiff {
		t.Fatalf("restarted exact preview diff = %+v, %v", resumed.Preview, err)
	}
	review, err := restarted.ListTestGenerationCandidates(context.Background(), owner, generationv15.TestGenerationCandidateListRequestV15{RunID: run.RunID})
	if err != nil || len(review.Items) != 1 || len(review.Items[0].PlannedEdits) != 2 ||
		review.Items[0].PlannedEdits[0].Path != "tests/generated/classify_test.cpp" ||
		review.Items[0].PlannedEdits[0].AfterDigest != strings.Repeat("8", 64) ||
		review.Items[0].PlannedEdits[1].Path != "CMakeLists.txt" {
		t.Fatalf("restarted review metadata = %+v, %v", review, err)
	}
	if _, err := restarted.ListTestGenerationCandidates(context.Background(), strings.Repeat("f", 64), generationv15.TestGenerationCandidateListRequestV15{RunID: run.RunID}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("foreign review = %v", err)
	}
	for _, mismatch := range []string{"oracle", "coverage", "diagnostic"} {
		driver.mu.Lock()
		driver.projectionMismatch = mismatch
		driver.mu.Unlock()
		if _, err := restarted.ListTestGenerationCandidates(context.Background(), owner, generationv15.TestGenerationCandidateListRequestV15{RunID: run.RunID}); !errors.Is(err, task.ErrStorageUnavailable) {
			t.Fatalf("%s projection mismatch = %v", mismatch, err)
		}
	}
	driver.mu.Lock()
	driver.projectionMismatch = ""
	driver.mu.Unlock()
	accepted, err := restarted.AcceptTestGeneration(context.Background(), owner, generationv15.TestGenerationAcceptRequestV15{
		RunID: run.RunID, CandidateID: strings.Repeat("5", 32), ConfirmationDigest: strings.Repeat("d", 64), ConfirmCharacterization: false,
	})
	if err != nil || accepted.State != generationv15.Accepted {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	restartedPublisher.mu.Lock()
	replans, accepts := restartedPublisher.plans, restartedPublisher.accepts
	restartedPublisher.mu.Unlock()
	if replans != 1 || accepts != 1 {
		t.Fatalf("receipt replay calls plan=%d accept=%d", replans, accepts)
	}
	stored, err := store.GetGeneration(context.Background(), run.RunID)
	if err != nil || stored.State != testgendomain.StateAccepted || stored.Record.Preview == nil {
		t.Fatalf("accepted durable state = %+v, %v", stored, err)
	}
}

func TestLargeExactPreviewDiffSurvivesDatabaseRestartAndAccept(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
	}{
		{"html", strings.Repeat("+<>&\n", 52000)},
		{"backslash", "+" + strings.Repeat("\\", 261000) + "\n"},
		{"quote", "+" + strings.Repeat("\"", 261000) + "\n"},
		{"control", "+" + strings.Repeat("\x00", 261000) + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testLargeExactPreviewDiffSurvivesDatabaseRestartAndAccept(t, fixtureGenerationDiff+tc.suffix)
		})
	}
}

func testLargeExactPreviewDiffSurvivesDatabaseRestartAndAccept(t *testing.T, diff string) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	store, err := taskstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	// These near-limit diffs exercise JSON's worst-case escape expansion through
	// checkpoint, restart, preview retrieval, and accept.
	if len(diff) <= 260000 || len(diff) >= 262144 {
		t.Fatalf("test diff size = %d", len(diff))
	}
	driver := &generationDriverFixture{complete: true}
	publisher := &generationPublisherFixture{complete: true, diff: diff}
	config := GenerationServiceConfig{
		Store: store, Driver: driver, Publisher: publisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil },
		VerifyProcess:  func(context.Context, string, string) error { return nil },
	}
	service, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	owner := strings.Repeat("e", 64)
	started, err := service.StartTestGeneration(context.Background(), owner, generationv15.TestGenerationStartRequestV15{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64), ProjectID: "core",
		Scope: generationv15.Workspace, Framework: generationv15.Auto,
		Goals:   generationv15.TestGenerationGoalsV15{FunctionPercent: 70, LinePercent: 80, BranchPercent: 60},
		Budgets: generationv15.TestGenerationBudgetsV15{WallTimeMS: 60000, CandidateCount: 4, MemoryMiB: 64, Concurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var run generationv15.TestGenerationRunV15
	for time.Now().Before(deadline) {
		run, err = service.GetTestGenerationRun(context.Background(), owner, started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State == generationv15.AwaitingConfirmation || run.State == generationv15.Failed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if run.State != generationv15.AwaitingConfirmation || run.Preview == nil || run.Preview.Diff == nil || *run.Preview.Diff != diff {
		t.Fatalf("large checkpoint preview = %+v", run)
	}
	service.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = taskstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config.Store = store
	config.Publisher = &generationPublisherFixture{complete: true, diff: diff}
	restarted, err := newGenerationService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.GetTestGenerationRun(context.Background(), owner, started.RunID)
	if err != nil || resumed.Preview == nil || resumed.Preview.Diff == nil || *resumed.Preview.Diff != diff {
		t.Fatalf("large restarted preview = %+v, %v", resumed.Preview, err)
	}
	accepted, err := restarted.AcceptTestGeneration(context.Background(), owner, generationv15.TestGenerationAcceptRequestV15{
		RunID: started.RunID, CandidateID: strings.Repeat("5", 32), ConfirmationDigest: resumed.Preview.ConfirmationDigest,
	})
	if err != nil || accepted.State != generationv15.Accepted {
		t.Fatalf("large preview accept = %+v, %v", accepted, err)
	}
}
