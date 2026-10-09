//go:build windows

package toolhash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolHashScopeReusesProtectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool.exe")
	data := []byte("verified tool bytes")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	consumer, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	ctx, s, err := Begin(context.Background())
	if err != nil || s == nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.Close()
	first, ok, err := Digest(ctx, path, consumer, 1024)
	if err != nil || !ok || first.Evidence == nil || first.Reused {
		t.Fatalf("first: %+v %v %v", first, ok, err)
	}
	sum := sha256.Sum256(data)
	if first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("not complete file hash")
	}
	for i := 0; i < 3; i++ {
		next, ok, err := Digest(ctx, path, consumer, 1024)
		if err != nil || !ok || !next.Reused || next.Evidence != first.Evidence {
			t.Fatalf("reuse: %+v %v", next, err)
		}
		got, err := first.Evidence.Verify(ctx, path, consumer, 1024)
		if err != nil || got != first.SHA256 {
			t.Fatal("verify lost binding", err)
		}
	}
	if got := s.Stats(); got.FullHashCalls != 1 || got.HashedBytes != int64(len(data)) || got.Retained != 1 {
		t.Fatalf("counters: %+v", got)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err == nil {
		t.Fatal("write allowed while guarded")
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("delete allowed while guarded")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal("guard not released", err)
	}
	if _, err := first.Evidence.Verify(ctx, path, consumer, 1024); err == nil {
		t.Fatal("closed evidence reused")
	}
}

func fixture(t *testing.T, data string) (string, *os.File) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return p, f
}
func testScope(t *testing.T, change func(*scopeOps)) (context.Context, *Scope) {
	t.Helper()
	_, base, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ops := base.ops
	_ = base.Close()
	if change != nil {
		change(&ops)
	}
	s := newScope(context.Background(), ops, 2, 8)
	t.Cleanup(func() { _ = s.Close() })
	return context.WithValue(context.Background(), scopeKey{}, s), s
}
func requireDigest(t *testing.T, ctx context.Context, p string, f *os.File) Result {
	t.Helper()
	r, ok, err := Digest(ctx, p, f, 1024)
	if err != nil || !ok || r.Evidence == nil {
		t.Fatalf("digest: %+v %v %v", r, ok, err)
	}
	return r
}
func released(t *testing.T, p string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("guard retained", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type digestOutcome struct {
	r   Result
	ok  bool
	err error
}

func startDigest(t *testing.T, ctx context.Context, p string, f *os.File) <-chan digestOutcome {
	return async(t, func() digestOutcome { r, ok, err := Digest(ctx, p, f, 1024); return digestOutcome{r, ok, err} })
}
func async[T any](t *testing.T, fn func() T) <-chan T {
	t.Helper()
	done := make(chan T, 1)
	joined := make(chan struct{})
	go func() { defer close(joined); done <- fn() }()
	t.Cleanup(func() { receive(t, joined) })
	return done
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("5s test watchdog")
		var zero T
		return zero
	}
}
func gate(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return ch, release
}

// Catches wrong-consumer acceptance both before publication and on reuse.
func TestToolHashOwnerPathAndIdentityDrift(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "bound"}[bound], func(t *testing.T) {
			p, f := fixture(t, "bytes")
			_, other := fixture(t, "other")
			ctx, s := testScope(t, nil)
			if bound {
				requireDigest(t, ctx, p, f)
			}
			r, ok, err := Digest(ctx, p, other, 1024)
			if err == nil || ok || r.Evidence != nil || s.Err() == nil || s.Close() == nil {
				t.Fatal("identity drift became fallback")
			}
			released(t, p)
		})
	}
	t.Run("scope_binding", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		ctx, _ := testScope(t, nil)
		r := requireDigest(t, ctx, p, f)
		other, _ := testScope(t, nil)
		if _, err := r.Evidence.Verify(other, p, f, 1024); err == nil {
			t.Fatal("evidence transferred scopes")
		}
	})
}

