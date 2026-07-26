//go:build !windows

package instance

import (
	"errors"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type unixLock struct {
	file *os.File
	once sync.Once
	err  error
}

func lock(path string) (io.Closer, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, ErrLockUnavailable
	}
	file := os.NewFile(uintptr(fd), "service-instance-lock")
	fail := func(result error) (io.Closer, error) {
		_ = file.Close()
		return nil, result
	}
	var status unix.Stat_t
	if err := unix.Fstat(fd, &status); err != nil || status.Mode&unix.S_IFMT != unix.S_IFREG || status.Uid != uint32(os.Geteuid()) || status.Mode&0o177 != 0 {
		return fail(ErrLockUnavailable)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return fail(ErrAlreadyRunning)
		}
		return fail(ErrLockUnavailable)
	}
	return &unixLock{file: file}, nil
}

func (l *unixLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
		closeErr := l.file.Close()
		if unlockErr != nil || closeErr != nil {
			l.err = ErrLockUnavailable
		}
	})
	return l.err
}
