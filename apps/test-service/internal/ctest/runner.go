package ctest

import (
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
)

var ErrInvalidRunnerPlan = errors.New("invalid CTest runner plan")

type Runner struct {
	installation cmake.Installation
}

func NewRunner(installation cmake.Installation) (*Runner, error) {
	if installation.Executable == "" || installation.CTestExecutable == "" ||
		installation.Version == "" || installation.Identity == "" ||
		!absoluteCleanPath(installation.Executable) ||
		!absoluteCleanPath(installation.CTestExecutable) ||
		installation.Executable == installation.CTestExecutable ||
		filepath.Dir(installation.Executable) !=
			filepath.Dir(installation.CTestExecutable) {
		return nil, ErrInvalidRunnerPlan
	}
	return &Runner{installation: installation}, nil
}

func (runner *Runner) ShowOnlyPlan(
	profile cmake.BuildProfile,
) (task.ExecutionStep, error) {
	if runner == nil || profile.ID == "" || profile.ProjectID == "" ||
		!absoluteCleanPath(profile.BinaryDir) {
		return task.ExecutionStep{}, ErrInvalidRunnerPlan
	}
	args := []string{"--test-dir", profile.BinaryDir}
	if profile.Configuration != "" {
		if !validArgument(profile.Configuration) {
			return task.ExecutionStep{}, ErrInvalidRunnerPlan
		}
		args = append(args, "-C", profile.Configuration)
	}
	args = append(args, "--show-only=json-v1")
	return runner.step(
		"ctest-show-only",
		task.StepTestDiscovery,
		profile.BinaryDir,
		args,
	), nil
}

func (runner *Runner) OpaqueRunPlan(
	descriptor ExecutionDescriptor,
	timeout time.Duration,
) (task.ExecutionStep, error) {
	if runner == nil || descriptor.Blocked ||
		!validLogicalName(descriptor.LogicalName) ||
		!absoluteCleanPath(descriptor.TestDirectory) ||
		timeout < time.Millisecond || timeout > 24*time.Hour ||
		descriptor.Configuration != "" && !validArgument(descriptor.Configuration) {
		return task.ExecutionStep{}, ErrInvalidRunnerPlan
	}
	args := []string{"--test-dir", descriptor.TestDirectory}
	if descriptor.Configuration != "" {
		args = append(args, "-C", descriptor.Configuration)
	}
	args = append(
		args,
		"--output-on-failure",
		"--no-tests=error",
		"--timeout", strconv.FormatInt(int64(math.Ceil(timeout.Seconds())), 10),
		"-R", exactNamePattern(descriptor.LogicalName),
	)
	return runner.step(
		"ctest-run",
		task.StepTestRun,
		descriptor.TestDirectory,
		args,
	), nil
}

func (runner *Runner) step(
	id string,
	kind task.StepKind,
	directory string,
	args []string,
) task.ExecutionStep {
	return task.ExecutionStep{
		ID:   id,
		Kind: kind,
		Process: task.ProcessSpec{
			Executable: runner.installation.CTestExecutable,
			Args:       append([]string(nil), args...),
			Env:        []string{},
			Dir:        directory,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(runner.installation.CTestExecutable),
			Args:       append([]string(nil), args...),
		},
	}
}

func exactNamePattern(value string) string {
	var result strings.Builder
	result.Grow(len(value) + 2)
	result.WriteByte('^')
	for _, character := range value {
		if strings.ContainsRune(`\.^$*+?()[]{}|`, character) {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	result.WriteByte('$')
	return result.String()
}

func absoluteCleanPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func validLogicalName(value string) bool {
	return validArgument(value) && len(value) <= 64*1024
}

func validArgument(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}
