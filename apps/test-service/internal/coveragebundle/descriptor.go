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
	"reflect"
	"runtime"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/serviceanchor"
)

var (
	ErrDescriptorIntegrity = errors.New("coverage descriptor integrity check failed")
	ErrDescriptorClosed    = errors.New("coverage descriptor is closed")

	// cleanupAuthorityPreflight is a narrow platform boundary. It is kept as a
	// variable so tests can prove that failure occurs before creating gcovr.
	cleanupAuthorityPreflight = preflightPinnedCleanupAuthority

	// descriptorPostPublication is a test seam for the error path after the
	// published descriptor has acquired its retained cleanup handle.
	descriptorPostPublication = func() error { return nil }

	// These narrow seams let failure-path tests prove cleanup failures are
	// returned to callers while the real retained operation still runs.
	acquireCleanupDirectoryPin = func(parent *pinnedObject, name string) (*pinnedObject, error) {
		return pinChildObjectWithDelete(parent, name, true, true)
	}
	writeDescriptorTemporary = func(file *os.File, contents []byte) (int, error) {
		return file.Write(contents)
	}
	removePinnedChildForCleanup = removePinnedChild
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
	if len(fields) != 5 {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("descriptor must contain exactly five fields"))
	}
	if _, ok := fields["schemaVersion"]; !ok {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("schemaVersion is required"))
	}
	var descriptor Descriptor
	encoded, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(encoded, &descriptor) != nil {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("invalid descriptor fields"))
	}
	if descriptor.SchemaVersion != 1 {
		return Descriptor{}, integrityError("descriptor JSON", errors.New("unsupported schema version"))
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

	descriptorFile       *os.File
	descriptorInfo       os.FileInfo
	descriptorPin        *pinnedObject
	collectorRoot        coverageplatform.DirectoryVerifier
	collectorCleanupRoot *VerifiedDirectory
	collectorChild       *pinnedObject
	taskRootCapability   *VerifiedDirectory
	rootCapability       coverageplatform.DirectoryVerifier
	objectCapability     coverageplatform.DirectoryVerifier
	gcovCapability       coveragerun.TrustedPath
	outputPin            *pinnedObject
	outputFile           *os.File
	outputInfo           os.FileInfo
	outputDigest         string
	closed               bool
}

// PinnedOutput is a consumer handle backed by the already-open output file.
// It never reopens the descriptor's pathname, preventing output ABA during
// normalization/consumption.
type PinnedOutput struct {
	descriptor *OwnedDescriptor
}

func (owned *OwnedDescriptor) Parse() (Descriptor, error) {
	if owned == nil {
		return Descriptor{}, ErrDescriptorClosed
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	if owned.closed || owned.descriptorFile == nil {
		return Descriptor{}, ErrDescriptorClosed
	}
	if _, err := owned.descriptorFile.Seek(0, io.SeekStart); err != nil {
		return Descriptor{}, err
	}
	contents, err := io.ReadAll(owned.descriptorFile)
	if err != nil {
		return Descriptor{}, err
	}
	return ParseDescriptor(contents)
}

func (output *PinnedOutput) ReadAll() ([]byte, error) {
	if output == nil || output.descriptor == nil {
		return nil, ErrDescriptorClosed
	}
	d := output.descriptor
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.verifyOutputAfterLocked(); err != nil {
		return nil, err
	}
	if _, err := d.outputFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(d.outputFile)
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
	ownsParent bool
	authority  *serviceanchor.Anchor
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
	CollectorRoot   coverageplatform.DirectoryVerifier
	Root            coverageplatform.DirectoryVerifier
	ObjectDirectory coverageplatform.DirectoryVerifier
	GcovExecutable  coveragerun.TrustedPath
}

func NewVerifiedDirectory(path string) (*VerifiedDirectory, error) {
	return nil, errors.New("verified directory requires service authority")
}

// newVerifiedDirectory is intentionally unexported: only an authority-bound
// constructor may mint a service path capability.
func newVerifiedDirectory(path string) (*VerifiedDirectory, error) {
	if err := validateAbsolutePath(path); err != nil {
		return nil, err
	}
	identities, pins, err := capturePathIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("capture verified directory: %w", err)
	}
	return &VerifiedDirectory{path: path, identities: identities, pins: pins}, nil
}

