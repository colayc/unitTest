//go:build windows

package toolhash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func Begin(ctx context.Context) (context.Context, *Scope, error) {
	if ctx == nil {
		return nil, nil, errors.New("nil tool hash context")
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	s := newScope(ctx, scopeOps{openGuard: openGuard, checkTriple: checkTriple, closeGuard: (*os.File).Close}, MaxEntries, MaxEligibleBytes)
	return context.WithValue(ctx, scopeKey{}, s), s, nil
}

// directConsumer validates independently of optional protection acquisition.
// EvalSymlinks supplies the filesystem's spelling without case folding or
// merging hard-link names. Parent layouts remain the caller's authority.
// A valid but unsupported spelling returns an empty canonical path: only
// first acquisition can treat that as an optional miss, never bound evidence.
func directConsumer(path string, consumer *os.File) (string, os.FileInfo, error) {
	if consumer == nil || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", nil, errors.New("invalid tool file")
	}
	cleaned := filepath.Clean(path)
	info, err := os.Lstat(cleaned)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("tool path is not a direct regular file")
	}
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", nil, errors.New("tool path is a reparse point")
	}
	held, err := consumer.Stat()
	if err != nil || !held.Mode().IsRegular() || !os.SameFile(info, held) || held.Size() != info.Size() {
		return "", nil, errors.New("tool consumer identity changed")
	}
	// The optional optimization does not add namespace/parser support or
	// tighten caller acceptance. Existing full validation owns these spellings.
	base := filepath.Base(cleaned)
	if strings.HasPrefix(cleaned, `\\?\`) || strings.HasPrefix(cleaned, `\\.\`) ||
		strings.Contains(strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned)), ":") ||
		strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
		return "", info, nil
	}
	canonical, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", nil, errors.New("tool canonical path unavailable")
	}
	current, err := os.Lstat(canonical)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) || info.Size() != current.Size() {
		return "", nil, errors.New("tool path identity changed")
	}
	if !os.SameFile(current, held) || held.Size() != current.Size() {
		return "", nil, errors.New("tool consumer identity changed")
	}
	return filepath.Clean(canonical), current, nil
}

func openGuard(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, errors.New("invalid guard handle")
	}
	return f, nil
}

// Caller owns guard and holds entry.mu; no consumer Fd is used.
func checkTriple(path string, consumer, guard *os.File) (identity, error) {
	canonical, info, err := directConsumer(path, consumer)
	if err != nil {
		return identity{}, err
	}
	if canonical != path || guard == nil {
		return identity{}, errors.New("tool canonical path changed")
	}
	held, err := guard.Stat()
	if err != nil || !held.Mode().IsRegular() || !os.SameFile(info, held) || info.Size() != held.Size() {
		return identity{}, errors.New("tool guard identity changed")
	}
	var native windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(guard.Fd()), &native); err != nil {
		return identity{}, errors.New("tool guard identity unavailable")
	}
	if native.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 || native.FileIndexHigh == 0 && native.FileIndexLow == 0 {
		return identity{}, errors.New("tool guard is not a direct identified file")
	}
	return identity{info: info, windowsID: fmt.Sprintf("windows:%08x:%08x%08x", native.VolumeSerialNumber, native.FileIndexHigh, native.FileIndexLow)}, nil
}

func sameIdentity(a, b identity) bool {
	return a.info != nil && b.info != nil && a.windowsID != "" && a.windowsID == b.windowsID && os.SameFile(a.info, b.info) && a.info.Size() == b.info.Size()
}

func Digest(ctx context.Context, path string, consumer *os.File, maximum int64) (Result, bool, error) {
	if err := contextError(ctx); err != nil {
		if ctx != nil {
			if s, _ := ctx.Value(scopeKey{}).(*Scope); s != nil {
				s.poison(err)
			}
		}
		return Result{}, false, err
	}
	s, _ := ctx.Value(scopeKey{}).(*Scope)
	if s == nil {
		return Result{}, false, nil
	}
	if maximum < 0 {
		return Result{}, false, s.poison(errors.New("invalid tool hash limit"))
	}
	canonical, info, err := directConsumer(path, consumer)
	if err != nil {
		return Result{}, false, s.poison(err)
	}
	if maximum > 0 && info.Size() > maximum {
		return Result{}, false, s.poison(errors.New("tool exceeds consumer limit"))
	}
	s.mu.Lock()
	if err := s.liveLocked(ctx); err != nil {
		s.mu.Unlock()
		return Result{}, false, s.poison(err)
	}
	if canonical == "" {
		s.mu.Unlock()
		return Result{}, false, nil
	}
	for _, spelling := range []string{filepath.Clean(path), canonical} {
		if owner := s.spellingOwnerLocked(spelling); owner != nil && owner.path != canonical {
			s.mu.Unlock()
			return Result{}, false, s.poison(errors.New("retained tool spelling changed"))
		}
	}
	if e := s.entries[canonical]; e != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Result{}, false, s.poison(ctx.Err())
		case <-s.work.Done():
			s.mu.Lock()
			err := s.liveLocked(ctx)
			s.mu.Unlock()
			return Result{}, false, err
		case <-e.ready:
		}
		s.mu.Lock()
		err := s.liveLocked(ctx)
		complete := e.complete
		evidence := e.evidence
		tracked := err == nil && complete && s.admitSpellingLocked(e, path)
		s.mu.Unlock()
		if err != nil {
			return Result{}, false, s.poison(err)
		}
		if !complete {
			return Result{}, false, nil
		}
		// Even an untracked optional miss must complete the bound identity and
		// live-guard checks; the spelling budget cannot hide a bound failure.
		digest, err := evidence.verify(ctx, path, consumer, maximum, false)
		if err != nil {
			return Result{}, false, err
		}
		s.mu.Lock()
		if err := s.liveLocked(ctx); err != nil {
			s.mu.Unlock()
			return Result{}, false, s.poison(err)
		}
		if !tracked {
			s.mu.Unlock()
			return Result{}, false, nil
		}
		s.stats.Reuses++
		result := Result{SHA256: digest, Size: e.size, Evidence: evidence, Reused: true}
		s.mu.Unlock()
		return result, true, nil
	}
	if info.Size() > s.maxBytes || len(s.entries) >= s.maxEntries {
		s.mu.Unlock()
		return Result{}, false, nil
	}
	e := &entry{path: canonical, inputPath: filepath.Clean(path), ready: make(chan struct{})}
	s.entries[canonical] = e
	// Open/register under admission serialization, so Close cannot miss a late
	// guard. There is no entry-lock acquisition under the membership lock.
	guard, openErr := s.ops.openGuard(canonical)
	if openErr != nil {
		delete(s.entries, canonical)
		e.finish()
		s.mu.Unlock()
		// A failed protection open is optional only if the path still identifies
		// the consumer. Disappearance/drift is never disguised as a sharing miss.
		current, after, err := directConsumer(path, consumer)
		if err == nil && (current != canonical || !os.SameFile(info, after) || info.Size() != after.Size()) {
			err = errors.New("tool identity changed while acquiring protection")
		}
		if err != nil {
			return Result{}, false, s.poison(err)
		}
		s.mu.Lock()
		err = s.liveLocked(ctx)
		s.mu.Unlock()
		return Result{}, false, s.poison(err)
	}
	e.guard = guard
	s.stats.Retained++
	s.mu.Unlock()
	e.mu.Lock()
	original, err := s.ops.checkTriple(canonical, consumer, e.guard)
	e.mu.Unlock()
	if err != nil {
		return Result{}, false, s.discard(e, err)
	}
	if !os.SameFile(info, original.info) || info.Size() != original.info.Size() {
		return Result{}, false, s.discard(e, errors.New("tool changed before hashing"))
	}
	s.mu.Lock()
	err = s.liveLocked(ctx)
	if err == nil {
		s.stats.FullHashCalls++
	}
	s.mu.Unlock()
	if err != nil {
		return Result{}, false, s.discard(e, err)
	}
	limit := s.maxBytes
	if maximum > 0 && maximum < limit {
		limit = maximum
	}
	work, cancel := context.WithCancel(s.work)
	stopCaller := context.AfterFunc(ctx, cancel)
	if ctx.Err() != nil {
		cancel()
	}
	digest, total, err := hashConsumer(work, consumer, limit, s.ops.beforeRead)
	stopCaller()
	cancel()
	s.mu.Lock()
	s.stats.HashedBytes += total
	s.mu.Unlock()
	if err != nil {
		return Result{}, false, s.discard(e, err)
	}
	e.mu.Lock()
	after, err := s.ops.checkTriple(canonical, consumer, e.guard)
	e.mu.Unlock()
	if err == nil && (!sameIdentity(original, after) || total != original.info.Size()) {
		err = errors.New("tool changed while hashing")
	}
	if err != nil {
		return Result{}, false, s.discard(e, err)
	}
	s.mu.Lock()
	if err = s.liveLocked(ctx); err != nil {
		s.mu.Unlock()
		return Result{}, false, s.discard(e, err)
	}
	e.original = original
	e.size = total
	e.digest = digest
	e.evidence = &Evidence{scope: s, entry: e}
	e.complete = true
	e.finish()
	result := Result{SHA256: digest, Size: total, Evidence: e.evidence}
	s.mu.Unlock()
	return result, true, nil
}

func (e *Evidence) Verify(ctx context.Context, path string, consumer *os.File, maximum int64) (string, error) {
	return e.verify(ctx, path, consumer, maximum, true)
}

func (e *Evidence) verify(ctx context.Context, path string, consumer *os.File, maximum int64, trackSpelling bool) (string, error) {
	if e == nil || e.scope == nil || e.entry == nil {
		return "", errors.New("tool hash evidence unavailable")
	}
	s, item := e.scope, e.entry
	if err := contextError(ctx); err != nil {
		return "", s.poison(err)
	}
	if ctx.Value(scopeKey{}) != s || maximum < 0 {
		return "", s.poison(errors.New("tool evidence binding invalid"))
	}
	canonical, _, err := directConsumer(path, consumer)
	if err != nil {
		return "", s.poison(err)
	}
	s.mu.Lock()
	err = s.liveLocked(ctx)
	if err == nil && (!item.complete || item.evidence != e || canonical != item.path || s.entries[item.path] != item || maximum > 0 && item.size > maximum) {
		err = errors.New("tool evidence binding changed")
	}
	if owner := s.spellingOwnerLocked(filepath.Clean(path)); err == nil && owner != nil && owner != item {
		err = errors.New("retained tool spelling changed")
	}
	if err == nil && trackSpelling && !s.admitSpellingLocked(item, path) {
		err = errors.New("tool evidence spelling budget exhausted")
	}
	original, digest := item.original, item.digest
	s.mu.Unlock()
	if err != nil {
		return "", s.poison(err)
	}
	item.mu.Lock()
	current, err := s.ops.checkTriple(item.path, consumer, item.guard)
	item.mu.Unlock()
	if err == nil && !sameIdentity(original, current) {
		err = errors.New("tool evidence identity changed")
	}
	if err != nil {
		return "", s.poison(err)
	}
	s.mu.Lock()
	err = s.liveLocked(ctx)
	s.mu.Unlock()
	if err != nil {
		return "", s.poison(err)
	}
	return digest, nil
}
