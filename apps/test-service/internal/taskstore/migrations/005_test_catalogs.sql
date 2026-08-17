CREATE TABLE test_catalogs (
  project_id TEXT NOT NULL,
  profile_id TEXT NOT NULL CHECK (
    length(profile_id) = 64 AND
    profile_id NOT GLOB '*[^0-9a-f]*'
  ),
  revision TEXT NOT NULL CHECK (
    length(revision) = 64 AND
    revision NOT GLOB '*[^0-9a-f]*'
  ),
  generated_at TEXT NOT NULL,
  artifact_id TEXT NOT NULL UNIQUE REFERENCES artifacts(artifact_id),
  container_count INTEGER NOT NULL CHECK (container_count BETWEEN 0 AND 10000),
  item_count INTEGER NOT NULL CHECK (item_count BETWEEN 0 AND 100000),
  diagnostic_count INTEGER NOT NULL CHECK (diagnostic_count BETWEEN 0 AND 1000),
  diagnostics_json TEXT NOT NULL CHECK (json_valid(diagnostics_json)),
  partial INTEGER NOT NULL CHECK (partial = 0),
  PRIMARY KEY (project_id, profile_id, revision)
);

CREATE TABLE current_test_catalogs (
  project_id TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  PRIMARY KEY (project_id, profile_id),
  FOREIGN KEY (project_id, profile_id, revision)
    REFERENCES test_catalogs(project_id, profile_id, revision)
);

CREATE TABLE test_catalog_entries (
  project_id TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  entry_kind TEXT NOT NULL CHECK (entry_kind IN ('container','item')),
  stable_id TEXT NOT NULL CHECK (
    length(stable_id) = 72 AND
    stable_id GLOB 'utid-v1-*'
  ),
  payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
  PRIMARY KEY (project_id, profile_id, revision, ordinal),
  UNIQUE (project_id, profile_id, revision, stable_id),
  FOREIGN KEY (project_id, profile_id, revision)
    REFERENCES test_catalogs(project_id, profile_id, revision)
    ON DELETE CASCADE
);

CREATE INDEX test_catalog_entries_page
  ON test_catalog_entries(project_id, profile_id, revision, ordinal);
