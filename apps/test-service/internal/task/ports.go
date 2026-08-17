package task

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("state conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrStorageUnavailable  = errors.New("storage unavailable")
)

type SimulationStart struct {
	IdempotencyKey string
	Scenario       Scenario
	Timeout        time.Duration
}

type Mutation struct {
	Task           Task
	Expected       Status
	Steps          []StepMutation
	AppendSteps    []StepSnapshot
	Events         []EventDraft
	PutLease       *ProcessLease
	DeleteLease    bool
	Artifacts      []Artifact
	FinishRun      *testdomain.TestRun
	FinishCoverage *CoverageCompletion
}

type CoverageCompletion struct {
	Run      coveragedomain.Run
	Expected coveragedomain.Status
	Report   *coveragedomain.Report
}

type StepMutation struct {
	Step     StepSnapshot
	Expected StepStatus
}

type Store interface {
	Create(context.Context, Task, []StepSnapshot, EventDraft) (Task, []Event, error)
	FindByIdempotencyKey(context.Context, string) (Task, error)
	Get(context.Context, string) (Task, error)
	List(context.Context, string, int, ...Kind) (Page[Task], error)
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

type TestTaskStore interface {
	CreateTestTask(
		context.Context,
		Task,
		[]StepSnapshot,
		EventDraft,
		testdomain.TestRun,
	) (Task, []Event, error)
}

type CoverageTaskStore interface {
	CreateCoverageTask(
		context.Context,
		Task,
		[]StepSnapshot,
		EventDraft,
		coveragedomain.Run,
		testdomain.TestRun,
	) (Task, []Event, error)
}

type CoverageRepository interface {
	GetCoverageRun(context.Context, string) (coveragedomain.Run, error)
	ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error)
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
}

type TestCatalogRepository interface {
	PublishCatalog(context.Context, testdomain.Catalog, Artifact) error
	GetCatalog(context.Context, string, string) (testdomain.Catalog, error)
	PageCatalog(context.Context, testdomain.CatalogPageRequest) (testdomain.CatalogPage, error)
}

type TestRunRepository interface {
	CreateRun(context.Context, testdomain.TestRun) error
	StartRun(context.Context, string, time.Time) error
	AppendResult(context.Context, string, testdomain.TestItemResult) error
	FinishRun(context.Context, testdomain.TestRun, []Artifact) error
	GetRun(context.Context, string) (testdomain.TestRun, error)
	ListRuns(context.Context, testdomain.RunPageRequest) (testdomain.RunPage, error)
}

type QueuedPlanStore interface {
	ReplaceQueuedPlan(context.Context, string, string, string, []StepSnapshot) (Task, error)
}

type Publisher interface {
	Publish(Event)
}

type ProcessFactory interface {
	Prepare(context.Context, ProcessSpec, string, string) (ManagedProcess, error)
}

type StepObserver interface {
	Succeeded(context.Context, Task, ExecutionStep) error
}

type PlanContinuation interface {
	AfterStep(
		context.Context,
		Task,
		ExecutionStep,
		StepResult,
	) (Continuation, error)
}

type ResultInterpreter interface {
	Interpret(
		context.Context,
		Task,
		ExecutionStep,
		ProcessResult,
	) (StepVerdict, error)
}

type ResultOutputObserver interface {
	ObserveOutput(
		context.Context,
		Task,
		ExecutionStep,
		ProcessOutput,
	) error
}

// DomainEventSource exposes bounded domain events that were produced by a
// runtime-only interpreter. The Manager remains the only owner of the durable
// Task journal and publishes each event only after Store.AppendEvent commits.
type DomainEventSource interface {
	DrainDomainEvents() []DomainEvent
}

type DomainEvent struct {
	Type    EventType
	Payload json.RawMessage
}

// CompletionPreparer projects domain state and writes domain artifacts before
// the Task terminal mutation. Store.Apply then commits the returned TestRun,
// artifact metadata, Task snapshot, and terminal events in one transaction.
type CompletionPreparer interface {
	PrepareCompletion(
		context.Context,
		Task,
		time.Time,
		Outcome,
		ArtifactSink,
		IDGenerator,
	) (DomainCompletion, error)
}

type DomainCompletion struct {
	TestRun *testdomain.TestRun
	Events  []DomainEvent
}

type ProcessSpec struct {
	// ProcessSpec is runtime-only service state. Its Env field must not be
	// persisted or exposed through the protocol.
	Executable string
	Args       []string
	Env        []string
	EnvUnset   []string
	Dir        string
	Batch      []ProcessBatchItem
}

type ProcessBatchItem struct {
	ID         string
	Executable string
	Args       []string
	Env        []string
	EnvUnset   []string
	Dir        string
	Timeout    time.Duration
}

type ProcessOutput struct {
	Source string
	Stream string
	Data   []byte
}

type ProcessResult struct {
	ExitCode int
	TimedOut bool
	Err      error
	Children []ProcessChildResult
}

type ProcessChildResult struct {
	ID       string
	ExitCode int
	TimedOut bool
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

type ArtifactSink interface {
	AppendOutput(context.Context, string, string, []byte) error
	AppendDiagnostic(context.Context, diagnostic.Diagnostic) error
	CommitJSON(context.Context, string, string, any) error
	CommitJSONLines(context.Context, string, string, []json.RawMessage) error
	Finalize(context.Context, time.Time) ([]Artifact, error)
	Abort(context.Context) error
}

type ArtifactWriter interface {
	OpenTask(context.Context, string, Kind) (ArtifactSink, error)
}

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type IDGenerator func() string

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

func (RealClock) After(delay time.Duration) <-chan time.Time { return time.After(delay) }
