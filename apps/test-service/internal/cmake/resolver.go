package cmake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

var (
	ErrCMakeUnavailable  = errors.New("CMake is unavailable")
	ErrInvalidExecutable = errors.New("invalid CMake executable")
	ErrVersionMismatch   = errors.New("CMake version does not match bundle manifest")

	humanVersionPattern = regexp.MustCompile(`(?m)^cmake version ([^\s]+)\s*$`)
	errJSONSyntax       = errors.New("invalid CMake version JSON syntax")
)

const resolverOutputLimit = 256 * 1024

func Resolve(ctx context.Context, runner probe.Runner, config ResolverConfig) (Installation, error) {
	return resolveWithPolicy(ctx, runner, config, productionManifestPolicy())
}

func resolveWithPolicy(ctx context.Context, runner probe.Runner, config ResolverConfig, policy Manifest) (Installation, error) {
	switch {
	case config.Override != "":
		return resolveStandalone(ctx, runner, config.Override, SourceOverride)
	case config.BundleRoot != "":
		if err := validateManifest(policy); err != nil {
			return Installation{}, fmt.Errorf("%w: invalid resolver policy: %v", ErrInvalidManifest, err)
		}
		return resolveBundle(ctx, runner, config, policy)
	case config.DevExecutable != "":
		return resolveStandalone(ctx, runner, config.DevExecutable, SourceDev)
	default:
		return Installation{}, ErrCMakeUnavailable
	}
}

func resolveStandalone(ctx context.Context, runner probe.Runner, executable, source string) (Installation, error) {
	canonical, snapshot, err := verifyStandaloneExecutable(executable)
	if err != nil {
		return Installation{}, err
	}
	defer snapshot.Close()
	if err := verifyStandaloneSnapshot(canonical, snapshot); err != nil {
		return Installation{}, fmt.Errorf("%w: verify executable before probe: %v", ErrInvalidExecutable, err)
	}
	version, probeErr := probeVersion(ctx, runner, canonical)
	if err := verifyStandaloneSnapshot(canonical, snapshot); err != nil {
		return Installation{}, fmt.Errorf("%w: verify executable after probe: %v", ErrInvalidExecutable, err)
	}
	if probeErr != nil {
		return Installation{}, probeErr
	}
	identity, err := installationIdentity(identityInput{
		Path:    canonical,
		Version: version,
		Source:  source,
		FileIdentity: fileIdentity{
			ExecutableSha256:     snapshot.digest,
			ExecutableOSIdentity: snapshot.osIdentity,
		},
	})
	if err != nil {
		return Installation{}, fmt.Errorf("construct CMake identity: %w", err)
	}
	return Installation{
		Executable: canonical,
		Version:    version,
		Source:     source,
		Identity:   identity,
	}, nil
}

