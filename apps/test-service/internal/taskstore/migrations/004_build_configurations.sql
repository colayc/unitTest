CREATE TABLE build_configurations (
  workspace_id TEXT NOT NULL CHECK (
    length(workspace_id) = 64 AND
    workspace_id NOT GLOB '*[^0-9a-f]*'
  ),
  project_id TEXT NOT NULL,
  profile_id TEXT NOT NULL CHECK (
    length(profile_id) = 64 AND
    profile_id NOT GLOB '*[^0-9a-f]*'
  ),
  configure_fingerprint TEXT NOT NULL CHECK (
    length(configure_fingerprint) = 64 AND
    configure_fingerprint NOT GLOB '*[^0-9a-f]*'
  ),
  build_directory TEXT NOT NULL,
  cmake_identity TEXT NOT NULL CHECK (
    length(cmake_identity) = 64 AND
    cmake_identity NOT GLOB '*[^0-9a-f]*'
  ),
  file_api_identity TEXT NOT NULL CHECK (
    length(file_api_identity) = 64 AND
    file_api_identity NOT GLOB '*[^0-9a-f]*'
  ),
  configured_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, project_id, profile_id)
);
