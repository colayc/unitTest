package task

import (
	"context"
	"time"
)

type taskSummaryProjector func(Task, TaskSummary) (any, error)

var taskSummaryProjectors = map[Kind]taskSummaryProjector{
	KindSimulation: projectSimulationSummary,
	KindCMakeBuild: projectCMakeSummary,
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

func (m *Manager) commitTaskSummary(
	ctx context.Context,
	current Task,
	artifactID string,
	finishedAt time.Time,
	outcome Outcome,
	mutations []StepMutation,
) (Artifact, error) {
	projector, ok := taskSummaryProjectors[current.Kind]
	if !ok {
		return Artifact{}, ErrInvalidArgument
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
		return Artifact{}, err
	}
	return m.artifacts.CommitJSON(ctx, current.ID, artifactID, finishedAt, value)
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
