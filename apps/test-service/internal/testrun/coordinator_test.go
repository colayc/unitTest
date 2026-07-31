package testrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

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
	if len(continued.Steps) != 2 ||
		continued.Steps[0].Kind != task.StepTestRun ||
		continued.Steps[1].Kind != task.StepTestRun ||
		fixture.refresher.calls != 1 ||
		fixture.prepared.allowCalls != 1 {
		t.Fatalf(
			"run continuation = %#v, refresh=%d pins=%d",
			continued,
			fixture.refresher.calls,
			fixture.prepared.allowCalls,
		)
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
	if !errors.Is(err, task.ErrInvalidArgument) ||
		len(continued.Steps) != 0 ||
		fixture.prepared.allowCalls != 0 {
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
	t.Helper()
	catalog, container, cases := plannerCatalog(
		t,
		testdomain.FrameworkCppUTest,
		"framework-tests",
		"Group",
		"CaseA",
		"CaseB",
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
				ItemIDs: []testdomain.ID{cases[0].ID, cases[1].ID},
			},
		},
	}
	coordinator, err := NewCoordinator(CoordinatorConfig{
		PrepareBuild: func(
			context.Context,
			build.StartRequest,
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
	result RefreshedCatalog
	calls  int
}

func (refresher *coordinatorCatalogRefresher) RefreshAfterBuild(
	context.Context,
	RefreshRequest,
) (RefreshedCatalog, error) {
	refresher.calls++
	return refresher.result.Clone(), nil
}

type coordinatorTaskStarter struct {
	taskID  string
	now     time.Time
	runs    *coordinatorRunStore
	request task.StartRequest
	calls   int
}

func (starter *coordinatorTaskStarter) Start(
	_ context.Context,
	request task.StartRequest,
) (task.Task, error) {
	starter.calls++
	starter.request = request
	run := request.TestRun.Clone()
	run.TaskID = starter.taskID
	run.CreatedAt = starter.now
	if err := starter.runs.create(run); err != nil {
		return task.Task{}, err
	}
	return task.Task{
		ID: starter.taskID, Kind: request.Kind,
		Status: task.StatusQueued, CreatedAt: starter.now,
	}, nil
}

type coordinatorRunStore struct {
	run testdomain.TestRun
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

func (*coordinatorRunStore) AppendResult(
	context.Context,
	string,
	testdomain.TestItemResult,
) error {
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
