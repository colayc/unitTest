// Package testgenbundle opens the product-owned, closed Clang frontend bundle.
// It never searches PATH or executes a compiler while establishing identity.
package testgenbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	clangVersion            = "22.1.8"
	resourceDirectory       = "lib/clang/22"
	sourceCommit            = "ca7933e47d3a3451d81e72ac174dcb5aa28b59d1"
	trustedManifestSHA256   = "e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345"
	maximumManifestBytes    = 2 * 1024 * 1024
	maximumReadyBytes       = 1024
	maximumInventoryEntries = 1000
)

type fileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type licenseRecord struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type archiveRecord struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type platformRecord struct {
	Archive     archiveRecord   `json:"archive"`
	ArchiveRoot string          `json:"archiveRoot"`
	Target      string          `json:"target"`
	Executable  string          `json:"executable"`
	ResourceDir string          `json:"resourceDir"`
	Files       []fileRecord    `json:"files"`
	Licenses    []licenseRecord `json:"licenses"`
}

type sourceManifest struct {
	SchemaVersion int                       `json:"schemaVersion"`
	ClangVersion  string                    `json:"clangVersion"`
	SourceCommit  string                    `json:"sourceCommit"`
	Platforms     map[string]platformRecord `json:"platforms"`
}

type readyRecord struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Platform       string `json:"platform"`
	ManifestSHA256 string `json:"manifestSha256"`
}

// Bundle is a verified snapshot of a platform-specific Clang installation.
// Call Verify immediately before using either returned path if installation
// storage is writable by a different principal.
type Bundle struct {
	root           string
	clangPath      string
	resourceDir    string
	manifestSHA256 string
	readySHA256    string
	platform       platformRecord
}

func (b *Bundle) ClangPath() string { return b.clangPath }

func (b *Bundle) ResourceDir() string { return b.resourceDir }

func (b *Bundle) ManifestSHA256() string { return b.manifestSHA256 }

// Open verifies the pinned source manifest, current platform, every file and
// license, and the absence of unlisted files or links before returning paths.
func Open(root string) (*Bundle, error) {
	return openWithExpectedManifest(root, trustedManifestSHA256)
}

func openWithExpectedManifest(root, expectedManifestSHA256 string) (*Bundle, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("Clang bundle root must be an absolute clean path")
	}
	for current := root; ; current = filepath.Dir(current) {
		ancestor, err := os.Lstat(current)
		if err != nil {
			return nil, fmt.Errorf("inspect Clang bundle ancestor: %w", err)
		}
		if ancestor.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Clang bundle ancestor is a symbolic link or reparse point")
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect Clang bundle root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Clang bundle root is not a direct directory")
	}
	manifestBytes, err := readBoundedRegularFile(filepath.Join(root, "manifest.json"), maximumManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read Clang bundle manifest: %w", err)
	}
	manifestHash := digest(manifestBytes)
	if manifestHash != expectedManifestSHA256 {
		return nil, errors.New("Clang bundle manifest does not match the product pin")
	}
	var manifest sourceManifest
	if err := decodeExactJSON(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse Clang bundle manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.ClangVersion != clangVersion || manifest.SourceCommit != sourceCommit || len(manifest.Platforms) != 2 {
		return nil, errors.New("Clang bundle manifest has an unsupported version or platform set")
	}
	key, err := hostPlatform()
	if err != nil {
		return nil, err
	}
	platform, ok := manifest.Platforms[key]
	if !ok || platform.Target != key || platform.ResourceDir != resourceDirectory {
		return nil, errors.New("Clang bundle platform or resource directory mismatch")
	}
	executable := "bin/clang-22"
	if key == "windows-x64" {
		executable = "bin/clang.exe"
	}
	if platform.Executable != executable || len(platform.Licenses) == 0 || len(platform.Files) == 0 {
		return nil, errors.New("Clang bundle executable or license inventory mismatch")
	}
	readyBytes, err := readBoundedRegularFile(filepath.Join(root, "READY"), maximumReadyBytes)
	if err != nil {
		return nil, fmt.Errorf("read Clang bundle READY identity: %w", err)
	}
	var ready readyRecord
	if err := decodeExactJSON(readyBytes, &ready); err != nil {
		return nil, fmt.Errorf("parse Clang bundle READY identity: %w", err)
	}
	if ready.SchemaVersion != 1 || ready.Platform != key || ready.ManifestSHA256 != manifestHash {
		return nil, errors.New("Clang bundle READY identity mismatch")
	}
	b := &Bundle{
		root:           root,
		clangPath:      filepath.Join(root, filepath.FromSlash(executable)),
		resourceDir:    filepath.Join(root, filepath.FromSlash(resourceDirectory)),
		manifestSHA256: manifestHash,
		readySHA256:    digest(readyBytes),
		platform:       platform,
	}
	if err := b.Verify(); err != nil {
		return nil, err
	}
	return b, nil
}

