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
	"unit-test-ide.local/test-service/internal/winprocess"
)

const windowsPostKillWait = time.Second

type windowsPlatform struct {
	operations  windowsTargetOperations
	cleanupWait time.Duration
}

var _ Platform = (*windowsPlatform)(nil)

type windowsTarget struct {
	processOwner *winprocess.HandleOwner
	jobOwner     *winprocess.HandleOwner
	pid          int
	ops          windowsTargetOperations

	waitOnce    sync.Once
	waitDone    chan struct{}
	waitCode    int
	waitErr     error
	cleanupWait time.Duration
}

var _ Target = (*windowsTarget)(nil)

type windowsTargetOperations struct {
	createProtectedJob     func(uint32) (windows.Handle, error)
	createSuspended        func(processcontrol.Spec, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error)
	assignProcess          func(windows.Handle, windows.Handle) error
	resumeThread           func(windows.Handle) error
	terminateJob           func(windows.Handle, uint32) error
	terminateProcess       func(windows.Handle, uint32) error
	nativeTerminateProcess func(windows.Handle, uint32) error
	waitProcess            func(windows.Handle, uint32) (uint32, error)
	exitCode               func(windows.Handle) (uint32, error)
	queryActiveProcesses   func(windows.Handle) (uint32, error)
	closeHandle            func(windows.Handle) error
}

func NewPlatform() Platform { return newWindowsPlatform(defaultWindowsTargetOperations()) }

func newWindowsPlatform(operations windowsTargetOperations) *windowsPlatform {
	return &windowsPlatform{operations: operations, cleanupWait: windowsPostKillWait}
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
		terminateJob:           windows.TerminateJobObject,
		terminateProcess:       windows.TerminateProcess,
		nativeTerminateProcess: winprocess.NativeTerminateProcess,
		waitProcess:            windows.WaitForSingleObject,
		exitCode: func(process windows.Handle) (uint32, error) {
			var code uint32
			err := windows.GetExitCodeProcess(process, &code)
			return code, err
		},
		queryActiveProcesses: windowsJobActiveProcesses,
		closeHandle:          windows.CloseHandle,
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
	jobOwner := winprocess.NewHandleOwner(job, platform.operations.closeHandle)
	info, err := platform.operations.createSuspended(spec, windows.Handle(nul.Fd()), windows.Handle(stdoutFile.Fd()), windows.Handle(stderrFile.Fd()))
	if err != nil {
		_ = jobOwner.CloseEventually()
		return nil, errors.New("target process could not start")
	}
	failed := func() error {
		cleanupErr := winprocess.FailCreatedProcess(info.Process, info.Thread, platform.createdProcessOperations(), 250*time.Millisecond)
		_ = jobOwner.CloseEventually()
		return cleanupErr
	}
	if err := platform.operations.assignProcess(job, info.Process); err != nil {
		if cleanupErr := failed(); cleanupErr != nil {
			return nil, errors.New("target process cleanup failed")
		}
		return nil, errors.New("target job assignment failed")
	}
	if err := platform.operations.resumeThread(info.Thread); err != nil {
		_, _ = jobOwner.UseExclusive(func(handle windows.Handle) error {
			return platform.operations.terminateJob(handle, 1)
		})
		if cleanupErr := failed(); cleanupErr != nil {
			return nil, errors.New("target process cleanup failed")
		}
		return nil, errors.New("target resume failed")
	}
	_ = winprocess.NewHandleOwner(info.Thread, platform.operations.closeHandle).CloseEventually()
	cleanupWait := platform.cleanupWait
	if cleanupWait <= 0 {
		cleanupWait = windowsPostKillWait
	}
	return &windowsTarget{
		processOwner: winprocess.NewHandleOwner(info.Process, platform.operations.closeHandle),
		jobOwner:     jobOwner,
		pid:          int(info.ProcessId),
		ops:          platform.operations,
		waitDone:     make(chan struct{}),
		cleanupWait:  cleanupWait,
	}, nil
}

func (platform *windowsPlatform) createdProcessOperations() winprocess.Operations {
	return winprocess.Operations{
		Terminate:       platform.operations.terminateProcess,
		NativeTerminate: platform.operations.nativeTerminateProcess,
		Wait:            platform.operations.waitProcess,
		Close:           platform.operations.closeHandle,
	}
}

func (target *windowsTarget) PID() int          { return target.pid }
func (target *windowsTarget) ProcessGroup() int { return target.pid }

