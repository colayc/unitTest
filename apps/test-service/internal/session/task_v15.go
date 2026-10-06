package session

import (
	"encoding/json"
	"errors"

	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	taskv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/task"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

// toProtocolTaskV15 keeps the v1.5 wire discriminator and fields intact. The
// v1.3 projection already validates persisted requests and associated runs, so
// this boundary only needs to translate the generated model types.
func toProtocolTaskV15(value task.Task, run *testdomain.TestRun) (taskv15.TaskSnapshotV15, error) {
	if value.Kind == task.KindCoverageRun {
		legacy, err := toProtocolCoverageTaskV14(value, run)
		if err != nil {
			return nil, err
		}
		return decodeTaskV15(legacy)
	}
	legacy, err := toProtocolTaskV13(value, run)
	if err != nil {
		return nil, err
	}
	return decodeTaskV15(legacy)
}

func decodeTaskV15(value any) (taskv15.TaskSnapshotV15, error) {
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
	var result taskv15.TaskSnapshotV15
	switch discriminator.Kind {
	case string(taskv13.CmakeBuild):
		var value taskv15.CmakeBuildTaskSnapshotV15
		result = value
	case string(taskv13.Simulation):
		var value taskv15.SimulationTaskSnapshotV15
		result = value
	case string(taskv13.TestDiscovery):
		var value taskv15.TestDiscoveryTaskSnapshotV15
		result = value
	case string(taskv13.TestRun):
		var value taskv15.TestRunTaskSnapshotV15
		result = value
	case "coverageRun":
		var value taskv15.CoverageRunTaskSnapshotV15
		result = value
	default:
		return nil, errors.New("unsupported v1.5 task kind")
	}
	// Decode into the concrete type selected above; interfaces cannot be
	// unmarshaled directly, and the discriminator is schema-controlled.
	switch result.(type) {
	case taskv15.CmakeBuildTaskSnapshotV15:
		var value taskv15.CmakeBuildTaskSnapshotV15
		err = json.Unmarshal(data, &value)
		result = value
	case taskv15.SimulationTaskSnapshotV15:
		var value taskv15.SimulationTaskSnapshotV15
		err = json.Unmarshal(data, &value)
		result = value
	case taskv15.TestDiscoveryTaskSnapshotV15:
		var value taskv15.TestDiscoveryTaskSnapshotV15
		err = json.Unmarshal(data, &value)
		result = value
	case taskv15.TestRunTaskSnapshotV15:
		var value taskv15.TestRunTaskSnapshotV15
		err = json.Unmarshal(data, &value)
		result = value
	case taskv15.CoverageRunTaskSnapshotV15:
		var value taskv15.CoverageRunTaskSnapshotV15
		err = json.Unmarshal(data, &value)
		result = value
	}
	return result, err
}
