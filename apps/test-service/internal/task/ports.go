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