// Verify rechecks the closed tree before a caller launches Clang.
func (b *Bundle) Verify() error {
	if b == nil {
		return errors.New("nil Clang bundle")
	}
	expected := make(map[string]fileRecord, len(b.platform.Files)+len(b.platform.Licenses)+2)
	expected["manifest.json"] = fileRecord{Path: "manifest.json", SHA256: b.manifestSHA256}
	expected["READY"] = fileRecord{Path: "READY", SHA256: b.readySHA256}
	for _, record := range b.platform.Files {
		if err := addRecord(expected, record); err != nil {
			return err
		}
	}
	for _, license := range b.platform.Licenses {
		if !strings.HasPrefix(license.Path, "licenses/") ||
			!strings.HasPrefix(license.URL, "https://raw.githubusercontent.com/llvm/llvm-project/"+sourceCommit+"/") {
			return errors.New("unreviewed Clang license coordinate")
		}
		if err := addRecord(expected, fileRecord{Path: license.Path, SHA256: license.SHA256, Size: license.Size}); err != nil {
			return err
		}
	}
	if len(expected) > maximumInventoryEntries {
		return errors.New("Clang bundle inventory exceeds entry budget")
	}
	if _, ok := expected[b.platform.Executable]; !ok {
		return errors.New("Clang executable absent from inventory")
	}
	resourceFound := false
	for path := range expected {
		if strings.HasPrefix(path, resourceDirectory+"/include/") {
			resourceFound = true
			break
		}
	}
	if !resourceFound {
		return errors.New("Clang resource directory absent from inventory")
	}
	expectedDirs := map[string]bool{}
	for path := range expected {
		parts := strings.Split(path, "/")
		for count := 1; count < len(parts); count++ {
			expectedDirs[strings.Join(parts[:count], "/")] = true
		}
	}
	seen := map[string]bool{}
	seenDirs := map[string]bool{}
	err := filepath.WalkDir(b.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == b.root {
			return nil
		}
		relative, err := filepath.Rel(b.root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if !safePath(name) {
			return fmt.Errorf("unsafe Clang bundle path %q", name)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Clang bundle path is a symlink or reparse point: %s", name)
		}
		if info.IsDir() {
			if !expectedDirs[name] {
				return fmt.Errorf("extra Clang bundle directory: %s", name)
			}
			seenDirs[name] = true
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported Clang bundle entry: %s", name)
		}
		record, ok := expected[name]
		if !ok {
			return fmt.Errorf("extra Clang bundle file: %s", name)
		}
		seen[name] = true
		if name != "manifest.json" && name != "READY" && info.Size() != record.Size {
			return fmt.Errorf("Clang bundle file size mismatch: %s", name)
		}
		actual, err := hashStableFile(path, info)
		if err != nil {
			return err
		}
		if actual != record.SHA256 {
			return fmt.Errorf("Clang bundle file SHA-256 mismatch: %s", name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for path := range expected {
		if !seen[path] {
			return fmt.Errorf("missing Clang bundle file: %s", path)
		}
	}
	for path := range expectedDirs {
		if !seenDirs[path] {
			return fmt.Errorf("missing Clang bundle directory: %s", path)
		}
	}
	return nil
}

func hostPlatform() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", errors.New("unsupported Clang bundle architecture")
	}
	switch runtime.GOOS {
	case "windows":
		return "windows-x64", nil
	case "linux":
		return "linux-x64", nil
	default:
		return "", errors.New("unsupported Clang bundle operating system")
	}
}

func safePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return false
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		for _, c := range component {
			if c < 32 || c > 126 {
				return false
			}
		}
	}
	return true
}

func addRecord(records map[string]fileRecord, record fileRecord) error {
	if !safePath(record.Path) || len(record.SHA256) != 64 || record.Size < 0 {
		return errors.New("invalid Clang bundle inventory record")
	}
	for _, c := range record.SHA256 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return errors.New("non-lowercase Clang bundle digest")
		}
	}
	for path := range records {
		if strings.EqualFold(path, record.Path) {
			return fmt.Errorf("duplicate or case-alias Clang bundle path: %s", record.Path)
		}
	}
	records[record.Path] = record
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("not a bounded direct regular file")
	}
	return os.ReadFile(path)
}

func decodeExactJSON(data []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var remaining any
	if err := decoder.Decode(&remaining); err != io.EOF {
		return errors.New("trailing JSON content")
	}
	return nil
}

func hashStableFile(path string, before os.FileInfo) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", errors.New("Clang bundle file changed while opening")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() {
		return "", errors.New("Clang bundle file changed while verifying")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
