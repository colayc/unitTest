package cmake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxPresetFileBytes  = 256 * 1024
	maxPresetFiles      = 64
	maxPresetDepth      = 16
	maxPresetTotalBytes = 1024 * 1024

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
	Profiles []BuildProfile
	Inputs   []string
	Issues   []Issue
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
		root:      root,
		sourceDir: sourceDir,
		state:     make(map[string]presetVisitState),
		documents: make(map[string]presetDocument),
		snapshots: make(map[string]*fileSnapshot),
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

	if userExists && !projectExists {
		return PresetDiscovery{}, fmt.Errorf(
			"%w: CMakeUserPresets.json requires CMakePresets.json",
			ErrInvalidPresets,
		)
	}
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
		return PresetDiscovery{}, nil
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
	if err := scanner.verifySnapshots(); err != nil {
		return PresetDiscovery{}, err
	}

	profiles, err := buildPresetProfiles(project.ID, scanner.documents, configureNames, buildNames)
	if err != nil {
		return PresetDiscovery{}, err
	}
	inputs := make([]string, 0, len(scanner.documents))
	for input := range scanner.documents {
		relative, err := filepath.Rel(root.NativePath, input)
		if err != nil {
			return PresetDiscovery{}, fmt.Errorf("%w: make input workspace-relative: %v", ErrPresetBoundary, err)
		}
		inputs = append(inputs, canonicalRelativePath(relative))
	}
	sort.Strings(inputs)
	return PresetDiscovery{Profiles: profiles, Inputs: inputs}, nil
}

type presetVisitState uint8

const (
	presetVisiting presetVisitState = iota + 1
	presetVisited
)

type presetScanner struct {
	root          workspace.Root
	sourceDir     string
	projectFile   string
	projectExists bool
	state         map[string]presetVisitState
	documents     map[string]presetDocument
	snapshots     map[string]*fileSnapshot
	totalBytes    int
}

func (scanner *presetScanner) rootFile(name string) (string, bool, error) {
	path, err := scanner.resolve(filepath.Join(scanner.sourceDir, name))
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.Mode().IsRegular():
		return path, true, nil
	case err == nil:
		return "", false, fmt.Errorf("%w: %s is not a regular file", ErrInvalidPresets, name)
	case errors.Is(err, os.ErrNotExist):
		return path, false, nil
	default:
		return "", false, fmt.Errorf("%w: inspect %s: %v", ErrInvalidPresets, name, err)
	}
}

func (scanner *presetScanner) scan(path string, depth int) error {
	if depth > maxPresetDepth {
		return fmt.Errorf("%w: include depth exceeds %d", ErrPresetLimit, maxPresetDepth)
	}
	switch scanner.state[path] {
	case presetVisiting:
		return fmt.Errorf("%w: %s", ErrPresetCycle, path)
	case presetVisited:
		return nil
	}
	if len(scanner.documents) >= maxPresetFiles {
		return fmt.Errorf("%w: include graph exceeds %d files", ErrPresetLimit, maxPresetFiles)
	}
	scanner.state[path] = presetVisiting

	document, size, snapshot, err := readPresetDocument(path)
	if err != nil {
		delete(scanner.state, path)
		return err
	}
	if scanner.totalBytes+size > maxPresetTotalBytes {
		_ = snapshot.Close()
		delete(scanner.state, path)
		return fmt.Errorf("%w: total input exceeds %d bytes", ErrPresetLimit, maxPresetTotalBytes)
	}
	scanner.totalBytes += size
	scanner.documents[path] = document
	scanner.snapshots[path] = snapshot

	if filepath.Base(path) == "CMakeUserPresets.json" && scanner.projectExists {
		if err := scanner.scan(scanner.projectFile, depth+1); err != nil {
			return err
		}
	}
	for _, include := range document.Include {
		includePath, err := scanner.resolve(filepath.Join(filepath.Dir(path), filepath.FromSlash(include)))
		if err != nil {
			return err
		}
		if err := scanner.scan(includePath, depth+1); err != nil {
			return err
		}
	}
	scanner.state[path] = presetVisited
	return nil
}

func (scanner *presetScanner) resolve(candidate string) (string, error) {
	relative, err := filepath.Rel(scanner.root.NativePath, candidate)
	if err != nil {
		return "", fmt.Errorf("%w: make workspace-relative: %v", ErrPresetBoundary, err)
	}
	resolved, err := scanner.root.ResolveRelative(relative)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPresetBoundary, err)
	}
	return resolved, nil
}

func (scanner *presetScanner) verifySnapshots() error {
	paths := make([]string, 0, len(scanner.snapshots))
	for path := range scanner.snapshots {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := scanner.snapshots[path].Verify(); err != nil {
			return fmt.Errorf("%w: preset input %s changed during listing: %v", ErrInvalidPresets, path, err)
		}
	}
	return nil
}

func (scanner *presetScanner) closeSnapshots() {
	for _, snapshot := range scanner.snapshots {
		_ = snapshot.Close()
	}
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
	Name      string     `json:"name"`
	Inherits  stringList `json:"inherits"`
	Generator string     `json:"generator"`
	BinaryDir string     `json:"binaryDir"`
}

type buildPreset struct {
	Name            string     `json:"name"`
	Inherits        stringList `json:"inherits"`
	ConfigurePreset string     `json:"configurePreset"`
	Configuration   string     `json:"configuration"`
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
		if include == "" || filepath.IsAbs(filepath.FromSlash(include)) ||
			filepath.VolumeName(filepath.FromSlash(include)) != "" {
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
		if strings.ContainsRune(suffix, '"') ||
			suffix != "" && (!strings.HasPrefix(suffix, " - ") || len(suffix) == len(" - ")) {
			return nil, fmt.Errorf("%w: malformed display suffix %q", ErrPresetListing, line)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate machine name %q", ErrPresetListing, name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func buildPresetProfiles(
	projectID string,
	documents map[string]presetDocument,
	configureNames []string,
	buildNames []string,
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
		profile := BuildProfile{
			ProjectID:       projectID,
			Origin:          "preset",
			ConfigurePreset: build.ConfigurePreset,
			BuildPreset:     name,
			Generator:       configure.Generator,
			Configuration:   build.Configuration,
			BinaryDir:       configure.BinaryDir,
		}
		profile.ID, err = profileID(profile)
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
		profile := BuildProfile{
			ProjectID:       projectID,
			Origin:          "preset",
			ConfigurePreset: name,
			Generator:       configure.Generator,
			BinaryDir:       configure.BinaryDir,
		}
		var err error
		profile.ID, err = profileID(profile)
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
	}
	delete(visiting, name)
	return preset, nil
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
