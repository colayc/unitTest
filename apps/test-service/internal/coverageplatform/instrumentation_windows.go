//go:build windows

package coverageplatform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

type fileDispositionInformation struct{ DeleteFile byte }

var instrumentationWindowsRootPinnedForTest = func() {}

func publishInstrumentationFile(root, name string, contents []byte) error {
	rootName, err := windows.NewNTUnicodeString("\\??\\" + root)
	if err != nil {
		return err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{ObjectName: rootName}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	var rootHandle windows.Handle
	// Deliberately omit FILE_SHARE_DELETE: while this retained directory handle
	// is live, Windows cannot replace the root between relative create and rename.
	if err := windows.NtCreateFile(&rootHandle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, attributes, &status, &allocationSize, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0); err != nil {
		return err
	}
	defer windows.CloseHandle(rootHandle)
	var rootInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(rootHandle, &rootInfo); err != nil || rootInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("invalid instrumentation root")
	}
	instrumentationWindowsRootPinnedForTest()
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
	attributes.RootDirectory, attributes.ObjectName = rootHandle, temporaryName
	var temporaryHandle windows.Handle
	if err := windows.NtCreateFile(&temporaryHandle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE, attributes, &status, &allocationSize, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0); err != nil {
		return err
	}
	file := os.NewFile(uintptr(temporaryHandle), temporary)
	deleteOnFailure := true
	defer func() {
		if deleteOnFailure {
			_ = markWindowsFileForDeletion(temporaryHandle)
		}
		_ = file.Close()
	}()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	if err := file.Chmod(0o400); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := renameWindowsFileRelative(temporaryHandle, rootHandle, name); err != nil {
		return err
	}
	deleteOnFailure = false
	return nil
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

func markWindowsFileForDeletion(file windows.Handle) error {
	value := fileDispositionInformation{DeleteFile: 1}
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(file, &status, (*byte)(unsafe.Pointer(&value)), uint32(unsafe.Sizeof(value)), windows.FileDispositionInformation)
}

func requireEmptyWindowsDirectory(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		return errors.New("instrumentation root is not empty")
	}
	return nil
}
