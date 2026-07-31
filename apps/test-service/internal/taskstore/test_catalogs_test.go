package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestMigration005UpgradeAndFailureRollback(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 5 || migrations[4].version != 5 {
		t.Fatalf("loaded migrations = %#v", migrations)
	}
	db := openConfiguredDatabase(t, filepath.Join(t.TempDir(), "migration.sqlite"))
	t.Cleanup(func() { _ = db.Close() })
	store := &Store{db: db, newID: task.NewID}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:4] {
		if err := store.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}

	broken := migrations[4]
	broken.sql += "\nINSERT INTO missing_catalog_migration_table(value) VALUES(1);"
	if err := store.applyMigration(ctx, broken); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("broken migration 005 error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("migration count after rollback = %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('test_catalogs','current_test_catalogs','test_catalog_entries')`).
		Scan(&count); err != nil || count != 0 {
		t.Fatalf("Catalog tables after rollback = %d, %v", count, err)
	}

	if err := store.applyMigration(ctx, migrations[4]); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("migration count after upgrade = %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('test_catalogs','current_test_catalogs','test_catalog_entries')`).
		Scan(&count); err != nil || count != 3 {
		t.Fatalf("Catalog tables after upgrade = %d, %v", count, err)
	}
}

func TestCatalogPublicationIsAtomicCurrentAndRestartSafe(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(root, "history.sqlite")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactstore.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })

	oldCatalog := catalogFixture(t, "1", "old")
	oldTask := createTask(t, store, newTask(40, 41, oldCatalog.GeneratedAt))
	oldArtifact, err := artifacts.CommitTestCatalog(ctx, oldTask.ID, id(42), oldCatalog.GeneratedAt, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishCatalog(ctx, oldCatalog, oldArtifact); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogFixture(t, "2", "new-a", "new-b")
	orphanTaskID := id(43)
	orphanArtifact, err := artifacts.CommitTestCatalog(ctx, orphanTaskID, id(44), newCatalog.GeneratedAt, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishCatalog(ctx, newCatalog, orphanArtifact); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("PublishCatalog(orphan artifact) error = %v", err)
	}
	got, err := store.GetCatalog(ctx, oldCatalog.ProjectID, oldCatalog.ProfileID)
	if err != nil || got.Revision != oldCatalog.Revision {
		t.Fatalf("current Catalog after failed transaction = %#v, %v", got, err)
	}

	newTask := createTask(t, store, newTask(45, 46, newCatalog.GeneratedAt))
	newArtifact, err := artifacts.CommitTestCatalog(ctx, newTask.ID, id(47), newCatalog.GeneratedAt, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishCatalog(ctx, newCatalog, newArtifact); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetCatalog(ctx, newCatalog.ProjectID, newCatalog.ProfileID)
	if err != nil || !reflect.DeepEqual(got, newCatalog) {
		t.Fatalf("current Catalog = %#v, %v; want %#v", got, err, newCatalog)
	}
	var currentCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM current_test_catalogs WHERE project_id=? AND profile_id=?`,
		newCatalog.ProjectID, newCatalog.ProfileID).Scan(&currentCount); err != nil || currentCount != 1 {
		t.Fatalf("current Catalog row count = %d, %v", currentCount, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	restarted, err := store.GetCatalog(ctx, newCatalog.ProjectID, newCatalog.ProfileID)
	if err != nil || !reflect.DeepEqual(restarted, newCatalog) {
		t.Fatalf("restarted Catalog = %#v, %v", restarted, err)
	}
}

func TestCatalogPaginationBindsCursorToCurrentRevision(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	root := t.TempDir()
	artifacts, err := artifactstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	first := catalogFixture(t, "3", "one", "two")
	publishCatalogFixture(t, store, artifacts, first, 50)

	page, err := store.PageCatalog(ctx, testdomain.CatalogPageRequest{
		ProjectID: first.ProjectID, ProfileID: first.ProfileID, Limit: 1,
	})
	if err != nil || len(page.Containers) != 1 || len(page.Items) != 0 || page.NextCursor == "" {
		t.Fatalf("first Catalog page = %#v, %v", page, err)
	}
	cursor := page.NextCursor
	page, err = store.PageCatalog(ctx, testdomain.CatalogPageRequest{
		ProjectID: first.ProjectID, ProfileID: first.ProfileID, Cursor: cursor, Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].LogicalName != "one" {
		t.Fatalf("second Catalog page = %#v, %v", page, err)
	}

	replacement := catalogFixture(t, "4", "replacement")
	publishCatalogFixture(t, store, artifacts, replacement, 53)
	if _, err := store.PageCatalog(ctx, testdomain.CatalogPageRequest{
		ProjectID: first.ProjectID, ProfileID: first.ProfileID, Cursor: cursor, Limit: 1,
	}); !errors.Is(err, testdomain.ErrCatalogStale) {
		t.Fatalf("stale Catalog cursor error = %v", err)
	}
}

func TestCatalogPublicationRejectsDomainLimitBeforeWriting(t *testing.T) {
	store := openTestStore(t)
	value := catalogFixture(t, "5", "case")
	value.Items = make([]testdomain.Item, 100_001)
	if err := store.PublishCatalog(context.Background(), value, task.Artifact{}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("oversized Catalog error = %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_catalogs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("oversized Catalog wrote %d rows, error=%v", count, err)
	}
}

func TestConcurrentCatalogReadersObserveOnlyCompleteSnapshots(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	artifacts, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	oldCatalog := catalogFixture(t, "6", "old")
	newCatalog := catalogFixture(t, "7", "new-a", "new-b", "new-c")
	publishCatalogFixture(t, store, artifacts, oldCatalog, 60)

	var wait sync.WaitGroup
	failures := make(chan string, 1)
	done := make(chan struct{})
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			value, err := store.GetCatalog(ctx, oldCatalog.ProjectID, oldCatalog.ProfileID)
			if err != nil {
				select {
				case failures <- err.Error():
				default:
				}
				return
			}
			validOld := value.Revision == oldCatalog.Revision && len(value.Items) == len(oldCatalog.Items)
			validNew := value.Revision == newCatalog.Revision && len(value.Items) == len(newCatalog.Items)
			if !validOld && !validNew {
				select {
				case failures <- "reader observed a hybrid Catalog":
				default:
				}
				return
			}
		}
	}()
	publishCatalogFixture(t, store, artifacts, newCatalog, 63)
	close(done)
	wait.Wait()
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
}

func publishCatalogFixture(
	t *testing.T,
	store *Store,
	artifacts *artifactstore.Store,
	catalog testdomain.Catalog,
	seed byte,
) {
	t.Helper()
	input := createTask(t, store, newTask(seed, seed+1, catalog.GeneratedAt))
	artifact, err := artifacts.CommitTestCatalog(context.Background(), input.ID, id(seed+2), catalog.GeneratedAt, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishCatalog(context.Background(), catalog, artifact); err != nil {
		t.Fatal(err)
	}
}

func catalogFixture(t *testing.T, revisionDigit string, names ...string) testdomain.Catalog {
	t.Helper()
	containerID, err := testdomain.ContainerID("core", "core.tests")
	if err != nil {
		t.Fatal(err)
	}
	items := make([]testdomain.Item, len(names))
	for index, name := range names {
		itemID, err := testdomain.CaseID(testdomain.CaseIdentity{
			ProjectID: "core", CTestName: "core.tests",
			Framework: testdomain.FrameworkCppUTest, Group: "Math", Name: name,
		})
		if err != nil {
			t.Fatal(err)
		}
		items[index] = testdomain.Item{
			ID: itemID, ContainerID: containerID, Kind: testdomain.ItemCase,
			Framework: testdomain.FrameworkCppUTest, LogicalName: name, DisplayName: name,
			Labels: []string{},
		}
	}
	value, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: strings.Repeat("a", 64),
		Revision:    strings.Repeat(revisionDigit, 64),
		GeneratedAt: time.Date(2026, 7, 31, int(revisionDigit[0]-'0'), 0, 0, 0, time.UTC),
		Containers: []testdomain.Container{{
			ID: containerID, ProjectID: "core", CTestLogicalName: "core.tests",
			DisplayName: "Core Tests", Framework: testdomain.FrameworkCppUTest,
			Labels: []string{},
		}},
		Items: items, Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
