-- Generation progress has a separate per-run sequence namespace. Legacy
-- event subscribers must never observe generation content or sequence gaps.
CREATE TABLE test_generation_events (
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL CHECK (sequence BETWEEN 1 AND 9007199254740991),
  event_id TEXT NOT NULL UNIQUE,
  event_type TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
  PRIMARY KEY (task_id, sequence)
);

INSERT INTO test_generation_events(task_id,sequence,event_id,event_type,occurred_at,payload_json)
SELECT e.task_id, ROW_NUMBER() OVER (PARTITION BY e.task_id ORDER BY e.sequence),
       e.event_id,e.event_type,e.occurred_at,e.payload_json
FROM task_events e JOIN tasks t ON t.task_id=e.task_id
WHERE t.kind='test_generation'
ORDER BY e.task_id,e.sequence;

UPDATE tasks SET last_sequence=COALESCE((
  SELECT MAX(sequence) FROM test_generation_events g WHERE g.task_id=tasks.task_id
),0) WHERE kind='test_generation';

-- Preserve historical global sequence continuity for old saved subscriptions.
-- The original generation content is available only in the private journal;
-- legacy clients receive a closed, empty output tombstone at the same cursor.
UPDATE task_events
SET event_type='task.output',
    payload_json='{"stepId":"generation-redacted","stream":"combined","text":"","truncated":false}'
WHERE task_id IN (SELECT task_id FROM tasks WHERE kind='test_generation');
