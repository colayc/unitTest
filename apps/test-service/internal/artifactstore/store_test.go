package artifactstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestCommitJSONAndReadChunk(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Unix(0, 0).UTC()
	artifact, err := store.CommitJSON(context.Background(), id(1), id(2), createdAt, map[string]string{"outcome": "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "task-summary" || artifact.MIMEType != "application/json" || len(artifact.SHA256) != 64 {
		t.Fatalf("artifact = %#v", artifact)
	}
	if artifact.ID != id(2) || artifact.TaskID != id(1) || artifact.RelativePath != "tasks/"+id(1)+"/"+id(2)+".json" || !artifact.CreatedAt.Equal(createdAt) {
		t.Fatalf("artifact identity = %#v", artifact)
	}

	first, next, eof, err := store.ReadChunk(context.Background(), artifact, 0, 8)
	if err != nil || len(first) != 8 || next != 8 || eof {
		t.Fatalf("chunk = %q, next = %d, eof = %v, err = %v", first, next, eof, err)
	}
	rest, next, eof, err := store.ReadChunk(context.Background(), artifact, next, MaxReadChunk)
	if err != nil || !eof || next != artifact.Size {
		t.Fatalf("rest = %q, next = %d, eof = %v, err = %v", rest, next, eof, err)
	}
	if got := string(append(first, rest...)); got != "{\"outcome\":\"cancelled\"}\n" {
		t.Fatalf("stored JSON = %q", got)
	}

	entries, err := os.ReadDir(filepath.Dir(artifactPath(root, artifact)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != id(2)+".json" {
		t.Fatalf("commit left non-final files: %#v", entries)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(artifactPath(root, artifact))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact mode = %o", info.Mode().Perm())
		}
	}
}

func TestPinnedDirectoryHandleSupportsDurableSync(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific directory FlushFileBuffers probe")
	}
	store, err := newStore(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := syncRootDirectory(store.root); err != nil {
		t.Fatalf("pinned parent durable sync failed: %T %v", err, err)
	}
}

func TestCommitJSONCanonicalizesFixedSummaryFields(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]any{
		"outcome":    "succeeded",
		"finishedAt": "1970-01-01T00:00:00Z",
		"scenario":   "success",
		"taskId":     id(1),
	}
	artifact, err := store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), full)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"taskId":"` + id(1) + `","scenario":"success","outcome":"succeeded","finishedAt":"1970-01-01T00:00:00Z"}` + "\n")
	got, err := os.ReadFile(artifactPath(root, artifact))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical summary = %q, want %q", got, want)
	}

	type reorderedSummary struct {
		FinishedAt string `json:"finishedAt"`
		Outcome    string `json:"outcome"`
		TaskID     string `json:"taskId"`
		Scenario   string `json:"scenario"`
	}
	artifact, err = store.CommitJSON(context.Background(), id(1), id(3), time.Unix(0, 0).UTC(), reorderedSummary{
		FinishedAt: "1970-01-01T00:00:00Z", Outcome: "succeeded", TaskID: id(1), Scenario: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(artifactPath(root, artifact))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("struct summary = %q, want %q", got, want)
	}
}

func TestCommitJSONRejectsNonSummaryOrSensitiveValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "unknown field", value: map[string]any{"outcome": "cancelled", "detail": "x"}},
		{name: "environment field", value: map[string]any{"outcome": "cancelled", "environment": "production"}},
		{name: "environment shaped value", value: map[string]any{"outcome": "cancelled", "scenario": "${SCENARIO}"}},
		{name: "absolute Unix path", value: map[string]any{"outcome": "cancelled", "finishedAt": "/tmp/result"}},
		{name: "absolute Windows path", value: map[string]any{"outcome": "cancelled", "finishedAt": `C:\result.json`}},
		{name: "nested value", value: map[string]any{"outcome": "cancelled", "scenario": map[string]string{"name": "success"}}},
		{name: "array value", value: map[string]any{"outcome": "cancelled", "scenario": []string{"success"}}},
		{name: "boolean value", value: map[string]any{"outcome": true}},
		{name: "null value", value: map[string]any{"outcome": nil}},
		{name: "float value", value: map[string]any{"outcome": 1.5}},
		{name: "missing outcome", value: map[string]any{"scenario": "success"}},
		{name: "mismatched task", value: map[string]any{"taskId": id(9), "scenario": "success", "outcome": "succeeded", "finishedAt": "1970-01-01T00:00:00Z"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := newStore(t, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), test.value)
			if !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("CommitJSON() error = %v", err)
			}
		})
	}
}

func TestCommitJSONRejectsNonGeneratedIDs(t *testing.T) {
	store, err := newStore(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		taskID     string
		artifactID string
	}{
		{name: "empty task", artifactID: id(2)},
		{name: "short task", taskID: "1", artifactID: id(2)},
		{name: "uppercase task", taskID: strings.ToUpper(id(10)), artifactID: id(2)},
		{name: "task traversal", taskID: "../" + id(1), artifactID: id(2)},
		{name: "empty artifact", taskID: id(1)},
		{name: "artifact traversal", taskID: id(1), artifactID: "../" + id(2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.CommitJSON(context.Background(), test.taskID, test.artifactID, time.Unix(0, 0).UTC(), struct{}{})
			if !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("CommitJSON() error = %v", err)
			}
		})
	}
}

func TestReadChunkValidatesRangeAndEOF(t *testing.T) {
	store, artifact, _ := committedArtifact(t)
	for _, test := range []struct {
		name   string
		offset int64
		length int
	}{
		{name: "negative offset", offset: -1, length: 1},
		{name: "zero length", offset: 0, length: 0},
		{name: "oversized length", offset: 0, length: MaxReadChunk + 1},
		{name: "past end", offset: artifact.Size + 1, length: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := store.ReadChunk(context.Background(), artifact, test.offset, test.length)
			if !errors.Is(err, ErrInvalidRange) {
				t.Fatalf("ReadChunk() error = %v", err)
			}
		})
	}

	chunk, next, eof, err := store.ReadChunk(context.Background(), artifact, artifact.Size, 1)
	if err != nil || len(chunk) != 0 || next != artifact.Size || !eof {
		t.Fatalf("EOF read = %q, %d, %v, %v", chunk, next, eof, err)
	}
}

func TestReadChunkRejectsForgedMetadataWithoutUsingItsPath(t *testing.T) {
	store, artifact, _ := committedArtifact(t)
	abs := artifact
	abs.RelativePath = filepath.Join(t.TempDir(), "secret.json")
	traversal := artifact
	traversal.RelativePath = "../secret.json"
	wrongTask := artifact
	wrongTask.TaskID = id(3)
	wrongID := artifact
	wrongID.ID = id(4)
	wrongKind := artifact
	wrongKind.Kind = "other"
	wrongMIME := artifact
	wrongMIME.MIMEType = "text/plain"
	badDigest := artifact
	badDigest.SHA256 = "not-a-digest"

	for _, forged := range []task.Artifact{abs, traversal, wrongTask, wrongID, wrongKind, wrongMIME, badDigest} {
		_, _, _, err := store.ReadChunk(context.Background(), forged, 0, 1)
		if !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("ReadChunk(%#v) error = %v", forged, err)
		}
	}
}

func TestReadChunkDetectsSizeAndDigestTampering(t *testing.T) {
	t.Run("size", func(t *testing.T) {
		store, artifact, root := committedArtifact(t)
		if err := os.WriteFile(artifactPath(root, artifact), []byte("short"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 1)
		if !errors.Is(err, ErrArtifactChanged) {
			t.Fatalf("ReadChunk() error = %v", err)
		}
	})

	t.Run("digest", func(t *testing.T) {
		root := t.TempDir()
		store, err := newStore(t, root)
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(artifactPath(root, artifact))
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)-2] ^= 1
		if err := os.WriteFile(artifactPath(root, artifact), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, _, err = store.ReadChunk(context.Background(), artifact, 0, 1)
		if !errors.Is(err, ErrArtifactChanged) {
			t.Fatalf("ReadChunk() error = %v", err)
		}
	})
}

func TestReadChunkNeverReturnsBytesMutatedAfterVerifiedSnapshot(t *testing.T) {
	store, artifact, root := committedArtifact(t)
	target := artifactPath(root, artifact)
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Repeat([]byte{'x'}, len(original))
	store.hooks.afterVerifiedSnapshot = func() {
		if err := os.WriteFile(target, mutated, 0o600); err != nil {
			t.Errorf("mutate artifact: %v", err)
		}
	}

	chunk, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 8)
	if err != nil && !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
	if err == nil && !bytes.Equal(chunk, original[:8]) {
		t.Fatalf("ReadChunk() returned unverified bytes %q, verified bytes were %q", chunk, original[:8])
	}
}

func TestReadChunkTreatsTruncationDuringSnapshotAsArtifactChanged(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{'a'}, MaxReadChunk*2)
	taskID, artifactID := id(1), id(2)
	relative := artifactRelativePath(taskID, artifactID)
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	artifact := task.Artifact{
		ID: artifactID, TaskID: taskID, Kind: "task-summary", RelativePath: relative,
		MIMEType: "application/json", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), CreatedAt: time.Unix(0, 0).UTC(),
	}
	store.hooks.afterSnapshotRead = func(position int64) {
		if position == MaxReadChunk {
			if err := os.Truncate(target, position); err != nil {
				t.Errorf("truncate artifact: %v", err)
			}
		}
	}

	_, _, _, err = store.ReadChunk(context.Background(), artifact, MaxReadChunk, 8)
	if !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
}

func TestCommitJSONChecksCancellationImmediatelyBeforePublication(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.hooks.afterTempSync = cancel
	_, err = store.CommitJSON(ctx, id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	target := filepath.Join(root, "tasks", id(1), id(2)+".json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled commit published final artifact: %v", err)
	}
	assertNoTemporaryArtifacts(t, root)
}

func TestCommitJSONRollsBackPublicationWhenFinalizationFails(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	var stages []directoryFinalizeStage
	store.hooks.finalizeDirectory = func(stage directoryFinalizeStage) error {
		stages = append(stages, stage)
		if stage == directoryFinalizePublished {
			return errors.New("injected finalization failure")
		}
		return nil
	}
	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	target := filepath.Join(root, "tasks", id(1), id(2)+".json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed commit left visible final artifact: %v", err)
	}
	if got, want := stages, []directoryFinalizeStage{directoryFinalizePublished, directoryFinalizeRollback}; !slices.Equal(got, want) {
		t.Fatalf("finalization stages = %v, want %v", got, want)
	}
	assertNoTemporaryArtifacts(t, root)
}

func TestCommitJSONFlushesPublicationAndTemporaryRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "tasks", id(1))
	target := filepath.Join(parent, id(2)+".json")
	var temporary string
	var stages []directoryFinalizeStage
	store.hooks.beforePublish = func(name string) { temporary = filepath.Join(parent, name) }
	store.hooks.finalizeDirectory = func(stage directoryFinalizeStage) error {
		stages = append(stages, stage)
		if _, err := os.Lstat(target); err != nil {
			t.Fatalf("final missing during %s: %v", stage, err)
		}
		_, temporaryErr := os.Lstat(temporary)
		switch stage {
		case directoryFinalizePublished:
			if temporaryErr != nil {
				t.Fatalf("temporary missing before publication flush: %v", temporaryErr)
			}
		case directoryFinalizeTemporaryRemoved:
			if !errors.Is(temporaryErr, os.ErrNotExist) {
				t.Fatalf("temporary remains before removal flush: %v", temporaryErr)
			}
		}
		return nil
	}

	if _, err := store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if got, want := stages, []directoryFinalizeStage{directoryFinalizePublished, directoryFinalizeTemporaryRemoved}; !slices.Equal(got, want) {
		t.Fatalf("finalization stages = %v, want %v", got, want)
	}
}

func TestCommitJSONRollsBackWhenTemporaryRemovalFinalizationFails(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	var stages []directoryFinalizeStage
	store.hooks.finalizeDirectory = func(stage directoryFinalizeStage) error {
		stages = append(stages, stage)
		if stage == directoryFinalizeTemporaryRemoved {
			return errors.New("injected temporary-removal finalization failure")
		}
		return nil
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	target := filepath.Join(root, "tasks", id(1), id(2)+".json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed commit left visible final artifact: %v", err)
	}
	if got, want := stages, []directoryFinalizeStage{directoryFinalizePublished, directoryFinalizeTemporaryRemoved, directoryFinalizeRollback}; !slices.Equal(got, want) {
		t.Fatalf("finalization stages = %v, want %v", got, want)
	}
}

func TestCommitJSONRejectsSubstitutedTemporaryFile(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "tasks", id(1))
	store.hooks.beforePublish = func(name string) {
		temporary := filepath.Join(parent, name)
		if err := os.Remove(temporary); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(temporary, []byte("attacker substitute"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	target := filepath.Join(parent, id(2)+".json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("substitute became visible final artifact: %v", err)
	}
}

func TestCommitJSONRejectsSubstitutedTemporaryLinkWithoutTouchingOutside(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "tasks", id(1))
	store.hooks.beforePublish = func(name string) {
		temporary := filepath.Join(parent, name)
		if err := os.Remove(temporary); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, temporary); err != nil {
			t.Skipf("file links are unavailable: %v", err)
		}
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if err == nil {
		t.Fatal("CommitJSON() accepted a substituted temporary link")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside must survive" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(parent, id(2)+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link became visible final artifact: %v", err)
	}
}

func TestCommitJSONRejectsSubstitutedTemporaryJunctionWithoutTouchingOutside(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows reparse-point coverage")
	}
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "outside.json")
	if err := os.WriteFile(outsideTarget, []byte("outside must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "tasks", id(1))
	store.hooks.beforePublish = func(name string) {
		temporary := filepath.Join(parent, name)
		if err := os.Remove(temporary); err != nil {
			t.Fatal(err)
		}
		if err := makeDirectoryLink(outside, temporary); err != nil {
			t.Skipf("directory junctions are unavailable: %v", err)
		}
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if err == nil {
		t.Fatal("CommitJSON() accepted a substituted temporary junction")
	}
	if got, err := os.ReadFile(outsideTarget); err != nil || string(got) != "outside must survive" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(parent, id(2)+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("junction became visible final artifact: %v", err)
	}
}

func TestCommitJSONSurfacesTemporaryRemovalFailureAndRollsBack(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "tasks", id(1))
	var temporary string
	store.hooks.beforeTempRemove = func(name string) {
		temporary = filepath.Join(parent, name)
		if err := os.Remove(temporary); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(temporary, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(temporary, "blocker"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, id(2)+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed commit left visible final artifact: %v", err)
	}
	if info, err := os.Lstat(temporary); err != nil || !info.IsDir() {
		t.Fatalf("temp-removal failure was not surfaced with the blocking entry intact: %v", err)
	}
}

func TestCommitJSONAncestorSwapCannotPublishOutsideRoot(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	parent := filepath.Join(root, "tasks", id(1))
	moved := filepath.Join(root, "moved-task")
	store.hooks.beforePublish = func(temporaryName string) {
		if err := os.Rename(parent, moved); err != nil {
			if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
				t.Skipf("Windows pins the open temporary file's parent against rename: %v", err)
			}
			t.Fatalf("move artifact parent: %v", err)
		}
		if err := makeDirectoryLink(outside, parent); err != nil {
			t.Skipf("directory links are unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(outside, temporaryName), []byte("outside temporary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err = store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if err == nil {
		t.Fatal("CommitJSON() succeeded after its parent path was replaced")
	}
	if _, err := os.Lstat(filepath.Join(outside, id(2)+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("commit published outside root: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(outside, firstTemporaryName(t, outside))); err != nil || string(got) != "outside temporary" {
		t.Fatalf("outside temporary changed: %q, %v", got, err)
	}
}

func TestCleanupDeletesTempsAndOrphansButPreservesReferences(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	referenced := commit(t, store, id(1), id(2))
	orphan := commit(t, store, id(1), id(3))
	temp := filepath.Join(filepath.Dir(artifactPath(root, referenced)), ".artifact-interrupted.tmp")
	if err := os.WriteFile(temp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	refs := map[string]struct{}{referenced.RelativePath: {}}
	if err := store.Cleanup(context.Background(), refs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifactPath(root, referenced)); err != nil {
		t.Fatalf("referenced artifact removed: %v", err)
	}
	for _, removed := range []string{artifactPath(root, orphan), temp} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup retained %q: %v", filepath.Base(removed), err)
		}
	}
}

func TestCleanupPreservesCanonicalDatabaseReferencesFromOtherArtifactKinds(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	relative := "history/stdout.txt"
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("output"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.Cleanup(context.Background(), map[string]struct{}{relative: {}}); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "output" {
		t.Fatalf("referenced artifact = %q, %v", got, err)
	}
}

func TestCleanupAncestorSwapCannotDeleteOutsideRoot(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := commit(t, store, id(1), id(2))
	parent := filepath.Join(root, "tasks", id(1))
	moved := filepath.Join(root, "moved-task")
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, id(2)+".json")
	if err := os.WriteFile(outsideTarget, []byte("outside must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	store.hooks.beforeCleanupRemove = func(relative string) {
		if swapped || relative != artifact.RelativePath {
			return
		}
		swapped = true
		if err := os.Rename(parent, moved); err != nil {
			t.Fatalf("move artifact parent: %v", err)
		}
		if err := makeDirectoryLink(outside, parent); err != nil {
			t.Skipf("directory links are unavailable: %v", err)
		}
	}

	_ = store.Cleanup(context.Background(), nil)
	if got, err := os.ReadFile(outsideTarget); err != nil || string(got) != "outside must survive" {
		t.Fatalf("cleanup changed outside target: %q, %v", got, err)
	}
}

func TestCleanupChecksCancellationAtExecutionEntry(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := commit(t, store, id(1), id(2))
	ctx, cancel := context.WithCancel(context.Background())
	store.hooks.beforeCleanupExecute = cancel

	if err := store.Cleanup(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(artifactPath(root, orphan)); err != nil {
		t.Fatalf("cancelled cleanup removed artifact: %v", err)
	}
}

func TestCleanupChecksCancellationImmediatelyBeforeDirectoryRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	emptyRelative := filepath.ToSlash(filepath.Join("tasks", id(1)))
	empty := filepath.Join(root, filepath.FromSlash(emptyRelative))
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.hooks.beforeCleanupRemove = func(relative string) {
		if relative == emptyRelative {
			cancel()
		}
	}

	if err := store.Cleanup(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if info, err := os.Stat(empty); err != nil || !info.IsDir() {
		t.Fatalf("cancelled cleanup removed directory: %v", err)
	}
}

func TestCleanupRejectsInvalidReferencesBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := commit(t, store, id(1), id(2))
	for _, invalid := range []string{"../escape", filepath.Join(root, "absolute.json"), "tasks/CON/file.json"} {
		err := store.Cleanup(context.Background(), map[string]struct{}{invalid: {}})
		if !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("Cleanup(%q) error = %v", invalid, err)
		}
		if _, err := os.Stat(artifactPath(root, orphan)); err != nil {
			t.Fatalf("cleanup mutated store before rejecting reference: %v", err)
		}
	}
}

func TestCleanupRejectsUnknownFileTypesWithoutDeletingAnything(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows AF_UNIX socket files do not expose a removable filesystem entry")
	}
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := commit(t, store, id(1), id(2))
	socketPath := filepath.Join(root, "unknown.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("Unix sockets are unavailable: %v", err)
	}
	defer listener.Close()

	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(artifactPath(root, orphan)); err != nil {
		t.Fatalf("cleanup deleted an orphan before completing its safety audit: %v", err)
	}
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("cleanup changed unknown entry: %v", err)
	}
}

func TestReadAndCleanupRejectLinksWithoutTouchingOutside(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := commit(t, store, id(1), id(2))
	target := artifactPath(root, artifact)
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("links are unavailable on this platform: %v", err)
	}

	if _, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("unsafe entry changed: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != string(data) {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestReadAndCleanupRejectLinkedAncestorWithoutTouchingOutside(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	taskID, artifactID := id(1), id(2)
	outside := t.TempDir()
	data := []byte("{\"outcome\":\"cancelled\"}\n")
	if err := os.WriteFile(filepath.Join(outside, artifactID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	tasks := filepath.Join(root, "tasks")
	if err := os.Mkdir(tasks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := makeDirectoryLink(outside, filepath.Join(tasks, taskID)); err != nil {
		t.Skipf("directory links are unavailable on this platform: %v", err)
	}
	sum := sha256.Sum256(data)
	artifact := task.Artifact{
		ID: artifactID, TaskID: taskID, Kind: "task-summary",
		RelativePath: "tasks/" + taskID + "/" + artifactID + ".json",
		MIMEType:     "application/json", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		CreatedAt: time.Unix(0, 0).UTC(),
	}

	if _, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(outside, artifactID+".json")); err != nil || string(got) != string(data) {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestNewRejectsLinkedRoot(t *testing.T) {
	outside := t.TempDir()
	root := filepath.Join(t.TempDir(), "artifacts")
	if err := makeDirectoryLink(outside, root); err != nil {
		t.Skipf("directory links are unavailable on this platform: %v", err)
	}
	if _, err := New(root); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("New() error = %v", err)
	}
}

func TestOperationsHonorCancelledContextWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CommitJSON(ctx, id(1), id(2), time.Unix(0, 0).UTC(), struct{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitJSON() error = %v", err)
	}
	if err := store.Cleanup(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled operations mutated store: %v", err)
	}
}

func committedArtifact(t *testing.T) (*Store, task.Artifact, string) {
	t.Helper()
	root := t.TempDir()
	store, err := newStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	return store, commit(t, store, id(1), id(2)), root
}

func commit(t *testing.T, store *Store, taskID, artifactID string) task.Artifact {
	t.Helper()
	artifact, err := store.CommitJSON(context.Background(), taskID, artifactID, time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func artifactPath(root string, artifact task.Artifact) string {
	return filepath.Join(root, filepath.FromSlash(artifact.RelativePath))
}

func id(value byte) string {
	return strings.Repeat(string("0123456789abcdef"[value%16]), 32)
}

func newStore(t *testing.T, root string) (*Store, error) {
	t.Helper()
	store, err := New(root)
	if err == nil {
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
	}
	return store, err
}

func assertNoTemporaryArtifacts(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && temporaryArtifactName(entry.Name()) {
			t.Errorf("temporary artifact remains: %s", entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func firstTemporaryName(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if temporaryArtifactName(entry.Name()) {
			return entry.Name()
		}
	}
	t.Fatal("outside temporary file not found")
	return ""
}

func makeDirectoryLink(target, link string) error {
	if err := os.Symlink(target, link); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// Directory junctions do not require the symbolic-link privilege. Arguments
	// are test-created absolute paths and are passed separately to cmd.exe.
	return exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run()
}
