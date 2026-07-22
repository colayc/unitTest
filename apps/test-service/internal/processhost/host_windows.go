//go:build windows

package processhost

import (
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

const windowsPostKillWait = time.Second

type windowsPlatform struct {
	operations windowsTargetOperations
}

var _ Platform = (*windowsPlatform)(nil)

type windowsTarget struct {
	process windows.Handle
	job     windows.Handle
	pid     int
	ops     windowsTargetOperations

	waitOnce sync.Once
	waitDone chan struct{}
	waitCode int
	waitErr  error
	jobOnce  sync.Once
	jobErr   error
}

var _ Target = (*windowsTarget)(nil)

type windowsTargetOperations struct {
	createProtectedJob func(uint32) (windows.Handle, error)
	createSuspended    func(processcontrol.Spec, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error)
	assignProcess      func(windows.Handle, windows.Handle) error
	resumeThread       func(windows.Handle) error
	terminateJob       func(windows.Handle, uint32) error
	terminateProcess   func(windows.Handle, uint32) error
	waitProcess        func(windows.Handle, uint32) (uint32, error)
	exitCode           func(windows.Handle) (uint32, error)
	closeHandle        func(windows.Handle) error
}

func NewPlatform() Platform { return newWindowsPlatform(defaultWindowsTargetOperations()) }

func newWindowsPlatform(operations windowsTargetOperations) *windowsPlatform {
	return &windowsPlatform{operations: operations}
}

func defaultWindowsTargetOperations() windowsTargetOperations {
	return windowsTargetOperations{
		createProtectedJob: createWindowsProtectedJob,
		createSuspended:    createSuspendedWindowsTarget,
		assignProcess:      windows.AssignProcessToJobObject,
		resumeThread: func(thread windows.Handle) error {
			_, err := windows.ResumeThread(thread)
			return err
		},
		terminateJob:     windows.TerminateJobObject,
		terminateProcess: windows.TerminateProcess,
		waitProcess:      windows.WaitForSingleObject,
		exitCode: func(process windows.Handle) (uint32, error) {
			var code uint32
			err := windows.GetExitCodeProcess(process, &code)
			return code, err
		},
		closeHandle: windows.CloseHandle,
	}
}

func (platform *windowsPlatform) Start(spec processcontrol.Spec, stdout, stderr io.Writer) (Target, error) {
	stdoutFile, stdoutOK := stdout.(*os.File)
	stderrFile, stderrOK := stderr.(*os.File)
	if !stdoutOK || !stderrOK || stdoutFile == nil || stderrFile == nil {
		return nil, errors.New("invalid target output handles")
	}
	nul, err := os.OpenFile("NUL", os.O_RDONLY, 0)
	if err != nil {
		return nil, errors.New("target standard input unavailable")
	}
	defer nul.Close()

	job, err := platform.operations.createProtectedJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
	if err != nil {
		return nil, errors.New("target job unavailable")
	}
	info, err := platform.operations.createSuspended(spec, windows.Handle(nul.Fd()), windows.Handle(stdoutFile.Fd()), windows.Handle(stderrFile.Fd()))
	if err != nil {
		_ = platform.operations.closeHandle(job)
		return nil, errors.New("target process could not start")
	}
	closeThread := func() { _ = platform.operations.closeHandle(info.Thread) }
	failed := func() {
		_ = platform.operations.terminateProcess(info.Process, 1)
		_, _ = platform.operations.waitProcess(info.Process, windowsPostKillWaitMilliseconds)
		closeThread()
		_ = platform.operations.closeHandle(info.Process)
		_ = platform.operations.closeHandle(job)
	}
	if err := platform.operations.assignProcess(job, info.Process); err != nil {
		failed()
		return nil, errors.New("target job assignment failed")
	}
	if err := platform.operations.resumeThread(info.Thread); err != nil {
		_ = platform.operations.terminateJob(job, 1)
		failed()
		return nil, errors.New("target resume failed")
	}
	closeThread()
	return &windowsTarget{
		process:  info.Process,
		job:      job,
		pid:      int(info.ProcessId),
		ops:      platform.operations,
		waitDone: make(chan struct{}),
	}, nil
}

const windowsPostKillWaitMilliseconds = uint32(1000)

func (target *windowsTarget) PID() int          { return target.pid }
func (target *windowsTarget) ProcessGroup() int { return target.pid }

func (target *windowsTarget) Wait() (int, error) {
	target.waitOnce.Do(func() {
		defer close(target.waitDone)
		waitResult, err := target.ops.waitProcess(target.process, windows.INFINITE)
		if err != nil || waitResult != windows.WAIT_OBJECT_0 {
			target.waitCode = -1
			target.waitErr = errors.New("target wait failed")
		} else {
			code, codeErr := target.ops.exitCode(target.process)
			if codeErr != nil {
				target.waitCode = -1
				target.waitErr = errors.New("target exit status unavailable")
			} else {
				target.waitCode = int(code)
			}
		}
		if err := target.closeJob(); err != nil {
			target.waitErr = errors.Join(target.waitErr, errors.New("target job cleanup failed"))
		}
		_ = target.ops.closeHandle(target.process)
	})
	<-target.waitDone
	return target.waitCode, target.waitErr
}

func (platform *windowsPlatform) Terminate(value Target, _ time.Duration) error {
	target, ok := value.(*windowsTarget)
	if !ok || target == nil || target.pid <= 0 || target.job == 0 || target.process == 0 {
		return errors.New("invalid windows process target")
	}
	var terminateErr error
	if err := target.closeJob(); err != nil {
		terminateErr = errors.New("target job termination failed")
		if err := target.ops.terminateProcess(target.process, 1); err != nil {
			terminateErr = errors.Join(terminateErr, errors.New("target process termination failed"))
		}
	}
	_, waitErr := target.Wait()
	return errors.Join(terminateErr, waitErr)
}

func (target *windowsTarget) closeJob() error {
	target.jobOnce.Do(func() {
		terminateErr := target.ops.terminateJob(target.job, 1)
		closeErr := target.ops.closeHandle(target.job)
		target.jobErr = errors.Join(terminateErr, closeErr)
	})
	return target.jobErr
}

func createWindowsProtectedJob(limitFlags uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = limitFlags
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func createSuspendedWindowsTarget(spec processcontrol.Spec, stdin, stdout, stderr windows.Handle) (windows.ProcessInformation, error) {
	environment := targetWindowsEnvironment(spec.Env)
	return createSuspendedWindowsProcess(spec.Executable, spec.Args, spec.Dir, environment, stdin, stdout, stderr, []windows.Handle{stdin, stdout, stderr})
}

func createSuspendedWindowsProcess(executable string, args []string, dir string, environment []string, stdin, stdout, stderr windows.Handle, inherited []windows.Handle) (windows.ProcessInformation, error) {
	var info windows.ProcessInformation
	for _, handle := range inherited {
		if handle == 0 || handle == windows.InvalidHandle {
			return info, errors.New("invalid inherited handle")
		}
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return info, err
		}
		defer windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0) //nolint:errcheck
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return info, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return info, err
	}
	startup := windows.StartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(startup))
	startup.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startup.StartupInfo.StdInput = stdin
	startup.StartupInfo.StdOutput = stdout
	startup.StartupInfo.StdErr = stderr
	startup.ProcThreadAttributeList = attributes.List()

	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return info, err
	}
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{executable}, args...)))
	if err != nil {
		return info, err
	}
	environmentBlock, err := windowsEnvironmentBlock(environment)
	if err != nil {
		return info, err
	}
	var directory *uint16
	if dir != "" {
		directory, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return info, err
		}
	}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	err = windows.CreateProcess(application, &commandLine[0], nil, nil, true, flags, &environmentBlock[0], directory, &startup.StartupInfo, &info)
	return info, err
}

