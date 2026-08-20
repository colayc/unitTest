//go:build windows

package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/toolchain"
)

const maximumLLVMToolBytes int64 = 512 * 1024 * 1024

var llvmSnapshotVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)

type nativeFileIdentity struct {
	volume    uint32
	indexHigh uint32
	indexLow  uint32
	links     uint32
	attrs     uint32
}

func PinToolset(instance toolchain.Instance) (*Toolset, error) {
	if instance.Family != toolchain.FamilyClangCL ||
		!validSnapshotVersion(instance.Version) ||
		instance.CCompiler == "" || !strings.EqualFold(instance.CCompiler, instance.CXXCompiler) ||
		instance.Coverage.LLVMProfdata == "" || instance.Coverage.LLVMCov == "" {
		return nil, ErrInvalidToolset
	}
	paths := []string{instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov}
	names := []string{"clang-cl.exe", "llvm-profdata.exe", "llvm-cov.exe"}
	for index, path := range paths {
		absolute, err := canonicalDirectWindowsPath(path)
		if err != nil || !strings.EqualFold(filepath.Base(absolute), names[index]) {
			return nil, ErrInvalidToolset
		}
		paths[index] = absolute
	}
	parent := filepath.Dir(paths[0])
	if !strings.EqualFold(filepath.Dir(paths[1]), parent) || !strings.EqualFold(filepath.Dir(paths[2]), parent) {
		return nil, ErrInvalidToolset
	}
	if err := rejectReparseAncestors(parent); err != nil {
		return nil, errors.Join(ErrInvalidToolset, err)
	}
	directoryFile, directoryInfo, directoryNative, err := pinWindowsDirectory(parent)
	if err != nil {
		return nil, errors.Join(ErrInvalidToolset, err)
	}
	result := &Toolset{
		version:          instance.Version,
		installationPath: parent, installationFile: directoryFile,
		installationInfo: directoryInfo, installationNative: directoryNative,
	}
	fail := func(cause error) (*Toolset, error) {
		_ = result.Close()
		return nil, errors.Join(ErrInvalidToolset, cause)
	}
	tools := []*pinnedTool{&result.compiler, &result.profdata, &result.cov}
	for index, path := range paths {
		pinned, err := pinWindowsTool(path)
		if err != nil {
			return fail(err)
		}
		*tools[index] = pinned
	}
	if err := rejectReparseAncestors(parent); err != nil {
		return fail(err)
	}
	if err := result.Verify(); err != nil {
		return fail(err)
	}
	return result, nil
}

func validSnapshotVersion(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) &&
		!strings.ContainsRune(value, 0) && llvmSnapshotVersion.MatchString(value)
}

func canonicalDirectWindowsPath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", errors.New("tool path is not an absolute direct path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(path) != path {
		return "", errors.New("tool path is not canonical")
	}
	return absolute, nil
}

func pinWindowsTool(path string) (pinnedTool, error) {
	before, err := windowsPathIdentity(path, false)
	if err != nil {
		return pinnedTool{}, err
	}
	file, err := openWindowsObject(path, false, windows.GENERIC_READ)
	if err != nil {
		return pinnedTool{}, err
	}
	result := pinnedTool{path: path, file: file, native: before}
	fail := func(cause error) (pinnedTool, error) {
		_ = file.Close()
		return pinnedTool{}, cause
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumLLVMToolBytes {
		return fail(errors.New("LLVM tool is not a bounded regular file"))
	}
	current, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || current != before || current.links != 1 || current.attrs&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return fail(errors.New("LLVM tool identity changed while pinning"))
	}
	digest, err := digestHandle(file, maximumLLVMToolBytes)
	if err != nil {
		return fail(err)
	}
	after, err := windowsPathIdentity(path, false)
	if err != nil || after != current {
		return fail(errors.New("LLVM tool path changed while pinning"))
	}
	result.info = info
	result.native = current
	result.sha256 = digest
	return result, nil
}

