package cmake

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

const (
	manifestSchemaVersion = 1
	manifestLicense       = "BSD-3-Clause"
	maxManifestSize       = 256 * 1024
)

var (
	ErrInvalidManifest = errors.New("invalid CMake bundle manifest")
	ErrBundleIntegrity = errors.New("CMake bundle integrity check failed")

	cmakeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

type Manifest struct {
	SchemaVersion int                `json:"schemaVersion"`
	CMakeVersion  string             `json:"cmakeVersion"`
	License       string             `json:"license"`
	Archives      map[string]Archive `json:"archives"`
}

type Archive struct {
	URL            string            `json:"url"`
	ArchiveSha256  string            `json:"archiveSha256"`
	RootDirectory  string            `json:"rootDirectory"`
	Executable     string            `json:"executable"`
	LicensePath    string            `json:"licensePath"`
	InstalledFiles map[string]string `json:"installedFiles"`
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
				URL:           "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip",
				ArchiveSha256: "86e5fcafb38bdf58346a78b187c7b6b4f252ae5242cffe24c463a92bbd2e77d1",
				RootDirectory: "cmake-4.3.4-windows-x86_64",
				Executable:    "bin/cmake.exe",
				LicensePath:   "doc/cmake/LICENSE.rst",
				InstalledFiles: map[string]string{
					"bin/cmake.exe":         "1aa884bf1f4949327fffcc8ee4a97c2d684bdc1d0a64b71f01dc16321c7fbc64",
					"doc/cmake/LICENSE.rst": "cd944d878806fee998ef3f88ca41ec060ae198bd8ba615e284f7d8d90c25593e",
				},
			},
			"linux-x64": {
				URL:           "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
				ArchiveSha256: "ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68",
				RootDirectory: "cmake-4.3.4-linux-x86_64",
				Executable:    "bin/cmake",
				LicensePath:   "doc/cmake/LICENSE.rst",
				InstalledFiles: map[string]string{
					"bin/cmake":             "8542b512ac147329e03de375583665a64f02afb65d6c4665099390be103ac2d0",
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

	var expectedURL, expectedRoot, expectedExecutable string
	switch key {
	case "win32-x64":
		expectedRoot = "cmake-" + version + "-windows-x86_64"
		expectedExecutable = "bin/cmake.exe"
		expectedURL = "https://cmake.org/files/v" + majorMinor + "/" + expectedRoot + ".zip"
	case "linux-x64":
		expectedRoot = "cmake-" + version + "-linux-x86_64"
		expectedExecutable = "bin/cmake"
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
	if archive.LicensePath != "doc/cmake/LICENSE.rst" || !safeManifestPath(archive.LicensePath) {
		return fmt.Errorf("licensePath = %q", archive.LicensePath)
	}
	if len(archive.InstalledFiles) != 2 {
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
