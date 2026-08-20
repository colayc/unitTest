package artifactstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	coverageJSON = []byte("{\"schemaVersion\":\"1.0\"}\n")
	junitXML     = []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><testsuites></testsuites>\n")
	coverageHTML = []byte("<!doctype html><meta charset=\"utf-8\"><title>Coverage</title>\n")
)

func TestCoverageArtifactSinkPublishesClosedReportSet(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	taskID := id(40)
	raw, err := store.OpenTask(context.Background(), taskID, task.KindCoverageRun)
	if err != nil {
		t.Fatal(err)
	}
	sink, ok := raw.(task.CoverageArtifactSink)
	if !ok {
		t.Fatal("coverage task sink does not implement CoverageArtifactSink")
	}
	inputs := []struct {
		id, kind, mime, extension string
		body                      []byte
	}{
		{id(41), "coverage-json", "application/json", ".coverage.json", coverageJSON},
		{id(42), "junit-xml", "application/xml", ".junit.xml", junitXML},
		{id(43), "coverage-html", "text/html", ".coverage.html", coverageHTML},
	}
	for _, input := range inputs {
		if err := sink.CommitBlob(context.Background(), input.id, input.kind, input.body); err != nil {
			t.Fatalf("CommitBlob(%q) error = %v", input.kind, err)
		}
	}
	finishedAt := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)
	artifacts, err := sink.Finalize(context.Background(), finishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("Finalize() artifacts = %#v, want the three public report artifacts", artifacts)
	}
	byKind := make(map[string]task.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		if _, duplicate := byKind[artifact.Kind]; duplicate {
			t.Fatalf("duplicate artifact kind %q", artifact.Kind)
		}
		byKind[artifact.Kind] = artifact
	}
	for _, input := range inputs {
		artifact, exists := byKind[input.kind]
		wantSHA := sha256.Sum256(input.body)
		if !exists || artifact.ID != input.id || artifact.TaskID != taskID ||
			artifact.MIMEType != input.mime || !strings.HasSuffix(artifact.RelativePath, input.extension) ||
			artifact.SHA256 != fmt.Sprintf("%x", wantSHA) || strings.ToLower(artifact.SHA256) != artifact.SHA256 {
			t.Fatalf("%s artifact = %#v", input.kind, artifact)
		}
		assertArtifactContent(t, root, artifact, string(input.body))
	}
}

