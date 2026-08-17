package testrun

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const (
	maxTestOutputEventBytes = 256 * 1024
	maxTestOutputTotalBytes = 16 * 1024 * 1024
)

func newDomainEvent(
	eventType task.EventType,
	payload any,
) (task.DomainEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return task.DomainEvent{}, err
	}
	return task.DomainEvent{
		Type:    eventType,
		Payload: encoded,
	}, nil
}

func appendDomainEvent(
	destination *[]task.DomainEvent,
	eventType task.EventType,
	payload any,
) error {
	event, err := newDomainEvent(eventType, payload)
	if err != nil {
		return err
	}
	*destination = append(*destination, event)
	return nil
}

func drainDomainEvents(
	source *[]task.DomainEvent,
) []task.DomainEvent {
	result := make([]task.DomainEvent, len(*source))
	for index, event := range *source {
		result[index] = task.DomainEvent{
			Type: event.Type,
			Payload: append(
				json.RawMessage(nil),
				event.Payload...,
			),
		}
	}
	*source = nil
	return result
}

func (interpreter *Interpreter) DrainDomainEvents() []task.DomainEvent {
	if interpreter == nil {
		return nil
	}
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	return drainDomainEvents(&interpreter.events)
}

func (interpreter *Interpreter) ensureInvocationStarted(
	state *invocationInterpreter,
) error {
	if state.started {
		return nil
	}
	if err := appendDomainEvent(
		&interpreter.events,
		task.EventTestContainerStarted,
		map[string]any{
			"runId":       interpreter.runID,
			"containerId": state.invocation.ContainerID,
			"iteration":   state.invocation.Job.Iteration,
		},
	); err != nil {
		return err
	}
	for _, expected := range state.invocation.ExpectedCases {
		if err := appendDomainEvent(
			&interpreter.events,
			task.EventTestItemStarted,
			map[string]any{
				"runId":       interpreter.runID,
				"containerId": state.invocation.ContainerID,
				"itemId":      expected.ItemID,
				"iteration":   state.invocation.Job.Iteration,
			},
		); err != nil {
			return err
		}
	}
	state.started = true
	return nil
}

func (interpreter *Interpreter) recordOutputEvent(
	state *invocationInterpreter,
	stream testframework.Stream,
	data []byte,
) error {
	if interpreter.outputTruncated {
		return nil
	}
	remaining := maxTestOutputTotalBytes -
		interpreter.eventOutputBytes
	accepted := data
	truncated := false
	if len(accepted) > remaining {
		accepted = accepted[:max(remaining, 0)]
		truncated = true
	}
	text := strings.ToValidUTF8(
		string(accepted),
		"\uFFFD",
	)
	blocks := boundedEventText(text)
	if len(blocks) == 0 && truncated {
		blocks = []string{""}
	}
	for index, block := range blocks {
		if err := appendDomainEvent(
			&interpreter.events,
			task.EventTestOutput,
			map[string]any{
				"runId":       interpreter.runID,
				"containerId": state.invocation.ContainerID,
				"iteration":   state.invocation.Job.Iteration,
				"stream":      stream,
				"text":        block,
				"truncated": truncated &&
					index == len(blocks)-1,
			},
		); err != nil {
			return err
		}
	}
	interpreter.eventOutputBytes += len(accepted)
	interpreter.outputTruncated = truncated
	return nil
}

func boundedEventText(value string) []string {
	if value == "" {
		return nil
	}
	result := make([]string, 0, 1)
	for len(value) != 0 {
		end := min(len(value), maxTestOutputEventBytes)
		for end < len(value) && end > 0 &&
			!utf8.RuneStart(value[end]) {
			end--
		}
		if end == 0 {
			end = len(value)
		}
		result = append(result, value[:end])
		value = value[end:]
	}
	return result
}

func (interpreter *Interpreter) recordItemFinished(
	state *invocationInterpreter,
	result testdomain.TestItemResult,
) error {
	if state.finishedItems == nil {
		state.finishedItems = make(map[testdomain.ID]struct{})
	}
	if _, exists := state.finishedItems[result.ItemID]; exists {
		return nil
	}
	if err := appendDomainEvent(
		&interpreter.events,
		task.EventTestItemFinished,
		map[string]any{
			"runId":  interpreter.runID,
			"result": result,
		},
	); err != nil {
		return err
	}
	state.finishedItems[result.ItemID] = struct{}{}
	return nil
}

