//go:build windows

package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const volumeNameGUID = 0x1

func finalExistingPath(path string) (string, string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", "", err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(handle)

	dosPath, err := finalPathName(handle, 0)
	if err != nil {
		return "", "", err
	}
	guidPath, err := finalPathName(handle, volumeNameGUID)
	if err != nil {
		return "", "", err
	}
	volume := filepath.VolumeName(guidPath)
	if volume == "" {
		return "", "", fmt.Errorf("final path %q has no volume identity", guidPath)
	}
	return filepath.Clean(normalizeWindowsFinalPath(dosPath)), volume, nil
}

func finalPathName(handle windows.Handle, flags uint32) (string, error) {
	buffer := make([]uint16, 256)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return string(utf16.Decode(buffer[:length])), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func normalizeWindowsFinalPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + path[len(`\\?\UNC\`):]
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path[len(`\\?\`):]
	}
	return path
}

func pathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if !strings.EqualFold(filepath.VolumeName(root), filepath.VolumeName(candidate)) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !filepath.IsAbs(relative) &&
		(len(relative) < 3 || relative[:3] != ".."+string(filepath.Separator))
}

func fileURLParts(path string) (string, string, error) {
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("path is not absolute")
	}
	slashPath := filepath.ToSlash(filepath.Clean(path))
	if strings.HasPrefix(slashPath, "//") {
		unc := strings.TrimPrefix(slashPath, "//")
		separator := strings.IndexByte(unc, '/')
		if separator <= 0 || separator == len(unc)-1 {
			return "", "", fmt.Errorf("UNC path is missing share")
		}
		return unc[:separator], "/" + unc[separator+1:], nil
	}
	return "", "/" + slashPath, nil
}

func platformIdentity() string {
	return "windows"
}

func normalizedVolumeIdentity(identity string) string {
	return strings.ToLower(filepath.ToSlash(strings.TrimRight(identity, `\/`)))
}

func identityPath(path string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
}
