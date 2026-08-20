package testrun

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestPrepareEmbeddedUsesPersistedRunWithoutNestedTask(t *testing.T) {
	fixture, request, allocator := newEmbeddedFixture(t, 2)
	embedded, err := fixture.coordinator.PrepareEmbedded(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.tasks.calls != 0 || fixture.prepareCalls != 0 {
		t.Fatalf("PrepareEmbedded created a nested task/build: tasks=%d builds=%d", fixture.tasks.calls, fixture.prepareCalls)
	}
	steps := embedded.Steps()
	expectations := embedded.Expectations()
	if len(steps) != 4 || len(expectations) != 4 || len(allocator.values) != 4 {
		t.Fatalf("embedded plan = steps=%#v expectations=%#v allocations=%#v", steps, expectations, allocator.values)
	}
	seenNames := make(map[string]struct{}, len(expectations))
	for index, expectation := range expectations {
		if expectation.InvocationID == "" || expectation.Iteration < 1 ||
			!strings.HasPrefix(expectation.FileName, "p-") ||
			!strings.Contains(expectation.FileName, "-i-") ||
			!strings.HasSuffix(expectation.FileName, "-%p-%m.profraw") {
			t.Fatalf("expectation[%d] = %#v", index, expectation)
		}
		if _, duplicate := seenNames[expectation.FileName]; duplicate {
			t.Fatalf("profile collision: %q", expectation.FileName)
		}
		seenNames[expectation.FileName] = struct{}{}
	}
	want := []string{
		"p-000001-i-000001-%p-%m.profraw",
		"p-000002-i-000001-%p-%m.profraw",
		"p-000003-i-000002-%p-%m.profraw",
		"p-000004-i-000002-%p-%m.profraw",
	}
	got := make([]string, len(expectations))
	for index := range expectations {
		got[index] = expectations[index].FileName
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile names = %#v, want %#v", got, want)
	}
	steps[0].Process.Batch[0].Env[0] = "mutated"
	if reflect.DeepEqual(steps, embedded.Steps()) {
		t.Fatal("Steps returned aliased process specs")
	}
}

func TestPrepareEmbeddedRevalidatesRunCatalogProfileAndGeneration(t *testing.T) {
	tests := []struct {
		name string
		edit func(*coordinatorRunFixture, *EmbeddedRequest)
	}{
		{"task", func(_ *coordinatorRunFixture, request *EmbeddedRequest) { request.TaskID = strings.Repeat("9", 32) }},
		{"selection", func(_ *coordinatorRunFixture, request *EmbeddedRequest) {
			request.Run.SelectionSnapshot.ItemIDs[0] = stableTestID("f")
		}},
		{"catalog", func(_ *coordinatorRunFixture, request *EmbeddedRequest) {
			request.Catalog.Revision = strings.Repeat("9", 64)
		}},
		{"profile", func(_ *coordinatorRunFixture, request *EmbeddedRequest) {
			request.PreparedBuild.(*coordinatorPreparedBuild).profile.ID = strings.Repeat("9", 64)
		}},
		{"workspace generation", func(_ *coordinatorRunFixture, request *EmbeddedRequest) {
			request.PreparedBuild.(*coordinatorPreparedBuild).generation = strings.Repeat("9", 64)
		}},
		{"toolchain", func(_ *coordinatorRunFixture, request *EmbeddedRequest) {
			request.PreparedBuild.(*coordinatorPreparedBuild).toolchain.ID = "other"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, request, _ := newEmbeddedFixture(t, 1)
			test.edit(fixture, &request)
			if _, err := fixture.coordinator.PrepareEmbedded(context.Background(), request); err == nil {
				t.Fatal("PrepareEmbedded accepted stale planning input")
			}
			if fixture.tasks.calls != 0 {
				t.Fatalf("revalidation failure started %d nested tasks", fixture.tasks.calls)
			}
		})
	}
}

func TestEmbeddedRunPersistsFrameworkResultsEventsAndFinishesRun(t *testing.T) {
	fixture, request, _ := newEmbeddedFixture(t, 1)
	embedded, err := fixture.coordinator.PrepareEmbedded(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := request.Run.CreatedAt.Add(time.Second)
	current := task.Task{
		ID: request.TaskID, Kind: task.KindCoverageRun,
		StartedAt: &startedAt,
	}
	for _, step := range embedded.Steps() {
		children := make([]task.ProcessChildResult, len(step.Process.Batch))
		for index, item := range step.Process.Batch {
			children[index] = task.ProcessChildResult{ID: item.ID, ExitCode: 1}
		}
		verdict, err := embedded.Interpret(
			context.Background(), current, step,
			task.ProcessResult{Children: children},
		)
		if err != nil || verdict != task.StepVerdictSucceeded {
			t.Fatalf("Interpret(%s) = %q, %v", step.ID, verdict, err)
		}
	}
	if len(fixture.runs.run.Results) != 2 {
		t.Fatalf("persisted results = %#v", fixture.runs.run.Results)
	}
	events := embedded.DrainDomainEvents()
	if len(events) < 7 || events[0].Type != task.EventTestRunStarted {
		t.Fatalf("embedded events = %#v", events)
	}
	finishedAt := startedAt.Add(time.Second)
	finished, err := embedded.Finish(
		context.Background(), finishedAt, task.OutcomeSucceeded,
	)
	if err != nil {
		t.Fatal(err)
	}
	if finished.TaskID != request.TaskID ||
		finished.Status != testdomain.RunCompleted ||
		finished.Outcome != testdomain.RunErrored ||
		finished.Summary.Total != 2 || !finished.Incomplete {
		t.Fatalf("finished embedded TestRun = %#v", finished)
	}
	if fixture.tasks.calls != 0 {
		t.Fatalf("embedded execution started %d nested tasks", fixture.tasks.calls)
	}
}

func TestEmbeddedFinishTerminalizesQueuedRunBeforeAnyCallback(t *testing.T) {
	tests := []struct {
		name    string
		outcome task.Outcome
		want    testdomain.RunOutcome
	}{
		{
			name:    "cancelled",
			outcome: task.OutcomeCancelled,
			want:    testdomain.RunCancelled,
		},
		{
			name:    "infrastructure failure",
			outcome: task.OutcomeInfrastructureFailed,
			want:    testdomain.RunErrored,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, request, _ := newEmbeddedFixture(t, 1)
			embedded, err := fixture.coordinator.PrepareEmbedded(
				context.Background(),
				request,
			)
			if err != nil {
				t.Fatal(err)
			}
			finishedAt := request.Run.CreatedAt.Add(2 * time.Second)
			finished, err := embedded.Finish(
				context.Background(),
				finishedAt,
				test.outcome,
			)
			if err != nil {
				t.Fatal(err)
			}
			validated, err := testdomain.NewTestRun(finished)
			if err != nil {
				t.Fatalf("terminal TestRun cannot enter coverage completion: %v", err)
			}
			if validated.Status != testdomain.RunCompleted ||
				validated.Outcome != test.want ||
				validated.StartedAt != nil ||
				validated.FinishedAt == nil ||
				!validated.FinishedAt.Equal(finishedAt) ||
				validated.Summary != (testdomain.RunSummary{Iterations: 1}) ||
				validated.ResultRevision != testdomain.EmptyResultRevision() ||
				!validated.Incomplete {
				t.Fatalf("zero-callback terminal TestRun = %#v", validated)
			}
			events := embedded.DrainDomainEvents()
			if len(events) != 1 || events[0].Type != task.EventTestRunFinished {
				t.Fatalf("zero-callback events = %#v", events)
			}
		})
	}
}

func TestPrepareEmbeddedEnforcesProfileCapacityBeforeAllocation(t *testing.T) {
	for _, test := range []struct {
		count   int
		wantErr bool
	}{
		{count: 250},
		{count: 251, wantErr: true},
	} {
		t.Run(fmt.Sprintf("%d", test.count), func(t *testing.T) {
			fixture, request, allocator := newEmbeddedFixtureWithItems(
				t,
				test.count,
			)
			embedded, err := fixture.coordinator.PrepareEmbedded(
				context.Background(),
				request,
			)
			if test.wantErr {
				if !errors.Is(err, task.ErrInvalidArgument) {
					t.Fatalf("PrepareEmbedded(%d) error = %v", test.count, err)
				}
				if len(allocator.values) != 0 {
					t.Fatalf("over-capacity plan allocated %d profiles", len(allocator.values))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(embedded.Expectations()) != 250 || len(allocator.values) != 250 {
				t.Fatalf("capacity plan = expectations %d allocations %d", len(embedded.Expectations()), len(allocator.values))
			}
		})
	}
}

type recordingProfileAllocator struct {
	values []ProfileExpectation
}

func (allocator *recordingProfileAllocator) Decorate(
	expectation ProfileExpectation,
	spec task.ProcessSpec,
) (task.ProcessSpec, error) {
	allocator.values = append(allocator.values, expectation)
	result := spec
	result.Env = append(append([]string(nil), spec.Env...), "LLVM_PROFILE_FILE="+expectation.FileName)
	return result, nil
}

func newEmbeddedFixture(
	t *testing.T,
	repeat int64,
) (*coordinatorRunFixture, EmbeddedRequest, *recordingProfileAllocator) {
	t.Helper()
	fixture := newCoordinatorRunFixture(t)
	return newEmbeddedFixtureForCoordinator(t, fixture, repeat)
}

func newEmbeddedFixtureWithItems(
	t *testing.T,
	count int,
) (*coordinatorRunFixture, EmbeddedRequest, *recordingProfileAllocator) {
	t.Helper()
	names := make([]string, count)
	for index := range names {
		names[index] = fmt.Sprintf("Case%06d", index+1)
	}
	fixture := newCoordinatorRunFixtureWithCases(t, names...)
	return newEmbeddedFixtureForCoordinator(t, fixture, 1)
}

func newEmbeddedFixtureForCoordinator(
	t *testing.T,
	fixture *coordinatorRunFixture,
	repeat int64,
) (*coordinatorRunFixture, EmbeddedRequest, *recordingProfileAllocator) {
	t.Helper()
	selection, err := Resolve(
		context.Background(), fixture.catalog, fixture.request.Selection,
		fixture.runs, testdomain.Limits{MaxSelectionSize: 100_000},
	)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	ownerTaskID := strings.Repeat("8", 32)
	run := testdomain.TestRun{
		RunID:             strings.Repeat("7", 32),
		TaskID:            ownerTaskID,
		IdempotencyKey:    strings.Repeat("6", 32),
		ProjectID:         fixture.catalog.ProjectID,
		ProfileID:         fixture.catalog.ProfileID,
		ToolchainID:       fixture.prepared.toolchain.ID,
		CatalogRevision:   fixture.catalog.Revision,
		SelectionSnapshot: selection,
		Status:            testdomain.RunQueued,
		Summary:           testdomain.RunSummary{Iterations: repeat},
		ResultRevision:    testdomain.EmptyResultRevision(),
		Incomplete:        true,
		CreatedAt:         createdAt,
	}
	if err := fixture.runs.create(run); err != nil {
		t.Fatal(err)
	}
	fixture.tasks.calls = 0
	fixture.prepareCalls = 0
	allocator := &recordingProfileAllocator{}
	return fixture, EmbeddedRequest{
		TaskID:         ownerTaskID,
		Run:            run,
		PreparedBuild:  fixture.prepared,
		Catalog:        fixture.catalog,
		Allocator:      allocator,
		MaxConcurrency: 2,
	}, allocator
}

func (refresher *coordinatorCatalogRefresher) PrepareEmbedded(
	_ context.Context,
	request RefreshRequest,
) (RefreshedCatalog, error) {
	if request.WorkspaceGeneration != refresher.embeddedGeneration {
		return RefreshedCatalog{}, task.ErrConflict
	}
	return refresher.result.Clone(), nil
}
