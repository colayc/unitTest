package testcontrol

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"unit-test-ide.local/test-service/internal/testframework"
)

var (
	ErrInvalidControlFile = errors.New("invalid test control file")
	ErrControlUnavailable = errors.New("test control file unavailable")
)

const maximumControlFileBytes int64 = 64 * 1024 * 1024

type Allocator struct {
	root string

	mu     sync.Mutex
	files  map[*controlFile]struct{}
	closed bool
}

func NewAllocator(root string) (*Allocator, error) {
	if root == "" || !filepath.IsAbs(root) ||
		filepath.Clean(root) != root {
		return nil, ErrInvalidControlFile
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrControlUnavailable
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, ErrControlUnavailable
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, ErrControlUnavailable
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 ||
			entry.IsDir() {
			return nil, ErrControlUnavailable
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return nil, ErrControlUnavailable
		}
	}
	return &Allocator{
		root:  root,
		files: make(map[*controlFile]struct{}),
	}, nil
}

func (allocator *Allocator) Allocate(
	ctx context.Context,
) (testframework.ControlFile, error) {
	if allocator == nil || ctx == nil {
		return nil, ErrInvalidControlFile
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.closed {
		return nil, ErrControlUnavailable
	}
	opened, err := os.CreateTemp(
		allocator.root,
		"test-control-*.jsonl",
	)
	if err != nil {
		return nil, ErrControlUnavailable
	}
	fail := func() (testframework.ControlFile, error) {
		_ = opened.Close()
		_ = os.Remove(opened.Name())
		return nil, ErrControlUnavailable
	}
	if err := opened.Chmod(0o600); err != nil {
		return fail()
	}
	info, err := opened.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail()
	}
	control := &controlFile{
		allocator: allocator,
		opened:    opened,
		path:      opened.Name(),
		identity:  info,
	}
	allocator.files[control] = struct{}{}
	return control, nil
}

func (allocator *Allocator) Close() error {
	if allocator == nil {
		return nil
	}
	allocator.mu.Lock()
	if allocator.closed {
		allocator.mu.Unlock()
		return nil
	}
	allocator.closed = true
	files := make([]*controlFile, 0, len(allocator.files))
	for file := range allocator.files {
		files = append(files, file)
	}
	allocator.mu.Unlock()
	var result error
	for _, file := range files {
		if err := file.release(); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

type controlFile struct {
	allocator *Allocator
	opened    *os.File
	path      string
	identity  os.FileInfo

	mu       sync.Mutex
	released bool
}

func (file *controlFile) Path() string {
	if file == nil {
		return ""
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.released {
		return ""
	}
	return file.path
}

func (file *controlFile) Read(
	ctx context.Context,
	maximum int64,
) ([]byte, error) {
	if file == nil || ctx == nil || maximum < 0 ||
		maximum > maximumControlFileBytes {
		return nil, ErrInvalidControlFile
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file.mu.Lock()
	if file.released || file.opened == nil ||
		file.path == "" || file.identity == nil {
		file.mu.Unlock()
		return nil, ErrControlUnavailable
	}
	current, err := os.Lstat(file.path)
	if err != nil || !current.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(file.identity, current) {
		file.mu.Unlock()
		_ = file.release()
		return nil, ErrControlUnavailable
	}
	if _, err := file.opened.Seek(0, io.SeekStart); err != nil {
		file.mu.Unlock()
		_ = file.release()
		return nil, ErrControlUnavailable
	}
	data, err := io.ReadAll(
		io.LimitReader(file.opened, maximum+1),
	)
	file.mu.Unlock()
	releaseErr := file.release()
	if err != nil || releaseErr != nil {
		return nil, ErrControlUnavailable
	}
	if int64(len(data)) > maximum {
		return nil, ErrInvalidControlFile
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func (file *controlFile) release() error {
	if file == nil {
		return nil
	}
	file.mu.Lock()
	if file.released {
		file.mu.Unlock()
		return nil
	}
	file.released = true
	opened := file.opened
	path := file.path
	file.opened = nil
	file.path = ""
	file.mu.Unlock()
	if file.allocator != nil {
		file.allocator.mu.Lock()
		delete(file.allocator.files, file)
		file.allocator.mu.Unlock()
	}
	var result error
	if opened != nil {
		result = errors.Join(result, opened.Close())
	}
	if path != "" {
		if err := os.Remove(path); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return ErrControlUnavailable
	}
	return nil
}

var _ testframework.ControlFileAllocator = (*Allocator)(nil)
