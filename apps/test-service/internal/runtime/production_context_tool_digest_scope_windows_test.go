//go:build windows

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolhash"
)

type productionDigestStore struct {
	*sourceBoundDetailStore
	graphErr error
	reads    int
}

func TestProductionContextToolScopeLifetime(t *testing.T) {
	p, _, c, q := newProductionDigestFixture(t)
	deadline := time.Now().Add(30 * time.Second)
	input, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var owners []*observedDigestScope
	factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
		scoped, scope, err := toolhash.Begin(ctx)
		observed := &observedDigestScope{Scope: scope}
		owners = append(owners, observed)
		return scoped, scope, observed, err
	}
	p.afterIndex = func(ctx context.Context) error {
		binding := ctx.Value(productionToolScopeKey{}).(*productionToolScopeBinding)
		if err := binding.scope.ValidateContext(ctx); err != nil {
			return err
		}
		if owners[len(owners)-1].closed != 0 {
			return errors.New("borrow closed parent")
		}
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			return errors.New("deadline changed")
		}
		if os.WriteFile(c.path, []byte("must be locked"), 0600) == nil || os.Remove(c.path) == nil {
			return errors.New("parent released before last discovery")
		}
		return nil
	}
	authority := runtimeProductionResolverAuthority{reader: p}
	for n := 0; n < 2; n++ {
		// The second input deliberately carries the first, now-closed internal marker.
		if n == 1 {
			input = c.contexts[0]
		}
		if _, err := authority.resolveProductionContext(input, q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory); err != nil {
			t.Fatal(err)
		}
		owner := owners[n]
		if owner.closed != 1 || owner.stats.FullHashCalls != 1 || owner.stats.Retained != 1 || owner.Scope.Stats().Retained != 0 {
			t.Fatalf("owner lifecycle: %+v", owner)
		}
		if c.results[5*n].Reused {
			t.Fatal("next resolution inherited old digest")
		}
	}
	if c.results[0].Evidence == c.results[5].Evidence {
		t.Fatal("cross-resolution evidence reuse")
	}
	naked, unrelated, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	before := len(c.results)
	if _, err := p.r.ReadCurrentCoverageIndex(naked, q); err != nil {
		t.Fatal(err)
	}
	if len(c.results) != before+2 || c.results[before].Reused || !c.results[before+1].Reused || unrelated.Stats().Retained != 0 {
		t.Fatal("standalone query borrowed arbitrary input scope")
	}
	if err := os.WriteFile(c.path, []byte("standalone released"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestProductionContextToolScopeBorrowView(t *testing.T) {
	p, _, c, q := newProductionDigestFixture(t)
	ctx, scope, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	ctx = context.WithValue(ctx, productionToolScopeKey{}, &productionToolScopeBinding{scope: scope, query: q})
	if _, err := p.r.InspectWorkspace(ctx); err != nil {
		t.Fatal(err)
	}
	view, present, err := borrowProductionToolScope(ctx)
	if !present || err != nil || view.Err() != nil {
		t.Fatal("live borrow rejected", err)
	}
	if view.Close() != nil || view.Close() != nil || scope.ValidateContext(ctx) != nil {
		t.Fatal("borrow close changed parent")
	}
	if view.Err() == nil {
		t.Fatal("ended view remains usable")
	}
	if _, err := p.r.InspectWorkspace(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.results[1].Reused {
		t.Fatal("parent lost digest after borrow")
	}
	poisoned, _, err := borrowProductionToolScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("sticky after borrow")
	c.results[0].Evidence.Reject(sentinel)
	if !errors.Is(poisoned.Close(), sentinel) || !errors.Is(poisoned.Close(), sentinel) {
		t.Fatal("borrow cleanup swallowed sticky failure")
	}
}

func TestProductionContextToolScopeBorrowValidation(t *testing.T) {
	for _, name := range []string{"project", "generation", "report", "foreign", "closed", "failed", "cancelled", "nil-marker", "wrong-marker-type"} {
		t.Run(name, func(t *testing.T) {
			p, _, c, q := newProductionDigestFixture(t)
			ctx, scope, err := toolhash.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			if _, err := p.r.InspectWorkspace(ctx); err != nil {
				t.Fatal(err)
			}
			binding := &productionToolScopeBinding{scope: scope, query: q}
			ctx = context.WithValue(ctx, productionToolScopeKey{}, binding)
			want := task.ErrStorageUnavailable
			switch name {
			case "project":
				binding.query.ProjectID = "other"
				want = task.ErrInvalidArgument
			case "generation":
				binding.query.WorkspaceGeneration = "other"
				want = task.ErrInvalidArgument
			case "report":
				binding.query.ReportID = "other"
				want = task.ErrInvalidArgument
			case "foreign":
				var other *toolhash.Scope
				ctx, other, err = toolhash.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
			case "closed":
				if err := scope.Close(); err != nil {
					t.Fatal(err)
				}
			case "failed":
				c.results[0].Evidence.Reject(errors.New("sticky bound failure"))
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "nil-marker":
				ctx = context.WithValue(ctx, productionToolScopeKey{}, (*productionToolScopeBinding)(nil))
			case "wrong-marker-type":
				ctx = context.WithValue(ctx, productionToolScopeKey{}, "untrusted value")
			}
			index, got := p.r.ReadCurrentCoverageIndex(ctx, q)
			if !errors.Is(got, want) || !reflect.DeepEqual(index, coveragedetail.Index{}) {
				t.Fatalf("accepted invalid borrow: index=%+v err=%v want=%v", index, got, want)
			}
			if len(c.results) != 1 {
				t.Fatal("invalid borrow opened fresh scope or inspected again")
			}
		})
	}
}

func TestProductionContextToolScopeFreshnessAndCleanup(t *testing.T) {
	names := []string{"first-generation", "index-before-generation", "index-after-generation", "last-generation", "last-project", "last-profile", "last-coverage-profile", "report-binding", "run-binding", "graph", "source", "untrusted", "list", "close", "primary-plus-close", "cancel", "swallowed"}
	for at := 1; at <= 5; at++ {
		names = append(names, fmt.Sprintf("inspect-%d", at))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p, s, c, q := newProductionDigestFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			primary := errors.New("controlled primary error")
			var observed *observedDigestScope
			factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
				scoped, scope, err := toolhash.Begin(ctx)
				observed = &observedDigestScope{Scope: scope}
				if name == "close" || name == "primary-plus-close" {
					observed.closeErr = errors.New("controlled close error")
				}
				return scoped, scope, observed, err
			}
			want := coveragedetail.ErrStale
			switch name {
			case "report-binding":
				s.report.ID = "wrong"
			case "run-binding":
				s.run.ReportID = "wrong"
			case "graph":
				s.graphErr = task.ErrStorageUnavailable
				want = task.ErrStorageUnavailable
			case "source":
				if err := os.WriteFile(filepath.Join(p.r.workspaceRoot.NativePath, "other.c"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "untrusted":
				p.r.trustedWorkspace = false
				want = build.ErrWorkspaceTrustRequired
			case "list", "primary-plus-close":
				p.listErr = primary
				want = primary
			case "close", "swallowed":
				want = task.ErrStorageUnavailable
			case "cancel":
				want = context.Canceled
			}
			c.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
				if name == fmt.Sprintf("inspect-%d", call) {
					return discovery.Snapshot{}, primary
				}
				if name == "first-generation" && call == 1 || name == "index-before-generation" && call == 3 || name == "index-after-generation" && call == 4 || name == "last-generation" && call == 5 {
					snapshot.Generation = "changed"
				}
				if name == "last-project" && call == 5 {
					snapshot.Projects = append(snapshot.Projects[:0:0], snapshot.Projects...)
					snapshot.Projects[0].SourceDir = "changed"
				}
				if name == "last-profile" && call == 5 {
					snapshot.Profiles = append([]cmake.BuildProfile(nil), snapshot.Profiles...)
					snapshot.Profiles[0].BinaryDir = "changed"
				}
				if name == "last-coverage-profile" && call == 5 {
					snapshot.CoverageProfiles = append(snapshot.CoverageProfiles[:0:0], snapshot.CoverageProfiles...)
					snapshot.CoverageProfiles[0].BaseBuildProfileID = "changed"
				}
				if name == "cancel" && call == 5 {
					cancel()
				}
				if name == "swallowed" && call == 5 {
					c.results[0].Evidence.Reject(primary)
				}
				return snapshot, nil
			}
			if len(name) >= 8 && name[:8] == "inspect-" {
				want = primary
			}
			resolved, err := (runtimeProductionResolverAuthority{reader: p}).resolveProductionContext(ctx, q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory)
			if !errors.Is(err, want) || !reflect.DeepEqual(resolved, productionResolverContext{}) {
				t.Fatalf("accepted failure: result=%+v err=%v want=%v", resolved, err, want)
			}
			if observed == nil || observed.closed != 1 || observed.Scope.Stats().Retained != 0 {
				t.Fatal("owner not released exactly once")
			}
			if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil {
				t.Fatal("guard leaked", err)
			}
			if err := os.Remove(c.path); err != nil {
				t.Fatal("delete guard leaked", err)
			}
		})
	}
}

func TestProductionContextToolScopeEntryAndFactory(t *testing.T) {
	for _, name := range []string{"reader", "context", "project", "generation", "report"} {
		t.Run("entry-"+name, func(t *testing.T) {
			p, _, _, q := newProductionDigestFixture(t)
			authority := runtimeProductionResolverAuthority{reader: p}
			ctx := context.Background()
			switch name {
			case "reader":
				authority.reader = nil
			case "context":
				ctx = nil
			case "project":
				q.ProjectID = ""
			case "generation":
				q.WorkspaceGeneration = "invalid"
			case "report":
				q.ReportID = "invalid"
			}
			calls := 0
			factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
				calls++
				return beginProductionToolScope(ctx)
			}
			result, err := authority.resolveProductionContext(ctx, q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory)
			if !errors.Is(err, task.ErrInvalidArgument) || !reflect.DeepEqual(result, productionResolverContext{}) || calls != 0 {
				t.Fatal("invalid entry created owner", err)
			}
		})
	}
	for _, name := range []string{"begin-error", "missing-owner", "missing-context", "panic"} {
		t.Run(name, func(t *testing.T) {
			p, _, c, q := newProductionDigestFixture(t)
			var observed *observedDigestScope
			var scope *toolhash.Scope
			factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
				scoped, s, err := toolhash.Begin(ctx)
				if err != nil {
					return scoped, nil, nil, err
				}
				scope = s
				observed = &observedDigestScope{Scope: s}
				if name != "panic" {
					if _, err := p.r.InspectWorkspace(scoped); err != nil {
						return scoped, s, observed, err
					}
				}
				switch name {
				case "begin-error":
					return scoped, s, observed, errors.New("controlled begin error")
				case "missing-owner":
					return scoped, s, nil, nil
				case "missing-context":
					return nil, s, observed, nil
				}
				return scoped, s, observed, nil
			}
			authority := runtimeProductionResolverAuthority{reader: p}
			if name == "panic" {
				sentinel := errors.New("controlled panic")
				c.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
					if call == 5 {
						panic(sentinel)
					}
					return snapshot, nil
				}
				func() {
					defer func() {
						if recover() != sentinel {
							t.Error("panic was replaced or swallowed")
						}
					}()
					_, _ = authority.resolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory)
				}()
			} else {
				result, err := authority.resolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory)
				if !errors.Is(err, task.ErrStorageUnavailable) || !reflect.DeepEqual(result, productionResolverContext{}) {
					t.Fatal("malformed factory accepted", err)
				}
			}
			if scope == nil || scope.Stats().Retained != 0 {
				t.Fatal("partial owner leaked")
			}
			if name != "missing-owner" && observed.closed != 1 {
				t.Fatal("owner close count changed")
			}
			if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductionContextToolScopeCapacity(t *testing.T) {
	p, _, c, q := newProductionDigestFixture(t)
	dir := t.TempDir()
	paths := make([]string, 130)
	for n := range paths {
		paths[n] = filepath.Join(dir, fmt.Sprintf("tool-%03d.exe", n))
		if err := os.WriteFile(paths[n], []byte("capacity tool"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fallback := 0
	var parent *toolhash.Scope
	c.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
		parent = ctx.Value(productionToolScopeKey{}).(*productionToolScopeBinding).scope
		first, last := 0, 0
		if call == 1 {
			first, last = 0, 65
		}
		if call == 3 {
			first, last = 65, 130
		}
		for _, path := range paths[first:last] {
			f, err := os.Open(path)
			if err != nil {
				return discovery.Snapshot{}, err
			}
			result, protected, err := toolhash.Digest(ctx, path, f, 1024)
			if err == nil && !protected {
				hash := sha256.New()
				_, err = io.Copy(hash, f)
				if err == nil && hex.EncodeToString(hash.Sum(nil)) != fmt.Sprintf("%x", sha256.Sum256([]byte("capacity tool"))) {
					err = errors.New("incorrect fallback hash")
				}
				fallback++
			}
			closeErr := f.Close()
			if err != nil {
				return discovery.Snapshot{}, err
			}
			if closeErr != nil {
				return discovery.Snapshot{}, closeErr
			}
			if protected && result.Evidence == nil {
				return discovery.Snapshot{}, errors.New("missing retained evidence")
			}
		}
		if call == 5 {
			stats := parent.Stats()
			if stats.Retained != 128 || stats.FullHashCalls != 128 || fallback != 3 {
				return discovery.Snapshot{}, fmt.Errorf("shared cap mismatch: %+v fallback=%d", stats, fallback)
			}
		}
		return snapshot, nil
	}
	if _, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID); err != nil {
		t.Fatal(err)
	}
	if parent.Stats().Retained != 0 {
		t.Fatal("capacity guards leaked")
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProductionContextToolScopeConcurrentConsumers(t *testing.T) {
	p, _, c, q := newProductionDigestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.after = func(scoped context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
		if call != 5 {
			return snapshot, nil
		}
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f, err := os.Open(c.path)
				if err != nil {
					failures <- err
					return
				}
				result, protected, err := toolhash.Digest(scoped, c.path, f, 1024)
				closeErr := f.Close()
				if err == nil && (!protected || !result.Reused || result.Evidence != c.results[0].Evidence) {
					err = errors.New("concurrent consumer lost parent evidence")
				}
				failures <- errors.Join(err, closeErr)
			}()
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return discovery.Snapshot{}, errors.New("consumer drain timeout")
		}
		close(failures)
		for err := range failures {
			if err != nil {
				return discovery.Snapshot{}, err
			}
		}
		cancel()
		return snapshot, nil
	}
	result, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(ctx, q.ProjectID, q.WorkspaceGeneration, q.ReportID)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, productionResolverContext{}) {
		t.Fatal("cancellation returned success", err)
	}
	if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestProductionContextToolScopeConcurrentResolutions(t *testing.T) {
	p1, _, c1, q1 := newProductionDigestFixture(t)
	p2, _, c2, q2 := newProductionDigestFixture(t)
	c2.path = c1.path
	input := context.Background()
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	firstDone := make(chan struct{})
	type outcome struct {
		result productionResolverContext
		err    error
	}
	first, second := make(chan outcome, 1), make(chan outcome, 1)
	barrier := func() error {
		arrived <- struct{}{}
		select {
		case <-release:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("resolution barrier timeout")
		}
	}
	c1.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
		if call == 1 {
			if err := barrier(); err != nil {
				return discovery.Snapshot{}, err
			}
		}
		if call == 5 {
			c1.results[0].Evidence.Reject(errors.New("first resolution poisoned"))
		}
		return snapshot, nil
	}
	c2.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
		if call == 1 {
			if err := barrier(); err != nil {
				return discovery.Snapshot{}, err
			}
		}
		if call == 5 {
			select {
			case <-firstDone:
			case <-time.After(2 * time.Second):
				return discovery.Snapshot{}, errors.New("first owner did not drain")
			}
			if os.WriteFile(c2.path, []byte("must remain protected"), 0600) == nil {
				return discovery.Snapshot{}, errors.New("other owner released my guard")
			}
		}
		return snapshot, nil
	}
	go func() {
		r, err := (runtimeProductionResolverAuthority{reader: p1}).ResolveProductionContext(input, q1.ProjectID, q1.WorkspaceGeneration, q1.ReportID)
		first <- outcome{r, err}
		close(firstDone)
	}()
	go func() {
		r, err := (runtimeProductionResolverAuthority{reader: p2}).ResolveProductionContext(input, q2.ProjectID, q2.WorkspaceGeneration, q2.ReportID)
		second <- outcome{r, err}
	}()
	for n := 0; n < 2; n++ {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("owners failed to reach barrier")
		}
	}
	close(release)
	var a, b outcome
	select {
	case a = <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first did not finish")
	}
	select {
	case b = <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("second did not finish")
	}
	if !errors.Is(a.err, task.ErrStorageUnavailable) || !reflect.DeepEqual(a.result, productionResolverContext{}) || b.err != nil {
		t.Fatalf("cross-owner contamination: %v %v", a.err, b.err)
	}
	if c1.results[0].Evidence == c2.results[0].Evidence || c1.results[0].Reused || c2.results[0].Reused {
		t.Fatal("concurrent resolutions shared evidence")
	}
	if err := os.WriteFile(c1.path, []byte("released"), 0600); err != nil {
		t.Fatal(err)
	}
}

