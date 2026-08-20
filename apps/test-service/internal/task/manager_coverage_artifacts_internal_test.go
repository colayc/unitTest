package task

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
)

func TestCoverageTaskArtifactsAreOwnedOnlyByCompletionPreparer(t *testing.T) {
	sink := &coverageOnlyRecordingSink{}
	manager := &Manager{
		artifacts: coverageOnlyArtifactWriter{sink: sink},
		newID:     func() string { return "11111111111111111111111111111111" },
	}
	owner := &activeTask{
		task: Task{ID: "22222222222222222222222222222222", Kind: KindCoverageRun},
		plan: ExecutionPlan{Version: 1, Steps: []ExecutionStep{{
			ID: "coverage-configure", Kind: StepCoverageConfigure,
		}}},
	}
	if err := manager.createTaskArtifacts(owner); err != nil {
		t.Fatal(err)
	}
	if len(sink.jsonKinds) != 0 {
		t.Fatalf("generic manager committed coverage JSON artifacts: %v", sink.jsonKinds)
	}
	finishedAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	if _, err := manager.finalizeTaskArtifacts(
		context.Background(), owner, owner.task, finishedAt,
		OutcomeInfrastructureFailed, nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(sink.jsonKinds) != 0 || sink.finalizeCalls != 1 {
		t.Fatalf("coverage completion ownership = JSON %v, finalize %d", sink.jsonKinds, sink.finalizeCalls)
	}
}

type coverageOnlyArtifactWriter struct{ sink *coverageOnlyRecordingSink }

func (writer coverageOnlyArtifactWriter) OpenTask(context.Context, string, Kind) (ArtifactSink, error) {
	return writer.sink, nil
}

type coverageOnlyRecordingSink struct {
	jsonKinds     []string
	finalizeCalls int
}

func (*coverageOnlyRecordingSink) AppendOutput(context.Context, string, string, []byte) error {
	return nil
}

func (*coverageOnlyRecordingSink) AppendDiagnostic(context.Context, diagnostic.Diagnostic) error {
	return nil
}

func (sink *coverageOnlyRecordingSink) CommitJSON(_ context.Context, _ string, kind string, _ any) error {
	sink.jsonKinds = append(sink.jsonKinds, kind)
	return nil
}

func (*coverageOnlyRecordingSink) CommitJSONLines(context.Context, string, string, []json.RawMessage) error {
	return nil
}

func (sink *coverageOnlyRecordingSink) Finalize(context.Context, time.Time) ([]Artifact, error) {
	sink.finalizeCalls++
	return nil, nil
}

func (*coverageOnlyRecordingSink) Abort(context.Context) error { return nil }
