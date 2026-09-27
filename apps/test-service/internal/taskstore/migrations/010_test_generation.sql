-- unit-test-ide: foreign-keys-off
CREATE TABLE tasks_v10 (
  task_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('simulation','cmake_build','test_discovery','test_run','coverage_run','test_generation')),
  scenario TEXT,
  request_json TEXT NOT NULL CHECK (json_valid(request_json)),
  workspace_generation TEXT NOT NULL DEFAULT '' CHECK (workspace_generation = '' OR (length(workspace_generation)=64 AND workspace_generation NOT GLOB '*[^0-9a-f]*')),
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
  CHECK ((kind='simulation' AND scenario IS NOT NULL AND workspace_generation='') OR
    (kind IN ('cmake_build','test_discovery','test_run','coverage_run','test_generation') AND scenario IS NULL AND workspace_generation<>'')),
  CHECK ((status='finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL) OR
    (status<>'finished' AND outcome IS NULL AND finished_at IS NULL))
);
INSERT INTO tasks_v10 SELECT * FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_v10 RENAME TO tasks;
CREATE INDEX tasks_history_order ON tasks(created_at DESC, task_id DESC);

CREATE TABLE test_generation_runs (
  run_id TEXT PRIMARY KEY CHECK (length(run_id)=32 AND run_id NOT GLOB '*[^0-9a-f]*'),
  task_id TEXT NOT NULL UNIQUE REFERENCES tasks(task_id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('queued','baseline','analyzing','solving','rendering','validating','minimizing','awaiting_confirmation','accepted','rejected','cancelled','failed')),
  revision INTEGER NOT NULL CHECK (revision BETWEEN 1 AND 10000),
  artifact_digests_json TEXT NOT NULL CHECK (json_valid(artifact_digests_json)),
  candidate_count INTEGER NOT NULL CHECK (candidate_count BETWEEN 0 AND 1000)
);
CREATE TABLE test_generation_candidates (
  run_id TEXT NOT NULL REFERENCES test_generation_runs(run_id) ON DELETE CASCADE,
  case_id TEXT NOT NULL CHECK (length(case_id)=32 AND case_id NOT GLOB '*[^0-9a-f]*'),
  candidate_json TEXT NOT NULL CHECK (json_valid(candidate_json) AND length(candidate_json)<=65536),
  PRIMARY KEY (run_id, case_id)
);