func (target *windowsTarget) Wait() (int, error) {
	target.waitOnce.Do(func() {
		defer close(target.waitDone)
		var waitResult uint32
		used, err := target.processOwner.Use(func(handle windows.Handle) error {
			var waitErr error
			waitResult, waitErr = target.ops.waitProcess(handle, windows.INFINITE)
			if waitErr != nil || waitResult != windows.WAIT_OBJECT_0 {
				return waitErr
			}
			code, codeErr := target.ops.exitCode(handle)
			if codeErr != nil {
				target.waitCode = -1
				target.waitErr = errors.New("target exit status unavailable")
			} else {
				target.waitCode = int(code)
			}
			return nil
		})
		if !used || err != nil || waitResult != windows.WAIT_OBJECT_0 {
			target.waitCode = -1
			target.waitErr = errors.New("target wait failed")
		}
		if err := target.closeJob(); err != nil {
			target.waitErr = errors.Join(target.waitErr, errors.New("target job cleanup failed"))
		}
		_ = target.processOwner.CloseEventually()
	})
	<-target.waitDone
	return target.waitCode, target.waitErr
}

func (platform *windowsPlatform) Terminate(value Target, _ time.Duration) error {
	target, ok := value.(*windowsTarget)
	if !ok || target == nil || target.pid <= 0 || target.jobOwner == nil || target.processOwner == nil {
		return errors.New("invalid windows process target")
	}
	var terminateErr error
	if err := target.closeJob(); err != nil {
		terminateErr = errors.New("target job termination failed")
		_, processErr := target.processOwner.Use(func(handle windows.Handle) error {
			return target.ops.terminateProcess(handle, 1)
		})
		if processErr != nil {
			terminateErr = errors.Join(terminateErr, errors.New("target process termination failed"))
		}
	}
	_, waitErr := target.Wait()
	return errors.Join(terminateErr, waitErr)
}

func (target *windowsTarget) closeJob() error {
	_, operationErr, closeErr := target.jobOwner.UseExclusiveAndCloseEventually(func(job windows.Handle) error {
		terminateErr := target.ops.terminateJob(job, 1)
		empty := waitWindowsJobEmpty(job, target.ops.queryActiveProcesses, target.cleanupWait)
		if !empty {
			_ = target.ops.terminateJob(job, 1)
			_, _ = target.processOwner.Use(func(process windows.Handle) error {
				return target.ops.nativeTerminateProcess(process, 1)
			})
		}
		if terminateErr != nil || !empty {
			return errors.New("target job cleanup failed")
		}
		return nil
	})
	if operationErr != nil || closeErr != nil {
		return errors.New("target job cleanup failed")
	}
	return nil
}

func waitWindowsJobEmpty(job windows.Handle, query func(windows.Handle) (uint32, error), duration time.Duration) bool {
	if job == 0 || job == windows.InvalidHandle || query == nil {
		return false
	}
	if duration < 0 {
		duration = 0
	}
	deadline := time.Now().Add(duration)
	for {
		active, err := query(job)
		if err != nil {
			return false
		}
		if active == 0 {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type windowsJobAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func windowsJobActiveProcesses(job windows.Handle) (uint32, error) {
	accounting := windowsJobAccountingInformation{}
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
		return 0, err
	}
	return accounting.ActiveProcesses, nil
}

func createWindowsProtectedJob(limitFlags uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = limitFlags
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = winprocess.NewHandleOwner(job, windows.CloseHandle).CloseEventually()
		return 0, err
	}
	return job, nil
}

func createSuspendedWindowsTarget(spec processcontrol.Spec, stdin, stdout, stderr windows.Handle) (windows.ProcessInformation, error) {
	environment := targetWindowsEnvironment(
		spec.Env,
		spec.EnvUnset,
	)
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

func targetWindowsEnvironment(
	extra []string,
	unsetValues ...[]string,
) []string {
	var unset []string
	if len(unsetValues) != 0 {
		unset = unsetValues[0]
	}
	removed := make(map[string]struct{}, len(unset))
	for _, key := range unset {
		removed[strings.ToUpper(key)] = struct{}{}
	}
	overrides := make(map[string]string, len(extra))
	for _, entry := range extra {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 {
			key := strings.ToUpper(entry[:separator])
			if !serviceOwnedTargetEnvironmentKey(key) {
				overrides[key] = entry
			}
		}
	}
	inherited := os.Environ()
	result := make([]string, 0, len(inherited)+len(extra))
	seen := make(map[string]struct{}, len(inherited)+len(extra))
	for _, entry := range inherited {
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
		if serviceOwnedTargetEnvironmentKey(key) {
			continue
		}
		if _, exists := removed[key]; exists {
			continue
		}
		if _, exists := overrides[key]; exists {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	for _, entry := range extra {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		key := strings.ToUpper(entry[:separator])
		if serviceOwnedTargetEnvironmentKey(key) {
			continue
		}
		if _, exists := removed[key]; exists {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
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
