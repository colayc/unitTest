package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestLegacyEventFilterRedactsGenerationEventsWithoutDroppingSequence(t *testing.T) {
	s := &Session{}
	event := task.Event{
		Sequence: 7,
		ID:       strings.Repeat("a", 32),
		EventDraft: task.EventDraft{
			TaskID:  strings.Repeat("b", 32),
			Type:    task.EventTestGenerationStateChanged,
			At:      time.Unix(123, 0).UTC(),
			Payload: json.RawMessage(`{"runId":"cccccccccccccccccccccccccccccccc","from":"queued","to":"baseline"}`),
		},
	}

	projected := s.legacyEventTransform(context.Background(), event)
	if projected.Sequence != event.Sequence {
		t.Fatalf("sequence = %d, want %d", projected.Sequence, event.Sequence)
	}
	if projected.TaskID != "00000000000000000000000000000000" {
		t.Fatalf("task id = %q, want redacted tombstone id", projected.TaskID)
	}
	if projected.Type != task.EventTaskOutput {
		t.Fatalf("event type = %q, want %q", projected.Type, task.EventTaskOutput)
	}
}
