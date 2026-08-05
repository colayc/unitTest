package coveragebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// Task 2's prepared Windows bundle has 42 outputs, 3 directories, 47
	// persistent bundle handles, depth 2, a 28-byte maximum relative path,
	// a 15.9 MiB maximum file, and 40.4 MiB of total hashed bytes. These
	// budgets leave substantial upgrade room while keeping attacker-controlled
	// work and persistent resources bounded.
	maximumManifestBytes         int64 = 16 * 1024 * 1024
	maximumReadyBytes            int64 = 64
	maximumManifestOutputs             = 768
	maximumBundleDirectories           = 96
	bundleMetadataFileCount             = 2
	maximumActualEntries               = maximumManifestOutputs + maximumBundleDirectories + bundleMetadataFileCount
	maximumBundleDepth                 = 24
	maximumPortablePathBytes           = 512
	directoryReadBatchSize             = 64
	maximumRegularFileBytes      int64 = 256 * 1024 * 1024
	maximumTotalHashBytes        int64 = 2 * 1024 * 1024 * 1024
	maximumProductRootComponents       = 64
	maximumPersistentHandles           = 900
)

type resourceBudget struct {
	handles     int
	entries     int
	directories int
	hashBytes   int64
}

func (budget *resourceBudget) reserveHandle() error {
	if budget.handles >= maximumPersistentHandles {
		return errors.New("persistent handle budget exceeded")
	}
	budget.handles++
	return nil
}

func (budget *resourceBudget) releaseHandle() {
	if budget.handles > 0 {
		budget.handles--
	}
}

func (budget *resourceBudget) recordEntry(directory bool) error {
	if budget.entries >= maximumActualEntries {
		return errors.New("actual bundle entry budget exceeded")
	}
	budget.entries++
	if directory {
		if budget.directories >= maximumBundleDirectories {
			return errors.New("bundle directory budget exceeded")
		}
		budget.directories++
	}
	return nil
}

func (budget *resourceBudget) addHashBytes(size int64) error {
	if size < 0 || size > maximumTotalHashBytes-budget.hashBytes {
		return errors.New("total hash byte budget exceeded")
	}
	budget.hashBytes += size
	return nil
}

type pinnedObject struct {
	path       string
	file       *os.File
	identity   os.FileInfo
	directory  bool
	digest     string
	maxBytes   int64
	executable bool
}

type bundlePin struct {
	mu                  sync.Mutex
	installation        Installation
	bundleRoot          string
	manifest            resolvedManifest
	directories         []*pinnedObject
	relativeDirectories []string
	directoryByRelative map[string]*pinnedObject
	files               map[string]*pinnedObject
	bundleRootPin       *pinnedObject
	resource            resourceBudget
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
	if err := verifyPinnedFileSet(pin.files); err != nil {
		return err
	}
	if err := pin.verifyCurrentTree(); err != nil {
		return err
	}
	if pin.files[manifestName].digest != pin.installation.ManifestSHA256 {
		return errors.New("resolved manifest identity changed")
	}
	return nil
}

func (pin *bundlePin) verifyCurrentTree() error {
	files := map[string]struct{}{}
	directories := map[string]struct{}{}
	budget := resourceBudget{}
	if err := scanPinnedTree(pin.bundleRootPin, "", pin, files, directories, &budget); err != nil {
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

func scanPinnedTree(parent *pinnedObject, relative string, pin *bundlePin, files, directories map[string]struct{}, budget *resourceBudget) error {
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
		if err := budget.recordEntry(directoryEntry); err != nil {
			return err
		}
		if directoryEntry {
			directories[childRelative] = struct{}{}
			child, expected := pin.directoryByRelative[childRelative]
			if !expected {
				return nil
			}
			if err := scanPinnedTree(child, childRelative, pin, files, directories, budget); err != nil {
				return err
			}
		} else {
			files[childRelative] = struct{}{}
		}
		return nil
	})
}

func forEachPinnedDirectoryEntry(parent *pinnedObject, visit func(os.DirEntry) error) error {
	file, err := openPinnedDirectoryReader(parent)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		entries, readErr := file.ReadDir(directoryReadBatchSize)
		for _, entry := range entries {
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
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
	return pinOpenedObject(path, directory, before, file)
}

func pinChildObject(parent *pinnedObject, name string, directory bool) (*pinnedObject, error) {
	if parent == nil || parent.file == nil || !parent.directory || name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return nil, errors.New("invalid pinned parent or child name")
	}
	childPath := filepath.Join(parent.path, name)
	before, err := directObjectInfo(childPath)
	if err != nil {
		return nil, err
	}
	if before.IsDir() != directory {
		return nil, errors.New("bundle object type does not match")
	}
	file, err := openPinnedChild(parent, name, directory)
	if err != nil {
		return nil, err
	}
	return pinOpenedObject(childPath, directory, before, file)
}

func pinOpenedObject(path string, directory bool, before os.FileInfo, file *os.File) (*pinnedObject, error) {
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
	if object.executable && (before.Mode().Perm()&0o111 == 0 || handle.Mode().Perm()&0o111 == 0 || after.Mode().Perm()&0o111 == 0) {
		return errors.New("pinned executable mode changed")
	}
	return nil
}

func (object *pinnedObject) verifyDigest(hashLimit int64) (int64, error) {
	if object.directory || !digestPattern.MatchString(object.digest) {
		return 0, errors.New("missing pinned file digest")
	}
	if err := object.verifyIdentity(); err != nil {
		return 0, err
	}
	limit := object.maxBytes
	if hashLimit < limit {
		limit = hashLimit
	}
	digest, count, err := digestOpenedFile(object.file, limit)
	if err != nil || digest != object.digest {
		return count, errors.New("bundle file digest changed")
	}
	return count, object.verifyIdentity()
}

func verifyPinnedFileSet(files map[string]*pinnedObject) error {
	paths := make([]string, 0, len(files))
	for relative := range files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	var total int64
	for _, relative := range paths {
		object := files[relative]
		if err := object.verifyIdentity(); err != nil {
			return fmt.Errorf("file %q: %w", relative, err)
		}
		info, err := object.file.Stat()
		if err != nil || info.Size() < 0 || info.Size() > object.maxBytes {
			return fmt.Errorf("file %q exceeds its size budget", relative)
		}
		if info.Size() > maximumTotalHashBytes-total {
			return errors.New("total hash byte budget exceeded")
		}
		total += info.Size()
	}
	remaining := maximumTotalHashBytes
	for _, relative := range paths {
		count, err := files[relative].verifyDigest(remaining)
		if err != nil {
			return fmt.Errorf("file %q: %w", relative, err)
		}
		remaining -= count
	}
	return nil
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

func digestOpenedFile(file *os.File, maximum int64) (string, int64, error) {
	if file == nil || maximum < 0 {
		return "", 0, errors.New("invalid file digest budget")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil {
		return "", count, err
	}
	if count > maximum {
		return "", count, errors.New("file exceeds size budget")
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}
