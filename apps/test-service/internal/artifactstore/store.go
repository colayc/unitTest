package artifactstore

import (
	"bytes"
	"context"
	"crypto/rand"
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
	root  *os.Root
	hooks storeHooks
}

type storeHooks struct {
	afterVerifiedSnapshot func()
	afterSnapshotRead     func(position int64)
	afterTempSync         func()
	beforePublish         func(temporaryName string)
	afterPublishLink      func(targetName string)
	finalizeDirectory     func(stage directoryFinalizeStage) error
	beforeTempRemove      func(temporaryName string)
	beforeCleanupExecute  func()
	beforeCleanupRemove   func(relative string)
}

type directoryFinalizeStage string

const (
	directoryFinalizePublished        directoryFinalizeStage = "published"
	directoryFinalizeTemporaryRemoved directoryFinalizeStage = "temporary-removed"
	directoryFinalizeRollback         directoryFinalizeStage = "rollback"
)

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
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, ErrStoreUnavailable
	}
	before, err := os.Lstat(absolute)
	if err != nil || !before.IsDir() {
		return nil, ErrStoreUnavailable
	}
	pinned, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, ErrStoreUnavailable
	}
	fail := func(result error) (*Store, error) {
		_ = pinned.Close()
		return nil, result
	}
	opened, err := pinned.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		return fail(ErrUnsafePath)
	}
	after, err := os.Lstat(absolute)
	if err != nil || isLinkInfo(after) || !os.SameFile(after, opened) {
		return fail(ErrUnsafePath)
	}
	return &Store{root: pinned}, nil
}

func (s *Store) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	if err := s.root.Close(); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func (s *Store) CommitJSON(ctx context.Context, taskID, artifactID string, at time.Time, value any) (artifact task.Artifact, resultErr error) {
	if err := ctx.Err(); err != nil {
		return task.Artifact{}, err
	}
	if !validGeneratedID(taskID) || !validGeneratedID(artifactID) || at.IsZero() {
		return task.Artifact{}, ErrInvalidArtifact
	}
	data, err := canonicalSummary(taskID, value)
	if err != nil {
		return task.Artifact{}, err
	}
	return s.commitArtifactData(ctx, taskID, artifactID, "task-summary", at, data)
}

func (s *Store) commitArtifactData(
	ctx context.Context,
	taskID string,
	artifactID string,
	kind string,
	at time.Time,
	data []byte,
) (artifact task.Artifact, resultErr error) {
	artifact, capability, err := s.commitArtifactDataRetained(
		ctx, taskID, artifactID, kind, at, data,
	)
	if capability != nil {
		err = errors.Join(err, capability.close())
	}
	if err != nil {
		return task.Artifact{}, err
	}
	return artifact, nil
}

type finalizedArtifactCapability struct {
	store          *Store
	artifact       task.Artifact
	parentRelative string
	targetName     string
	parent         *os.Root
	parentIdentity os.FileInfo
	file           *os.File
	fileIdentity   os.FileInfo
}

