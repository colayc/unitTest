package artifactstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

const MaxReadChunk = 64 * 1024

var (
	ErrInvalidArtifact  = errors.New("invalid artifact metadata")
	ErrInvalidRange     = errors.New("invalid artifact range")
	ErrArtifactChanged  = errors.New("artifact content changed")
	ErrUnsafePath       = errors.New("unsafe artifact path")
	ErrStoreUnavailable = errors.New("artifact store unavailable")
)

type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if root == "" {
		return nil, ErrUnsafePath
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrUnsafePath
	}
	absolute = filepath.Clean(absolute)
	if err := checkAbsoluteNoLinks(absolute, true); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, ErrStoreUnavailable
	}
	if err := checkAbsoluteNoLinks(absolute, false); err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() {
		return nil, ErrStoreUnavailable
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, ErrStoreUnavailable
	}
	return &Store{root: absolute}, nil
}

func (s *Store) CommitJSON(ctx context.Context, taskID, artifactID string, at time.Time, value any) (task.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return task.Artifact{}, err
	}
	if !validGeneratedID(taskID) || !validGeneratedID(artifactID) || at.IsZero() {
		return task.Artifact{}, ErrInvalidArtifact
	}
	data, err := json.Marshal(value)
	if err != nil {
		return task.Artifact{}, ErrInvalidArtifact
	}
	data = append(data, '\n')
	relative := artifactRelativePath(taskID, artifactID)
	target, err := s.safeTarget(relative, true)
	if err != nil {
		return task.Artifact{}, err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return task.Artifact{}, ErrStoreUnavailable
	}
	if err := checkAbsoluteNoLinks(parent, false); err != nil {
		return task.Artifact{}, err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return task.Artifact{}, ErrStoreUnavailable
	}
	if _, err := os.Lstat(target); err == nil {
		return task.Artifact{}, ErrInvalidArtifact
	} else if !errors.Is(err, os.ErrNotExist) {
		return task.Artifact{}, ErrStoreUnavailable
	}

	temporary, err := os.CreateTemp(parent, ".artifact-*.tmp")
	if err != nil {
		return task.Artifact{}, ErrStoreUnavailable
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	failed := func(err error) (task.Artifact, error) {
		_ = temporary.Close()
		return task.Artifact{}, err
	}
	if err := temporary.Chmod(0o600); err != nil {
		return failed(ErrStoreUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	if _, err := temporary.Write(data); err != nil {
		return failed(ErrStoreUnavailable)
	}
	if err := temporary.Sync(); err != nil {
		return failed(ErrStoreUnavailable)
	}
	if err := temporary.Close(); err != nil {
		return task.Artifact{}, ErrStoreUnavailable
	}
	if err := checkAbsoluteNoLinks(parent, false); err != nil {
		return task.Artifact{}, err
	}
	if err := renameAtomic(temporaryName, target); err != nil {
		return task.Artifact{}, err
	}
	if err := syncDirectory(parent); err != nil {
		return task.Artifact{}, err
	}

	sum := sha256.Sum256(data)
	return task.Artifact{
		ID:           artifactID,
		TaskID:       taskID,
		Kind:         "task-summary",
		RelativePath: relative,
		MIMEType:     "application/json",
		Size:         int64(len(data)),
		SHA256:       hex.EncodeToString(sum[:]),
		CreatedAt:    at,
	}, nil
}

func (s *Store) ReadChunk(ctx context.Context, artifact task.Artifact, offset int64, length int) ([]byte, int64, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	if offset < 0 || length < 1 || length > MaxReadChunk {
		return nil, 0, false, ErrInvalidRange
	}
	if !validArtifact(artifact) {
		return nil, 0, false, ErrInvalidArtifact
	}
	expected := artifactRelativePath(artifact.TaskID, artifact.ID)
	target, err := s.safeTarget(expected, false)
	if err != nil {
		return nil, 0, false, err
	}
	file, err := openNoFollow(target)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, false, ErrStoreUnavailable
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, ErrUnsafePath
	}
	if info.Size() != artifact.Size {
		return nil, 0, false, ErrArtifactChanged
	}
	if offset > artifact.Size {
		return nil, 0, false, ErrInvalidRange
	}
	actual, err := hashFile(ctx, file)
	if err != nil {
		return nil, 0, false, err
	}
	expectedHash, _ := hex.DecodeString(artifact.SHA256)
	if subtle.ConstantTimeCompare(actual[:], expectedHash) != 1 {
		return nil, 0, false, ErrArtifactChanged
	}
	if after, err := file.Stat(); err != nil {
		return nil, 0, false, ErrStoreUnavailable
	} else if after.Size() != artifact.Size || after.ModTime() != info.ModTime() {
		return nil, 0, false, ErrArtifactChanged
	}

	buffer := make([]byte, length)
	n, readErr := file.ReadAt(buffer, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, 0, false, ErrStoreUnavailable
	}
	next := offset + int64(n)
	return buffer[:n], next, next == artifact.Size, nil
}

func (s *Store) Cleanup(ctx context.Context, referenced map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for relative := range referenced {
		if !validArtifactRelativePath(relative) {
			return ErrInvalidArtifact
		}
	}
	if err := checkAbsoluteNoLinks(s.root, false); err != nil {
		return err
	}

	var files []string
	var directories []string
	var inspect func(string) error
	inspect = func(directory string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return ErrStoreUnavailable
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			entryPath := filepath.Join(directory, entry.Name())
			relative, err := filepath.Rel(s.root, entryPath)
			if err != nil || !containedRelative(relative) {
				return ErrUnsafePath
			}
			if _, err := s.safeTarget(filepath.ToSlash(relative), false); err != nil {
				return err
			}
			info, err := os.Lstat(entryPath)
			if err != nil {
				return ErrStoreUnavailable
			}
			linked, err := pathEntryIsLink(entryPath, info)
			if err != nil {
				return ErrStoreUnavailable
			}
			if linked {
				return ErrUnsafePath
			}
			switch {
			case info.IsDir():
				if err := inspect(entryPath); err != nil {
					return err
				}
				directories = append(directories, filepath.ToSlash(relative))
			case info.Mode().IsRegular():
				portable := filepath.ToSlash(relative)
				if temporaryArtifactName(entry.Name()) {
					files = append(files, portable)
				} else if _, ok := referenced[portable]; !ok {
					files = append(files, portable)
				}
			default:
				return ErrUnsafePath
			}
		}
		return nil
	}
	if err := inspect(s.root); err != nil {
		return err
	}

	for _, relative := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := s.safeTarget(relative, false)
		if err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil {
			return ErrStoreUnavailable
		}
		linked, err := pathEntryIsLink(target, info)
		if err != nil {
			return ErrStoreUnavailable
		}
		if linked || !info.Mode().IsRegular() {
			return ErrUnsafePath
		}
		if err := os.Remove(target); err != nil {
			return ErrStoreUnavailable
		}
	}
	for _, relative := range directories {
		target, err := s.safeTarget(relative, false)
		if err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !isDirectoryNotEmpty(err) {
			return ErrStoreUnavailable
		}
	}
	return nil
}