func targetWindowsEnvironment(extra []string) []string {
	combined := append(os.Environ(), extra...)
	result := make([]string, 0, len(combined))
	seen := make(map[string]struct{}, len(combined))
	for index := len(combined) - 1; index >= 0; index-- {
		entry := combined[index]
		separator := strings.IndexByte(entry, '=')
		if separator == 0 {
			if next := strings.IndexByte(entry[1:], '='); next >= 0 {
				separator = next + 1
			}
		}
		if separator < 0 {
			if entry != "" {
				result = append(result, entry)
			}
			continue
		}
		key := strings.ToUpper(entry[:separator])
		if key == "UNIT_TEST_IDE_STATUS_HANDLE" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func windowsEnvironmentBlock(environment []string) ([]uint16, error) {
	environment = append([]string(nil), environment...)
	sort.SliceStable(environment, func(i, j int) bool {
		return strings.ToUpper(environment[i]) < strings.ToUpper(environment[j])
	})
	block := make([]uint16, 0)
	for _, entry := range environment {
		if strings.IndexByte(entry, 0) >= 0 {
			return nil, errors.New("environment contains NUL")
		}
		for _, value := range entry {
			block = utf16.AppendRune(block, value)
		}
		block = append(block, 0)
	}
	block = append(block, 0)
	if len(environment) == 0 {
		block = append(block, 0)
	}
	return block, nil
}
