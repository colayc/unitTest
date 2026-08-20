package coverageexec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

type executionRootOwner struct {
	path      string
	file      *os.File
	info      os.FileInfo
	closeOnce sync.Once
	closeErr  error
}

func retainExecutionRoot(path string) (*executionRootOwner, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		strings.ContainsRune(path, '\x00') {
		return nil, task.ErrInvalidArgument
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, task.ErrInvalidArgument
	}
	file, err := openRetainedDirectory(path)
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	owner := &executionRootOwner{path: path, file: file, info: info}
	if err := owner.Verify(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return owner, nil
}

func (owner *executionRootOwner) Verify() error {
	if owner == nil || owner.file == nil || owner.info == nil {
		return task.ErrInvalidArgument
	}
	pathInfo, err := os.Lstat(owner.path)
	if err != nil || !pathInfo.IsDir() ||
		pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(owner.info, pathInfo) {
		return task.ErrInvalidArgument
	}
	handleInfo, err := owner.file.Stat()
	if err != nil || !handleInfo.IsDir() ||
		!os.SameFile(owner.info, handleInfo) {
		return task.ErrInvalidArgument
	}
	return nil
}

func (owner *executionRootOwner) VerifyDirectory(path string) error {
	if err := owner.Verify(); err != nil ||
		(!samePath(owner.path, path) && !pathWithin(owner.path, path)) {
		return task.ErrInvalidArgument
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return task.ErrInvalidArgument
	}
	return nil
}

func (owner *executionRootOwner) Close() error {
	if owner == nil {
		return nil
	}
	owner.closeOnce.Do(func() {
		if err := owner.Verify(); err != nil {
			owner.closeErr = err
			if owner.file != nil {
				owner.closeErr = errors.Join(owner.closeErr, owner.file.Close())
				owner.file = nil
			}
			return
		}
		if owner.file != nil {
			owner.closeErr = owner.file.Close()
			owner.file = nil
		}
		if owner.closeErr == nil {
			owner.closeErr = os.RemoveAll(owner.path)
		}
	})
	return owner.closeErr
}

type retainedFile struct {
	path      string
	file      *os.File
	info      os.FileInfo
	digest    string
	closeOnce sync.Once
	closeErr  error
}

func retainFile(path string) (*retainedFile, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(absolute) != path {
		return nil, task.ErrInvalidArgument
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, task.ErrInvalidArgument
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	digest, err := digestFile(file)
	if err != nil {
		_ = file.Close()
		return nil, task.ErrInvalidArgument
	}
	result := &retainedFile{path: path, file: file, info: info, digest: digest}
	if err := result.Verify(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return result, nil
}

func (file *retainedFile) Path() string {
	if file == nil {
		return ""
	}
	return file.path
}

func (file *retainedFile) Verify() error {
	if file == nil || file.file == nil || file.info == nil {
		return task.ErrInvalidArgument
	}
	info, err := os.Lstat(file.path)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(file.info, info) {
		return task.ErrInvalidArgument
	}
	digest, err := digestFile(file.file)
	if err != nil || digest != file.digest {
		return task.ErrInvalidArgument
	}
	return nil
}

func (file *retainedFile) Close() error {
	if file == nil {
		return nil
	}
	file.closeOnce.Do(func() {
		if file.file != nil {
			file.closeErr = file.file.Close()
			file.file = nil
		}
	})
	return file.closeErr
}

func digestFile(file *os.File) (string, error) {
	if file == nil {
		return "", task.ErrInvalidArgument
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type executionBoundary struct {
	delegate  task.ExecutionBoundary
	execution *execution
	root      *executionRootOwner
	closeOnce sync.Once
	closeErr  error
}

type processTarget struct {
	executable  string
	arguments   []string
	environment []string
	unset       []string
	directory   string
}

func (boundary *executionBoundary) ValidateExecutable(path string) error {
	if boundary == nil || boundary.execution == nil || boundary.root == nil ||
		boundary.root.Verify() != nil || boundary.execution.verifyRetained() != nil {
		return task.ErrInvalidArgument
	}
	if boundary.delegate != nil && boundary.delegate.ValidateExecutable(path) == nil {
		return nil
	}
	boundary.execution.mu.Lock()
	defer boundary.execution.mu.Unlock()
	adapter := boundary.execution.adapter
	if adapter != nil && adapter.Toolset() != nil {
		for _, candidate := range []coveragerun.TrustedPath{
			adapter.Toolset().Compiler(),
			adapter.Toolset().Profdata(),
			adapter.Toolset().Cov(),
		} {
			if samePath(candidate.Path(), path) && candidate.Verify() == nil {
				return nil
			}
		}
	}
	for _, candidate := range boundary.execution.binaries {
		if samePath(candidate.Path(), path) && candidate.Verify() == nil {
			return nil
		}
	}
	return task.ErrInvalidArgument
}

func (boundary *executionBoundary) ValidateWorkingDirectory(path string) error {
	if boundary == nil || boundary.execution == nil || boundary.root == nil ||
		boundary.root.Verify() != nil || boundary.execution.verifyRetained() != nil {
		return task.ErrInvalidArgument
	}
	if boundary.delegate != nil && boundary.delegate.ValidateWorkingDirectory(path) == nil {
		return nil
	}
	if boundary.root.VerifyDirectory(path) == nil {
		return nil
	}
	return task.ErrInvalidArgument
}

func (boundary *executionBoundary) ValidateProcessTarget(
	executable string,
	arguments, environment, unset []string,
	directory string,
) error {
	if boundary == nil || boundary.execution == nil ||
		!boundary.execution.approvesTarget(
			executable, arguments, environment, unset, directory,
		) {
		return task.ErrInvalidArgument
	}
	if target, ok := boundary.delegate.(task.ProcessTargetBoundary); ok {
		if target.ValidateProcessTarget(
			executable, arguments, environment, unset, directory,
		) == nil {
			return nil
		}
	}
	if boundary.ValidateExecutable(executable) != nil ||
		boundary.ValidateWorkingDirectory(directory) != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

func (execution *execution) approvesTarget(
	executable string,
	arguments, environment, unset []string,
	directory string,
) bool {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	for _, target := range execution.targets {
		if samePath(target.executable, executable) &&
			samePath(target.directory, directory) &&
			reflect.DeepEqual(target.arguments, arguments) &&
			reflect.DeepEqual(target.environment, environment) &&
			reflect.DeepEqual(target.unset, unset) {
			return true
		}
	}
	return false
}

func (boundary *executionBoundary) Adopt(taskID string) {
	if managed, ok := boundary.delegate.(task.ManagedExecutionBoundary); ok {
		managed.Adopt(taskID)
	}
}

func (boundary *executionBoundary) Release() error {
	if boundary == nil {
		return nil
	}
	boundary.closeOnce.Do(func() {
		if managed, ok := boundary.delegate.(task.ManagedExecutionBoundary); ok {
			boundary.closeErr = errors.Join(boundary.closeErr, managed.Release())
		}
		if boundary.execution != nil {
			boundary.closeErr = errors.Join(
				boundary.closeErr,
				boundary.execution.closeRuntime(),
			)
		} else if boundary.root != nil {
			boundary.closeErr = errors.Join(boundary.closeErr, boundary.root.Close())
		}
	})
	return boundary.closeErr
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

var _ task.ManagedExecutionBoundary = (*executionBoundary)(nil)
var _ task.ProcessTargetBoundary = (*executionBoundary)(nil)
