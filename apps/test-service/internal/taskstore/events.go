package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func insertEvents(ctx context.Context, tx *sql.Tx, drafts []task.EventDraft, newID func() string) ([]task.Event, error) {
	events := make([]task.Event, 0, len(drafts))
	for _, draft := range drafts {
		if !validEventDraft(draft) {
			return nil, task.ErrInvalidArgument
		}
		event := task.Event{ID: newID(), EventDraft: draft}
		result, err := tx.ExecContext(ctx, `INSERT INTO task_events(event_id, task_id, event_type, occurred_at, payload_version, payload_json)
			VALUES(?,?,?,?,1,?)`, event.ID, event.TaskID, string(event.Type), formatTime(event.At), string(event.Payload))
		if err != nil {
			return nil, storageError("insert event", err)
		}
		event.Sequence, err = result.LastInsertId()
		if err != nil {
			return nil, storageError("read event sequence", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func validEventDraft(value task.EventDraft) bool {
	return value.TaskID != "" &&
		task.ValidEventType(value.Type) &&
		!value.At.IsZero() &&
		json.Valid(value.Payload)
}

func hasTaskFinishedEvent(drafts []task.EventDraft) bool {
	for _, draft := range drafts {
		if draft.Type == task.EventTaskFinished {
			return true
		}
	}
	return false
}

func (s *Store) AppendEvent(ctx context.Context, taskID string, draft task.EventDraft) (task.Event, error) {
	if taskID == "" || draft.TaskID != taskID || !validEventDraft(draft) {
		return task.Event{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Event{}, storageError("begin append event", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM tasks WHERE task_id=?`, taskID).Scan(&exists); isNoRows(err) {
		return task.Event{}, task.ErrNotFound
	} else if err != nil {
		return task.Event{}, storageError("find event task", err)
	}
	events, err := insertEvents(ctx, tx, []task.EventDraft{draft}, s.newID)
	if err != nil {
		return task.Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, events[0].Sequence, taskID); err != nil {
		return task.Event{}, storageError("update task sequence", err)
	}
	if err := tx.Commit(); err != nil {
		return task.Event{}, storageError("commit append event", err)
	}
	return events[0], nil
}

func (s *Store) Watermark(ctx context.Context) (int64, error) {
	var watermark int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM task_events`).Scan(&watermark); err != nil {
		return 0, storageError("read event watermark", err)
	}
	return watermark, nil
}

func (s *Store) EventsAfter(ctx context.Context, after, through int64, limit int) ([]task.Event, error) {
	if after < 0 || through < after || limit < 1 {
		return nil, task.ErrInvalidArgument
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence, event_id, task_id, event_type, occurred_at, payload_json
		FROM task_events WHERE sequence > ? AND sequence <= ? ORDER BY sequence LIMIT ?`, after, through, limit)
	if err != nil {
		return nil, storageError("list events", err)
	}
	defer rows.Close()
	events := make([]task.Event, 0)
	for rows.Next() {
		var event task.Event
		var eventType, occurredAt, payload string
		if err := rows.Scan(&event.Sequence, &event.ID, &event.TaskID, &eventType, &occurredAt, &payload); err != nil {
			return nil, storageError("read event", err)
		}
		at, err := parseEventTime(occurredAt)
		if err != nil {
			return nil, storageError("read event time", err)
		}
		event.Type = task.EventType(eventType)
		event.At = at
		event.Payload = json.RawMessage(payload)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list events", err)
	}
	return events, nil
}

func parseEventTime(value string) (timeValue time.Time, err error) {
	return time.Parse(time.RFC3339Nano, value)
}
