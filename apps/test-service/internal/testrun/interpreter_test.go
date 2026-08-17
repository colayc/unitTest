package testrun

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestInterpreterPersistsValidatedAssertionAndAcceptsNonzeroExit(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	failed := testframework.ParsedCaseResult{
		ItemID: itemID, ParentLogicalName: "Group",
		LogicalName: "Case", Status: testframework.CaseFailed,
		Category: "assertion_failure", Message: "expected 1",
		FailureDetails: []testframework.ParsedFailureDetail{{
			Category: "assertion_failure",
			Message:  "expected 1",
			Expected: "1", Actual: "2",
		}},
	}
	parser := &recordingResultParser{
		feedEvents: map[testframework.Stream][]testframework.ResultEvent{
			testframework.StreamStdout: {{Case: failed}},
		},
		finish: testframework.ParseResult{
			Cases:    []testframework.ParsedCaseResult{failed},
			Complete: true,
		},
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
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
			Data:   []byte("validated assertion\n"),
		},
	); err != nil {
		t.Fatal(err)
	}
	verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{ExitCode: 7},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].Outcome != testdomain.ItemFailed ||
		results[0].Iteration != 1 ||
		len(results[0].FailureDetails) != 1 ||
		results[0].FailureDetails[0].Expected != "1" {
		t.Fatalf("persisted results = %#v", results)
	}
}

func TestInterpreterPreservesGenericAssertionEvidence(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	failed := testframework.ParsedCaseResult{
		ItemID: itemID, ParentLogicalName: "Group",
		LogicalName: "Case", Status: testframework.CaseFailed,
		Category: "assertion_failure", Message: "expected 1 but was 2",
		FailureDetails: []testframework.ParsedFailureDetail{},
	}
	parser := &recordingResultParser{
		finish: testframework.ParseResult{
			Cases:    []testframework.ParsedCaseResult{failed},
			Complete: true,
		},
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
		containerID,
		itemID,
		parser,
		nil,
	)
	current, step := interpreterTaskAndStep(interpreter)

	verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{ExitCode: 1},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].Outcome != testdomain.ItemFailed ||
		len(results[0].FailureDetails) != 1 ||
		results[0].FailureDetails[0].Category !=
			"assertion_failure" ||
		results[0].FailureDetails[0].Message !=
			"expected 1 but was 2" {
		t.Fatalf("generic assertion result = %#v", results)
	}
}

func TestInterpreterTurnsMalformedFrameworkOutputIntoDomainError(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	parser := &recordingResultParser{
		finishErr: errors.New("malformed framework output"),
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
		containerID,
		itemID,
		parser,
		nil,
	)
	current, step := interpreterTaskAndStep(interpreter)

	verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{ExitCode: 1},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].Outcome != testdomain.ItemErrored ||
		!results[0].Partial ||
		len(results[0].FailureDetails) != 1 ||
		results[0].FailureDetails[0].Category !=
			"framework_output_invalid" {
		t.Fatalf("malformed results = %#v", results)
	}
}

func TestInterpreterDoesNotPersistServiceTokenMarkerFromFrameworkOutput(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	parser := &recordingResultParser{
		finish: testframework.ParseResult{
			Cases: []testframework.ParsedCaseResult{{
				ItemID: itemID, ParentLogicalName: "Group",
				LogicalName: "Case",
				Status:      testframework.CaseFailed,
				FailureDetails: []testframework.ParsedFailureDetail{{
					Category: "assertion_failure",
					Message: "UNIT_TEST_SERVICE_TOKEN " +
						"must stay private",
				}},
			}},
			Complete: true,
		},
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
		containerID,
		itemID,
		parser,
		nil,
	)
	current, step := interpreterTaskAndStep(interpreter)
	if verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{ExitCode: 1},
	); err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %q, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].Outcome != testdomain.ItemErrored ||
		strings.Contains(
			results[0].FailureDetails[0].Message,
			"UNIT_TEST_SERVICE_TOKEN",
		) {
		t.Fatalf("sanitized results = %#v", results)
	}
}

