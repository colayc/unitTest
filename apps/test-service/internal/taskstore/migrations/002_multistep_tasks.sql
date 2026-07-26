-- unit-test-ide: foreign-keys-off
CREATE TABLE tasks_v2 (
  task_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('simulation','cmake_build')),
  scenario TEXT,
  request_json TEXT NOT NULL CHECK (json_valid(request_json)),
  workspace_generation TEXT NOT NULL DEFAULT ''
    CHECK (workspace_generation = '' OR (
      length(workspace_generation) = 64 AND
      workspace_generation NOT GLOB '*[^0-9a-f]*'
    )),
  plan_fingerprint TEXT NOT NULL DEFAULT '',
  active_step TEXT NOT NULL DEFAULT '',
  timeout_ms INTEGER NOT NULL CHECK (timeout_ms BETWEEN 1 AND 86400000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','cancelling','finished')),
  outcome TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  CHECK ((kind = 'simulation' AND scenario IS NOT NULL AND workspace_generation = '') OR
         (kind = 'cmake_build' AND scenario IS NULL AND workspace_generation <> '')),
  CHECK ((status = 'finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL) OR
         (status <> 'finished' AND outcome IS NULL AND finished_at IS NULL))
);

INSERT INTO tasks_v2(
  task_id, idempotency_key, request_hash, kind, scenario, request_json,
  workspace_generation, plan_fingerprint, active_step, timeout_ms, status,
  outcome, created_at, started_at, finished_at, last_sequence, error_code, error_message
)
SELECT
  task_id, idempotency_key, request_hash, 'simulation', scenario,
  json_object('scenario', scenario), '', '', '', timeout_ms, status,
  outcome, created_at, started_at, finished_at, last_sequence, error_code, error_message
FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_v2 RENAME TO tasks;

CREATE INDEX tasks_history_order ON tasks(created_at DESC, task_id DESC);

CREATE TABLE task_steps (
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  step_ordinal INTEGER NOT NULL CHECK (step_ordinal >= 0),
  step_id TEXT NOT NULL,
  step_kind TEXT NOT NULL CHECK (step_kind IN ('simulation','configure','build')),
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped')),
  started_at TEXT,
  finished_at TEXT,
  exit_code INTEGER,
  error_code TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (task_id, step_ordinal),
  UNIQUE (task_id, step_id)
);
