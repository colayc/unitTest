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
	artifactv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/artifact"
	capabilitiesv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/capabilities"
	coveragev14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/coverage"
	diagnosticv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/diagnostic"
	eventv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/event"
	taskv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/task"
	testv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/test"
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

func TestGeneratedV14ModelsCompile(t *testing.T) {
	var selection testv14.TestSelectionV14 = testv14.ItemsTestSelectionV14{}
	var task taskv14.TaskSnapshotV14 = taskv14.CoverageRunTaskSnapshotV14{}
	var event eventv14.TaskEventV14 = eventv14.CoverageRunFinishedEventV14{}
	var coverageEvent eventv14.CoverageEventV14 = eventv14.CoverageReportAvailableEventV14{}
	capabilities := capabilitiesv14.CapabilitiesV14{}
	diagnostic := diagnosticv14.DiagnosticV14{}
	request := coveragev14.CoverageRunStartRequest{}
	run := coveragev14.CoverageRun{}
	page := coveragev14.CoverageRunPage{}
	report := coveragev14.CoverageReport{}
	artifact := artifactv14.ArtifactMetadataV14{}

	if selection == nil || task == nil || event == nil || coverageEvent == nil {
		t.Fatal("generated v1.4 branch models must satisfy their union interfaces")
	}
	_ = []any{capabilities, diagnostic, request, run, page, report, artifact}
}