func TestInterpreterReadsServiceOwnedControlFileBeforeFinish(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	passed := testframework.ParsedCaseResult{
		ItemID: itemID, ParentLogicalName: "Group",
		LogicalName: "Case", Status: testframework.CasePassed,
	}
	parser := &recordingResultParser{
		feedEvents: map[testframework.Stream][]testframework.ResultEvent{
			testframework.StreamControl: {{Case: passed}},
		},
		finish: testframework.ParseResult{
			Cases:    []testframework.ParsedCaseResult{passed},
			Complete: true,
		},
	}
	control := &memoryControlFile{
		path: "service-owned-control",
		data: []byte(`{"status":"passed"}` + "\n"),
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
		containerID,
		itemID,
		parser,
		control,
	)
	current, step := interpreterTaskAndStep(interpreter)

	verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{ExitCode: 0},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	if control.readCalls != 1 ||
		!reflect.DeepEqual(
			parser.feedData[testframework.StreamControl],
			control.data,
		) {
		t.Fatalf(
			"control reads=%d data=%q",
			control.readCalls,
			parser.feedData[testframework.StreamControl],
		)
	}
}

func TestInterpreterPersistenceFailureRemainsInfrastructureFailure(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	passed := testframework.ParsedCaseResult{
		ItemID: itemID, ParentLogicalName: "Group",
		LogicalName: "Case", Status: testframework.CasePassed,
	}
	parser := &recordingResultParser{
		feedEvents: map[testframework.Stream][]testframework.ResultEvent{
			testframework.StreamStdout: {{Case: passed}},
		},
	}
	store := newResultAppender()
	store.err = task.ErrStorageUnavailable
	interpreter := newTestInterpreter(
		t,
		store,
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
			Stream: "stdout", Data: []byte("pass\n"),
		},
	); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("ObserveOutput() error = %v", err)
	}
}

