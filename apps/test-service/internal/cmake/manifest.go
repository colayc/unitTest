package cmake

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"unit-test-ide.local/test-service/internal/unityrunner"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	manifestSchemaVersion = 1
	manifestLicense       = "BSD-3-Clause"
	maxManifestSize       = 256 * 1024
	productManifestName   = "product-manifest.json"
)

var (
	ErrInvalidManifest        = errors.New("invalid CMake bundle manifest")
	ErrBundleIntegrity        = errors.New("CMake bundle integrity check failed")
	ErrInvalidProductManifest = errors.New("invalid product installation manifest")
	ErrProductIntegrity       = errors.New("product installation integrity check failed")

	cmakeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

type ProductManifest struct {
	SchemaVersion        int                       `json:"schemaVersion"`
	UnityRunnerGenerator ProductExecutableManifest `json:"unityRunnerGenerator"`
}

type ProductExecutableManifest struct {
	RelativePath string `json:"relativePath"`
	Version      string `json:"version"`
	SHA256       string `json:"sha256"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
}

type ProductExecutable struct {
	Path         string
	RelativePath string
	Version      string
	SHA256       string
	Platform     string
	Architecture string
	Identity     string
}

func (executable ProductExecutable) Valid() bool {
	entry := ProductExecutableManifest{
		RelativePath: executable.RelativePath,
		Version:      executable.Version,
		SHA256:       executable.SHA256,
		Platform:     executable.Platform,
		Architecture: executable.Architecture,
	}
	return executable.Path != "" && filepath.IsAbs(executable.Path) &&
		filepath.Clean(executable.Path) == executable.Path &&
		validateProductExecutableManifest(entry, entry.Platform, entry.Architecture) == nil &&
		executable.Identity == ProductExecutableIdentity(entry)
}

func ProductExecutableIdentity(entry ProductExecutableManifest) string {
	identity, err := canonicalSHA256(struct {
		Kind         string `json:"kind"`
		RelativePath string `json:"relativePath"`
		Version      string `json:"version"`
		SHA256       string `json:"sha256"`
		Platform     string `json:"platform"`
		Architecture string `json:"architecture"`
	}{
		Kind:         "unity-runner-generator",
		RelativePath: entry.RelativePath,
		Version:      entry.Version, SHA256: entry.SHA256,
		Platform: entry.Platform, Architecture: entry.Architecture,
	})
	if err != nil {
		return ""
	}
	return identity
}

func ResolveUnityRunnerGenerator(productRoot, platform, architecture string) (ProductExecutable, error) {
	if productRoot == "" || !filepath.IsAbs(productRoot) {
		return ProductExecutable{}, fmt.Errorf("%w: product root must be absolute", ErrInvalidProductManifest)
	}
	rootInfo, err := os.Lstat(productRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ProductExecutable{}, fmt.Errorf("%w: product root is not a direct directory", ErrInvalidProductManifest)
	}
	root, err := workspace.OpenRoot(productRoot)
	if err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: open product root: %v", ErrInvalidProductManifest, err)
	}
	manifestPath := filepath.Join(root.NativePath, productManifestName)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return ProductExecutable{}, fmt.Errorf("%w: product manifest is not a direct regular file", ErrInvalidProductManifest)
	}
	var manifest ProductManifest
	if err := decodeStrictFile(manifestPath, maxManifestSize, &manifest); err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: %v", ErrInvalidProductManifest, err)
	}
	if manifest.SchemaVersion != 1 {
		return ProductExecutable{}, fmt.Errorf("%w: schemaVersion = %d", ErrInvalidProductManifest, manifest.SchemaVersion)
	}
	entry := manifest.UnityRunnerGenerator
	if err := validateProductExecutableManifest(entry, platform, architecture); err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: Unity runner generator: %v", ErrInvalidProductManifest, err)
	}
	executablePath, err := directProductFile(root, filepath.FromSlash(entry.RelativePath))
	if err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: Unity runner generator: %v", ErrProductIntegrity, err)
	}
	snapshot, err := captureFileSnapshot(executablePath, 0)
	if err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: snapshot Unity runner generator: %v", ErrProductIntegrity, err)
	}
	defer snapshot.Close()
	if snapshot.digest != entry.SHA256 {
		return ProductExecutable{}, fmt.Errorf("%w: Unity runner generator digest mismatch", ErrProductIntegrity)
	}
	if err := requireExecutableMode(executablePath, snapshot.info); err != nil {
		return ProductExecutable{}, fmt.Errorf("%w: %v", ErrProductIntegrity, err)
	}
	result := ProductExecutable{
		Path: executablePath, RelativePath: entry.RelativePath,
		Version: entry.Version, SHA256: entry.SHA256,
		Platform: entry.Platform, Architecture: entry.Architecture,
		Identity: ProductExecutableIdentity(entry),
	}
	if !result.Valid() {
		return ProductExecutable{}, fmt.Errorf("%w: resolved generator identity is invalid", ErrProductIntegrity)
	}
	return result, nil
}

func validateProductExecutableManifest(
	entry ProductExecutableManifest,
	platform string,
	architecture string,
) error {
	if platform != "win32" && platform != "linux" {
		return fmt.Errorf("unsupported platform %q", platform)
	}
	if architecture != "x64" {
		return fmt.Errorf("unsupported architecture %q", architecture)
	}
	if entry.Platform != platform || entry.Architecture != architecture {
		return fmt.Errorf("platform or architecture does not match the installation")
	}
	if entry.RelativePath != expectedUnityRunnerGeneratorPath(platform) ||
		!safeManifestPath(entry.RelativePath) {
		return fmt.Errorf("relativePath = %q", entry.RelativePath)
	}
	if entry.Version != unityrunner.CurrentGeneratorVersion {
		return fmt.Errorf("version = %q", entry.Version)
	}
	if !validSHA256(entry.SHA256) {
		return fmt.Errorf("invalid SHA-256")
	}
	return nil
}

func expectedUnityRunnerGeneratorPath(platform string) string {
	if platform == "win32" {
		return "bin/unity-runner-generator.exe"
	}
	return "bin/unity-runner-generator"
}

func directProductFile(root workspace.Root, relative string) (string, error) {
	current := root.NativePath
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("path contains a symbolic link or junction")
		}
	}
	canonical, err := root.ResolveRelative(relative)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("path is not a direct regular file")
	}
	return canonical, nil
}

type Manifest struct {
	SchemaVersion int                `json:"schemaVersion"`
	CMakeVersion  string             `json:"cmakeVersion"`
	License       string             `json:"license"`
	Archives      map[string]Archive `json:"archives"`
}

type Archive struct {
	URL             string            `json:"url"`
	ArchiveSha256   string            `json:"archiveSha256"`
	RootDirectory   string            `json:"rootDirectory"`
	Executable      string            `json:"executable"`
	CTestExecutable string            `json:"ctestExecutable"`
	LicensePath     string            `json:"licensePath"`
	InstalledFiles  map[string]string `json:"installedFiles"`
}

type bundleState struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Key            string            `json:"key"`
	CMakeVersion   string            `json:"cmakeVersion"`
	ArchiveSha256  string            `json:"archiveSha256"`
	InstalledFiles map[string]string `json:"installedFiles"`
}

func loadManifest(filePath string) (Manifest, error) {
	manifest, snapshot, err := loadManifestSnapshot(filePath)
	if snapshot != nil {
		_ = snapshot.Close()
	}
	return manifest, err
}

func loadManifestSnapshot(filePath string) (Manifest, *fileSnapshot, error) {
	var manifest Manifest
	snapshot, err := captureFileSnapshot(filePath, maxManifestSize)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	fail := func(result error) (Manifest, *fileSnapshot, error) {
		_ = snapshot.Close()
		return Manifest{}, nil, result
	}
	if err := decodeStrictSnapshot(snapshot, maxManifestSize, &manifest); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrInvalidManifest, err))
	}
	if err := validateManifest(manifest); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrInvalidManifest, err))
	}
	return manifest, snapshot, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != manifestSchemaVersion {
		return fmt.Errorf("schemaVersion = %d", manifest.SchemaVersion)
	}
	if !cmakeVersionPattern.MatchString(manifest.CMakeVersion) {
		return fmt.Errorf("invalid cmakeVersion %q", manifest.CMakeVersion)
	}
	if manifest.License != manifestLicense {
		return fmt.Errorf("license = %q", manifest.License)
	}
	if len(manifest.Archives) != 2 {
		return fmt.Errorf("archives has %d entries", len(manifest.Archives))
	}

	for _, key := range []string{"win32-x64", "linux-x64"} {
		archive, ok := manifest.Archives[key]
		if !ok {
			return fmt.Errorf("missing archive %q", key)
		}
		if err := validateArchive(manifest.CMakeVersion, key, archive); err != nil {
			return fmt.Errorf("archive %q: %w", key, err)
		}
	}
	return nil
}

func productionManifestPolicy() Manifest {
	return Manifest{
		SchemaVersion: 1,
		CMakeVersion:  "4.3.4",
		License:       "BSD-3-Clause",
		Archives: map[string]Archive{
			"win32-x64": {
				URL:             "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip",
				ArchiveSha256:   "86e5fcafb38bdf58346a78b187c7b6b4f252ae5242cffe24c463a92bbd2e77d1",
				RootDirectory:   "cmake-4.3.4-windows-x86_64",
				Executable:      "bin/cmake.exe",
				CTestExecutable: "bin/ctest.exe",
				LicensePath:     "doc/cmake/LICENSE.rst",
				InstalledFiles: map[string]string{
					"bin/cmake.exe":         "1aa884bf1f4949327fffcc8ee4a97c2d684bdc1d0a64b71f01dc16321c7fbc64",
					"bin/ctest.exe":         "73baacbeb272ca6f40422b4f789403390af678beb491783cef1727d69cd3e1cb",
					"doc/cmake/LICENSE.rst": "cd944d878806fee998ef3f88ca41ec060ae198bd8ba615e284f7d8d90c25593e",
				},
			},
			"linux-x64": {
				URL:             "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
				ArchiveSha256:   "ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68",
				RootDirectory:   "cmake-4.3.4-linux-x86_64",
				Executable:      "bin/cmake",
				CTestExecutable: "bin/ctest",
				LicensePath:     "doc/cmake/LICENSE.rst",
				InstalledFiles: map[string]string{
					"bin/cmake":             "8542b512ac147329e03de375583665a64f02afb65d6c4665099390be103ac2d0",
					"bin/ctest":             "189eaf845c588c3dabe9862dad16ca0b1f62ed6155e064692e811e6f14fbd6c7",
					"doc/cmake/LICENSE.rst": "4382e7c1879ac90e3f101a395d23846fa4dbcaa1eed7265b43681e348754825d",
				},
			},
		},
	}
}

func validateArchive(version, key string, archive Archive) error {
	majorMinor := version
	if separator := strings.LastIndexByte(majorMinor, '.'); separator >= 0 {
		majorMinor = majorMinor[:separator]
	}

	var expectedURL, expectedRoot, expectedExecutable, expectedCTestExecutable string
	switch key {
	case "win32-x64":
		expectedRoot = "cmake-" + version + "-windows-x86_64"
		expectedExecutable = "bin/cmake.exe"
		expectedCTestExecutable = "bin/ctest.exe"
		expectedURL = "https://cmake.org/files/v" + majorMinor + "/" + expectedRoot + ".zip"
	case "linux-x64":
		expectedRoot = "cmake-" + version + "-linux-x86_64"
		expectedExecutable = "bin/cmake"
		expectedCTestExecutable = "bin/ctest"
		expectedURL = "https://cmake.org/files/v" + majorMinor + "/" + expectedRoot + ".tar.gz"
	default:
		return fmt.Errorf("unsupported platform key")
	}
	if archive.URL != expectedURL {
		return fmt.Errorf("url = %q", archive.URL)
	}
	parsedURL, err := url.Parse(archive.URL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "cmake.org" ||
		parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("url is not the fixed HTTPS cmake.org location")
	}
	if !validSHA256(archive.ArchiveSha256) {
		return fmt.Errorf("invalid archiveSha256")
	}
	if archive.RootDirectory != expectedRoot || !safeManifestRoot(archive.RootDirectory) {
		return fmt.Errorf("rootDirectory = %q", archive.RootDirectory)
	}
	if archive.Executable != expectedExecutable || !safeManifestPath(archive.Executable) {
		return fmt.Errorf("executable = %q", archive.Executable)
	}
	if archive.CTestExecutable != expectedCTestExecutable ||
		!safeManifestPath(archive.CTestExecutable) {
		return fmt.Errorf("ctestExecutable = %q", archive.CTestExecutable)
	}
	if archive.LicensePath != "doc/cmake/LICENSE.rst" || !safeManifestPath(archive.LicensePath) {
		return fmt.Errorf("licensePath = %q", archive.LicensePath)
	}
	if len(archive.InstalledFiles) != 3 {
		return fmt.Errorf("installedFiles has %d entries", len(archive.InstalledFiles))
	}
	for relative, digest := range archive.InstalledFiles {
		if !safeManifestPath(relative) {
			return fmt.Errorf("unsafe installed file %q", relative)
		}
		if !validSHA256(digest) {
			return fmt.Errorf("invalid installed file digest for %q", relative)
		}
	}
	if _, ok := archive.InstalledFiles[archive.Executable]; !ok {
		return fmt.Errorf("executable is missing from installedFiles")
	}
	if _, ok := archive.InstalledFiles[archive.CTestExecutable]; !ok {
		return fmt.Errorf("ctestExecutable is missing from installedFiles")
	}
	if _, ok := archive.InstalledFiles[archive.LicensePath]; !ok {
		return fmt.Errorf("license is missing from installedFiles")
	}
	return nil
}

func loadBundleState(filePath string, manifest Manifest, key string, archive Archive) error {
	snapshot, err := loadBundleStateSnapshot(filePath, manifest, key, archive)
	if snapshot != nil {
		_ = snapshot.Close()
	}
	return err
}

func loadBundleStateSnapshot(filePath string, manifest Manifest, key string, archive Archive) (*fileSnapshot, error) {
	var state bundleState
	snapshot, err := captureFileSnapshot(filePath, maxManifestSize)
	if err != nil {
		return nil, fmt.Errorf("%w: read bundle state: %v", ErrBundleIntegrity, err)
	}
	fail := func(result error) (*fileSnapshot, error) {
		_ = snapshot.Close()
		return nil, result
	}
	if err := decodeStrictSnapshot(snapshot, maxManifestSize, &state); err != nil {
		return fail(fmt.Errorf("%w: read bundle state: %v", ErrBundleIntegrity, err))
	}
	expected := bundleState{
		SchemaVersion:  manifest.SchemaVersion,
		Key:            key,
		CMakeVersion:   manifest.CMakeVersion,
		ArchiveSha256:  archive.ArchiveSha256,
		InstalledFiles: archive.InstalledFiles,
	}
	if !reflect.DeepEqual(state, expected) {
		return fail(fmt.Errorf("%w: bundle state does not match immutable policy", ErrBundleIntegrity))
	}
	return snapshot, nil
}

func decodeStrictFile(filePath string, maximum int64, destination any) error {
	snapshot, err := captureFileSnapshot(filePath, maximum)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	return decodeStrictSnapshot(snapshot, maximum, destination)
}

func decodeStrictSnapshot(snapshot *fileSnapshot, maximum int64, destination any) error {
	data, err := snapshot.ReadAll(maximum)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func safeManifestRoot(value string) bool {
	return safeManifestPath(value) && path.Base(value) == value
}

func safeManifestPath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") || filepath.VolumeName(value) != "" {
		return false
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || strings.Contains(component, ":") {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
