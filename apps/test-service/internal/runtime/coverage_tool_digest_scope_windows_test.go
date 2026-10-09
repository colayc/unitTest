//go:build windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/toolhash"
	"unit-test-ide.local/test-service/internal/workspace"
)

type digestScopeCoordinator struct {
	runtimeCoordinator
	generation, path string
	results          []toolhash.Result
	contexts         []context.Context
	afterDigest      func(context.Context, int) error
}

type optionalConsumerAdapter struct {
	toolchain.Adapter
	discover func(context.Context) ([]toolchain.Instance, error)
}

func (a *optionalConsumerAdapter) Discover(ctx context.Context) ([]toolchain.Instance, error) {
	return a.discover(ctx)
}

type noNativeDigestRunner struct{}

func (noNativeDigestRunner) Run(context.Context, probe.Spec) (probe.Result, error) {
	return probe.Result{}, errors.New("unexpected native probe request")
}

type inspectorDigestCoordinator struct {
	runtimeCoordinator
	inspector *discovery.Inspector
	snapshots []discovery.Snapshot
}

func (c *inspectorDigestCoordinator) Inspect(ctx context.Context) (discovery.Snapshot, error) {
	s, err := c.inspector.Inspect(ctx)
	if err == nil {
		c.snapshots = append(c.snapshots, s)
	}
	return s, err
}

