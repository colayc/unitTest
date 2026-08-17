package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	fixtureVersion   = "4.3.4"
	fixtureStateName = ".unit-test-ide-cmake-fixture.json"
)

var locateFixtureExecutable = os.Executable

type fixtureState struct {
	SourceDir      string `json:"sourceDir"`
	ConfigureCount int    `json:"configureCount"`
	BuildCount     int    `json:"buildCount"`
}

type fixturePresetDocument struct {
	ConfigurePresets []struct {
		Name      string `json:"name"`
		BinaryDir string `json:"binaryDir"`
	} `json:"configurePresets"`
	BuildPresets []struct {
		Name string `json:"name"`
	} `json:"buildPresets"`
}

func main() {
	if isCTestProgram(os.Args[0]) {
		os.Exit(runCTest(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func isCTestProgram(program string) bool {
	return strings.EqualFold(strings.TrimSuffix(filepath.Base(program), filepath.Ext(program)), "ctest")
}

func runCTest(args []string, stdout, stderr io.Writer) int {
	if equalArgs(args, "--version") {
		_, err := fmt.Fprintf(stdout, "ctest version %s\n", fixtureVersion)
		if err == nil {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "ctest-fixture: write version: %v\n", err)
		return 2
	}
	testDir, ok := ctestShowOnlyDirectory(args)
	if ok {
		if err := writeCTestShowOnly(testDir, stdout); err == nil {
			return 0
		} else {
			_, _ = fmt.Fprintf(
				stderr,
				"ctest-fixture: show-only: %v\n",
				err,
			)
			return 2
		}
	}
	_, _ = fmt.Fprintf(stderr, "ctest-fixture: unsupported arguments: %q\n", args)
	return 2
}

func ctestShowOnlyDirectory(args []string) (string, bool) {
	if (len(args) != 3 && len(args) != 5) ||
		args[0] != "--test-dir" ||
		args[len(args)-1] != "--show-only=json-v1" {
		return "", false
	}
	if len(args) == 5 &&
		(args[2] != "-C" || args[3] == "") {
		return "", false
	}
	directory, err := cleanAbsoluteDirectory(args[1])
	return directory, err == nil
}

func writeCTestShowOnly(testDir string, stdout io.Writer) error {
	state, err := readFixtureState(testDir)
	if err != nil {
		return fmt.Errorf("read configured state: %w", err)
	}
	executable := filepath.Join(
		testDir,
		"bin",
		fixtureExecutableName("fixture-app"),
	)
	document := map[string]any{
		"kind": "ctestInfo",
		"version": map[string]any{
			"major": 1,
			"minor": 0,
		},
		"backtraceGraph": map[string]any{
			"commands": []string{"add_test"},
			"files": []string{
				filepath.Join(state.SourceDir, "CMakeLists.txt"),
			},
			"nodes": []map[string]any{{
				"file":    0,
				"line":    4,
				"command": 0,
			}},
		},
		"tests": []map[string]any{{
			"name":   "framework-tests",
			"config": "Debug",
			"command": []string{
				executable,
				"--fixture-scenario",
				"normal",
			},
			"backtrace": 0,
			"properties": []map[string]any{
				{
					"name":  "WORKING_DIRECTORY",
					"value": testDir,
				},
				{
					"name":  "LABELS",
					"value": []string{"deterministic", "cpputest"},
				},
				{
					"name":  "TIMEOUT",
					"value": 30,
				},
			},
		}},
	}
	if err := json.NewEncoder(stdout).Encode(document); err != nil {
		return fmt.Errorf("encode show-only response: %w", err)
	}
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case equalArgs(args, "--version=json-v1"):
		return writeVersion(stdout, stderr)
	case equalArgs(args, "--list-presets=configure"):
		return listPresets(stdout, stderr, false)
	case equalArgs(args, "--build", "--list-presets"):
		return listPresets(stdout, stderr, true)
	case len(args) > 0 && args[0] == "--build":
		if err := build(args, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "cmake-fixture: build: %v\n", err)
			return 2
		}
		return 0
	default:
		if err := configure(args, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "cmake-fixture: unsupported arguments: %v\n", err)
			return 2
		}
		return 0
	}
}

func listPresets(stdout, stderr io.Writer, build bool) int {
	document, err := readPresetDocument()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cmake-fixture: list presets: %v\n", err)
		return 2
	}
	heading := "Available configure presets:\n"
	names := make([]string, 0, len(document.ConfigurePresets))
	for _, preset := range document.ConfigurePresets {
		names = append(names, preset.Name)
	}
	if build {
		heading = "Available build presets:\n"
		names = names[:0]
		for _, preset := range document.BuildPresets {
			names = append(names, preset.Name)
		}
	}
	_, _ = io.WriteString(stdout, heading)
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, "\r\n\"") {
			_, _ = io.WriteString(stderr, "cmake-fixture: preset name is invalid\n")
			return 2
		}
		_, _ = fmt.Fprintf(stdout, "  %q\n", name)
	}
	return 0
}

