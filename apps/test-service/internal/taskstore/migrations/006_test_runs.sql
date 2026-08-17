CREATE TABLE test_runs (
  run_id TEXT PRIMARY KEY CHECK (
    length(run_id) = 32 AND run_id NOT GLOB '*[^0-9a-f]*'
  ),
  task_id TEXT NOT NULL UNIQUE REFERENCES tasks(task_id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL UNIQUE CHECK (
    length(idempotency_key) = 32 AND idempotency_key NOT GLOB '*[^0-9a-f]*'
  ),
  project_id TEXT NOT NULL,
  profile_id TEXT NOT NULL CHECK (
    length(profile_id) = 64 AND profile_id NOT GLOB '*[^0-9a-f]*'
  ),
  toolchain_id TEXT NOT NULL,
  catalog_revision TEXT NOT NULL CHECK (
    length(catalog_revision) = 64 AND catalog_revision NOT GLOB '*[^0-9a-f]*'
  ),
  selection_json TEXT NOT NULL CHECK (json_valid(selection_json)),
  status TEXT NOT NULL CHECK (status IN ('queued','running','completed')),
  outcome TEXT CHECK (
    outcome IS NULL OR outcome IN (
      'passed','failed','blocked','errored','cancelled','timed_out','interrupted'
    )
  ),
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  summary_json TEXT NOT NULL CHECK (json_valid(summary_json)),
  result_revision TEXT NOT NULL CHECK (
    length(result_revision) = 64 AND result_revision NOT GLOB '*[^0-9a-f]*'
  ),
  incomplete INTEGER NOT NULL CHECK (incomplete IN (0,1)),
  CHECK (
    (status='queued' AND outcome IS NULL AND started_at IS NULL AND finished_at IS NULL) OR
    (status='running' AND outcome IS NULL AND started_at IS NOT NULL AND finished_at IS NULL) OR
    (status='completed' AND outcome IS NOT NULL AND finished_at IS NOT NULL)
  )
);

CREATE INDEX test_runs_history
  ON test_runs(created_at DESC, run_id DESC);
CREATE INDEX test_runs_project_history
  ON test_runs(project_id, profile_id, created_at DESC, run_id DESC);

CREATE TABLE test_run_results (
  run_id TEXT NOT NULL REFERENCES test_runs(run_id) ON DELETE CASCADE,
  item_id TEXT NOT NULL CHECK (
    length(item_id) = 72 AND item_id GLOB 'utid-v1-*'
  ),
  container_id TEXT NOT NULL CHECK (
    length(container_id) = 72 AND container_id GLOB 'utid-v1-*'
  ),
  iteration INTEGER NOT NULL CHECK (iteration BETWEEN 1 AND 100),
  outcome TEXT NOT NULL CHECK (
    outcome IN ('passed','failed','skipped','errored','cancelled','timed_out','not_run')
  ),
  partial INTEGER NOT NULL CHECK (partial IN (0,1)),
  payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
  PRIMARY KEY (run_id, item_id, iteration)
);

CREATE INDEX test_run_results_failure
  ON test_run_results(run_id, outcome, container_id, item_id, iteration);

CREATE TABLE test_run_artifacts (
  run_id TEXT NOT NULL REFERENCES test_runs(run_id) ON DELETE CASCADE,
  artifact_id TEXT NOT NULL UNIQUE REFERENCES artifacts(artifact_id),
  PRIMARY KEY (run_id, artifact_id)
);
