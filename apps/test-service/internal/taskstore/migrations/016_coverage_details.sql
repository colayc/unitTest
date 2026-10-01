-- Detailed coverage is an optional v1.6 extension. No v1 report row is rebuilt.
CREATE TABLE coverage_detail_reports (
  report_id TEXT PRIMARY KEY REFERENCES coverage_reports(report_id) ON DELETE CASCADE,
  workspace_generation TEXT NOT NULL CHECK(length(workspace_generation)=64 AND workspace_generation NOT GLOB '*[^0-9a-f]*'),
  project_id TEXT NOT NULL,
  coverage_run_id TEXT NOT NULL REFERENCES coverage_runs(coverage_run_id) ON DELETE CASCADE,
  toolchain_json TEXT NOT NULL CHECK(json_valid(toolchain_json)),
  project_summary_json TEXT NOT NULL CHECK(json_valid(project_summary_json)),
  project_delta_json TEXT NOT NULL CHECK(json_valid(project_delta_json)),
  project_status TEXT NOT NULL CHECK(project_status IN ('current','stale','incomplete')),
  cursor_key BLOB NOT NULL CHECK(length(cursor_key)=32),
  UNIQUE(report_id, workspace_generation)
);
CREATE TABLE coverage_detail_files (
  report_id TEXT NOT NULL REFERENCES coverage_detail_reports(report_id) ON DELETE CASCADE,
  file_id TEXT NOT NULL CHECK(length(file_id)=32 AND file_id NOT GLOB '*[^0-9a-f]*'),
  relative_path TEXT NOT NULL,
  source_sha256 TEXT NOT NULL CHECK(length(source_sha256)=64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'),
  summary_json TEXT NOT NULL CHECK(json_valid(summary_json)),
  delta_json TEXT NOT NULL CHECK(json_valid(delta_json)),
  status TEXT NOT NULL CHECK(status IN ('current','stale','incomplete')),
  PRIMARY KEY(report_id,file_id), UNIQUE(report_id,relative_path)
);
CREATE INDEX coverage_detail_files_path ON coverage_detail_files(report_id,relative_path,file_id);
CREATE TABLE coverage_detail_functions (
  report_id TEXT NOT NULL,
  file_id TEXT NOT NULL,
  function_id TEXT NOT NULL CHECK(length(function_id)=32 AND function_id NOT GLOB '*[^0-9a-f]*'),
  name TEXT NOT NULL, linkage_name TEXT NOT NULL, signature_digest TEXT NOT NULL,
  start_line INTEGER NOT NULL, start_column INTEGER NOT NULL, end_line INTEGER NOT NULL, end_column INTEGER NOT NULL,
  summary_json TEXT NOT NULL CHECK(json_valid(summary_json)),
  delta_json TEXT NOT NULL CHECK(json_valid(delta_json)),
  status TEXT NOT NULL CHECK(status IN ('current','stale','incomplete')),
  PRIMARY KEY(report_id,function_id),
  FOREIGN KEY(report_id,file_id) REFERENCES coverage_detail_files(report_id,file_id) ON DELETE CASCADE
);
CREATE INDEX coverage_detail_functions_name ON coverage_detail_functions(report_id,file_id,name,function_id);
CREATE TABLE coverage_detail_file_lines (
  report_id TEXT NOT NULL, file_id TEXT NOT NULL,
  line INTEGER NOT NULL CHECK(line BETWEEN 1 AND 9007199254740991),
  count INTEGER NOT NULL CHECK(count BETWEEN 0 AND 9007199254740991),
  PRIMARY KEY(report_id,file_id,line),
  FOREIGN KEY(report_id,file_id) REFERENCES coverage_detail_files(report_id,file_id) ON DELETE CASCADE
);
CREATE TABLE coverage_detail_function_lines (
  report_id TEXT NOT NULL, function_id TEXT NOT NULL,
  line INTEGER NOT NULL CHECK(line BETWEEN 1 AND 9007199254740991),
  count INTEGER NOT NULL CHECK(count BETWEEN 0 AND 9007199254740991),
  PRIMARY KEY(report_id,function_id,line),
  FOREIGN KEY(report_id,function_id) REFERENCES coverage_detail_functions(report_id,function_id) ON DELETE CASCADE
);
CREATE TABLE coverage_detail_branches (
  report_id TEXT NOT NULL, function_id TEXT NOT NULL,
  line INTEGER NOT NULL CHECK(line BETWEEN 1 AND 9007199254740991),
  column INTEGER NOT NULL CHECK(column BETWEEN 0 AND 9007199254740991),
  ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 9007199254740991),
  count INTEGER NOT NULL CHECK(count BETWEEN 0 AND 9007199254740991),
  PRIMARY KEY(report_id,function_id,line,column,ordinal),
  FOREIGN KEY(report_id,function_id) REFERENCES coverage_detail_functions(report_id,function_id) ON DELETE CASCADE
);
CREATE TABLE coverage_detail_gaps (
  report_id TEXT NOT NULL REFERENCES coverage_detail_reports(report_id) ON DELETE CASCADE,
  gap_id TEXT NOT NULL CHECK(length(gap_id)=32 AND gap_id NOT GLOB '*[^0-9a-f]*'),
  file_id TEXT NOT NULL, function_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('line','branch')),
  line INTEGER NOT NULL CHECK(line BETWEEN 1 AND 9007199254740991),
  column INTEGER NOT NULL CHECK(column BETWEEN 0 AND 9007199254740991),
  ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 9007199254740991),
  PRIMARY KEY(report_id,gap_id),
  FOREIGN KEY(report_id,file_id) REFERENCES coverage_detail_files(report_id,file_id) ON DELETE CASCADE,
  FOREIGN KEY(report_id,function_id) REFERENCES coverage_detail_functions(report_id,function_id) ON DELETE CASCADE
);
CREATE TABLE coverage_detail_reasons (
  report_id TEXT NOT NULL REFERENCES coverage_detail_reports(report_id) ON DELETE CASCADE,
  owner_kind TEXT NOT NULL CHECK(owner_kind IN ('project','file','function')),
  owner_id TEXT NOT NULL,
  file_id TEXT,
  function_id TEXT,
  reason TEXT NOT NULL,
  PRIMARY KEY(report_id,owner_kind,owner_id,reason),
  CHECK ((owner_kind='project' AND owner_id=report_id AND file_id IS NULL AND function_id IS NULL) OR
    (owner_kind='file' AND owner_id=file_id AND file_id IS NOT NULL AND function_id IS NULL) OR
    (owner_kind='function' AND owner_id=function_id AND function_id IS NOT NULL AND file_id IS NULL)),
  FOREIGN KEY(report_id,file_id) REFERENCES coverage_detail_files(report_id,file_id) ON DELETE CASCADE,
  FOREIGN KEY(report_id,function_id) REFERENCES coverage_detail_functions(report_id,function_id) ON DELETE CASCADE
);
