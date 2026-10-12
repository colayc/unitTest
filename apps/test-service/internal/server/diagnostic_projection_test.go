package server

import (
	"encoding/json"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/task"
)

func TestV15AndV16ProjectLegacyDiagnosticCategory(t *testing.T) {
	for _, version := range []string{protocol.Version15, protocol.Version16} {
		t.Run(version, func(t *testing.T) {
			event, err := toProtocolEvent(task.Event{
				Sequence: 1,
				ID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				EventDraft: task.EventDraft{
					TaskID:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					Type:    task.EventTaskDiagnostic,
					At:      time.Unix(123, 0).UTC(),
					Payload: json.RawMessage(`{"diagnostic":{"severity":"warning","code":"C4996","message":"warning"}}`),
				},
			}, version)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Diagnostic struct {
					Category string `json:"category"`
				} `json:"diagnostic"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Diagnostic.Category != "build_error" {
				t.Fatalf("projected diagnostic = %#v", payload)
			}
		})
	}
}

func TestV15AndV16PreserveModernTestEvents(t *testing.T) {
	for _, version := range []string{protocol.Version15, protocol.Version16} {
		t.Run(version, func(t *testing.T) {
			event, err := toProtocolEvent(task.Event{
				Sequence: 2,
				ID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				EventDraft: task.EventDraft{
					TaskID:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					Type:    task.EventTestCatalogPublished,
					At:      time.Unix(123, 0).UTC(),
					Payload: json.RawMessage(`{"projectId":"core","profileId":"cpputest","revision":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","containerCount":1,"itemCount":1}`),
				},
			}, version)
			if err != nil {
				t.Fatal(err)
			}
			if event.Event != string(task.EventTestCatalogPublished) {
				t.Fatalf("event type was projected to %q", event.Event)
			}
			var payload map[string]any
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["projectId"] != "core" || payload["profileId"] != "cpputest" {
				t.Fatalf("modern test event payload was not preserved: %#v", payload)
			}
		})
	}
}

func TestV15AndV16PreserveModernCoverageEvents(t *testing.T) {
	for _, version := range []string{protocol.Version15, protocol.Version16} {
		t.Run(version, func(t *testing.T) {
			event, err := toProtocolEvent(task.Event{
				Sequence: 3,
				ID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				EventDraft: task.EventDraft{
					TaskID:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					Type:    task.EventCoverageRunFinished,
					At:      time.Unix(123, 0).UTC(),
					Payload: json.RawMessage(`{"coverageRunId":"run-1","outcome":"succeeded"}`),
				},
			}, version)
			if err != nil {
				t.Fatal(err)
			}
			if event.Event != string(task.EventCoverageRunFinished) {
				t.Fatalf("event type was projected to %q", event.Event)
			}
			if string(event.Payload) != `{"coverageRunId":"run-1","outcome":"succeeded"}` {
				t.Fatalf("modern coverage event payload was not preserved: %s", event.Payload)
			}
		})
	}
}