func resolveBundle(ctx context.Context, runner probe.Runner, config ResolverConfig, policy Manifest) (Installation, error) {
	key, err := platformKey(config.Platform, config.Architecture)
	if err != nil {
		return Installation{}, err
	}
	root, err := canonicalBundleRoot(config.BundleRoot)
	if err != nil {
		return Installation{}, err
	}
	manifestPath, err := containedRegularPath(root, "manifest.json")
	if err != nil {
		return Installation{}, fmt.Errorf("%w: manifest: %v", ErrBundleIntegrity, err)
	}
	manifest, manifestSnapshot, err := loadManifestSnapshot(manifestPath)
	if err != nil {
		return Installation{}, err
	}
	snapshots := []*fileSnapshot{manifestSnapshot}
	defer func() { closeFileSnapshots(snapshots) }()
	if !reflect.DeepEqual(manifest, policy) {
		return Installation{}, fmt.Errorf("%w: manifest does not match immutable production policy", ErrInvalidManifest)
	}
	archive := policy.Archives[key]

	platformRelative := filepath.Join(policy.CMakeVersion, key)
	statePath, err := containedRegularPath(root, filepath.Join(platformRelative, "bundle-state.json"))
	if err != nil {
		return Installation{}, fmt.Errorf("%w: bundle state: %v", ErrBundleIntegrity, err)
	}
	stateSnapshot, err := loadBundleStateSnapshot(statePath, policy, key, archive)
	if err != nil {
		return Installation{}, err
	}
	snapshots = append(snapshots, stateSnapshot)
	installRoot, err := containedDirectoryPath(root, filepath.Join(platformRelative, filepath.FromSlash(archive.RootDirectory)))
	if err != nil {
		return Installation{}, fmt.Errorf("%w: install root: %v", ErrBundleIntegrity, err)
	}

	installed := make([]installedFileIdentity, 0, len(archive.InstalledFiles))
	resolvedFiles := make(map[string]string, len(archive.InstalledFiles))
	for relative, expectedDigest := range archive.InstalledFiles {
		filePath, err := containedRegularPath(installRoot, filepath.FromSlash(relative))
		if err != nil {
			return Installation{}, fmt.Errorf("%w: installed file %q: %v", ErrBundleIntegrity, relative, err)
		}
		snapshot, err := captureFileSnapshot(filePath, 0)
		if err != nil {
			return Installation{}, fmt.Errorf("%w: snapshot installed file %q: %v", ErrBundleIntegrity, relative, err)
		}
		snapshots = append(snapshots, snapshot)
		actualDigest := snapshot.digest
		if actualDigest != expectedDigest {
			return Installation{}, fmt.Errorf("%w: installed file %q digest mismatch", ErrBundleIntegrity, relative)
		}
		resolvedFiles[relative] = filePath
		installed = append(installed, installedFileIdentity{
			Path: relative, Sha256: actualDigest, OSIdentity: snapshot.osIdentity,
		})
	}
	sort.Slice(installed, func(left, right int) bool {
		return installed[left].Path < installed[right].Path
	})

	executable := resolvedFiles[archive.Executable]
	executableSnapshot := snapshotsByPath(snapshots)[executable]
	if executableSnapshot == nil {
		return Installation{}, fmt.Errorf("%w: executable snapshot is missing", ErrBundleIntegrity)
	}
	if err := requireExecutableMode(executable, executableSnapshot.info); err != nil {
		return Installation{}, err
	}
	if err := verifyBundleSnapshots(root, snapshots); err != nil {
		return Installation{}, fmt.Errorf("%w: verify bundle before probe: %v", ErrBundleIntegrity, err)
	}
	version, probeErr := probeVersion(ctx, runner, executable)
	if err := verifyBundleSnapshots(root, snapshots); err != nil {
		return Installation{}, fmt.Errorf("%w: verify bundle after probe: %v", ErrBundleIntegrity, err)
	}
	if probeErr != nil {
		return Installation{}, probeErr
	}
	if version != policy.CMakeVersion {
		return Installation{}, fmt.Errorf("%w: got %q, want %q", ErrVersionMismatch, version, policy.CMakeVersion)
	}
	identity, err := installationIdentity(identityInput{
		Path:    executable,
		Version: version,
		Source:  SourceBundle,
		FileIdentity: fileIdentity{
			ExecutableSha256:     archive.InstalledFiles[archive.Executable],
			ExecutableOSIdentity: executableSnapshot.osIdentity,
			ArchiveSha256:        archive.ArchiveSha256,
			InstalledFiles:       installed,
		},
	})
	if err != nil {
		return Installation{}, fmt.Errorf("construct CMake identity: %w", err)
	}
	return Installation{
		Executable:  executable,
		Version:     version,
		Source:      SourceBundle,
		Identity:    identity,
		LicensePath: resolvedFiles[archive.LicensePath],
	}, nil
}

