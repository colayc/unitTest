package testrun

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxControlResultBytes int64 = 128 * 1024 * 1024

type ResultAppender interface {
	AppendResult(
		context.Context,
		string,
		testdomain.TestItemResult,
	) error
}

type invocationInterpreter struct {
	invocation PlannedInvocation
	parser     testframework.ResultParser
	persisted  map[testdomain.ID]testdomain.TestItemResult
	parseErr   error
	completed  bool
	verdict    task.StepVerdict
}

type Interpreter struct {
	mu          sync.Mutex
	runID       string
	results     ResultAppender
	invocations map[string]*invocationInterpreter
}

func NewInterpreter(
	runID string,
	results ResultAppender,
	plan PlannedRun,
) (*Interpreter, error) {
	if !lowerHexID(runID, 32) ||
		nilResultAppender(results) ||
		len(plan.Invocations) == 0 ||
		len(plan.Invocations) > maxPlannedInvocations {
		return nil, task.ErrInvalidArgument
	}
	interpreter := &Interpreter{
		runID:   runID,
		results: results,
		invocations: make(
			map[string]*invocationInterpreter,
			len(plan.Invocations),
		),
	}
	for _, planned := range plan.Invocations {
		if planned.Step.ID == "" ||
			planned.Job.ID != planned.Step.ID ||
			!testdomain.ValidID(planned.ContainerID) ||
			planned.Job.ContainerID != planned.ContainerID ||
			planned.Job.Iteration < 1 ||
			planned.Job.Iteration > MaxRepeatCount ||
			planned.Step.Kind != task.StepTestRun ||
			len(planned.ExpectedCases) == 0 {
			return nil, task.ErrInvalidArgument
		}
		if _, duplicate := interpreter.invocations[planned.Step.ID]; duplicate {
			return nil, task.ErrInvalidArgument
		}
		expected := make(
			map[testdomain.ID]struct{},
			len(planned.ExpectedCases),
		)
		for _, item := range planned.ExpectedCases {
			if !testdomain.ValidID(item.ItemID) {
				return nil, task.ErrInvalidArgument
			}
			if _, duplicate := expected[item.ItemID]; duplicate {
				return nil, task.ErrInvalidArgument
			}
			expected[item.ItemID] = struct{}{}
		}
		var parser testframework.ResultParser
		if planned.Adapter == nil {
			if planned.Framework != testdomain.FrameworkOpaqueCTest ||
				len(planned.ExpectedCases) != 1 ||
				planned.ExpectedCases[0].ItemID != planned.ContainerID {
				return nil, task.ErrInvalidArgument
			}
		} else {
			if nilAdapter(planned.Adapter) ||
				planned.Adapter.Framework() != planned.Framework ||
				planned.Adapter.ContractVersion() !=
					planned.AdapterVersion {
				return nil, task.ErrInvalidArgument
			}
			var err error
			parser, err = planned.Adapter.NewParser(planned.ParseInput)
			if err != nil || nilResultParser(parser) {
				return nil, task.ErrInvalidArgument
			}
		}
		copy := clonePlannedInvocation(planned)
		interpreter.invocations[planned.Step.ID] = &invocationInterpreter{
			invocation: copy,
			parser:     parser,
			persisted: make(
				map[testdomain.ID]testdomain.TestItemResult,
				len(planned.ExpectedCases),
			),
		}
	}
	return interpreter, nil
}

func (interpreter *Interpreter) ObserveOutput(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if interpreter == nil || ctx == nil || current.ID == "" {
		return task.ErrInvalidArgument
	}
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	state, err := interpreter.lookup(step)
	if err != nil {
		return err
	}
	if state.completed || state.parser == nil || state.parseErr != nil {
		return nil
	}
	stream, ok := frameworkStream(output.Stream)
	if !ok {
		state.parseErr = errors.New("unsupported framework output stream")
		return nil
	}
	events, err := state.parser.Feed(
		stream,
		append([]byte(nil), output.Data...),
	)
	if err != nil {
		state.parseErr = err
		return nil
	}
	return interpreter.persistEvents(
		ctx,
		state,
		events,
		true,
	)
}

