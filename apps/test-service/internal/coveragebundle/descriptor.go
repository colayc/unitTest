package coveragebundle

import (
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
	"sync"
)

var (
	ErrDescriptorIntegrity = errors.New("coverage descriptor integrity check failed")
	ErrDescriptorClosed    = errors.New("coverage descriptor is closed")
)

// Descriptor is the closed JSON contract consumed by the bundled runner.
// Keep the field set in this order: encoding/json preserves struct order and
// therefore produces deterministic bytes for the atomic descriptor file.
type Descriptor struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Root            string `json:"root"`
	ObjectDirectory string `json:"objectDirectory"`
	GcovExecutable  string `json:"gcovExecutable"`
	OutputPath      string `json:"outputPath"`
}

type DescriptorInput = Descriptor

// OwnedDescriptor is an immutable, service-owned descriptor file. It retains
// native path identities for the descriptor, task root, and gcov executable so
// verification is not a lexical-prefix check.
type OwnedDescriptor struct {
	mu sync.Mutex

	descriptor Descriptor
	path       string
	root       string
	taskRoot   string
	digest     string

	descriptorFile *os.File
	descriptorInfo os.FileInfo
	gcovFile       *os.File
	gcovInfo       os.FileInfo
	closed         bool
}

func NewDescriptor(root, objectDirectory, gcovExecutable, outputPath string) (Descriptor, error) {
	descriptor := Descriptor{
		SchemaVersion:   1,
		Root:            root,
		ObjectDirectory: objectDirectory,
		GcovExecutable:  gcovExecutable,
		OutputPath:      outputPath,
	}
	if err := validateDescriptorFields(descriptor); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

func (descriptor Descriptor) WriteAtomic(coverageRoot, taskID string) (*OwnedDescriptor, error) {
	if err := validateDescriptorFields(descriptor); err != nil {
		return nil, err
	}
	coverageRoot, err := canonicalAbsoluteDirectory(coverageRoot)
	if err != nil {
		return nil, integrityError("coverage root", err)
	}
	if !validTaskID(taskID) {
		return nil, integrityError("task id", errors.New("invalid task id"))
	}
	taskRoot := filepath.Join(coverageRoot, taskID)
	if filepath.Clean(taskRoot) != taskRoot || !pathWithin(coverageRoot, taskRoot) {
		return nil, integrityError("task root", errors.New("task root escapes coverage root"))
	}
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		return nil, integrityError("task root", err)
	}
	cleanup := func() { _ = os.RemoveAll(taskRoot) }
	if err := verifyDirectDirectory(coverageRoot); err != nil {
		cleanup()
		return nil, integrityError("coverage root", err)
	}
	if err := verifyDirectDirectory(taskRoot); err != nil {
		cleanup()
		return nil, integrityError("task root", err)
	}
	if !pathWithin(taskRoot, descriptor.OutputPath) || filepath.Dir(descriptor.OutputPath) != taskRoot {
		cleanup()
		return nil, integrityError("output path", errors.New("output must be a direct child of task root"))
	}
	for label, path := range map[string]string{
		"root": descriptor.Root, "object directory": descriptor.ObjectDirectory,
	} {
		if err := verifyDirectDirectory(path); err != nil {
			cleanup()
			return nil, integrityError(label, err)
		}
	}
	gcovFile, gcovInfo, err := openDescriptorGcov(descriptor.GcovExecutable)
	if err != nil {
		cleanup()
		return nil, integrityError("gcov executable", err)
	}
	closeGcov := func() {
		_ = gcovFile.Close()
		cleanup()
	}
	if err := verifyFilePath(descriptor.GcovExecutable, gcovInfo, gcovFile); err != nil {
		closeGcov()
		return nil, integrityError("gcov executable", err)
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		closeGcov()
		return nil, integrityError("marshal descriptor", err)
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(taskRoot, ".descriptor-*.tmp")
	if err != nil {
		closeGcov()
		return nil, integrityError("create descriptor temporary", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		closeGcov()
	}
	if _, err := temporary.Write(raw); err != nil {
		removeTemporary()
		return nil, integrityError("write descriptor", err)
	}
	if err := temporary.Sync(); err != nil {
		removeTemporary()
		return nil, integrityError("sync descriptor", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		closeGcov()
		return nil, integrityError("close descriptor temporary", err)
	}
	descriptorPath := filepath.Join(taskRoot, "descriptor.json")
	if err := os.Rename(temporaryPath, descriptorPath); err != nil {
		_ = os.Remove(temporaryPath)
		closeGcov()
		return nil, integrityError("publish descriptor", err)
	}
	path := descriptorPath
	descriptorFile, err := os.Open(path)
	if err != nil {
		closeGcov()
		cleanup()
		return nil, integrityError("open descriptor", err)
	}
	descriptorInfo, err := descriptorFile.Stat()
	if err != nil {
		_ = descriptorFile.Close()
		closeGcov()
		return nil, integrityError("stat descriptor", err)
	}
	digest, err := digestFile(descriptorFile)
	if err != nil {
		_ = descriptorFile.Close()
		closeGcov()
		return nil, integrityError("digest descriptor", err)
	}
	owned := &OwnedDescriptor{
		descriptor: descriptor,
		path:       path, root: coverageRoot, taskRoot: taskRoot,
		digest: digest, descriptorFile: descriptorFile,
		descriptorInfo: descriptorInfo, gcovFile: gcovFile, gcovInfo: gcovInfo,
	}
	if err := owned.Verify(); err != nil {
		_ = owned.Close()
		return nil, err
	}
	return owned, nil
}

func (owned *OwnedDescriptor) Path() string {
	if owned == nil {
		return ""
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.path
}

func (owned *OwnedDescriptor) TaskRoot() string {
	if owned == nil {
		return ""
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.taskRoot
}

func (owned *OwnedDescriptor) Descriptor() Descriptor {
	if owned == nil {
		return Descriptor{}
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.descriptor
}

func (owned *OwnedDescriptor) Verify() error {
	if owned == nil {
		return ErrDescriptorClosed
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.verifyLocked()
}

func (owned *OwnedDescriptor) verifyLocked() error {
	if owned.closed || owned.descriptorFile == nil || owned.gcovFile == nil {
		return ErrDescriptorClosed
	}
	if err := verifyDirectDirectory(owned.root); err != nil {
		return fmt.Errorf("%w: root: %v", ErrDescriptorIntegrity, err)
	}
	if err := verifyDirectDirectory(owned.taskRoot); err != nil {
		return fmt.Errorf("%w: task root: %v", ErrDescriptorIntegrity, err)
	}
	if err := verifyFilePath(owned.path, owned.descriptorInfo, owned.descriptorFile); err != nil {
		return fmt.Errorf("%w: descriptor: %v", ErrDescriptorIntegrity, err)
	}
	if digest, err := digestFile(owned.descriptorFile); err != nil || digest != owned.digest {
		if err == nil {
			err = errors.New("descriptor digest changed")
		}
		return fmt.Errorf("%w: %v", ErrDescriptorIntegrity, err)
	}
	for label, path := range map[string]string{
		"root": owned.descriptor.Root, "object directory": owned.descriptor.ObjectDirectory,
	} {
		if err := verifyDirectDirectory(path); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrDescriptorIntegrity, label, err)
		}
	}
	if err := verifyFilePath(owned.descriptor.GcovExecutable, owned.gcovInfo, owned.gcovFile); err != nil {
		return fmt.Errorf("%w: gcov executable: %v", ErrDescriptorIntegrity, err)
	}
	return nil
}

func (owned *OwnedDescriptor) Close() error {
	if owned == nil {
		return nil
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	if owned.closed {
		return nil
	}
	verifyErr := owned.verifyLocked()
	owned.closed = true
	var closeErr error
	if owned.descriptorFile != nil {
		closeErr = errors.Join(closeErr, owned.descriptorFile.Close())
		owned.descriptorFile = nil
	}
	if owned.gcovFile != nil {
		closeErr = errors.Join(closeErr, owned.gcovFile.Close())
		owned.gcovFile = nil
	}
	return errors.Join(verifyErr, closeErr)
}

func validateDescriptorFields(descriptor Descriptor) error {
	if descriptor.SchemaVersion != 1 {
		return integrityError("descriptor schema", errors.New("unsupported schema version"))
	}
	for label, path := range map[string]string{
		"root": descriptor.Root, "object directory": descriptor.ObjectDirectory,
		"gcov executable": descriptor.GcovExecutable, "output path": descriptor.OutputPath,
	} {
		if err := validateAbsolutePath(path); err != nil {
			return integrityError(label, err)
		}
	}
	return nil
}

func validateAbsolutePath(path string) error {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("path must be absolute, normalized, and NUL-free")
	}
	return nil
}

func canonicalAbsoluteDirectory(path string) (string, error) {
	if err := validateAbsolutePath(path); err != nil {
		return "", err
	}
	if err := verifyDirectDirectory(path); err != nil {
		return "", err
	}
	return path, nil
}

func verifyDirectDirectory(path string) error {
	if err := validateAbsolutePath(path); err != nil {
		return err
	}
	pinned, err := pinDirectObject(path, true)
	if err != nil {
		return fmt.Errorf("directory resolves through symlink or junction: %v", err)
	}
	_ = pinned.Close()
	return nil
}

func verifyFilePath(path string, expected os.FileInfo, file *os.File) error {
	if file == nil || expected == nil {
		return errors.New("file pin is closed")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, info) {
		if err == nil {
			err = errors.New("file identity changed")
		}
		return err
	}
	handle, err := file.Stat()
	if err != nil || !os.SameFile(expected, handle) {
		if err == nil {
			err = errors.New("file handle identity changed")
		}
		return err
	}
	return nil
}

func openDescriptorGcov(path string) (*os.File, os.FileInfo, error) {
	if err := validateAbsolutePath(path); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("not a direct regular file")
		}
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	handle, err := file.Stat()
	if err != nil || !os.SameFile(info, handle) {
		_ = file.Close()
		if err == nil {
			err = errors.New("file identity changed while opening")
		}
		return nil, nil, err
	}
	return file, handle, nil
}

func digestFile(file *os.File) (string, error) {
	if file == nil {
		return "", ErrDescriptorClosed
	}
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

func validTaskID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' && index > 0 || char == '-' && index > 0 || char == '_' && index > 0 {
			continue
		}
		return false
	}
	return true
}

func pathWithin(root, candidate string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.VolumeName(root), filepath.VolumeName(candidate))
	}
	return true
}

func sameNativePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
