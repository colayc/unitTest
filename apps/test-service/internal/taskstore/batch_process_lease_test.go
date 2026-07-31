package taskstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestMigration008PreservesLegacyLeaseAndAddsBatchGroups(
	t *testing.T,
) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 8 || migrations[7].version != 8 {
		t.Fatalf("migrations = %#v", migrations)
	}
	db := openConfiguredDatabase(
		t,
		filepath.Join(t.TempDir(), "migration.sqlite"),
	)
	t.Cleanup(func() { _ = db.Close() })
	store := &Store{db: db, newID: task.NewID}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:7] {
		if err := store.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	created := createTask(t, store, newTask(180, 181, now))
	running := mustTransition(
		t,
		created,
		task.Transition{
			From: task.StatusQueued, To: task.StatusRunning,
			At: now.Add(time.Second),
		},
	)
	if _, _, err := store.Apply(
		ctx,
		task.Mutation{
			Task: running, Expected: task.StatusQueued,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO process_leases(
		task_id, host_pid, host_start_identity,
		target_process_group, service_instance_id
	) VALUES(?,?,?,?,?)`,
		created.ID,
		200,
		"legacy-host",
		201,
		id(182),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigration(
		ctx,
		migrations[7],
	); err != nil {
		t.Fatal(err)
	}
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 ||
		leases[0].TargetProcessGroup != 201 ||
		len(leases[0].TargetProcessGroups) != 0 {
		t.Fatalf("legacy leases = %#v, %v", leases, err)
	}
	lease := leases[0]
	lease.TargetProcessGroup = 0
	lease.TargetProcessGroups = []int{301, 302}
	if err := store.UpdateLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	leases, err = store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 ||
		len(leases[0].TargetProcessGroups) != 2 ||
		leases[0].TargetProcessGroups[0] != 301 ||
		leases[0].TargetProcessGroups[1] != 302 {
		t.Fatalf("batch leases = %#v, %v", leases, err)
	}
}
