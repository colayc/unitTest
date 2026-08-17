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
	result := &bundlePin{bundleRoot: bundleRoot, files: map[string]*pinnedObject{}, directoryByRelative: map[string]*pinnedObject{}}
	fail := func(label string, cause error) (*bundlePin, error) {
		_ = result.Close()
		return nil, integrityError(label, cause)
	}

	ancestors, err := pinProductRootAncestors(productRoot)
	if err != nil {
		return fail("pin product root ancestors", err)
	}
	for _, ancestor := range ancestors {
		if err := result.resource.reserveHandle(); err != nil {
			for index := len(ancestors) - 1; index >= 0; index-- {
				_ = ancestors[index].Close()
			}
			return fail("pin product root ancestors", err)
		}
		result.directories = append(result.directories, ancestor)
	}
	productRootPin := ancestors[len(ancestors)-1]
	coverageRootPin, err := result.pinChild(productRootPin, "coverage-bundle", true, 0)
	if err != nil {
		return fail("pin coverage bundle root", err)
	}
	result.directories = append(result.directories, coverageRootPin)
	bundleRootPin, err := result.pinChild(coverageRootPin, key, true, 0)
	if err != nil {
		return fail("pin platform bundle root", err)
	}
	result.directories = append(result.directories, bundleRootPin)
	result.bundleRootPin = bundleRootPin
	result.directoryByRelative[""] = bundleRootPin

	manifestPin, err := result.pinChild(bundleRootPin, manifestName, false, maximumManifestBytes)
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
	result.files[manifestName] = manifestPin
	if hooks.afterManifest != nil {
		hooks.afterManifest()
	}

	readyPin, err := result.pinChild(bundleRootPin, readyName, false, maximumReadyBytes)
	if err != nil {
		return fail("pin READY", err)
	}
	readyContents, readyDigest, err := readAndDigest(readyPin.file, maximumReadyBytes)
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
	pythonRelative := "python/bin/python3"
	if key == "windows-x64" {
		pythonRelative = "python/python.exe"
	} else if pythonPin := result.files[pythonRelative]; pythonPin != nil {
		pythonPin.executable = true
	}
	if err := result.validateClosedLayout(); err != nil {
		return fail("validate closed bundle layout", err)
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
	return pin.pinDirectoryContents(pin.bundleRootPin, "")
}

func (pin *bundlePin) pinDirectoryContents(parent *pinnedObject, relative string) error {
	return forEachPinnedDirectoryEntry(parent, func(entry os.DirEntry) error {
		childRelative := entry.Name()
		if relative != "" {
			childRelative = relative + "/" + entry.Name()
		}
		if !canonicalTreePath(childRelative) {
			return fmt.Errorf("non-canonical bundle entry %q", childRelative)
		}
		directoryEntry, err := pinnedChildDirectory(parent, entry.Name())
		if err != nil {
			return err
		}
		if err := pin.resource.recordEntry(directoryEntry); err != nil {
			return err
		}
		if directoryEntry {
			directory, err := pin.pinChild(parent, entry.Name(), true, 0)
			if err != nil {
				return err
			}
			pin.directories = append(pin.directories, directory)
			pin.relativeDirectories = append(pin.relativeDirectories, childRelative)
			pin.directoryByRelative[childRelative] = directory
			if err := pin.pinDirectoryContents(directory, childRelative); err != nil {
				return err
			}
			return nil
		}
		if _, exists := pin.files[childRelative]; exists {
			if err := pin.files[childRelative].verifyIdentity(); err != nil {
				return err
			}
			return nil
		}
		file, err := pin.pinChild(parent, entry.Name(), false, maximumRegularFileBytes)
		if err != nil {
			return err
		}
		pin.files[childRelative] = file
		return nil
	})
}

func (pin *bundlePin) pinChild(parent *pinnedObject, name string, directory bool, maxBytes int64) (*pinnedObject, error) {
	if err := pin.resource.reserveHandle(); err != nil {
		return nil, err
	}
	object, err := pinChildObject(parent, name, directory)
	if err != nil {
		pin.resource.releaseHandle()
		return nil, err
	}
	if !directory {
		object.maxBytes = maxBytes
		if object.identity.Size() < 0 || object.identity.Size() > maxBytes {
			_ = object.Close()
			pin.resource.releaseHandle()
			return nil, errors.New("bundle file size budget exceeded")
		}
		if err := pin.resource.addHashBytes(object.identity.Size()); err != nil {
			_ = object.Close()
			pin.resource.releaseHandle()
			return nil, err
		}
	}
	return object, nil
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
	return verifyPinnedFileSet(pin.files)
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
