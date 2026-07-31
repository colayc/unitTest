package artifactstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestStoreCommitsAndReadsVerifiedTestCatalog(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	catalog := artifactCatalogFixture(t)
	artifact, err := store.CommitTestCatalog(
		context.Background(), strings.Repeat("1", 32), strings.Repeat("2", 32),
		catalog.GeneratedAt, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "test-catalog" || artifact.MIMEType != "application/json" {
		t.Fatalf("Catalog artifact = %#v", artifact)
	}
	got, err := store.ReadTestCatalog(context.Background(), artifact)
	if err != nil || !reflect.DeepEqual(got, catalog) {
		t.Fatalf("ReadTestCatalog() = %#v, %v", got, err)
	}
}

func TestStoreCatalogCommitFailurePublishesNoReadableArtifact(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.hooks.finalizeDirectory = func(stage directoryFinalizeStage) error {
		if stage == directoryFinalizePublished {
			return errors.New("injected directory sync failure")
		}
		return nil
	}
	catalog := artifactCatalogFixture(t)
	artifact, err := store.CommitTestCatalog(
		context.Background(), strings.Repeat("3", 32), strings.Repeat("4", 32),
		catalog.GeneratedAt, catalog,
	)
	if !errors.Is(err, ErrStoreUnavailable) || artifact.ID != "" {
		t.Fatalf("CommitTestCatalog() = %#v, %v", artifact, err)
	}
}

func artifactCatalogFixture(t *testing.T) testdomain.Catalog {
	t.Helper()
	containerID, err := testdomain.ContainerID("core", "core.tests")
	if err != nil {
		t.Fatal(err)
	}
	caseID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "core", CTestName: "core.tests",
		Framework: testdomain.FrameworkCppUTest, Group: "Math", Name: "adds",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: strings.Repeat("5", 64), Revision: strings.Repeat("6", 64),
		GeneratedAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		Containers: []testdomain.Container{{
			ID: containerID, ProjectID: "core", CTestLogicalName: "core.tests",
			DisplayName: "Core Tests", Framework: testdomain.FrameworkCppUTest, Labels: []string{},
		}},
		Items: []testdomain.Item{{
			ID: caseID, ContainerID: containerID, Kind: testdomain.ItemCase,
			Framework: testdomain.FrameworkCppUTest, LogicalName: "adds", DisplayName: "adds",
			Labels: []string{},
		}},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
