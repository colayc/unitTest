package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// fileSnapshot pins one filesystem object while the resolver trusts its bytes.
// The platform opener rejects a final-component link/reparse point and, on
// Windows, denies write/delete sharing for the lifetime of the snapshot.
//
// BundleRoot and standalone executable parent directories are trusted,
// owner-controlled publication boundaries: publishers must atomically install
// a complete tree and never mutate it in place. Linux path-based exec cannot be
// bound to this handle through probe.Spec/os/exec, so the resolver verifies the
// path, OS identity, and content immediately before and after probing and fails
// closed if that publication contract is violated.
type fileSnapshot struct {
	path       string
	file       *os.File
	info       os.FileInfo
	digest     string
	osIdentity string
}

// SnapshotLaunchInput returns the exact bytes parsed for a closed native
// launch declaration together with their filesystem identity and digest.
func SnapshotLaunchInput(path string, maximum int64) (FingerprintFile, []byte, error) {
	snapshot, err := captureFileSnapshot(path, maximum)
	if err != nil {
		return FingerprintFile{}, nil, err
	}
	defer snapshot.Close()
	content, err := snapshot.ReadAll(maximum)
	if err != nil || snapshot.Verify() != nil {
		return FingerprintFile{}, nil, fmt.Errorf("launch input changed while reading")
	}
	return fingerprintFileFromSnapshot(snapshot), content, nil
}

// VerifyLaunchInput fails when a previously parsed launch input no longer
// names the same direct file with the same bytes.
func VerifyLaunchInput(state FingerprintFile, maximum int64) error {
	if !validFingerprintFile(state) {
		return fmt.Errorf("invalid launch input")
	}
	snapshot, err := captureFileSnapshot(filepath.FromSlash(state.Path), maximum)
	if err != nil {
		return fmt.Errorf("launch input changed")
	}
	defer snapshot.Close()
	if err := snapshot.Verify(); err != nil || snapshot.osIdentity != state.Identity ||
		snapshot.digest != strings.ToLower(state.SHA256) {
		return fmt.Errorf("launch input changed")
	}
	return nil
}

func captureFileSnapshot(path string, maximum int64) (*fileSnapshot, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("not a direct regular file")
	}
	if maximum > 0 && pathInfo.Size() > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}

	file, err := openStableFile(path)
	if err != nil {
		return nil, err
	}
	fail := func(result error) (*fileSnapshot, error) {
		_ = file.Close()
		return nil, result
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) {
		return fail(fmt.Errorf("file identity changed while opening"))
	}
	if maximum > 0 && info.Size() > maximum {
		return fail(fmt.Errorf("file exceeds %d bytes", maximum))
	}
	osIdentity, err := stableFileOSIdentity(file)
	if err != nil {
		return fail(fmt.Errorf("read OS file identity: %w", err))
	}
	digest, err := sha256OpenFile(file)
	if err != nil {
		return fail(err)
	}
	snapshot := &fileSnapshot{
		path: path, file: file, info: info, digest: digest, osIdentity: osIdentity,
	}
	if err := snapshot.Verify(); err != nil {
		return fail(err)
	}
	return snapshot, nil
}

func (snapshot *fileSnapshot) Close() error {
	if snapshot == nil || snapshot.file == nil {
		return nil
	}
	return snapshot.file.Close()
}

func (snapshot *fileSnapshot) ReadAll(maximum int64) ([]byte, error) {
	if snapshot == nil || snapshot.file == nil {
		return nil, fmt.Errorf("file snapshot is closed")
	}
	if snapshot.info.Size() > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	if _, err := snapshot.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(snapshot.file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return data, nil
}

func (snapshot *fileSnapshot) Verify() error {
	if snapshot == nil || snapshot.file == nil {
		return fmt.Errorf("file snapshot is closed")
	}
	before, err := directRegularPathInfo(snapshot.path)
	if err != nil {
		return err
	}
	if !os.SameFile(snapshot.info, before) {
		return fmt.Errorf("path now names a different file")
	}
	info, err := snapshot.file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(snapshot.info, info) {
		return fmt.Errorf("open file identity changed")
	}
	osIdentity, err := stableFileOSIdentity(snapshot.file)
	if err != nil {
		return fmt.Errorf("read OS file identity: %w", err)
	}
	if osIdentity != snapshot.osIdentity {
		return fmt.Errorf("OS file identity changed")
	}
	digest, err := sha256OpenFile(snapshot.file)
	if err != nil {
		return err
	}
	if digest != snapshot.digest {
		return fmt.Errorf("file content changed")
	}
	after, err := directRegularPathInfo(snapshot.path)
	if err != nil {
		return err
	}
	if !os.SameFile(snapshot.info, after) {
		return fmt.Errorf("path changed while verifying")
	}
	return nil
}

func directRegularPathInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("path is not a direct regular file")
	}
	return info, nil
}

func sha256OpenFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
