package protocolmodel

import (
	"testing"

	eventv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/event"
	taskv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/task"
)

func TestGeneratedModelsCompile(t *testing.T) {
	var task taskv12.TaskSnapshotV12 = taskv12.CmakeBuildTaskSnapshotV12{}
	var event eventv12.TaskEventV12 = eventv12.TaskDiagnosticEventV12{}
	if task == nil || event == nil {
		t.Fatal("generated v1.2 branch models must satisfy their union interfaces")
	}
}
