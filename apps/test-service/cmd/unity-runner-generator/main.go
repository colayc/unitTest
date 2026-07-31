package main

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

	"unit-test-ide.local/test-service/internal/unityrunner"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	exitSuccess = 0
	exitUsage   = 2
	exitFailure = 3
)

var replacePublishedFile = atomicReplaceFile

type generateOptions struct {
	workspaceRoot string
	buildRoot     string
	manifest      string
	runner        string
	sources       []string
}

type sourceSnapshot struct {
	path   string
	info   os.FileInfo
	size   int64
	sha256 string
}

type outputPath struct {
	relative string
	absolute string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if stdout == nil || stderr == nil {
		return exitUsage
	}
	if len(args) == 1 && args[0] == "--version=json-v1" {
		version := struct {
			SchemaVersion  int    `json:"schemaVersion"`
			Name           string `json:"name"`
			Version        string `json:"version"`
			RunnerProtocol string `json:"runnerProtocol"`
		}{
			SchemaVersion: 1, Name: "unity-runner-generator",
			Version:        unityrunner.CurrentGeneratorVersion,
			RunnerProtocol: "utide.runner.v1",
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(version); err != nil {
			_, _ = fmt.Fprintf(stderr, "unity-runner-generator: write version: %v\n", err)
			return exitFailure
		}
		return exitSuccess
	}
	options, err := parseGenerateOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "unity-runner-generator: %v\n", err)
		return exitUsage
	}
	if err := generate(options); err != nil {
		_, _ = fmt.Fprintf(stderr, "unity-runner-generator: generate: %v\n", err)
		return exitFailure
	}
	return exitSuccess
}

func parseGenerateOptions(args []string) (generateOptions, error) {
	if len(args) == 0 || args[0] != "generate" {
		return generateOptions{}, errors.New("expected generate or --version=json-v1")
	}
	var result generateOptions
	seen := make(map[string]struct{})
	for index := 1; index < len(args); {
		flag := args[index]
		if flag != "--workspace-root" && flag != "--build-root" &&
			flag != "--manifest" && flag != "--runner" && flag != "--source" {
			return generateOptions{}, fmt.Errorf("unknown argument %q", flag)
		}
		if index+1 >= len(args) {
			return generateOptions{}, fmt.Errorf("%s requires a value", flag)
		}
		value := args[index+1]
		if value == "" || strings.IndexByte(value, 0) >= 0 {
			return generateOptions{}, fmt.Errorf("%s has an empty or malformed value", flag)
		}
		if flag != "--source" {
			if _, duplicate := seen[flag]; duplicate {
				return generateOptions{}, fmt.Errorf("%s was specified more than once", flag)
			}
			seen[flag] = struct{}{}
		}
		switch flag {
		case "--workspace-root":
			result.workspaceRoot = value
		case "--build-root":
			result.buildRoot = value
		case "--manifest":
			result.manifest = value
		case "--runner":
			result.runner = value
		case "--source":
			result.sources = append(result.sources, value)
		}
		index += 2
	}
	if result.workspaceRoot == "" || result.buildRoot == "" ||
		result.manifest == "" || result.runner == "" || len(result.sources) == 0 {
		return generateOptions{}, errors.New("generate requires workspace root, build root, runner, manifest, and at least one source")
	}
	if !filepath.IsAbs(result.workspaceRoot) || !filepath.IsAbs(result.buildRoot) {
		return generateOptions{}, errors.New("workspace root and build root must be absolute")
	}
	return result, nil
}

func generate(options generateOptions) error {
	limits := unityrunner.DefaultLimits()
	if len(options.sources) > limits.MaxSources {
		return fmt.Errorf("source count %d exceeds %d", len(options.sources), limits.MaxSources)
	}
	workspaceRoot, err := workspace.OpenRoot(options.workspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}
	buildRoot, err := openDirectRoot(options.buildRoot)
	if err != nil {
		return fmt.Errorf("build root: %w", err)
	}
	runner, err := resolveOutput(buildRoot, options.runner)
	if err != nil {
		return fmt.Errorf("runner output: %w", err)
	}
	manifestOutput, err := resolveOutput(buildRoot, options.manifest)
	if err != nil {
		return fmt.Errorf("manifest output: %w", err)
	}
	if sameNativePath(runner.absolute, manifestOutput.absolute) {
		return errors.New("runner and manifest outputs must be distinct")
	}

	before, err := snapshotDeclaredSources(workspaceRoot, options.sources, limits.MaxSourceBytes)
	if err != nil {
		return err
	}
	for _, source := range before {
		if sameNativePath(source.path, runner.absolute) ||
			sameNativePath(source.path, manifestOutput.absolute) {
			return errors.New("an output path aliases a declared source")
		}
	}
	manifest, err := unityrunner.ParseSources(
		workspaceRoot.NativePath, options.sources, limits,
	)
	if err != nil {
		return err
	}
	runnerBytes, manifestBytes, err := unityrunner.Generate(unityrunner.GenerateInput{
		Manifest: manifest, GeneratorVersion: unityrunner.CurrentGeneratorVersion,
	})
	if err != nil {
		return err
	}
	after, err := snapshotDeclaredSources(workspaceRoot, options.sources, limits.MaxSourceBytes)
	if err != nil {
		return err
	}
	if !sameSourceSnapshots(before, after) {
		return errors.New("declared source changed while generation was in progress")
	}

	if err := revalidateOutput(buildRoot, runner); err != nil {
		return fmt.Errorf("runner output changed before publication: %w", err)
	}
	if err := atomicWriteFile(runner.absolute, runnerBytes, 0o600); err != nil {
		return fmt.Errorf("publish runner: %w", err)
	}
	// The manifest is the publication marker and is intentionally committed last.
	if err := revalidateOutput(buildRoot, manifestOutput); err != nil {
		return fmt.Errorf("manifest output changed before publication: %w", err)
	}
	if err := atomicWriteFile(manifestOutput.absolute, manifestBytes, 0o600); err != nil {
		return fmt.Errorf("publish manifest: %w", err)
	}
	return nil
}

