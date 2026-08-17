package testrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestInterpreterEmitsBoundedOrderedLifecycleEventsAfterResults(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	passed := testframework.ParsedCaseResult{
		ItemID: itemID, ParentLogicalName: "Group",
		LogicalName: "Case", Status: testframework.CasePassed,
	}
	parser := &recordingResultParser{
		finish: testframework.ParseResult{
			Cases:    []testframework.ParsedCaseResult{passed},
			Complete: true,
		},
	}
	results := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		results,
		containerID,
		itemID,
		parser,
		nil,
	)
	current, step := interpreterTaskAndStep(interpreter)
	if err := interpreter.ObserveOutput(
		context.Background(),
		current,
		step,
		task.ProcessOutput{
			Stream: "stdout",
			Data:   []byte("first\n"),
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := interpreter.ObserveOutput(
		context.Background(),
		current,
		step,
		task.ProcessOutput{
			Stream: "stdout",
			Data:   []byte("second\n"),
		},
	); err != nil {
		t.Fatal(err)
	}
	outputEvents := interpreter.DrainDomainEvents()
	first := domainEventTypes(outputEvents)
	if !reflect.DeepEqual(first, []task.EventType{
		task.EventTestContainerStarted,
		task.EventTestItemStarted,
		task.EventTestOutput,
		task.EventTestOutput,
	}) {
		t.Fatalf("output lifecycle events = %#v", first)
	}
	for _, event := range outputEvents[2:] {
		var output struct {
			RunID       string        `json:"runId"`
			ContainerID testdomain.ID `json:"containerId"`
			Iteration   int64         `json:"iteration"`
			Stream      string        `json:"stream"`
			Text        string        `json:"text"`
			Truncated   bool          `json:"truncated"`
		}
		decodeDomainPayload(t, event, &output)
		if output.Text == "" || output.Truncated {
			t.Fatalf("test output payload = %#v", output)
		}
	}
	if verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{},
	); err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %q, %v", verdict, err)
	}
	if len(results.results()) != 1 {
		t.Fatal("item finished event became visible without a durable result")
	}
	terminalEvents := interpreter.DrainDomainEvents()
	second := domainEventTypes(terminalEvents)
	if !reflect.DeepEqual(second, []task.EventType{
		task.EventTestItemFinished,
		task.EventTestContainerFinished,
	}) {
		t.Fatalf("terminal lifecycle events = %#v", second)
	}
	var itemFinished struct {
		RunID  string                    `json:"runId"`
		Result testdomain.TestItemResult `json:"result"`
	}
	decodeDomainPayload(
		t,
		terminalEvents[0],
		&itemFinished,
	)
	if itemFinished.Result.ItemID != itemID {
		t.Fatalf("item finished payload = %#v", itemFinished)
	}
	var containerFinished struct {
		RunID       string                 `json:"runId"`
		ContainerID testdomain.ID          `json:"containerId"`
		Iteration   int64                  `json:"iteration"`
		Outcome     testdomain.ItemOutcome `json:"outcome"`
	}
	decodeDomainPayload(
		t,
		terminalEvents[1],
		&containerFinished,
	)
	if containerFinished.Outcome != testdomain.ItemPassed {
		t.Fatalf(
			"container finished payload = %#v",
			containerFinished,
		)
	}
	if events := interpreter.DrainDomainEvents(); len(events) != 0 {
		t.Fatalf("second drain returned %#v", events)
	}
}

