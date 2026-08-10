package coveragebundle

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

// ParseDescriptor is the closed parser used by runner-facing tests and
// consumers. It rejects unknown and duplicate JSON members before decoding
// the descriptor contract.
func ParseDescriptor(data []byte) (Descriptor, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("expected object"))
	}
	fields := make(map[string]json.RawMessage, 5)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return Descriptor{}, integrityError("descriptor JSON", errors.New("expected member name"))
		}
		if _, exists := fields[key]; exists {
			return Descriptor{}, integrityError("descriptor JSON", fmt.Errorf("duplicate field %q", key))
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return Descriptor{}, integrityError("descriptor JSON", err)
		}
		fields[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return Descriptor{}, integrityError("descriptor JSON", err)
	}
	if trailing, err := decoder.Token(); err != io.EOF || trailing != nil {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("trailing JSON"))
	}
	allowed := map[string]bool{"schemaVersion": true, "root": true, "objectDirectory": true, "gcovExecutable": true, "outputPath": true}
	for key := range fields {
		if !allowed[key] {
			return Descriptor{}, integrityError("descriptor JSON", fmt.Errorf("unknown field %q", key))
		}
	}
	var descriptor Descriptor
	encoded, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(encoded, &descriptor) != nil {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("invalid descriptor fields"))
	}
	return NewDescriptor(descriptor.Root, descriptor.ObjectDirectory, descriptor.GcovExecutable, descriptor.OutputPath)
}

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

	descriptorFile     *os.File
	descriptorInfo     os.FileInfo
	coverageRoot       *VerifiedDirectory
	taskRootCapability *VerifiedDirectory
	rootCapability     *VerifiedDirectory
	objectCapability   *VerifiedDirectory
	gcovCapability     *VerifiedExecutable
	provenance         *VerifiedDirectory
	outputPin          *pinnedObject
	outputFile         *os.File
	outputInfo         os.FileInfo
	outputDigest       string
	closed             bool
}

type pathIdentity struct {
	path string
	info os.FileInfo
}

// VerifiedDirectory is a capability containing component-wise native path
// identities. It is required by descriptor construction; a bare absolute
// path cannot authorize a runner input.
type VerifiedDirectory struct {
	mu         sync.Mutex
	path       string
	identities []pathIdentity
	pins       []*pinnedObject
	parent     *VerifiedDirectory
	closed     bool
}

// VerifiedExecutable pins an executable handle and digest and requires its
// path to be under the caller-authorized directory capability.
type VerifiedExecutable struct {
	mu       sync.Mutex
	path     string
	root     *VerifiedDirectory
	parent   *VerifiedDirectory
	ownsRoot bool
	pin      *pinnedObject
	file     *os.File
	info     os.FileInfo
	digest   string
	closed   bool
}

type DescriptorCapabilities struct {
	// Provenance is the service-owned authority from which all three
	// descriptor directories are resolved. Bare absolute paths are not an
	// authorization boundary.
	Provenance      *VerifiedDirectory
	CoverageRoot    *VerifiedDirectory
	Root            *VerifiedDirectory
	ObjectDirectory *VerifiedDirectory
	GcovExecutable  *VerifiedExecutable
}

func NewVerifiedDirectory(path string) (*VerifiedDirectory, error) {
	if err := validateAbsolutePath(path); err != nil {
		return nil, err
	}
	identities, pins, err := capturePathIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("capture verified directory: %w", err)
	}
	return &VerifiedDirectory{path: path, identities: identities, pins: pins}, nil
}

