package testgenvalidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

var errIsolation = errors.New("validation isolation failed")

const maxSnapshotFiles = 10000
const maxSnapshotBytes int64 = 512 << 20
const maxEditBytes = 4 << 20

type Roots struct{ Source, Build, Artifacts, SnapshotDigest string }

func sourceFingerprint(root string) (map[string]string, string, error) {
	if !directDirectory(root) {
		return nil, "", errIsolation
	}
	files := map[string]string{}
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errIsolation
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return errIsolation
		}
		rel = filepath.ToSlash(rel)
		if !safeRelative(rel) {
			return errIsolation
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errIsolation
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || !singlyLinked(path) || len(files) >= maxSnapshotFiles || info.Size() < 0 || size > maxSnapshotBytes-info.Size() {
			return errIsolation
		}
		key := strings.ToLower(rel)
		if _, exists := files[key]; exists {
			return errIsolation
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return errIsolation
		}
		files[key] = digestBytes(content)
		size += info.Size()
		return nil
	})
	if err != nil {
		return nil, "", errIsolation
	}
	encoded, _ := json.Marshal(files)
	return files, digestBytes(encoded), nil
}

func safeRelative(name string) bool {
	if name == "" || len(name) > 240 || filepath.IsAbs(name) || strings.ContainsAny(name, "\\:\x00\r\n") || strings.Contains(name, "//") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func directDirectory(path string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if parent := filepath.Dir(current); parent == current {
			return true
		}
	}
}

func snapshot(source, temporary string, edits []testgenrender.StagedFile) (Roots, string, error) {
	if !directDirectory(source) || !directDirectory(temporary) || source == temporary || within(source, temporary) || within(temporary, source) {
		return Roots{}, "", errIsolation
	}
	root, err := os.MkdirTemp(temporary, "testgen-")
	if err != nil {
		return Roots{}, "", errIsolation
	}
	roots := Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}
	for _, dir := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return Roots{}, root, errIsolation
		}
	}
	count := 0
	var bytes int64
	identities := []os.FileInfo{}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errIsolation
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return errIsolation
		}
		rel = filepath.ToSlash(rel)
		if !safeRelative(rel) {
			return errIsolation
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errIsolation
		}
		dest := filepath.Join(roots.Source, filepath.FromSlash(rel))
		if info.IsDir() {
			if err := os.Mkdir(dest, 0700); err != nil {
				return errIsolation
			}
			return nil
		}
		if !info.Mode().IsRegular() || !singlyLinked(path) || count >= maxSnapshotFiles || info.Size() < 0 || bytes > maxSnapshotBytes-info.Size() {
			return errIsolation
		}
		for _, other := range identities {
			if os.SameFile(other, info) {
				return errIsolation
			}
		}
		identities = append(identities, info)
		count++
		bytes += info.Size()
		input, err := os.Open(path)
		if err != nil {
			return errIsolation
		}
		opened, err := input.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return errIsolation
		}
		output, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		if err != nil {
			return errIsolation
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputErr := input.Close()
		if copyErr != nil || closeErr != nil || inputErr != nil {
			return errIsolation
		}
		after, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return errIsolation
		}
		return nil
	})
	if err != nil {
		return Roots{}, root, errIsolation
	}
	seen := map[string]bool{}
	for _, edit := range edits {
		if !safeRelative(edit.Path) || seen[strings.ToLower(edit.Path)] || len(edit.Content) > maxEditBytes || digestBytes(edit.Content) != edit.AfterDigest {
			return Roots{}, root, errIsolation
		}
		seen[strings.ToLower(edit.Path)] = true
		path := filepath.Join(roots.Source, filepath.FromSlash(edit.Path))
		if err := safeEdit(roots.Source, path, edit); err != nil {
			return Roots{}, root, errIsolation
		}
	}
	return roots, root, nil
}

func safeEdit(root, path string, edit testgenrender.StagedFile) error {
	parent := filepath.Dir(path)
	if !within(root, parent) {
		return errIsolation
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return errIsolation
	}
	for current := parent; current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errIsolation
		}
	}
	info, err := os.Lstat(path)
	if edit.BeforeDigest == "" {
		if !os.IsNotExist(err) {
			return errIsolation
		}
	} else {
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errIsolation
		}
		before, err := os.ReadFile(path)
		if err != nil || digestBytes(before) != edit.BeforeDigest {
			return errIsolation
		}
		if err := os.Chmod(path, 0600); err != nil {
			return errIsolation
		}
	}
	if err := os.WriteFile(path, edit.Content, 0400); err != nil {
		return errIsolation
	}
	if err := os.Chmod(path, 0400); err != nil {
		return errIsolation
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func digestBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func cleanup(root string) error {
	if root == "" {
		return nil
	}
	for attempts := 0; attempts < 5; attempts++ {
		if err := os.RemoveAll(root); err == nil {
			if _, err := os.Lstat(root); os.IsNotExist(err) {
				return nil
			}
		}
		time.Sleep(time.Duration(attempts+1) * 20 * time.Millisecond)
	}
	return errIsolation
}