func (s *Store) safeTarget(relative string, allowMissingLeaf bool) (string, error) {
	if !canonicalRelativePath(relative) {
		return "", ErrUnsafePath
	}
	target := filepath.Join(s.root, filepath.FromSlash(relative))
	relativeToRoot, err := filepath.Rel(s.root, target)
	if err != nil || !containedRelative(relativeToRoot) {
		return "", ErrUnsafePath
	}
	if err := checkAbsoluteNoLinks(target, allowMissingLeaf); err != nil {
		return "", err
	}
	return target, nil
}

func hashFile(ctx context.Context, file *os.File) ([sha256.Size]byte, error) {
	hash := sha256.New()
	buffer := make([]byte, MaxReadChunk)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		n, err := file.ReadAt(buffer, offset)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			offset += int64(n)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return [sha256.Size]byte{}, ErrStoreUnavailable
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func validArtifact(value task.Artifact) bool {
	if !validGeneratedID(value.ID) || !validGeneratedID(value.TaskID) || value.Kind != "task-summary" ||
		value.MIMEType != "application/json" || value.Size < 0 || value.CreatedAt.IsZero() ||
		value.RelativePath != artifactRelativePath(value.TaskID, value.ID) || len(value.SHA256) != sha256.Size*2 ||
		strings.ToLower(value.SHA256) != value.SHA256 {
		return false
	}
	_, err := hex.DecodeString(value.SHA256)
	return err == nil
}

func validGeneratedID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func artifactRelativePath(taskID, artifactID string) string {
	return path.Join("tasks", taskID, artifactID+".json")
}

func validArtifactRelativePath(relative string) bool {
	return canonicalRelativePath(relative)
}

func canonicalRelativePath(relative string) bool {
	if relative == "" || path.IsAbs(relative) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || strings.Contains(relative, "\\") {
		return false
	}
	clean := path.Clean(relative)
	if clean != relative || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, segment := range strings.Split(relative, "/") {
		if !portablePathSegment(segment) {
			return false
		}
	}
	return true
}

func portablePathSegment(segment string) bool {
	if segment == "" || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return false
	}
	for index := range len(segment) {
		character := segment[index]
		if character <= 0x1f || strings.ContainsRune(`<>:"|?*\`, rune(character)) {
			return false
		}
	}
	base, _, _ := strings.Cut(segment, ".")
	upper := strings.ToUpper(base)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
		return false
	}
	return len(upper) != 4 || (!strings.HasPrefix(upper, "COM") && !strings.HasPrefix(upper, "LPT")) || upper[3] < '1' || upper[3] > '9'
}

func containedRelative(relative string) bool {
	return relative != "" && relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func temporaryArtifactName(name string) bool {
	return strings.HasPrefix(name, ".artifact-") && strings.HasSuffix(name, ".tmp") && len(name) > len(".artifact-.tmp")
}

func checkAbsoluteNoLinks(absolute string, allowMissing bool) error {
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimPrefix(absolute, volume)
	current := volume
	if strings.HasPrefix(remainder, string(filepath.Separator)) {
		current += string(filepath.Separator)
		remainder = strings.TrimLeft(remainder, string(filepath.Separator))
	}
	segments := strings.FieldsFunc(remainder, func(character rune) bool {
		return character == '/' || character == '\\'
	})
	for _, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			return nil
		}
		if err != nil {
			return ErrStoreUnavailable
		}
		linked, err := pathEntryIsLink(current, info)
		if err != nil {
			return ErrStoreUnavailable
		}
		if linked {
			return ErrUnsafePath
		}
	}
	return nil
}