// NewVerifiedDirectoryFromAnchor derives a capability from the
// service-owned anchor and a relative descendant. Bare absolute paths cannot
// mint capabilities.
func NewVerifiedDirectoryFromAnchor(anchor serviceanchor.Anchor, relative string) (*VerifiedDirectory, error) {
	root := anchor.Root()
	if err := anchor.Verify(root); err != nil {
		return nil, err
	}
	if relative == "" || relative == "." {
		rootCapability, err := newVerifiedDirectory(root)
		if err != nil {
			return nil, err
		}
		rootCapability.authority = &anchor
		return rootCapability, nil
	}
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("invalid relative verified directory")
	}
	parent, err := newVerifiedDirectory(root)
	if err != nil {
		return nil, err
	}
	child, err := NewVerifiedDirectoryFrom(parent, relative)
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	child.authority = &anchor
	child.ownsParent = true
	return child, nil
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
	return &VerifiedDirectory{path: path, identities: identities, pins: pins, parent: parent, authority: parent.authority}, nil
}

func NewVerifiedExecutable(authorizedRoot, path string) (*VerifiedExecutable, error) {
	return nil, errors.New("verified executable requires service authority")
}

// NewVerifiedExecutableFromAnchor derives an executable from the
// authority anchor and a relative path. The returned executable owns the
// complete retained directory chain.
func NewVerifiedExecutableFromAnchor(anchor serviceanchor.Anchor, relative string) (*VerifiedExecutable, error) {
	if filepath.IsAbs(relative) || relative == "" || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("invalid relative verified executable")
	}
	directory := filepath.Dir(relative)
	if directory == "." {
		directory = "."
	}
	root, err := NewVerifiedDirectoryFromAnchor(anchor, directory)
	if err != nil {
		return nil, err
	}
	executable, err := NewVerifiedExecutableFrom(root, filepath.Base(relative))
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	executable.ownsRoot = true
	return executable, nil
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
	ownsRoot := dirCapability != parent
	cleanupNested := func() {
		if ownsRoot {
			_ = dirCapability.Close()
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
		cleanupNested()
		return nil, ErrDescriptorIntegrity
	}
	filePin, err := pinChildObject(parentPin, filepath.Base(relative), false)
	dirCapability.mu.Unlock()
	if err != nil {
		cleanupNested()
		return nil, err
	}
	path := filepath.Join(base, relative)
	file, err := openDescriptorOutput(path)
	if err != nil {
		_ = filePin.Close()
		cleanupNested()
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil || !os.SameFile(filePin.identity, info) {
		_ = file.Close()
		_ = filePin.Close()
		cleanupNested()
		if statErr == nil {
			statErr = errors.New("executable identity changed while opening")
		}
		return nil, statErr
	}
	_ = filePin.Close()
	digest, err := digestFile(file)
	if err != nil {
		_ = file.Close()
		cleanupNested()
		return nil, err
	}
	return &VerifiedExecutable{path: path, root: dirCapability, parent: parent, ownsRoot: ownsRoot, file: file, info: info, digest: digest}, nil
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
		if err := pin.verifyIdentity(); err != nil {
			return fmt.Errorf("%w: verified directory %s: %v", ErrDescriptorIntegrity, pin.path, err)
		}
	}
	pinnedPaths := make(map[string]struct{}, len(directory.pins))
	for _, pin := range directory.pins {
		pinnedPaths[pin.path] = struct{}{}
	}
	for parent := directory.parent; parent != nil; parent = parent.parent {
		parent.mu.Lock()
		for _, pin := range parent.pins {
			pinnedPaths[pin.path] = struct{}{}
		}
		parent.mu.Unlock()
	}
	for _, identity := range directory.identities {
		info, err := os.Lstat(identity.path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			if err == nil {
				err = errors.New("directory is no longer a direct directory")
			}
			return fmt.Errorf("%w: verified directory %s: %v", ErrDescriptorIntegrity, identity.path, err)
		}
		if _, pinned := pinnedPaths[identity.path]; !pinned {
			return fmt.Errorf("%w: ancestor %s has no retained pin", ErrDescriptorIntegrity, identity.path)
		}
		// The retained component handle is the authority; pathname Stat is not
		// used as a second, TOCTOU-prone identity source.
	}
	return nil
}

