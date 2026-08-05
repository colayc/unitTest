package coveragebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

const maximumManifestBytes = 16 * 1024 * 1024

type pinnedObject struct {
	path      string
	file      *os.File
	identity  os.FileInfo
	directory bool
	digest    string
}

type bundlePin struct {
	mu                  sync.Mutex
	installation        Installation
	bundleRoot          string
	manifest            resolvedManifest
	directories         []*pinnedObject
	relativeDirectories []string
	files               map[string]*pinnedObject
	closed              bool
	verifyHook          func()
}

func (pin *bundlePin) Installation() Installation {
	if pin == nil {
		return Installation{}
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	return pin.installation
}

func (pin *bundlePin) Verify() error {
	if pin == nil {
		return integrityError("verify pin", errors.New("nil pin"))
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.closed {
		return integrityError("verify pin", errors.New("pin is closed"))
	}
	if err := pin.verifyPass(); err != nil {
		return integrityError("verify before use", err)
	}
	if pin.verifyHook != nil {
		hook := pin.verifyHook
		pin.verifyHook = nil
		hook()
	}
	if err := pin.verifyPass(); err != nil {
		return integrityError("verify after use", err)
	}
	return nil
}

func (pin *bundlePin) verifyPass() error {
	for _, directory := range pin.directories {
		if err := directory.verifyIdentity(); err != nil {
			return fmt.Errorf("directory identity: %w", err)
		}
	}
	paths := make([]string, 0, len(pin.files))
	for relative := range pin.files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		if err := pin.files[relative].verifyDigest(); err != nil {
			return fmt.Errorf("file %q: %w", relative, err)
		}
	}
	if err := pin.verifyCurrentTree(); err != nil {
		return err
	}
	contents, digest, err := readAndDigest(pin.files[manifestName].file, maximumManifestBytes)
	if err != nil || digest != pin.installation.ManifestSHA256 {
		return errors.New("resolved manifest identity changed")
	}
	manifest, err := parseResolvedManifest(contents, pin.manifest.Platform)
	if err != nil || manifest.PythonVersion != pin.manifest.PythonVersion || manifest.GcovrVersion != pin.manifest.GcovrVersion {
		return errors.New("resolved manifest identity changed")
	}
	return nil
}

func (pin *bundlePin) verifyCurrentTree() error {
	files := map[string]struct{}{}
	directories := map[string]struct{}{}
	if err := scanDirectTree(pin.bundleRoot, "", files, directories); err != nil {
		return err
	}
	if len(files) != len(pin.files) || len(directories) != len(pin.relativeDirectories) {
		return errors.New("bundle tree shape changed")
	}
	for relative := range pin.files {
		if _, ok := files[relative]; !ok {
			return fmt.Errorf("bundle file path changed: %q", relative)
		}
	}
	for _, relative := range pin.relativeDirectories {
		if _, ok := directories[relative]; !ok {
			return fmt.Errorf("bundle directory path changed: %q", relative)
		}
	}
	return nil
}

func scanDirectTree(absolute, relative string, files, directories map[string]struct{}) error {
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
		childAbsolute := absolute + string(os.PathSeparator) + entry.Name()
		info, err := directObjectInfo(childAbsolute)
		if err != nil {
			return err
		}
		if info.IsDir() {
			directories[childRelative] = struct{}{}
			if err := scanDirectTree(childAbsolute, childRelative, files, directories); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			files[childRelative] = struct{}{}
		} else {
			return fmt.Errorf("bundle entry is not a direct regular object: %q", childRelative)
		}
	}
	return nil
}

func (pin *bundlePin) Close() error {
	if pin == nil {
		return nil
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.closed {
		return nil
	}
	pin.closed = true
	var result error
	paths := make([]string, 0, len(pin.files))
	for relative := range pin.files {
		paths = append(paths, relative)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, relative := range paths {
		result = errors.Join(result, pin.files[relative].Close())
	}
	for index := len(pin.directories) - 1; index >= 0; index-- {
		result = errors.Join(result, pin.directories[index].Close())
	}
	return result
}

func pinDirectObject(path string, directory bool) (*pinnedObject, error) {
	before, err := directObjectInfo(path)
	if err != nil {
		return nil, err
	}
	if before.IsDir() != directory {
		return nil, errors.New("bundle object type does not match")
	}
	var file *os.File
	if directory {
		file, err = openPinnedDirectory(path)
	} else {
		file, err = openPinnedRegular(path)
	}
	if err != nil {
		return nil, err
	}
	pinned := &pinnedObject{path: path, file: file, identity: before, directory: directory}
	fail := func(cause error) (*pinnedObject, error) {
		_ = pinned.Close()
		return nil, cause
	}
	handleInfo, err := file.Stat()
	if err != nil || handleInfo.IsDir() != directory || !os.SameFile(before, handleInfo) {
		return fail(errors.New("bundle object identity changed while pinning"))
	}
	after, err := directObjectInfo(path)
	if err != nil || !os.SameFile(handleInfo, after) {
		return fail(errors.New("bundle object path changed while pinning"))
	}
	pinned.identity = handleInfo
	return pinned, nil
}

func directObjectInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return nil, errors.New("bundle object is not direct")
	}
	return info, nil
}

func (object *pinnedObject) verifyIdentity() error {
	if object == nil || object.file == nil || object.identity == nil {
		return errors.New("bundle object pin is closed")
	}
	before, err := directObjectInfo(object.path)
	if err != nil || before.IsDir() != object.directory || !os.SameFile(object.identity, before) {
		return errors.New("bundle object path identity changed")
	}
	handle, err := object.file.Stat()
	if err != nil || handle.IsDir() != object.directory || !os.SameFile(object.identity, handle) {
		return errors.New("bundle object handle identity changed")
	}
	after, err := directObjectInfo(object.path)
	if err != nil || !os.SameFile(object.identity, after) {
		return errors.New("bundle object path changed while validating")
	}
	return nil
}

func (object *pinnedObject) verifyDigest() error {
	if object.directory || !digestPattern.MatchString(object.digest) {
		return errors.New("missing pinned file digest")
	}
	if err := object.verifyIdentity(); err != nil {
		return err
	}
	_, digest, err := readAndDigest(object.file, -1)
	if err != nil || digest != object.digest {
		return errors.New("bundle file digest changed")
	}
	return object.verifyIdentity()
}

func (object *pinnedObject) Close() error {
	if object == nil || object.file == nil {
		return nil
	}
	err := object.file.Close()
	object.file = nil
	object.identity = nil
	return err
}

func readAndDigest(file *os.File, maximum int64) ([]byte, string, error) {
	if file == nil {
		return nil, "", errors.New("file pin is closed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	if maximum < 0 {
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return nil, "", err
		}
		return nil, hex.EncodeToString(hash.Sum(nil)), nil
	}
	reader := io.LimitReader(file, maximum+1)
	contents, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if int64(len(contents)) > maximum {
		return nil, "", errors.New("file exceeds size limit")
	}
	digest := sha256.Sum256(contents)
	return contents, hex.EncodeToString(digest[:]), nil
}
