package coveragecoord

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestNewQueuedAggregateBuildsClosedCoverageGraph(t *testing.T) {
	now := time.Date(2026, 8, 19, 4, 5, 6, 0, time.UTC)
	aggregate, err := NewQueuedAggregate(QueuedInput{
		Request:         validRequest(),
		Selection:       validSelectionSnapshot(),
		BuildProfileID:  strings.Repeat("b", 64),
		ToolchainID:     "gcc-linux",
		Toolchain:       validToolchain(),
		CreatedAt:       now,
		NewID:           sequentialIDs("1", "2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.Kind != task.KindCoverageRun || aggregate.Task.Status != task.StatusQueued {
		t.Fatalf("task = %#v", aggregate.Task)
	}
	if aggregate.Run.ID == "" || aggregate.Run.TaskID != aggregate.Task.ID || aggregate.Run.TestRunID != aggregate.TestRun.RunID {
		t.Fatalf("coverage graph identities are not aligned: task=%#v run=%#v test=%#v", aggregate.Task, aggregate.Run, aggregate.TestRun)
	}
	if aggregate.Run.Status != coveragedomain.StatusQueued || aggregate.TestRun.Status != testdomain.RunQueued {
		t.Fatalf("queued statuses = run=%q test=%q", aggregate.Run.Status, aggregate.TestRun.Status)
	}
	if len(aggregate.Steps) != 7 || aggregate.Steps[0].Kind != task.StepCoverageConfigure || aggregate.Steps[6].Kind != task.StepCoveragePublish {
		t.Fatalf("steps = %#v", aggregate.Steps)
	}
	if !reflect.DeepEqual(aggregate.Run.SelectionSnapshot, aggregate.TestRun.SelectionSnapshot) {
		t.Fatal("coverage and test selections diverged")
	}
	if !json.Valid(aggregate.Event.Payload) || strings.Contains(string(aggregate.Event.Payload), "executable") {
		t.Fatalf("event payload is not safe: %s", aggregate.Event.Payload)
	}
}

func TestQueuedAggregatePersistDelegatesAtomically(t *testing.T) {
	aggregate, err := NewQueuedAggregate(QueuedInput{
		Request:         validRequest(),
		Selection:       validSelectionSnapshot(),
		BuildProfileID:  strings.Repeat("b", 64),
		ToolchainID:     "gcc-linux",
		Toolchain:       validToolchain(),
		CreatedAt:       time.Date(2026, 8, 19, 4, 5, 6, 0, time.UTC),
		NewID:           sequentialIDs("3", "4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingCoverageStore{}
	got, events, err := aggregate.Persist(context.Background(), store)
	if err != nil || got.ID != aggregate.Task.ID || len(events) != 1 || store.calls != 1 {
		t.Fatalf("Persist() = %#v, %#v, %v; store=%#v", got, events, err, store)
	}
}

func TestNewQueuedAggregateRejectsUnboundOrInvalidIdentity(t *testing.T) {
	input := QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
		CreatedAt:      time.Now().UTC(),
		NewID:          sequentialIDs("5", "6"),
	}
	for name, mutate := range map[string]func(*QueuedInput){
		"missing build profile": func(v *QueuedInput) { v.BuildProfileID = "" },
		"missing toolchain":     func(v *QueuedInput) { v.ToolchainID = "" },
		"missing selection":     func(v *QueuedInput) { v.Selection = testdomain.SelectionSnapshot{} },
		"missing clock":         func(v *QueuedInput) { v.CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := input
			mutate(&candidate)
			if _, err := NewQueuedAggregate(candidate); err == nil {
				t.Fatal("expected invalid queued aggregate input to fail")
			}
		})
	}
}

func validRequest() coveragedomain.Request {
	return coveragedomain.Request{
		IdempotencyKey:      strings.Repeat("a", 32),
		WorkspaceGeneration: strings.Repeat("c", 64),
		ProjectID:           "core",
		CoverageProfileID:   "coverage-debug",
		CatalogRevision:     strings.Repeat("d", 64),
		Selection:           testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{"utid-v1-" + strings.Repeat("e", 64)}},
		RepeatCount:         1,
		Timeout:             time.Minute,
	}
}

func validSelectionSnapshot() testdomain.SelectionSnapshot {
	return testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{"utid-v1-" + strings.Repeat("e", 64)}}
}

func validToolchain() coveragedomain.ToolchainSnapshot {
	return coveragedomain.ToolchainSnapshot{
		Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
		Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
		Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
		Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
		NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("f", 64),
	}
}

func sequentialIDs(values ...string) task.IDGenerator {
	index := 0
	return func() string {
		value := strings.Repeat(values[index], 32)
		index++
		return value
	}
}

type recordingCoverageStore struct {
	calls int
	err   error
}

func (store *recordingCoverageStore) CreateCoverageTask(_ context.Context, input task.Task, steps []task.StepSnapshot, event task.EventDraft, _ coveragedomain.Run, _ testdomain.TestRun) (task.Task, []task.Event, error) {
	store.calls++
	if store.err != nil {
		return task.Task{}, nil, store.err
	}
	return input, []task.Event{{Sequence: 1, EventDraft: event, ID: strings.Repeat("e", 32)}}, nil
}
