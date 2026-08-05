package coveragebundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrBundleIntegrity = errors.New("coverage bundle integrity check failed")

type Installation struct {
	Root           string
	Python         string
	Runner         string
	PythonVersion  string
	GcovrVersion   string
	ManifestSHA256 string
}

type Pin interface {
	Installation() Installation
	Verify() error
	Close() error
}

type resolveHooks struct {
	afterManifest func()
}

func Resolve(productRoot string) (Pin, error) {
	return resolveBundle(productRoot, resolveHooks{})
}

func resolveBundle(productRoot string, hooks resolveHooks) (*bundlePin, error) {
	key, err := currentPlatformKey()
	if err != nil {
		return nil, integrityError("platform", err)
	}
	if productRoot == "" || !filepath.IsAbs(productRoot) || filepath.Clean(productRoot) != productRoot {
		return nil, integrityError("product root", errors.New("must be an absolute canonical path"))
	}
	bundleRoot := filepath.Join(productRoot, "coverage-bundle", key)
	result := &bundlePin{bundleRoot: bundleRoot}
	fail := func(label string, cause error) (*bundlePin, error) {
		_ = result.Close()
		return nil, integrityError(label, cause)
	}

	for _, directory := range []string{productRoot, filepath.Join(productRoot, "coverage-bundle"), bundleRoot} {
		pinned, pinErr := pinDirectObject(directory, true)
		if pinErr != nil {
			return fail("pin bundle directory", pinErr)
		}
		result.directories = append(result.directories, pinned)
	}

	manifestPath := filepath.Join(bundleRoot, manifestName)
	manifestPin, err := pinDirectObject(manifestPath, false)
	if err != nil {
		return fail("pin resolved manifest", err)
	}
	contents, manifestDigest, err := readAndDigest(manifestPin.file, maximumManifestBytes)
	if err != nil {
		_ = manifestPin.Close()
		return fail("read resolved manifest", err)
	}
	manifestPin.digest = manifestDigest
	manifest, err := parseResolvedManifest(contents, key)
	if err != nil {
		_ = manifestPin.Close()
		return fail("validate resolved manifest", err)
	}
	result.manifest = manifest
	result.files = map[string]*pinnedObject{manifestName: manifestPin}
	if hooks.afterManifest != nil {
		hooks.afterManifest()
	}

	readyPin, err := pinDirectObject(filepath.Join(bundleRoot, readyName), false)
	if err != nil {
		return fail("pin READY", err)
	}
	readyContents, readyDigest, err := readAndDigest(readyPin.file, 64)
	if err != nil || string(readyContents) != "ready\n" {
		_ = readyPin.Close()
		if err == nil {
			err = errors.New("READY contents do not match")
		}
		return fail("verify READY", err)
	}
	readyPin.digest = readyDigest
	result.files[readyName] = readyPin

	if err := result.pinTree(); err != nil {
		return fail("pin bundle tree", err)
	}
	if err := result.validateClosedLayout(); err != nil {
		return fail("validate closed bundle layout", err)
	}
	pythonRelative := "python/bin/python3"
	if key == "windows-x64" {
		pythonRelative = "python/python.exe"
	} else if result.files[pythonRelative].identity.Mode().Perm()&0o111 == 0 {
		return fail("validate Python executable", errors.New("Linux Python is not executable"))
	}
	result.installation = Installation{
		Root:           bundleRoot,
		Python:         filepath.Join(bundleRoot, filepath.FromSlash(pythonRelative)),
		Runner:         filepath.Join(bundleRoot, "app", "gcovr-runner.pyz"),
		PythonVersion:  manifest.PythonVersion,
		GcovrVersion:   manifest.GcovrVersion,
		ManifestSHA256: manifestDigest,
	}
	if err := result.Verify(); err != nil {
		return fail("initial verification", err)
	}
	return result, nil
}

func currentPlatformKey() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "windows-x64", nil
	case "linux/amd64":
		return "linux-x64", nil
	default:
		return "", fmt.Errorf("unsupported runtime %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func (pin *bundlePin) pinTree() error {
	return pin.pinDirectoryContents(pin.bundleRoot, "")
}

func (pin *bundlePin) pinDirectoryContents(absolute, relative string) error {
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childRelative := entry.Name()
		if relative != "" {
			childRelative = relative + "/" + entry.Name()
		}
		if !canonicalTreePath(childRelative) {
			return fmt.Errorf("non-canonical bundle entry %q", childRelative)
		}
		childAbsolute := filepath.Join(absolute, entry.Name())
		info, err := directObjectInfo(childAbsolute)
		if err != nil {
			return err
		}
		if info.IsDir() {
			directory, err := pinDirectObject(childAbsolute, true)
			if err != nil {
				return err
			}
			pin.directories = append(pin.directories, directory)
			pin.relativeDirectories = append(pin.relativeDirectories, childRelative)
			if err := pin.pinDirectoryContents(childAbsolute, childRelative); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle entry is not a regular file: %q", childRelative)
		}
		if _, exists := pin.files[childRelative]; exists {
			if err := pin.files[childRelative].verifyIdentity(); err != nil {
				return err
			}
			continue
		}
		file, err := pinDirectObject(childAbsolute, false)
		if err != nil {
			return err
		}
		pin.files[childRelative] = file
	}
	return nil
}

func (pin *bundlePin) validateClosedLayout() error {
	expectedFiles := map[string]string{
		manifestName: pin.files[manifestName].digest,
		readyName:    pin.files[readyName].digest,
	}
	expectedDirectories := map[string]struct{}{"app": {}, "licenses": {}, "python": {}}
	for _, output := range pin.manifest.Outputs {
		expectedFiles[output.Path] = output.SHA256
		for parent := pathParent(output.Path); parent != ""; parent = pathParent(parent) {
			expectedDirectories[parent] = struct{}{}
		}
	}
	if len(pin.files) != len(expectedFiles) {
		return fmt.Errorf("bundle file set has %d entries, expected %d", len(pin.files), len(expectedFiles))
	}
	foldedFiles := map[string]string{}
	for relative, file := range pin.files {
		folded := strings.ToLower(relative)
		if prior, exists := foldedFiles[folded]; exists {
			return fmt.Errorf("case-alias bundle files %q and %q", prior, relative)
		}
		foldedFiles[folded] = relative
		digest, expected := expectedFiles[relative]
		if !expected {
			return fmt.Errorf("unlisted bundle file %q", relative)
		}
		file.digest = digest
		if err := file.verifyDigest(); err != nil {
			return fmt.Errorf("verify %q: %w", relative, err)
		}
	}
	if len(pin.relativeDirectories) != len(expectedDirectories) {
		return fmt.Errorf("bundle directory set has %d entries, expected %d", len(pin.relativeDirectories), len(expectedDirectories))
	}
	foldedDirectories := map[string]string{}
	for _, relative := range pin.relativeDirectories {
		folded := strings.ToLower(relative)
		if prior, exists := foldedDirectories[folded]; exists {
			return fmt.Errorf("case-alias bundle directories %q and %q", prior, relative)
		}
		foldedDirectories[folded] = relative
		if _, expected := expectedDirectories[relative]; !expected {
			return fmt.Errorf("unlisted bundle directory %q", relative)
		}
	}
	return nil
}

func pathParent(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return ""
	}
	return value[:index]
}

func canonicalTreePath(value string) bool {
	return canonicalOutputPath(value) || value == readyName || value == manifestName
}

func integrityError(label string, cause error) error {
	return fmt.Errorf("%w: %s: %v", ErrBundleIntegrity, label, cause)
}
