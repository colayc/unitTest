-- unit-test-ide: foreign-keys-off
CREATE TABLE tasks_v9 (
  task_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (
    kind IN ('simulation','cmake_build','test_discovery','test_run','coverage_run')
  ),
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
  status TEXT NOT NULL CHECK (
    status IN ('queued','running','cancelling','finished')
  ),
  outcome TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  CHECK (
    (kind = 'simulation' AND scenario IS NOT NULL AND
      workspace_generation = '') OR
    (kind IN ('cmake_build','test_discovery','test_run','coverage_run') AND
      scenario IS NULL AND workspace_generation <> '')
  ),
  CHECK (
    (status = 'finished' AND outcome IS NOT NULL AND
      finished_at IS NOT NULL) OR
    (status <> 'finished' AND outcome IS NULL AND finished_at IS NULL)
  )
);

INSERT INTO tasks_v9(
  task_id, idempotency_key, request_hash, kind, scenario, request_json,
  workspace_generation, plan_fingerprint, active_step, timeout_ms, status,
  outcome, created_at, started_at, finished_at, last_sequence,
  error_code, error_message
)
SELECT
  task_id, idempotency_key, request_hash, kind, scenario, request_json,
  workspace_generation, plan_fingerprint, active_step, timeout_ms, status,
  outcome, created_at, started_at, finished_at, last_sequence,
  error_code, error_message
FROM tasks;

CREATE TABLE task_steps_v9 (
  task_id TEXT NOT NULL REFERENCES tasks_v9(task_id) ON DELETE CASCADE,
  step_ordinal INTEGER NOT NULL CHECK (step_ordinal >= 0),
  step_id TEXT NOT NULL,
  step_kind TEXT NOT NULL CHECK (
    step_kind IN (
      'simulation','configure','build','test-discovery','test-run',
      'coverage-configure','coverage-build','coverage-test','coverage-merge',
      'coverage-normalize','coverage-report','coverage-publish'
    )
  ),
  status TEXT NOT NULL CHECK (
    status IN ('pending','running','succeeded','failed','skipped')
  ),
  started_at TEXT,
  finished_at TEXT,
  exit_code INTEGER,
  error_code TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (task_id, step_ordinal),
  UNIQUE (task_id, step_id)
);

INSERT INTO task_steps_v9(
  task_id, step_ordinal, step_id, step_kind, status, started_at,
  finished_at, exit_code, error_code
)
SELECT
  task_id, step_ordinal, step_id, step_kind, status, started_at,
  finished_at, exit_code, error_code
FROM task_steps;

DROP TABLE task_steps;
DROP TABLE tasks;
ALTER TABLE tasks_v9 RENAME TO tasks;
ALTER TABLE task_steps_v9 RENAME TO task_steps;

CREATE INDEX tasks_history_order
  ON tasks(created_at DESC, task_id DESC);

CREATE UNIQUE INDEX artifacts_identity_task
  ON artifacts(artifact_id, task_id);
CREATE UNIQUE INDEX test_runs_identity_task
  ON test_runs(run_id, task_id);