// Controlled optional adapter/output and stored index metadata; actual CMake
// consumer, Registry, Inspector, guards and runtime gates. No native execution
// or claim of an on-disk replacement beneath a retained deny-delete guard.
func TestRuntimeToolDigestScopeSwallowedConsumerFailure(t *testing.T) {
	r, stored, _, q := newDigestScopeRuntime(t)
	dir := t.TempDir()
	path, alias := filepath.Join(dir, "cmake.exe"), filepath.Join(dir, "alias.exe")
	if err := os.WriteFile(path, []byte("optional tool"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var consumerErrors []error
	adapter := &optionalConsumerAdapter{discover: func(ctx context.Context) ([]toolchain.Instance, error) {
		calls++
		selected := path
		if calls == 2 {
			selected = alias
		}
		_, err := cmake.Resolve(ctx, noNativeDigestRunner{}, cmake.ResolverConfig{Override: selected})
		consumerErrors = append(consumerErrors, err)
		return nil, err
	}}
	registry, err := toolchain.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := discovery.NewInspector(r.workspaceRoot, noNativeDigestRunner{}, cmake.ResolverConfig{}, registry, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	calls, consumerErrors = 0, nil
	q.WorkspaceGeneration = baseline.Generation
	stored.index.WorkspaceGeneration = baseline.Generation
	stored.run.Request.WorkspaceGeneration = baseline.Generation
	c := &inspectorDigestCoordinator{inspector: inspector}
	r.coordinator = c
	index, err := r.ReadCurrentCoverageIndex(context.Background(), q)
	if !errors.Is(err, task.ErrStorageUnavailable) || !reflect.DeepEqual(index, coveragedetail.Index{}) {
		t.Errorf("swallowed bound failure returned index=%+v err=%v", index, err)
	}
	if calls != 2 || len(c.snapshots) != 2 {
		t.Fatalf("inspections=%d snapshots=%d", calls, len(c.snapshots))
	}
	if !errors.Is(consumerErrors[0], cmake.ErrCTestUnavailable) || !errors.Is(consumerErrors[1], cmake.ErrInvalidExecutable) {
		t.Fatalf("consumer errors=%v", consumerErrors)
	}
	for _, snapshot := range c.snapshots {
		if snapshot.Generation != baseline.Generation || len(snapshot.Toolchains) != 0 || len(snapshot.Profiles) != 0 {
			t.Fatal("optional failure changed generation/output")
		}
		found := false
		for _, d := range snapshot.Diagnostics {
			if d.Code == "TOOLCHAIN_DISCOVERY_FAILED" {
				found = true
			}
		}
		if !found {
			t.Fatal("Inspector did not preserve best-effort diagnostic")
		}
	}
	if err := os.WriteFile(path, []byte("released"), 0600); err != nil {
		t.Fatal("guard leaked", err)
	}
}

func (c *digestScopeCoordinator) Inspect(ctx context.Context) (discovery.Snapshot, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	defer f.Close()
	result, protected, err := toolhash.Digest(ctx, c.path, f, 1024)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	if !protected || result.Evidence == nil {
		return discovery.Snapshot{}, errors.New("missing current-query scope")
	}
	c.results = append(c.results, result)
	c.contexts = append(c.contexts, ctx)
	if c.afterDigest != nil {
		if err := c.afterDigest(ctx, len(c.results)); err != nil {
			return discovery.Snapshot{}, err
		}
	}
	return discovery.Snapshot{Generation: c.generation}, nil
}

// These fixtures fake stored/discovery metadata only; guarded hashing is real.
func newDigestScopeRuntime(t *testing.T) (*Runtime, *sourceBoundDetailStore, *digestScopeCoordinator, coveragedetail.CurrentIndexQuery) {
	t.Helper()
	dir := t.TempDir()
	root, err := workspace.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	q := coveragedetail.CurrentIndexQuery{ProjectID: "core", ReportID: strings.Repeat("a", 32), WorkspaceGeneration: strings.Repeat("b", 64)}
	runID := strings.Repeat("c", 32)
	s := &sourceBoundDetailStore{report: coveragedomain.Report{ID: q.ReportID, RunID: runID}, run: coveragedomain.Run{Request: coveragedomain.Request{ProjectID: q.ProjectID, WorkspaceGeneration: q.WorkspaceGeneration}}, index: coveragedetail.Index{ProjectID: q.ProjectID, ReportID: q.ReportID, RunID: runID, WorkspaceGeneration: q.WorkspaceGeneration, Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}}}
	for _, name := range []string{"a.c", "other.c"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("int f(void) { return 1; }\n"), 0600); err != nil {
			t.Fatal(err)
		}
		binding, err := coveragenormalize.DigestSource(dir, path, coveragenormalize.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		id, err := coveragedetail.StableFileID(q.ProjectID, binding.URI)
		if err != nil {
			t.Fatal(err)
		}
		s.index.Files = append(s.index.Files, coveragedetail.File{ID: id, RelativePath: binding.URI, SourceSHA256: binding.SHA256, Status: coveragedetail.StatusCurrent})
	}
	path := filepath.Join(t.TempDir(), "cl.exe")
	if err := os.WriteFile(path, []byte("controlled tool bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &digestScopeCoordinator{generation: q.WorkspaceGeneration, path: path}
	return &Runtime{store: s, coordinator: c, workspaceRoot: root, trustedWorkspace: true}, s, c, q
}

func requireDigestScopeFailure(t *testing.T, index coveragedetail.Index, err, want error, c *digestScopeCoordinator) {
	t.Helper()
	if !errors.Is(err, want) || !reflect.DeepEqual(index, coveragedetail.Index{}) {
		t.Fatalf("index=%+v err=%v, want empty index and %v", index, err, want)
	}
	if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil {
		t.Fatal("guard leaked", err)
	}
}

func TestRuntimeToolDigestScopeRetainsBothFreshnessChecks(t *testing.T) {
	for _, at := range []int{1, 2} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			r, _, c, q := newDigestScopeRuntime(t)
			c.afterDigest = func(_ context.Context, call int) error {
				if call == at {
					c.generation = "changed"
				}
				return nil
			}
			index, err := r.ReadCurrentCoverageIndex(context.Background(), q)
			requireDigestScopeFailure(t, index, err, coveragedetail.ErrStale, c)
			if len(c.results) != at {
				t.Fatalf("inspections=%d want %d", len(c.results), at)
			}
		})
	}
}

func TestRuntimeToolDigestScopeSourceDrift(t *testing.T) {
	r, _, c, q := newDigestScopeRuntime(t)
	c.afterDigest = func(_ context.Context, _ int) error {
		if err := os.WriteFile(c.path, []byte("must stay protected"), 0600); err == nil {
			return errors.New("tool was not protected")
		}
		return os.WriteFile(filepath.Join(r.workspaceRoot.NativePath, "other.c"), []byte("changed indexed source"), 0600)
	}
	index, err := r.ReadCurrentCoverageIndex(context.Background(), q)
	requireDigestScopeFailure(t, index, err, coveragedetail.ErrStale, c)
	if len(c.results) != 1 {
		t.Fatal("source drift did not reject before second inspection")
	}
}

func TestRuntimeToolDigestScopeEarlyBindingRejectsBeforeScope(t *testing.T) {
	for _, name := range []string{"invalid-query", "nil-context", "untrusted", "run-project", "run-generation"} {
		t.Run(name, func(t *testing.T) {
			r, s, c, q := newDigestScopeRuntime(t)
			ctx := context.Background()
			want := task.ErrInvalidArgument
			switch name {
			case "invalid-query":
				q.ProjectID = ""
			case "nil-context":
				ctx = nil
			case "untrusted":
				r.trustedWorkspace = false
				want = task.ErrStorageUnavailable
			case "run-project":
				s.run.Request.ProjectID = "other"
			case "run-generation":
				s.run.Request.WorkspaceGeneration = "other"
			}
			calls := 0
			index, err := r.readCurrentCoverageIndex(ctx, q, func(ctx context.Context) (context.Context, coverageToolScope, error) {
				calls++
				return beginCoverageToolScope(ctx)
			})
			requireDigestScopeFailure(t, index, err, want, c)
			if calls != 0 || len(c.results) != 0 {
				t.Fatal("early rejection created a scope or inspected")
			}
		})
	}
}

type observedDigestScope struct {
	*toolhash.Scope
	closeErr, scopeErr error
	closed             int
	stats              toolhash.Stats
}

func (s *observedDigestScope) Close() error {
	s.closed++
	s.stats = s.Stats()
	return errors.Join(s.Scope.Close(), s.closeErr)
}
func (s *observedDigestScope) Err() error { return errors.Join(s.Scope.Err(), s.scopeErr) }
func observeDigestScope(t *testing.T, out **observedDigestScope, closeErr, scopeErr error) coverageToolScopeFactory {
	t.Helper()
	return func(ctx context.Context) (context.Context, coverageToolScope, error) {
		scoped, s, err := toolhash.Begin(ctx)
		if err != nil {
			return scoped, nil, err
		}
		*out = &observedDigestScope{Scope: s, closeErr: closeErr, scopeErr: scopeErr}
		t.Cleanup(func() { _ = s.Close() })
		return scoped, *out, nil
	}
}

func TestRuntimeToolDigestScopeIndexBindingRejectsReportRunProject(t *testing.T) {
	for _, name := range []string{"project", "report", "run", "generation", "current", "nonempty"} {
		t.Run(name, func(t *testing.T) {
			r, s, c, q := newDigestScopeRuntime(t)
			switch name {
			case "project":
				s.index.ProjectID = "other"
			case "report":
				s.index.ReportID = "other"
			case "run":
				s.index.RunID = "other"
			case "generation":
				s.index.WorkspaceGeneration = "other"
			case "current":
				s.index.Project.Status = coveragedetail.StatusStale
			case "nonempty":
				s.index.Files = nil
			}
			var scope *observedDigestScope
			index, err := r.readCurrentCoverageIndex(context.Background(), q, observeDigestScope(t, &scope, nil, nil))
			requireDigestScopeFailure(t, index, err, task.ErrStorageUnavailable, c)
			if scope == nil || scope.closed != 1 || len(c.results) != 1 || scope.stats.Retained != 1 {
				t.Fatal("post-inspection rejection lost scope cleanup")
			}
		})
	}
}

func TestRuntimeToolDigestScopeCancellationAndCleanupError(t *testing.T) {
	for _, name := range []string{"cancel", "close-error", "scope-error", "real-scope-error", "close-preserves-stale"} {
		t.Run(name, func(t *testing.T) {
			r, _, c, q := newDigestScopeRuntime(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("injected cleanup or scope failure")
			var closeErr, scopeErr error
			want := task.ErrStorageUnavailable
			switch name {
			case "cancel":
				want = context.Canceled
				c.afterDigest = func(_ context.Context, call int) error {
					if call == 2 {
						cancel()
					}
					return nil
				}
			case "close-error":
				closeErr = sentinel
			case "scope-error":
				scopeErr = sentinel
			case "real-scope-error":
				c.afterDigest = func(ctx context.Context, call int) error {
					if call != 2 {
						return nil
					}
					f, err := os.Open(c.path)
					if err != nil {
						return err
					}
					defer f.Close()
					_, _, digestErr := toolhash.Digest(ctx, c.path, f, 1)
					if digestErr == nil {
						return errors.New("bound limit failure was ignored")
					}
					// Simulate best-effort discovery swallowing the error; runtime
					// must still refuse the index through the real scope failure.
					return nil
				}
			case "close-preserves-stale":
				closeErr = sentinel
				want = coveragedetail.ErrStale
				c.generation = "changed"
			}
			var scope *observedDigestScope
			index, err := r.readCurrentCoverageIndex(ctx, q, observeDigestScope(t, &scope, closeErr, scopeErr))
			requireDigestScopeFailure(t, index, err, want, c)
			if scope == nil || scope.closed != 1 {
				t.Fatal("scope not closed exactly once")
			}
			if name != "close-preserves-stale" && (scope.stats.FullHashCalls != 1 || scope.stats.HashedBytes != 21 || scope.stats.Reuses != 1) {
				t.Fatalf("scope stats=%+v", scope.stats)
			}
		})
	}
}

func TestRuntimeToolDigestScopeFactoryFailure(t *testing.T) {
	for _, name := range []string{"nil-factory", "error", "nil-context", "scope-and-error", "cancelled-parent"} {
		t.Run(name, func(t *testing.T) {
			r, _, c, q := newDigestScopeRuntime(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var scope *observedDigestScope
			want := task.ErrStorageUnavailable
			factory := coverageToolScopeFactory(func(ctx context.Context) (context.Context, coverageToolScope, error) {
				return ctx, nil, errors.New("begin failed")
			})
			switch name {
			case "nil-factory":
				factory = nil
			case "nil-context":
				factory = func(ctx context.Context) (context.Context, coverageToolScope, error) {
					_, s, e := observeDigestScope(t, &scope, nil, nil)(ctx)
					return nil, s, e
				}
			case "scope-and-error":
				factory = func(ctx context.Context) (context.Context, coverageToolScope, error) {
					c, s, _ := observeDigestScope(t, &scope, nil, nil)(ctx)
					return c, s, errors.New("begin failed after acquisition")
				}
			case "cancelled-parent":
				cancel()
				want = context.Canceled
				factory = beginCoverageToolScope
			}
			index, err := r.readCurrentCoverageIndex(ctx, q, factory)
			requireDigestScopeFailure(t, index, err, want, c)
			if len(c.results) != 0 {
				t.Fatal("factory failure reached discovery")
			}
			if scope != nil && scope.closed != 1 {
				t.Fatal("factory failure leaked scope")
			}
		})
	}
}

func TestRuntimeToolDigestScopeActualEntryAndFreshQuery(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.c")
	if err := os.WriteFile(source, []byte("int f(void) { return 1; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := coveragenormalize.DigestSource(dir, source, coveragenormalize.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	projectID, reportID, generation, runID := "core", strings.Repeat("a", 32), strings.Repeat("b", 64), strings.Repeat("c", 32)
	fileID, err := coveragedetail.StableFileID(projectID, binding.URI)
	if err != nil {
		t.Fatal(err)
	}
	file := coveragedetail.File{ID: fileID, RelativePath: binding.URI, SourceSHA256: binding.SHA256, Status: coveragedetail.StatusCurrent}
	store := &sourceBoundDetailStore{
		report: coveragedomain.Report{ID: reportID, RunID: runID},
		run:    coveragedomain.Run{Request: coveragedomain.Request{ProjectID: projectID, WorkspaceGeneration: generation}},
		index: coveragedetail.Index{ProjectID: projectID, ReportID: reportID, RunID: runID, WorkspaceGeneration: generation,
			Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}, Files: []coveragedetail.File{file}},
	}
	tool := filepath.Join(t.TempDir(), "cl.exe")
	bytes := []byte("controlled tool bytes")
	if err := os.WriteFile(tool, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	coordinator := &digestScopeCoordinator{generation: generation, path: tool}
	r := &Runtime{store: store, coordinator: coordinator, workspaceRoot: root, trustedWorkspace: true}
	q := coveragedetail.CurrentIndexQuery{ProjectID: projectID, ReportID: reportID, WorkspaceGeneration: generation}
	for call := 0; call < 2; call++ {
		index, err := r.ReadCurrentCoverageIndex(context.Background(), q)
		if err != nil || len(index.Files) != 1 {
			t.Fatalf("query %d: %#v %v", call, index, err)
		}
		if len(coordinator.results) != 2*(call+1) {
			t.Fatal("lost one complete inspection")
		}
		first, last := coordinator.results[2*call], coordinator.results[2*call+1]
		if first.Reused || !last.Reused || first.Evidence != last.Evidence {
			t.Fatal("not one shared query scope")
		}
		if err := os.WriteFile(tool, []byte("changed after return"), 0600); err != nil {
			t.Fatal("guard leaked", err)
		}
		consumer, err := os.Open(tool)
		if err != nil {
			t.Fatal(err)
		}
		_, verifyErr := first.Evidence.Verify(coordinator.contexts[2*call], tool, consumer, 1024)
		if err := consumer.Close(); err != nil {
			t.Fatal(err)
		}
		if verifyErr == nil {
			t.Fatal("returned query retained digest authority")
		}
		if err := os.WriteFile(tool, bytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if coordinator.results[0].Evidence == coordinator.results[2].Evidence {
		t.Fatal("scope survived into next query")
	}
}
