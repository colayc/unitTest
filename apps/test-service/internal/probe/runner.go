package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout   = 5 * time.Second
	defaultMaxOutput = 256 * 1024
	waitDelay        = time.Second
)

type defaultRunner struct{}

func NewRunner() Runner {
	return defaultRunner{}
}

func (defaultRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return Result{ExitCode: -1}, err
	}

	runContext, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	stdout := newLimitedBuffer(spec.MaxOutput)
	stderr := newLimitedBuffer(spec.MaxOutput)
	command := exec.CommandContext(runContext, spec.Executable, spec.Args...)
	command.Env = append([]string{}, spec.Env...)
	command.Dir = spec.Dir
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = waitDelay

	if err := command.Start(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Result{ExitCode: -1}, contextErr
		}
		return Result{ExitCode: -1}, fmt.Errorf("start probe: %w", err)
	}

	processDone := make(chan struct{})
	limitMonitorDone := make(chan struct{})
	go func() {
		defer close(limitMonitorDone)
		select {
		case <-stdout.exceeded:
			cancel()
			_ = command.Process.Kill()
		case <-stderr.exceeded:
			cancel()
			_ = command.Process.Kill()
		case <-processDone:
		}
	}()

	waitErr := command.Wait()
	close(processDone)
	<-limitMonitorDone

	result := Result{
		ExitCode: command.ProcessState.ExitCode(),
		Stdout:   stdout.bytes(),
		Stderr:   stderr.bytes(),
	}
	if stdout.didExceed() || stderr.didExceed() {
		result.ExitCode = -1
		return result, ErrOutputLimit
	}
	if contextErr := ctx.Err(); contextErr != nil {
		result.ExitCode = -1
		return result, contextErr
	}
	if runContext.Err() != nil {
		result.ExitCode = -1
		return result, ErrTimeout
	}
	if waitErr != nil {
		return result, fmt.Errorf("wait for probe: %w", waitErr)
	}
	return result, nil
}

func normalizeSpec(spec Spec) (Spec, error) {
	if spec.Executable == "" || !filepath.IsAbs(spec.Executable) {
		return Spec{}, fmt.Errorf("%w: executable must be absolute", ErrInvalidSpec)
	}
	info, err := os.Stat(spec.Executable)
	if err != nil {
		return Spec{}, fmt.Errorf("%w: inspect executable: %v", ErrInvalidSpec, err)
	}
	if !info.Mode().IsRegular() {
		return Spec{}, fmt.Errorf("%w: executable is not a regular file", ErrInvalidSpec)
	}
	if spec.Dir != "" {
		if !filepath.IsAbs(spec.Dir) {
			return Spec{}, fmt.Errorf("%w: working directory must be absolute", ErrInvalidSpec)
		}
		info, err := os.Stat(spec.Dir)
		if err != nil || !info.IsDir() {
			return Spec{}, fmt.Errorf("%w: working directory is not a directory", ErrInvalidSpec)
		}
	}
	for _, argument := range spec.Args {
		if strings.IndexByte(argument, 0) >= 0 {
			return Spec{}, fmt.Errorf("%w: argument contains NUL", ErrInvalidSpec)
		}
	}
	if err := validateEnvironment(spec.Env); err != nil {
		return Spec{}, err
	}
	if spec.Timeout < 0 {
		return Spec{}, fmt.Errorf("%w: timeout is negative", ErrInvalidSpec)
	}
	if spec.Timeout == 0 {
		spec.Timeout = defaultTimeout
	}
	if spec.MaxOutput < 0 {
		return Spec{}, fmt.Errorf("%w: output limit is negative", ErrInvalidSpec)
	}
	if spec.MaxOutput == 0 {
		spec.MaxOutput = defaultMaxOutput
	}
	spec.Executable = filepath.Clean(spec.Executable)
	if spec.Dir != "" {
		spec.Dir = filepath.Clean(spec.Dir)
	}
	spec.Args = append([]string(nil), spec.Args...)
	spec.Env = append([]string{}, spec.Env...)
	return spec, nil
}

func validateEnvironment(environment []string) error {
	seen := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		if strings.IndexByte(entry, 0) >= 0 {
			return fmt.Errorf("%w: environment contains NUL", ErrInvalidSpec)
		}
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			return fmt.Errorf("%w: malformed environment entry", ErrInvalidSpec)
		}
		key := entry[:separator]
		if strings.IndexByte(key, '=') >= 0 {
			return fmt.Errorf("%w: malformed environment key", ErrInvalidSpec)
		}
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate environment key", ErrInvalidSpec)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type limitedBuffer struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	exceeded chan struct{}
	once     sync.Once
	overflow bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{
		data:     make([]byte, 0, limit),
		limit:    limit,
		exceeded: make(chan struct{}),
	}
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	remaining := buffer.limit - len(buffer.data)
	if remaining > len(data) {
		remaining = len(data)
	}
	if remaining > 0 {
		buffer.data = append(buffer.data, data[:remaining]...)
	}
	if remaining < len(data) {
		buffer.overflow = true
		buffer.once.Do(func() {
			close(buffer.exceeded)
		})
	}
	return len(data), nil
}

func (buffer *limitedBuffer) bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data...)
}

func (buffer *limitedBuffer) didExceed() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.overflow
}
