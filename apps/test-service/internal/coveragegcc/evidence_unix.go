//go:build !windows

package coveragegcc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/testrun"
)

const maxEvidenceEntries = 4096
const maxEvidenceBytes int64 = 512 * 1024 * 1024
const maxEvidenceDepth = 16

type unixEvidenceState struct {
	mu       sync.Mutex
	root     string
	fd       int
	identity evidenceIdentity
	closed   bool
}

func sealEvidence(ctx context.Context, root string, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	if ctx == nil || ctx.Err() != nil || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, 0) {
		return Manifest{}, ErrInvalidEvidence
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Manifest{}, errors.Join(ErrInvalidEvidence, err)
	}
	state := &unixEvidenceState{root: root, fd: fd}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Ino == 0 || stat.Dev == 0 {
		_ = unix.Close(fd)
		return Manifest{}, ErrInvalidEvidence
	}
	state.identity = evidenceIdentity{dev: uint64(stat.Dev), ino: uint64(stat.Ino)}
	fail := func(err error) (Manifest, error) {
		_ = unix.Close(fd)
		return Manifest{}, errors.Join(ErrInvalidEvidence, err)
	}
	notes, data, err := scanEvidence(ctx, fd, "", 0)
	if err != nil {
		return fail(err)
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].RelativePath < notes[j].RelativePath })
	sort.Slice(data, func(i, j int) bool { return data[i].RelativePath < data[j].RelativePath })
	if err := validateEvidenceEntries(notes, data); err != nil {
		return fail(err)
	}
	if len(notes) == 0 && len(data) > 0 {
		return fail(errors.New("data exists without notes"))
	}
	expected := make(map[string]struct{}, len(notes))
	for _, e := range notes {
		expected[strings.TrimSuffix(e.RelativePath, ".gcno")+".gcda"] = struct{}{}
	}
	for _, e := range data {
		if _, ok := expected[e.RelativePath]; !ok {
			return fail(errors.New("unexpected gcda"))
		}
	}
	reasons := evidenceReasons(outcomes)
	for _, want := range sortedEvidenceKeys(expected) {
		if !hasEvidenceEntry(data, want) {
			if len(reasons) == 0 {
				return fail(errors.New("expected gcda missing"))
			}
		}
	}
	manifest := Manifest{Notes: notes, Data: data, PartialReasons: reasons, state: &evidenceState{}}
	manifest.state.verify = func() error { return state.verify(root, notes, data) }
	manifest.state.close = state.close
	manifest.state.cleanup = state.unlink
	manifest.state.prepare = func(notes []Entry) error { return state.verify(root, notes, nil) }
	manifest.state.root = root
	if err := manifest.Verify(); err != nil {
		_ = manifest.Close()
		return Manifest{}, err
	}
	return manifest, nil
}

func prepareEvidence(root string) (*PreparedEvidence, error) {
	manifest, err := sealEvidence(context.Background(), root, []testrun.InvocationOutcome{{Crashed: true}})
	if err != nil {
		return nil, err
	}
	// Sealing validates the direct object tree. Only stale data derived from a
	// sealed note entry can be removed before test execution.
	for _, entry := range manifest.Data {
		if manifest.state.cleanup == nil || manifest.state.cleanup(entry.RelativePath) != nil {
			_ = manifest.Close()
			return nil, ErrInvalidEvidence
		}
	}
	p := &PreparedEvidence{Notes: append([]Entry(nil), manifest.Notes...), state: manifest.state}
	manifest.state = nil
	p.state.verify = func() error { return p.state.prepare(p.Notes) }
	return p, nil
}
func sealPreparedEvidence(ctx context.Context, p *PreparedEvidence, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	if p == nil || p.state == nil || p.state.verify == nil || p.state.verify() != nil {
		return Manifest{}, ErrInvalidEvidence
	}
	return sealEvidence(ctx, p.state.root, outcomes)
}

func verifyEvidenceManifest(m Manifest) error {
	if m.state == nil || m.state.verify == nil || !sortedEntries(m.Notes) || !sortedEntries(m.Data) {
		return ErrInvalidEvidence
	}
	if err := m.state.verify(); err != nil {
		return ErrInvalidEvidence
	}
	return nil
}
func closeEvidenceManifest(m *Manifest) error {
	if m == nil || m.state == nil || m.state.close == nil {
		return nil
	}
	return m.state.close()
}

