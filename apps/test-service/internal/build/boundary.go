package build

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/workspace"
)

type executionBoundary struct {
	executable     string
	executableFile *os.File
	executableInfo os.FileInfo
	workspaceRoot  workspace.Root
	workspaceInfo  os.FileInfo
	dataRoot       workspace.Root
	dataInfo       os.FileInfo
	lock           *DirectoryLock
	mu             sync.Mutex
	adoptedTaskID  string
	releaseOnce    sync.Once
	releaseErr     error
}

func NewExecutionBoundary(
	installation cmake.Installation,
	workspaceRoot workspace.Root,
	serviceDataRoot string,
) (task.ExecutionBoundary, error) {
	return newExecutionBoundary(installation, workspaceRoot, serviceDataRoot, nil)
}

func newExecutionBoundary(
	installation cmake.Installation,
	workspaceRoot workspace.Root,
	serviceDataRoot string,
	lock *DirectoryLock,
) (*executionBoundary, error) {
	if installation.Executable == "" || workspaceRoot.NativePath == "" {
		return nil, task.ErrInvalidArgument
	}
	executable, err := filepath.Abs(installation.Executable)
	if err != nil || filepath.Clean(executable) != installation.Executable {
		return nil, task.ErrInvalidArgument
	}
	executableFile, executableInfo, err := pinExecutable(executable)
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	fail := func() (*executionBoundary, error) {
		_ = executableFile.Close()
		return nil, task.ErrInvalidArgument
	}
	workspaceInfo, err := os.Stat(workspaceRoot.NativePath)
	if err != nil || !workspaceInfo.IsDir() {
		return fail()
	}
	dataRoot, err := workspace.OpenRoot(serviceDataRoot)
	if err != nil || dataRoot.ID == workspaceRoot.ID ||
		dataRoot.Contains(workspaceRoot.NativePath) ||
		workspaceRoot.Contains(dataRoot.NativePath) {
		return fail()
	}
	dataInfo, err := os.Stat(dataRoot.NativePath)
	if err != nil || !dataInfo.IsDir() {
		return fail()
	}
	return &executionBoundary{
		executable: executable, executableFile: executableFile,
		executableInfo: executableInfo,
		workspaceRoot:  workspaceRoot, workspaceInfo: workspaceInfo,
		dataRoot: dataRoot, dataInfo: dataInfo, lock: lock,
	}, nil
}

func (b *executionBoundary) ValidateExecutable(path string) error {
	if b == nil {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil {
		return task.ErrInvalidArgument
	}
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(absolute) != b.executable {
		return task.ErrInvalidArgument
	}
	if err := validatePinnedExecutable(
		b.executableFile,
		b.executableInfo,
		absolute,
	); err != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

func (b *executionBoundary) Adopt(taskID string) {
	if b == nil || taskID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.adoptedTaskID == "" {
		b.adoptedTaskID = taskID
	}
}

func (b *executionBoundary) Release() error {
	if b == nil {
		return nil
	}
	b.releaseOnce.Do(func() {
		b.mu.Lock()
		lock := b.lock
		executableFile := b.executableFile
		b.lock = nil
		b.executableFile = nil
		b.mu.Unlock()

		var result error
		if lock != nil {
			result = errors.Join(result, lock.Release())
		}
		if executableFile != nil {
			result = errors.Join(result, executableFile.Close())
		}
		b.mu.Lock()
		b.releaseErr = result
		b.mu.Unlock()
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.releaseErr
}

func (b *executionBoundary) adopted() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.adoptedTaskID != ""
}

func (b *executionBoundary) ValidateWorkingDirectory(path string) error {
	if b == nil || path == "" {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil {
		return task.ErrInvalidArgument
	}
	workspaceInfo, workspaceErr := os.Stat(b.workspaceRoot.NativePath)
	dataInfo, dataErr := os.Stat(b.dataRoot.NativePath)
	if workspaceErr != nil || dataErr != nil ||
		!os.SameFile(b.workspaceInfo, workspaceInfo) ||
		!os.SameFile(b.dataInfo, dataInfo) {
		return task.ErrInvalidArgument
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return task.ErrInvalidArgument
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return task.ErrInvalidArgument
	}
	if !b.workspaceRoot.Contains(absolute) && !b.dataRoot.Contains(absolute) {
		return task.ErrInvalidArgument
	}
	return nil
}
