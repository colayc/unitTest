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
	var manifest Manifest
	if err := decodeStrictFile(filePath, maxManifestSize, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return manifest, nil
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
	var state bundleState
	if err := decodeStrictFile(filePath, maxManifestSize, &state); err != nil {
		return fmt.Errorf("%w: read bundle state: %v", ErrBundleIntegrity, err)
	}
	expected := bundleState{
		SchemaVersion:  manifest.SchemaVersion,
		Key:            key,
		CMakeVersion:   manifest.CMakeVersion,
		ArchiveSha256:  archive.ArchiveSha256,
		InstalledFiles: archive.InstalledFiles,
	}
	if !reflect.DeepEqual(state, expected) {
		return fmt.Errorf("%w: bundle state does not match manifest", ErrBundleIntegrity)
	}
	return nil
}

func decodeStrictFile(filePath string, maximum int64, destination any) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if info.Size() > maximum {
		return fmt.Errorf("file exceeds %d bytes", maximum)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maximum {
		return fmt.Errorf("file exceeds %d bytes", maximum)
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
