//go:build windows

package probe

import (
	"context"
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

	"unit-test-ide.local/test-service/internal/winprocess"
)

type windowsProcessTree struct {
	processOwner *winprocess.HandleOwner
	jobOwner     *winprocess.HandleOwner
	readers      []*os.File
	copyDone     chan struct{}

	closeReadersOnce sync.Once
	terminateOnce    sync.Once
	terminateErr     error
}

func startProcessTree(ctx context.Context, spec Spec, stdout, stderr io.Writer) (processTree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeProbeFiles(stdoutReader, stdoutWriter)
		return nil, err
	}
	stdin, err := os.OpenFile("NUL", os.O_RDONLY, 0)
	if err != nil {
		closeProbeFiles(stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, err
	}

	job, err := createProbeJob()
	if err != nil {
		closeProbeFiles(stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, err
	}
	jobOwner := winprocess.NewHandleOwner(job, windows.CloseHandle)
	info, err := createSuspendedProbeProcess(
		spec,
		windows.Handle(stdin.Fd()),
		windows.Handle(stdoutWriter.Fd()),
		windows.Handle(stderrWriter.Fd()),
	)
	if err != nil {
		_ = jobOwner.CloseEventually()
		closeProbeFiles(stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, err
	}
	failCreated := func() {
		_ = winprocess.FailCreatedProcess(info.Process, info.Thread, winprocess.DefaultOperations(), 250*time.Millisecond)
		_ = jobOwner.CloseEventually()
		closeProbeFiles(stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
	}
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		failCreated()
		return nil, errors.New("assign probe process to job")
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		failCreated()
		return nil, errors.New("resume probe process")
	}
	_ = winprocess.NewHandleOwner(info.Thread, windows.CloseHandle).CloseEventually()
	closeProbeFiles(stdin, stdoutWriter, stderrWriter)

	tree := &windowsProcessTree{
		processOwner: winprocess.NewHandleOwner(info.Process, windows.CloseHandle),
		jobOwner:     jobOwner,
		readers:      []*os.File{stdoutReader, stderrReader},
		copyDone:     make(chan struct{}),
	}
	go tree.copyOutput(stdout, stderr)
	return tree, nil
}

func (tree *windowsProcessTree) Terminate() error {
	tree.terminateOnce.Do(func() {
		used, operationErr, closeErr := tree.jobOwner.UseExclusiveAndCloseEventually(func(job windows.Handle) error {
			terminateErr := windows.TerminateJobObject(job, 1)
			if !waitProbeJobEmpty(job, time.Second) {
				return errors.Join(terminateErr, errors.New("probe job remained active"))
			}
			return terminateErr
		})
		if !used {
			tree.terminateErr = errors.New("probe job handle unavailable")
		} else {
			tree.terminateErr = errors.Join(operationErr, closeErr)
		}
		if tree.terminateErr != nil {
			_, processErr := tree.processOwner.Use(func(process windows.Handle) error {
				return windows.TerminateProcess(process, 1)
			})
			tree.terminateErr = errors.Join(tree.terminateErr, processErr)
		}
	})
	return tree.terminateErr
}

func (tree *windowsProcessTree) Wait() (int, error) {
	exitCode := -1
	var waitErr error
	used, err := tree.processOwner.Use(func(process windows.Handle) error {
		result, err := windows.WaitForSingleObject(process, windows.INFINITE)
		if err != nil || result != windows.WAIT_OBJECT_0 {
			return errors.Join(err, errors.New("wait for probe process"))
		}
		var code uint32
		if err := windows.GetExitCodeProcess(process, &code); err != nil {
			return err
		}
		exitCode = int(code)
		return nil
	})
	if !used {
		waitErr = errors.New("probe process handle unavailable")
	} else {
		waitErr = err
	}
	cleanupErr := tree.Terminate()
	select {
	case <-tree.copyDone:
	case <-time.After(waitDelay):
		tree.closeReaders()
		<-tree.copyDone
		cleanupErr = errors.Join(cleanupErr, errors.New("probe output pipes required forced closure"))
	}
	if err := tree.processOwner.CloseEventually(); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return exitCode, errors.Join(waitErr, cleanupErr)
}

func (tree *windowsProcessTree) copyOutput(stdout, stderr io.Writer) {
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(stdout, tree.readers[0])
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(stderr, tree.readers[1])
	}()
	copies.Wait()
	tree.closeReaders()
	close(tree.copyDone)
}

func (tree *windowsProcessTree) closeReaders() {
	tree.closeReadersOnce.Do(func() {
		closeProbeFiles(tree.readers...)
	})
}

func createProbeJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func createSuspendedProbeProcess(spec Spec, stdin, stdout, stderr windows.Handle) (windows.ProcessInformation, error) {
	var info windows.ProcessInformation
	inherited := []windows.Handle{stdin, stdout, stderr}
	for _, handle := range inherited {
		if handle == 0 || handle == windows.InvalidHandle {
			return info, errors.New("invalid probe stdio handle")
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
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inherited[0]),
		uintptr(len(inherited))*unsafe.Sizeof(inherited[0]),
	); err != nil {
		return info, err
	}
	startup := windows.StartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(startup))
	startup.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startup.StartupInfo.StdInput = stdin
	startup.StartupInfo.StdOutput = stdout
	startup.StartupInfo.StdErr = stderr
	startup.ProcThreadAttributeList = attributes.List()

	application, err := windows.UTF16PtrFromString(spec.Executable)
	if err != nil {
		return info, err
	}
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{spec.Executable}, spec.Args...)))
	if err != nil {
		return info, err
	}
	environment, err := probeWindowsEnvironmentBlock(spec.Env)
	if err != nil {
		return info, err
	}
	var directory *uint16
	if spec.Dir != "" {
		directory, err = windows.UTF16PtrFromString(spec.Dir)
		if err != nil {
			return info, err
		}
	}
	flags := uint32(
		windows.CREATE_SUSPENDED |
			windows.CREATE_NO_WINDOW |
			windows.CREATE_UNICODE_ENVIRONMENT |
			windows.EXTENDED_STARTUPINFO_PRESENT,
	)
	err = windows.CreateProcess(
		application,
		&commandLine[0],
		nil,
		nil,
		true,
		flags,
		&environment[0],
		directory,
		&startup.StartupInfo,
		&info,
	)
	return info, err
}

func probeWindowsEnvironmentBlock(environment []string) ([]uint16, error) {
	environment = append([]string(nil), environment...)
	sort.SliceStable(environment, func(left, right int) bool {
		return strings.ToUpper(environment[left]) < strings.ToUpper(environment[right])
	})
	block := make([]uint16, 0)
	for _, entry := range environment {
		if strings.IndexByte(entry, 0) >= 0 {
			return nil, errors.New("probe environment contains NUL")
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

func waitProbeJobEmpty(job windows.Handle, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for {
		accounting := probeJobAccountingInformation{}
		err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)),
			uint32(unsafe.Sizeof(accounting)),
			nil,
		)
		if err != nil {
			return false
		}
		if accounting.ActiveProcesses == 0 {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type probeJobAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func closeProbeFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
