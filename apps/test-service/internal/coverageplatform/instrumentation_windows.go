//go:build windows

package coverageplatform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func publishInstrumentationFile(root, name string, contents []byte) error {
	rootHandle, err := os.Open(root)
	if err != nil {
		return err
	}
	defer rootHandle.Close()
	before, err := rootHandle.Stat()
	if err != nil || !before.IsDir() {
		return errors.New("invalid root")
	}
	entries, err := rootHandle.ReadDir(-1)
	if err != nil || len(entries) != 0 {
		return errors.New("root is not empty")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := filepath.Join(root, ".coverage-instrumentation-"+hex.EncodeToString(nonce[:])+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o400); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	pathInfo, err := os.Stat(root)
	if err != nil || !os.SameFile(before, pathInfo) {
		return errors.New("root changed")
	}
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(root, name))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		return err
	}
	cleanup = false
	pathInfo, err = os.Stat(root)
	if err != nil || !os.SameFile(before, pathInfo) {
		return errors.New("root changed")
	}
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid final")
	}
	return nil
}
