package protocolmodel

import (
	"testing"

	eventv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/event"
	taskv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/task"
	artifactv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/artifact"
	capabilitiesv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/capabilities"
	diagnosticv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/diagnostic"
	eventv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/event"
	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	testv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/test"
)

func TestGeneratedModelsCompile(t *testing.T) {
	var task taskv12.TaskSnapshotV12 = taskv12.CmakeBuildTaskSnapshotV12{}
	var event eventv12.TaskEventV12 = eventv12.TaskDiagnosticEventV12{}
	var createdEvent eventv12.TaskEventV12 = eventv12.TaskCreatedEventV12{Event: eventv12.TaskCreated}
	var outputEvent eventv12.TaskEventV12 = eventv12.TaskOutputEventV12{Event: eventv12.TaskOutput}
	if task == nil || event == nil || createdEvent == nil || outputEvent == nil {
		t.Fatal("generated v1.2 branch models must satisfy their union interfaces")
	}
}

func TestGeneratedV13ModelsCompile(t *testing.T) {
	var task taskv13.TaskSnapshotV13 = taskv13.TestRunTaskSnapshotV13{}
	var event eventv13.TaskEventV13 = eventv13.TestItemFinishedEventV13{}
	var selection testv13.TestSelection = testv13.ItemsTestSelectionV13{}
	catalog := testv13.TestCatalog{}
	result := testv13.TestItemResult{}
	run := testv13.TestRun{}
	capabilities := capabilitiesv13.CapabilitiesV13{}
	diagnostic := diagnosticv13.DiagnosticV13{}
	artifact := artifactv13.ArtifactMetadataV13{}

	if task == nil || event == nil || selection == nil {
		t.Fatal("generated v1.3 branch models must satisfy their union interfaces")
	}
	_ = []any{catalog, result, run, capabilities, diagnostic, artifact}
}