func readPresetDocument() (fixturePresetDocument, error) {
	data, err := os.ReadFile("CMakePresets.json")
	if errors.Is(err, os.ErrNotExist) {
		return fixturePresetDocument{}, nil
	}
	if err != nil {
		return fixturePresetDocument{}, err
	}
	var document fixturePresetDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fixturePresetDocument{}, fmt.Errorf("decode CMakePresets.json: %w", err)
	}
	return document, nil
}

func equalArgs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func writeVersion(stdout, stderr io.Writer) int {
	document := map[string]any{
		"dependencies": []any{},
		"program": map[string]any{
			"name": "cmake",
			"version": map[string]any{
				"major": 4, "minor": 3, "patch": 4, "string": fixtureVersion,
			},
		},
		"version": map[string]any{"major": 1, "minor": 0},
	}
	if err := json.NewEncoder(stdout).Encode(document); err != nil {
		_, _ = fmt.Fprintf(stderr, "cmake-fixture: write version: %v\n", err)
		return 2
	}
	return 0
}

func configure(args []string, stdout io.Writer) error {
	sourceDir, buildDir, err := configureDirectories(args)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "CMakeLists.txt")); err != nil {
		return fmt.Errorf("source directory has no CMakeLists.txt: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o700); err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}

	state, err := readFixtureState(buildDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	state.SourceDir = sourceDir
	state.ConfigureCount++
	if err := writeFileAPIReply(sourceDir, buildDir); err != nil {
		return err
	}
	if err := writeFixtureState(buildDir, state); err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, "-- cmake-fixture configure complete\n")
	return nil
}

func configureDirectories(args []string) (string, string, error) {
	source, sourceOK := argumentValue(args, "-S")
	build, buildOK := argumentValue(args, "-B")
	if !sourceOK || !buildOK {
		return presetDirectories(args)
	}
	source, err := cleanAbsoluteDirectory(source)
	if err != nil {
		return "", "", fmt.Errorf("source directory: %w", err)
	}
	build, err = cleanAbsoluteDirectory(build)
	if err != nil {
		return "", "", fmt.Errorf("build directory: %w", err)
	}
	return source, build, nil
}

func presetDirectories(args []string) (string, string, error) {
	if len(args) != 2 || args[0] != "--preset" || args[1] == "" {
		return "", "", errors.New("configure requires -S/-B or --preset <name>")
	}
	sourceDir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get preset source directory: %w", err)
	}
	sourceDir, err = cleanAbsoluteDirectory(sourceDir)
	if err != nil {
		return "", "", err
	}
	document, err := readPresetDocument()
	if err != nil {
		return "", "", err
	}
	binaryDir := ""
	for _, preset := range document.ConfigurePresets {
		if preset.Name == args[1] {
			binaryDir = preset.BinaryDir
			break
		}
	}
	if binaryDir == "" {
		return "", "", fmt.Errorf("configure preset %q is absent or has no binaryDir", args[1])
	}
	binaryDir = strings.ReplaceAll(binaryDir, "${sourceDir}", filepath.ToSlash(sourceDir))
	binaryDir = strings.ReplaceAll(binaryDir, "${presetName}", args[1])
	if strings.Contains(binaryDir, "${") {
		return "", "", errors.New("configure preset contains an unsupported macro")
	}
	binaryDir = filepath.FromSlash(binaryDir)
	if !filepath.IsAbs(binaryDir) {
		binaryDir = filepath.Join(sourceDir, binaryDir)
	}
	binaryDir, err = cleanAbsoluteDirectory(binaryDir)
	if err != nil {
		return "", "", err
	}
	return sourceDir, binaryDir, nil
}