func (s *Store) commitArtifactDataRetained(
	ctx context.Context,
	taskID string,
	artifactID string,
	kind string,
	at time.Time,
	data []byte,
) (artifact task.Artifact, capability *finalizedArtifactCapability, resultErr error) {
	if s == nil || s.root == nil || ctx == nil {
		return task.Artifact{}, nil, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return task.Artifact{}, nil, err
	}
	mimeType, extension, ok := artifactDescriptor(kind)
	if !validGeneratedID(taskID) || !validGeneratedID(artifactID) || at.IsZero() ||
		!ok {
		return task.Artifact{}, nil, ErrInvalidArtifact
	}
	relative := artifactRelativePathFor(taskID, artifactID, extension)
	parentRelative := path.Dir(relative)
	if err := validateRootPath(s.root, parentRelative, true); err != nil {
		return task.Artifact{}, nil, err
	}
	if err := s.root.MkdirAll(nativePath(parentRelative), 0o700); err != nil {
		return task.Artifact{}, nil, rootOperationError(err)
	}
	parent, parentIdentity, err := openVerifiedRoot(s.root, parentRelative)
	if err != nil {
		return task.Artifact{}, nil, err
	}
	parentTransferred := false
	defer func() {
		if !parentTransferred {
			if err := parent.Close(); err != nil && resultErr == nil {
				artifact = task.Artifact{}
				capability = nil
				resultErr = ErrStoreUnavailable
			}
		}
	}()

	temporaryName, temporary, err := createTemporary(parent)
	if err != nil {
		return task.Artifact{}, nil, err
	}
	temporaryPresent := true
	temporaryOpen := true
	defer func() {
		if temporaryOpen {
			if err := temporary.Close(); err != nil && resultErr == nil {
				artifact = task.Artifact{}
				capability = nil
				resultErr = ErrStoreUnavailable
			}
		}
		if temporaryPresent {
			removeErr := parent.Remove(temporaryName)
			if removeErr != nil {
				if _, statErr := parent.Lstat(temporaryName); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
					artifact = task.Artifact{}
					capability = nil
					resultErr = ErrStoreUnavailable
				}
			}
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return task.Artifact{}, nil, ErrStoreUnavailable
	}
	if err := temporary.Sync(); err != nil {
		return task.Artifact{}, nil, ErrStoreUnavailable
	}
	if s.hooks.afterTempSync != nil {
		s.hooks.afterTempSync()
	}

	if s.hooks.beforePublish != nil {
		s.hooks.beforePublish(temporaryName)
	}
	if !rootIdentityMatches(s.root, parentRelative, parentIdentity) {
		return task.Artifact{}, nil, ErrUnsafePath
	}
	if err := ctx.Err(); err != nil {
		return task.Artifact{}, nil, err
	}
	temporaryInfo, temporaryErr := temporary.Stat()
	if temporaryErr != nil || !temporaryInfo.Mode().IsRegular() {
		return task.Artifact{}, nil, ErrUnsafePath
	}
	temporaryPathInfo, err := parent.Lstat(temporaryName)
	if err != nil || isLinkInfo(temporaryPathInfo) ||
		!temporaryPathInfo.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, temporaryPathInfo) {
		return task.Artifact{}, nil, ErrUnsafePath
	}
	targetName := path.Base(relative)
	if err := parent.Link(temporaryName, targetName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return task.Artifact{}, nil, ErrInvalidArtifact
		}
		return task.Artifact{}, nil, rootOperationError(err)
	}
	published := true
	publishedIdentity := temporaryInfo
	if s.hooks.afterPublishLink != nil {
		s.hooks.afterPublishLink(targetName)
	}
	finalize := func(stage directoryFinalizeStage) error {
		if s.hooks.finalizeDirectory != nil {
			if err := s.hooks.finalizeDirectory(stage); err != nil {
				return ErrStoreUnavailable
			}
		}
		return syncRootDirectory(parent)
	}
	rollback := func() error {
		if !published {
			return nil
		}
		if publishedIdentity != nil {
			current, err := parent.Lstat(targetName)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					published = false
					return finalize(directoryFinalizeRollback)
				}
				return rootOperationError(err)
			}
			if isLinkInfo(current) || !current.Mode().IsRegular() || !os.SameFile(current, publishedIdentity) {
				return ErrUnsafePath
			}
		}
		if err := parent.Remove(targetName); err != nil {
			return rootOperationError(err)
		}
		published = false
		return finalize(directoryFinalizeRollback)
	}
	failPublished := func(cause error) (task.Artifact, *finalizedArtifactCapability, error) {
		if rollbackErr := rollback(); rollbackErr != nil {
			return task.Artifact{}, nil, rollbackErr
		}
		return task.Artifact{}, nil, cause
	}

	if !rootIdentityMatches(s.root, parentRelative, parentIdentity) {
		return failPublished(ErrUnsafePath)
	}
	final, finalInfo, err := openVerifiedFile(parent, targetName)
	if err != nil {
		return failPublished(err)
	}
	finalTransferred := false
	defer func() {
		if !finalTransferred {
			if err := final.Close(); err != nil && resultErr == nil {
				artifact = task.Artifact{}
				capability = nil
				resultErr = ErrStoreUnavailable
			}
		}
	}()
	if !finalInfo.Mode().IsRegular() || !os.SameFile(temporaryInfo, finalInfo) {
		return failPublished(ErrUnsafePath)
	}
	if err := finalize(directoryFinalizePublished); err != nil {
		return failPublished(err)
	}
	if s.hooks.beforeTempRemove != nil {
		s.hooks.beforeTempRemove(temporaryName)
	}
	if err := parent.Remove(temporaryName); err != nil {
		return failPublished(ErrStoreUnavailable)
	}
	temporaryPresent = false
	if err := finalize(directoryFinalizeTemporaryRemoved); err != nil {
		return failPublished(err)
	}
	if !rootIdentityMatches(s.root, parentRelative, parentIdentity) {
		return failPublished(ErrUnsafePath)
	}
	if err := temporary.Close(); err != nil {
		temporaryOpen = false
		return failPublished(ErrStoreUnavailable)
	}
	temporaryOpen = false

	sum := sha256.Sum256(data)
	artifact = task.Artifact{
		ID:           artifactID,
		TaskID:       taskID,
		Kind:         kind,
		RelativePath: relative,
		MIMEType:     mimeType,
		Size:         int64(len(data)),
		SHA256:       hex.EncodeToString(sum[:]),
		CreatedAt:    at,
	}
	capability = &finalizedArtifactCapability{
		store: s, artifact: artifact,
		parentRelative: parentRelative, targetName: targetName,
		parent: parent, parentIdentity: parentIdentity,
		file: final, fileIdentity: finalInfo,
	}
	parentTransferred = true
	finalTransferred = true
	return artifact, capability, nil
}

