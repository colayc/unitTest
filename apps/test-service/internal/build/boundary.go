package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/workspace"
)

type executionBoundary struct {
	executable                 string
	executableFile             *os.File
	executableInfo             os.FileInfo
	ctestExecutable            string
	ctestFile                  *os.File
	ctestInfo                  os.FileInfo
	unityRunnerGenerator       string
	unityRunnerGeneratorFile   *os.File
	unityRunnerGeneratorInfo   os.FileInfo
	unityRunnerGeneratorSHA256 string
	workspaceRoot              workspace.Root
	workspaceInfo              os.FileInfo
	dataRoot                   workspace.Root
	dataInfo                   os.FileInfo
	lock                       *DirectoryLock
	mu                         sync.Mutex
	adoptedTaskID              string
	releaseOnce                sync.Once
	releaseErr                 error
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
	var ctestExecutable string
	var ctestFile *os.File
	var ctestInfo os.FileInfo
	var unityRunnerGenerator string
	var unityRunnerGeneratorFile *os.File
	var unityRunnerGeneratorInfo os.FileInfo
	fail := func() (*executionBoundary, error) {
		if unityRunnerGeneratorFile != nil {
			_ = unityRunnerGeneratorFile.Close()
		}
		if ctestFile != nil {
			_ = ctestFile.Close()
		}
		_ = executableFile.Close()
		return nil, task.ErrInvalidArgument
	}
	if installation.CTestExecutable != "" {
		ctestExecutable, err = filepath.Abs(installation.CTestExecutable)
		if err != nil || filepath.Clean(ctestExecutable) != installation.CTestExecutable ||
			ctestExecutable == executable {
			return fail()
		}
		ctestFile, ctestInfo, err = pinExecutable(ctestExecutable)
		if err != nil {
			return fail()
		}
	}
	if installation.UnityRunnerGenerator != (cmake.ProductExecutable{}) {
		if !installation.UnityRunnerGenerator.Valid() {
			return fail()
		}
		unityRunnerGenerator, err = filepath.Abs(installation.UnityRunnerGenerator.Path)
		if err != nil || filepath.Clean(unityRunnerGenerator) != installation.UnityRunnerGenerator.Path ||
			unityRunnerGenerator == executable || unityRunnerGenerator == ctestExecutable {
			return fail()
		}
		unityRunnerGeneratorFile, unityRunnerGeneratorInfo, err = pinExecutable(unityRunnerGenerator)
		if err != nil {
			return fail()
		}
		digest, digestErr := pinnedExecutableDigest(unityRunnerGeneratorFile)
		if digestErr != nil || digest != installation.UnityRunnerGenerator.SHA256 {
			return fail()
		}
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
		executableInfo:  executableInfo,
		ctestExecutable: ctestExecutable, ctestFile: ctestFile,
		ctestInfo:                  ctestInfo,
		unityRunnerGenerator:       unityRunnerGenerator,
		unityRunnerGeneratorFile:   unityRunnerGeneratorFile,
		unityRunnerGeneratorInfo:   unityRunnerGeneratorInfo,
		unityRunnerGeneratorSHA256: installation.UnityRunnerGenerator.SHA256,
		workspaceRoot:              workspaceRoot, workspaceInfo: workspaceInfo,
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
	if err != nil {
		return task.ErrInvalidArgument
	}
	var file *os.File
	var info os.FileInfo
	switch filepath.Clean(absolute) {
	case b.executable:
		file, info = b.executableFile, b.executableInfo
	case b.ctestExecutable:
		file, info = b.ctestFile, b.ctestInfo
	case b.unityRunnerGenerator:
		file, info = b.unityRunnerGeneratorFile, b.unityRunnerGeneratorInfo
	default:
		return task.ErrInvalidArgument
	}
	if file == nil {
		return task.ErrInvalidArgument
	}
	if err := validatePinnedExecutable(
		file,
		info,
		absolute,
	); err != nil {
		return task.ErrInvalidArgument
	}
	if b.unityRunnerGeneratorFile != nil {
		if err := validatePinnedExecutable(
			b.unityRunnerGeneratorFile,
			b.unityRunnerGeneratorInfo,
			b.unityRunnerGenerator,
		); err != nil {
			return task.ErrInvalidArgument
		}
		digest, err := pinnedExecutableDigest(b.unityRunnerGeneratorFile)
		if err != nil || digest != b.unityRunnerGeneratorSHA256 {
			return task.ErrInvalidArgument
		}
	}
	return nil
}

func pinnedExecutableDigest(file *os.File) (string, error) {
	if file == nil {
		return "", errors.New("executable pin is closed")
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
		ctestFile := b.ctestFile
		unityRunnerGeneratorFile := b.unityRunnerGeneratorFile
		b.lock = nil
		b.executableFile = nil
		b.ctestFile = nil
		b.unityRunnerGeneratorFile = nil
		b.mu.Unlock()

		var result error
		if lock != nil {
			result = errors.Join(result, lock.Release())
		}
		if executableFile != nil {
			result = errors.Join(result, executableFile.Close())
		}
		if ctestFile != nil {
			result = errors.Join(result, ctestFile.Close())
		}
		if unityRunnerGeneratorFile != nil {
			result = errors.Join(result, unityRunnerGeneratorFile.Close())
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
