package session

import (
	"errors"
	"time"

	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	taskv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/task"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func toProtocolTaskV14(value task.Task, run *testdomain.TestRun) (taskv14.TaskSnapshotV14, error) {
	if value.Kind == task.KindCoverageRun {
		return toProtocolCoverageTaskV14(value, run)
	}
	legacy, err := toProtocolTaskV13(value, run)
	if err != nil {
		return nil, err
	}
	switch projected := legacy.(type) {
	case taskv13.SimulationTaskSnapshotV13:
		result := taskv14.SimulationTaskSnapshotV14{
			TaskID: projected.TaskID, Kind: taskv14.TaskKindSimulationV14,
			Scenario: taskv14.SimulationScenarioV14(projected.Scenario), TimeoutMS: cloneInt64(projected.TimeoutMS),
			Status: taskv14.TaskStatusV14(projected.Status), CreatedAt: projected.CreatedAt,
			LastSequence: projected.LastSequence,
		}
		projectV14TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	case taskv13.CmakeBuildTaskSnapshotV13:
		result := taskv14.CmakeBuildTaskSnapshotV14{
			TaskID: projected.TaskID, Kind: taskv14.TaskKindCmakeBuildV14,
			WorkspaceGeneration: projected.WorkspaceGeneration, ProjectID: projected.ProjectID,
			BuildProfileID: projected.BuildProfileID, TargetIDs: append([]string{}, projected.TargetIDs...),
			Jobs: projected.Jobs, TimeoutMS: projected.TimeoutMS,
			Status: taskv14.TaskStatusV14(projected.Status), CreatedAt: projected.CreatedAt,
			LastSequence: projected.LastSequence,
		}
		projectV14TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	case taskv13.TestDiscoveryTaskSnapshotV13:
		result := taskv14.TestDiscoveryTaskSnapshotV14{
			TaskID: projected.TaskID, Kind: taskv14.TaskKindTestDiscoveryV14,
			ProjectID: projected.ProjectID, ProfileID: projected.ProfileID,
			CatalogRevision: cloneString(projected.CatalogRevision), Status: taskv14.TaskStatusV14(projected.Status),
			CreatedAt: projected.CreatedAt, LastSequence: projected.LastSequence,
		}
		projectV14TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	case taskv13.TestRunTaskSnapshotV13:
		result := taskv14.TestRunTaskSnapshotV14{
			TaskID: projected.TaskID, Kind: taskv14.TaskKindTestRunV14,
			ProjectID: projected.ProjectID, ProfileID: projected.ProfileID,
			CatalogRevision: projected.CatalogRevision, RunID: projected.RunID,
			RepeatCount: projected.RepeatCount, Status: taskv14.TaskStatusV14(projected.Status),
			CreatedAt: projected.CreatedAt, LastSequence: projected.LastSequence,
		}
		projectV14TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	default:
		return nil, errors.New("unsupported v1.4 task projection")
	}
}

func projectV14TaskCompletion(
	value task.Task,
	outcome **taskv14.TaskOutcomeV14,
	startedAt, finishedAt **time.Time,
	errorCode, errorMessage **string,
) {
	if value.Status == task.StatusFinished {
		projected := taskv14.TaskOutcomeV14(value.Outcome)
		*outcome = &projected
	}
	if value.StartedAt != nil {
		projected := *value.StartedAt
		*startedAt = &projected
	}
	if value.FinishedAt != nil {
		projected := *value.FinishedAt
		*finishedAt = &projected
	}
	if value.ErrorCode != "" {
		projected := value.ErrorCode
		*errorCode = &projected
	}
	if value.ErrorMessage != "" {
		projected := value.ErrorMessage
		*errorMessage = &projected
	}
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