func TestCatalogEventsJournalContainersButNotTenThousandItems(
	t *testing.T,
) {
	const containerCount = 513
	const itemCount = 10_000
	catalog := testdomain.Catalog{
		ProjectID: "core",
		ProfileID: strings.Repeat("a", 64),
		Revision:  strings.Repeat("b", 64),
		Containers: make(
			[]testdomain.Container,
			containerCount,
		),
		Items: make([]testdomain.Item, itemCount),
	}
	for index := range catalog.Containers {
		catalog.Containers[index].ID = testdomain.ID(
			"utid-v1-" + fmt.Sprintf("%064x", index+1),
		)
		catalog.Containers[index].Framework =
			testdomain.FrameworkCppUTest
		catalog.Containers[index].DisplayName =
			fmt.Sprintf("container-%06d", index)
	}
	execution := &runExecution{runID: "run"}
	if err := execution.recordCatalogPublished(catalog); err != nil {
		t.Fatal(err)
	}
	events := execution.DrainDomainEvents()
	if len(events) != containerCount+1 {
		t.Fatalf(
			"catalog event count = %d, want %d",
			len(events),
			containerCount+1,
		)
	}
	types := domainEventTypes(events)
	for index := range containerCount {
		if types[index] != task.EventTestContainerDiscovered {
			t.Fatalf("catalog event %d = %q", index, types[index])
		}
	}
	if types[len(types)-1] != task.EventTestCatalogPublished {
		t.Fatalf("catalog terminal event = %q", types[len(types)-1])
	}
	var discovered struct {
		ContainerID testdomain.ID        `json:"containerId"`
		Framework   testdomain.Framework `json:"framework"`
		DisplayName string               `json:"displayName"`
	}
	decodeDomainPayload(t, events[0], &discovered)
	var published struct {
		ProjectID      string `json:"projectId"`
		ProfileID      string `json:"profileId"`
		Revision       string `json:"revision"`
		ContainerCount int64  `json:"containerCount"`
		ItemCount      int64  `json:"itemCount"`
	}
	decodeDomainPayload(
		t,
		events[len(events)-1],
		&published,
	)
	if published.ItemCount != itemCount ||
		published.ContainerCount != containerCount {
		t.Fatalf("catalog published payload = %#v", published)
	}
}

func TestRunStartedStateIsDurableBeforeEventIsDrained(t *testing.T) {
	startedAt := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store := &eventRunStore{
		coordinatorRunStore: &coordinatorRunStore{},
	}
	execution := &runExecution{
		runID: "run",
		runs:  store,
	}
	if err := execution.ensureRunStarted(
		context.Background(),
		task.Task{
			ID:        "task",
			StartedAt: &startedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	if !store.started || !store.startedAt.Equal(startedAt) {
		t.Fatal("test.run.started was queued before RunRunning persisted")
	}
	events := execution.DrainDomainEvents()
	if len(events) != 1 ||
		events[0].Type != task.EventTestRunStarted {
		t.Fatalf("run started events = %#v", events)
	}
	var payload struct {
		RunID           string `json:"runId"`
		CatalogRevision string `json:"catalogRevision"`
		Total           int64  `json:"total"`
	}
	decodeDomainPayload(t, events[0], &payload)
	if payload.CatalogRevision != strings.Repeat("a", 64) {
		t.Fatalf("run started payload = %#v", payload)
	}
}

type eventRunStore struct {
	*coordinatorRunStore
	started   bool
	startedAt time.Time
}

func (store *eventRunStore) StartRun(
	_ context.Context,
	runID string,
	startedAt time.Time,
) error {
	if runID != "run" || store.started {
		return task.ErrConflict
	}
	store.started = true
	store.startedAt = startedAt
	return nil
}

func (store *eventRunStore) GetRun(
	context.Context,
	string,
) (testdomain.TestRun, error) {
	return testdomain.TestRun{
		CatalogRevision: strings.Repeat("a", 64),
	}, nil
}

func domainEventTypes(events []task.DomainEvent) []task.EventType {
	result := make([]task.EventType, len(events))
	for index, event := range events {
		result[index] = event.Type
	}
	return result
}

func decodeDomainPayload(
	t *testing.T,
	event task.DomainEvent,
	destination any,
) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("%s payload error = %v", event.Type, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf(
			"%s payload trailing JSON error = %v",
			event.Type,
			err,
		)
	}
}
