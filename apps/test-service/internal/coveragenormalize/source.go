package coveragenormalize

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrSourceIdentity = errors.New("coverage source identity validation failed")

// DigestSource opens and hashes one source snapshot after validating its
// resolved path and file identity. The path is checked before opening, on the
// opened handle, and after reading so replacement during digest fails closed.
func DigestSource(workspaceRoot, nativePath string, limits Limits) (SourceBinding, error) {
	if err := limits.Validate(); err != nil {
		return SourceBinding{}, err
	}
	binding, err := BindSourcePath(workspaceRoot, nativePath)
	if err != nil {
		return SourceBinding{}, err
	}
	if hasSymlinkComponent(workspaceRoot, nativePath) {
		return SourceBinding{}, ErrSourceIdentity
	}
	before, err := os.Lstat(nativePath)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return SourceBinding{}, ErrSourceIdentity
	}
	file, err := os.Open(nativePath)
	if err != nil {
		return SourceBinding{}, fmt.Errorf("%w: open: %v", ErrSourceIdentity, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return SourceBinding{}, ErrSourceIdentity
	}
	hash := sha256.New()
	readLimit := limits.MaxInputBytes
	if readLimit < int64(^uint64(0)>>1) {
		readLimit++
	}
	read, err := io.Copy(hash, io.LimitReader(file, readLimit))
	if err != nil {
		return SourceBinding{}, fmt.Errorf("%w: read: %v", ErrSourceIdentity, err)
	}
	if read > limits.MaxInputBytes {
		return SourceBinding{}, ErrLimitExceeded
	}
	after, err := os.Lstat(nativePath)
	if err != nil || !os.SameFile(opened, after) {
		return SourceBinding{}, ErrSourceIdentity
	}
	binding.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return binding, nil
}

func hasSymlinkComponent(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return true
	}
	current := root
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if component == "" || component == "." || component == ".." {
			return true
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}
