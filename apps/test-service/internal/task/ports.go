package task

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("state conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrStorageUnavailable  = errors.New("storage unavailable")
)

type Mutation struct {
	Task        Task
	Expected    Status
	Events      []EventDraft
	PutLease    *ProcessLease
	DeleteLease bool
	Artifacts   []Artifact
}

type Store interface {
	Create(context.Context, Task, EventDraft) (Task, []Event, error)
	FindByIdempotencyKey(context.Context, string) (Task, error)
	Get(context.Context, string) (Task, error)
	List(context.Context, string, int) (Page[Task], error)
	Apply(context.Context, Mutation) (Task, []Event, error)
	AppendEvent(context.Context, string, EventDraft) (Event, error)
	UpdateLease(context.Context, ProcessLease) error
	Watermark(context.Context) (int64, error)
	EventsAfter(context.Context, int64, int64, int) ([]Event, error)
	ListArtifacts(context.Context, string, string, int) (Page[Artifact], error)
	GetArtifact(context.Context, string) (Artifact, error)
	ActiveLeases(context.Context) ([]ProcessLease, error)
	RecoverInterrupted(context.Context, time.Time) ([]Event, error)
	ReferencedArtifactPaths(context.Context) (map[string]struct{}, error)
	Close() error
}

type Publisher interface {
	Publish(Event)
}

type ProcessFactory interface {
	Prepare(context.Context, ProcessSpec, string, string) (ManagedProcess, error)
}

type ProcessSpec struct {
	Executable string
	Args       []string
	Env        []string
	Dir        string
}

type ProcessOutput struct {
	Stream string
	Data   []byte
}

type ProcessResult struct {
	ExitCode int
	Err      error
}

type ManagedProcess interface {
	Lease() ProcessLease
	Start(context.Context) error
	Output() <-chan ProcessOutput
	Done() <-chan ProcessResult
	Terminate(context.Context, time.Duration) error
	Close(context.Context) error
}

type ArtifactWriter interface {
	CommitJSON(context.Context, string, string, time.Time, any) (Artifact, error)
}

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type IDGenerator func() string

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

func (RealClock) After(delay time.Duration) <-chan time.Time { return time.After(delay) }