func (s *productionDigestStore) ReadValidatedCoverageIndex(ctx context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	s.reads++
	if s.graphErr != nil {
		return coveragedetail.Index{}, s.graphErr
	}
	return s.sourceBoundDetailStore.ReadValidatedCoverageIndex(ctx, q)
}

type productionDigestCoordinator struct {
	*digestScopeCoordinator
	snapshot discovery.Snapshot
	after    func(context.Context, int, discovery.Snapshot) (discovery.Snapshot, error)
}

func (c *productionDigestCoordinator) Inspect(ctx context.Context) (discovery.Snapshot, error) {
	if _, err := c.digestScopeCoordinator.Inspect(ctx); err != nil {
		return discovery.Snapshot{}, err
	}
	snapshot := c.snapshot
	if c.after != nil {
		return c.after(ctx, len(c.results), snapshot)
	}
	return snapshot, nil
}

type productionDigestReader struct {
	r          *Runtime
	targets    []cmake.Target
	listErr    error
	afterIndex func(context.Context) error
}

func (p *productionDigestReader) InspectWorkspace(ctx context.Context) (discovery.Snapshot, error) {
	return p.r.InspectWorkspace(ctx)
}
func (p *productionDigestReader) GetCoverageReport(ctx context.Context, id string) (coveragedomain.Report, error) {
	return p.r.store.GetCoverageReport(ctx, id)
}
func (p *productionDigestReader) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) {
	return p.r.store.GetCoverageRun(ctx, id)
}
func (p *productionDigestReader) ListTargets(ctx context.Context, q build.TargetsRequest) ([]cmake.Target, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	if _, err := p.r.InspectWorkspace(ctx); err != nil {
		return nil, err
	}
	return cmake.CloneTargets(p.targets), nil
}
func (p *productionDigestReader) ReadCurrentCoverageIndex(ctx context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	index, err := p.r.ReadCurrentCoverageIndex(ctx, q)
	if err == nil && p.afterIndex != nil {
		err = p.afterIndex(ctx)
	}
	if err != nil {
		return coveragedetail.Index{}, err
	}
	return index, nil
}
func newProductionDigestFixture(t *testing.T) (*productionDigestReader, *productionDigestStore, *productionDigestCoordinator, coveragedetail.CurrentIndexQuery) {
	t.Helper()
	r, s, c, q := newDigestScopeRuntime(t)
	original := productionRuntimeAuthorityFixture()
	snapshot := original.snapshots[0]
	snapshot.Generation = q.WorkspaceGeneration
	s.run.ID = s.report.RunID
	s.run.ReportID = q.ReportID
	s.run.Outcome = coveragedomain.OutcomeAvailable
	s.run.Request.CoverageProfileID = "coverage"
	s.report.SchemaVersion = coveragedomain.SchemaVersion10
	stored := &productionDigestStore{sourceBoundDetailStore: s}
	coordinator := &productionDigestCoordinator{digestScopeCoordinator: c, snapshot: snapshot}
	r.store, r.coordinator = stored, coordinator
	return &productionDigestReader{r: r, targets: original.targets}, stored, coordinator, q
}
func TestProductionContextToolScopeFiveDiscoveries(t *testing.T) {
	p, s, c, q := newProductionDigestFixture(t)
	resolved, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID)
	if err != nil {
		t.Fatalf("five-site protected resolution failed: %v", err)
	}
	if len(c.results) != 5 || s.reads != 1 || len(resolved.index.Files) != 2 {
		t.Fatalf("discoveries=%d graph-reads=%d", len(c.results), s.reads)
	}
	for n, result := range c.results {
		if result.Reused != (n != 0) || result.Evidence != c.results[0].Evidence {
			t.Fatalf("site %d did not share one retained digest", n+1)
		}
	}
	if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil {
		t.Fatal("owner guard leaked", err)
	}
	f, err := os.Open(c.path)
	if err != nil {
		t.Fatal(err)
	}
	_, verifyErr := c.results[0].Evidence.Verify(c.contexts[0], c.path, f, 1024)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if verifyErr == nil {
		t.Fatal("returned owner left evidence reusable")
	}
	if err := os.Remove(c.path); err != nil {
		t.Fatal("delete guard leaked", err)
	}
}
