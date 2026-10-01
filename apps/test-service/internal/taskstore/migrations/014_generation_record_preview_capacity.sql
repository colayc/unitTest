-- Rebuild the generation run table to allow a bounded exact review diff in
-- the durable record. Migration runner disables foreign keys for the atomic
-- rebuild and verifies all references before committing.
CREATE TABLE test_generation_runs_v14 (
  run_id TEXT PRIMARY KEY CHECK (length(run_id)=32 AND run_id NOT GLOB '*[^0-9a-f]*'),
  task_id TEXT NOT NULL UNIQUE REFERENCES tasks(task_id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('queued','baseline','analyzing','solving','rendering','validating','minimizing','awaiting_confirmation','accepted','rejected','cancelled','failed')),
  revision INTEGER NOT NULL CHECK (revision BETWEEN 1 AND 10000),
  artifact_digests_json TEXT NOT NULL CHECK (json_valid(artifact_digests_json)),
  candidate_count INTEGER NOT NULL CHECK (candidate_count BETWEEN 0 AND 1000),
  record_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(record_json) AND length(record_json) <= 393216),
  record_sha256 TEXT NOT NULL DEFAULT '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a' CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*')
);
INSERT INTO test_generation_runs_v14(run_id,task_id,state,revision,artifact_digests_json,candidate_count,record_json,record_sha256)
SELECT run_id,task_id,state,revision,artifact_digests_json,candidate_count,record_json,record_sha256 FROM test_generation_runs;
DROP TABLE test_generation_runs;
ALTER TABLE test_generation_runs_v14 RENAME TO test_generation_runs;
