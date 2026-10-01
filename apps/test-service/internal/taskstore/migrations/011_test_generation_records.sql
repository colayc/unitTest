ALTER TABLE test_generation_runs ADD COLUMN record_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(record_json) AND length(record_json) <= 65536);
ALTER TABLE test_generation_runs ADD COLUMN record_sha256 TEXT NOT NULL DEFAULT '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a' CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*');
