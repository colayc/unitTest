package probe

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidSpec = errors.New("invalid probe specification")
	ErrTimeout     = errors.New("probe timed out")
	ErrOutputLimit = errors.New("probe output limit exceeded")
)

type Spec struct {
	Executable string
	Args       []string
	Env        []string
	Dir        string
	Timeout    time.Duration
	MaxOutput  int
}

type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type Runner interface {
	Run(context.Context, Spec) (Result, error)
}
