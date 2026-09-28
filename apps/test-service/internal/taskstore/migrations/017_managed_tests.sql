-- Optional managed generated-test registry. No v1.5 row is changed.
CREATE TABLE managed_test_records (
  case_id TEXT PRIMARY KEY CHECK(length(case_id)=36 AND case_id GLOB 'utc_*'),
  project_id TEXT NOT NULL,
  source_file_id TEXT NOT NULL CHECK(length(source_file_id)=32 AND source_file_id NOT GLOB '*[^0-9a-f]*'),
  function_id TEXT NOT NULL CHECK(length(function_id)=32 AND function_id NOT GLOB '*[^0-9a-f]*'),
  source_relative_path TEXT NOT NULL,
  scenario_id TEXT NOT NULL,
  test_relative_path TEXT NOT NULL,
  accepted_block_digest TEXT NOT NULL CHECK(length(accepted_block_digest)=64 AND accepted_block_digest NOT GLOB '*[^0-9a-f]*'),
  generator_version TEXT NOT NULL,
  framework TEXT NOT NULL,
  toolchain_id TEXT NOT NULL,
  source_digest TEXT NOT NULL CHECK(length(source_digest)=64 AND source_digest NOT GLOB '*[^0-9a-f]*'),
  validation_receipt_digest TEXT NOT NULL CHECK(length(validation_receipt_digest)=64 AND validation_receipt_digest NOT GLOB '*[^0-9a-f]*'),
  status TEXT NOT NULL CHECK(status IN ('current','stale','conflicted','orphaned','invalid')),
  last_verified_at TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK(revision >= 1)
);
CREATE INDEX managed_test_records_project_order ON managed_test_records(project_id,source_file_id,case_id);
CREATE INDEX managed_test_records_project_status_order ON managed_test_records(project_id,status,source_file_id,case_id);
CREATE TABLE managed_test_transitions (
  case_id TEXT NOT NULL REFERENCES managed_test_records(case_id) ON DELETE RESTRICT,
  revision INTEGER NOT NULL CHECK(revision >= 1),
  from_status TEXT NOT NULL CHECK(from_status IN ('none','current','stale','conflicted','orphaned','invalid')),
  to_status TEXT NOT NULL CHECK(to_status IN ('current','stale','conflicted','orphaned','invalid')),
  reason TEXT NOT NULL,
  receipt_digest TEXT NOT NULL CHECK(length(receipt_digest)=64 AND receipt_digest NOT GLOB '*[^0-9a-f]*'),
  acceptance_id TEXT,
  record_digest TEXT,
  occurred_at TEXT NOT NULL,
  PRIMARY KEY(case_id,revision),
  UNIQUE(acceptance_id)
);
CREATE TABLE managed_test_pending_acceptances (
  acceptance_id TEXT PRIMARY KEY CHECK(length(acceptance_id)=32 AND acceptance_id NOT GLOB '*[^0-9a-f]*'),
  case_id TEXT NOT NULL,
  review_digest TEXT NOT NULL CHECK(length(review_digest)=64 AND review_digest NOT GLOB '*[^0-9a-f]*'),
  preimage_digest TEXT NOT NULL CHECK(length(preimage_digest)=64 AND preimage_digest NOT GLOB '*[^0-9a-f]*'),
  published_file_digest TEXT NOT NULL CHECK(length(published_file_digest)=64 AND published_file_digest NOT GLOB '*[^0-9a-f]*'),
  record_json TEXT NOT NULL CHECK(json_valid(record_json)),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','file_written')),
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX managed_test_pending_case ON managed_test_pending_acceptances(case_id);
CREATE TABLE managed_test_commits (
  acceptance_id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES managed_test_records(case_id) ON DELETE RESTRICT,
  acceptance_digest TEXT NOT NULL CHECK(length(acceptance_digest)=64 AND acceptance_digest NOT GLOB '*[^0-9a-f]*')
);
CREATE TABLE managed_test_registry_meta (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  cursor_key BLOB NOT NULL CHECK(length(cursor_key)=32)
);
INSERT INTO managed_test_registry_meta(singleton,cursor_key) VALUES(1,randomblob(32));
