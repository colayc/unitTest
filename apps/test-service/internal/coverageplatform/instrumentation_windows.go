//go:build windows

package coverageplatform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}
type fileDispositionInformationEx struct{ Flags uint32 }
type fileDispositionInformation struct{ DeleteFile byte }
type windowsFileIdentity struct{ volume, indexHigh, indexLow uint32 }
type windowsDirectoryPin struct {
	path     string
	handle   windows.Handle
	identity windowsFileIdentity
	bound    bool
}

var instrumentationWindowsRootPinnedForTest = func() {}
var instrumentationWindowsAncestorsPinnedForTest = func() {}
var instrumentationWindowsBeforeRenameForTest = func() {}
var instrumentationWindowsAncestorBindingFailureForTest = func(string) error { return nil }

func publishInstrumentationFile(root, name string, contents []byte) error {
	pins, err := pinWindowsAncestors(root)
	if err != nil {
		return err
	}
	defer closeWindowsPins(pins)
	rootHandle, err := openWindowsWritableDirectory(root)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(rootHandle)
	instrumentationWindowsRootPinnedForTest()
	instrumentationWindowsAncestorsPinnedForTest()
	if err := validateWindowsPins(pins); err != nil {
		return err
	}
	// Ancestors are all retained without FILE_SHARE_DELETE before this path is
	// read. That makes the name-to-handle binding stable through publication.
	if err := requireEmptyWindowsDirectory(root); err != nil {
		return err
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".coverage-instrumentation-" + hex.EncodeToString(nonce[:]) + ".tmp"
	temporaryName, err := windows.NewNTUnicodeString(temporary)
	if err != nil {
		return err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{RootDirectory: rootHandle, ObjectName: temporaryName}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	var temporaryHandle windows.Handle
	if err := windows.NtCreateFile(&temporaryHandle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE, attributes, &status, &allocationSize, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0); err != nil {
		return err
	}
	file := os.NewFile(uintptr(temporaryHandle), temporary)
	fail := func(cause error) error {
		return errors.Join(cause, deleteAndCloseWindowsTemporary(file, temporaryHandle))
	}
	if _, err := file.Write(contents); err != nil {
		return fail(err)
	}
	if err := file.Chmod(0o400); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	instrumentationWindowsBeforeRenameForTest()
	if err := renameWindowsFileRelative(temporaryHandle, rootHandle, name); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := validateWindowsPins(pins); err != nil {
		return err
	}
	return nil
}

func pinWindowsAncestors(root string) ([]windowsDirectoryPin, error) {
	clean := filepath.Clean(root)
	volume := filepath.VolumeName(clean)
	if volume == "" {
		return nil, errors.New("instrumentation root has no volume")
	}
	current := volume + string(filepath.Separator)
	tail := strings.TrimPrefix(clean, current)
	paths := []string{current}
	if tail != "" {
		for _, part := range strings.Split(tail, string(filepath.Separator)) {
			if part == "" || part == "." || part == ".." {
				return nil, errors.New("invalid instrumentation ancestor")
			}
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	pins := make([]windowsDirectoryPin, 0, len(paths))
	for _, path := range paths {
		pin, err := openPinnedWindowsDirectory(path)
		if err != nil {
			closeWindowsPins(pins)
			return nil, errors.Join(errors.New("open instrumentation ancestor "+path), err)
		}
		pins = append(pins, pin)
	}
	return pins, nil
}

func openPinnedWindowsDirectory(path string) (windowsDirectoryPin, error) {
	if err := instrumentationWindowsAncestorBindingFailureForTest(path); err != nil {
		return windowsDirectoryPin{}, err
	}
	name, err := windows.NewNTUnicodeString("\\??\\" + path)
	if err != nil {
		return windowsDirectoryPin{}, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{ObjectName: name}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	var handle windows.Handle
	// This one handle both obtains the identity and blocks delete/rename. We do
	// not inspect a reparse point and then follow it through a second path API.
	if err := windows.NtCreateFile(&handle, windows.FILE_READ_ATTRIBUTES, attributes, &status, &allocationSize, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0); err != nil {
		return windowsDirectoryPin{}, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return windowsDirectoryPin{}, errors.New("invalid instrumentation ancestor")
	}
	return windowsDirectoryPin{path: path, handle: handle, identity: windowsFileIdentity{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, bound: true}, nil
}
func validateWindowsPins(pins []windowsDirectoryPin) error {
	if len(pins) == 0 {
		return errors.New("missing instrumentation root pin")
	}
	for _, pin := range pins {
		var info windows.ByHandleFileInformation
		if !pin.bound || windows.GetFileInformationByHandle(pin.handle, &info) != nil || pin.identity != (windowsFileIdentity{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}) {
			return errors.New("instrumentation ancestor identity changed")
		}
	}
	return nil
}

func closeWindowsPins(pins []windowsDirectoryPin) {
	for index := len(pins) - 1; index >= 0; index-- {
		_ = windows.CloseHandle(pins[index].handle)
	}
}

func openWindowsWritableDirectory(path string) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString("\\??\\" + path)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{ObjectName: name}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	var handle windows.Handle
	if err := windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, attributes, &status, &allocationSize, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0); err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, errors.New("invalid instrumentation root")
	}
	return handle, nil
}

func renameWindowsFileRelative(file, root windows.Handle, name string) error {
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	bytes := len(encoded)*2 - 2
	var layout fileRenameInformation
	bufferSize := int(unsafe.Offsetof(layout.FileName)) + bytes
	buffer := make([]byte, bufferSize)
	value := (*fileRenameInformation)(unsafe.Pointer(&buffer[0]))
	value.RootDirectory = root
	value.FileNameLength = uint32(bytes)
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&value.FileName[0]))[:bytes/2:bytes/2], encoded)
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(file, &status, &buffer[0], uint32(bufferSize), windows.FileRenameInformation)
}
func deleteAndCloseWindowsTemporary(file *os.File, handle windows.Handle) error {
	value := fileDispositionInformationEx{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_ON_CLOSE | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE}
	var status windows.IO_STATUS_BLOCK
	deleteErr := windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&value)), uint32(unsafe.Sizeof(value)), windows.FileDispositionInformationEx)
	if deleteErr != nil {
		// Older Windows filesystems can reject the extended disposition class.
		// Clear the only attribute we set, then use the same retained handle with
		// the legacy disposition class; never fall back to a pathname deletion.
		chmodErr := file.Chmod(0o600)
		legacy := fileDispositionInformation{DeleteFile: 1}
		legacyErr := windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&legacy)), uint32(unsafe.Sizeof(legacy)), windows.FileDispositionInformation)
		if chmodErr != nil || legacyErr != nil {
			return errors.Join(deleteErr, chmodErr, legacyErr, file.Close())
		}
	}
	return file.Close()
}
func requireEmptyWindowsDirectory(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		return errors.New("instrumentation root is not empty")
	}
	return nil
}
