package processcontrol

import (
	"context"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Spec struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
	EnvUnset   []string
	Batch      []BatchItem
}

type BatchItem struct {
	ID         string
	Executable string
	Args       []string
	Dir        string
	Env        []string
	EnvUnset   []string
	TimeoutMS  int64
}

type Output struct {
	Source string
	Stream Stream
	Data   []byte
}

type Result struct {
	ExitCode int
	Err      error
	Children []ChildResult
}

type ChildResult struct {
	ID       string
	ExitCode int
	TimedOut bool
	Err      error
}

type Process interface {
	Lease() task.ProcessLease
	Start(context.Context) error
	Output() <-chan Output
	Done() <-chan Result
	Terminate(context.Context, time.Duration) error
	Close(context.Context) error
}

type Runner interface {
	Prepare(context.Context, Spec, string, string) (Process, error)
	Cleanup(context.Context, task.ProcessLease, time.Duration) error
}

func validateBatchStartedStatus(
	status HostStatus,
	batch []BatchItem,
) error {
	if status.Kind != "started" ||
		status.PID <= 0 ||
		status.ProcessGroup != 0 ||
		len(status.TargetProcessGroups) != len(batch) {
		return errProcessHostFailed
	}
	seen := make(map[int]struct{}, len(batch))
	for _, group := range status.TargetProcessGroups {
		if group <= 1 {
			return errProcessHostFailed
		}
		if _, duplicate := seen[group]; duplicate {
			return errProcessHostFailed
		}
		seen[group] = struct{}{}
	}
	return nil
}

func validBatchOutputStatus(
	status HostStatus,
	batch bool,
) bool {
	return batch &&
		status.Source != "" &&
		len(status.Source) <= 64 &&
		(status.Stream == StreamStdout ||
			status.Stream == StreamStderr) &&
		len(status.Data) != 0
}

func hostChildResults(
	values []HostChildResult,
	batch []BatchItem,
) ([]ChildResult, bool) {
	if len(values) != len(batch) {
		return nil, false
	}
	expected := make(map[string]struct{}, len(batch))
	for _, item := range batch {
		expected[item.ID] = struct{}{}
	}
	result := make([]ChildResult, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if value.ID == "" ||
			strings.ContainsRune(value.ID, '\x00') {
			return nil, false
		}
		if _, exists := expected[value.ID]; !exists {
			return nil, false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return nil, false
		}
		if value.ErrorCode != "" &&
			value.ErrorCode != "PROCESS_WAIT_FAILED" {
			return nil, false
		}
		seen[value.ID] = struct{}{}
		result[index] = ChildResult{
			ID:       value.ID,
			ExitCode: value.ExitCode,
			TimedOut: value.TimedOut,
		}
		if value.ErrorCode != "" {
			result[index].Err = errProcessHostFailed
		}
	}
	return result, true
}