// NewVerifiedDirectoryFrom resolves a child relative to an already trusted
// directory capability. The parent capability remains independently owned and
// is re-verified on every child verification.
func NewVerifiedDirectoryFrom(parent *VerifiedDirectory, relative string) (*VerifiedDirectory, error) {
	if parent == nil || filepath.IsAbs(relative) || relative == "" || filepath.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return nil, errors.New("invalid relative verified directory")
	}
	parent.mu.Lock()
	if parent.closed || len(parent.pins) == 0 {
		parent.mu.Unlock()
		return nil, ErrDescriptorClosed
	}
	base := parent.path
	var current *pinnedObject
	for _, pin := range parent.pins {
		if pin.path == parent.path {
			current = pin
			break
		}
	}
	if current == nil {
		parent.mu.Unlock()
		return nil, ErrDescriptorIntegrity
	}
	parts := strings.FieldsFunc(relative, func(r rune) bool { return r == '\\' || r == '/' })
	pins := make([]*pinnedObject, 0, len(parts))
	for _, part := range parts {
		child, err := pinChildObject(current, part, true)
		if err != nil {
			for i := len(pins) - 1; i >= 0; i-- {
				_ = pins[i].Close()
			}
			parent.mu.Unlock()
			return nil, err
		}
		pins = append(pins, child)
		current = child
	}
	parent.mu.Unlock()
	path := filepath.Join(base, relative)
	identities := make([]pathIdentity, 0, len(parent.identities)+len(pins))
	identities = append(identities, parent.identities...)
	for _, pin := range pins {
		identities = append(identities, pathIdentity{path: pin.path, info: pin.identity})
	}
	return &VerifiedDirectory{path: path, identities: identities, pins: pins, parent: parent}, nil
}

func NewVerifiedExecutable(authorizedRoot, path string) (*VerifiedExecutable, error) {
	root, err := NewVerifiedDirectory(authorizedRoot)
	if err != nil {
		return nil, err
	}
	if !pathWithin(root.path, path) {
		_ = root.Close()
		return nil, errors.New("executable is outside authorized root")
	}
	file, info, err := openDescriptorGcov(path)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	digest, err := digestFile(file)
	if err != nil {
		_ = file.Close()
		_ = root.Close()
		return nil, err
	}
	return &VerifiedExecutable{path: path, root: root, file: file, info: info, digest: digest}, nil
}

func NewVerifiedExecutableFrom(parent *VerifiedDirectory, relative string) (*VerifiedExecutable, error) {
	if parent == nil || filepath.IsAbs(relative) || relative == "" || filepath.Clean(relative) != relative {
		return nil, errors.New("invalid relative verified executable")
	}
	parent.mu.Lock()
	if parent.closed || len(parent.pins) == 0 {
		parent.mu.Unlock()
		return nil, ErrDescriptorClosed
	}
	base := parent.path
	parent.mu.Unlock()
	dirCapability := parent
	directory := filepath.Dir(relative)
	if directory != "." {
		var dirErr error
		dirCapability, dirErr = NewVerifiedDirectoryFrom(parent, directory)
		if dirErr != nil {
			return nil, dirErr
		}
	}
	dirCapability.mu.Lock()
	var parentPin *pinnedObject
	for _, pin := range dirCapability.pins {
		if pin.path == dirCapability.path {
			parentPin = pin
			break
		}
	}
	if parentPin == nil {
		dirCapability.mu.Unlock()
		return nil, ErrDescriptorIntegrity
	}
	filePin, err := pinChildObject(parentPin, filepath.Base(relative), false)
	dirCapability.mu.Unlock()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, relative)
	file, err := openDescriptorOutput(path)
	if err != nil {
		_ = filePin.Close()
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil || !os.SameFile(filePin.identity, info) {
		_ = file.Close()
		_ = filePin.Close()
		if statErr == nil {
			statErr = errors.New("executable identity changed while opening")
		}
		return nil, statErr
	}
	_ = filePin.Close()
	digest, err := digestFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &VerifiedExecutable{path: path, root: dirCapability, parent: parent, ownsRoot: dirCapability != parent, file: file, info: info, digest: digest}, nil
}