func verifyPinnedTool(tool *pinnedTool) error {
	if tool == nil || tool.file == nil || tool.info == nil || tool.sha256 == "" {
		return errors.New("LLVM tool pin is closed")
	}
	before, err := windowsPathIdentity(tool.path, false)
	if err != nil || before != tool.native {
		return errors.New("LLVM tool path identity changed")
	}
	current, err := identityFromHandle(windows.Handle(tool.file.Fd()))
	if err != nil || current != tool.native || current.links != 1 {
		return errors.New("LLVM tool handle identity changed")
	}
	info, err := tool.file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(tool.info, info) || info.Size() < 0 || info.Size() > maximumLLVMToolBytes {
		return errors.New("LLVM tool file information changed")
	}
	digest, err := digestHandle(tool.file, maximumLLVMToolBytes)
	if err != nil || digest != tool.sha256 {
		return errors.New("LLVM tool content changed")
	}
	after, err := windowsPathIdentity(tool.path, false)
	if err != nil || after != current {
		return errors.New("LLVM tool path changed while validating")
	}
	return nil
}

func pinWindowsDirectory(path string) (*os.File, os.FileInfo, nativeFileIdentity, error) {
	before, err := windowsPathIdentity(path, true)
	if err != nil {
		return nil, nil, nativeFileIdentity{}, err
	}
	file, err := openWindowsObject(path, true, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return nil, nil, nativeFileIdentity{}, err
	}
	fail := func(cause error) (*os.File, os.FileInfo, nativeFileIdentity, error) {
		_ = file.Close()
		return nil, nil, nativeFileIdentity{}, cause
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return fail(errors.New("LLVM installation is not a directory"))
	}
	current, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || current != before || current.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(errors.New("LLVM installation identity changed while pinning"))
	}
	after, err := windowsPathIdentity(path, true)
	if err != nil || after != current {
		return fail(errors.New("LLVM installation path changed while pinning"))
	}
	return file, info, current, nil
}

func verifyPinnedDirectory(path string, file *os.File, expected os.FileInfo, native nativeFileIdentity) error {
	if file == nil || expected == nil {
		return errors.New("LLVM installation pin is closed")
	}
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	before, err := windowsPathIdentity(path, true)
	if err != nil || before != native {
		return errors.New("LLVM installation path identity changed")
	}
	current, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || current != native {
		return errors.New("LLVM installation handle identity changed")
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() || !os.SameFile(expected, info) {
		return errors.New("LLVM installation file information changed")
	}
	after, err := windowsPathIdentity(path, true)
	if err != nil || after != current {
		return errors.New("LLVM installation changed while validating")
	}
	return rejectReparseAncestors(path)
}

func windowsPathIdentity(path string, directory bool) (nativeFileIdentity, error) {
	access := uint32(windows.FILE_READ_ATTRIBUTES)
	file, err := openWindowsObject(path, directory, access)
	if err != nil {
		return nativeFileIdentity{}, err
	}
	defer file.Close()
	identity, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil {
		return nativeFileIdentity{}, err
	}
	wantDirectory := identity.attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if wantDirectory != directory || identity.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		!directory && identity.links != 1 || identity.indexHigh == 0 && identity.indexLow == 0 {
		return nativeFileIdentity{}, errors.New("Windows object is not direct and uniquely linked")
	}
	return identity, nil
}

func openWindowsObject(path string, directory bool, access uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags = windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := windows.CreateFile(
		name, access, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, flags, 0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("construct retained Windows handle")
	}
	return file, nil
}

func identityFromHandle(handle windows.Handle) (nativeFileIdentity, error) {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return nativeFileIdentity{}, err
	}
	return nativeFileIdentity{
		volume: information.VolumeSerialNumber, indexHigh: information.FileIndexHigh,
		indexLow: information.FileIndexLow, links: information.NumberOfLinks,
		attrs: information.FileAttributes,
	}, nil
}

func digestHandle(file *os.File, maximum int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || count > maximum {
		return "", errors.New("LLVM tool exceeds digest budget")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func rejectReparseAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	root := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(absolute, root)
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("LLVM installation crosses a reparse point")
		}
	}
	return nil
}