func TestInterpreterMapsOpaqueCTestExitToDomainResult(t *testing.T) {
	containerID, _ := interpreterIDs(t)
	store := newResultAppender()
	step := task.ExecutionStep{ID: "test-000001", Kind: task.StepTestRun}
	interpreter, err := NewInterpreter(
		strings.Repeat("1", 32),
		store,
		PlannedRun{Invocations: []PlannedInvocation{{
			Job: ScheduledJob{
				ID: "test-000001", ContainerID: containerID,
				Iteration: 1,
			},
			Step:        step,
			ContainerID: containerID,
			Framework:   testdomain.FrameworkOpaqueCTest,
			ExpectedCases: []testframework.ExpectedCase{{
				ItemID:      containerID,
				LogicalName: "opaque",
			}},
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := interpreter.Interpret(
		context.Background(),
		task.Task{ID: strings.Repeat("2", 32)},
		step,
		task.ProcessResult{ExitCode: 8},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].ItemID != containerID ||
		results[0].Outcome != testdomain.ItemFailed {
		t.Fatalf("opaque result = %#v", results)
	}
}

func TestInterpreterMapsFrameworkTimeoutToTimedOutResult(
	t *testing.T,
) {
	containerID, itemID := interpreterIDs(t)
	parser := &recordingResultParser{
		finish: testframework.ParseResult{
			Cases: []testframework.ParsedCaseResult{{
				ItemID:            itemID,
				ParentLogicalName: "Group",
				LogicalName:       "Case",
				Status:            testframework.CaseNotRun,
				Partial:           true,
			}},
			Complete: false,
		},
	}
	store := newResultAppender()
	interpreter := newTestInterpreter(
		t,
		store,
		containerID,
		itemID,
		parser,
		nil,
	)
	current, step := interpreterTaskAndStep(interpreter)

	verdict, err := interpreter.Interpret(
		context.Background(),
		current,
		step,
		task.ProcessResult{TimedOut: true},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %s, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 1 ||
		results[0].Outcome != testdomain.ItemTimedOut ||
		!results[0].Partial ||
		results[0].Reason != "" ||
		len(results[0].FailureDetails) != 1 ||
		results[0].FailureDetails[0].Category != "test_timeout" {
		t.Fatalf("timeout result = %#v", results)
	}
}

func TestInterpreterMarksOnlyFirstMissingCaseTimedOut(
	t *testing.T,
) {
	containerID, firstID := interpreterIDs(t)
	secondID, err := testdomain.CaseID(
		testdomain.CaseIdentity{
			ProjectID: "project", CTestName: "tests",
			Framework: testdomain.FrameworkCppUTest,
			Group:     "Group", Name: "CaseTwo",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parser := &recordingResultParser{
		finish: testframework.ParseResult{
			Cases: []testframework.ParsedCaseResult{
				{
					ItemID:            firstID,
					ParentLogicalName: "Group",
					LogicalName:       "Case",
					Status:            testframework.CaseNotRun,
					Partial:           true,
				},
				{
					ItemID:            secondID,
					ParentLogicalName: "Group",
					LogicalName:       "CaseTwo",
					Status:            testframework.CaseNotRun,
					Partial:           true,
				},
			},
			Complete: false,
		},
	}
	adapter := &interpreterAdapter{parser: parser}
	step := task.ExecutionStep{
		ID: "test-000001", Kind: task.StepTestRun,
	}
	plan := PlannedRun{
		Invocations: []PlannedInvocation{{
			Job: ScheduledJob{
				ID: "test-000001", ContainerID: containerID,
				Iteration: 1,
			},
			Step:        step,
			ContainerID: containerID,
			Framework:   testdomain.FrameworkCppUTest,
			ExpectedCases: []testframework.ExpectedCase{
				{
					ItemID:            firstID,
					ParentLogicalName: "Group",
					LogicalName:       "Case",
				},
				{
					ItemID:            secondID,
					ParentLogicalName: "Group",
					LogicalName:       "CaseTwo",
				},
			},
			Adapter:        adapter,
			AdapterVersion: adapter.ContractVersion(),
			ParseInput: testframework.ParseInput{
				Items: []testframework.RunItem{
					{
						ItemID:            firstID,
						ParentLogicalName: "Group",
						LogicalName:       "Case",
					},
					{
						ItemID:            secondID,
						ParentLogicalName: "Group",
						LogicalName:       "CaseTwo",
					},
				},
			},
		}},
	}
	store := newResultAppender()
	interpreter, err := NewInterpreter(
		strings.Repeat("1", 32),
		store,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := interpreter.Interpret(
		context.Background(),
		task.Task{ID: strings.Repeat("2", 32)},
		step,
		task.ProcessResult{TimedOut: true},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("Interpret() = %q, %v", verdict, err)
	}
	results := store.results()
	if len(results) != 2 ||
		results[0].Outcome != testdomain.ItemTimedOut ||
		results[1].Outcome != testdomain.ItemNotRun ||
		results[1].Reason !=
			testdomain.ReasonContainerTerminated {
		t.Fatalf("timeout results = %#v", results)
	}
}

func newTestInterpreter(
	t *testing.T,
	store *recordingResultAppender,
	containerID testdomain.ID,
	itemID testdomain.ID,
	parser testframework.ResultParser,
	control testframework.ControlFile,
) *Interpreter {
	t.Helper()
	adapter := &interpreterAdapter{parser: parser}
	step := task.ExecutionStep{ID: "test-000001", Kind: task.StepTestRun}
	interpreter, err := NewInterpreter(
		strings.Repeat("1", 32),
		store,
		PlannedRun{Invocations: []PlannedInvocation{{
			Job: ScheduledJob{
				ID: "test-000001", ContainerID: containerID,
				Iteration: 1,
			},
			Step:        step,
			ContainerID: containerID,
			Framework:   testdomain.FrameworkCppUTest,
			ExpectedCases: []testframework.ExpectedCase{{
				ItemID: itemID, ParentLogicalName: "Group",
				LogicalName: "Case",
			}},
			Adapter: adapter, AdapterVersion: adapter.ContractVersion(),
			ParseInput: testframework.ParseInput{
				Items: []testframework.RunItem{{
					ItemID: itemID, ParentLogicalName: "Group",
					LogicalName: "Case",
				}},
			},
			ControlFile: control,
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return interpreter
}

func interpreterTaskAndStep(
	interpreter *Interpreter,
) (task.Task, task.ExecutionStep) {
	return task.Task{ID: strings.Repeat("2", 32)},
		interpreter.invocations["test-000001"].invocation.Step
}

func interpreterIDs(
	t *testing.T,
) (testdomain.ID, testdomain.ID) {
	t.Helper()
	containerID, err := testdomain.ContainerID("project", "tests")
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "project", CTestName: "tests",
		Framework: testdomain.FrameworkCppUTest,
		Group:     "Group", Name: "Case",
	})
	if err != nil {
		t.Fatal(err)
	}
	return containerID, itemID
}

type interpreterAdapter struct {
	parser testframework.ResultParser
}

func (*interpreterAdapter) Framework() testdomain.Framework {
	return testdomain.FrameworkCppUTest
}

func (*interpreterAdapter) ContractVersion() string {
	return "cpputest.v1"
}

func (*interpreterAdapter) Verify(
	context.Context,
	ctest.ExecutionDescriptor,
) (testframework.Capabilities, error) {
	panic("not used")
}

func (*interpreterAdapter) Discover(
	context.Context,
	ctest.ExecutionDescriptor,
) (testframework.DiscoveryResult, error) {
	panic("not used")
}

func (*interpreterAdapter) PlanRun(
	context.Context,
	testframework.RunInput,
) (testframework.RunPlan, error) {
	panic("not used")
}

func (adapter *interpreterAdapter) NewParser(
	testframework.ParseInput,
) (testframework.ResultParser, error) {
	return adapter.parser, nil
}

type recordingResultParser struct {
	mu         sync.Mutex
	feedEvents map[testframework.Stream][]testframework.ResultEvent
	feedData   map[testframework.Stream][]byte
	feedErr    error
	finish     testframework.ParseResult
	finishErr  error
}

func (parser *recordingResultParser) Feed(
	stream testframework.Stream,
	data []byte,
) ([]testframework.ResultEvent, error) {
	parser.mu.Lock()
	defer parser.mu.Unlock()
	if parser.feedData == nil {
		parser.feedData = make(map[testframework.Stream][]byte)
	}
	parser.feedData[stream] = append(
		parser.feedData[stream],
		data...,
	)
	return append(
		[]testframework.ResultEvent(nil),
		parser.feedEvents[stream]...,
	), parser.feedErr
}

func (parser *recordingResultParser) Finish(
	testframework.ProcessResult,
) (testframework.ParseResult, error) {
	parser.mu.Lock()
	defer parser.mu.Unlock()
	return parser.finish, parser.finishErr
}

type recordingResultAppender struct {
	mu      sync.Mutex
	err     error
	byKey   map[string]testdomain.TestItemResult
	ordered []string
}

func newResultAppender() *recordingResultAppender {
	return &recordingResultAppender{
		byKey: make(map[string]testdomain.TestItemResult),
	}
}

func (store *recordingResultAppender) AppendResult(
	_ context.Context,
	_ string,
	result testdomain.TestItemResult,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return store.err
	}
	key := result.ItemID.String() + "/" +
		strconv.FormatInt(result.Iteration, 10)
	if existing, exists := store.byKey[key]; exists {
		if reflect.DeepEqual(existing, result) {
			return nil
		}
		if existing.Partial && !result.Partial {
			store.byKey[key] = result
			return nil
		}
		return task.ErrConflict
	}
	store.byKey[key] = result
	store.ordered = append(store.ordered, key)
	return nil
}

func (store *recordingResultAppender) results() []testdomain.TestItemResult {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]testdomain.TestItemResult, len(store.ordered))
	for index, key := range store.ordered {
		result[index] = store.byKey[key]
	}
	return result
}

type memoryControlFile struct {
	path      string
	data      []byte
	readCalls int
}

func (file *memoryControlFile) Path() string { return file.path }

func (file *memoryControlFile) Read(
	_ context.Context,
	maximum int64,
) ([]byte, error) {
	file.readCalls++
	if int64(len(file.data)) > maximum {
		return nil, errors.New("control data exceeds limit")
	}
	return append([]byte(nil), file.data...), nil
}
