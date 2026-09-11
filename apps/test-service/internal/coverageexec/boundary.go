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

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/task"
)

type executionRootOwner struct {
	path      string
	file      *os.File
	info      os.FileInfo
	collector *retainedExecutionDirectory
	closeOnce sync.Once
	closeErr  error
}

// retainedExecutionDirectory is a handle-backed child of the private
// execution root. It can mint independent handle views without giving a
// caller the right to remove the execution-owned directory.
type retainedExecutionDirectory struct {
	owner     *executionRootOwner
	path      string
	file      *os.File
	info      os.FileInfo
	closeOnce sync.Once
	closeErr  error
}

type executionDirectoryView struct{ directory *retainedExecutionDirectory }

func retainExecutionDirectory(owner *executionRootOwner, path string) (*retainedExecutionDirectory, error) {
	if owner == nil || owner.VerifyDirectory(path) != nil {
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
	directory := &retainedExecutionDirectory{owner: owner, path: path, file: file, info: info}
	if err := directory.Verify(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return directory, nil
}

func (directory *retainedExecutionDirectory) Path() string {
	if directory == nil {
		return ""
	}
	return directory.path
}

func (directory *retainedExecutionDirectory) Verify() error {
	if directory == nil || directory.owner == nil || directory.file == nil || directory.info == nil ||
		directory.owner.VerifyDirectory(directory.path) != nil {
		return task.ErrInvalidArgument
	}
	pathInfo, err := os.Lstat(directory.path)
	if err != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(directory.info, pathInfo) {
		return task.ErrInvalidArgument
	}
	handleInfo, err := directory.file.Stat()
	if err != nil || !handleInfo.IsDir() || !os.SameFile(directory.info, handleInfo) {
		return task.ErrInvalidArgument
	}
	return nil
}

func (directory *retainedExecutionDirectory) Close() error {
	if directory == nil {
		return nil
	}
	directory.closeOnce.Do(func() {
		if directory.file != nil {
			directory.closeErr = directory.file.Close()
			directory.file = nil
		}
	})
	return directory.closeErr
}

func (view executionDirectoryView) Path() string {
	if view.directory == nil {
		return ""
	}
	return view.directory.Path()
}

func (view executionDirectoryView) Verify() error {
	if view.directory == nil {
		return task.ErrInvalidArgument
	}
	return view.directory.Verify()
}

func (view executionDirectoryView) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	if err := view.Verify(); err != nil {
		return nil, err
	}
	return retainExecutionDirectory(view.directory.owner, view.directory.path)
}

// CollectorRoot exposes the execution-owned collector directory as a
// non-owning verifier. Consumers must retain a clone before keeping it.
func (owner *executionRootOwner) CollectorRoot() coverageplatform.DirectoryVerifier {
	if owner == nil || owner.collector == nil {
		return nil
	}
	return executionDirectoryView{directory: owner.collector}
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
		collector := owner.collector
		owner.collector = nil
		if err := owner.Verify(); err != nil {
			owner.closeErr = errors.Join(owner.closeErr, err)
			if collector != nil {
				owner.closeErr = errors.Join(owner.closeErr, collector.Close())
			}
			if owner.file != nil {
				owner.closeErr = errors.Join(owner.closeErr, owner.file.Close())
				owner.file = nil
			}
			return
		}
		if collector != nil {
			owner.closeErr = errors.Join(owner.closeErr, collector.Close())
		}
		if owner.file != nil {
			owner.closeErr = errors.Join(owner.closeErr, owner.file.Close())
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
	// Continuation processes are explicitly approved by the coverage execution
	// after their retained capabilities and launch contract have been checked.
	// Accept that capability here without delegating back into the build
	// boundary, whose coverage-plan verification would recursively re-verify the
	// same toolset while the plan is being extended.
	if boundary.execution.approvesExecutable(path) {
		return nil
	}
	if boundary.delegate != nil && boundary.delegate.ValidateExecutable(path) == nil {
		return nil
	}
	boundary.execution.mu.Lock()
	defer boundary.execution.mu.Unlock()
	adapter := boundary.execution.adapter
	if adapter != nil && adapter.Toolset() != nil {
		if validatesToolsetExecutable(adapter.Toolset(), path) {
			return nil
		}
	}
	for _, candidate := range boundary.execution.binaries {
		if samePath(candidate.Path(), path) && candidate.Verify() == nil {
			return nil
		}
	}
	return task.ErrInvalidArgument
}

func validatesToolsetExecutable(toolset coverageplatform.Toolset, path string) bool {
	if coverageplatform.VerifyToolset(toolset) != nil {
		return false
	}
	for _, candidate := range toolset.Tools() {
		if samePath(candidate.Path(), path) && candidate.Verify() == nil {
			return true
		}
	}
	return false
}

func (boundary *executionBoundary) ValidateWorkingDirectory(path string) error {
	if boundary == nil || boundary.execution == nil || boundary.root == nil ||
		boundary.root.Verify() != nil {
		return task.ErrInvalidArgument
	}
	// A collector/continuation target is approved only after its complete
	// launch contract has been retained.  Re-verifying the entire toolset for
	// the directory half of that same target can recursively re-enter the
	// prepared coverage boundary while a plan is being extended.  The root
	// handle and the exact approved directory are still checked here, so an
	// approved target cannot escape the execution-owned tree or be replaced by
	// a symlink.
	if boundary.execution.approvesDirectory(path) {
		if boundary.root.VerifyDirectory(path) == nil {
			return nil
		}
	}
	if boundary.execution.verifyRetained() != nil {
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
	if boundary.ValidateExecutable(executable) == nil &&
		boundary.ValidateWorkingDirectory(directory) == nil {
		return nil
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

func (execution *execution) approvesExecutable(path string) bool {
	if execution == nil {
		return false
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	for _, target := range execution.targets {
		if samePath(target.executable, path) {
			return true
		}
	}
	return false
}

func (execution *execution) approvesDirectory(path string) bool {
	if execution == nil {
		return false
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	for _, target := range execution.targets {
		if samePath(target.directory, path) {
			return true
		}
	}
	return false
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