func validateFinalizedCapabilities(
	artifacts []task.Artifact,
	capabilities []*finalizedArtifactCapability,
) error {
	if len(artifacts) != len(capabilities) {
		return ErrInvalidArtifact
	}
	for index := range capabilities {
		capability := capabilities[index]
		if capability == nil || artifacts[index] != capability.artifact ||
			!validArtifact(artifacts[index]) {
			return ErrInvalidArtifact
		}
		if err := capability.validate(); err != nil {
			return err
		}
	}
	return nil
}

func rollbackFinalizedCapabilities(
	artifacts []task.Artifact,
	capabilities []*finalizedArtifactCapability,
) (result error) {
	defer func() {
		result = errors.Join(result, closeFinalizedCapabilities(capabilities))
	}()
	// Preflight the whole graph before removing any sibling. A replacement of
	// even one object makes the entire rollback fail closed.
	if err := validateFinalizedCapabilities(artifacts, capabilities); err != nil {
		return err
	}
	for index := len(capabilities) - 1; index >= 0; index-- {
		capability := capabilities[index]
		if err := capability.validatePathIdentity(); err != nil {
			return err
		}
		if err := capability.parent.Remove(capability.targetName); err != nil {
			return rootOperationError(err)
		}
		if err := syncRootDirectory(capability.parent); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func closeFinalizedCapabilities(capabilities []*finalizedArtifactCapability) (result error) {
	for _, capability := range capabilities {
		if capability != nil {
			result = errors.Join(result, capability.close())
		}
	}
	return result
}

func (capability *finalizedArtifactCapability) validate() error {
	if capability == nil || capability.store == nil || capability.store.root == nil ||
		capability.parent == nil || capability.file == nil ||
		capability.parentIdentity == nil || capability.fileIdentity == nil {
		return ErrStoreUnavailable
	}
	if !rootIdentityMatches(
		capability.store.root,
		capability.parentRelative,
		capability.parentIdentity,
	) {
		return ErrUnsafePath
	}
	info, err := capability.file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		!os.SameFile(info, capability.fileIdentity) ||
		info.Size() != capability.artifact.Size {
		return ErrUnsafePath
	}
	if _, err := capability.file.Seek(0, io.SeekStart); err != nil {
		return ErrStoreUnavailable
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, capability.file); err != nil {
		return ErrStoreUnavailable
	}
	expectedHash, err := hex.DecodeString(capability.artifact.SHA256)
	if err != nil || subtle.ConstantTimeCompare(hash.Sum(nil), expectedHash) != 1 {
		return ErrUnsafePath
	}
	return capability.validatePathIdentity()
}

func (capability *finalizedArtifactCapability) validatePathIdentity() error {
	if capability == nil || capability.parent == nil || capability.fileIdentity == nil {
		return ErrStoreUnavailable
	}
	current, err := capability.parent.Lstat(capability.targetName)
	if err != nil || isLinkInfo(current) || !current.Mode().IsRegular() ||
		!os.SameFile(current, capability.fileIdentity) {
		return ErrUnsafePath
	}
	return nil
}

func (capability *finalizedArtifactCapability) close() (result error) {
	if capability == nil {
		return nil
	}
	if capability.file != nil {
		if err := capability.file.Close(); err != nil {
			result = errors.Join(result, ErrStoreUnavailable)
		}
		capability.file = nil
	}
	if capability.parent != nil {
		if err := capability.parent.Close(); err != nil {
			result = errors.Join(result, ErrStoreUnavailable)
		}
		capability.parent = nil
	}
	return result
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
	if offset > artifact.Size {
		return nil, 0, false, ErrInvalidRange
	}
	relative := artifact.RelativePath
	file, info, err := openVerifiedFile(s.root, relative)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	if !info.Mode().IsRegular() {
		return nil, 0, false, ErrUnsafePath
	}
	if info.Size() != artifact.Size {
		return nil, 0, false, ErrArtifactChanged
	}
	chunk, err := verifiedChunk(ctx, file, artifact, offset, length, s.hooks.afterSnapshotRead)
	if err != nil {
		return nil, 0, false, err
	}
	if s.hooks.afterVerifiedSnapshot != nil {
		s.hooks.afterVerifiedSnapshot()
	}
	next := offset + int64(len(chunk))
	return chunk, next, next == artifact.Size, nil
}

func verifiedChunk(ctx context.Context, file *os.File, artifact task.Artifact, offset int64, length int, afterRead func(int64)) ([]byte, error) {
	hash := sha256.New()
	readBuffer := make([]byte, MaxReadChunk)
	wanted := int64(length)
	if remaining := artifact.Size - offset; remaining < wanted {
		wanted = remaining
	}
	chunk := make([]byte, 0, int(wanted))
	var position int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(readBuffer)
		if n > 0 {
			block := readBuffer[:n]
			_, _ = hash.Write(block)
			blockStart := position
			blockEnd := position + int64(n)
			captureStart := max(offset, blockStart)
			captureEnd := min(offset+wanted, blockEnd)
			if captureStart < captureEnd {
				chunk = append(chunk, block[captureStart-blockStart:captureEnd-blockStart]...)
			}
			position = blockEnd
			if afterRead != nil {
				afterRead(position)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, ErrStoreUnavailable
		}
		if n == 0 {
			return nil, ErrArtifactChanged
		}
	}
	if position != artifact.Size || int64(len(chunk)) != wanted {
		return nil, ErrArtifactChanged
	}
	expectedHash, _ := hex.DecodeString(artifact.SHA256)
	if subtle.ConstantTimeCompare(hash.Sum(nil), expectedHash) != 1 {
		return nil, ErrArtifactChanged
	}
	return chunk, nil
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
	rootNode := &cleanupDirectory{root: s.root}
	if err := auditCleanup(ctx, rootNode, "", referenced); err != nil {
		rootNode.closeChildren()
		return err
	}
	defer rootNode.closeChildren()
	return s.executeCleanup(ctx, rootNode)
}

type cleanupDirectory struct {
	root     *os.Root
	parent   *os.Root
	name     string
	relative string
	identity os.FileInfo
	files    []cleanupFile
	children []*cleanupDirectory
}

type cleanupFile struct {
	name, relative string
	identity       os.FileInfo
}

func auditCleanup(ctx context.Context, directory *cleanupDirectory, prefix string, referenced map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := directory.root.Open(".")
	if err != nil {
		return rootOperationError(err)
	}
	entries, err := handle.ReadDir(-1)
	closeErr := handle.Close()
	if err != nil || closeErr != nil {
		return ErrStoreUnavailable
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !portablePathSegment(entry.Name()) {
			return ErrUnsafePath
		}
		relative := entry.Name()
		if prefix != "" {
			relative = path.Join(prefix, entry.Name())
		}
		info, err := directory.root.Lstat(entry.Name())
		if err != nil {
			return rootOperationError(err)
		}
		if isLinkInfo(info) {
			return ErrUnsafePath
		}
		switch {
		case info.IsDir():
			childRoot, childIdentity, err := openVerifiedRoot(directory.root, entry.Name())
			if err != nil {
				return err
			}
			child := &cleanupDirectory{
				root: childRoot, parent: directory.root, name: entry.Name(), relative: relative, identity: childIdentity,
			}
			directory.children = append(directory.children, child)
			if err := auditCleanup(ctx, child, relative, referenced); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if temporaryArtifactName(entry.Name()) {
				directory.files = append(directory.files, cleanupFile{name: entry.Name(), relative: relative, identity: info})
			} else if _, ok := referenced[relative]; !ok {
				directory.files = append(directory.files, cleanupFile{name: entry.Name(), relative: relative, identity: info})
			}
		default:
			return ErrUnsafePath
		}
	}
	return nil
}

func (s *Store) executeCleanup(ctx context.Context, directory *cleanupDirectory) error {
	if s.hooks.beforeCleanupExecute != nil {
		s.hooks.beforeCleanupExecute()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, file := range directory.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := directory.root.Lstat(file.name)
		if err != nil {
			return rootOperationError(err)
		}
		if isLinkInfo(current) || !current.Mode().IsRegular() || !os.SameFile(current, file.identity) {
			return ErrUnsafePath
		}
		if s.hooks.beforeCleanupRemove != nil {
			s.hooks.beforeCleanupRemove(file.relative)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := directory.root.Remove(file.name); err != nil {
			return rootOperationError(err)
		}
	}
	for _, child := range directory.children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.executeCleanup(ctx, child); err != nil {
			return err
		}
		if err := child.root.Close(); err != nil {
			return ErrStoreUnavailable
		}
		child.root = nil
		current, err := directory.root.Lstat(child.name)
		if err != nil {
			return rootOperationError(err)
		}
		if isLinkInfo(current) || !current.IsDir() || !os.SameFile(current, child.identity) {
			return ErrUnsafePath
		}
		if s.hooks.beforeCleanupRemove != nil {
			s.hooks.beforeCleanupRemove(child.relative)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := directory.root.Remove(child.name); err != nil && !isDirectoryNotEmpty(err) {
			return rootOperationError(err)
		}
	}
	return nil
}

func (directory *cleanupDirectory) closeChildren() {
	for _, child := range directory.children {
		child.closeChildren()
		if child.root != nil {
			_ = child.root.Close()
			child.root = nil
		}
	}
}

type summaryEncoder func(string, []byte) ([]byte, error)

var summaryEncoders = map[task.Kind]summaryEncoder{
	task.KindSimulation:    encodeSimulationSummary,
	task.KindCMakeBuild:    encodeCMakeSummary,
	task.KindTestDiscovery: encodeTestDiscoverySummary,
	task.KindTestRun:       encodeTestRunSummary,
}

func canonicalSummary(taskID string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidArtifact
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, ErrInvalidArtifact
	}
	kind := task.KindSimulation
	if encodedKind, ok := fields["kind"]; ok {
		if err := json.Unmarshal(encodedKind, &kind); err != nil {
			return nil, ErrInvalidArtifact
		}
	}
	encoder, ok := summaryEncoders[kind]
	if !ok {
		return nil, ErrInvalidArtifact
	}
	return encoder(taskID, raw)
}

func encodeSimulationSummary(taskID string, raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, ErrInvalidArtifact
	}
	allowed := map[string]struct{}{"taskId": {}, "scenario": {}, "outcome": {}, "finishedAt": {}}
	values := make(map[string]string, len(fields))
	for name, rawValue := range fields {
		if _, ok := allowed[name]; !ok {
			return nil, ErrInvalidArtifact
		}
		stringValue, ok := rawValue.(string)
		if !ok || sensitiveSummaryString(stringValue) {
			return nil, ErrInvalidArtifact
		}
		values[name] = stringValue
	}
	outcome, hasOutcome := values["outcome"]
	minimal := len(values) == 1 && hasOutcome
	full := len(values) == 4 && values["taskId"] != "" && values["scenario"] != "" && values["finishedAt"] != "" && hasOutcome
	if !minimal && !full {
		return nil, ErrInvalidArtifact
	}
	if !validOutcome(outcome) {
		return nil, ErrInvalidArtifact
	}
	if full {
		if values["taskId"] != taskID || !task.ValidScenario(task.Scenario(values["scenario"])) {
			return nil, ErrInvalidArtifact
		}
		if _, err := time.Parse(time.RFC3339Nano, values["finishedAt"]); err != nil {
			return nil, ErrInvalidArtifact
		}
	}
	var result []byte
	result = append(result, '{')
	first := true
	for _, name := range []string{"taskId", "scenario", "outcome", "finishedAt"} {
		fieldValue, ok := values[name]
		if !ok {
			continue
		}
		if !first {
			result = append(result, ',')
		}
		first = false
		encodedName, _ := json.Marshal(name)
		encodedValue, _ := json.Marshal(fieldValue)
		result = append(result, encodedName...)
		result = append(result, ':')
		result = append(result, encodedValue...)
	}
	result = append(result, '}', '\n')
	return result, nil
}

func encodeCMakeSummary(taskID string, raw []byte) ([]byte, error) {
	return encodeExecutionSummary(
		taskID,
		task.KindCMakeBuild,
		8,
		raw,
	)
}

func encodeTestDiscoverySummary(
	taskID string,
	raw []byte,
) ([]byte, error) {
	return encodeExecutionSummary(
		taskID,
		task.KindTestDiscovery,
		10_000,
		raw,
	)
}

func encodeTestRunSummary(
	taskID string,
	raw []byte,
) ([]byte, error) {
	return encodeExecutionSummary(
		taskID,
		task.KindTestRun,
		10_000,
		raw,
	)
}

func encodeExecutionSummary(
	taskID string,
	expectedKind task.Kind,
	maximumSteps int,
	raw []byte,
) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var summary task.TaskSummary
	if err := decoder.Decode(&summary); err != nil {
		return nil, ErrInvalidArtifact
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, ErrInvalidArtifact
	}
	if summary.TaskID != taskID ||
		summary.Kind != expectedKind ||
		!validOutcome(string(summary.Outcome)) ||
		summary.FinishedAt.IsZero() ||
		len(summary.Steps) < 1 ||
		len(summary.Steps) > maximumSteps {
		return nil, ErrInvalidArtifact
	}
	ids := make(map[string]struct{}, len(summary.Steps))
	for _, step := range summary.Steps {
		if !validSummaryStep(step, expectedKind) {
			return nil, ErrInvalidArtifact
		}
		if _, exists := ids[step.ID]; exists {
			return nil, ErrInvalidArtifact
		}
		ids[step.ID] = struct{}{}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil, ErrInvalidArtifact
	}
	return append(encoded, '\n'), nil
}

