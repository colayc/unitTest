package task_test

import (
	"context"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestManagerCreatesAssociatedTestRunBeforeStartingProcess(
	t *testing.T,
) {
	fixture := newManagerFixture(t)
	request := oneStepBuildRequest(testID(181))
	request.Kind = task.KindTestRun
	runID := testID(182)
	request.TestRun = &testdomain.TestRun{
		RunID:           runID,
		IdempotencyKey:  testID(183),
		ProjectID:       "core",
		ProfileID:       strings.Repeat("b", 64),
		ToolchainID:     "msvc",
		CatalogRevision: strings.Repeat("c", 64),
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode: testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{
				testdomain.ID(
					"utid-v1-" + strings.Repeat("1", 64),
				),
			},
		},
		Status: testdomain.RunQueued,
		Summary: testdomain.RunSummary{
			Iterations: 1,
		},
		ResultRevision: testdomain.EmptyResultRevision(),
		Incomplete:     true,
	}

	started, err := fixture.manager.Start(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	persisted := fixture.store.testRun(runID)
	if persisted.TaskID != started.ID ||
		persisted.CreatedAt.IsZero() ||
		persisted.Status != testdomain.RunQueued ||
		fixture.process.startCalls() != 1 {
		t.Fatalf(
			"associated TestRun/process = %#v / %d",
			persisted,
			fixture.process.startCalls(),
		)
	}
}