func platformKey(platform, architecture string) (string, error) {
	if architecture != "x64" {
		return "", fmt.Errorf("%w: unsupported architecture %q", ErrCMakeUnavailable, architecture)
	}
	switch platform {
	case "win32":
		return "win32-x64", nil
	case "linux":
		return "linux-x64", nil
	default:
		return "", fmt.Errorf("%w: unsupported platform %q", ErrCMakeUnavailable, platform)
	}
}

func canonicalBundleRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: bundle root must be absolute", ErrBundleIntegrity)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("%w: inspect bundle root: %v", ErrBundleIntegrity, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: bundle root is not a direct directory", ErrBundleIntegrity)
	}
	boundary, err := workspace.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve bundle root: %v", ErrBundleIntegrity, err)
	}
	return boundary.NativePath, nil
}

func verifyStandaloneExecutable(executable string) (string, *fileSnapshot, error) {
	if executable == "" || !filepath.IsAbs(executable) {
		return "", nil, fmt.Errorf("%w: path must be absolute", ErrInvalidExecutable)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return "", nil, fmt.Errorf("%w: inspect path: %v", ErrInvalidExecutable, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("%w: path is not a direct regular file", ErrInvalidExecutable)
	}
	parent, err := workspace.OpenRoot(filepath.Dir(executable))
	if err != nil {
		return "", nil, fmt.Errorf("%w: resolve path: %v", ErrInvalidExecutable, err)
	}
	canonical, err := parent.ResolveRelative(filepath.Base(executable))
	if err != nil {
		return "", nil, fmt.Errorf("%w: resolve executable: %v", ErrInvalidExecutable, err)
	}
	snapshot, err := captureFileSnapshot(canonical, 0)
	if err != nil {
		return "", nil, fmt.Errorf("%w: snapshot executable: %v", ErrInvalidExecutable, err)
	}
	if err := requireExecutableMode(canonical, snapshot.info); err != nil {
		_ = snapshot.Close()
		return "", nil, err
	}
	return canonical, snapshot, nil
}

func requireExecutableMode(filePath string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: executable is not a regular file", ErrInvalidExecutable)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: executable permission bits are not set", ErrInvalidExecutable)
	}
	return nil
}

func containedRegularPath(root, relative string) (string, error) {
	return containedPath(root, relative, false)
}

func containedDirectoryPath(root, relative string) (string, error) {
	return containedPath(root, relative, true)
}

func containedPath(root, relative string, wantDirectory bool) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" ||
		strings.IndexByte(relative, 0) >= 0 {
		return "", fmt.Errorf("invalid relative path")
	}
	boundary, err := workspace.OpenRoot(root)
	if err != nil {
		return "", err
	}
	canonical, err := boundary.ResolveRelative(relative)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if wantDirectory {
		if !info.IsDir() {
			return "", fmt.Errorf("path is not a directory")
		}
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}
	return canonical, nil
}

func closeFileSnapshots(snapshots []*fileSnapshot) {
	for _, snapshot := range snapshots {
		_ = snapshot.Close()
	}
}

func verifyStandaloneSnapshot(canonical string, snapshot *fileSnapshot) error {
	parent, err := workspace.OpenRoot(filepath.Dir(canonical))
	if err != nil {
		return fmt.Errorf("reopen executable parent boundary: %w", err)
	}
	resolved, err := parent.ResolveRelative(filepath.Base(canonical))
	if err != nil {
		return fmt.Errorf("resolve executable in current boundary: %w", err)
	}
	if identityPath(resolved) != identityPath(canonical) {
		return fmt.Errorf("executable containment changed")
	}
	return snapshot.Verify()
}

func verifyBundleSnapshots(root string, snapshots []*fileSnapshot) error {
	boundary, err := workspace.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("reopen bundle boundary: %w", err)
	}
	if identityPath(boundary.NativePath) != identityPath(root) {
		return fmt.Errorf("bundle root identity changed")
	}
	for _, snapshot := range snapshots {
		if !boundary.Contains(snapshot.path) {
			return fmt.Errorf("%q: no longer contained by bundle root", snapshot.path)
		}
		if err := snapshot.Verify(); err != nil {
			return fmt.Errorf("%q: %w", snapshot.path, err)
		}
	}
	return nil
}

