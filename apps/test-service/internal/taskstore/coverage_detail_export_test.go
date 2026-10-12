package taskstore

import (
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
)

// OpenCoverageDetailTestStore shares the SQLite fixture with external IPC tests.
func OpenCoverageDetailTestStore(t *testing.T) *Store {
	t.Helper()
	return openTestStore(t)
}

// AttestedCoverageDetailTestFixture reuses the real durable coverage lifecycle.
func AttestedCoverageDetailTestFixture(t *testing.T, store *Store, number int) coveragedetail.Index {
	t.Helper()
	return attestedDetailFixture(t, store, number)
}
