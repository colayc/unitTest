package instance

import (
	"errors"
	"io"
)

var (
	ErrAlreadyRunning  = errors.New("service is already running")
	ErrLockUnavailable = errors.New("service instance lock is unavailable")
)

func Lock(path string) (io.Closer, error) {
	if path == "" {
		return nil, ErrLockUnavailable
	}
	return lock(path)
}