func validSummaryStep(
	step task.StepSnapshot,
	taskKind task.Kind,
) bool {
	if !validSummaryStepID(step.ID) ||
		!validSummaryStepKind(step.Kind, taskKind) ||
		(step.Status != task.StepSucceeded && step.Status != task.StepFailed && step.Status != task.StepSkipped) ||
		step.FinishedAt == nil ||
		step.FinishedAt.IsZero() ||
		len(step.ErrorCode) > 128 ||
		strings.ContainsRune(step.ErrorCode, 0) ||
		sensitiveSummaryString(step.ErrorCode) {
		return false
	}
	if step.StartedAt != nil {
		if step.StartedAt.IsZero() || step.StartedAt.After(*step.FinishedAt) {
			return false
		}
	}
	return true
}

func validSummaryStepKind(
	stepKind task.StepKind,
	taskKind task.Kind,
) bool {
	switch taskKind {
	case task.KindCMakeBuild:
		return stepKind == task.StepConfigure ||
			stepKind == task.StepBuild
	case task.KindTestDiscovery, task.KindTestRun:
		switch stepKind {
		case task.StepConfigure, task.StepBuild,
			task.StepTestDiscovery, task.StepTestRun:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func validSummaryStepID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func sensitiveSummaryString(value string) bool {
	if filepath.IsAbs(value) || path.IsAbs(value) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "/") {
		return true
	}
	if len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	return strings.Contains(value, "${") || strings.HasPrefix(value, "$ENV{") ||
		(strings.HasPrefix(value, "%") && strings.HasSuffix(value, "%") && len(value) > 2)
}

func validOutcome(value string) bool {
	switch task.Outcome(value) {
	case task.OutcomeSucceeded, task.OutcomeCommandFailed, task.OutcomeCancelled, task.OutcomeTimedOut,
		task.OutcomeInterrupted, task.OutcomeInfrastructureFailed:
		return true
	default:
		return false
	}
}

func createTemporary(parent *os.Root) (string, *os.File, error) {
	for range 100 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, ErrStoreUnavailable
		}
		name := ".artifact-" + hex.EncodeToString(random[:]) + ".tmp"
		file, err := parent.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, rootOperationError(err)
		}
	}
	return "", nil, ErrStoreUnavailable
}

func syncRootDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return rootOperationError(err)
	}
	defer directory.Close()
	return syncDirectoryHandle(directory)
}

func openVerifiedRoot(root *os.Root, relative string) (*os.Root, os.FileInfo, error) {
	if !canonicalRelativePath(relative) {
		return nil, nil, ErrUnsafePath
	}
	if err := validateRootPath(root, relative, false); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat(nativePath(relative))
	if err != nil || isLinkInfo(before) || !before.IsDir() {
		return nil, nil, rootOperationError(err)
	}
	child, err := root.OpenRoot(nativePath(relative))
	if err != nil {
		return nil, nil, rootOperationError(err)
	}
	opened, err := child.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = child.Close()
		return nil, nil, ErrUnsafePath
	}
	after, err := root.Lstat(nativePath(relative))
	if err != nil || isLinkInfo(after) || !os.SameFile(after, opened) {
		_ = child.Close()
		return nil, nil, ErrUnsafePath
	}
	return child, opened, nil
}

func openVerifiedFile(root *os.Root, relative string) (*os.File, os.FileInfo, error) {
	if !canonicalRelativePath(relative) {
		return nil, nil, ErrUnsafePath
	}
	if err := validateRootPath(root, relative, false); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat(nativePath(relative))
	if err != nil || isLinkInfo(before) || !before.Mode().IsRegular() {
		return nil, nil, rootOperationError(err)
	}
	file, err := root.Open(nativePath(relative))
	if err != nil {
		return nil, nil, rootOperationError(err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, ErrUnsafePath
	}
	after, err := root.Lstat(nativePath(relative))
	if err != nil || isLinkInfo(after) || !os.SameFile(after, opened) {
		_ = file.Close()
		return nil, nil, ErrUnsafePath
	}
	return file, opened, nil
}

func rootIdentityMatches(root *os.Root, relative string, identity os.FileInfo) bool {
	info, err := root.Lstat(nativePath(relative))
	return err == nil && !isLinkInfo(info) && info.IsDir() && os.SameFile(info, identity)
}

func validateRootPath(root *os.Root, relative string, allowMissing bool) error {
	if !canonicalRelativePath(relative) {
		return ErrUnsafePath
	}
	segments := strings.Split(relative, "/")
	current := ""
	for index, segment := range segments {
		if current == "" {
			current = segment
		} else {
			current = path.Join(current, segment)
		}
		info, err := root.Lstat(nativePath(current))
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			return nil
		}
		if err != nil {
			return rootOperationError(err)
		}
		if isLinkInfo(info) || (index < len(segments)-1 && !info.IsDir()) {
			return ErrUnsafePath
		}
	}
	return nil
}