func argumentValue(args []string, name string) (string, bool) {
	for index := 0; index < len(args); index++ {
		if args[index] == name && index+1 < len(args) && args[index+1] != "" {
			return args[index+1], true
		}
	}
	return "", false
}

func cleanAbsoluteDirectory(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("path is empty or contains NUL")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func build(args []string, stdout io.Writer) error {
	if len(args) < 2 || args[1] == "" || strings.HasPrefix(args[1], "-") {
		return errors.New("--build requires a build directory")
	}
	buildDir, err := cleanAbsoluteDirectory(args[1])
	if err != nil {
		return err
	}
	state, err := readFixtureState(buildDir)
	if err != nil {
		return fmt.Errorf("read configured state: %w", err)
	}
	state.BuildCount++
	if err := writeFixtureState(buildDir, state); err != nil {
		return err
	}
	if err := materializeFixtureExecutable(buildDir); err != nil {
		return err
	}

	gnuPath := filepath.ToSlash(filepath.Join(state.SourceDir, "main.cpp"))
	msvcPath := filepath.Join(state.SourceDir, "main.cpp")
	_, _ = fmt.Fprintf(stdout, "%s:7:3: warning: deterministic fixture warning [-Wfixture]\n", gnuPath)
	_, _ = fmt.Fprintf(stdout, "%s(8,3): warning C4996: deterministic fixture warning\n", msvcPath)
	return nil
}

func materializeFixtureExecutable(buildDir string) error {
	source, err := locateFixtureExecutable()
	if err != nil {
		return fmt.Errorf("locate CMake fixture: %w", err)
	}
	source = filepath.Join(
		filepath.Dir(source),
		fixtureExecutableName("test-framework-fixture"),
	)
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open test framework fixture: %w", err)
	}
	defer input.Close()
	targetDirectory := filepath.Join(buildDir, "bin")
	if err := os.MkdirAll(targetDirectory, 0o700); err != nil {
		return fmt.Errorf("create fixture target directory: %w", err)
	}
	target := filepath.Join(
		targetDirectory,
		fixtureExecutableName("fixture-app"),
	)
	output, err := os.OpenFile(
		target,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o700,
	)
	if err != nil {
		return fmt.Errorf("create fixture target: %w", err)
	}
	_, copyErr := io.Copy(output, input)
	chmodErr := output.Chmod(0o700)
	closeErr := output.Close()
	if err := errors.Join(copyErr, chmodErr, closeErr); err != nil {
		return fmt.Errorf("copy fixture target: %w", err)
	}
	return nil
}

func readFixtureState(buildDir string) (fixtureState, error) {
	data, err := os.ReadFile(filepath.Join(buildDir, fixtureStateName))
	if err != nil {
		return fixtureState{}, err
	}
	var state fixtureState
	if err := json.Unmarshal(data, &state); err != nil {
		return fixtureState{}, fmt.Errorf("decode fixture state: %w", err)
	}
	if state.SourceDir == "" || state.ConfigureCount < 1 || state.BuildCount < 0 {
		return fixtureState{}, errors.New("invalid fixture state")
	}
	return state, nil
}

func writeFixtureState(buildDir string, state fixtureState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, fixtureStateName), data, 0o600); err != nil {
		return fmt.Errorf("write fixture state: %w", err)
	}
	return nil
}

