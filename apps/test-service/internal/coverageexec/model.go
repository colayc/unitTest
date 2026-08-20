// Package coverageexec owns the live execution of a durable queued
// CoverageRun. Native capabilities and paths are retained only by this
// package and never enter the persisted coverage domain model.
package coverageexec

import (
	"context"
	"io"
	"reflect"
	"sync"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type TaskResumer interface {
	ResumeQueued(context.Context, task.ResumeRequest) (task.Task, error)
}

type Store interface {
	task.CoverageRepository
	task.TestRunRepository
	Get(context.Context, string) (task.Task, error)
	GetRunForTask(context.Context, string) (testdomain.TestRun, error)
}

type BuildPreparer interface {
	PreparePlan(context.Context, build.StartRequest) (*build.PreparedPlan, error)
}

type EmbeddedTestPreparer interface {
	PrepareEmbedded(context.Context, testrun.EmbeddedRequest) (testrun.EmbeddedRun, error)
}

type AdapterInput struct {
	Toolchain   toolchain.Instance
	TaskRoot    string
	ProfileRoot string
}

type PreparedAdapter interface {
	Toolset() *coveragellvm.Toolset
	Instrumentation() coveragellvm.Instrumentation
	Allocator() testrun.ProfileAllocator
	SealProfiles([]testrun.ProfileExpectation, []testrun.InvocationOutcome) (coveragellvm.Manifest, error)
	Collector(coveragellvm.Manifest, []coveragerun.TrustedPath) (merge, export task.ProcessSpec, err error)
	Close() error
}

type Adapter interface {
	Prepare(context.Context, AdapterInput) (PreparedAdapter, error)
}

type Config struct {
	Tasks         TaskResumer
	Store         Store
	Build         BuildPreparer
	Tests         EmbeddedTestPreparer
	Adapter       Adapter
	WorkspaceRoot workspace.Root
	ExecutionRoot string
	Clock         task.Clock
	NewID         task.IDGenerator
}

type liveExecution interface {
	task.PlanContinuation
	task.ResultInterpreter
	task.ResultOutputObserver
	task.DomainEventSource
	task.ServiceActionExecutor
	task.CompletionPreparer
	io.Closer
}

type Coordinator struct {
	config     Config
	mu         sync.Mutex
	executions map[string]liveExecution
	closed     bool
	preparing  map[string]chan struct{}
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ liveExecution = (*execution)(nil)
