CREATE TABLE tasks (
  task_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind = 'simulation'),
  scenario TEXT NOT NULL,
  timeout_ms INTEGER NOT NULL CHECK (timeout_ms BETWEEN 1 AND 86400000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','cancelling','finished')),
  outcome TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  CHECK ((status = 'finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL) OR
         (status <> 'finished' AND outcome IS NULL AND finished_at IS NULL))
);

CREATE TABLE task_events (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL UNIQUE,
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  payload_version INTEGER NOT NULL DEFAULT 1,
  payload_json TEXT NOT NULL CHECK (json_valid(payload_json))
);

CREATE INDEX task_events_task_sequence ON task_events(task_id, sequence);
CREATE INDEX tasks_history_order ON tasks(created_at DESC, task_id DESC);

CREATE TABLE artifacts (
  artifact_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  mime_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
  created_at TEXT NOT NULL,
  complete INTEGER NOT NULL CHECK (complete = 1)
);

CREATE INDEX artifacts_task_order ON artifacts(task_id, created_at, artifact_id);

CREATE TABLE process_leases (
  task_id TEXT PRIMARY KEY REFERENCES tasks(task_id) ON DELETE CASCADE,
  host_pid INTEGER NOT NULL,
  host_start_identity TEXT NOT NULL,
  target_process_group INTEGER NOT NULL DEFAULT 0,
  service_instance_id TEXT NOT NULL
);