func (interpreter *Interpreter) recordContainerFinished(
	state *invocationInterpreter,
) error {
	if state.finished {
		return nil
	}
	for _, expected := range state.invocation.ExpectedCases {
		result, exists := state.persisted[expected.ItemID]
		if !exists {
			continue
		}
		if err := interpreter.recordItemFinished(
			state,
			result,
		); err != nil {
			return err
		}
	}
	if err := appendDomainEvent(
		&interpreter.events,
		task.EventTestContainerFinished,
		map[string]any{
			"runId":       interpreter.runID,
			"containerId": state.invocation.ContainerID,
			"iteration":   state.invocation.Job.Iteration,
			"outcome":     containerOutcome(state),
		},
	); err != nil {
		return err
	}
	state.finished = true
	return nil
}

func containerOutcome(
	state *invocationInterpreter,
) testdomain.ItemOutcome {
	outcome := testdomain.ItemPassed
	for _, result := range state.persisted {
		switch result.Outcome {
		case testdomain.ItemTimedOut:
			return testdomain.ItemTimedOut
		case testdomain.ItemCancelled:
			outcome = testdomain.ItemCancelled
		case testdomain.ItemErrored:
			if outcome != testdomain.ItemCancelled {
				outcome = testdomain.ItemErrored
			}
		case testdomain.ItemFailed:
			if outcome != testdomain.ItemCancelled &&
				outcome != testdomain.ItemErrored {
				outcome = testdomain.ItemFailed
			}
		case testdomain.ItemNotRun:
			if outcome == testdomain.ItemPassed {
				outcome = testdomain.ItemNotRun
			}
		}
		if result.Partial &&
			outcome != testdomain.ItemTimedOut &&
			outcome != testdomain.ItemCancelled {
			outcome = testdomain.ItemErrored
		}
	}
	return outcome
}

func (execution *runExecution) DrainDomainEvents() []task.DomainEvent {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	events := drainDomainEvents(&execution.events)
	interpreter := execution.interpreter
	execution.mu.Unlock()
	if interpreter != nil {
		events = append(events, interpreter.DrainDomainEvents()...)
	}
	return events
}

func (execution *runExecution) ensureRunStarted(
	ctx context.Context,
	current task.Task,
) error {
	if execution.runStarted {
		return nil
	}
	if !nilCoordinatorPort(execution.runs) {
		if current.StartedAt == nil {
			return task.ErrInvalidArgument
		}
		if err := execution.runs.StartRun(
			ctx,
			execution.runID,
			*current.StartedAt,
		); err != nil {
			return err
		}
		run, err := execution.runs.GetRun(
			ctx,
			execution.runID,
		)
		if err != nil {
			return err
		}
		if err := appendDomainEvent(
			&execution.events,
			task.EventTestRunStarted,
			map[string]any{
				"runId":           execution.runID,
				"catalogRevision": run.CatalogRevision,
				"total":           execution.expectedResults,
			},
		); err != nil {
			return err
		}
		execution.runStarted = true
		return nil
	}
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestRunStarted,
		map[string]any{
			"runId":           execution.runID,
			"catalogRevision": strings.Repeat("0", 64),
			"total":           execution.expectedResults,
		},
	); err != nil {
		return err
	}
	execution.runStarted = true
	return nil
}

func (execution *runExecution) ensureDiscoveryStarted() error {
	if execution.discoveryStarted {
		return nil
	}
	if nilCoordinatorPort(execution.prepared) {
		return task.ErrInvalidArgument
	}
	project := execution.prepared.Project()
	profile := execution.prepared.Profile()
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestDiscoveryStarted,
		map[string]any{
			"projectId": project.ID,
			"profileId": profile.ID,
		},
	); err != nil {
		return err
	}
	execution.discoveryStarted = true
	return nil
}

func (execution *runExecution) recordCatalogPublished(
	catalog testdomain.Catalog,
) error {
	if execution.catalogPublished {
		return nil
	}
	for _, container := range catalog.Containers {
		if err := appendDomainEvent(
			&execution.events,
			task.EventTestContainerDiscovered,
			map[string]any{
				"containerId": container.ID,
				"framework":   container.Framework,
				"displayName": container.DisplayName,
			},
		); err != nil {
			return err
		}
	}
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestCatalogPublished,
		map[string]any{
			"projectId":      catalog.ProjectID,
			"profileId":      catalog.ProfileID,
			"revision":       catalog.Revision,
			"containerCount": len(catalog.Containers),
			"itemCount":      len(catalog.Items),
		},
	); err != nil {
		return err
	}
	execution.catalogPublished = true
	return nil
}
