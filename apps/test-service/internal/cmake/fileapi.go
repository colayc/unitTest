package cmake

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	fileAPIQueryRelativePath = ".cmake/api/v1/query/client-unit-test-ide/query.json"
	fileAPIReplyRelativePath = ".cmake/api/v1/reply"

	maxFileAPIFileBytes      = 512 * 1024
	maxFileAPITotalBytes     = 4 * 1024 * 1024
	maxFileAPITotalFiles     = 140
	maxFileAPIObjects        = 64
	maxFileAPIConfigs        = 64
	maxFileAPITargets        = 1024
	maxFileAPITargetFiles    = 256
	maxFileAPIArtifacts      = 128
	maxFileAPITotalArtifacts = 4096
	maxFileAPIInputs         = 2048
	maxFileAPIToolchains     = 64
	maxFileAPICacheEntries   = 4096
	maxCommandFragmentBytes  = 16 * 1024
)

var (
	ErrFileAPIReply    = errors.New("invalid CMake File API reply")
	ErrFileAPIBoundary = errors.New("CMake File API path is outside allowed roots")
	ErrFileAPILimit    = errors.New("CMake File API reply exceeds limit")
)

type Target struct {
	ID        string
	Name      string
	Type      string
	SourceDir string
	BuildDir  string
	Artifacts []string
}

type FileAPIReply struct {
	Targets          []Target
	ToolchainIDs     []string
	CMakeInputs      []string
	CMakeInputStates []FingerprintFile
	Cache            FingerprintFile
	StateFiles       []FingerprintFile
	Configurations   []string
}

var fileAPIQuery = []byte("{\n" +
	"  \"requests\": [\n" +
	"    {\"kind\": \"codemodel\", \"version\": {\"major\": 2}},\n" +
	"    {\"kind\": \"cache\", \"version\": {\"major\": 2}},\n" +
	"    {\"kind\": \"cmakeFiles\", \"version\": {\"major\": 1}},\n" +
	"    {\"kind\": \"toolchains\", \"version\": {\"major\": 1}}\n" +
	"  ]\n" +
	"}\n")

func WriteQuery(buildDir string) error {
	return writeQueryWithPublisher(buildDir, replaceFileAtomically)
}

func writeQueryWithPublisher(
	buildDir string,
	publish func(source, destination string) error,
) error {
	root, err := workspace.OpenRoot(buildDir)
	if err != nil {
		return fmt.Errorf("open build directory: %w", err)
	}
	queryPath, err := root.ResolveRelative(filepath.FromSlash(fileAPIQueryRelativePath))
	if err != nil {
		return fmt.Errorf("resolve File API query path: %w", err)
	}
	queryDir := filepath.Dir(queryPath)
	if err := os.MkdirAll(queryDir, 0o700); err != nil {
		return fmt.Errorf("create File API query directory: %w", err)
	}
	queryPath, err = root.ResolveRelative(filepath.FromSlash(fileAPIQueryRelativePath))
	if err != nil {
		return fmt.Errorf("verify File API query path: %w", err)
	}
	queryDir = filepath.Dir(queryPath)

	temporary, err := os.CreateTemp(queryDir, ".query-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary File API query: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary File API query: %w", err)
	}
	if _, err := temporary.Write(fileAPIQuery); err != nil {
		return fmt.Errorf("write temporary File API query: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary File API query: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary File API query: %w", err)
	}
	if err := publish(temporaryPath, queryPath); err != nil {
		return fmt.Errorf("publish File API query: %w", err)
	}
	published = true
	if err := syncParentDirectory(queryDir); err != nil {
		return fmt.Errorf("flush File API query directory: %w", err)
	}
	return nil
}

type fileAPIVersion struct {
	Major int  `json:"major"`
	Minor *int `json:"minor"`
}

type fileAPIReference struct {
	Kind     string         `json:"kind"`
	Version  fileAPIVersion `json:"version"`
	JSONFile string         `json:"jsonFile"`
	Error    string         `json:"error"`
}

type fileAPIRequest struct {
	Kind    string         `json:"kind"`
	Version fileAPIVersion `json:"version"`
}

type fileAPIIndex struct {
	Objects []fileAPIReference `json:"objects"`
	Reply   struct {
		Client struct {
			Query struct {
				Requests  []fileAPIRequest   `json:"requests"`
				Responses []fileAPIReference `json:"responses"`
				Error     string             `json:"error"`
			} `json:"query.json"`
		} `json:"client-unit-test-ide"`
	} `json:"reply"`
}

