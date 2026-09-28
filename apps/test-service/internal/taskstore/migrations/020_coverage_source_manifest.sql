-- Independent source-set commitment for source-attested current coverage.
-- Existing 016 rows have no manifest and remain readable through legacy pages,
-- but cannot be promoted to attested current IDs.
CREATE TABLE coverage_detail_manifests (
  report_id TEXT PRIMARY KEY REFERENCES coverage_detail_reports(report_id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  workspace_generation TEXT NOT NULL CHECK(length(workspace_generation)=64 AND workspace_generation NOT GLOB '*[^0-9a-f]*'),
  file_count INTEGER NOT NULL CHECK(file_count BETWEEN 0 AND 100000),
  manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256)=64 AND manifest_sha256 NOT GLOB '*[^0-9a-f]*')
);
CREATE TABLE coverage_detail_manifest_files (
  report_id TEXT NOT NULL REFERENCES coverage_detail_manifests(report_id) ON DELETE CASCADE,
  relative_path TEXT NOT NULL,
  source_sha256 TEXT NOT NULL CHECK(length(source_sha256)=64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'),
  PRIMARY KEY(report_id, relative_path)
);
