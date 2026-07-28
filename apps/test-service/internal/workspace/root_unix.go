//go:build !windows

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func finalExistingPath(path string) (string, string, error) {
	finalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	finalPath, err = filepath.Abs(finalPath)
	if err != nil {
		return "", "", err
	}
	finalPath = filepath.Clean(finalPath)
	info, err := os.Stat(finalPath)
	if err != nil {
		return "", "", err
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("filesystem stat has unexpected type %T", info.Sys())
	}
	return finalPath, fmt.Sprintf("dev:%016x", uint64(status.Dev)), nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
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
	return "", filepath.ToSlash(filepath.Clean(path)), nil
}

func platformIdentity() string {
	return runtime.GOOS
}

func normalizedVolumeIdentity(identity string) string {
	return identity
}

func identityPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}
