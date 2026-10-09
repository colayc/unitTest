// Package toolhash provides invocation-local evidence for protected tool files.
package toolhash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
)

const MaxEntries = 128
const MaxEligibleBytes int64 = 512 * 1024 * 1024

type Scope struct {
	mu           sync.Mutex
	entries      map[string]*entry
	parent, work context.Context
	stop         context.CancelFunc
	closed       bool
	failed       error
	closeOnce    sync.Once
	closeErr     error
	stats        Stats
	ops          scopeOps
	maxEntries   int
	maxBytes     int64
}
type Evidence struct {
	scope *Scope
	entry *entry
}
type Result struct {
	SHA256   string
	Size     int64
	Evidence *Evidence
	Reused   bool
}
type Stats struct {
	FullHashCalls, Reuses, Retained int
	HashedBytes                     int64
}
type identity struct {
	info      os.FileInfo
	windowsID string
}
type entry struct {
	mu            sync.Mutex
	path          string
	guard         *os.File
	guardCloseErr error // entry.mu: retained even after another closer consumed guard
	original      identity
	size          int64
	digest        string
	evidence      *Evidence
	ready         chan struct{}
	finishOnce    sync.Once
	complete      bool
}
type scopeOps struct {
	openGuard   func(string) (*os.File, error)
	checkTriple func(string, *os.File, *os.File) (identity, error)
	closeGuard  func(*os.File) error
	beforeRead  func(context.Context, *os.File, int64) error
}
type scopeKey struct{}

func newScope(ctx context.Context, ops scopeOps, maxEntries int, maxBytes int64) *Scope {
	work, stop := context.WithCancel(ctx)
	return &Scope{entries: make(map[string]*entry), parent: ctx, work: work, stop: stop, ops: ops, maxEntries: maxEntries, maxBytes: maxBytes}
}
func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil tool hash context")
	}
	return ctx.Err()
}

func (s *Scope) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Err observes the external parent, not work: ordinary Close cancels only work.
func (s *Scope) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.failed, s.parent.Err())
}

// liveLocked is the publication/admission check. The caller holds only mu.
func (s *Scope) liveLocked(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s.failed != nil {
		return s.failed
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed {
		return errors.New("tool hash scope closed")
	}
	return nil
}

func (s *Scope) poison(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A worker unwinding internal cancellation after Close cannot manufacture a
	// sticky external failure or change the idempotent Close result.
	if !s.closed && s.failed == nil {
		s.failed = err
		s.stop()
	}
	return err
}

func (e *entry) finish() { e.finishOnce.Do(func() { close(e.ready) }) }

func (s *Scope) closeEntry(e *entry) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.guard == nil {
		return e.guardCloseErr
	}
	err := s.ops.closeGuard(e.guard)
	e.guardCloseErr = err
	e.guard = nil
	return err
}

func (s *Scope) discard(e *entry, err error) error {
	s.poison(err)
	closeErr := s.closeEntry(e)
	s.mu.Lock()
	defer s.mu.Unlock()
	if closeErr != nil {
		s.failed = errors.Join(s.failed, closeErr)
	}
	if s.entries[e.path] == e {
		delete(s.entries, e.path)
		s.stats.Retained--
	}
	e.finish()
	return errors.Join(err, closeErr)
}

// Close closes admission before touching guards. It waits only for the short
// guard-identity critical sections, never for consumer hashing or probes.
func (s *Scope) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.stop()
		entries := make([]*entry, 0, len(s.entries))
		for _, e := range s.entries {
			entries = append(entries, e)
			e.finish()
		}
		s.mu.Unlock()
		var closeErr error
		for _, e := range entries {
			closeErr = errors.Join(closeErr, s.closeEntry(e))
		}
		s.mu.Lock()
		s.entries = make(map[string]*entry)
		s.stats.Retained = 0
		s.failed = errors.Join(s.failed, closeErr)
		s.closeErr = errors.Join(s.failed, s.parent.Err())
		s.mu.Unlock()
	})
	return s.closeErr
}

func hashConsumer(ctx context.Context, f *os.File, maximum int64, observe func(context.Context, *os.File, int64) error) (string, int64, error) {
	h := sha256.New()
	r := io.NewSectionReader(f, 0, maximum+1)
	buf := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		if observe != nil {
			if err := observe(ctx, f, total); err != nil {
				return "", total, err
			}
		}
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		n, err := r.Read(buf)
		total += int64(n)
		if total > maximum {
			return "", total, errors.New("tool exceeds hash limit")
		}
		if e := ctx.Err(); e != nil {
			return "", total, e
		}
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			return hex.EncodeToString(h.Sum(nil)), total, nil
		}
		if err != nil {
			return "", total, errors.New("tool consumer read failed")
		}
		if n == 0 {
			return "", total, io.ErrNoProgress
		}
	}
}
