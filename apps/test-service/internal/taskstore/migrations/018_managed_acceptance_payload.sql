-- Preserve the canonical accepted payload so record fields can be rechecked
-- against the verified acceptance rather than only validating their shape.
-- Legacy 017 rows remain NULL and cannot be advertised until re-accepted.
ALTER TABLE managed_test_commits ADD COLUMN acceptance_json TEXT CHECK(acceptance_json IS NULL OR json_valid(acceptance_json));
ALTER TABLE managed_test_commits ADD COLUMN acceptance_mac TEXT CHECK(acceptance_mac IS NULL OR (length(acceptance_mac)=64 AND acceptance_mac NOT GLOB '*[^0-9a-f]*'));
