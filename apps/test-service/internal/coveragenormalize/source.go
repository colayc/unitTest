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

type sourceSnapshot struct {
	binding  SourceBinding
	file     *os.File
	identity physicalSourceID
	opened   os.FileInfo
	limits   Limits
}

// DigestSource opens and hashes one source snapshot after validating its
// resolved path and file identity. The path is checked before opening, on the
// opened handle, and after reading so replacement during digest fails closed.
func DigestSource(workspaceRoot, nativePath string, limits Limits) (SourceBinding, error) {
	snapshot, err := openSourceSnapshot(workspaceRoot, nativePath, limits)
	if err != nil {
		return SourceBinding{}, err
	}
	defer snapshot.close()
	return snapshot.digest()
}

func openSourceSnapshot(workspaceRoot, nativePath string, limits Limits) (*sourceSnapshot, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	binding, err := BindSourcePath(workspaceRoot, nativePath)
	if err != nil {
		return nil, err
	}
	if hasSymlinkComponent(workspaceRoot, nativePath) {
		return nil, ErrSourceIdentity
	}
	before, err := os.Lstat(nativePath)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSourceIdentity
	}
	file, err := os.Open(nativePath)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", ErrSourceIdentity, err)
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		file.Close()
		return nil, ErrSourceIdentity
	}
	identity, err := physicalSourceIdentity(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &sourceSnapshot{binding: binding, file: file, identity: identity, opened: opened, limits: limits}, nil
}

func (snapshot *sourceSnapshot) digest() (SourceBinding, error) {
	if snapshot == nil || snapshot.file == nil {
		return SourceBinding{}, ErrSourceIdentity
	}
	hash := sha256.New()
	readLimit := snapshot.limits.MaxInputBytes
	if readLimit < int64(^uint64(0)>>1) {
		readLimit++
	}
	read, err := io.Copy(hash, io.LimitReader(snapshot.file, readLimit))
	if err != nil {
		return SourceBinding{}, fmt.Errorf("%w: read: %v", ErrSourceIdentity, err)
	}
	if read > snapshot.limits.MaxInputBytes {
		return SourceBinding{}, ErrLimitExceeded
	}
	after, err := os.Lstat(snapshot.binding.NativePath)
	if err != nil || !os.SameFile(snapshot.opened, after) {
		return SourceBinding{}, ErrSourceIdentity
	}
	binding := snapshot.binding
	binding.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return binding, nil
}

func (snapshot *sourceSnapshot) close() {
	if snapshot != nil && snapshot.file != nil {
		_ = snapshot.file.Close()
		snapshot.file = nil
	}
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
