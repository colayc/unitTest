package cmake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxPresetFileBytes   = 256 * 1024
	maxPresetFiles       = 64
	maxPresetDepth       = 16
	maxPresetTotalBytes  = 1024 * 1024
	maxExpandedPathBytes = 32 * 1024

	presetProbeTimeout   = 5 * time.Second
	presetProbeMaxOutput = 256 * 1024
)

var (
	ErrInvalidPresets = errors.New("invalid CMake presets")
	ErrPresetBoundary = errors.New("CMake preset path is outside workspace root")
	ErrPresetCycle    = errors.New("CMake preset include cycle")
	ErrPresetLimit    = errors.New("CMake preset graph exceeds limit")
	ErrPresetListing  = errors.New("invalid CMake preset listing")
)

type Issue = workspace.Issue

type PresetDiscovery struct {
	Profiles        []BuildProfile
	Inputs          []string
	InputGeneration string
	Issues          []Issue
}

func DiscoverPresets(
	ctx context.Context,
	runner probe.Runner,
	installation Installation,
	root workspace.Root,
	project workspace.ProjectConfig,
) (PresetDiscovery, error) {
	if runner == nil {
		return PresetDiscovery{}, fmt.Errorf("%w: nil probe runner", ErrInvalidPresets)
	}
	sourceDir, err := root.ResolveRelative(project.SourceDir)
	if err != nil {
		return PresetDiscovery{}, fmt.Errorf("%w: resolve project source: %v", ErrPresetBoundary, err)
	}

	scanner := presetScanner{
		root:           root,
		sourceDir:      sourceDir,
		sourceRelative: project.SourceDir,
		state:          make(map[string]presetVisitState),
		documents:      make(map[string]presetDocument),
		snapshots:      make(map[string]*fileSnapshot),
		paths:          make(map[string]presetPath),
	}
	defer scanner.closeSnapshots()
	projectFile, projectExists, err := scanner.rootFile("CMakePresets.json")
	if err != nil {
		return PresetDiscovery{}, err
	}
	userFile, userExists, err := scanner.rootFile("CMakeUserPresets.json")
	if err != nil {
		return PresetDiscovery{}, err
	}
	scanner.projectFile = projectFile
	scanner.projectExists = projectExists

	if projectExists {
		if err := scanner.scan(projectFile, 0); err != nil {
			return PresetDiscovery{}, err
		}
	}
	if userExists {
		if err := scanner.scan(userFile, 0); err != nil {
			return PresetDiscovery{}, err
		}
	}
	if !projectExists && !userExists {
		inputGeneration, err := scanner.inputGeneration()
		if err != nil {
			return PresetDiscovery{}, err
		}
		return PresetDiscovery{InputGeneration: inputGeneration}, nil
	}

	if err := scanner.verifyInputs(); err != nil {
		return PresetDiscovery{}, err
	}
	configureNames, err := runPresetListing(
		ctx,
		runner,
		installation,
		sourceDir,
		[]string{"--list-presets=configure"},
		"Available configure presets:",
	)
	if err != nil {
		return PresetDiscovery{}, err
	}
	if err := scanner.verifyInputs(); err != nil {
		return PresetDiscovery{}, err
	}
	if err := scanner.verifyInputs(); err != nil {
		return PresetDiscovery{}, err
	}
	buildNames, err := runPresetListing(
		ctx,
		runner,
		installation,
		sourceDir,
		[]string{"--build", "--list-presets"},
		"Available build presets:",
	)
	if err != nil {
		return PresetDiscovery{}, err
	}
	if err := scanner.verifyInputs(); err != nil {
		return PresetDiscovery{}, err
	}
	inputGeneration, err := scanner.inputGeneration()
	if err != nil {
		return PresetDiscovery{}, err
	}

	profiles, err := buildPresetProfiles(
		project.ID,
		root,
		sourceDir,
		scanner.documents,
		configureNames,
		buildNames,
		inputGeneration,
	)
	if err != nil {
		return PresetDiscovery{}, err
	}
	inputs := make([]string, 0, len(scanner.documents))
	for key := range scanner.documents {
		relative, err := filepath.Rel(root.NativePath, scanner.paths[key].Raw)
		if err != nil {
			return PresetDiscovery{}, fmt.Errorf("%w: make input workspace-relative: %v", ErrPresetBoundary, err)
		}
		inputs = append(inputs, canonicalRelativePath(relative))
	}
	sort.Strings(inputs)
	return PresetDiscovery{
		Profiles:        profiles,
		Inputs:          inputs,
		InputGeneration: inputGeneration,
	}, nil
}