// RetainDirectory returns an independently owned pin for this exact direct
// directory. It deliberately does not transfer or close the source
// capability, which may be a caller-owned Build Boundary view.
func (directory *VerifiedDirectory) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	if directory == nil || directory.Verify() != nil {
		return nil, ErrDescriptorClosed
	}
	path := directory.Path()
	retained, err := newVerifiedDirectory(path)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (coverageplatform.RetainedDirectory, error) {
		_ = retained.Close()
		return nil, cause
	}
	if retained.Path() != path || directory.Verify() != nil || retained.Verify() != nil {
		return fail(ErrDescriptorIntegrity)
	}
	return retained, nil
}

func (directory *VerifiedDirectory) Close() error {
	if directory == nil {
		return nil
	}
	directory.mu.Lock()
	directory.closed = true
	pins := directory.pins
	parent := directory.parent
	ownsParent := directory.ownsParent
	directory.pins = nil
	directory.parent = nil
	directory.ownsParent = false
	directory.mu.Unlock()
	var result error
	for index := len(pins) - 1; index >= 0; index-- {
		result = errors.Join(result, pins[index].Close())
	}
	if ownsParent && parent != nil {
		result = errors.Join(result, parent.Close())
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
			return fail(fmt.Errorf("path component %s cannot be strictly pinned: %w", current, pinErr))
		}
		pins = append(pins, pinned)
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

func (descriptor Descriptor) WriteAtomic(capabilities DescriptorCapabilities) (*OwnedDescriptor, error) {
	if err := validateDescriptorFields(descriptor); err != nil {
		return nil, err
	}
	coverageRoot, err := directoryCapabilityPath(capabilities.CollectorRoot)
	if err != nil {
		return nil, integrityError("collector root capability", err)
	}
	if err := validateDescriptorCapabilities(coverageRoot, descriptor, capabilities); err != nil {
		return nil, err
	}
	rootCapability, err := retainInternalDirectory(capabilities.Root, descriptor.Root)
	if err != nil {
		return nil, integrityError("root capability", err)
	}
	objectCapability, err := retainInternalDirectory(capabilities.ObjectDirectory, descriptor.ObjectDirectory)
	if err != nil {
		_ = rootCapability.Close()
		return nil, integrityError("object directory capability", err)
	}
	collectorRoot, err := retainInternalDirectory(capabilities.CollectorRoot, coverageRoot)
	if err != nil {
		_ = objectCapability.Close()
		_ = rootCapability.Close()
		return nil, integrityError("collector root", err)
	}
	closeRetainedInputs := func() error {
		return errors.Join(collectorRoot.Close(), objectCapability.Close(), rootCapability.Close())
	}
	taskRoot := filepath.Join(coverageRoot, "gcovr")
	coveragePin := directoryFinalPin(collectorRoot)
	if coveragePin == nil {
		_ = closeRetainedInputs()
		return nil, integrityError("collector root", errors.New("collector root capability is not pinned"))
	}
	if !cleanupAuthorityAvailable() {
		_ = closeRetainedInputs()
		return nil, integrityError("collector cleanup authority", errors.New("platform cannot prove identity-bound task cleanup before creation"))
	}
	if err := cleanupAuthorityPreflight(collectorRoot, coveragePin); err != nil {
		_ = closeRetainedInputs()
		return nil, integrityError("collector cleanup authority", err)
	}
	if _, err := os.Lstat(taskRoot); err == nil || !errors.Is(err, os.ErrNotExist) {
		_ = closeRetainedInputs()
		return nil, integrityError("collector child", errors.New("gcovr child already exists"))
	}
	if err := mkdirPinnedChild(coveragePin, "gcovr", 0o700); err != nil {
		_ = closeRetainedInputs()
		return nil, integrityError("collector child", err)
	}
	creationChild, err := pinChildObject(coveragePin, "gcovr", true)
	if err != nil {
		return nil, integrityError("collector child identity", errors.Join(err, closeRetainedInputs()))
	}
	removeFreshCreatedChild := func() error {
		return errors.Join(removeFreshPinnedDirectory(coveragePin, "gcovr", creationChild), creationChild.Close(), syncPinnedDirectory(coveragePin))
	}
	cleanupChild, err := acquireCleanupDirectoryPin(coveragePin, "gcovr")
	if err != nil {
		return nil, integrityError("collector child cleanup pin", errors.Join(err, removeFreshCreatedChild(), closeRetainedInputs()))
	}
	removeCreatedChild := func() error {
		return errors.Join(removePinnedChildForCleanup(coveragePin, cleanupChild, "gcovr"), cleanupChild.Close())
	}
	if err := creationChild.Close(); err != nil {
		return nil, integrityError("collector child creation pin", errors.Join(err, removeCreatedChild(), closeRetainedInputs()))
	}
	if err := syncPinnedDirectory(coveragePin); err != nil {
		cleanupErr := removeCreatedChild()
		return nil, integrityError("collector child sync", errors.Join(err, cleanupErr, closeRetainedInputs()))
	}
	taskRootCapability, err := NewVerifiedDirectoryFrom(collectorRoot, "gcovr")
	if err != nil {
		cleanupErr := removeCreatedChild()
		return nil, integrityError("task root capability", errors.Join(err, cleanupErr, closeRetainedInputs()))
	}
	taskPin := directoryFinalPin(taskRootCapability)
	if taskPin == nil {
		_ = taskRootCapability.Close()
		cleanupErr := removeCreatedChild()
		return nil, integrityError("task root capability", errors.Join(errors.New("task root is not pinned"), cleanupErr, closeRetainedInputs()))
	}
	var temporaryName string
	var descriptorCleanupPin *pinnedObject
	closeTaskRoot := func() error {
		cleanupErr := cleanupDescriptorTaskRoot(collectorRoot, taskRootCapability, cleanupChild, descriptorCleanupPin, nil)
		return errors.Join(cleanupErr, taskRootCapability.Close(), cleanupChild.Close(), closeRetainedInputs())
	}
	if err := validateDescriptorCapabilities(coverageRoot, descriptor, capabilities); err != nil {
		return nil, integrityError("collector root", errors.Join(err, closeTaskRoot()))
	}
	if !pathWithin(taskRoot, descriptor.OutputPath) || filepath.Dir(descriptor.OutputPath) != taskRoot {
		return nil, integrityError("output path", errors.Join(errors.New("output must be a direct child of task root"), closeTaskRoot()))
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, integrityError("marshal descriptor", errors.Join(err, closeTaskRoot()))
	}
	raw = append(raw, '\n')
	temporary, temporaryName, err := createPinnedTemp(taskPin, ".descriptor")
	if err != nil {
		return nil, integrityError("create descriptor temporary", errors.Join(err, closeTaskRoot()))
	}
	temporaryCleanupPin, err := duplicatePinnedTemporary(taskPin, temporary, temporaryName)
	if err != nil {
		cleanupErr := removeCreatedTemporary(taskPin, temporary, temporaryName)
		closeErr := temporary.Close()
		return nil, integrityError("retain descriptor temporary", errors.Join(err, cleanupErr, closeErr, closeTaskRoot()))
	}
	removeTemporaryFile := func() error {
		return errors.Join(removePinnedChildForCleanup(taskPin, temporaryCleanupPin, temporaryName), temporaryCleanupPin.Close())
	}
	removeTemporary := func() error {
		return errors.Join(temporary.Close(), removeTemporaryFile(), closeTaskRoot())
	}
	if _, err := writeDescriptorTemporary(temporary, raw); err != nil {
		return nil, integrityError("write descriptor", errors.Join(err, removeTemporary()))
	}
	if err := temporary.Sync(); err != nil {
		return nil, integrityError("sync descriptor", errors.Join(err, removeTemporary()))
	}
	if err := temporary.Close(); err != nil {
		return nil, integrityError("close descriptor temporary", errors.Join(err, removeTemporaryFile(), closeTaskRoot()))
	}
	descriptorPath := filepath.Join(taskRoot, "descriptor.json")
	if err := renamePinnedChild(taskPin, temporaryName, "descriptor.json"); err != nil {
		return nil, integrityError("publish descriptor", errors.Join(err, removeTemporaryFile(), closeTaskRoot()))
	}
	// The retained temporary handle was acquired while the creation handle was
	// still open. After the relative rename it is the exact descriptor identity
	// used for cleanup; no mutable descriptor pathname is reopened.
	temporaryCleanupPin.path = descriptorPath
	descriptorCleanupPin = temporaryCleanupPin
	if err := temporaryCleanupPin.verifyIdentity(); err != nil {
		return nil, integrityError("retain published descriptor", errors.Join(err, closeTaskRoot()))
	}
	if err := descriptorPostPublication(); err != nil {
		return nil, integrityError("post-publication descriptor", errors.Join(err, closeTaskRoot()))
	}
	if err := syncPinnedDirectory(taskPin); err != nil {
		return nil, integrityError("publish descriptor sync", errors.Join(err, closeTaskRoot()))
	}
	path := descriptorPath
	descriptorFile, err := openDescriptorOutput(path)
	if err != nil {
		return nil, integrityError("open descriptor", errors.Join(err, closeTaskRoot()))
	}
	descriptorInfo, err := descriptorFile.Stat()
	if err != nil {
		_ = descriptorFile.Close()
		return nil, integrityError("stat descriptor", errors.Join(err, closeTaskRoot()))
	}
	digest, err := digestFile(descriptorFile)
	if err != nil {
		_ = descriptorFile.Close()
		return nil, integrityError("digest descriptor", errors.Join(err, closeTaskRoot()))
	}
	owned := &OwnedDescriptor{
		descriptor: descriptor,
		path:       path, root: coverageRoot, taskRoot: taskRoot,
		digest: digest, descriptorFile: descriptorFile,
		descriptorInfo: descriptorInfo, descriptorPin: descriptorCleanupPin, collectorRoot: capabilities.CollectorRoot, collectorCleanupRoot: collectorRoot, collectorChild: cleanupChild,
		taskRootCapability: taskRootCapability,
		rootCapability:     rootCapability, objectCapability: objectCapability,
		gcovCapability: capabilities.GcovExecutable,
	}
	if err := owned.Verify(); err != nil {
		return nil, errors.Join(err, owned.Close())
	}
	return owned, nil
}

// retainInternalDirectory accepts only the narrow retained-capability contract,
// then mints a coveragebundle-local capability after both the producer's
// retained clone and the new local pin have independently verified the exact
// same path. The producer's view is never closed here.
func retainInternalDirectory(source coverageplatform.DirectoryVerifier, expected string) (*VerifiedDirectory, error) {
	retained, err := coverageplatform.RetainDirectory(source)
	if err != nil {
		return nil, err
	}
	if retained.Path() != expected || retained.Verify() != nil || source.Path() != expected || source.Verify() != nil {
		return nil, errors.Join(coverageplatform.ErrInvalidCapability, retained.Close())
	}
	local, err := newVerifiedDirectory(expected)
	if err != nil {
		return nil, errors.Join(err, retained.Close())
	}
	fail := func(cause error) (*VerifiedDirectory, error) {
		return nil, errors.Join(cause, local.Close(), retained.Close())
	}
	if local.Path() != expected || local.Verify() != nil || retained.Verify() != nil || source.Verify() != nil || retained.Path() != expected || source.Path() != expected {
		return fail(coverageplatform.ErrInvalidCapability)
	}
	if err := retained.Close(); err != nil {
		return nil, errors.Join(err, local.Close())
	}
	return local, nil
}

// cleanupDescriptorTaskRoot removes only the task-private direct children
// pinned from the retained task root, then removes that exact pinned root from
// its retained parent. Any identity change, unexpected entry, or deletion
// failure stops cleanup and is returned to the caller; it never falls back to
// a pathname walk or recursive deletion.
func cleanupDescriptorTaskRoot(collectorRoot, taskRoot *VerifiedDirectory, cleanupChild, descriptorChild, outputChild *pinnedObject) error {
	if collectorRoot == nil || taskRoot == nil || cleanupChild == nil {
		return ErrDescriptorClosed
	}
	if err := collectorRoot.Verify(); err != nil {
		return fmt.Errorf("verify cleanup collector root: %w", err)
	}
	if err := taskRoot.Verify(); err != nil {
		return fmt.Errorf("verify cleanup task root: %w", err)
	}
	collectorPin := directoryFinalPin(collectorRoot)
	taskPin := directoryFinalPin(taskRoot)
	if collectorPin == nil || taskPin == nil {
		return ErrDescriptorIntegrity
	}
	var result error
	for _, child := range []*pinnedObject{descriptorChild, outputChild} {
		if child == nil {
			continue
		}
		name := filepath.Base(child.path)
		if child.path != filepath.Join(taskPin.path, name) || name == "." || name == ".." {
			result = errors.Join(result, fmt.Errorf("invalid retained cleanup child %q", child.path))
			continue
		}
		if err := child.verifyIdentity(); err != nil {
			result = errors.Join(result, fmt.Errorf("verify cleanup child %q: %w", name, err))
			continue
		}
		if err := removePinnedChildForCleanup(taskPin, child, name); err != nil {
			result = errors.Join(result, fmt.Errorf("remove cleanup child %q: %w", name, err))
		}
		if err := child.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close cleanup child %q: %w", name, err))
		}
	}
	if result != nil {
		return result
	}
	if err := forEachPinnedDirectoryEntry(taskPin, func(entry os.DirEntry) error {
		return fmt.Errorf("unowned descriptor task child %q", entry.Name())
	}); err != nil {
		return err
	}
	if err := cleanupChild.verifyIdentity(); err != nil {
		return fmt.Errorf("verify task cleanup child: %w", err)
	}
	if err := removePinnedChildForCleanup(collectorPin, cleanupChild, "gcovr"); err != nil {
		return fmt.Errorf("remove task cleanup child: %w", err)
	}
	if err := syncPinnedDirectory(collectorPin); err != nil {
		return fmt.Errorf("sync collector after cleanup: %w", err)
	}
	return nil
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
	return owned.verifyOutputAfterLocked()
}