func TestCoverageArtifactSinkNeverPersistsRawProcessOutputOrDiagnostics(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	taskID := id(44)
	sink, err := store.OpenTask(context.Background(), taskID, task.KindCoverageRun)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := `C:\private\build\secret.profraw --token=coverage-secret`
	for _, phase := range []string{
		"coverage-configure", "coverage-build", "coverage-test", "coverage-normalize",
	} {
		if err := sink.AppendOutput(context.Background(), phase, "stdout", []byte(sentinel)); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.AppendDiagnostic(context.Background(), diagnostic.Diagnostic{
		TaskID: taskID, StepID: "coverage-build", Severity: "error",
		Code: "COVERAGE_BUILD_FAILED", Message: sentinel,
	}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := sink.Finalize(context.Background(), time.Date(2026, 8, 20, 4, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("raw coverage artifacts = %#v, want none", artifacts)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "**", "*")); err != nil || len(matches) != 0 {
		t.Fatalf("coverage raw persistence left files = %v, %v", matches, err)
	}
}

func TestCoverageArtifactSinkRejectsInvalidBlobs(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seed := 50
	open := func(t *testing.T, kind task.Kind) task.CoverageArtifactSink {
		t.Helper()
		seed++
		raw, err := store.OpenTask(context.Background(), id(byte(seed)), kind)
		if err != nil {
			t.Fatal(err)
		}
		sink, ok := raw.(task.CoverageArtifactSink)
		if !ok {
			t.Fatal("task sink does not implement CoverageArtifactSink")
		}
		return sink
	}
	t.Run("empty", func(t *testing.T) {
		if err := open(t, task.KindCoverageRun).CommitBlob(context.Background(), id(61), "coverage-json", nil); err != ErrInvalidArtifact {
			t.Fatalf("CommitBlob() error = %v", err)
		}
	})
	t.Run("over limit", func(t *testing.T) {
		if err := open(t, task.KindCoverageRun).CommitBlob(context.Background(), id(62), "coverage-json", make([]byte, maxCoverageArtifactBytes+1)); err != ErrInvalidArtifact {
			t.Fatalf("CommitBlob() error = %v", err)
		}
	})
	t.Run("duplicate kind and ID", func(t *testing.T) {
		sink := open(t, task.KindCoverageRun)
		if err := sink.CommitBlob(context.Background(), id(63), "coverage-json", coverageJSON); err != nil {
			t.Fatal(err)
		}
		if err := sink.CommitBlob(context.Background(), id(64), "coverage-json", coverageJSON); err != ErrInvalidArtifact {
			t.Fatalf("duplicate kind error = %v", err)
		}
		if err := sink.CommitBlob(context.Background(), id(63), "junit-xml", junitXML); err != ErrInvalidArtifact {
			t.Fatalf("duplicate ID error = %v", err)
		}
	})
	t.Run("non coverage task", func(t *testing.T) {
		if err := open(t, task.KindTestRun).CommitBlob(context.Background(), id(65), "coverage-json", coverageJSON); err != ErrInvalidArtifact {
			t.Fatalf("CommitBlob() error = %v", err)
		}
	})
	for _, kind := range []string{"raw.profraw", "indexed.profdata"} {
		t.Run(kind, func(t *testing.T) {
			if err := open(t, task.KindCoverageRun).CommitBlob(context.Background(), id(66), kind, []byte("raw profile")); err != ErrInvalidArtifact {
				t.Fatalf("CommitBlob(%q) error = %v", kind, err)
			}
		})
	}
	for _, kind := range []string{"junit-xml", "coverage-html"} {
		t.Run("CommitJSON "+kind, func(t *testing.T) {
			if err := open(t, task.KindCoverageRun).CommitJSON(context.Background(), id(67), kind, map[string]any{"data": "report"}); err != ErrInvalidArtifact {
				t.Fatalf("CommitJSON(%q) error = %v", kind, err)
			}
		})
	}
}

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
	containerID := testdomain.ID(
		"utid-v1-" + strings.Repeat("1", 64),
	)
	itemID := testdomain.ID(
		"utid-v1-" + strings.Repeat("2", 64),
	)
	if err := sink.CommitJSON(
		context.Background(),
		id(12),
		"test-selection",
		testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{itemID},
		},
	); err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(testdomain.TestItemResult{
		ItemID:         itemID,
		ContainerID:    containerID,
		Iteration:      1,
		Outcome:        testdomain.ItemPassed,
		FailureDetails: []testdomain.FailureDetail{},
		OutputRefs:     []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSONLines(
		context.Background(),
		id(13),
		"test-results",
		[]json.RawMessage{resultJSON},
	); err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSON(
		context.Background(),
		id(14),
		"test-run-summary",
		map[string]any{
			"runId":   id(15),
			"taskId":  taskID,
			"outcome": "passed",
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
			"test-results",
			"test-run-summary",
			"test-selection",
		},
	) {
		t.Fatalf("test task artifact kinds = %#v", kinds)
	}
}

func TestArtifactSinkRejectsServiceTokenMarkerInTestResults(
	t *testing.T,
) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sink, err := store.OpenTask(
		context.Background(),
		id(20),
		task.KindTestRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(testdomain.TestItemResult{
		ItemID:      testdomain.ID("utid-v1-" + strings.Repeat("1", 64)),
		ContainerID: testdomain.ID("utid-v1-" + strings.Repeat("2", 64)),
		Iteration:   1,
		Outcome:     testdomain.ItemErrored,
		FailureDetails: []testdomain.FailureDetail{{
			Category:     "framework_output_invalid",
			Message:      "UNIT_TEST_SERVICE_TOKEN must stay private",
			Locations:    []testdomain.SourceLocation{},
			EvidenceRefs: []string{},
		}},
		OutputRefs: []string{},
		Partial:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.CommitJSONLines(
		context.Background(),
		id(21),
		"test-results",
		[]json.RawMessage{encoded},
	); err != ErrInvalidArtifact {
		t.Fatalf("CommitJSONLines() error = %v", err)
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