type presetVisitState uint8

const (
	presetVisiting presetVisitState = iota + 1
	presetVisited
)

type presetScanner struct {
	root           workspace.Root
	sourceDir      string
	sourceRelative string
	projectFile    presetPath
	projectExists  bool
	state          map[string]presetVisitState
	documents      map[string]presetDocument
	snapshots      map[string]*fileSnapshot
	paths          map[string]presetPath
	totalBytes     int
}

type presetPathComponent struct {
	Path     string
	Identity string
}

type presetPath struct {
	Raw      string
	Resolved string
	Ancestry []presetPathComponent
}

func capturePresetPath(root workspace.Root, candidate string) (presetPath, error) {
	raw, err := filepath.Abs(candidate)
	if err != nil {
		return presetPath{}, fmt.Errorf("%w: make preset path absolute: %v", ErrPresetBoundary, err)
	}
	raw = filepath.Clean(raw)
	relative, err := filepath.Rel(root.NativePath, raw)
	if err != nil {
		return presetPath{}, fmt.Errorf("%w: make preset path workspace-relative: %v", ErrPresetBoundary, err)
	}
	if relativeEscapesWorkspace(relative) {
		return presetPath{}, fmt.Errorf("%w: %s", ErrPresetBoundary, raw)
	}

	before, err := capturePresetAncestry(root.NativePath, relative)
	if err != nil {
		return presetPath{}, err
	}
	resolved, err := root.ResolveRelative(relative)
	if err != nil {
		return presetPath{}, fmt.Errorf("%w: %v", ErrPresetBoundary, err)
	}
	after, err := capturePresetAncestry(root.NativePath, relative)
	if err != nil {
		return presetPath{}, err
	}
	if !samePresetAncestry(before, after) {
		return presetPath{}, fmt.Errorf(
			"%w: preset path identity changed while resolving %s",
			ErrInvalidPresets,
			raw,
		)
	}
	return presetPath{Raw: raw, Resolved: resolved, Ancestry: after}, nil
}

func capturePresetAncestry(rootPath, relative string) ([]presetPathComponent, error) {
	paths := []string{filepath.Clean(rootPath)}
	if clean := filepath.Clean(relative); clean != "." {
		current := filepath.Clean(rootPath)
		for _, component := range strings.Split(clean, string(filepath.Separator)) {
			if component == "" || component == "." {
				continue
			}
			current = filepath.Join(current, component)
			paths = append(paths, current)
		}
	}

	ancestry := make([]presetPathComponent, 0, len(paths))
	for _, path := range paths {
		identity, isLink, err := inspectPresetPathComponent(path)
		switch {
		case err == nil && isLink:
			return nil, fmt.Errorf("%w: preset path traverses link or reparse point %s", ErrPresetBoundary, path)
		case err == nil:
			ancestry = append(ancestry, presetPathComponent{Path: path, Identity: identity})
		case errors.Is(err, os.ErrNotExist):
			return ancestry, nil
		default:
			return nil, fmt.Errorf("%w: inspect preset path component %s: %v", ErrInvalidPresets, path, err)
		}
	}
	return ancestry, nil
}

func relativeEscapesWorkspace(relative string) bool {
	clean := filepath.Clean(relative)
	return filepath.IsAbs(clean) ||
		filepath.VolumeName(clean) != "" ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func samePresetAncestry(first, second []presetPathComponent) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if presetPathKey(first[index].Path) != presetPathKey(second[index].Path) ||
			first[index].Identity != second[index].Identity {
			return false
		}
	}
	return true
}

func samePresetPath(first, second presetPath) bool {
	return presetPathKey(first.Raw) == presetPathKey(second.Raw) &&
		presetPathKey(first.Resolved) == presetPathKey(second.Resolved) &&
		samePresetAncestry(first.Ancestry, second.Ancestry)
}

func presetPathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func (scanner *presetScanner) rootFile(name string) (presetPath, bool, error) {
	// Keep the configured source directory lexical. ResolveRelative is used
	// independently for the CMake working directory, while the preset guard
	// must see every reparse point in the path CMake itself will open.
	path, err := scanner.resolve(filepath.Join(
		scanner.root.NativePath,
		filepath.FromSlash(scanner.sourceRelative),
		name,
	))
	if err != nil {
		return presetPath{}, false, err
	}
	key := presetPathKey(path.Raw)
	scanner.paths[key] = path
	info, err := os.Stat(path.Raw)
	switch {
	case err == nil && info.Mode().IsRegular():
		return path, true, nil
	case err == nil:
		return presetPath{}, false, fmt.Errorf("%w: %s is not a regular file", ErrInvalidPresets, name)
	case errors.Is(err, os.ErrNotExist):
		return path, false, nil
	default:
		return presetPath{}, false, fmt.Errorf("%w: inspect %s: %v", ErrInvalidPresets, name, err)
	}
}

func (scanner *presetScanner) scan(path presetPath, depth int) error {
	if depth > maxPresetDepth {
		return fmt.Errorf("%w: include depth exceeds %d", ErrPresetLimit, maxPresetDepth)
	}
	key := presetPathKey(path.Raw)
	switch scanner.state[key] {
	case presetVisiting:
		return fmt.Errorf("%w: %s", ErrPresetCycle, path.Raw)
	case presetVisited:
		return nil
	}
	if len(scanner.documents) >= maxPresetFiles {
		return fmt.Errorf("%w: include graph exceeds %d files", ErrPresetLimit, maxPresetFiles)
	}
	if existing, ok := scanner.paths[key]; ok {
		if !samePresetPath(existing, path) {
			return fmt.Errorf("%w: preset path identity changed before reading %s", ErrInvalidPresets, path.Raw)
		}
	} else {
		scanner.paths[key] = path
	}
	current, err := capturePresetPath(scanner.root, path.Raw)
	if err != nil {
		return err
	}
	if !samePresetPath(path, current) {
		return fmt.Errorf("%w: preset path identity changed before reading %s", ErrInvalidPresets, path.Raw)
	}
	scanner.state[key] = presetVisiting

	document, size, snapshot, err := readPresetDocument(path.Resolved)
	if err != nil {
		delete(scanner.state, key)
		return err
	}
	context := presetMacroContext{
		Version: document.Version,
		FileDir: filepath.Dir(path.Raw),
	}
	for index := range document.ConfigurePresets {
		preset := &document.ConfigurePresets[index]
		preset.context = context
	}
	if scanner.totalBytes+size > maxPresetTotalBytes {
		_ = snapshot.Close()
		delete(scanner.state, key)
		return fmt.Errorf("%w: total input exceeds %d bytes", ErrPresetLimit, maxPresetTotalBytes)
	}
	scanner.totalBytes += size
	scanner.documents[key] = document
	scanner.snapshots[key] = snapshot

	if filepath.Base(path.Raw) == "CMakeUserPresets.json" && scanner.projectExists {
		if err := scanner.scan(scanner.projectFile, depth+1); err != nil {
			return err
		}
	}
	for _, include := range document.Include {
		expanded, err := expandIncludePath(
			include,
			document.Version,
			scanner.sourceDir,
			filepath.Dir(path.Raw),
		)
		if err != nil {
			return err
		}
		native := filepath.FromSlash(expanded)
		candidate := native
		if !filepath.IsAbs(native) && filepath.VolumeName(native) == "" {
			candidate = filepath.Join(filepath.Dir(path.Raw), native)
		}
		includePath, err := scanner.resolve(candidate)
		if err != nil {
			return err
		}
		if err := scanner.scan(includePath, depth+1); err != nil {
			return err
		}
	}
	scanner.state[key] = presetVisited
	return nil
}

func (scanner *presetScanner) resolve(candidate string) (presetPath, error) {
	return capturePresetPath(scanner.root, candidate)
}

func (scanner *presetScanner) verifyInputs() error {
	keys := make([]string, 0, len(scanner.paths))
	for key := range scanner.paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := scanner.verifyInput(key); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *presetScanner) verifyInput(key string) error {
	expected := scanner.paths[key]
	current, err := capturePresetPath(scanner.root, expected.Raw)
	if err != nil {
		if errors.Is(err, ErrPresetBoundary) {
			return err
		}
		return fmt.Errorf("%w: recapture preset input %s: %v", ErrInvalidPresets, expected.Raw, err)
	}
	if !samePresetPath(expected, current) {
		return fmt.Errorf("%w: preset input path %s changed during listing", ErrInvalidPresets, expected.Raw)
	}
	snapshot, exists := scanner.snapshots[key]
	if !exists {
		if _, err := os.Lstat(current.Raw); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("%w: inspect absent preset input %s: %v", ErrInvalidPresets, current.Raw, err)
		}
		return fmt.Errorf("%w: preset input %s appeared during listing", ErrInvalidPresets, current.Raw)
	}
	info, err := directRegularPathInfo(current.Raw)
	if err != nil {
		return fmt.Errorf("%w: preset input %s changed during listing: %v", ErrInvalidPresets, current.Raw, err)
	}
	if !os.SameFile(snapshot.info, info) {
		return fmt.Errorf("%w: preset input %s now names a different file", ErrInvalidPresets, current.Raw)
	}
	if err := snapshot.Verify(); err != nil {
		return fmt.Errorf("%w: preset input %s changed during listing: %v", ErrInvalidPresets, current.Raw, err)
	}
	return nil
}