func snapshotsByPath(snapshots []*fileSnapshot) map[string]*fileSnapshot {
	result := make(map[string]*fileSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		result[snapshot.path] = snapshot
	}
	return result
}

func probeVersion(ctx context.Context, runner probe.Runner, executable string) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("CMake version probe runner is nil")
	}
	result, err := runner.Run(ctx, versionSpec(executable, "--version=json-v1"))
	if err != nil {
		return "", fmt.Errorf("probe CMake JSON version: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("probe CMake JSON version exited with %d", result.ExitCode)
	}
	version, parseErr := parseJSONVersion(result.Stdout)
	if parseErr == nil {
		return version, nil
	}
	if !errors.Is(parseErr, errJSONSyntax) {
		return "", fmt.Errorf("parse CMake JSON version: %w", parseErr)
	}

	result, err = runner.Run(ctx, versionSpec(executable, "--version"))
	if err != nil {
		return "", fmt.Errorf("probe CMake text version: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("probe CMake text version exited with %d", result.ExitCode)
	}
	version, err = parseHumanVersion(result.Stdout)
	if err != nil {
		return "", fmt.Errorf("parse CMake version: JSON: %v; text: %w", parseErr, err)
	}
	return version, nil
}

func versionSpec(executable, argument string) probe.Spec {
	return probe.Spec{
		Executable: executable,
		Args:       []string{argument},
		Env:        []string{},
		Timeout:    5 * time.Second,
		MaxOutput:  resolverOutputLimit,
	}
}

func parseJSONVersion(output []byte) (string, error) {
	if !json.Valid(output) {
		return "", errJSONSyntax
	}
	var document struct {
		Program struct {
			Name    string `json:"name"`
			Version struct {
				Major  int    `json:"major"`
				Minor  int    `json:"minor"`
				Patch  int    `json:"patch"`
				String string `json:"string"`
			} `json:"version"`
		} `json:"program"`
		Version struct {
			Major int `json:"major"`
			Minor int `json:"minor"`
		} `json:"version"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return "", fmt.Errorf("decode valid CMake version JSON: %w", err)
	}
	if document.Version.Major != 1 || document.Program.Name != "cmake" ||
		!cmakeVersionPattern.MatchString(document.Program.Version.String) {
		return "", fmt.Errorf("output does not match CMake json-v1 version schema")
	}
	want := fmt.Sprintf("%d.%d.%d",
		document.Program.Version.Major,
		document.Program.Version.Minor,
		document.Program.Version.Patch,
	)
	if document.Program.Version.String != want {
		return "", fmt.Errorf("version string %q does not match components %s", document.Program.Version.String, want)
	}
	return document.Program.Version.String, nil
}

func parseHumanVersion(output []byte) (string, error) {
	match := humanVersionPattern.FindSubmatch(output)
	if len(match) != 2 || !cmakeVersionPattern.Match(match[1]) {
		return "", fmt.Errorf("unrecognized CMake version output")
	}
	return string(match[1]), nil
}

type installedFileIdentity struct {
	Path       string `json:"path"`
	Sha256     string `json:"sha256"`
	OSIdentity string `json:"osIdentity"`
}

type fileIdentity struct {
	ExecutableSha256     string                  `json:"executableSha256"`
	ExecutableOSIdentity string                  `json:"executableOsIdentity"`
	ArchiveSha256        string                  `json:"archiveSha256,omitempty"`
	InstalledFiles       []installedFileIdentity `json:"installedFiles,omitempty"`
}

type identityInput struct {
	Path         string       `json:"path"`
	FileIdentity fileIdentity `json:"fileIdentity"`
	Version      string       `json:"version"`
	Source       string       `json:"source"`
}

func installationIdentity(input identityInput) (string, error) {
	input.Path = identityPath(input.Path)
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func identityPath(filePath string) string {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}
