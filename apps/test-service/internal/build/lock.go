package build

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var ErrBuildDirectoryBusy = errors.New("build directory is busy")

type DirectoryLocks struct {
	mu     sync.Mutex
	active map[string]struct{}
}

type DirectoryLock struct {
	owner *DirectoryLocks
	key   string
	file  *os.File
	once  sync.Once
	err   error
}

func NewDirectoryLocks() *DirectoryLocks {
	return &DirectoryLocks{active: make(map[string]struct{})}
}

func (l *DirectoryLocks) Acquire(directory string) (*DirectoryLock, error) {
	if l == nil || directory == "" {
		return nil, ErrBuildDirectoryBusy
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, ErrBuildDirectoryBusy
	}
	key := filepath.Clean(absolute)
	l.mu.Lock()
	if _, exists := l.active[key]; exists {
		l.mu.Unlock()
		return nil, ErrBuildDirectoryBusy
	}
	l.active[key] = struct{}{}
	l.mu.Unlock()
	fail := func(err error) (*DirectoryLock, error) {
		l.mu.Lock()
		delete(l.active, key)
		l.mu.Unlock()
		return nil, err
	}
	info, err := os.Stat(key)
	if err != nil || !info.IsDir() {
		return fail(ErrBuildDirectoryBusy)
	}
	lockFile := filepath.Join(key, ".unit-test-ide.lock")
	file, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fail(ErrBuildDirectoryBusy)
	}
	if err := tryLockFile(file); err != nil {
		_ = file.Close()
		return fail(ErrBuildDirectoryBusy)
	}
	return &DirectoryLock{owner: l, key: key, file: file}, nil
}

func (l *DirectoryLock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if err := unlockFile(l.file); err != nil {
			l.err = err
		}
		if err := l.file.Close(); err != nil {
			l.err = errors.Join(l.err, err)
		}
		l.owner.mu.Lock()
		delete(l.owner.active, l.key)
		l.owner.mu.Unlock()
	})
	return l.err
}
