//go:build windows

package cmake

import "golang.org/x/sys/windows"

func replaceFileAtomically(source, destination string) error {
	sourceName, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationName, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourceName,
		destinationName,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncParentDirectory(string) error {
	// MoveFileEx with MOVEFILE_WRITE_THROUGH flushes the replacement to disk.
	return nil
}
