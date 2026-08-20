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
	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/coveragellvm"
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
	testExecutables            map[string]pinnedTestExecutable
	workspaceRoot              workspace.Root
	workspaceInfo              os.FileInfo
	dataRoot                   workspace.Root
	dataInfo                   os.FileInfo
	lock                       *DirectoryLock
	mu                         sync.Mutex
	adoptedTaskID              string
	releaseOnce                sync.Once
	releaseErr                 error
	coverageExecution          *coveragebundle.PreparedExecution
	coverageToolset            *coveragellvm.Toolset
	coverageBinaryDir          string
	coverageInclude            pinnedTestExecutable
}

type pinnedTestExecutable struct {
	file   *os.File
	info   os.FileInfo
	sha256 string
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
		testExecutables:            make(map[string]pinnedTestExecutable),
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
	if err := b.verifyCoveragePlanLocked(); err != nil {
		return task.ErrInvalidArgument
	}
	if b.coverageExecution != nil {
		spec := b.coverageExecution.ProcessSpec()
		if filepath.Clean(path) == spec.Executable {
			if err := b.coverageExecution.Verify(); err != nil {
				return task.ErrInvalidArgument
			}
			return nil
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return task.ErrInvalidArgument
	}
	var file *os.File
	var info os.FileInfo
	var expectedDigest string
	switch filepath.Clean(absolute) {
	case b.executable:
		file, info = b.executableFile, b.executableInfo
	case b.ctestExecutable:
		file, info = b.ctestFile, b.ctestInfo
	case b.unityRunnerGenerator:
		file, info = b.unityRunnerGeneratorFile, b.unityRunnerGeneratorInfo
	default:
		testExecutable, exists := b.testExecutables[filepath.Clean(absolute)]
		if !exists {
			return task.ErrInvalidArgument
		}
		file, info = testExecutable.file, testExecutable.info
		expectedDigest = testExecutable.sha256
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
	if expectedDigest != "" {
		digest, err := pinnedExecutableDigest(file)
		if err != nil || digest != expectedDigest {
			return task.ErrInvalidArgument
		}
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

func (b *executionBoundary) allowTestExecutable(
	state cmake.FingerprintFile,
) error {
	if b == nil || cmake.VerifyTargetArtifact(state) != nil {
		return task.ErrInvalidArgument
	}
	path, err := filepath.Abs(filepath.FromSlash(state.Path))
	if err != nil || filepath.Clean(path) != path {
		return task.ErrInvalidArgument
	}
	file, info, err := pinExecutable(path)
	if err != nil {
		return task.ErrInvalidArgument
	}
	fail := func() error {
		_ = file.Close()
		return task.ErrInvalidArgument
	}
	if cmake.VerifyTargetArtifact(state) != nil {
		return fail()
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(info, current) {
		return fail()
	}
	digest, err := pinnedExecutableDigest(file)
	if err != nil || digest != state.SHA256 {
		return fail()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil {
		return fail()
	}
	if existing, exists := b.testExecutables[path]; exists {
		_ = file.Close()
		if existing.sha256 != state.SHA256 ||
			!os.SameFile(existing.info, info) {
			return task.ErrInvalidArgument
		}
		return nil
	}
	b.testExecutables[path] = pinnedTestExecutable{
		file: file, info: info, sha256: state.SHA256,
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
		testExecutables := b.testExecutables
		coverageExecution := b.coverageExecution
		coverageToolset := b.coverageToolset
		coverageInclude := b.coverageInclude
		b.lock = nil
		b.executableFile = nil
		b.ctestFile = nil
		b.unityRunnerGeneratorFile = nil
		b.testExecutables = nil
		b.coverageExecution = nil
		b.coverageToolset = nil
		b.coverageInclude = pinnedTestExecutable{}
		b.coverageBinaryDir = ""
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
		for _, executable := range testExecutables {
			result = errors.Join(result, executable.file.Close())
		}
		if coverageExecution != nil {
			result = errors.Join(result, coverageExecution.Close())
		}
		if coverageToolset != nil {
			result = errors.Join(result, coverageToolset.Close())
		}
		if coverageInclude.file != nil {
			result = errors.Join(result, coverageInclude.file.Close())
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
	if err := b.verifyCoveragePlanLocked(); err != nil {
		return task.ErrInvalidArgument
	}
	if b.coverageExecution != nil {
		spec := b.coverageExecution.ProcessSpec()
		if filepath.Clean(path) == spec.Dir {
			if err := b.coverageExecution.Verify(); err != nil {
				return task.ErrInvalidArgument
			}
			return nil
		}
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

func (b *executionBoundary) attachCoveragePlan(options *CoverageOptions) error {
	if b == nil || !validCoverageOptions(options, "") {
		return task.ErrInvalidArgument
	}
	file, info, err := pinExecutable(options.TopLevelInclude.Path)
	if err != nil {
		return task.ErrInvalidArgument
	}
	digest, err := pinnedExecutableDigest(file)
	if err != nil || digest != options.TopLevelInclude.SHA256 {
		_ = file.Close()
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil || b.coverageInclude.file != nil || b.coverageBinaryDir != "" {
		_ = file.Close()
		return task.ErrInvalidArgument
	}
	b.coverageBinaryDir = options.BinaryDir
	b.coverageInclude = pinnedTestExecutable{file: file, info: info, sha256: digest}
	return nil
}

func (b *executionBoundary) attachCoverageToolset(toolset *coveragellvm.Toolset) error {
	if b == nil || toolset == nil || toolset.Verify() != nil {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil || b.coverageInclude.file == nil || b.coverageBinaryDir == "" || b.coverageToolset != nil {
		return task.ErrInvalidArgument
	}
	b.coverageToolset = toolset
	return nil
}

func (b *executionBoundary) verifyCoveragePlanLocked() error {
	if b.coverageBinaryDir == "" && b.coverageInclude.file == nil && b.coverageToolset == nil {
		return nil
	}
	if b.coverageBinaryDir == "" || b.coverageInclude.file == nil || b.coverageToolset == nil {
		return task.ErrInvalidArgument
	}
	if err := validatePinnedExecutable(
		b.coverageInclude.file, b.coverageInclude.info,
		b.coverageInclude.file.Name(),
	); err != nil {
		return task.ErrInvalidArgument
	}
	digest, err := pinnedExecutableDigest(b.coverageInclude.file)
	if err != nil || digest != b.coverageInclude.sha256 {
		return task.ErrInvalidArgument
	}
	if b.coverageToolset.Verify() != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

// AttachCoverageExecution transfers ownership to the boundary only after the
// execution has passed its pin and descriptor verification. A failed attach
// leaves ownership with the caller.
func (b *executionBoundary) AttachCoverageExecution(execution *coveragebundle.PreparedExecution) error {
	if b == nil || execution == nil {
		return task.ErrInvalidArgument
	}
	if err := execution.Verify(); err != nil {
		return task.ErrInvalidArgument
	}
	spec := execution.ProcessSpec()
	if spec.Executable == "" || len(spec.Args) != 4 || spec.Args[0] != "-I" || spec.Args[1] != "-S" ||
		spec.Args[2] == "" || spec.Args[3] == "" || spec.Dir == "" || len(spec.Env) != 0 || len(spec.EnvUnset) == 0 || len(spec.Batch) != 0 ||
		spec.Dir != execution.TaskRoot() || execution.DescriptorPath() != spec.Args[3] {
		return task.ErrInvalidArgument
	}
	if err := execution.ValidateProcessTarget(spec.Executable, spec.Args, spec.Env, spec.EnvUnset, spec.Dir); err != nil {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executableFile == nil || b.coverageExecution != nil {
		return task.ErrInvalidArgument
	}
	b.coverageExecution = execution
	return nil
}

func (b *executionBoundary) ValidateProcessTarget(
	executable string,
	arguments, environment, unset []string,
	directory string,
) error {
	if b == nil {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	coverageExecution := b.coverageExecution
	b.mu.Unlock()
	if coverageExecution != nil {
		spec := coverageExecution.ProcessSpec()
		if executable == spec.Executable {
			if err := coverageExecution.ValidateProcessTarget(
				executable, arguments, environment, unset, directory,
			); err != nil {
				return task.ErrInvalidArgument
			}
			return nil
		}
	}
	if err := b.ValidateExecutable(executable); err != nil {
		return task.ErrInvalidArgument
	}
	if err := b.ValidateWorkingDirectory(directory); err != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

func (b *executionBoundary) VerifyCoverageExecutionAfter() error {
	if b == nil {
		return task.ErrInvalidArgument
	}
	b.mu.Lock()
	coverageExecution := b.coverageExecution
	b.mu.Unlock()
	if coverageExecution == nil {
		return task.ErrInvalidArgument
	}
	if err := coverageExecution.VerifyAfter(); err != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

// PinnedCoverageOutput hands downstream consumers the retained output handle;
// consumers must use ReadAll and cannot reopen a mutable pathname.
func (b *executionBoundary) PinnedCoverageOutput() (*coveragebundle.PinnedOutput, error) {
	if b == nil {
		return nil, task.ErrInvalidArgument
	}
	b.mu.Lock()
	execution := b.coverageExecution
	b.mu.Unlock()
	if execution == nil {
		return nil, task.ErrInvalidArgument
	}
	output, err := execution.PinnedOutput()
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	return output, nil
}
