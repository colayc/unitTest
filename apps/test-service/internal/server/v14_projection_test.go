package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type projectionCoverageBackend struct{}

func (projectionCoverageBackend) StartCoverageRun(context.Context, session.CoverageRunStart) (task.Task, coveragedomain.Run, testdomain.TestRun, error) {
	return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errors.New("unexpected coverage start")
}

func (projectionCoverageBackend) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return coveragedomain.Run{}, errors.New("unexpected coverage get")
}

func (projectionCoverageBackend) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	return coveragedomain.RunPage{}, errors.New("unexpected coverage list")
}

func (projectionCoverageBackend) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return coveragedomain.Report{}, errors.New("unexpected coverage report get")
}

func TestCoverageEventV14ProjectionAndCompatibility(t *testing.T) {
	taskID := testID('e')
	at := time.Date(2026, 8, 20, 6, 0, 0, 0, time.UTC)
	events := []struct {
		typeValue task.EventType
		payload   string
	}{
		{task.EventCoverageRunStarted, `{"coverageRunId":"11111111111111111111111111111111","testRunId":"22222222222222222222222222222222","catalogRevision":"3333333333333333333333333333333333333333333333333333333333333333","repeatCount":1}`},
		{task.EventCoverageBuildFinished, `{"coverageRunId":"11111111111111111111111111111111"}`},
		{task.EventCoverageCollectionStarted, `{"coverageRunId":"11111111111111111111111111111111","testRunId":"22222222222222222222222222222222"}`},
		{task.EventCoverageReportAvailable, `{"coverageRunId":"11111111111111111111111111111111","reportId":"44444444444444444444444444444444","artifactId":"55555555555555555555555555555555","completeness":{"outcome":"available","reasons":[]},"summary":{"lines":{"covered":1,"total":1},"branches":{"covered":0,"total":0},"functions":{"covered":1,"total":1}}}`},
		{task.EventCoverageRunFinished, `{"coverageRunId":"11111111111111111111111111111111","outcome":"unavailable","reason":"service_restarted"}`},
	}
	for index, tc := range events {
		t.Run(string(tc.typeValue)+" v1.4", func(t *testing.T) {
			persisted := eventForProjection(int64(100+index), taskID, tc.typeValue, at.Add(time.Duration(index)*time.Second), tc.payload)
			projected := subscribeSingleProjectedEvent(t, protocol.Version14, persisted)
			if projected.MessageID != persisted.ID || projected.Sequence != persisted.Sequence ||
				projected.SentAt != persisted.At.Format(time.RFC3339Nano) ||
				projected.Event != string(tc.typeValue) || string(projected.Payload) != tc.payload {
				t.Fatalf("projected event = %#v", projected)
			}
		})
		for _, version := range []string{protocol.Version11, protocol.Version12, protocol.Version13} {
			t.Run(string(tc.typeValue)+" "+version, func(t *testing.T) {
				persisted := eventForProjection(int64(200+index), taskID, tc.typeValue, at.Add(time.Duration(index)*time.Second), tc.payload)
				projected := subscribeSingleProjectedEvent(t, version, persisted)
				wantPayload := `{"stream":"service","text":"","truncated":false}`
				if version == protocol.Version12 {
					wantPayload = `{"stepId":"test-compatibility","stream":"combined","text":"","truncated":false}`
				}
				if projected.MessageID != persisted.ID || projected.Sequence != persisted.Sequence ||
					projected.Event != string(task.EventTaskOutput) || string(projected.Payload) != wantPayload {
					t.Fatalf("compatibility event = %#v", projected)
				}
			})
		}
	}
}
