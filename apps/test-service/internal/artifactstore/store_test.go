package artifactstore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/task"
)

func TestCommitJSONAndReadChunk(t *testing.T) {
	root := t.TempDir()
	store, err := artifactstore.New(root)
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
	rest, next, eof, err := store.ReadChunk(context.Background(), artifact, next, artifactstore.MaxReadChunk)
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

func TestCommitJSONRejectsNonGeneratedIDs(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
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
			if !errors.Is(err, artifactstore.ErrInvalidArtifact) {
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
		{name: "oversized length", offset: 0, length: artifactstore.MaxReadChunk + 1},
		{name: "past end", offset: artifact.Size + 1, length: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := store.ReadChunk(context.Background(), artifact, test.offset, test.length)
			if !errors.Is(err, artifactstore.ErrInvalidRange) {
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
		if !errors.Is(err, artifactstore.ErrInvalidArtifact) {
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
		if !errors.Is(err, artifactstore.ErrArtifactChanged) {
			t.Fatalf("ReadChunk() error = %v", err)
		}
	})

	t.Run("digest", func(t *testing.T) {
		root := t.TempDir()
		store, err := artifactstore.New(root)
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
		if !errors.Is(err, artifactstore.ErrArtifactChanged) {
			t.Fatalf("ReadChunk() error = %v", err)
		}
	})
}

func TestCleanupDeletesTempsAndOrphansButPreservesReferences(t *testing.T) {
	root := t.TempDir()
	store, err := artifactstore.New(root)
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
	store, err := artifactstore.New(root)
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

func TestCleanupRejectsInvalidReferencesBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	store, err := artifactstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := commit(t, store, id(1), id(2))
	for _, invalid := range []string{"../escape", filepath.Join(root, "absolute.json"), "tasks/CON/file.json"} {
		err := store.Cleanup(context.Background(), map[string]struct{}{invalid: {}})
		if !errors.Is(err, artifactstore.ErrInvalidArtifact) {
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
	store, err := artifactstore.New(root)
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

	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, artifactstore.ErrUnsafePath) {
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
	store, err := artifactstore.New(root)
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

	if _, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 1); !errors.Is(err, artifactstore.ErrUnsafePath) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, artifactstore.ErrUnsafePath) {
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
	store, err := artifactstore.New(root)
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

	if _, _, _, err := store.ReadChunk(context.Background(), artifact, 0, 1); !errors.Is(err, artifactstore.ErrUnsafePath) {
		t.Fatalf("ReadChunk() error = %v", err)
	}
	if err := store.Cleanup(context.Background(), nil); !errors.Is(err, artifactstore.ErrUnsafePath) {
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
	if _, err := artifactstore.New(root); !errors.Is(err, artifactstore.ErrUnsafePath) {
		t.Fatalf("New() error = %v", err)
	}
}

func TestOperationsHonorCancelledContextWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store, err := artifactstore.New(root)
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

func committedArtifact(t *testing.T) (*artifactstore.Store, task.Artifact, string) {
	t.Helper()
	root := t.TempDir()
	store, err := artifactstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return store, commit(t, store, id(1), id(2)), root
}

func commit(t *testing.T, store *artifactstore.Store, taskID, artifactID string) task.Artifact {
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
