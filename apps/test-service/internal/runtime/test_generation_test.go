package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
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
}

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
	if next == testgendomain.StateMinimizing && !d.complete {
		return GenerationStageResult{Next: testgendomain.StateRejected}, nil
	}
	if next == testgendomain.StateValidating && d.complete {
		artifact := task.Artifact{ID: strings.Repeat("4", 32), TaskID: run.TaskID, Kind: "test-generation-source", RelativePath: "tasks/" + run.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 12, SHA256: strings.Repeat("e", 64), CreatedAt: run.CreatedAt}
		candidate := testgendomain.Candidate{CaseID: strings.Repeat("5", 32), Kind: testgendomain.KindVerified, TargetSymbol: "fn:classify", Assertions: []testgendomain.Assertion{{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: strings.Repeat("6", 64)}}, StagedSourceArtifact: testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256}, CodeDigest: strings.Repeat("7", 64), PlannedEdits: []testgendomain.PlannedEdit{{Path: "tests/generated/classify_test.cpp", Operation: testgendomain.EditCreate, AfterDigest: strings.Repeat("8", 64)}}}
		return GenerationStageResult{Next: next, Candidates: []testgendomain.Candidate{candidate}, Artifacts: []task.Artifact{artifact}}, nil
	}
	if next == testgendomain.StateAwaitingConfirmation && d.complete {
		set := testgenpublish.CandidateSet{RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: []string{strings.Repeat("5", 32)}}
		return GenerationStageResult{Next: next, MinimizedCaseIDs: []string{strings.Repeat("5", 32)}, PreviewSet: &set}, nil
	}
	return GenerationStageResult{Next: next}, nil
}
func (d *generationDriverFixture) ProjectCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) (generationv15.TestGenerationCandidateV15, error) {
	return generationv15.TestGenerationCandidateV15{}, nil
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
		return testgenpublish.PublishPlan{RunID: set.RunID, SnapshotDigest: set.SnapshotDigest, CandidateSetDigest: strings.Repeat("b", 64), DiffDigest: strings.Repeat("c", 64), ConfirmationDigest: strings.Repeat("d", 64)}, nil
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