func writeFileAPIReply(sourceDir, buildDir string) error {
	replyDir := filepath.Join(buildDir, ".cmake", "api", "v1", "reply")
	if err := os.MkdirAll(replyDir, 0o700); err != nil {
		return fmt.Errorf("create File API reply directory: %w", err)
	}
	entries, err := os.ReadDir(replyDir)
	if err != nil {
		return fmt.Errorf("read File API reply directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "index-") && !strings.HasPrefix(entry.Name(), "error-") {
			continue
		}
		if err := os.Remove(filepath.Join(replyDir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale File API candidate: %w", err)
		}
	}

	version := map[string]any{"major": 2, "minor": 9}
	objects := []map[string]any{
		{"kind": "codemodel", "version": version, "jsonFile": "codemodel-v2.json"},
		{"kind": "cache", "version": map[string]any{"major": 2, "minor": 0}, "jsonFile": "cache-v2.json"},
		{"kind": "cmakeFiles", "version": map[string]any{"major": 1, "minor": 1}, "jsonFile": "cmakeFiles-v1.json"},
		{"kind": "toolchains", "version": map[string]any{"major": 1, "minor": 1}, "jsonFile": "toolchains-v1.json"},
	}
	requests := []map[string]any{
		{"kind": "codemodel", "version": map[string]any{"major": 2}},
		{"kind": "cache", "version": map[string]any{"major": 2}},
		{"kind": "cmakeFiles", "version": map[string]any{"major": 1}},
		{"kind": "toolchains", "version": map[string]any{"major": 1}},
	}
	index := map[string]any{
		"objects": objects,
		"reply": map[string]any{
			"client-unit-test-ide": map[string]any{
				"query.json": map[string]any{
					"requests": requests, "responses": objects,
				},
			},
		},
	}
	codemodel := map[string]any{
		"kind": "codemodel", "version": version,
		"paths": map[string]any{"source": sourceDir, "build": buildDir},
		"configurations": []map[string]any{{
			"name": "Debug",
			"targets": []map[string]any{{
				"name": "fixture-app", "id": "fixture-app::@fixture", "jsonFile": "target-fixture-Debug.json",
			}},
		}},
	}
	target := map[string]any{
		"codemodelVersion": version,
		"name":             "fixture-app",
		"id":               "fixture-app::@fixture",
		"type":             "EXECUTABLE",
		"paths":            map[string]any{"source": ".", "build": "."},
		"artifacts": []map[string]any{{
			"path": filepath.ToSlash(
				filepath.Join(
					"bin",
					fixtureExecutableName("fixture-app"),
				),
			),
		}},
	}
	cache := map[string]any{
		"kind": "cache", "version": map[string]any{"major": 2, "minor": 0},
		"entries": []map[string]any{{
			"name": "CMAKE_BUILD_TYPE", "value": "Debug", "type": "STRING",
		}},
	}
	cmakeFiles := map[string]any{
		"kind": "cmakeFiles", "version": map[string]any{"major": 1, "minor": 1},
		"paths":  map[string]any{"source": sourceDir, "build": buildDir},
		"inputs": []map[string]any{{"path": "CMakeLists.txt"}, {"path": "main.cpp"}},
	}
	toolchains := map[string]any{
		"kind": "toolchains", "version": map[string]any{"major": 1, "minor": 1},
		"toolchains": []map[string]any{{
			"language": "CXX",
			"compiler": map[string]any{
				"id": "Fixture", "version": "1.0.0", "target": "fixture-target",
			},
		}},
	}
	files := map[string]any{
		"index-fixture.json":        index,
		"codemodel-v2.json":         codemodel,
		"target-fixture-Debug.json": target,
		"cache-v2.json":             cache,
		"cmakeFiles-v1.json":        cmakeFiles,
		"toolchains-v1.json":        toolchains,
	}
	for name, document := range files {
		if err := writeJSON(filepath.Join(replyDir, name), document); err != nil {
			return err
		}
	}
	cacheContent := "CMAKE_BUILD_TYPE:STRING=Debug\n"
	if err := os.WriteFile(filepath.Join(buildDir, "CMakeCache.txt"), []byte(cacheContent), 0o600); err != nil {
		return fmt.Errorf("write CMakeCache.txt: %w", err)
	}
	return nil
}

func fixtureExecutableName(name string) string {
	if filepath.Ext(os.Args[0]) == ".exe" {
		return name + ".exe"
	}
	return name
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