func (scanner *presetScanner) closeSnapshots() {
	for _, snapshot := range scanner.snapshots {
		_ = snapshot.Close()
	}
}

func (scanner *presetScanner) inputGeneration() (string, error) {
	type inputIdentity struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	payload := struct {
		Inputs []inputIdentity `json:"inputs"`
	}{
		Inputs: make([]inputIdentity, 0, len(scanner.snapshots)),
	}
	for key, snapshot := range scanner.snapshots {
		relative, err := filepath.Rel(scanner.root.NativePath, scanner.paths[key].Raw)
		if err != nil {
			return "", fmt.Errorf("make Preset input workspace-relative: %w", err)
		}
		payload.Inputs = append(payload.Inputs, inputIdentity{
			Path:   canonicalRelativePath(relative),
			SHA256: snapshot.digest,
		})
	}
	sort.Slice(payload.Inputs, func(first, second int) bool {
		if payload.Inputs[first].Path != payload.Inputs[second].Path {
			return payload.Inputs[first].Path < payload.Inputs[second].Path
		}
		return payload.Inputs[first].SHA256 < payload.Inputs[second].SHA256
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Preset input generation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type stringList []string

func (values *stringList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("expected string or string array, got null")
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*values = append((*values)[:0], list...)
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return errors.New("expected string or string array")
	}
	*values = []string{single}
	return nil
}

type presetDocument struct {
	Version          int               `json:"version"`
	Include          stringList        `json:"include"`
	ConfigurePresets []configurePreset `json:"configurePresets"`
	BuildPresets     []buildPreset     `json:"buildPresets"`
}

type configurePreset struct {
	Name        string             `json:"name"`
	Inherits    stringList         `json:"inherits"`
	Generator   string             `json:"generator"`
	BinaryDir   string             `json:"binaryDir"`
	Environment map[string]*string `json:"environment"`
	context     presetMacroContext
}

type buildPreset struct {
	Name            string     `json:"name"`
	Inherits        stringList `json:"inherits"`
	ConfigurePreset string     `json:"configurePreset"`
	Configuration   string     `json:"configuration"`
}

type presetMacroContext struct {
	Version int
	FileDir string
}

type presetMacro struct {
	Family string
	Name   string
}

func expandIncludePath(
	value string,
	version int,
	sourceDir string,
	fileDir string,
) (string, error) {
	expanded, err := expandPresetMacroString(value, func(macro presetMacro) (string, error) {
		switch {
		case version < 7:
			return "", fmt.Errorf("include macros require Preset version 7 or later")
		case version <= 8:
			if macro.Family == "penv" {
				return "", nil
			}
			return "", fmt.Errorf("Preset version %d include only permits $penv macros", version)
		case version > 11:
			return "", fmt.Errorf("Preset version %d include macro semantics are not locked", version)
		}

		if macro.Family == "penv" {
			// Probe specs use an explicit empty environment. Keeping include
			// expansion on the same frozen environment makes the pre-scan and
			// CMake listing observe the same parent environment.
			return "", nil
		}
		if macro.Family != "builtin" {
			return "", fmt.Errorf("%s macro is forbidden in include", macro.Family)
		}
		switch macro.Name {
		case "sourceDir":
			return sourceDir, nil
		case "sourceParentDir":
			return filepath.Dir(sourceDir), nil
		case "sourceDirName":
			return filepath.Base(sourceDir), nil
		case "fileDir":
			return fileDir, nil
		case "hostSystemName":
			return presetHostSystemName(), nil
		case "dollar":
			return "$", nil
		case "pathListSep":
			return string(os.PathListSeparator), nil
		case "presetName", "generator":
			return "", fmt.Errorf("%s is preset-specific and forbidden in include", macro.Name)
		default:
			return "", fmt.Errorf("unknown include macro %q", macro.Name)
		}
	})
	if err != nil {
		return "", fmt.Errorf("%w: expand include %q: %v", ErrInvalidPresets, value, err)
	}
	if expanded == "" {
		return "", fmt.Errorf("%w: include expands to an empty path", ErrInvalidPresets)
	}
	return expanded, nil
}

func expandPresetMacroString(
	value string,
	resolve func(presetMacro) (string, error),
) (string, error) {
	var expanded strings.Builder
	appendValue := func(part string) error {
		if expanded.Len()+len(part) > maxExpandedPathBytes {
			return fmt.Errorf("expanded path exceeds %d bytes", maxExpandedPathBytes)
		}
		expanded.WriteString(part)
		return nil
	}

	for index := 0; index < len(value); {
		if value[index] != '$' {
			next := strings.IndexByte(value[index:], '$')
			if next < 0 {
				if err := appendValue(value[index:]); err != nil {
					return "", err
				}
				break
			}
			if err := appendValue(value[index : index+next]); err != nil {
				return "", err
			}
			index += next
			continue
		}

		family := ""
		nameStart := 0
		switch {
		case strings.HasPrefix(value[index:], "${"):
			family = "builtin"
			nameStart = index + len("${")
		case strings.HasPrefix(value[index:], "$env{"):
			family = "env"
			nameStart = index + len("$env{")
		case strings.HasPrefix(value[index:], "$penv{"):
			family = "penv"
			nameStart = index + len("$penv{")
		case strings.HasPrefix(value[index:], "$vendor{"):
			family = "vendor"
			nameStart = index + len("$vendor{")
		default:
			return "", fmt.Errorf("unknown or malformed macro at byte %d", index)
		}
		closingOffset := strings.IndexByte(value[nameStart:], '}')
		if closingOffset < 0 {
			return "", fmt.Errorf("unterminated macro at byte %d", index)
		}
		closing := nameStart + closingOffset
		name := value[nameStart:closing]
		if name == "" || strings.ContainsAny(name, "${}") {
			return "", fmt.Errorf("empty or nested macro at byte %d", index)
		}
		replacement, err := resolve(presetMacro{Family: family, Name: name})
		if err != nil {
			return "", err
		}
		if err := appendValue(replacement); err != nil {
			return "", err
		}
		index = closing + 1
	}
	return expanded.String(), nil
}

func presetHostSystemName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	default:
		if runtime.GOOS == "" {
			return ""
		}
		return strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]
	}
}

