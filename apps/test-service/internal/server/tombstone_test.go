package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/task"
)

func TestLegacyCursorTombstoneDoesNotExposeGenerationIdentityOrTime(t *testing.T) {
	original := task.Event{Sequence: 7, ID: strings.Repeat("a", 32), EventDraft: task.EventDraft{
		TaskID: strings.Repeat("b", 32), Type: task.EventTaskOutput,
		At:      time.Date(2026, 9, 27, 12, 30, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"stepId":"cursor-redacted","stream":"combined","text":"","truncated":false}`),
	}}
	var stableID string
	for _, version := range []string{protocol.Version10, protocol.Version11, protocol.Version12, protocol.Version13, protocol.Version14} {
		projected, err := toProtocolEvent(original, version)
		if err != nil || projected.Sequence != 7 || projected.MessageID == original.ID || projected.TaskID == original.TaskID || projected.SentAt == original.At.Format(time.RFC3339Nano) ||
			strings.Contains(string(projected.Payload), original.TaskID) || strings.Contains(string(projected.Payload), "generation") {
			t.Fatalf("version %s leaked tombstone metadata: %+v, %v", version, projected, err)
		}
		if stableID != "" && projected.MessageID != stableID {
			t.Fatalf("synthetic ID changed across protocol versions: %q, %q", stableID, projected.MessageID)
		}
		stableID = projected.MessageID
	}
	next := original
	next.Sequence++
	projected, err := toProtocolEvent(next, protocol.Version14)
	if err != nil || projected.MessageID == stableID {
		t.Fatalf("synthetic ID was not sequence-scoped: %+v, %v", projected, err)
	}
}