type fileAPIObjectIdentity struct {
	Kind    string         `json:"kind"`
	Version fileAPIVersion `json:"version"`
}

type fileAPICodemodel struct {
	Kind           string         `json:"kind"`
	Version        fileAPIVersion `json:"version"`
	Paths          fileAPIPaths   `json:"paths"`
	Configurations []struct {
		Name    string `json:"name"`
		Targets []struct {
			Name     string `json:"name"`
			ID       string `json:"id"`
			JSONFile string `json:"jsonFile"`
		} `json:"targets"`
	} `json:"configurations"`
}

type fileAPIPaths struct {
	Source string `json:"source"`
	Build  string `json:"build"`
}

type fileAPITargetObject struct {
	CodemodelVersion fileAPIVersion `json:"codemodelVersion"`
	Name             string         `json:"name"`
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	Paths            fileAPIPaths   `json:"paths"`
	Artifacts        []struct {
		Path string `json:"path"`
	} `json:"artifacts"`
}

type fileAPICMakeFiles struct {
	Kind    string         `json:"kind"`
	Version fileAPIVersion `json:"version"`
	Paths   fileAPIPaths   `json:"paths"`
	Inputs  []struct {
		Path string `json:"path"`
	} `json:"inputs"`
}

type fileAPIToolchains struct {
	Kind       string         `json:"kind"`
	Version    fileAPIVersion `json:"version"`
	Toolchains []struct {
		Language string `json:"language"`
		Compiler struct {
			Path            string  `json:"path"`
			ID              string  `json:"id"`
			Version         string  `json:"version"`
			Target          string  `json:"target"`
			CommandFragment *string `json:"commandFragment"`
		} `json:"compiler"`
	} `json:"toolchains"`
}