func readPresetDocument(path string) (presetDocument, int, *fileSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return presetDocument{}, 0, nil, fmt.Errorf("%w: inspect %s: %v", ErrInvalidPresets, path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return presetDocument{}, 0, nil, fmt.Errorf("%w: %s is not a direct regular file", ErrInvalidPresets, path)
	}
	if info.Size() > maxPresetFileBytes {
		return presetDocument{}, 0, nil, fmt.Errorf(
			"%w: %s exceeds %d bytes",
			ErrPresetLimit,
			path,
			maxPresetFileBytes,
		)
	}
	snapshot, err := captureFileSnapshot(path, maxPresetFileBytes)
	if err != nil {
		return presetDocument{}, 0, nil, fmt.Errorf("%w: pin %s: %v", ErrInvalidPresets, path, err)
	}
	fail := func(result error) (presetDocument, int, *fileSnapshot, error) {
		_ = snapshot.Close()
		return presetDocument{}, 0, nil, result
	}
	data, err := snapshot.ReadAll(maxPresetFileBytes)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			return fail(fmt.Errorf("%w: %s: %v", ErrPresetLimit, path, err))
		}
		return fail(fmt.Errorf("%w: read %s: %v", ErrInvalidPresets, path, err))
	}
	if err := validatePresetJSONStructure(data); err != nil {
		return fail(fmt.Errorf("%w: decode %s: %v", ErrInvalidPresets, path, err))
	}

	var document presetDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return fail(fmt.Errorf("%w: decode %s: %v", ErrInvalidPresets, path, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fail(fmt.Errorf("%w: decode %s: %v", ErrInvalidPresets, path, err))
	}
	if document.Version <= 0 {
		return fail(fmt.Errorf("%w: %s has invalid version", ErrInvalidPresets, path))
	}
	for _, include := range document.Include {
		if include == "" {
			return fail(fmt.Errorf("%w: %s has invalid include %q", ErrPresetBoundary, path, include))
		}
	}
	return document, len(data), snapshot, nil
}

func validatePresetJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validatePresetJSONValue(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing token %v", token)
	}
	return nil
}

func validatePresetJSONValue(decoder *json.Decoder, depth int) error {
	const maxJSONDepth = 128
	if depth > maxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d", maxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key has type %T", keyToken)
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			keys[key] = struct{}{}
			if err := validatePresetJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object closed by %v", closing)
		}
	case '[':
		for decoder.More() {
			if err := validatePresetJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array closed by %v", closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func runPresetListing(
	ctx context.Context,
	runner probe.Runner,
	installation Installation,
	dir string,
	args []string,
	header string,
) ([]string, error) {
	result, err := runner.Run(ctx, probe.Spec{
		Executable: installation.Executable,
		Args:       append([]string(nil), args...),
		Env:        []string{},
		Dir:        dir,
		Timeout:    presetProbeTimeout,
		MaxOutput:  presetProbeMaxOutput,
	})
	if err != nil {
		return nil, fmt.Errorf("list CMake presets: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf(
			"%w: CMake exited with %d: %s",
			ErrPresetListing,
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	if len(result.Stderr) != 0 {
		return nil, fmt.Errorf("%w: unexpected stderr", ErrPresetListing)
	}
	names, err := parsePresetListing(result.Stdout, header)
	if err != nil {
		return nil, err
	}
	return names, nil
}

func parsePresetListing(output []byte, header string) ([]string, error) {
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != header {
		return nil, fmt.Errorf("%w: missing %q header", ErrPresetListing, header)
	}

	names := make([]string, 0, len(lines)-1)
	seen := make(map[string]struct{}, len(lines)-1)
	started := false
	for _, line := range lines[1:] {
		if line == "" && !started {
			continue
		}
		started = true
		if !strings.HasPrefix(line, `  "`) {
			return nil, fmt.Errorf("%w: unrecognized line %q", ErrPresetListing, line)
		}
		rest := line[len(`  "`):]
		closing := strings.IndexByte(rest, '"')
		if closing <= 0 {
			return nil, fmt.Errorf("%w: malformed machine name %q", ErrPresetListing, line)
		}
		name := rest[:closing]
		suffix := rest[closing+1:]
		if strings.ContainsRune(suffix, '"') {
			return nil, fmt.Errorf("%w: malformed display suffix %q", ErrPresetListing, line)
		}
		if suffix != "" {
			spaces := 0
			for spaces < len(suffix) && suffix[spaces] == ' ' {
				spaces++
			}
			display := suffix[spaces:]
			if spaces == 0 || !strings.HasPrefix(display, "- ") ||
				len(display) == len("- ") ||
				strings.TrimSpace(display[len("- "):]) != display[len("- "):] ||
				hasASCIIControl(display[len("- "):]) {
				return nil, fmt.Errorf("%w: malformed display suffix %q", ErrPresetListing, line)
			}
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate machine name %q", ErrPresetListing, name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func hasASCIIControl(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return true
		}
	}
	return false
}

func buildPresetProfiles(
	projectID string,
	root workspace.Root,
	sourceDir string,
	documents map[string]presetDocument,
	configureNames []string,
	buildNames []string,
	inputGeneration string,
) ([]BuildProfile, error) {
	configures := make(map[string]configurePreset)
	builds := make(map[string]buildPreset)
	for _, document := range documents {
		for _, preset := range document.ConfigurePresets {
			if preset.Name == "" {
				return nil, fmt.Errorf("%w: configure preset has empty name", ErrInvalidPresets)
			}
			if _, duplicate := configures[preset.Name]; duplicate {
				return nil, fmt.Errorf("%w: duplicate configure preset %q", ErrInvalidPresets, preset.Name)
			}
			configures[preset.Name] = preset
		}
		for _, preset := range document.BuildPresets {
			if preset.Name == "" {
				return nil, fmt.Errorf("%w: build preset has empty name", ErrInvalidPresets)
			}
			if _, duplicate := builds[preset.Name]; duplicate {
				return nil, fmt.Errorf("%w: duplicate build preset %q", ErrInvalidPresets, preset.Name)
			}
			builds[preset.Name] = preset
		}
	}

	validConfigures := make(map[string]configurePreset, len(configureNames))
	for _, name := range configureNames {
		resolved, err := resolveConfigurePreset(name, configures, make(map[string]bool))
		if err != nil {
			return nil, err
		}
		validConfigures[name] = resolved
	}

	profiles := make([]BuildProfile, 0, len(buildNames)+len(configureNames))
	configureHasBuild := make(map[string]bool, len(configureNames))
	for _, name := range buildNames {
		build, err := resolveBuildPreset(name, builds, make(map[string]bool))
		if err != nil {
			return nil, err
		}
		configure, ok := validConfigures[build.ConfigurePreset]
		if !ok {
			return nil, fmt.Errorf(
				"%w: listed build preset %q references unlisted configure preset %q",
				ErrPresetListing,
				name,
				build.ConfigurePreset,
			)
		}
		binaryDir, err := resolveBinaryDirectory(
			root,
			sourceDir,
			build.ConfigurePreset,
			configure,
		)
		if err != nil {
			return nil, err
		}
		profile := BuildProfile{
			ProjectID:       projectID,
			Origin:          "preset",
			ConfigurePreset: build.ConfigurePreset,
			BuildPreset:     name,
			Generator:       configure.Generator,
			Configuration:   build.Configuration,
			BinaryDir:       binaryDir,
		}
		profile.ID, err = profileID(profile, inputGeneration)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
		configureHasBuild[build.ConfigurePreset] = true
	}
	for _, name := range configureNames {
		if configureHasBuild[name] {
			continue
		}
		configure := validConfigures[name]
		binaryDir, err := resolveBinaryDirectory(root, sourceDir, name, configure)
		if err != nil {
			return nil, err
		}
		profile := BuildProfile{
			ProjectID:       projectID,
			Origin:          "preset",
			ConfigurePreset: name,
			Generator:       configure.Generator,
			BinaryDir:       binaryDir,
		}
		profile.ID, err = profileID(profile, inputGeneration)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(first, second int) bool {
		if profiles[first].ConfigurePreset != profiles[second].ConfigurePreset {
			return profiles[first].ConfigurePreset < profiles[second].ConfigurePreset
		}
		return profiles[first].BuildPreset < profiles[second].BuildPreset
	})
	return profiles, nil
}

func resolveConfigurePreset(
	name string,
	presets map[string]configurePreset,
	visiting map[string]bool,
) (configurePreset, error) {
	preset, ok := presets[name]
	if !ok {
		return configurePreset{}, fmt.Errorf("%w: listed configure preset %q is absent from inputs", ErrPresetListing, name)
	}
	if visiting[name] {
		return configurePreset{}, fmt.Errorf("%w: configure inheritance cycle at %q", ErrInvalidPresets, name)
	}
	visiting[name] = true
	for _, parentName := range preset.Inherits {
		parent, err := resolveConfigurePreset(parentName, presets, visiting)
		if err != nil {
			return configurePreset{}, err
		}
		if preset.Generator == "" {
			preset.Generator = parent.Generator
		}
		if preset.BinaryDir == "" {
			preset.BinaryDir = parent.BinaryDir
		}
		if preset.Environment == nil {
			preset.Environment = make(map[string]*string)
		}
		for variable, value := range parent.Environment {
			if _, exists := preset.Environment[variable]; !exists {
				preset.Environment[variable] = value
			}
		}
	}
	delete(visiting, name)
	return preset, nil
}

func resolveBinaryDirectory(
	root workspace.Root,
	sourceDir string,
	presetName string,
	preset configurePreset,
) (string, error) {
	if preset.BinaryDir == "" {
		return sourceDir, nil
	}
	environmentVisiting := make(map[string]bool, len(preset.Environment))
	expanded, err := expandPresetMacroString(
		preset.BinaryDir,
		func(macro presetMacro) (string, error) {
			switch macro.Family {
			case "builtin":
				return resolveBinaryBuiltin(
					macro.Name,
					sourceDir,
					presetName,
					preset.Generator,
					preset.context,
				)
			case "env":
				return resolvePresetEnvironment(
					macro.Name,
					preset.Environment,
					sourceDir,
					presetName,
					preset.Generator,
					preset.context,
					environmentVisiting,
					0,
				)
			case "penv":
				return "", fmt.Errorf("$penv is forbidden until the execution environment is frozen")
			default:
				return "", fmt.Errorf("%s macro is not supported in binaryDir", macro.Family)
			}
		},
	)
	if err != nil {
		return "", fmt.Errorf(
			"%w: expand binaryDir for configure preset %q: %v",
			ErrInvalidPresets,
			presetName,
			err,
		)
	}
	native := filepath.FromSlash(expanded)
	candidate := native
	if !filepath.IsAbs(native) && filepath.VolumeName(native) == "" {
		candidate = filepath.Join(sourceDir, native)
	}
	relative, err := filepath.Rel(root.NativePath, candidate)
	if err != nil || relativeEscapesWorkspace(relative) {
		return "", fmt.Errorf(
			"%w: binaryDir for configure preset %q resolves outside workspace root",
			ErrPresetBoundary,
			presetName,
		)
	}
	resolved, err := root.ResolveRelative(relative)
	if err != nil {
		return "", fmt.Errorf(
			"%w: resolve binaryDir for configure preset %q: %v",
			ErrPresetBoundary,
			presetName,
			err,
		)
	}
	return resolved, nil
}

func resolveBinaryBuiltin(
	name string,
	sourceDir string,
	presetName string,
	generator string,
	context presetMacroContext,
) (string, error) {
	switch name {
	case "sourceDir":
		return sourceDir, nil
	case "sourceParentDir":
		return filepath.Dir(sourceDir), nil
	case "sourceDirName":
		return filepath.Base(sourceDir), nil
	case "presetName":
		return presetName, nil
	case "generator":
		return generator, nil
	case "hostSystemName":
		return presetHostSystemName(), nil
	case "fileDir":
		if context.Version < 4 || context.Version > 11 {
			return "", fmt.Errorf(
				"${fileDir} is unsupported for Preset version %d",
				context.Version,
			)
		}
		return context.FileDir, nil
	case "dollar":
		return "$", nil
	case "pathListSep":
		return string(os.PathListSeparator), nil
	default:
		return "", fmt.Errorf("unknown binaryDir macro %q", name)
	}
}

func resolvePresetEnvironment(
	name string,
	environment map[string]*string,
	sourceDir string,
	presetName string,
	generator string,
	context presetMacroContext,
	visiting map[string]bool,
	depth int,
) (string, error) {
	const maxEnvironmentExpansionDepth = 64
	if depth > maxEnvironmentExpansionDepth {
		return "", fmt.Errorf(
			"environment expansion exceeds depth %d",
			maxEnvironmentExpansionDepth,
		)
	}
	value, exists := environment[name]
	if !exists || value == nil {
		// CMake substitutes an empty string for an unset variable. A null
		// child value deliberately removes an inherited variable.
		return "", nil
	}
	if visiting[name] {
		return "", fmt.Errorf("environment expansion cycle at %q", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	return expandPresetMacroString(*value, func(macro presetMacro) (string, error) {
		switch macro.Family {
		case "builtin":
			return resolveBinaryBuiltin(
				macro.Name,
				sourceDir,
				presetName,
				generator,
				context,
			)
		case "env":
			return resolvePresetEnvironment(
				macro.Name,
				environment,
				sourceDir,
				presetName,
				generator,
				context,
				visiting,
				depth+1,
			)
		case "penv":
			return "", fmt.Errorf("$penv is forbidden until the execution environment is frozen")
		default:
			return "", fmt.Errorf("%s macro is not supported in preset environment", macro.Family)
		}
	})
}

func resolveBuildPreset(
	name string,
	presets map[string]buildPreset,
	visiting map[string]bool,
) (buildPreset, error) {
	preset, ok := presets[name]
	if !ok {
		return buildPreset{}, fmt.Errorf("%w: listed build preset %q is absent from inputs", ErrPresetListing, name)
	}
	if visiting[name] {
		return buildPreset{}, fmt.Errorf("%w: build inheritance cycle at %q", ErrInvalidPresets, name)
	}
	visiting[name] = true
	for _, parentName := range preset.Inherits {
		parent, err := resolveBuildPreset(parentName, presets, visiting)
		if err != nil {
			return buildPreset{}, err
		}
		if preset.ConfigurePreset == "" {
			preset.ConfigurePreset = parent.ConfigurePreset
		}
		if preset.Configuration == "" {
			preset.Configuration = parent.Configuration
		}
	}
	delete(visiting, name)
	if preset.ConfigurePreset == "" {
		return buildPreset{}, fmt.Errorf("%w: build preset %q has no configure preset", ErrInvalidPresets, name)
	}
	return preset, nil
}
