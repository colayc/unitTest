-- A legacy 017 acceptance cannot be rebound from its digest alone. Recovery
-- requires explicit fresh evidence, pinned to the old record revision.
ALTER TABLE managed_test_pending_acceptances ADD COLUMN legacy_recovery_revision INTEGER CHECK(legacy_recovery_revision IS NULL OR legacy_recovery_revision >= 1);
ALTER TABLE managed_test_pending_acceptances ADD COLUMN legacy_recovery_mac TEXT CHECK(legacy_recovery_mac IS NULL OR (length(legacy_recovery_mac)=64 AND legacy_recovery_mac NOT GLOB '*[^0-9a-f]*'));