func (directory *VerifiedDirectory) Path() string {
	if directory == nil {
		return ""
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	return directory.path
}

func (directory *VerifiedDirectory) Verify() error {
	if directory == nil {
		return ErrDescriptorIntegrity
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return ErrDescriptorClosed
	}
	if directory.parent != nil {
		if err := directory.parent.Verify(); err != nil {
			return err
		}
	}
	for _, pin := range directory.pins {
		if pin.path != directory.path {
			continue
		}
		if err := pin.verifyIdentity(); err != nil {
			return fmt.Errorf("%w: verified directory %s: %v", ErrDescriptorIntegrity, pin.path, err)
		}
	}
	pinnedPaths := make(map[string]struct{}, len(directory.pins))
	for _, pin := range directory.pins {
		pinnedPaths[pin.path] = struct{}{}
	}
	for _, identity := range directory.identities {
		info, err := os.Lstat(identity.path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			if err == nil {
				err = errors.New("directory is no longer a direct directory")
			}
			return fmt.Errorf("%w: verified directory %s: %v", ErrDescriptorIntegrity, identity.path, err)
		}
		if runtime.GOOS == "windows" && identity.path != directory.path {
			continue
		}
		if _, pinned := pinnedPaths[identity.path]; pinned {
			continue
		}
		resolved, statErr := os.Stat(identity.path)
		if statErr != nil || !os.SameFile(identity.info, resolved) {
			if statErr == nil {
				statErr = errors.New("directory identity changed or resolves through reparse point")
			}
			return fmt.Errorf("%w: verified directory %s: %v", ErrDescriptorIntegrity, identity.path, statErr)
		}
	}
	return nil
}

func (directory *VerifiedDirectory) Close() error {
	if directory == nil {
		return nil
	}
	directory.mu.Lock()
	directory.closed = true
	pins := directory.pins
	directory.pins = nil
	directory.mu.Unlock()
	var result error
	for index := len(pins) - 1; index >= 0; index-- {
		result = errors.Join(result, pins[index].Close())
	}
	return result
}

func (executable *VerifiedExecutable) Verify() error {
	if executable == nil {
		return ErrDescriptorIntegrity
	}
	executable.mu.Lock()
	defer executable.mu.Unlock()
	if executable.closed || executable.file == nil || executable.root == nil {
		return ErrDescriptorClosed
	}
	if err := executable.root.Verify(); err != nil {
		return err
	}
	if executable.pin != nil {
		if err := executable.pin.verifyIdentity(); err != nil {
			return fmt.Errorf("%w: executable identity: %v", ErrDescriptorIntegrity, err)
		}
	}
	if !pathWithin(executable.root.path, executable.path) || !pathWithin(executable.root.path, executable.path) {
		return ErrDescriptorIntegrity
	}
	if err := verifyFilePath(executable.path, executable.info, executable.file); err != nil {
		return fmt.Errorf("%w: executable identity: %v", ErrDescriptorIntegrity, err)
	}
	digest, err := digestFile(executable.file)
	if err != nil || digest != executable.digest {
		if err == nil {
			err = errors.New("executable digest changed")
		}
		return fmt.Errorf("%w: executable digest: %v", ErrDescriptorIntegrity, err)
	}
	return nil
}

func (executable *VerifiedExecutable) Path() string {
	if executable == nil {
		return ""
	}
	executable.mu.Lock()
	defer executable.mu.Unlock()
	return executable.path
}

func (executable *VerifiedExecutable) Close() error {
	if executable == nil {
		return nil
	}
	executable.mu.Lock()
	if executable.closed {
		executable.mu.Unlock()
		return nil
	}
	executable.closed = true
	file := executable.file
	root := executable.root
	executable.file = nil
	executable.pin = nil
	executable.root = nil
	executable.mu.Unlock()
	var result error
	if file != nil {
		result = errors.Join(result, file.Close())
	}
	if root != nil {
		if executable.parent == nil || executable.ownsRoot {
			result = errors.Join(result, root.Close())
		}
	}
	return result
}

func capturePathIdentities(path string) ([]pathIdentity, []*pinnedObject, error) {
	var identities []pathIdentity
	var pins []*pinnedObject
	fail := func(err error) ([]pathIdentity, []*pinnedObject, error) {
		for index := len(pins) - 1; index >= 0; index-- {
			_ = pins[index].Close()
		}
		return nil, nil, err
	}
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return fail(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(errors.New("path component is not a direct directory"))
		}
		resolvedInfo, statErr := os.Stat(current)
		if statErr != nil {
			return fail(statErr)
		}
		pinned, pinErr := pinDirectObject(current, true)
		if pinErr != nil {
			if current == path {
				return fail(fmt.Errorf("path component resolves through reparse point: %w", pinErr))
			}
		} else {
			pins = append(pins, pinned)
		}
		identities = append([]pathIdentity{{path: current, info: resolvedInfo}}, identities...)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return identities, pins, nil
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

func directoryFinalPin(directory *VerifiedDirectory) *pinnedObject {
	if directory == nil {
		return nil
	}
	for _, pin := range directory.pins {
		if pin.path == directory.path {
			return pin
		}
	}
	return nil
}

func (descriptor Descriptor) WriteAtomic(coverageRoot, taskID string, capabilities DescriptorCapabilities) (*OwnedDescriptor, error) {
	if err := validateDescriptorFields(descriptor); err != nil {
		return nil, err
	}
	coverageRoot, err := canonicalAbsoluteDirectory(coverageRoot)
	if err != nil {
		return nil, integrityError("coverage root", err)
	}
	if err := validateDescriptorCapabilities(coverageRoot, descriptor, capabilities); err != nil {
		return nil, err
	}
	if !validTaskID(taskID) {
		return nil, integrityError("task id", errors.New("invalid task id"))
	}
	taskRoot := filepath.Join(coverageRoot, taskID)
	if filepath.Clean(taskRoot) != taskRoot || !pathWithin(coverageRoot, taskRoot) {
		return nil, integrityError("task root", errors.New("task root escapes coverage root"))
	}
	coveragePin := directoryFinalPin(capabilities.CoverageRoot)
	if coveragePin == nil {
		return nil, integrityError("task root", errors.New("coverage root capability is not pinned"))
	}
	if err := mkdirPinnedChild(coveragePin, taskID, 0o700); err != nil {
		return nil, integrityError("task root", err)
	}
	if err := syncPinnedDirectory(coveragePin); err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, integrityError("task root sync", err)
	}
	cleanup := func() { _ = os.RemoveAll(taskRoot) }
	if err := capabilities.CoverageRoot.Verify(); err != nil {
		cleanup()
		return nil, integrityError("coverage root", err)
	}
	if !pathWithin(taskRoot, descriptor.OutputPath) || filepath.Dir(descriptor.OutputPath) != taskRoot {
		cleanup()
		return nil, integrityError("output path", errors.New("output must be a direct child of task root"))
	}
	taskRootCapability, err := NewVerifiedDirectoryFrom(capabilities.CoverageRoot, filepath.Base(taskRoot))
	if err != nil {
		cleanup()
		return nil, integrityError("task root capability", err)
	}
	closeTaskRoot := func() {
		// Never recursively clean a path after its retained capability has
		// changed; doing so could delete an attacker-controlled replacement.
		if err := taskRootCapability.Verify(); err == nil {
			cleanup()
		}
		_ = taskRootCapability.Close()
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		closeTaskRoot()
		return nil, integrityError("marshal descriptor", err)
	}
	raw = append(raw, '\n')
	taskPin := directoryFinalPin(taskRootCapability)
	if taskPin == nil {
		closeTaskRoot()
		return nil, integrityError("task root capability", errors.New("task root is not pinned"))
	}
	temporary, temporaryName, err := createPinnedTemp(taskPin, ".descriptor")
	if err != nil {
		closeTaskRoot()
		return nil, integrityError("create descriptor temporary", err)
	}
	temporaryPath := filepath.Join(taskRoot, temporaryName)
	removeTemporaryFile := func() {
		if err := taskRootCapability.Verify(); err == nil {
			_ = os.Remove(temporaryPath)
		}
	}
	removeTemporary := func() {
		_ = temporary.Close()
		removeTemporaryFile()
		closeTaskRoot()
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
		removeTemporaryFile()
		closeTaskRoot()
		return nil, integrityError("close descriptor temporary", err)
	}
	descriptorPath := filepath.Join(taskRoot, "descriptor.json")
	if err := renamePinnedChild(taskPin, temporaryName, "descriptor.json"); err != nil {
		removeTemporaryFile()
		closeTaskRoot()
		return nil, integrityError("publish descriptor", err)
	}
	if err := syncPinnedDirectory(taskPin); err != nil {
		closeTaskRoot()
		return nil, integrityError("publish descriptor sync", err)
	}
	path := descriptorPath
	descriptorFile, err := openDescriptorOutput(path)
	if err != nil {
		closeTaskRoot()
		cleanup()
		return nil, integrityError("open descriptor", err)
	}
	descriptorInfo, err := descriptorFile.Stat()
	if err != nil {
		_ = descriptorFile.Close()
		closeTaskRoot()
		return nil, integrityError("stat descriptor", err)
	}
	digest, err := digestFile(descriptorFile)
	if err != nil {
		_ = descriptorFile.Close()
		closeTaskRoot()
		return nil, integrityError("digest descriptor", err)
	}
	owned := &OwnedDescriptor{
		descriptor: descriptor,
		path:       path, root: coverageRoot, taskRoot: taskRoot,
		digest: digest, descriptorFile: descriptorFile,
		descriptorInfo: descriptorInfo, coverageRoot: capabilities.CoverageRoot,
		taskRootCapability: taskRootCapability,
		rootCapability:     capabilities.Root, objectCapability: capabilities.ObjectDirectory,
		gcovCapability: capabilities.GcovExecutable, provenance: capabilities.Provenance,
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

func (owned *OwnedDescriptor) VerifyOutputAfter() error {
	if owned == nil {
		return ErrDescriptorClosed
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	if owned.closed || owned.taskRootCapability == nil {
		return ErrDescriptorClosed
	}
	if err := owned.taskRootCapability.Verify(); err != nil {
		return err
	}
	path := owned.descriptor.OutputPath
	if !pathWithin(owned.taskRoot, path) || filepath.Dir(path) != owned.taskRoot {
		return ErrDescriptorIntegrity
	}
	if owned.outputFile == nil {
		lstat, err := os.Lstat(path)
		if err != nil || !lstat.Mode().IsRegular() || lstat.Mode()&os.ModeSymlink != 0 {
			if err == nil {
				err = errors.New("runner output is not a direct regular file")
			}
			return fmt.Errorf("%w: output identity: %v", ErrDescriptorIntegrity, err)
		}
		file, err := openDescriptorOutput(path)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(lstat, info) {
			_ = file.Close()
			if err == nil {
				err = errors.New("runner output identity changed while opening")
			}
			return fmt.Errorf("%w: output identity: %v", ErrDescriptorIntegrity, err)
		}
		owned.outputFile, owned.outputInfo = file, info
		digest, digestErr := digestFile(file)
		if digestErr != nil {
			_ = file.Close()
			owned.outputFile, owned.outputInfo = nil, nil
			return digestErr
		}
		owned.outputDigest = digest
		return nil
	}
	if owned.outputPin != nil {
		if err := owned.outputPin.verifyIdentity(); err != nil {
			return fmt.Errorf("%w: output identity: %v", ErrDescriptorIntegrity, err)
		}
	}
	if err := verifyFilePath(path, owned.outputInfo, owned.outputFile); err != nil {
		return err
	}
	digest, err := digestFile(owned.outputFile)
	if err != nil || digest != owned.outputDigest {
		if err == nil {
			err = errors.New("runner output digest changed")
		}
		return fmt.Errorf("%w: output digest: %v", ErrDescriptorIntegrity, err)
	}
	return nil
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
	if owned.closed || owned.descriptorFile == nil || owned.coverageRoot == nil || owned.taskRootCapability == nil || owned.rootCapability == nil || owned.objectCapability == nil || owned.gcovCapability == nil {
		return ErrDescriptorClosed
	}
	if err := owned.coverageRoot.Verify(); err != nil {
		return fmt.Errorf("%w: coverage root: %v", ErrDescriptorIntegrity, err)
	}
	if owned.taskRootCapability == nil || owned.taskRootCapability.Verify() != nil {
		return fmt.Errorf("%w: task root identity changed", ErrDescriptorIntegrity)
	}
	if err := owned.rootCapability.Verify(); err != nil {
		return fmt.Errorf("%w: root: %v", ErrDescriptorIntegrity, err)
	}
	if err := owned.objectCapability.Verify(); err != nil {
		return fmt.Errorf("%w: object directory: %v", ErrDescriptorIntegrity, err)
	}
	if err := owned.gcovCapability.Verify(); err != nil {
		return fmt.Errorf("%w: gcov executable: %v", ErrDescriptorIntegrity, err)
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
	if owned.outputFile != nil {
		if owned.outputPin == nil {
			closeErr = errors.Join(closeErr, owned.outputFile.Close())
		}
		owned.outputFile = nil
	}
	if owned.outputPin != nil {
		closeErr = errors.Join(closeErr, owned.outputPin.Close())
		owned.outputPin = nil
	}
	if owned.gcovCapability != nil {
		closeErr = errors.Join(closeErr, owned.gcovCapability.Close())
		owned.gcovCapability = nil
	}
	if owned.objectCapability != nil {
		closeErr = errors.Join(closeErr, owned.objectCapability.Close())
		owned.objectCapability = nil
	}
	if owned.rootCapability != nil {
		closeErr = errors.Join(closeErr, owned.rootCapability.Close())
		owned.rootCapability = nil
	}
	if owned.coverageRoot != nil {
		closeErr = errors.Join(closeErr, owned.coverageRoot.Close())
		owned.coverageRoot = nil
	}
	if owned.taskRootCapability != nil {
		closeErr = errors.Join(closeErr, owned.taskRootCapability.Close())
		owned.taskRootCapability = nil
	}
	if owned.provenance != nil {
		closeErr = errors.Join(closeErr, owned.provenance.Close())
		owned.provenance = nil
	}
	return errors.Join(verifyErr, closeErr)
}

func validateDescriptorCapabilities(coverageRoot string, descriptor Descriptor, capabilities DescriptorCapabilities) error {
	if capabilities.Provenance == nil || capabilities.CoverageRoot == nil || capabilities.Root == nil || capabilities.ObjectDirectory == nil || capabilities.GcovExecutable == nil {
		return integrityError("descriptor capabilities", errors.New("all verified capabilities are required"))
	}
	if capabilities.CoverageRoot.parent != capabilities.Provenance || capabilities.Root.parent != capabilities.Provenance || capabilities.ObjectDirectory.parent != capabilities.Provenance || capabilities.GcovExecutable.parent != capabilities.Provenance {
		return integrityError("descriptor capabilities", errors.New("capabilities lack common authorized provenance"))
	}
	if err := capabilities.Provenance.Verify(); err != nil {
		return integrityError("capability provenance", err)
	}
	if capabilities.CoverageRoot.Path() != coverageRoot || capabilities.Root.Path() != descriptor.Root ||
		capabilities.ObjectDirectory.Path() != descriptor.ObjectDirectory || capabilities.GcovExecutable.Path() != descriptor.GcovExecutable {
		return integrityError("descriptor capabilities", errors.New("capability paths do not match descriptor"))
	}
	if err := capabilities.CoverageRoot.Verify(); err != nil {
		return integrityError("coverage root capability", err)
	}
	if err := capabilities.Root.Verify(); err != nil {
		return integrityError("root capability", err)
	}
	if err := capabilities.ObjectDirectory.Verify(); err != nil {
		return integrityError("object directory capability", err)
	}
	if err := capabilities.GcovExecutable.Verify(); err != nil {
		return integrityError("gcov executable capability", err)
	}
	return nil
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
