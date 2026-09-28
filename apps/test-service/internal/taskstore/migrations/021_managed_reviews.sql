-- Optional, owner-bound immutable managed review manifests. Failure cannot
-- invalidate v1/v1.5 task, coverage or generation history.
CREATE TABLE managed_review_meta (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  mac_key BLOB NOT NULL CHECK(length(mac_key)=32)
);
INSERT INTO managed_review_meta(singleton,mac_key) VALUES(1,randomblob(32));
CREATE TABLE managed_review_manifests (
  review_id TEXT PRIMARY KEY CHECK(length(review_id)=32 AND review_id NOT GLOB '*[^0-9a-f]*'),
  owner_digest TEXT NOT NULL CHECK(length(owner_digest)=64 AND owner_digest NOT GLOB '*[^0-9a-f]*'),
  run_id TEXT NOT NULL REFERENCES test_generation_runs(run_id) ON DELETE RESTRICT,
  run_revision INTEGER NOT NULL CHECK(run_revision>=1),
  project_id TEXT NOT NULL,
  workspace_generation TEXT NOT NULL CHECK(length(workspace_generation)=64 AND workspace_generation NOT GLOB '*[^0-9a-f]*'),
  report_id TEXT NOT NULL REFERENCES coverage_detail_reports(report_id) ON DELETE RESTRICT CHECK(length(report_id)=32 AND report_id NOT GLOB '*[^0-9a-f]*'),
  toolchain_id TEXT NOT NULL,
  review_digest TEXT NOT NULL CHECK(length(review_digest)=64 AND review_digest NOT GLOB '*[^0-9a-f]*'),
  candidate_set_digest TEXT NOT NULL CHECK(length(candidate_set_digest)=64 AND candidate_set_digest NOT GLOB '*[^0-9a-f]*'),
  manifest_json BLOB NOT NULL CHECK(length(manifest_json) BETWEEN 1 AND 8192),
  manifest_mac TEXT NOT NULL CHECK(length(manifest_mac)=64 AND manifest_mac NOT GLOB '*[^0-9a-f]*'),
  candidate_count INTEGER NOT NULL CHECK(candidate_count BETWEEN 1 AND 200),
  artifact_ref TEXT NOT NULL CHECK(length(artifact_ref) BETWEEN 1 AND 256),
  status TEXT NOT NULL CHECK(status IN ('current','stale')),
  status_mac TEXT NOT NULL CHECK(length(status_mac)=64 AND status_mac NOT GLOB '*[^0-9a-f]*'),
  created_at TEXT NOT NULL
);
CREATE INDEX managed_review_owner_order ON managed_review_manifests(owner_digest,project_id,review_id);
CREATE TABLE managed_review_candidates (
  review_id TEXT NOT NULL REFERENCES managed_review_manifests(review_id) ON DELETE RESTRICT,
  candidate_id TEXT NOT NULL CHECK(length(candidate_id)=36 AND candidate_id GLOB 'utc_*'),
  candidate_json BLOB NOT NULL CHECK(length(candidate_json) BETWEEN 1 AND 524288),
  candidate_mac TEXT NOT NULL CHECK(length(candidate_mac)=64 AND candidate_mac NOT GLOB '*[^0-9a-f]*'),
  PRIMARY KEY(review_id,candidate_id)
);