// Catches lexical forbidden aliases without relying on symlink privileges.
func TestToolHashDirectAliasRejected(t *testing.T) {
	t.Run("unsupported_initial_spelling_is_optional", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		for _, spelling := range []string{p + ".", p + " ", `\\?\` + p} {
			ctx, s := testScope(t, nil)
			if r, ok, err := Digest(ctx, spelling, f, 1024); err != nil || ok || r.Evidence != nil || s.Stats().Retained != 0 || s.Err() != nil {
				t.Fatalf("optional spelling became fatal: %+v %v %v", r, ok, err)
			}
		}
	})
	t.Run("unsupported_spelling_cannot_hide_mismatch", func(t *testing.T) {
		p, _ := fixture(t, "bytes")
		_, other := fixture(t, "other")
		ctx, s := testScope(t, nil)
		if _, ok, err := Digest(ctx, p+".", other, 1024); err == nil || ok || s.Close() == nil {
			t.Fatal("unsupported spelling hid real identity mismatch")
		}
	})
	t.Run("actual_symlink", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		link := filepath.Join(t.TempDir(), "alias.exe")
		if err := os.Symlink(p, link); err != nil {
			t.Logf("OPEN INTEGRATION GATE: OS symlink fixture unavailable without additional privilege: %v", err)
			return
		}
		ctx, s := testScope(t, nil)
		if _, ok, err := Digest(ctx, link, f, 1024); err == nil || ok || s.Close() == nil {
			t.Fatal("symlink alias accepted")
		}
	})
	t.Run("forbidden_final_alias", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		ctx, s := testScope(t, nil)
		r := requireDigest(t, ctx, p, f)
		if _, err := r.Evidence.Verify(ctx, p+".", f, 1024); err == nil || s.Err() == nil || s.Close() == nil {
			t.Fatal("bound evidence accepted final dot alias")
		}
	})
	t.Run("real_path_drift_before_publication", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		q, g := fixture(t, "other")
		ctx, s := testScope(t, func(ops *scopeOps) {
			check := ops.checkTriple
			ops.checkTriple = func(path string, consumer, guard *os.File) (identity, error) { return check(q, g, guard) }
		})
		if _, ok, err := Digest(ctx, p, f, 1024); err == nil || ok || s.Close() == nil {
			t.Fatal("real mismatched path accepted")
		}
		released(t, p)
	})
}

// Catches optional budgets becoming input rejection and failed-open leaks.
func TestToolHashOptionalMissAndCapacity(t *testing.T) {
	t.Run("capacity_and_size", func(t *testing.T) {
		ctx, s := testScope(t, nil)
		defer s.Close()
		for _, data := range []string{"one", "two", "three", "ninebytes"} {
			p, f := fixture(t, data)
			r, ok, err := Digest(ctx, p, f, 0)
			want := data == "one" || data == "two"
			if err != nil || ok != want {
				t.Fatalf("%s: %+v %v %v", data, r, ok, err)
			}
			if !want {
				b, err := io.ReadAll(f)
				if err != nil || string(b) != data {
					t.Fatal("fallback handle changed", err)
				}
			}
		}
		if got := s.Stats(); got.FullHashCalls != 2 || got.Retained != 2 {
			t.Fatalf("capacity: %+v", got)
		}
	})
	t.Run("oversize_without_capacity", func(t *testing.T) {
		ctx, s := testScope(t, nil)
		p, f := fixture(t, "ninebytes")
		if _, ok, err := Digest(ctx, p, f, 0); err != nil || ok || s.Stats().Retained != 0 {
			t.Fatal("optional size became rejection", err)
		}
	})
	t.Run("open_failure", func(t *testing.T) {
		ctx, s := testScope(t, func(ops *scopeOps) {
			ops.openGuard = func(string) (*os.File, error) { return nil, errors.New("protection unavailable") }
		})
		p, f := fixture(t, "bytes")
		if _, ok, err := Digest(ctx, p, f, 0); err != nil || ok || s.Stats().Retained != 0 {
			t.Fatal("open miss", err)
		}
		released(t, p)
		_, other := fixture(t, "other")
		if _, _, err := Digest(ctx, p, other, 0); err == nil {
			t.Fatal("mismatch hidden by open failure")
		}
	})
}

// Catches reuse bypassing a stricter caller limit or rejecting maximum zero.
func TestToolHashConsumerLimitRemainsStrict(t *testing.T) {
	ctx, s := testScope(t, nil)
	p, f := fixture(t, "bytes")
	r := requireDigest(t, ctx, p, f)
	if _, ok, err := Digest(ctx, p, f, 0); err != nil || !ok {
		t.Fatal("zero limit rejected", err)
	}
	if _, err := r.Evidence.Verify(ctx, p, f, 1); err == nil {
		t.Fatal("strict limit bypassed")
	}
	if s.Close() == nil {
		t.Fatal("strict limit failure not sticky")
	}
}

// Catches publishing after cancellation and hashing the guard instead of consumer.
func TestToolHashCancelledAndHashFailure(t *testing.T) {
	t.Run("cancelled_child_is_sticky", func(t *testing.T) {
		p, f := fixture(t, "bytes")
		ctx, s := testScope(t, nil)
		child, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := Digest(child, p, f, 1024); !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
		if !errors.Is(s.Err(), context.Canceled) || !errors.Is(s.Close(), context.Canceled) {
			t.Fatal("swallowed cancellation did not poison scope")
		}
	})
	for _, mode := range []string{"cancel", "closed_consumer"} {
		t.Run(mode, func(t *testing.T) {
			p, f := fixture(t, "bytes")
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			ctx, s := testScope(t, func(ops *scopeOps) {
				ops.beforeRead = func(context.Context, *os.File, int64) error {
					once.Do(func() {
						if mode == "cancel" {
							cancel()
						} else {
							_ = f.Close()
						}
					})
					return nil
				}
			})
			ctx = context.WithValue(parent, scopeKey{}, s)
			r, ok, err := Digest(ctx, p, f, 1024)
			if err == nil || ok || r.Evidence != nil || s.Stats().Retained != 0 || s.Close() == nil {
				t.Fatal("failed read published", err)
			}
			released(t, p)
		})
	}
	t.Run("invalid_context", func(t *testing.T) {
		if _, _, err := Begin(nil); err == nil {
			t.Fatal("nil begin accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := Begin(ctx); err == nil {
			t.Fatal("cancelled begin accepted")
		}
		if _, _, err := Digest(nil, "", nil, 0); err == nil {
			t.Fatal("nil digest accepted")
		}
		if _, _, err := Digest(ctx, "", nil, 0); err == nil {
			t.Fatal("cancelled digest accepted")
		}
	})
}

func TestToolHashCanonicalNamesAndFullStream(t *testing.T) {
	data := strings.Repeat("actual streamed bytes", 8192)
	p, f := fixture(t, data)
	ctx, s, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, ok, err := Digest(ctx, strings.ToLower(p), f, 0)
	if err != nil || !ok {
		t.Fatal("filesystem case spelling not normalized", err)
	}
	sum := sha256.Sum256([]byte(data))
	if r.SHA256 != hex.EncodeToString(sum[:]) || r.Size != int64(len(data)) {
		t.Fatal("incomplete stream hash")
	}
	link := filepath.Join(filepath.Dir(p), "hardlink.exe")
	if err := os.Link(p, link); err != nil {
		t.Fatal(err)
	}
	g, err := os.Open(link)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	second, ok, err := Digest(ctx, link, g, 0)
	if err != nil || !ok || second.Reused || second.Evidence == r.Evidence || s.Stats().FullHashCalls != 2 {
		t.Fatal("hard-link names merged", err)
	}
}

func TestToolHashPriorGuardCloseFailure(t *testing.T) {
	p, f := fixture(t, "bytes")
	sentinel := errors.New("actual prior close failed")
	ctx, s := testScope(t, func(ops *scopeOps) {
		ops.closeGuard = func(f *os.File) error { return errors.Join(f.Close(), sentinel) }
	})
	r := requireDigest(t, ctx, p, f)
	// Reproduces the state between discard releasing the entry lock and its
	// scope-lock failure publication: the guard has already been closed.
	if err := s.closeEntry(r.Evidence.entry); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := s.Close(); !errors.Is(err, sentinel) {
		t.Fatal("Close lost a completed guard close failure", err)
	}
	released(t, p)
}

func TestToolHashConcurrentDiscardAndClose(t *testing.T) {
	p, f := fixture(t, "bytes")
	entered := make(chan struct{})
	release, unblock := gate(t)
	defer unblock()
	sentinel := errors.New("discard close sentinel")
	var closes atomic.Int32
	ctx, s := testScope(t, func(ops *scopeOps) {
		ops.beforeRead = func(context.Context, *os.File, int64) error { return f.Close() }
		ops.closeGuard = func(guard *os.File) error {
			closes.Add(1)
			err := guard.Close()
			close(entered)
			<-release
			return errors.Join(err, sentinel)
		}
	})
	done := startDigest(t, ctx, p, f)
	receive(t, entered)
	closed := async(t, s.Close)
	unblock()
	if result := receive(t, done); !errors.Is(result.err, sentinel) || result.ok {
		t.Fatalf("discard: %+v", result)
	}
	if err := receive(t, closed); !errors.Is(err, sentinel) {
		t.Fatal("Close lost concurrent discard failure", err)
	}
	if err := s.Close(); !errors.Is(err, sentinel) || closes.Load() != 1 {
		t.Fatal("close repeated raw cleanup", err)
	}
	released(t, p)
}

// Catches duplicate first hashes and accidental use of shared consumer offsets.
func TestToolHashConcurrentDuplicate(t *testing.T) {
	p, f := fixture(t, "bytes")
	g, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, file := range []*os.File{f, g} {
		if _, err := file.Seek(2, io.SeekStart); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{})
	release, unblock := gate(t)
	defer unblock()
	var once sync.Once
	ctx, s := testScope(t, func(ops *scopeOps) {
		ops.beforeRead = func(context.Context, *os.File, int64) error {
			once.Do(func() { close(entered); <-release })
			return nil
		}
	})
	first := startDigest(t, ctx, p, f)
	select {
	case <-entered:
	case outcome := <-first:
		t.Fatalf("worker did not hash: %+v", outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("5s test watchdog")
	}
	waiting := &waitingContext{Context: ctx, waiting: make(chan struct{})}
	second := startDigest(t, waiting, p, g)
	receive(t, waiting.waiting)
	unblock()
	a, b := receive(t, first), receive(t, second)
	sum := sha256.Sum256([]byte("bytes"))
	if a.err != nil || b.err != nil || !a.ok || !b.ok || a.r.Evidence != b.r.Evidence || a.r.SHA256 != hex.EncodeToString(sum[:]) || a.r.SHA256 != b.r.SHA256 {
		t.Fatalf("duplicates: %+v %+v", a, b)
	}
	if st := s.Stats(); st.FullHashCalls != 1 || st.Retained != 1 || st.HashedBytes != 5 {
		t.Fatalf("stats: %+v", st)
	}
	for _, file := range []*os.File{f, g} {
		pos, err := file.Seek(0, io.SeekCurrent)
		if err != nil || pos != 2 {
			t.Fatal("offset changed", pos, err)
		}
	}
}

// Catches holding lifecycle locks across hashing or publishing after Close.
func TestToolHashCloseDuringHash(t *testing.T) {
	p, f := fixture(t, "bytes")
	entered := make(chan struct{})
	release, unblock := gate(t)
	defer unblock()
	var once sync.Once
	ctx, s := testScope(t, func(ops *scopeOps) {
		ops.beforeRead = func(context.Context, *os.File, int64) error {
			once.Do(func() { close(entered); <-release })
			return nil
		}
	})
	done := startDigest(t, ctx, p, f)
	select {
	case <-entered:
	case outcome := <-done:
		t.Fatalf("worker did not hash: %+v", outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("5s test watchdog")
	}
	closed := async(t, s.Close)
	if err := receive(t, closed); err != nil {
		t.Fatal(err)
	}
	released(t, p)
	unblock()
	outcome := receive(t, done)
	if outcome.err == nil || outcome.ok || outcome.r.Evidence != nil || s.Stats().Retained != 0 {
		t.Fatalf("closed worker published: %+v", outcome)
	}
	if s.Err() != nil {
		t.Fatal("internal cancel fabricated external failure", s.Err())
	}
}

// Catches raw handle close racing short identity calls and lost close failures.
func TestToolHashCloseIdentityAndError(t *testing.T) {
	p, f := fixture(t, "bytes")
	entered := make(chan struct{})
	release, unblock := gate(t)
	defer unblock()
	var checking atomic.Bool
	var rawCloses atomic.Int32
	var armed atomic.Bool
	sentinel := errors.New("close sentinel")
	ctx, s := testScope(t, func(ops *scopeOps) {
		check := ops.checkTriple
		ops.checkTriple = func(path string, consumer, guard *os.File) (identity, error) {
			if armed.Load() {
				checking.Store(true)
				close(entered)
				<-release
				defer checking.Store(false)
			}
			return check(path, consumer, guard)
		}
		ops.closeGuard = func(file *os.File) error {
			if checking.Load() {
				t.Error("raw close overlapped identity")
			}
			rawCloses.Add(1)
			return errors.Join(file.Close(), sentinel)
		}
	})
	r := requireDigest(t, ctx, p, f)
	armed.Store(true)
	verified := async(t, func() error { _, err := r.Evidence.Verify(ctx, p, f, 1024); return err })
	receive(t, entered)
	closed := async(t, s.Close)
	receive(t, s.work.Done())
	if rawCloses.Load() != 0 {
		t.Fatal("close did not wait for short identity")
	}
	unblock()
	receive(t, verified)
	if err := receive(t, closed); !errors.Is(err, sentinel) {
		t.Fatal("lost close failure", err)
	}
	if err := s.Close(); !errors.Is(err, sentinel) || rawCloses.Load() != 1 {
		t.Fatal("close not idempotent", err)
	}
	released(t, p)
}

// Signals entry into the real duplicate-wait select without a timing sleep.
type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

// Catches accidental scope inheritance and cross-invocation cached digests.
func TestToolHashNewScopeRehashesChangedFile(t *testing.T) {
	parent := context.Background()
	ctx, s, err := Begin(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, other, err := Begin(ctx)
	if err != nil || other == s {
		t.Fatal("scope inherited", err)
	}
	defer other.Close()
	path := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(path, []byte("old bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	first, ok, err := Digest(ctx, path, f, 1024)
	if err != nil || !ok {
		t.Fatal("first digest unavailable", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if parent.Err() != nil || ctx.Err() != nil {
		t.Fatal("scope cancelled parent")
	}
	if err := os.WriteFile(path, []byte("new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	nextCtx, nextScope, err := Begin(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer nextScope.Close()
	g, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	next, ok, err := Digest(nextCtx, path, g, 1024)
	if err != nil || !ok {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("new bytes"))
	if next.SHA256 == first.SHA256 || next.SHA256 != hex.EncodeToString(sum[:]) || nextScope.Stats().FullHashCalls != 1 {
		t.Fatal("new scope did not rehash")
	}
}