func (owned *OwnedDescriptor) verifyOutputAfterLocked() error {
	if owned.closed || owned.taskRootCapability == nil {
		return ErrDescriptorClosed
	}
	if err := owned.verifyLocked(); err != nil {
		return err
	}
	if err := owned.taskRootCapability.Verify(); err != nil {
		return err
	}
	path := owned.descriptor.OutputPath
	if !pathWithin(owned.taskRoot, path) || filepath.Dir(path) != owned.taskRoot {
		return ErrDescriptorIntegrity
	}
	if owned.outputFile == nil {
		var taskPin *pinnedObject
		for _, pin := range owned.taskRootCapability.pins {
			if pin.path == owned.taskRoot {
				taskPin = pin
				break
			}
		}
		if taskPin == nil {
			return ErrDescriptorIntegrity
		}
		outputPin, err := pinOutputChild(taskPin, filepath.Base(path))
		if err != nil {
			return fmt.Errorf("%w: output identity: %v", ErrDescriptorIntegrity, err)
		}
		if outputPin.identity == nil || !outputPin.identity.Mode().IsRegular() || outputPin.identity.Mode()&os.ModeSymlink != 0 {
			_ = outputPin.Close()
			return fmt.Errorf("%w: output is not a direct regular file", ErrDescriptorIntegrity)
		}
		owned.outputPin, owned.outputFile, owned.outputInfo = outputPin, outputPin.file, outputPin.identity
		digest, digestErr := digestFile(outputPin.file)
		if digestErr != nil {
			_ = outputPin.Close()
			owned.outputPin, owned.outputFile, owned.outputInfo = nil, nil, nil
			return digestErr
		}
		owned.outputDigest = digest
		if err := owned.taskRootCapability.Verify(); err != nil {
			_ = outputPin.Close()
			owned.outputPin, owned.outputFile, owned.outputInfo, owned.outputDigest = nil, nil, nil, ""
			return err
		}
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

func (owned *OwnedDescriptor) PinnedOutput() (*PinnedOutput, error) {
	if owned == nil {
		return nil, ErrDescriptorClosed
	}
	owned.mu.Lock()
	defer owned.mu.Unlock()
	if err := owned.verifyOutputAfterLocked(); err != nil {
		return nil, err
	}
	return &PinnedOutput{descriptor: owned}, nil
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
	if owned.closed || owned.descriptorFile == nil || owned.collectorRoot == nil || owned.collectorChild == nil || owned.taskRootCapability == nil || owned.rootCapability == nil || owned.objectCapability == nil || owned.gcovCapability == nil {
		return ErrDescriptorClosed
	}
	if err := validateOwnedCapabilities(owned); err != nil {
		return err
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
	if verifyErr != nil {
		verifyErr = fmt.Errorf("verify before descriptor close: %w", verifyErr)
	}
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
		// Keep the original output handle through cleanup; reopening the current
		// pathname here could delete a replacement written after verification.
	}
	if owned.collectorCleanupRoot != nil && owned.taskRootCapability != nil {
		closeErr = errors.Join(closeErr, cleanupDescriptorTaskRoot(
			owned.collectorCleanupRoot, owned.taskRootCapability, owned.collectorChild, owned.descriptorPin, owned.outputPin,
		))
	}
	if owned.descriptorPin != nil {
		closeErr = errors.Join(closeErr, owned.descriptorPin.Close())
		owned.descriptorPin = nil
	}
	if owned.outputPin != nil {
		closeErr = errors.Join(closeErr, owned.outputPin.Close())
		owned.outputPin = nil
	}
	if owned.taskRootCapability != nil {
		if err := owned.taskRootCapability.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close task root capability: %w", err))
		}
		owned.taskRootCapability = nil
	}
	if owned.collectorCleanupRoot != nil {
		if err := owned.collectorCleanupRoot.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close cleanup collector root: %w", err))
		}
		owned.collectorCleanupRoot = nil
	}
	if owned.collectorChild != nil {
		if err := owned.collectorChild.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close task cleanup child: %w", err))
		}
		owned.collectorChild = nil
	}
	owned.collectorRoot = nil
	if owned.rootCapability != nil {
		if closer, ok := owned.rootCapability.(interface{ Close() error }); ok {
			closeErr = errors.Join(closeErr, closer.Close())
		}
	}
	owned.rootCapability = nil
	if owned.objectCapability != nil {
		if closer, ok := owned.objectCapability.(interface{ Close() error }); ok {
			closeErr = errors.Join(closeErr, closer.Close())
		}
	}
	owned.objectCapability = nil
	owned.gcovCapability = nil
	return errors.Join(verifyErr, closeErr)
}

