ALTER TABLE process_leases
ADD COLUMN target_process_groups_json TEXT NOT NULL DEFAULT '[]'
CHECK (
  json_valid(target_process_groups_json) AND
  json_type(target_process_groups_json) = 'array'
);