func (interpreter *Interpreter) Interpret(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if interpreter == nil || ctx == nil || current.ID == "" ||
		result.Err != nil {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	state, err := interpreter.lookup(step)
	if err != nil {
		return task.StepVerdictDefault, err
	}
	if state.completed {
		return state.verdict, nil
	}
	if state.parser == nil {
		if err := interpreter.persistOpaque(
			ctx,
			state,
			result,
		); err != nil {
			return task.StepVerdictDefault, err
		}
		state.completed = true
		state.verdict = task.StepVerdictSucceeded
		return state.verdict, nil
	}

	if state.parseErr == nil && state.invocation.ControlFile != nil {
		encoded, readErr := state.invocation.ControlFile.Read(
			ctx,
			maxControlResultBytes,
		)
		if readErr != nil {
			return task.StepVerdictDefault, readErr
		}
		events, feedErr := state.parser.Feed(
			testframework.StreamControl,
			encoded,
		)
		if feedErr != nil {
			state.parseErr = feedErr
		} else if err := interpreter.persistEvents(
			ctx,
			state,
			events,
			true,
		); err != nil {
			return task.StepVerdictDefault, err
		}
	}
	if state.parseErr != nil {
		if err := interpreter.persistMalformed(ctx, state); err != nil {
			return task.StepVerdictDefault, err
		}
		state.completed = true
		state.verdict = task.StepVerdictSucceeded
		return state.verdict, nil
	}

	parsed, finishErr := state.parser.Finish(
		testframework.ProcessResult{
			ExitCode:    result.ExitCode,
			Termination: testframework.ProcessExited,
		},
	)
	if finishErr != nil {
		state.parseErr = finishErr
		if err := interpreter.persistMalformed(ctx, state); err != nil {
			return task.StepVerdictDefault, err
		}
		state.completed = true
		state.verdict = task.StepVerdictSucceeded
		return state.verdict, nil
	}
	for _, candidate := range parsed.Cases {
		domainResult, err := parsedDomainResult(
			state,
			candidate,
			false,
		)
		if err != nil {
			state.parseErr = err
			break
		}
		if err := interpreter.persistResult(
			ctx,
			state,
			domainResult,
		); err != nil {
			return task.StepVerdictDefault, err
		}
	}
	if state.parseErr != nil ||
		!parsed.Complete ||
		len(state.persisted) != len(state.invocation.ExpectedCases) {
		if err := interpreter.persistMalformed(ctx, state); err != nil {
			return task.StepVerdictDefault, err
		}
	}
	state.completed = true
	state.verdict = task.StepVerdictSucceeded
	return state.verdict, nil
}

func (interpreter *Interpreter) lookup(
	step task.ExecutionStep,
) (*invocationInterpreter, error) {
	state := interpreter.invocations[step.ID]
	if state == nil ||
		step.ID != state.invocation.Step.ID ||
		step.Kind != task.StepTestRun {
		return nil, task.ErrInvalidArgument
	}
	return state, nil
}

func (interpreter *Interpreter) persistEvents(
	ctx context.Context,
	state *invocationInterpreter,
	events []testframework.ResultEvent,
	provisional bool,
) error {
	for _, event := range events {
		result, err := parsedDomainResult(
			state,
			event.Case,
			provisional,
		)
		if err != nil {
			state.parseErr = err
			return nil
		}
		if err := interpreter.persistResult(ctx, state, result); err != nil {
			return err
		}
	}
	return nil
}

func (interpreter *Interpreter) persistResult(
	ctx context.Context,
	state *invocationInterpreter,
	result testdomain.TestItemResult,
) error {
	if err := interpreter.results.AppendResult(
		ctx,
		interpreter.runID,
		result,
	); err != nil {
		return err
	}
	state.persisted[result.ItemID] = result
	return nil
}

func (interpreter *Interpreter) persistMalformed(
	ctx context.Context,
	state *invocationInterpreter,
) error {
	for _, expected := range state.invocation.ExpectedCases {
		if _, exists := state.persisted[expected.ItemID]; exists {
			continue
		}
		result := testdomain.TestItemResult{
			ItemID:      expected.ItemID,
			ContainerID: state.invocation.ContainerID,
			Iteration:   state.invocation.Job.Iteration,
			Outcome:     testdomain.ItemErrored,
			FailureDetails: []testdomain.FailureDetail{{
				Category:     "framework_output_invalid",
				Message:      "framework output could not be validated",
				Locations:    []testdomain.SourceLocation{},
				EvidenceRefs: []string{},
			}},
			OutputRefs: []string{},
			Partial:    true,
		}
		if err := interpreter.persistResult(ctx, state, result); err != nil {
			return err
		}
	}
	return nil
}

func (interpreter *Interpreter) persistOpaque(
	ctx context.Context,
	state *invocationInterpreter,
	process task.ProcessResult,
) error {
	expected := state.invocation.ExpectedCases[0]
	outcome := testdomain.ItemPassed
	if process.ExitCode != 0 {
		outcome = testdomain.ItemFailed
	}
	return interpreter.persistResult(
		ctx,
		state,
		testdomain.TestItemResult{
			ItemID:         expected.ItemID,
			ContainerID:    state.invocation.ContainerID,
			Iteration:      state.invocation.Job.Iteration,
			Outcome:        outcome,
			FailureDetails: []testdomain.FailureDetail{},
			OutputRefs:     []string{},
		},
	)
}

func parsedDomainResult(
	state *invocationInterpreter,
	value testframework.ParsedCaseResult,
	provisional bool,
) (testdomain.TestItemResult, error) {
	expected := expectedCase(
		state.invocation.ExpectedCases,
		value.ItemID,
	)
	if expected == nil ||
		expected.ParentLogicalName != value.ParentLogicalName ||
		expected.LogicalName != value.LogicalName ||
		value.DurationMS < 0 {
		return testdomain.TestItemResult{}, task.ErrInvalidArgument
	}
	outcome := testdomain.ItemOutcome("")
	reason := testdomain.ResultReason("")
	switch value.Status {
	case testframework.CasePassed:
		outcome = testdomain.ItemPassed
	case testframework.CaseFailed:
		outcome = testdomain.ItemFailed
	case testframework.CaseSkipped:
		outcome = testdomain.ItemSkipped
	case testframework.CaseNotRun:
		outcome = testdomain.ItemNotRun
		reason = testdomain.ReasonContainerTerminated
	default:
		return testdomain.TestItemResult{}, task.ErrInvalidArgument
	}
	duration := value.DurationMS
	details := make(
		[]testdomain.FailureDetail,
		len(value.FailureDetails),
	)
	for index, detail := range value.FailureDetails {
		details[index] = testdomain.FailureDetail{
			Category:     detail.Category,
			Subtype:      detail.Subtype,
			Message:      detail.Message,
			Expected:     detail.Expected,
			Actual:       detail.Actual,
			Locations:    []testdomain.SourceLocation{},
			EvidenceRefs: []string{},
		}
	}
	result := testdomain.TestItemResult{
		ItemID:         value.ItemID,
		ContainerID:    state.invocation.ContainerID,
		Iteration:      state.invocation.Job.Iteration,
		Outcome:        outcome,
		DurationMS:     &duration,
		FailureDetails: details,
		OutputRefs:     []string{},
		Partial:        provisional || value.Partial,
		Reason:         reason,
	}
	validated, err := testdomain.NewTestItemResult(result)
	if err != nil {
		return testdomain.TestItemResult{}, task.ErrInvalidArgument
	}
	return validated, nil
}

func expectedCase(
	values []testframework.ExpectedCase,
	itemID testdomain.ID,
) *testframework.ExpectedCase {
	for index := range values {
		if values[index].ItemID == itemID {
			return &values[index]
		}
	}
	return nil
}

func frameworkStream(value string) (testframework.Stream, bool) {
	switch value {
	case "stdout":
		return testframework.StreamStdout, true
	case "stderr":
		return testframework.StreamStderr, true
	default:
		return "", false
	}
}

func nilResultAppender(value ResultAppender) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func nilResultParser(value testframework.ResultParser) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func lowerHexID(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
