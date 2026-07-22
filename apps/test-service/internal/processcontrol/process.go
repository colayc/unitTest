package processcontrol

import (
	"context"
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
}

type Output struct {
	Stream Stream
	Data   []byte
}

type Result struct {
	ExitCode int
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
