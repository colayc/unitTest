package testrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/buildcontract"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestDiscoveryCoordinatorPreservesBuildValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*coordinatorPreparedBuild)
		want error
	}{
		{
			name: "workspace generation",
			edit: func(prepared *coordinatorPreparedBuild) {
				prepared.generation = strings.Repeat("9", 64)
			},
			want: buildcontract.ErrWorkspaceChanged,
		},
		{
			name: "project",
			edit: func(prepared *coordinatorPreparedBuild) {
				prepared.project.ID = "other"
			},
			want: buildcontract.ErrProjectNotFound,
		},
		{
			name: "profile",
			edit: func(prepared *coordinatorPreparedBuild) {
				prepared.profile.ID = strings.Repeat("9", 64)
			},
			want: buildcontract.ErrBuildProfileNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorRunFixture(t)
			test.edit(fixture.prepared)
			_, err := fixture.coordinator.StartDiscovery(
				context.Background(),
				DiscoveryRequest{
					IdempotencyKey:      fixture.request.IdempotencyKey,
					WorkspaceGeneration: fixture.request.WorkspaceGeneration,
					ProjectID:           fixture.request.ProjectID,
					BuildProfileID:      fixture.request.BuildProfileID,
					TargetIDs:           fixture.request.TargetIDs,
					Jobs:                fixture.request.Jobs,
					Timeout:             fixture.request.Timeout,
				},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("StartDiscovery error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCoordinatorContinuesBuildIntoPinnedFrameworkRunSteps(
	t *testing.T,
) {
	fixture := newCoordinatorRunFixture(t)
	started, run, err := fixture.coordinator.StartRun(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != task.KindTestRun ||
		run.TaskID != started.ID ||
		run.CatalogRevision != fixture.catalog.Revision ||
		fixture.tasks.calls != 1 ||
		fixture.prepareCalls != 1 {
		t.Fatalf(
			"started Task/TestRun = %#v / %#v",
			started,
			run,
		)
	}
	internal := fixture.tasks.request
	if internal.Continuation == nil ||
		internal.ResultInterpreter == nil ||
		internal.TestRun == nil ||
		len(internal.Plan.Steps) != 2 {
		t.Fatalf("internal StartRequest = %#v", internal)
	}
	current := task.Task{ID: started.ID, Kind: task.KindTestRun}
	first, err := internal.Continuation.AfterStep(
		context.Background(),
		current,
		internal.Plan.Steps[0],
		task.StepResult{Verdict: task.StepVerdictSucceeded},
	)
	if err != nil || len(first.Steps) != 0 ||
		fixture.refresher.calls != 0 {
		t.Fatalf("configure continuation = %#v, %v", first, err)
	}
	continued, err := internal.Continuation.AfterStep(
		context.Background(),
		current,
		internal.Plan.Steps[1],
		task.StepResult{Verdict: task.StepVerdictSucceeded},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(continued.Steps) != 1 ||
		continued.Steps[0].Kind != task.StepTestDiscovery ||
		fixture.refresher.calls != 1 ||
		fixture.prepared.allowCalls != 1 {
		t.Fatalf(
			"discovery continuation = %#v, refresh=%d pins=%d",
			continued,
			fixture.refresher.calls,
			fixture.prepared.allowCalls,
		)
	}
	discoveryStep := continued.Steps[0]
	verdict, err := internal.ResultInterpreter.Interpret(
		context.Background(),
		current,
		discoveryStep,
		task.ProcessResult{ExitCode: 0},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("discovery verdict = %q, %v", verdict, err)
	}
	eventSource := internal.ResultInterpreter.(task.DomainEventSource)
	discoveryEvents := eventSource.DrainDomainEvents()
	if len(discoveryEvents) != 1 ||
		discoveryEvents[0].Type !=
			task.EventTestDiscoveryStarted {
		t.Fatalf("discovery events = %#v", discoveryEvents)
	}
	var discoveryStarted struct {
		ProjectID string `json:"projectId"`
		ProfileID string `json:"profileId"`
	}
	decodeDomainPayload(
		t,
		discoveryEvents[0],
		&discoveryStarted,
	)
	if discoveryStarted.ProjectID != fixture.catalog.ProjectID ||
		discoveryStarted.ProfileID != fixture.catalog.ProfileID {
		t.Fatalf(
			"discovery started payload = %#v",
			discoveryStarted,
		)
	}
	continued, err = internal.Continuation.AfterStep(
		context.Background(),
		current,
		discoveryStep,
		task.StepResult{Verdict: verdict},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(continued.Steps) != 2 ||
		continued.Steps[0].Kind != task.StepTestRun ||
		continued.Steps[1].Kind != task.StepTestRun ||
		len(continued.Steps[0].Process.Batch) != 1 ||
		len(continued.Steps[1].Process.Batch) != 1 ||
		fixture.prepared.allowCalls != 1 {
		t.Fatalf(
			"run continuation = %#v, pins=%d",
			continued,
			fixture.prepared.allowCalls,
		)
	}
	catalogEvents := eventSource.DrainDomainEvents()
	if len(catalogEvents) !=
		len(fixture.catalog.Containers)+1 ||
		catalogEvents[len(catalogEvents)-1].Type !=
			task.EventTestCatalogPublished {
		t.Fatalf("catalog events = %#v", catalogEvents)
	}
}

func TestRunExecutionBuildsAndInterpretsConcurrentWave(t *testing.T) {
	firstContainer, err := testdomain.ContainerID(
		"project",
		"first",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondContainer, err := testdomain.ContainerID(
		"project",
		"second",
	)
	if err != nil {
		t.Fatal(err)
	}
	planned := PlannedRun{
		Invocations: []PlannedInvocation{
			opaquePlannedInvocation(
				"test-000001",
				firstContainer,
				"first",
			),
			opaquePlannedInvocation(
				"test-000002",
				secondContainer,
				"second",
			),
		},
		Waves: []ScheduleWave{{Jobs: []ScheduledJob{
			{
				ID: "test-000001", ContainerID: firstContainer,
				Iteration: 1,
			},
			{
				ID: "test-000002", ContainerID: secondContainer,
				Iteration: 1,
			},
		}}},
	}
	steps, invocationSteps, waveInvocations, err :=
		buildRunWaveSteps(planned)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 ||
		steps[0].ID != "run-wave-000001" ||
		len(steps[0].Process.Batch) != 2 ||
		steps[0].Process.Batch[0].ID != "test-000001" ||
		steps[0].Process.Batch[1].ID != "test-000002" {
		t.Fatalf("wave steps = %#v", steps)
	}
	store := newResultAppender()
	interpreter, err := NewInterpreter(
		strings.Repeat("1", 32),
		store,
		planned,
	)
	if err != nil {
		t.Fatal(err)
	}
	execution := &runExecution{
		interpreter:     interpreter,
		invocationSteps: invocationSteps,
		waveInvocations: waveInvocations,
	}
	verdict, err := execution.Interpret(
		context.Background(),
		task.Task{ID: strings.Repeat("2", 32)},
		steps[0],
		task.ProcessResult{Children: []task.ProcessChildResult{
			{ID: "test-000002", TimedOut: true},
			{ID: "test-000001"},
		}},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %q, %v", verdict, err)
	}
	results := store.results()
	outcomes := make(
		map[testdomain.ID]testdomain.ItemOutcome,
		len(results),
	)
	for _, result := range results {
		outcomes[result.ItemID] = result.Outcome
	}
	if len(results) != 2 ||
		outcomes[firstContainer] != testdomain.ItemPassed ||
		outcomes[secondContainer] != testdomain.ItemTimedOut {
		t.Fatalf("wave results = %#v", results)
	}
}

func opaquePlannedInvocation(
	id string,
	containerID testdomain.ID,
	name string,
) PlannedInvocation {
	step := task.ExecutionStep{
		ID: id, Kind: task.StepTestRun,
		Process: task.ProcessSpec{
			Executable: "ctest",
			Args:       []string{"-R", name},
			Dir:        ".",
		},
		Public: task.CommandSummary{
			Executable: "ctest",
			Args:       []string{"-R", name},
		},
	}
	return PlannedInvocation{
		Job: ScheduledJob{
			ID: id, ContainerID: containerID,
			Iteration: 1,
		},
		Step:        step,
		ContainerID: containerID,
		Framework:   testdomain.FrameworkOpaqueCTest,
		ExpectedCases: []testframework.ExpectedCase{{
			ItemID: containerID, LogicalName: name,
		}},
		Timeout: time.Second,
	}
}

func TestCoordinatorRejectsStaleCatalogBeforeTaskCreation(t *testing.T) {
	fixture := newCoordinatorRunFixture(t)
	fixture.request.CatalogRevision = strings.Repeat("f", 64)
	if _, _, err := fixture.coordinator.StartRun(
		context.Background(),
		fixture.request,
	); !errors.Is(err, testdomain.ErrCatalogStale) {
		t.Fatalf("StartRun() error = %v", err)
	}
	if fixture.tasks.calls != 0 || fixture.prepared.releaseCalls != 1 {
		t.Fatalf(
			"stale request reached task=%d releases=%d",
			fixture.tasks.calls,
			fixture.prepared.releaseCalls,
		)
	}
}

func TestCoordinatorResumesQueuedRunWithRevalidatedRuntimeState(
	t *testing.T,
) {
	fixture := newCoordinatorRunFixture(t)
	started, run, err := fixture.coordinator.StartRun(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	persisted := started
	persisted.IdempotencyKey = fixture.request.IdempotencyKey
	persisted.Request = append(
		[]byte(nil),
		fixture.tasks.request.Request...,
	)
	persisted.WorkspaceGeneration =
		fixture.request.WorkspaceGeneration
	persisted.PlanFingerprint =
		fixture.tasks.request.Plan.Fingerprint
	persisted.Timeout = fixture.request.Timeout
	persisted.Status = task.StatusQueued

	resumed, err := fixture.coordinator.ResumeRun(
		context.Background(),
		persisted,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != persisted.ID ||
		fixture.prepareCalls != 2 ||
		fixture.tasks.resumeCalls != 1 ||
		fixture.tasks.resumeRequest.Task.ID != persisted.ID ||
		fixture.tasks.resumeRequest.Continuation == nil ||
		fixture.tasks.resumeRequest.ResultInterpreter == nil ||
		fixture.runs.run.RunID != run.RunID {
		t.Fatalf(
			"resumed Task = %#v, prepare=%d resume=%d request=%#v",
			resumed,
			fixture.prepareCalls,
			fixture.tasks.resumeCalls,
			fixture.tasks.resumeRequest,
		)
	}
}

func TestCoordinatorResumesQueuedDiscoveryWithRevalidatedRuntimeState(
	t *testing.T,
) {
	fixture := newCoordinatorRunFixture(t)
	request := DiscoveryRequest{
		IdempotencyKey:      strings.Repeat("3", 32),
		WorkspaceGeneration: fixture.prepared.generation,
		ProjectID:           fixture.catalog.ProjectID,
		BuildProfileID:      fixture.catalog.ProfileID,
		Jobs:                2,
		Timeout:             time.Minute,
	}
	started, err := fixture.coordinator.StartDiscovery(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	persisted := started
	persisted.IdempotencyKey = request.IdempotencyKey
	persisted.Request = append(
		[]byte(nil),
		fixture.tasks.request.Request...,
	)
	persisted.WorkspaceGeneration =
		request.WorkspaceGeneration
	persisted.PlanFingerprint =
		fixture.tasks.request.Plan.Fingerprint
	persisted.Timeout = request.Timeout
	persisted.Status = task.StatusQueued

	resumed, err := fixture.coordinator.ResumeDiscovery(
		context.Background(),
		persisted,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != persisted.ID ||
		fixture.prepareCalls != 2 ||
		fixture.tasks.resumeCalls != 1 ||
		fixture.tasks.resumeRequest.Task.ID != persisted.ID ||
		fixture.tasks.resumeRequest.Continuation == nil ||
		fixture.tasks.resumeRequest.ResultInterpreter == nil {
		t.Fatalf(
			"resumed discovery = %#v, prepare=%d resume=%d request=%#v",
			resumed,
			fixture.prepareCalls,
			fixture.tasks.resumeCalls,
			fixture.tasks.resumeRequest,
		)
	}
}

func TestCoordinatorDoesNotAppendProcessForDeletedSelectedID(
	t *testing.T,
) {
	fixture := newCoordinatorRunFixture(t)
	started, _, err := fixture.coordinator.StartRun(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := fixture.catalog.Clone()
	refreshed.Items = refreshed.Items[:1]
	refreshed.Revision = strings.Repeat("e", 64)
	fixture.refresher.result.Catalog = refreshed
	internal := fixture.tasks.request
	continued, err := internal.Continuation.AfterStep(
		context.Background(),
		task.Task{ID: started.ID, Kind: task.KindTestRun},
		internal.Plan.Steps[len(internal.Plan.Steps)-1],
		task.StepResult{Verdict: task.StepVerdictSucceeded},
	)
	if err != nil || len(continued.Steps) != 1 {
		t.Fatalf(
			"discovery continuation = %#v, %v",
			continued,
			err,
		)
	}
	discoveryStep := continued.Steps[0]
	verdict, err := internal.ResultInterpreter.Interpret(
		context.Background(),
		task.Task{ID: started.ID, Kind: task.KindTestRun},
		discoveryStep,
		task.ProcessResult{ExitCode: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	continued, err = internal.Continuation.AfterStep(
		context.Background(),
		task.Task{ID: started.ID, Kind: task.KindTestRun},
		discoveryStep,
		task.StepResult{Verdict: verdict},
	)
	if !errors.Is(err, task.ErrInvalidArgument) ||
		len(continued.Steps) != 0 ||
		fixture.prepared.allowCalls != 1 {
		t.Fatalf(
			"deleted-ID continuation = %#v, %v, pins=%d",
			continued,
			err,
			fixture.prepared.allowCalls,
		)
	}
}

type coordinatorRunFixture struct {
	coordinator  *Coordinator
	catalog      testdomain.Catalog
	request      RunRequest
	tasks        *coordinatorTaskStarter
	runs         *coordinatorRunStore
	prepared     *coordinatorPreparedBuild
	refresher    *coordinatorCatalogRefresher
	prepareCalls int
}

func newCoordinatorRunFixture(t *testing.T) *coordinatorRunFixture {
	return newCoordinatorRunFixtureWithCases(t, "CaseA", "CaseB")
}

func newCoordinatorRunFixtureWithCases(
	t *testing.T,
	caseNames ...string,
) *coordinatorRunFixture {
	t.Helper()
	catalog, container, cases := plannerCatalog(
		t,
		testdomain.FrameworkCppUTest,
		"framework-tests",
		"Group",
		caseNames...,
	)
	profile := cmake.BuildProfile{
		ID: catalog.ProfileID, ProjectID: catalog.ProjectID,
		BinaryDir: t.TempDir(),
	}
	prepared := &coordinatorPreparedBuild{
		plan: task.ExecutionPlan{
			Version: 1,
			Steps: []task.ExecutionStep{
				{
					ID: "configure", Kind: task.StepConfigure,
					Process: task.ProcessSpec{
						Executable: "cmake", Dir: profile.BinaryDir,
					},
					Public: task.CommandSummary{Executable: "cmake"},
				},
				{
					ID: "build", Kind: task.StepBuild,
					Process: task.ProcessSpec{
						Executable: "cmake", Dir: profile.BinaryDir,
					},
					Public: task.CommandSummary{Executable: "cmake"},
				},
			},
		},
		generation: strings.Repeat("d", 64),
		project: workspace.ProjectConfig{
			ID:        catalog.ProjectID,
			SourceDir: "project",
		},
		profile: profile,
		toolchain: toolchain.Instance{
			ID: "msvc", Family: toolchain.FamilyMSVC,
		},
	}
	prepared.plan.Fingerprint = task.FingerprintPlan(prepared.plan)
	adapter := &coordinatorAdapter{}
	refresher := &coordinatorCatalogRefresher{
		embeddedGeneration: prepared.generation,
		result: RefreshedCatalog{
			Catalog: catalog,
			Bindings: []ContainerBinding{{
				ContainerID: container.ID,
				Descriptor: plannerDescriptor(
					t,
					container.CTestLogicalName,
				),
				Adapter: adapter,
			}},
		},
	}
	tasks := &coordinatorTaskStarter{
		taskID: strings.Repeat("1", 32),
		now: time.Date(
			2026,
			7,
			31,
			10,
			0,
			0,
			0,
			time.UTC,
		),
	}
	runs := &coordinatorRunStore{}
	tasks.runs = runs
	runner, _ := plannerCTestRunner(t)
	selected := make([]testdomain.ID, len(cases))
	for index, item := range cases {
		selected[index] = item.ID
	}
	fixture := &coordinatorRunFixture{
		catalog: catalog, tasks: tasks, runs: runs,
		prepared: prepared, refresher: refresher,
		request: RunRequest{
			IdempotencyKey:      strings.Repeat("2", 32),
			WorkspaceGeneration: prepared.generation,
			ProjectID:           catalog.ProjectID,
			BuildProfileID:      catalog.ProfileID,
			CatalogRevision:     catalog.Revision,
			Jobs:                4,
			Timeout:             time.Minute,
			RepeatCount:         1,
			MaxConcurrency:      2,
			Selection: testdomain.Selection{
				Mode:    testdomain.SelectionItems,
				ItemIDs: selected,
			},
		},
	}
	coordinator, err := NewCoordinator(CoordinatorConfig{
		PrepareBuild: func(
			context.Context,
			BuildRequest,
		) (PreparedBuild, error) {
			fixture.prepareCalls++
			return prepared, nil
		},
		Catalogs:  &coordinatorCatalogReader{catalog: catalog},
		Refresher: refresher,
		Tasks:     tasks,
		Runs:      runs,
		Runner:    runner,
		Limits: testdomain.Limits{
			MaxSelectionSize: 100_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.coordinator = coordinator
	return fixture
}

type coordinatorPreparedBuild struct {
	plan         task.ExecutionPlan
	generation   string
	project      workspace.ProjectConfig
	profile      cmake.BuildProfile
	toolchain    toolchain.Instance
	allowCalls   int
	releaseCalls int
}

func (prepared *coordinatorPreparedBuild) Plan() task.ExecutionPlan {
	return prepared.plan
}

func (*coordinatorPreparedBuild) Boundary() task.ExecutionBoundary {
	return coordinatorBoundary{}
}

func (prepared *coordinatorPreparedBuild) WorkspaceGeneration() string {
	return prepared.generation
}

func (prepared *coordinatorPreparedBuild) Project() workspace.ProjectConfig {
	return prepared.project
}

func (prepared *coordinatorPreparedBuild) Profile() cmake.BuildProfile {
	return prepared.profile
}

func (prepared *coordinatorPreparedBuild) Toolchain() toolchain.Instance {
	return prepared.toolchain
}

func (*coordinatorPreparedBuild) Targets() []cmake.Target { return nil }

func (prepared *coordinatorPreparedBuild) AllowTestExecutable(
	cmake.FingerprintFile,
) error {
	prepared.allowCalls++
	return nil
}

func (prepared *coordinatorPreparedBuild) ReleaseIfUnadopted() {
	prepared.releaseCalls++
}

type coordinatorBoundary struct{}

func (coordinatorBoundary) ValidateExecutable(string) error { return nil }

func (coordinatorBoundary) ValidateWorkingDirectory(string) error {
	return nil
}

type coordinatorCatalogReader struct {
	catalog testdomain.Catalog
}

func (reader *coordinatorCatalogReader) GetCatalog(
	context.Context,
	string,
	string,
) (testdomain.Catalog, error) {
	return reader.catalog.Clone(), nil
}

type coordinatorCatalogRefresher struct {
	result             RefreshedCatalog
	embeddedGeneration string
	calls              int
}

func (refresher *coordinatorCatalogRefresher) PrepareAfterBuild(
	context.Context,
	RefreshRequest,
) (CatalogRefresh, RefreshProgress, error) {
	refresher.calls++
	descriptor := refresher.result.Bindings[0].Descriptor
	step := task.ExecutionStep{
		ID:   "framework-discovery-000001",
		Kind: task.StepTestDiscovery,
		Process: task.ProcessSpec{
			Executable: descriptor.Executable.Path,
			Dir:        descriptor.WorkingDirectory,
		},
		Public: task.CommandSummary{
			Executable: "framework-tests",
			Args: []string{
				"<service-owned-discovery-invocation>",
			},
		},
	}
	session := &coordinatorCatalogRefresh{
		stepID: step.ID,
		result: refresher.result.Clone(),
	}
	return session, RefreshProgress{
		Steps: []task.ExecutionStep{step},
		Pins:  []cmake.FingerprintFile{descriptor.Executable},
	}, nil
}

type coordinatorCatalogRefresh struct {
	stepID   string
	result   RefreshedCatalog
	finished bool
}

func (*coordinatorCatalogRefresh) ObserveOutput(
	context.Context,
	task.ExecutionStep,
	task.ProcessOutput,
) error {
	return nil
}

func (refresh *coordinatorCatalogRefresh) Interpret(
	_ context.Context,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if step.ID != refresh.stepID || refresh.finished {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	if result.Err != nil || result.ExitCode != 0 {
		return task.StepVerdictDefault, nil
	}
	refresh.finished = true
	return task.StepVerdictSucceeded, nil
}

func (refresh *coordinatorCatalogRefresh) AfterStep(
	_ context.Context,
	step task.ExecutionStep,
) (RefreshProgress, error) {
	if step.ID != refresh.stepID || !refresh.finished {
		return RefreshProgress{}, task.ErrInvalidArgument
	}
	result := refresh.result.Clone()
	return RefreshProgress{Snapshot: &result}, nil
}

type coordinatorTaskStarter struct {
	taskID        string
	now           time.Time
	runs          *coordinatorRunStore
	request       task.StartRequest
	resumeRequest task.ResumeRequest
	calls         int
	resumeCalls   int
}

func (starter *coordinatorTaskStarter) Start(
	_ context.Context,
	request task.StartRequest,
) (task.Task, error) {
	starter.calls++
	starter.request = request
	if request.TestRun != nil {
		run := request.TestRun.Clone()
		run.TaskID = starter.taskID
		run.CreatedAt = starter.now
		if err := starter.runs.create(run); err != nil {
			return task.Task{}, err
		}
	}
	return task.Task{
		ID: starter.taskID, Kind: request.Kind,
		Status: task.StatusQueued, CreatedAt: starter.now,
	}, nil
}

func (starter *coordinatorTaskStarter) ResumeQueued(
	_ context.Context,
	request task.ResumeRequest,
) (task.Task, error) {
	starter.resumeCalls++
	starter.resumeRequest = request
	resumed := request.Task
	resumed.Status = task.StatusRunning
	return resumed, nil
}

type coordinatorRunStore struct {
	run         testdomain.TestRun
	appendCalls int
}

func (store *coordinatorRunStore) create(
	value testdomain.TestRun,
) error {
	validated, err := testdomain.NewTestRun(value)
	if err != nil {
		return err
	}
	store.run = validated
	return nil
}

func (*coordinatorRunStore) CreateRun(
	context.Context,
	testdomain.TestRun,
) error {
	panic("Task starter owns atomic creation")
}

func (store *coordinatorRunStore) StartRun(
	_ context.Context,
	runID string,
	startedAt time.Time,
) error {
	if store.run.RunID != runID {
		return task.ErrNotFound
	}
	if store.run.Status == testdomain.RunRunning &&
		store.run.StartedAt != nil &&
		store.run.StartedAt.Equal(startedAt) {
		return nil
	}
	if store.run.Status != testdomain.RunQueued ||
		startedAt.Before(store.run.CreatedAt) {
		return task.ErrConflict
	}
	store.run.Status = testdomain.RunRunning
	store.run.StartedAt = &startedAt
	return nil
}

func (store *coordinatorRunStore) AppendResult(
	_ context.Context,
	runID string,
	result testdomain.TestItemResult,
) error {
	if store.run.RunID != runID {
		return task.ErrNotFound
	}
	validated, err := testdomain.NewTestItemResult(result)
	if err != nil {
		return err
	}
	store.appendCalls++
	replaced := false
	for index := range store.run.Results {
		if store.run.Results[index].ItemID == validated.ItemID &&
			store.run.Results[index].Iteration == validated.Iteration {
			store.run.Results[index] = validated
			replaced = true
			break
		}
	}
	if !replaced {
		store.run.Results = append(store.run.Results, validated)
	}
	store.run.ResultRevision, err = testdomain.ResultRevision(store.run.Results)
	if err != nil {
		return err
	}
	return nil
}

func (*coordinatorRunStore) FinishRun(
	context.Context,
	testdomain.TestRun,
	[]task.Artifact,
) error {
	return nil
}

func (store *coordinatorRunStore) GetRun(
	context.Context,
	string,
) (testdomain.TestRun, error) {
	if store.run.RunID == "" {
		return testdomain.TestRun{}, task.ErrNotFound
	}
	return store.run.Clone(), nil
}

func (*coordinatorRunStore) ListRuns(
	context.Context,
	testdomain.RunPageRequest,
) (testdomain.RunPage, error) {
	return testdomain.RunPage{}, nil
}

func (store *coordinatorRunStore) RebindQueuedRun(
	_ context.Context,
	runID string,
	_ string,
	catalog testdomain.Catalog,
	selection testdomain.SelectionSnapshot,
) error {
	if store.run.RunID != runID {
		return task.ErrNotFound
	}
	store.run.CatalogRevision = catalog.Revision
	store.run.SelectionSnapshot = selection.Clone()
	return nil
}

type coordinatorAdapter struct{}

func (*coordinatorAdapter) Framework() testdomain.Framework {
	return testdomain.FrameworkCppUTest
}

func (*coordinatorAdapter) ContractVersion() string {
	return "cpputest.v1"
}

func (*coordinatorAdapter) Verify(
	context.Context,
	cmakeDescriptor,
) (testframework.Capabilities, error) {
	panic("not used")
}

type cmakeDescriptor = ctest.ExecutionDescriptor

func (*coordinatorAdapter) Discover(
	context.Context,
	cmakeDescriptor,
) (testframework.DiscoveryResult, error) {
	panic("not used")
}

func (*coordinatorAdapter) PlanRun(
	_ context.Context,
	input testframework.RunInput,
) (testframework.RunPlan, error) {
	invocations := make(
		[]testframework.RunInvocation,
		len(input.Items),
	)
	for index, item := range input.Items {
		invocations[index] = testframework.RunInvocation{
			Arguments: []string{"--case", item.ItemID.String()},
			ExpectedCases: []testframework.ExpectedCase{{
				ItemID:            item.ItemID,
				ParentLogicalName: item.ParentLogicalName,
				LogicalName:       item.LogicalName,
			}},
		}
	}
	return testframework.RunPlan{
		Invocations:      invocations,
		Environment:      []ctest.EnvironmentEntry{},
		WorkingDirectory: input.Descriptor.WorkingDirectory,
	}, nil
}

func (*coordinatorAdapter) NewParser(
	testframework.ParseInput,
) (testframework.ResultParser, error) {
	return &coordinatorParser{}, nil
}

type coordinatorParser struct{}

func (*coordinatorParser) Feed(
	testframework.Stream,
	[]byte,
) ([]testframework.ResultEvent, error) {
	return nil, nil
}

func (*coordinatorParser) Finish(
	testframework.ProcessResult,
) (testframework.ParseResult, error) {
	return testframework.ParseResult{Complete: true}, nil
}