CREATE TABLE coverage_runs (
  coverage_run_id TEXT PRIMARY KEY CHECK (
    length(coverage_run_id) = 32 AND coverage_run_id NOT GLOB '*[^0-9a-f]*'
  ),
  task_id TEXT NOT NULL UNIQUE CHECK (
    length(task_id) = 32 AND task_id NOT GLOB '*[^0-9a-f]*'
  ) REFERENCES tasks(task_id) ON DELETE CASCADE,
  test_run_id TEXT NOT NULL UNIQUE CHECK (
    length(test_run_id) = 32 AND test_run_id NOT GLOB '*[^0-9a-f]*'
  ),
  idempotency_key TEXT NOT NULL UNIQUE CHECK (
    length(idempotency_key) = 32 AND idempotency_key NOT GLOB '*[^0-9a-f]*'
  ),
  request_json TEXT NOT NULL CHECK (json_valid(request_json)),
  workspace_generation TEXT NOT NULL CHECK (
    length(workspace_generation) = 64 AND workspace_generation NOT GLOB '*[^0-9a-f]*'
  ),
  project_id TEXT NOT NULL CHECK (
    length(project_id) BETWEEN 1 AND 64 AND
    substr(project_id, 1, 1) GLOB '[A-Za-z0-9]' AND
    project_id NOT GLOB '*[^A-Za-z0-9._-]*'
  ),
  coverage_profile_id TEXT NOT NULL CHECK (
    length(coverage_profile_id) BETWEEN 1 AND 64 AND
    substr(coverage_profile_id, 1, 1) GLOB '[A-Za-z0-9]' AND
    coverage_profile_id NOT GLOB '*[^A-Za-z0-9._-]*'
  ),
  catalog_revision TEXT NOT NULL CHECK (
    length(catalog_revision) = 64 AND catalog_revision NOT GLOB '*[^0-9a-f]*'
  ),
  selection_snapshot_json TEXT NOT NULL CHECK (json_valid(selection_snapshot_json)),
  repeat_count INTEGER NOT NULL CHECK (repeat_count BETWEEN 1 AND 100),
  timeout_ms INTEGER NOT NULL CHECK (timeout_ms BETWEEN 1 AND 86400000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','finished')),
  outcome TEXT CHECK (outcome IS NULL OR outcome IN ('available','partial','unavailable','cancelled')),
  reason TEXT CHECK (reason IS NULL OR reason IN (
    'user_cancelled','task_timed_out','instrumentation_failed','build_failed',
    'profile_collection_failed','merge_failed','normalization_failed',
    'report_generation_failed','persistence_failed','service_restarted'
  )),
  toolchain_json TEXT NOT NULL CHECK (json_valid(toolchain_json)),
  summary_json TEXT CHECK (summary_json IS NULL OR json_valid(summary_json)),
  report_id TEXT UNIQUE CHECK (
    report_id IS NULL OR (length(report_id) = 32 AND report_id NOT GLOB '*[^0-9a-f]*')
  ),
  coverage_json_artifact_id TEXT CHECK (
    coverage_json_artifact_id IS NULL OR (
      length(coverage_json_artifact_id) = 32 AND coverage_json_artifact_id NOT GLOB '*[^0-9a-f]*'
    )
  ),
  junit_xml_artifact_id TEXT CHECK (
    junit_xml_artifact_id IS NULL OR (
      length(junit_xml_artifact_id) = 32 AND junit_xml_artifact_id NOT GLOB '*[^0-9a-f]*'
    )
  ),
  coverage_html_artifact_id TEXT CHECK (
    coverage_html_artifact_id IS NULL OR (
      length(coverage_html_artifact_id) = 32 AND coverage_html_artifact_id NOT GLOB '*[^0-9a-f]*'
    )
  ),
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0 CHECK (last_sequence BETWEEN 0 AND 9007199254740991),
  UNIQUE(coverage_run_id, task_id),
  FOREIGN KEY(test_run_id, task_id) REFERENCES test_runs(run_id, task_id) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(coverage_json_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id),
  FOREIGN KEY(junit_xml_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id),
  FOREIGN KEY(coverage_html_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id),
  CHECK (
    (status = 'queued' AND started_at IS NULL AND finished_at IS NULL AND outcome IS NULL AND reason IS NULL AND
      report_id IS NULL AND summary_json IS NULL AND coverage_json_artifact_id IS NULL AND
      junit_xml_artifact_id IS NULL AND coverage_html_artifact_id IS NULL) OR
    (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL AND outcome IS NULL AND reason IS NULL AND
      report_id IS NULL AND summary_json IS NULL AND coverage_json_artifact_id IS NULL AND
      junit_xml_artifact_id IS NULL AND coverage_html_artifact_id IS NULL) OR
    (status = 'finished' AND outcome IN ('available','partial') AND finished_at IS NOT NULL AND reason IS NULL AND
      report_id IS NOT NULL AND summary_json IS NOT NULL AND coverage_json_artifact_id IS NOT NULL AND
      junit_xml_artifact_id IS NOT NULL AND coverage_html_artifact_id IS NOT NULL) OR
    (status = 'finished' AND outcome = 'unavailable' AND finished_at IS NOT NULL AND
      reason IN ('instrumentation_failed','build_failed','profile_collection_failed','merge_failed',
        'normalization_failed','report_generation_failed','persistence_failed','service_restarted') AND
      report_id IS NULL AND summary_json IS NULL AND coverage_json_artifact_id IS NULL AND
      junit_xml_artifact_id IS NULL AND coverage_html_artifact_id IS NULL) OR
    (status = 'finished' AND outcome = 'cancelled' AND finished_at IS NOT NULL AND
      reason IN ('user_cancelled','task_timed_out') AND report_id IS NULL AND summary_json IS NULL AND
      coverage_json_artifact_id IS NULL AND junit_xml_artifact_id IS NULL AND coverage_html_artifact_id IS NULL)
  ),
  CHECK (
    coverage_json_artifact_id IS NULL OR junit_xml_artifact_id IS NULL OR
    coverage_json_artifact_id <> junit_xml_artifact_id
  ),
  CHECK (
    coverage_json_artifact_id IS NULL OR coverage_html_artifact_id IS NULL OR
    coverage_json_artifact_id <> coverage_html_artifact_id
  ),
  CHECK (
    junit_xml_artifact_id IS NULL OR coverage_html_artifact_id IS NULL OR
    junit_xml_artifact_id <> coverage_html_artifact_id
  )
);

CREATE TABLE coverage_reports (
  report_id TEXT PRIMARY KEY CHECK (
    length(report_id) = 32 AND report_id NOT GLOB '*[^0-9a-f]*'
  ),
  coverage_run_id TEXT NOT NULL UNIQUE CHECK (
    length(coverage_run_id) = 32 AND coverage_run_id NOT GLOB '*[^0-9a-f]*'
  ),
  task_id TEXT NOT NULL CHECK (
    length(task_id) = 32 AND task_id NOT GLOB '*[^0-9a-f]*'
  ),
  test_run_id TEXT NOT NULL UNIQUE CHECK (
    length(test_run_id) = 32 AND test_run_id NOT GLOB '*[^0-9a-f]*'
  ),
  schema_version TEXT NOT NULL CHECK (schema_version='1.0'),
  created_at TEXT NOT NULL,
  completeness_json TEXT NOT NULL CHECK (json_valid(completeness_json)),
  summary_json TEXT NOT NULL CHECK (json_valid(summary_json)),
  toolchain_json TEXT NOT NULL CHECK (json_valid(toolchain_json)),
  artifact_id TEXT NOT NULL UNIQUE CHECK (
    length(artifact_id) = 32 AND artifact_id NOT GLOB '*[^0-9a-f]*'
  ),
  FOREIGN KEY(coverage_run_id, task_id) REFERENCES coverage_runs(coverage_run_id, task_id) ON DELETE CASCADE,
  FOREIGN KEY(test_run_id, task_id) REFERENCES test_runs(run_id, task_id),
  FOREIGN KEY(artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id)
);

CREATE INDEX coverage_runs_workspace_history
  ON coverage_runs(workspace_generation, created_at DESC, coverage_run_id DESC);
CREATE INDEX coverage_runs_workspace_project_history
  ON coverage_runs(workspace_generation, project_id, created_at DESC, coverage_run_id DESC);
CREATE INDEX coverage_runs_workspace_project_profile_history
  ON coverage_runs(workspace_generation, project_id, coverage_profile_id, created_at DESC, coverage_run_id DESC);