func validateDescriptorCapabilities(coverageRoot string, descriptor Descriptor, capabilities DescriptorCapabilities) error {
	collectorRoot, err := directoryCapabilityPath(capabilities.CollectorRoot)
	if err != nil {
		return integrityError("collector root capability", err)
	}
	root, err := directoryCapabilityPath(capabilities.Root)
	if err != nil {
		return integrityError("root capability", err)
	}
	objects, err := directoryCapabilityPath(capabilities.ObjectDirectory)
	if err != nil {
		return integrityError("object directory capability", err)
	}
	gcov, err := trustedCapabilityPath(capabilities.GcovExecutable)
	if err != nil {
		return integrityError("gcov executable capability", err)
	}
	if collectorRoot != coverageRoot || root != descriptor.Root || objects != descriptor.ObjectDirectory || gcov != descriptor.GcovExecutable {
		return integrityError("descriptor capabilities", errors.New("capability paths do not match descriptor"))
	}
	if err := coverageplatform.VerifyDirectory(capabilities.CollectorRoot); err != nil {
		return integrityError("collector root capability", err)
	}
	if err := coverageplatform.VerifyDirectory(capabilities.Root); err != nil {
		return integrityError("root capability", err)
	}
	if err := coverageplatform.VerifyDirectory(capabilities.ObjectDirectory); err != nil {
		return integrityError("object directory capability", err)
	}
	if err := verifyTrustedCapability(capabilities.GcovExecutable); err != nil {
		return integrityError("gcov executable capability", err)
	}
	if descriptor.OutputPath != filepath.Join(coverageRoot, "gcovr", "coverage.json") {
		return integrityError("output path", errors.New("output must be collector/gcovr/coverage.json"))
	}
	return nil
}

