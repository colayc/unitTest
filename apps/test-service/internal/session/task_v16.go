package session

import (
	"encoding/json"
	"errors"

	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	taskv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/task"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func toProtocolTaskV16(value task.Task, run *testdomain.TestRun) (taskv16.TaskSnapshotV16, error) {
	if value.Kind == task.KindCoverageRun {
		legacy, err := toProtocolCoverageTaskV14(value, run)
		if err != nil {
			return nil, err
		}
		return decodeTaskV16(legacy)
	}
	legacy, err := toProtocolTaskV13(value, run)
	if err != nil {
		return nil, err
	}
	return decodeTaskV16(legacy)
}

func decodeTaskV16(value any) (taskv16.TaskSnapshotV16, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, err
	}
	var result taskv16.TaskSnapshotV16
	switch discriminator.Kind {
	case string(taskv13.CmakeBuild):
		var value taskv16.CmakeBuildTaskSnapshotV16
		result = value
	case string(taskv13.Simulation):
		var value taskv16.SimulationTaskSnapshotV16
		result = value
	case string(taskv13.TestDiscovery):
		var value taskv16.TestDiscoveryTaskSnapshotV16
		result = value
	case string(taskv13.TestRun):
		var value taskv16.TestRunTaskSnapshotV16
		result = value
	case "coverageRun":
		var value taskv16.CoverageRunTaskSnapshotV16
		result = value
	default:
		return nil, errors.New("unsupported v1.6 task kind")
	}
	switch result.(type) {
	case taskv16.CmakeBuildTaskSnapshotV16:
		var value taskv16.CmakeBuildTaskSnapshotV16
		err = json.Unmarshal(data, &value)
		result = value
	case taskv16.SimulationTaskSnapshotV16:
		var value taskv16.SimulationTaskSnapshotV16
		err = json.Unmarshal(data, &value)
		result = value
	case taskv16.TestDiscoveryTaskSnapshotV16:
		var value taskv16.TestDiscoveryTaskSnapshotV16
		err = json.Unmarshal(data, &value)
		result = value
	case taskv16.TestRunTaskSnapshotV16:
		var value taskv16.TestRunTaskSnapshotV16
		err = json.Unmarshal(data, &value)
		result = value
	case taskv16.CoverageRunTaskSnapshotV16:
		var value taskv16.CoverageRunTaskSnapshotV16
		err = json.Unmarshal(data, &value)
		result = value
	}
	return result, err
}
