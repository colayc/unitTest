//go:build !windows

package coverageplatform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func publishInstrumentationFile(root, name string, contents []byte) error {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Ino == 0 || before.Dev == 0 {
		return errors.New("invalid root")
	}
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return err
	}
	reader := os.NewFile(uintptr(copyFD), "")
	entries, err := reader.Readdirnames(-1)
	_ = reader.Close()
	if err != nil || len(entries) != 0 {
		return errors.New("root is not empty")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".coverage-instrumentation-" + hex.EncodeToString(nonce[:]) + ".tmp"
	temporaryFD, err := unix.Openat(fd, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Unlinkat(fd, temporary, 0)
		}
	}()
	file := os.NewFile(uintptr(temporaryFD), temporary)
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := unix.Fchmod(temporaryFD, 0o400); err != nil {
		_ = file.Close()
		return err
	}
	if err := unix.Fsync(temporaryFD); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	var held, path unix.Stat_t
	if unix.Fstat(fd, &held) != nil || unix.Lstat(root, &path) != nil || held.Dev != before.Dev || held.Ino != before.Ino || path.Dev != before.Dev || path.Ino != before.Ino {
		return errors.New("root changed")
	}
	if err := unix.Renameat2(fd, temporary, fd, name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	cleanup = false
	if unix.Fstat(fd, &held) != nil || unix.Lstat(root, &path) != nil || held.Dev != before.Dev || held.Ino != before.Ino || path.Dev != before.Dev || path.Ino != before.Ino {
		return errors.New("root changed")
	}
	var final unix.Stat_t
	if unix.Fstatat(fd, name, &final, unix.AT_SYMLINK_NOFOLLOW) != nil || final.Mode&unix.S_IFMT != unix.S_IFREG || final.Nlink != 1 {
		return errors.New("invalid final")
	}
	return nil
}