func rootOperationError(err error) error {
	if err == nil {
		return ErrUnsafePath
	}
	if errors.Is(err, os.ErrInvalid) {
		return ErrUnsafePath
	}
	return ErrStoreUnavailable
}

func nativePath(relative string) string {
	return filepath.FromSlash(relative)
}

func validArtifact(value task.Artifact) bool {
	mimeType, extension, ok := artifactDescriptor(value.Kind)
	if !validGeneratedID(value.ID) || !validGeneratedID(value.TaskID) || !ok ||
		value.MIMEType != mimeType || value.Size < 0 || value.CreatedAt.IsZero() ||
		value.RelativePath != artifactRelativePathFor(value.TaskID, value.ID, extension) ||
		len(value.SHA256) != sha256.Size*2 ||
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
	return artifactRelativePathFor(taskID, artifactID, ".json")
}

func artifactRelativePathFor(taskID, artifactID, extension string) string {
	return path.Join("tasks", taskID, artifactID+extension)
}

func artifactDescriptor(kind string) (mimeType, extension string, ok bool) {
	switch kind {
	case "task-summary", "build-summary", "execution-plan", "test-catalog",
		"test-selection", "test-run-summary":
		return "application/json", ".json", true
	case "diagnostics", "test-results":
		return "application/x-ndjson", ".jsonl", true
	case "stdout", "stderr":
		return "application/octet-stream", ".log", true
	case "coverage-json":
		return "application/json", ".coverage.json", true
	case "junit-xml":
		return "application/xml", ".junit.xml", true
	case "coverage-html":
		return "text/html", ".coverage.html", true
	default:
		return "", "", false
	}
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
