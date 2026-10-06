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

func TestV15AndV16CompatibilityOutputUsesModernStream(t *testing.T) {
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
			var payload struct {
				StepID string `json:"stepId"`
				Stream string `json:"stream"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.StepID != "test-compatibility" || payload.Stream != "combined" {
				t.Fatalf("projected compatibility output = %#v", payload)
			}
		})
	}
}