func verifyTrustedCapability(value coveragerun.TrustedPath) error {
	if _, err := trustedCapabilityPath(value); err != nil {
		return err
	}
	return value.Verify()
}

func directoryCapabilityPath(value coverageplatform.DirectoryVerifier) (string, error) {
	if interfaceNil(value) {
		return "", coverageplatform.ErrInvalidCapability
	}
	path := value.Path()
	if path == "" {
		return "", coverageplatform.ErrInvalidCapability
	}
	return path, nil
}

func trustedCapabilityPath(value coveragerun.TrustedPath) (string, error) {
	if interfaceNil(value) {
		return "", coverageplatform.ErrInvalidCapability
	}
	path := value.Path()
	if path == "" {
		return "", coverageplatform.ErrInvalidCapability
	}
	return path, nil
}

func interfaceNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func validateOwnedCapabilities(owned *OwnedDescriptor) error {
	collectorRoot, err := directoryCapabilityPath(owned.collectorRoot)
	if err != nil || collectorRoot != owned.root {
		return fmt.Errorf("%w: collector root", ErrDescriptorIntegrity)
	}
	root, err := directoryCapabilityPath(owned.rootCapability)
	if err != nil || root != owned.descriptor.Root {
		return fmt.Errorf("%w: root", ErrDescriptorIntegrity)
	}
	objects, err := directoryCapabilityPath(owned.objectCapability)
	if err != nil || objects != owned.descriptor.ObjectDirectory {
		return fmt.Errorf("%w: object directory", ErrDescriptorIntegrity)
	}
	gcov, err := trustedCapabilityPath(owned.gcovCapability)
	if err != nil || gcov != owned.descriptor.GcovExecutable {
		return fmt.Errorf("%w: gcov executable", ErrDescriptorIntegrity)
	}
	if err := coverageplatform.VerifyDirectory(owned.collectorRoot); err != nil {
		return fmt.Errorf("%w: collector root: %v", ErrDescriptorIntegrity, err)
	}
	if err := coverageplatform.VerifyDirectory(owned.rootCapability); err != nil {
		return fmt.Errorf("%w: root: %v", ErrDescriptorIntegrity, err)
	}
	if err := coverageplatform.VerifyDirectory(owned.objectCapability); err != nil {
		return fmt.Errorf("%w: object directory: %v", ErrDescriptorIntegrity, err)
	}
	if err := verifyTrustedCapability(owned.gcovCapability); err != nil {
		return fmt.Errorf("%w: gcov executable: %v", ErrDescriptorIntegrity, err)
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
