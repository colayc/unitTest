package artifactstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
)

func TestArtifactSinkFinalizesDeterministicCMakeArtifactSet(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	synced := 0
	publishedTemporary := make([]string, 0)
	store.hooks.afterTempSync = func() { synced++ }
	store.hooks.beforePublish = func(temporary string) {
		publishedTemporary = append(publishedTemporary, temporary)
	}

	sink, err := store.OpenTask(context.Background(), id(1), task.KindCMakeBuild)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.AppendOutput(context.Background(), "configure", "stdout", []byte("configure output\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.AppendOutput(context.Background(), "build", "stderr", []byte("build warning\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.AppendDiagnostic(context.Background(), diagnostic.Diagnostic{
		ID: id(9), TaskID: id(1), StepID: "build", Source: "compiler",
		Severity: "warning", Code: "W100", Message: "deterministic warning",
		FileURI: "workspace:///src/main.cpp",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSON(context.Background(), id(2), "execution-plan", map[string]any{
		"version": 1,
		"steps": []map[string]any{{
			"id": "build", "kind": "build",
			"command": map[string]any{"executable": "cmake", "args": []string{"--build", "<build>"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSON(context.Background(), id(3), "build-summary", map[string]any{
		"taskId": id(1), "kind": "cmake_build", "outcome": "succeeded",
		"finishedAt": time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		"steps": []map[string]any{{
			"id": "build", "kind": "build", "status": "succeeded",
			"finishedAt": time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	finishedAt := time.Date(2026, 7, 29, 12, 0, 1, 0, time.UTC)
	artifacts, err := sink.Finalize(context.Background(), finishedAt)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, len(artifacts))
	byKind := make(map[string]task.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		kinds[index] = artifact.Kind
		byKind[artifact.Kind] = artifact
		if artifact.TaskID != id(1) || !artifact.CreatedAt.Equal(finishedAt) ||
			artifact.RelativePath == "" || artifact.Size < 0 ||
			len(artifact.SHA256) != 64 {
			t.Fatalf("artifact = %#v", artifact)
		}
	}
	sort.Strings(kinds)
	if want := []string{"build-summary", "diagnostics", "execution-plan", "stderr", "stdout"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("artifact kinds = %v, want %v", kinds, want)
	}
	if synced != len(artifacts) || len(publishedTemporary) != len(artifacts) {
		t.Fatalf("atomic publication hooks = synced %d, published %v", synced, publishedTemporary)
	}
	for _, temporary := range publishedTemporary {
		if !temporaryArtifactName(temporary) {
			t.Fatalf("published source %q is not a temporary artifact", temporary)
		}
	}

	assertArtifactContent(t, root, byKind["stdout"], "configure output\n")
	assertArtifactContent(t, root, byKind["stderr"], "build warning\n")
	diagnostics := artifactContent(t, root, byKind["diagnostics"])
	var decoded diagnostic.Diagnostic
	if lines := strings.Split(strings.TrimSuffix(diagnostics, "\n"), "\n"); len(lines) != 1 ||
		json.Unmarshal([]byte(lines[0]), &decoded) != nil ||
		decoded.Code != "W100" || decoded.FileURI != "workspace:///src/main.cpp" {
		t.Fatalf("diagnostics artifact = %q, decoded = %#v", diagnostics, decoded)
	}
}

func TestArtifactSinkAbortLeavesNoPublishedArtifacts(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sink, err := store.OpenTask(context.Background(), id(4), task.KindCMakeBuild)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.AppendOutput(context.Background(), "build", "stdout", []byte("private")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Finalize(context.Background(), time.Now().UTC()); err == nil {
		t.Fatal("Finalize() after Abort succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("artifact root after Abort = %#v", entries)
	}
}

func TestArtifactSinkSupportsTestRunExecutionArtifacts(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	taskID := id(9)
	sink, err := store.OpenTask(
		context.Background(),
		taskID,
		task.KindTestRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.AppendOutput(
		context.Background(),
		"test-000001",
		"stdout",
		[]byte("test output\n"),
	); err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSON(
		context.Background(),
		id(10),
		"execution-plan",
		map[string]any{
			"version": 1,
			"steps": []map[string]any{{
				"id":   "build",
				"kind": "build",
				"command": map[string]any{
					"executable": "cmake",
					"args":       []string{"--build"},
				},
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Date(
		2026,
		7,
		31,
		9,
		0,
		0,
		0,
		time.UTC,
	)
	if err := sink.CommitJSON(
		context.Background(),
		id(11),
		"task-summary",
		task.TaskSummary{
			TaskID:     taskID,
			Kind:       task.KindTestRun,
			Outcome:    task.OutcomeSucceeded,
			FinishedAt: finishedAt,
			Steps: []task.StepSnapshot{{
				ID: "test-000001", Kind: task.StepTestRun,
				Status: task.StepSucceeded, FinishedAt: &finishedAt,
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	artifacts, err := sink.Finalize(
		context.Background(),
		finishedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, len(artifacts))
	for index, artifact := range artifacts {
		kinds[index] = artifact.Kind
	}
	if !reflect.DeepEqual(
		kinds,
		[]string{
			"diagnostics",
			"execution-plan",
			"stderr",
			"stdout",
			"task-summary",
		},
	) {
		t.Fatalf("test task artifact kinds = %#v", kinds)
	}
}

func assertArtifactContent(t *testing.T, root string, artifact task.Artifact, want string) {
	t.Helper()
	if got := artifactContent(t, root, artifact); got != want {
		t.Fatalf("%s artifact = %q, want %q", artifact.Kind, got, want)
	}
}

func artifactContent(t *testing.T, root string, artifact task.Artifact) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
