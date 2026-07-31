package task

import (
	"context"
	"time"
)

type taskSummaryProjector func(Task, TaskSummary) (any, error)

var taskSummaryProjectors = map[Kind]taskSummaryProjector{
	KindSimulation:    projectSimulationSummary,
	KindCMakeBuild:    projectCMakeSummary,
	KindTestDiscovery: projectTestTaskSummary,
	KindTestRun:       projectTestTaskSummary,
}

type executionPlanArtifact struct {
	Version int                 `json:"version"`
	Steps   []executionPlanStep `json:"steps"`
}

type executionPlanStep struct {
	ID      string         `json:"id"`
	Kind    StepKind       `json:"kind"`
	Command CommandSummary `json:"command"`
}

type simulationSummary struct {
	TaskID     string   `json:"taskId"`
	Scenario   Scenario `json:"scenario"`
	Outcome    Outcome  `json:"outcome"`
	FinishedAt string   `json:"finishedAt"`
}

func projectSimulationSummary(current Task, summary TaskSummary) (any, error) {
	if current.Kind != KindSimulation || !ValidScenario(current.Scenario) {
		return nil, ErrInvalidArgument
	}
	return simulationSummary{
		TaskID:     summary.TaskID,
		Scenario:   current.Scenario,
		Outcome:    summary.Outcome,
		FinishedAt: summary.FinishedAt.Format(time.RFC3339Nano),
	}, nil
}

func projectCMakeSummary(current Task, summary TaskSummary) (any, error) {
	if current.Kind != KindCMakeBuild {
		return nil, ErrInvalidArgument
	}
	return summary, nil
}

func projectTestTaskSummary(
	current Task,
	summary TaskSummary,
) (any, error) {
	if current.Kind != KindTestDiscovery &&
		current.Kind != KindTestRun {
		return nil, ErrInvalidArgument
	}
	return summary, nil
}

func (m *Manager) openTaskArtifacts(
	current *activeTask,
	active map[string]*activeTask,
) error {
	if err := m.createTaskArtifacts(current); err != nil {
		m.tripStorage(active)
		return ErrStorageUnavailable
	}
	return nil
}

func (m *Manager) createTaskArtifacts(current *activeTask) error {
	if current == nil {
		return ErrInvalidArgument
	}
	if current.artifactSink != nil {
		return nil
	}
	sink, err := m.artifacts.OpenTask(
		context.Background(), current.task.ID, current.task.Kind,
	)
	if err != nil || sink == nil {
		return ErrStorageUnavailable
	}
	current.artifactSink = sink
	if current.task.Kind != KindCMakeBuild &&
		current.task.Kind != KindTestDiscovery &&
		current.task.Kind != KindTestRun {
		return nil
	}
	plan := executionPlanArtifact{
		Version: current.plan.Version,
		Steps:   make([]executionPlanStep, len(current.plan.Steps)),
	}
	for index, step := range current.plan.Steps {
		plan.Steps[index] = executionPlanStep{
			ID: step.ID, Kind: step.Kind,
			Command: CommandSummary{
				Executable: step.Public.Executable,
				Args:       append([]string(nil), step.Public.Args...),
			},
		}
	}
	if err := sink.CommitJSON(
		context.Background(), m.newID(), "execution-plan", plan,
	); err != nil {
		return ErrStorageUnavailable
	}
	return nil
}

func (m *Manager) finalizeTaskArtifacts(
	ctx context.Context,
	owner *activeTask,
	current Task,
	finishedAt time.Time,
	outcome Outcome,
	mutations []StepMutation,
) ([]Artifact, error) {
	if owner == nil {
		return nil, ErrStorageUnavailable
	}
	if err := m.createTaskArtifacts(owner); err != nil {
		return nil, err
	}
	projector, ok := taskSummaryProjectors[current.Kind]
	if !ok {
		return nil, ErrInvalidArgument
	}
	summary := TaskSummary{
		TaskID:     current.ID,
		Kind:       current.Kind,
		Outcome:    outcome,
		FinishedAt: finishedAt,
		Steps:      summarySteps(current.Steps, mutations),
	}
	value, err := projector(current, summary)
	if err != nil {
		return nil, err
	}
	kind := "task-summary"
	if current.Kind == KindCMakeBuild {
		kind = "build-summary"
	}
	if err := owner.artifactSink.CommitJSON(
		ctx, m.newID(), kind, value,
	); err != nil {
		return nil, err
	}
	artifacts, err := owner.artifactSink.Finalize(ctx, finishedAt)
	if err != nil {
		return nil, err
	}
	owner.artifactSink = nil
	return artifacts, nil
}

func summarySteps(current []StepSnapshot, mutations []StepMutation) []StepSnapshot {
	result := cloneStepSnapshots(current)
	for _, mutation := range mutations {
		for index := range result {
			if result[index].ID == mutation.Step.ID {
				result[index] = cloneStepSnapshot(mutation.Step)
				break
			}
		}
	}
	return result
}

func cloneStepSnapshots(steps []StepSnapshot) []StepSnapshot {
	result := make([]StepSnapshot, len(steps))
	for index := range steps {
		result[index] = cloneStepSnapshot(steps[index])
	}
	return result
}

func cloneStepSnapshot(step StepSnapshot) StepSnapshot {
	result := step
	if step.StartedAt != nil {
		value := *step.StartedAt
		result.StartedAt = &value
	}
	if step.FinishedAt != nil {
		value := *step.FinishedAt
		result.FinishedAt = &value
	}
	if step.ExitCode != nil {
		value := *step.ExitCode
		result.ExitCode = &value
	}
	return result
}

func stepEventPayload(step StepSnapshot) map[string]any {
	payload := map[string]any{
		"stepId": step.ID,
		"kind":   step.Kind,
		"status": step.Status,
	}
	if step.ExitCode != nil {
		payload["exitCode"] = *step.ExitCode
	}
	if step.ErrorCode != "" {
		payload["errorCode"] = step.ErrorCode
	}
	return payload
}