func openDirectRoot(path string) (workspace.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return workspace.Root{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return workspace.Root{}, errors.New("path is not a direct directory")
	}
	return workspace.OpenRoot(path)
}

func resolveOutput(root workspace.Root, relative string) (outputPath, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" ||
		strings.IndexByte(relative, 0) >= 0 {
		return outputPath{}, errors.New("path must be build-root-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(relative, "\\", "/")))
	slash := filepath.ToSlash(clean)
	if clean == "." || slash == ".." || strings.HasPrefix(slash, "../") ||
		portableVolume(slash) {
		return outputPath{}, errors.New("path escapes the build root")
	}
	absolute, err := root.ResolveRelative(clean)
	if err != nil {
		return outputPath{}, err
	}
	parent := filepath.Dir(absolute)
	parentRoot, err := openDirectRoot(parent)
	if err != nil {
		return outputPath{}, fmt.Errorf("output parent: %w", err)
	}
	if !root.Contains(parentRoot.NativePath) {
		return outputPath{}, errors.New("output parent is outside the build root")
	}
	if info, err := os.Lstat(absolute); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return outputPath{}, errors.New("existing output is not a direct regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return outputPath{}, err
	}
	return outputPath{relative: clean, absolute: absolute}, nil
}

func revalidateOutput(root workspace.Root, output outputPath) error {
	current, err := resolveOutput(root, output.relative)
	if err != nil {
		return err
	}
	if !sameNativePath(current.absolute, output.absolute) {
		return errors.New("canonical output path changed")
	}
	return nil
}

func snapshotDeclaredSources(
	root workspace.Root,
	sources []string,
	maximumBytes int64,
) ([]sourceSnapshot, error) {
	if maximumBytes < 1 {
		return nil, errors.New("source byte limit is invalid")
	}
	result := make([]sourceSnapshot, 0, len(sources))
	for _, source := range sources {
		if filepath.IsAbs(source) || portableVolume(source) {
			return nil, errors.New("source paths must be workspace-relative")
		}
		path, err := root.ResolveRelative(filepath.FromSlash(strings.ReplaceAll(source, "\\", "/")))
		if err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, statErr := file.Stat()
		hash := sha256.New()
		copied, copyErr := io.Copy(hash, io.LimitReader(file, maximumBytes+1))
		closeErr := file.Close()
		if statErr != nil || copyErr != nil || closeErr != nil || !info.Mode().IsRegular() {
			return nil, errors.Join(statErr, copyErr, closeErr, errors.New("source snapshot failed"))
		}
		if info.Size() > maximumBytes || copied > maximumBytes {
			return nil, fmt.Errorf("source exceeds %d bytes", maximumBytes)
		}
		result = append(result, sourceSnapshot{
			path: path, info: info, size: info.Size(),
			sha256: hex.EncodeToString(hash.Sum(nil)),
		})
	}
	return result, nil
}

func sameSourceSnapshots(first, second []sourceSnapshot) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !sameNativePath(first[index].path, second[index].path) ||
			!os.SameFile(first[index].info, second[index].info) ||
			first[index].size != second[index].size ||
			first[index].sha256 != second[index].sha256 {
			return false
		}
	}
	return true
}

func atomicWriteFile(destination string, data []byte, mode os.FileMode) (result error) {
	directory := filepath.Dir(destination)
	directoryBefore, err := os.Stat(directory)
	if err != nil || !directoryBefore.IsDir() {
		return errors.Join(err, errors.New("destination directory is unavailable"))
	}
	var destinationBefore os.FileInfo
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("destination is not a direct regular file")
		}
		existing, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		if bytes.Equal(existing, data) {
			return nil
		}
		destinationBefore = info
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	temporary, err := os.CreateTemp(directory, ".utide-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			result = errors.Join(result, temporary.Close())
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			result = errors.Join(result, removeErr)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		temporary = nil
		return err
	}
	temporary = nil

	directoryAfter, err := os.Stat(directory)
	if err != nil || !os.SameFile(directoryBefore, directoryAfter) {
		return errors.Join(err, errors.New("destination directory changed before publication"))
	}
	current, err := os.Lstat(destination)
	switch {
	case destinationBefore == nil && errors.Is(err, os.ErrNotExist):
	case destinationBefore != nil && err == nil && current.Mode().IsRegular() &&
		current.Mode()&os.ModeSymlink == 0 && os.SameFile(destinationBefore, current):
	default:
		return errors.New("destination changed before publication")
	}
	if err := replacePublishedFile(temporaryPath, destination); err != nil {
		return err
	}
	if err := syncOutputDirectory(directory); err != nil {
		return err
	}
	return nil
}

func portableVolume(path string) bool {
	return len(path) >= 2 &&
		(path[0] >= 'A' && path[0] <= 'Z' || path[0] >= 'a' && path[0] <= 'z') &&
		path[1] == ':'
}

func sameNativePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}