func scanEvidence(ctx context.Context, fd int, prefix string, depth int) ([]Entry, []Entry, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if depth > maxEvidenceDepth {
		return nil, nil, errors.New("evidence depth exceeded")
	}
	duplicate, err := unix.Dup(fd)
	if err != nil {
		return nil, nil, err
	}
	read := os.NewFile(uintptr(duplicate), "")
	names, err := read.Readdirnames(-1)
	_ = read.Close()
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(names)
	var notes, data []Entry
	for _, name := range names {
		if name == "." || name == ".." || strings.ContainsRune(name, 0) {
			return nil, nil, errors.New("invalid evidence name")
		}
		var st unix.Stat_t
		if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, nil, err
		}
		relative := name
		if prefix != "" {
			relative = prefix + "/" + name
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			if st.Nlink != 1 {
				return nil, nil, errors.New("linked evidence file")
			}
			if !strings.HasSuffix(name, ".gcno") && !strings.HasSuffix(name, ".gcda") {
				return nil, nil, errors.New("unknown evidence extension")
			}
			entry, err := digestEvidenceFile(fd, name, relative, st.Size)
			if err != nil {
				return nil, nil, err
			}
			if strings.HasSuffix(name, ".gcno") {
				notes = append(notes, entry)
			} else {
				data = append(data, entry)
			}
		case unix.S_IFDIR:
			child, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return nil, nil, err
			}
			childNotes, childData, err := scanEvidence(ctx, child, relative, depth+1)
			_ = unix.Close(child)
			if err != nil {
				return nil, nil, err
			}
			notes = append(notes, childNotes...)
			data = append(data, childData...)
		default:
			return nil, nil, errors.New("special evidence file")
		}
		if len(notes)+len(data) > maxEvidenceEntries {
			return nil, nil, errors.New("evidence count exceeded")
		}
	}
	return notes, data, nil
}
func digestEvidenceFile(parent int, name, relative string, size int64) (Entry, error) {
	if size < 0 || size > maxEvidenceBytes {
		return Entry{}, errors.New("evidence size exceeded")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Entry{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, maxEvidenceBytes+1))
	if err != nil || n != size {
		return Entry{}, errors.New("evidence changed")
	}
	return Entry{RelativePath: relative, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}, nil
}
func sortedEvidenceKeys(values map[string]struct{}) []string {
	r := make([]string, 0, len(values))
	for k := range values {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
func hasEvidenceEntry(entries []Entry, want string) bool {
	for _, e := range entries {
		if e.RelativePath == want {
			return true
		}
	}
	return false
}
func sortedEntries(entries []Entry) bool {
	for i, e := range entries {
		if e.RelativePath == "" || e.SHA256 == "" || e.Size < 0 {
			return false
		}
		if i > 0 && entries[i-1].RelativePath >= e.RelativePath {
			return false
		}
	}
	return true
}
func validateEvidenceEntries(notes, data []Entry) error {
	if !sortedEntries(notes) || !sortedEntries(data) || len(notes)+len(data) > maxEvidenceEntries {
		return errors.New("invalid evidence entries")
	}
	var total int64
	seen := map[string]struct{}{}
	folded := map[string]struct{}{}
	for _, entry := range append(append([]Entry(nil), notes...), data...) {
		if _, ok := seen[entry.RelativePath]; ok {
			return errors.New("duplicate evidence")
		}
		seen[entry.RelativePath] = struct{}{}
		key := strings.ToLower(entry.RelativePath)
		if _, ok := folded[key]; ok {
			return errors.New("case duplicate evidence")
		}
		folded[key] = struct{}{}
		total += entry.Size
		if total > maxEvidenceBytes {
			return errors.New("evidence total too large")
		}
	}
	return nil
}
func evidenceReasons(outcomes []testrun.InvocationOutcome) []coveragedomain.CompletenessReason {
	seen := map[coveragedomain.CompletenessReason]struct{}{}
	for _, o := range outcomes {
		if o.TimedOut {
			seen[coveragedomain.CompletenessReasonTestTimedOut] = struct{}{}
		} else if o.Crashed {
			seen[coveragedomain.CompletenessReasonTestCrashed] = struct{}{}
		}
	}
	order := []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed, coveragedomain.CompletenessReasonTestTimedOut}
	r := make([]coveragedomain.CompletenessReason, 0, len(order))
	for _, v := range order {
		if _, ok := seen[v]; ok {
			r = append(r, v)
		}
	}
	return r
}

func (state *unixEvidenceState) verify(root string, notes, data []Entry) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.fd < 0 {
		return ErrInvalidEvidence
	}
	var held, path unix.Stat_t
	if unix.Fstat(state.fd, &held) != nil || unix.Lstat(root, &path) != nil || uint64(held.Dev) != state.identity.dev || uint64(held.Ino) != state.identity.ino || uint64(path.Dev) != state.identity.dev || uint64(path.Ino) != state.identity.ino {
		return ErrInvalidEvidence
	}
	for _, entry := range append(append([]Entry(nil), notes...), data...) {
		if _, err := digestEvidenceRelative(state.fd, entry.RelativePath); err != nil {
			return ErrInvalidEvidence
		}
	}
	return nil
}
func (state *unixEvidenceState) close() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil
	}
	state.closed = true
	err := unix.Close(state.fd)
	state.fd = -1
	return err
}

func digestEvidenceRelative(root int, relative string) (Entry, error) {
	parts := strings.Split(relative, "/")
	if len(parts) == 0 {
		return Entry{}, ErrInvalidEvidence
	}
	parent, err := unix.Dup(root)
	if err != nil {
		return Entry{}, err
	}
	defer unix.Close(parent)
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return Entry{}, ErrInvalidEvidence
		}
		next, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return Entry{}, err
		}
		_ = unix.Close(parent)
		parent = next
	}
	name := parts[len(parts)-1]
	var st unix.Stat_t
	if name == "" || unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return Entry{}, ErrInvalidEvidence
	}
	return digestEvidenceFile(parent, name, relative, st.Size)
}

func unlinkEvidenceRelative(root int, relative string) error {
	if root < 0 {
		return ErrInvalidEvidence
	}
	parts := strings.Split(relative, "/")
	parent, err := unix.Dup(root)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return ErrInvalidEvidence
		}
		next, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		_ = unix.Close(parent)
		parent = next
	}
	name := parts[len(parts)-1]
	if name == "" {
		return ErrInvalidEvidence
	}
	return unix.Unlinkat(parent, name, 0)
}
func (state *unixEvidenceState) unlink(relative string) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.fd < 0 {
		return ErrInvalidEvidence
	}
	return unlinkEvidenceRelative(state.fd, relative)
}

type evidenceIdentity struct{ dev, ino uint64 }