type fileAPICache struct {
	Kind    string         `json:"kind"`
	Version fileAPIVersion `json:"version"`
	Entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"entries"`
}

type fileAPIReader struct {
	replyRoot    workspace.Root
	allowedRoots []workspace.Root
	snapshots    map[string]*fileSnapshot
	data         map[string][]byte
	replyFiles   map[string]struct{}
	totalBytes   int64
}

type fileAPIReplyCandidate struct {
	Name    string
	Suffix  string
	IsError bool
}

func ReadReply(
	buildDir string,
	allowedRoots []string,
	profiles ...BuildProfile,
) (FileAPIReply, error) {
	buildRoot, err := workspace.OpenRoot(buildDir)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: open build directory: %v", ErrFileAPIReply, err)
	}
	roots, err := openFileAPIAllowedRoots(allowedRoots)
	if err != nil {
		return FileAPIReply{}, err
	}
	if _, err := canonicalAllowedPath(buildRoot.NativePath, roots); err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: build directory: %v", ErrFileAPIBoundary, err)
	}
	replyPath, err := buildRoot.ResolveRelative(filepath.FromSlash(fileAPIReplyRelativePath))
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: resolve reply directory: %v", ErrFileAPIReply, err)
	}
	replyRoot, err := workspace.OpenRoot(replyPath)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: open reply directory: %v", ErrFileAPIReply, err)
	}
	reader := &fileAPIReader{
		replyRoot:    replyRoot,
		allowedRoots: roots,
		snapshots:    make(map[string]*fileSnapshot),
		data:         make(map[string][]byte),
		replyFiles:   make(map[string]struct{}),
	}
	defer reader.close()

	current, err := reader.currentReply()
	if err != nil {
		return FileAPIReply{}, err
	}
	currentData, err := reader.read(current.Name)
	if err != nil {
		return FileAPIReply{}, err
	}
	if current.IsError {
		if err := reader.verifyCurrentReply(current); err != nil {
			return FileAPIReply{}, err
		}
		return FileAPIReply{}, fmt.Errorf("%w: current CMake reply is an error %q", ErrFileAPIReply, current.Name)
	}
	var index fileAPIIndex
	if err := decodeFileAPIJSON(currentData, &index); err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: decode index %q: %v", ErrFileAPIReply, current.Name, err)
	}
	references, err := validateFileAPIIndex(index)
	if err != nil {
		return FileAPIReply{}, err
	}

	var codemodel fileAPICodemodel
	if err := reader.readObject(references["codemodel"], &codemodel); err != nil {
		return FileAPIReply{}, err
	}
	var cache fileAPICache
	if err := reader.readObject(references["cache"], &cache); err != nil {
		return FileAPIReply{}, err
	}
	if len(cache.Entries) > maxFileAPICacheEntries {
		return FileAPIReply{}, fmt.Errorf("%w: cache entries exceed %d", ErrFileAPILimit, maxFileAPICacheEntries)
	}
	var cmakeFiles fileAPICMakeFiles
	if err := reader.readObject(references["cmakeFiles"], &cmakeFiles); err != nil {
		return FileAPIReply{}, err
	}
	var toolchains fileAPIToolchains
	if err := reader.readObject(references["toolchains"], &toolchains); err != nil {
		return FileAPIReply{}, err
	}

	result, err := reader.assemble(codemodel, cmakeFiles, toolchains, profiles)
	if err != nil {
		return FileAPIReply{}, err
	}
	if err := reader.verifyCurrentReply(current); err != nil {
		return FileAPIReply{}, err
	}
	result.StateFiles = reader.states(reader.replyFiles)
	return result, nil
}

func openFileAPIAllowedRoots(paths []string) ([]workspace.Root, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no allowed roots", ErrFileAPIBoundary)
	}
	roots := make([]workspace.Root, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		root, err := workspace.OpenRoot(path)
		if err != nil {
			return nil, fmt.Errorf("%w: open allowed root %q: %v", ErrFileAPIBoundary, path, err)
		}
		if _, duplicate := seen[root.ID]; duplicate {
			continue
		}
		seen[root.ID] = struct{}{}
		roots = append(roots, root)
	}
	return roots, nil
}

func canonicalAllowedPath(value string, roots []workspace.Root) (string, error) {
	value = filepath.Clean(filepath.FromSlash(value))
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("path %q is not absolute", value)
	}
	for _, root := range roots {
		if !root.Contains(value) {
			continue
		}
		relative, err := filepath.Rel(root.NativePath, value)
		if err != nil {
			continue
		}
		resolved, err := root.ResolveRelative(relative)
		if err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q is not contained by an allowed root", value)
}

func (reader *fileAPIReader) currentReply() (fileAPIReplyCandidate, error) {
	entries, err := os.ReadDir(reader.replyRoot.NativePath)
	if err != nil {
		return fileAPIReplyCandidate{}, fmt.Errorf("%w: enumerate reply directory: %v", ErrFileAPIReply, err)
	}
	bySuffix := make(map[string]fileAPIReplyCandidate)
	var current fileAPIReplyCandidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		prefix := ""
		isError := false
		switch {
		case strings.HasPrefix(name, "index-"):
			prefix = "index-"
		case strings.HasPrefix(name, "error-"):
			prefix = "error-"
			isError = true
		default:
			continue
		}
		if !strings.HasSuffix(name, ".json") || len(name) <= len(prefix)+len(".json") {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		if previous, duplicate := bySuffix[suffix]; duplicate {
			return fileAPIReplyCandidate{}, fmt.Errorf(
				"%w: ambiguous CMake replies %q and %q",
				ErrFileAPIReply,
				previous.Name,
				name,
			)
		}
		candidate := fileAPIReplyCandidate{Name: name, Suffix: suffix, IsError: isError}
		bySuffix[suffix] = candidate
		if current.Name == "" || candidate.Suffix > current.Suffix {
			current = candidate
		}
	}
	if current.Name == "" {
		return fileAPIReplyCandidate{}, fmt.Errorf("%w: no CMake reply index or error", ErrFileAPIReply)
	}
	return current, nil
}

func (reader *fileAPIReader) verifyCurrentReply(selected fileAPIReplyCandidate) error {
	current, err := reader.currentReply()
	if err != nil {
		return err
	}
	if current != selected {
		return fmt.Errorf(
			"%w: current CMake reply changed from %q to %q while reading",
			ErrFileAPIReply,
			selected.Name,
			current.Name,
		)
	}
	return reader.verify()
}

func (reader *fileAPIReader) read(relative string) ([]byte, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" ||
		strings.ContainsRune(relative, 0) || strings.ContainsAny(relative, `/\`) ||
		filepath.Base(relative) != relative {
		return nil, fmt.Errorf("%w: invalid reply file reference %q", ErrFileAPIReply, relative)
	}
	native := filepath.FromSlash(relative)
	canonical, err := reader.replyRoot.ResolveRelative(native)
	if err != nil {
		if errors.Is(err, workspace.ErrPathOutsideRoot) {
			return nil, fmt.Errorf("%w: reply file %q: %v", ErrFileAPIBoundary, relative, err)
		}
		return nil, fmt.Errorf("%w: resolve reply file %q: %v", ErrFileAPIReply, relative, err)
	}
	if _, err := canonicalAllowedPath(canonical, reader.allowedRoots); err != nil {
		return nil, fmt.Errorf("%w: reply file %q: %v", ErrFileAPIBoundary, relative, err)
	}
	key := canonicalPortablePath(canonical)
	reader.replyFiles[key] = struct{}{}
	if data, exists := reader.data[key]; exists {
		return data, nil
	}
	snapshot, err := reader.capture(canonical)
	if err != nil {
		return nil, fmt.Errorf("reply file %q: %w", relative, err)
	}
	data, err := snapshot.ReadAll(maxFileAPIFileBytes)
	if err != nil {
		_ = snapshot.Close()
		return nil, fmt.Errorf("%w: read reply file %q: %v", ErrFileAPIReply, relative, err)
	}
	reader.data[key] = data
	return data, nil
}

func (reader *fileAPIReader) capture(canonical string) (*fileSnapshot, error) {
	key := canonicalPortablePath(canonical)
	if snapshot, exists := reader.snapshots[key]; exists {
		return snapshot, nil
	}
	if len(reader.snapshots) >= maxFileAPITotalFiles {
		return nil, fmt.Errorf("%w: consumed files exceed %d", ErrFileAPILimit, maxFileAPITotalFiles)
	}
	snapshot, err := captureFileSnapshot(canonical, maxFileAPIFileBytes)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			return nil, fmt.Errorf("%w: %q: %v", ErrFileAPILimit, canonical, err)
		}
		return nil, fmt.Errorf("%w: snapshot %q: %v", ErrFileAPIReply, canonical, err)
	}
	if reader.totalBytes+snapshot.info.Size() > maxFileAPITotalBytes {
		_ = snapshot.Close()
		return nil, fmt.Errorf("%w: total consumed bytes exceed %d", ErrFileAPILimit, maxFileAPITotalBytes)
	}
	reader.totalBytes += snapshot.info.Size()
	reader.snapshots[key] = snapshot
	return snapshot, nil
}

func (reader *fileAPIReader) snapshotAllowed(path string) (FingerprintFile, error) {
	canonical, err := canonicalAllowedPath(path, reader.allowedRoots)
	if err != nil {
		return FingerprintFile{}, fmt.Errorf("%w: %v", ErrFileAPIBoundary, err)
	}
	snapshot, err := reader.capture(canonical)
	if err != nil {
		return FingerprintFile{}, err
	}
	return fingerprintFileFromSnapshot(snapshot), nil
}

func (reader *fileAPIReader) readObject(reference fileAPIReference, destination any) error {
	data, err := reader.read(reference.JSONFile)
	if err != nil {
		return err
	}
	var identity fileAPIObjectIdentity
	if err := decodeFileAPIJSON(data, &identity); err != nil {
		return fmt.Errorf("%w: decode %s object %q: %v", ErrFileAPIReply, reference.Kind, reference.JSONFile, err)
	}
	if identity.Kind != reference.Kind || !sameFileAPIVersion(identity.Version, reference.Version) {
		return fmt.Errorf(
			"%w: object %q identity is %s v%s, expected %s v%s",
			ErrFileAPIReply,
			reference.JSONFile,
			identity.Kind,
			formatFileAPIVersion(identity.Version),
			reference.Kind,
			formatFileAPIVersion(reference.Version),
		)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("%w: decode %s object %q: %v", ErrFileAPIReply, reference.Kind, reference.JSONFile, err)
	}
	return nil
}

func validateFileAPIIndex(index fileAPIIndex) (map[string]fileAPIReference, error) {
	if len(index.Objects) > maxFileAPIObjects {
		return nil, fmt.Errorf("%w: index objects exceed %d", ErrFileAPILimit, maxFileAPIObjects)
	}
	expected := []fileAPIRequest{
		{Kind: "codemodel", Version: fileAPIVersion{Major: 2}},
		{Kind: "cache", Version: fileAPIVersion{Major: 2}},
		{Kind: "cmakeFiles", Version: fileAPIVersion{Major: 1}},
		{Kind: "toolchains", Version: fileAPIVersion{Major: 1}},
	}
	query := index.Reply.Client.Query
	if query.Error != "" || len(query.Requests) != len(expected) ||
		len(query.Responses) != len(expected) {
		return nil, fmt.Errorf("%w: client query request/response shape is invalid", ErrFileAPIReply)
	}
	result := make(map[string]fileAPIReference, len(expected))
	for position, want := range expected {
		request := query.Requests[position]
		if request.Kind != want.Kind || request.Version.Major != want.Version.Major ||
			request.Version.Minor != nil {
			return nil, fmt.Errorf("%w: client query request %d has unexpected identity", ErrFileAPIReply, position)
		}
		response := query.Responses[position]
		if response.Error != "" || response.Kind != want.Kind ||
			response.Version.Major != want.Version.Major || response.Version.Minor == nil ||
			*response.Version.Minor < 0 || response.JSONFile == "" {
			return nil, fmt.Errorf("%w: client query response %d has unexpected identity", ErrFileAPIReply, position)
		}
		matches := 0
		kindMatches := 0
		for _, object := range index.Objects {
			if object.Kind == want.Kind {
				kindMatches++
			}
			if sameFileAPIReference(object, response) {
				matches++
			}
		}
		if matches != 1 || kindMatches != 1 {
			return nil, fmt.Errorf(
				"%w: response %s v%s/%q has %d exact and %d kind-matching index objects",
				ErrFileAPIReply,
				response.Kind,
				formatFileAPIVersion(response.Version),
				response.JSONFile,
				matches,
				kindMatches,
			)
		}
		result[want.Kind] = response
	}
	return result, nil
}

func (reader *fileAPIReader) assemble(
	codemodel fileAPICodemodel,
	cmakeFiles fileAPICMakeFiles,
	toolchains fileAPIToolchains,
	profiles []BuildProfile,
) (FileAPIReply, error) {
	if len(codemodel.Configurations) > maxFileAPIConfigs {
		return FileAPIReply{}, fmt.Errorf("%w: configurations exceed %d", ErrFileAPILimit, maxFileAPIConfigs)
	}
	sourcePath, err := canonicalAllowedPath(codemodel.Paths.Source, reader.allowedRoots)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: codemodel source path: %v", ErrFileAPIBoundary, err)
	}
	buildPath, err := canonicalAllowedPath(codemodel.Paths.Build, reader.allowedRoots)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: codemodel build path: %v", ErrFileAPIBoundary, err)
	}
	if canonicalPortablePath(buildPath) != canonicalPortablePath(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(reader.replyRoot.NativePath))))) {
		return FileAPIReply{}, fmt.Errorf("%w: codemodel build path does not match reply build directory", ErrFileAPIReply)
	}

	result := FileAPIReply{
		Targets:          make([]Target, 0),
		ToolchainIDs:     make([]string, 0),
		CMakeInputs:      make([]string, 0),
		CMakeInputStates: make([]FingerprintFile, 0),
		StateFiles:       make([]FingerprintFile, 0),
		Configurations:   make([]string, 0),
	}
	targetCount := 0
	targetFiles := make(map[string]struct{})
	for _, configuration := range codemodel.Configurations {
		if configuration.Name == "" {
			return FileAPIReply{}, fmt.Errorf("%w: configuration has empty name", ErrFileAPIReply)
		}
		result.Configurations = append(result.Configurations, configuration.Name)
		targetCount += len(configuration.Targets)
		for _, target := range configuration.Targets {
			targetFiles[target.JSONFile] = struct{}{}
		}
		if targetCount > maxFileAPITargets {
			return FileAPIReply{}, fmt.Errorf("%w: targets exceed %d", ErrFileAPILimit, maxFileAPITargets)
		}
		if len(targetFiles) > maxFileAPITargetFiles {
			return FileAPIReply{}, fmt.Errorf("%w: target detail files exceed %d", ErrFileAPILimit, maxFileAPITargetFiles)
		}
	}
	if targetCount > 0 {
		if len(profiles) != 1 || profiles[0].ID == "" || profiles[0].ProjectID == "" {
			return FileAPIReply{}, fmt.Errorf("%w: targets require exactly one profile with ID and ProjectID", ErrFileAPIReply)
		}
	}

	targetsByID := make(map[string]Target, targetCount)
	totalArtifacts := 0
	for _, configuration := range codemodel.Configurations {
		for _, reference := range configuration.Targets {
			if reference.Name == "" || reference.ID == "" || reference.JSONFile == "" {
				return FileAPIReply{}, fmt.Errorf("%w: target reference is incomplete", ErrFileAPIReply)
			}
			data, err := reader.read(reference.JSONFile)
			if err != nil {
				return FileAPIReply{}, err
			}
			var object fileAPITargetObject
			if err := decodeFileAPIJSON(data, &object); err != nil {
				return FileAPIReply{}, fmt.Errorf("%w: decode target %q: %v", ErrFileAPIReply, reference.JSONFile, err)
			}
			if object.Name != reference.Name || object.ID != reference.ID ||
				!sameFileAPIVersion(object.CodemodelVersion, codemodel.Version) ||
				object.Type == "" {
				return FileAPIReply{}, fmt.Errorf("%w: target %q identity does not match codemodel", ErrFileAPIReply, reference.JSONFile)
			}
			targetSource, err := resolveFileAPIPath(object.Paths.Source, sourcePath, reader.allowedRoots)
			if err != nil {
				return FileAPIReply{}, fmt.Errorf("%w: target %q source path: %v", ErrFileAPIBoundary, object.Name, err)
			}
			targetBuild, err := resolveFileAPIPath(object.Paths.Build, buildPath, reader.allowedRoots)
			if err != nil {
				return FileAPIReply{}, fmt.Errorf("%w: target %q build path: %v", ErrFileAPIBoundary, object.Name, err)
			}
			if len(object.Artifacts) > maxFileAPIArtifacts {
				return FileAPIReply{}, fmt.Errorf("%w: target %q artifacts exceed %d", ErrFileAPILimit, object.Name, maxFileAPIArtifacts)
			}
			artifacts := make([]string, 0, len(object.Artifacts))
			for _, artifact := range object.Artifacts {
				resolved, err := resolveFileAPIPath(artifact.Path, buildPath, reader.allowedRoots)
				if err != nil {
					return FileAPIReply{}, fmt.Errorf("%w: target %q artifact: %v", ErrFileAPIBoundary, object.Name, err)
				}
				artifacts = append(artifacts, resolved)
			}
			artifacts = sortedUniqueStrings(artifacts)
			totalArtifacts += len(artifacts)
			if totalArtifacts > maxFileAPITotalArtifacts {
				return FileAPIReply{}, fmt.Errorf("%w: total target artifacts exceed %d", ErrFileAPILimit, maxFileAPITotalArtifacts)
			}
			identity, err := targetIdentity(profiles[0], configuration.Name, reference.ID)
			if err != nil {
				return FileAPIReply{}, fmt.Errorf("%w: construct target identity: %v", ErrFileAPIReply, err)
			}
			target := Target{
				ID: identity, Name: object.Name, Type: object.Type,
				SourceDir: targetSource, BuildDir: targetBuild, Artifacts: artifacts,
			}
			if previous, duplicate := targetsByID[target.ID]; duplicate && !equalTargets(previous, target) {
				return FileAPIReply{}, fmt.Errorf("%w: conflicting duplicate target identity %q", ErrFileAPIReply, target.ID)
			}
			targetsByID[target.ID] = target
		}
	}
	for _, target := range targetsByID {
		result.Targets = append(result.Targets, target)
	}
	sort.Slice(result.Targets, func(first, second int) bool {
		if result.Targets[first].Name != result.Targets[second].Name {
			return result.Targets[first].Name < result.Targets[second].Name
		}
		return result.Targets[first].ID < result.Targets[second].ID
	})
	result.Configurations = sortedUniqueStrings(result.Configurations)

	cmakeSource, err := canonicalAllowedPath(cmakeFiles.Paths.Source, reader.allowedRoots)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: cmakeFiles source path: %v", ErrFileAPIBoundary, err)
	}
	cmakeBuild, err := canonicalAllowedPath(cmakeFiles.Paths.Build, reader.allowedRoots)
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: cmakeFiles build path: %v", ErrFileAPIBoundary, err)
	}
	if canonicalPortablePath(cmakeSource) != canonicalPortablePath(sourcePath) ||
		canonicalPortablePath(cmakeBuild) != canonicalPortablePath(buildPath) {
		return FileAPIReply{}, fmt.Errorf("%w: cmakeFiles paths do not match codemodel", ErrFileAPIReply)
	}
	cache, err := reader.snapshotAllowed(filepath.Join(buildPath, "CMakeCache.txt"))
	if err != nil {
		return FileAPIReply{}, fmt.Errorf("%w: CMake cache: %v", ErrFileAPIReply, err)
	}
	result.Cache = cache
	if len(cmakeFiles.Inputs) > maxFileAPIInputs {
		return FileAPIReply{}, fmt.Errorf("%w: CMake inputs exceed %d", ErrFileAPILimit, maxFileAPIInputs)
	}
	for _, input := range cmakeFiles.Inputs {
		resolved, err := resolveFileAPIPath(input.Path, sourcePath, reader.allowedRoots)
		if err != nil {
			return FileAPIReply{}, fmt.Errorf("%w: CMake input: %v", ErrFileAPIBoundary, err)
		}
		result.CMakeInputs = append(result.CMakeInputs, resolved)
		state, err := reader.snapshotAllowed(resolved)
		if err != nil {
			return FileAPIReply{}, fmt.Errorf("%w: CMake input %q: %v", ErrFileAPIReply, resolved, err)
		}
		result.CMakeInputStates = append(result.CMakeInputStates, state)
	}
	result.CMakeInputs = sortedUniqueStrings(result.CMakeInputs)
	result.CMakeInputStates = sortedUniqueFingerprintFiles(result.CMakeInputStates)

	if len(toolchains.Toolchains) > maxFileAPIToolchains {
		return FileAPIReply{}, fmt.Errorf("%w: toolchains exceed %d", ErrFileAPILimit, maxFileAPIToolchains)
	}
	toolchainByLanguage := make(map[string]string, len(toolchains.Toolchains))
	for _, toolchain := range toolchains.Toolchains {
		if toolchain.Language == "" {
			return FileAPIReply{}, fmt.Errorf("%w: toolchain language is empty", ErrFileAPIReply)
		}
		commandFragment := ""
		if toolchains.Version.Minor != nil && *toolchains.Version.Minor >= 1 {
			if toolchain.Compiler.CommandFragment == nil {
				return FileAPIReply{}, fmt.Errorf(
					"%w: toolchain %q compiler commandFragment is missing",
					ErrFileAPIReply,
					toolchain.Language,
				)
			}
			commandFragment = *toolchain.Compiler.CommandFragment
			if len(commandFragment) > maxCommandFragmentBytes {
				return FileAPIReply{}, fmt.Errorf(
					"%w: toolchain %q compiler commandFragment exceeds %d bytes",
					ErrFileAPILimit,
					toolchain.Language,
					maxCommandFragmentBytes,
				)
			}
		}
		compilerPath := ""
		if toolchain.Compiler.Path != "" {
			compilerPath, err = canonicalAllowedPath(toolchain.Compiler.Path, reader.allowedRoots)
			if err != nil {
				return FileAPIReply{}, fmt.Errorf("%w: toolchain compiler path: %v", ErrFileAPIBoundary, err)
			}
		}
		identity, err := canonicalSHA256(struct {
			Language        string `json:"language"`
			Path            string `json:"path"`
			ID              string `json:"id"`
			Version         string `json:"version"`
			Target          string `json:"target"`
			CommandFragment string `json:"commandFragment"`
		}{
			Language:        toolchain.Language,
			Path:            canonicalPortablePath(compilerPath),
			ID:              toolchain.Compiler.ID,
			Version:         toolchain.Compiler.Version,
			Target:          toolchain.Compiler.Target,
			CommandFragment: commandFragment,
		})
		if err != nil {
			return FileAPIReply{}, fmt.Errorf("%w: construct toolchain identity: %v", ErrFileAPIReply, err)
		}
		if previous, duplicate := toolchainByLanguage[toolchain.Language]; duplicate {
			if previous != identity {
				return FileAPIReply{}, fmt.Errorf(
					"%w: conflicting toolchain descriptors for language %q",
					ErrFileAPIReply,
					toolchain.Language,
				)
			}
			continue
		}
		toolchainByLanguage[toolchain.Language] = identity
		result.ToolchainIDs = append(result.ToolchainIDs, identity)
	}
	result.ToolchainIDs = sortedUniqueStrings(result.ToolchainIDs)
	return result, nil
}

func resolveFileAPIPath(raw, relativeRoot string, allowedRoots []workspace.Root) (string, error) {
	if raw == "" || strings.ContainsRune(raw, 0) || strings.Contains(raw, `\`) {
		return "", fmt.Errorf("invalid File API path %q", raw)
	}
	native := filepath.FromSlash(raw)
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return canonicalAllowedPath(native, allowedRoots)
	}
	root, err := workspace.OpenRoot(relativeRoot)
	if err != nil {
		return "", err
	}
	resolved, err := root.ResolveRelative(native)
	if err != nil {
		return "", err
	}
	return canonicalAllowedPath(resolved, allowedRoots)
}

func targetIdentity(profile BuildProfile, configuration, nativeIdentity string) (string, error) {
	return canonicalSHA256(struct {
		ProjectID     string `json:"projectId"`
		ProfileID     string `json:"profileId"`
		Configuration string `json:"configuration"`
		NativeID      string `json:"nativeId"`
	}{
		ProjectID:     profile.ProjectID,
		ProfileID:     profile.ID,
		Configuration: configuration,
		NativeID:      nativeIdentity,
	})
}

func canonicalSHA256(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func decodeFileAPIJSON(data []byte, destination any) error {
	if err := validatePresetJSONStructure(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func sameFileAPIVersion(first, second fileAPIVersion) bool {
	if first.Major != second.Major || first.Minor == nil || second.Minor == nil {
		return false
	}
	return *first.Minor == *second.Minor
}

func sameFileAPIReference(first, second fileAPIReference) bool {
	return first.Kind == second.Kind && first.JSONFile == second.JSONFile &&
		sameFileAPIVersion(first.Version, second.Version)
}

func formatFileAPIVersion(version fileAPIVersion) string {
	if version.Minor == nil {
		return fmt.Sprintf("%d", version.Major)
	}
	return fmt.Sprintf("%d.%d", version.Major, *version.Minor)
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

func equalTargets(first, second Target) bool {
	firstJSON, firstErr := json.Marshal(first)
	secondJSON, secondErr := json.Marshal(second)
	return firstErr == nil && secondErr == nil && bytes.Equal(firstJSON, secondJSON)
}

func (reader *fileAPIReader) verify() error {
	for key, snapshot := range reader.snapshots {
		if _, err := canonicalAllowedPath(snapshot.path, reader.allowedRoots); err != nil {
			return fmt.Errorf("%w: consumed file escaped allowed roots: %v", ErrFileAPIBoundary, err)
		}
		if _, replyFile := reader.replyFiles[key]; replyFile && !reader.replyRoot.Contains(snapshot.path) {
			return fmt.Errorf("%w: reply file escaped reply root", ErrFileAPIBoundary)
		}
		if err := snapshot.Verify(); err != nil {
			return fmt.Errorf("%w: reply file changed while reading: %v", ErrFileAPIReply, err)
		}
	}
	return nil
}

func (reader *fileAPIReader) states(keys map[string]struct{}) []FingerprintFile {
	result := make([]FingerprintFile, 0, len(keys))
	for key := range keys {
		snapshot := reader.snapshots[key]
		if snapshot != nil {
			result = append(result, fingerprintFileFromSnapshot(snapshot))
		}
	}
	sort.Slice(result, func(first, second int) bool {
		return result[first].Path < result[second].Path
	})
	return result
}

func fingerprintFileFromSnapshot(snapshot *fileSnapshot) FingerprintFile {
	return FingerprintFile{
		Path:     snapshot.path,
		Identity: snapshot.osIdentity,
		SHA256:   snapshot.digest,
	}
}

func sortedUniqueFingerprintFiles(files []FingerprintFile) []FingerprintFile {
	result := append([]FingerprintFile(nil), files...)
	sort.Slice(result, func(first, second int) bool {
		if result[first].Path != result[second].Path {
			return result[first].Path < result[second].Path
		}
		if result[first].Identity != result[second].Identity {
			return result[first].Identity < result[second].Identity
		}
		return result[first].SHA256 < result[second].SHA256
	})
	if len(result) == 0 {
		return []FingerprintFile{}
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

func (reader *fileAPIReader) close() {
	for _, snapshot := range reader.snapshots {
		_ = snapshot.Close()
	}
}
