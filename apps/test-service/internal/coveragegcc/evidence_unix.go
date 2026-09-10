//go:build !windows

package coveragegcc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
type evidenceIdentity struct{ dev, ino uint64 }

var evidenceScannedEntryForTest = func() {}
var evidenceHashedChunkForTest = func() {}

func sealEvidence(ctx context.Context, root string, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	state, err := openEvidenceState(ctx, root)
	if err != nil {
		return Manifest{}, err
	}
	fail := func(cause error) (Manifest, error) {
		_ = state.close()
		return Manifest{}, errors.Join(ErrInvalidEvidence, cause)
	}
	notes, data, err := scanEvidence(ctx, state.fd, "", 0)
	if err != nil {
		return fail(err)
	}
	if err := validateEvidenceEntries(notes, data); err != nil || len(notes) == 0 {
		return fail(ErrInvalidEvidence)
	}
	if err := validateEvidenceData(notes, data, outcomes); err != nil {
		return fail(err)
	}
	manifest, err := newEvidenceManifest(state, notes, data, evidenceReasons(outcomes), true)
	if err != nil {
		return fail(err)
	}
	return manifest, nil
}

func prepareEvidence(root string) (*PreparedEvidence, error) {
	return prepareEvidenceWithMode(root, false)
}

func prepareBuildEvidence(root string) (*PreparedEvidence, error) {
	return prepareEvidenceWithMode(root, true)
}

func prepareEvidenceWithMode(root string, allowBuildArtifacts bool) (*PreparedEvidence, error) {
	state, err := openEvidenceState(context.Background(), root)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*PreparedEvidence, error) {
		_ = state.close()
		return nil, errors.Join(ErrInvalidEvidence, cause)
	}
	notes, data, err := scanEvidenceMode(context.Background(), state.fd, "", 0, allowBuildArtifacts)
	if err != nil {
		return fail(err)
	}
	if err := validateEvidenceEntries(notes, data); err != nil || len(notes) == 0 {
		return fail(ErrInvalidEvidence)
	}
	if err := validateEvidenceData(notes, data, []testrun.InvocationOutcome{{Crashed: true}}); err != nil {
		return fail(err)
	}
	noteSnapshots, err := snapshotEvidenceEntries(state.fd, notes)
	if err != nil {
		return fail(err)
	}
	dataSnapshots, err := snapshotEvidenceEntries(state.fd, data)
	if err != nil {
		return fail(err)
	}
	for _, entry := range data {
		if err := state.removeExpected(entry, dataSnapshots[entry.RelativePath]); err != nil {
			return fail(err)
		}
	}
	sealedNotes := cloneEvidenceEntries(notes)
	p := &PreparedEvidence{Notes: cloneEvidenceEntries(notes), state: &evidenceState{}}
	p.state.prepare = func(observed []Entry) error {
		if !sameEvidenceEntries(sealedNotes, observed) || state.verifyEntries(noteSnapshots) != nil {
			return ErrInvalidEvidence
		}
		// Data entries are intentionally created after PrepareEvidence returns;
		// the final seal scan below validates their names, bytes, and identities.
		return nil
	}
	p.state.seal = func(ctx context.Context, observed []Entry, outcomes []testrun.InvocationOutcome) (Manifest, error) {
		if ctx == nil || ctx.Err() != nil {
			return Manifest{}, ErrInvalidEvidence
		}
		if err := p.state.prepare(observed); err != nil {
			return Manifest{}, ErrInvalidEvidence
		}
		nowNotes, nowData, err := scanEvidenceMode(ctx, state.fd, "", 0, allowBuildArtifacts)
		if err != nil {
			return Manifest{}, ErrInvalidEvidence
		}
		if !sameEvidenceEntries(sealedNotes, nowNotes) {
			return Manifest{}, ErrInvalidEvidence
		}
		if err := validateEvidenceEntries(nowNotes, nowData); err != nil {
			return Manifest{}, errors.Join(ErrInvalidEvidence, errors.New("prepared evidence entries invalid"), err)
		}
		if err := validateEvidenceData(nowNotes, nowData, outcomes); err != nil {
			return Manifest{}, errors.Join(ErrInvalidEvidence, errors.New("prepared evidence data invalid"), err)
		}
		return newEvidenceManifest(state, nowNotes, nowData, evidenceReasons(outcomes), true)
	}
	p.state.close = func([]Entry) error { return state.close() }
	return p, nil
}

func sealPreparedEvidence(ctx context.Context, p *PreparedEvidence, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	if p == nil || p.state == nil || p.state.seal == nil {
		return Manifest{}, ErrInvalidEvidence
	}
	return p.state.seal(ctx, p.Notes, outcomes)
}

func newEvidenceManifest(state *unixEvidenceState, notes, data []Entry, reasons []coveragedomain.CompletenessReason, cleanup bool) (Manifest, error) {
	notes, data, reasons = cloneEvidenceEntries(notes), cloneEvidenceEntries(data), cloneEvidenceReasons(reasons)
	noteSnapshots, err := snapshotEvidenceEntries(state.fd, notes)
	if err != nil {
		return Manifest{}, ErrInvalidEvidence
	}
	dataSnapshots, err := snapshotEvidenceEntries(state.fd, data)
	if err != nil {
		return Manifest{}, ErrInvalidEvidence
	}
	result := Manifest{Notes: cloneEvidenceEntries(notes), Data: cloneEvidenceEntries(data), PartialReasons: cloneEvidenceReasons(reasons), state: &evidenceState{}}
	result.state.verify = func(observedNotes, observedData []Entry, observedReasons []coveragedomain.CompletenessReason) error {
		if !sameEvidenceEntries(notes, observedNotes) || !sameEvidenceEntries(data, observedData) || !sameEvidenceReasons(reasons, observedReasons) {
			return ErrInvalidEvidence
		}
		if state.verifyEntries(noteSnapshots) != nil || state.verifyEntries(dataSnapshots) != nil {
			return ErrInvalidEvidence
		}
		return nil
	}
	result.state.close = func([]Entry) error {
		if cleanup {
			for _, entry := range data {
				if err := state.removeExpected(entry, dataSnapshots[entry.RelativePath]); err != nil {
					_ = state.close()
					return ErrInvalidEvidence
				}
			}
		}
		return state.close()
	}
	if err := result.Verify(); err != nil {
		_ = result.Close()
		return Manifest{}, ErrInvalidEvidence
	}
	return result, nil
}

func verifyEvidenceManifest(m Manifest) error {
	if m.state == nil || m.state.verify == nil || !sortedEntries(m.Notes) || !sortedEntries(m.Data) || !validEvidenceReasons(m.PartialReasons) {
		return ErrInvalidEvidence
	}
	if err := m.state.verify(m.Notes, m.Data, m.PartialReasons); err != nil {
		return ErrInvalidEvidence
	}
	return nil
}
func closeEvidenceManifest(m *Manifest) error {
	if m == nil || m.state == nil || m.state.close == nil {
		return nil
	}
	state := m.state
	m.state = nil
	return state.close(m.Data)
}

func openEvidenceState(ctx context.Context, root string) (*unixEvidenceState, error) {
	if ctx == nil || ctx.Err() != nil || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, 0) {
		return nil, ErrInvalidEvidence
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.Join(ErrInvalidEvidence, err)
	}
	state := &unixEvidenceState{root: root, fd: fd}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Ino == 0 || stat.Dev == 0 {
		_ = unix.Close(fd)
		return nil, ErrInvalidEvidence
	}
	state.identity = evidenceIdentity{uint64(stat.Dev), uint64(stat.Ino)}
	return state, nil
}

func scanEvidence(ctx context.Context, fd int, prefix string, depth int) ([]Entry, []Entry, error) {
	return scanEvidenceMode(ctx, fd, prefix, depth, false)
}

func scanEvidenceMode(ctx context.Context, fd int, prefix string, depth int, allowBuildArtifacts bool) ([]Entry, []Entry, error) {
	if ctx == nil || ctx.Err() != nil || depth > maxEvidenceDepth {
		return nil, nil, ErrInvalidEvidence
	}
	// Open a fresh directory description for every scan. Duplicating a
	// directory descriptor shares its read offset, so a second scan would
	// incorrectly observe an empty directory after the first scan consumed it.
	duplicate, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
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
		if ctx.Err() != nil {
			return nil, nil, ErrInvalidEvidence
		}
		if name == "." || name == ".." || strings.ContainsRune(name, 0) {
			return nil, nil, ErrInvalidEvidence
		}
		var st unix.Stat_t
		if unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
			return nil, nil, ErrInvalidEvidence
		}
		relative := name
		if prefix != "" {
			relative = prefix + "/" + name
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			if st.Nlink != 1 {
				return nil, nil, ErrInvalidEvidence
			}
			if allowBuildArtifacts && ignoredBuildEvidence(prefix) && (strings.HasSuffix(name, ".gcno") || strings.HasSuffix(name, ".gcda")) {
				continue
			}
			if !strings.HasSuffix(name, ".gcno") && !strings.HasSuffix(name, ".gcda") {
				if allowBuildArtifacts && allowedBuildArtifact(prefix, name, st.Mode) {
					continue
				}
				if allowBuildArtifacts && os.Getenv("UT_DEBUG_PROCESS_HOST_FAILURES") == "1" {
					return nil, nil, fmt.Errorf("unexpected build evidence file %q", relative)
				}
				return nil, nil, ErrInvalidEvidence
			}
			entry, err := digestEvidenceFile(ctx, fd, name, relative, st.Size)
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
			childNotes, childData, err := scanEvidenceMode(ctx, child, relative, depth+1, allowBuildArtifacts)
			_ = unix.Close(child)
			if err != nil {
				return nil, nil, err
			}
			notes, data = append(notes, childNotes...), append(data, childData...)
		default:
			return nil, nil, ErrInvalidEvidence
		}
		if len(notes)+len(data) > maxEvidenceEntries {
			return nil, nil, ErrInvalidEvidence
		}
		evidenceScannedEntryForTest()
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].RelativePath < notes[j].RelativePath })
	sort.Slice(data, func(i, j int) bool { return data[i].RelativePath < data[j].RelativePath })
	return notes, data, nil
}

func allowedBuildArtifact(prefix, name string, mode uint32) bool {
	if strings.HasPrefix(prefix, ".unit-test-ide/") {
		parts := strings.Split(prefix, "/")
		if len(parts) == 2 && validRunnerIdentity(parts[1]) {
			return name == "manifest.json" || name == "runner.c"
		}
		return false
	}
	if prefix == "" {
		switch name {
		case "CMakeCache.txt", "build.ninja", "rules.ninja", ".ninja_deps", ".ninja_log", ".unit-test-ide.lock", "cmake_install.cmake", "CTestTestfile.cmake", "Makefile", "install_manifest.txt", "coverage-custom-command.stamp":
			return true
		}
		// Static libraries are emitted beside the executable by the fixture's
		// Ninja build and are not executable on Unix.
		if strings.HasSuffix(name, ".a") {
			return true
		}
		// CMake target binaries at the build root are executable. Coverage
		// files are handled by the caller before reaching this branch.
		return mode&0111 != 0
	}
	first := prefix
	if index := strings.IndexByte(first, '/'); index >= 0 {
		first = first[:index]
	}
	switch first {
	case ".cmake", "CMakeFiles", "Testing", "cpputest":
		return true
	default:
		return false
	}
}

func validRunnerIdentity(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func digestEvidenceFile(ctx context.Context, parent int, name, relative string, size int64) (Entry, error) {
	if size < 0 || size > maxEvidenceBytes {
		return Entry{}, ErrInvalidEvidence
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Entry{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	h := sha256.New()
	var n int64
	buffer := make([]byte, 32*1024)
	for {
		if ctx == nil || ctx.Err() != nil {
			return Entry{}, ErrInvalidEvidence
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			n += int64(count)
			if n > maxEvidenceBytes {
				return Entry{}, ErrInvalidEvidence
			}
			if _, err := h.Write(buffer[:count]); err != nil {
				return Entry{}, ErrInvalidEvidence
			}
			evidenceHashedChunkForTest()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Entry{}, ErrInvalidEvidence
		}
	}
	if n != size {
		return Entry{}, ErrInvalidEvidence
	}
	return Entry{RelativePath: relative, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}, nil
}

func validateEvidenceData(notes, data []Entry, outcomes []testrun.InvocationOutcome) error {
	expected := make(map[string]struct{}, len(notes))
	for _, e := range notes {
		expected[strings.TrimSuffix(e.RelativePath, ".gcno")+".gcda"] = struct{}{}
	}
	for _, e := range data {
		if _, ok := expected[e.RelativePath]; !ok {
			return ErrInvalidEvidence
		}
	}
	reasons := evidenceReasons(outcomes)
	for name := range expected {
		if !hasEvidenceEntry(data, name) && len(reasons) == 0 {
			return ErrInvalidEvidence
		}
	}
	return nil
}

func ignoredBuildEvidence(prefix string) bool {
	first := prefix
	if index := strings.IndexByte(first, '/'); index >= 0 {
		first = first[:index]
	}
	return first == "cpputest"
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
		if e.RelativePath == "" || e.SHA256 == "" || e.Size < 0 || (i > 0 && entries[i-1].RelativePath >= e.RelativePath) {
			return false
		}
	}
	return true
}
func validateEvidenceEntries(notes, data []Entry) error {
	if !sortedEntries(notes) || !sortedEntries(data) || len(notes)+len(data) > maxEvidenceEntries {
		return ErrInvalidEvidence
	}
	total := int64(0)
	seen, folded := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range append(append([]Entry(nil), notes...), data...) {
		if _, ok := seen[e.RelativePath]; ok {
			return ErrInvalidEvidence
		}
		seen[e.RelativePath] = struct{}{}
		key := strings.ToLower(e.RelativePath)
		if _, ok := folded[key]; ok {
			return ErrInvalidEvidence
		}
		folded[key] = struct{}{}
		total += e.Size
		if total > maxEvidenceBytes {
			return ErrInvalidEvidence
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
	result := make([]coveragedomain.CompletenessReason, 0, len(order))
	for _, r := range order {
		if _, ok := seen[r]; ok {
			result = append(result, r)
		}
	}
	return result
}
func validEvidenceReasons(values []coveragedomain.CompletenessReason) bool {
	seen := map[coveragedomain.CompletenessReason]struct{}{}
	for _, v := range values {
		if v != coveragedomain.CompletenessReasonTestCrashed && v != coveragedomain.CompletenessReasonTestTimedOut {
			return false
		}
		if _, ok := seen[v]; ok {
			return false
		}
		seen[v] = struct{}{}
	}
	return sameEvidenceReasons(values, evidenceReasonsFromSet(seen))
}
func evidenceReasonsFromSet(seen map[coveragedomain.CompletenessReason]struct{}) []coveragedomain.CompletenessReason {
	result := make([]coveragedomain.CompletenessReason, 0, len(seen))
	for _, v := range []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed, coveragedomain.CompletenessReasonTestTimedOut} {
		if _, ok := seen[v]; ok {
			result = append(result, v)
		}
	}
	return result
}
func cloneEvidenceEntries(values []Entry) []Entry { return append([]Entry(nil), values...) }
func cloneEvidenceReasons(values []coveragedomain.CompletenessReason) []coveragedomain.CompletenessReason {
	return append([]coveragedomain.CompletenessReason(nil), values...)
}
func sameEvidenceEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func sameEvidenceReasons(a, b []coveragedomain.CompletenessReason) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type evidenceSnapshot struct {
	entry    Entry
	identity evidenceIdentity
}

func snapshotEvidenceEntries(root int, entries []Entry) (map[string]evidenceSnapshot, error) {
	result := make(map[string]evidenceSnapshot, len(entries))
	for _, entry := range entries {
		actual, identity, err := inspectEvidenceRelative(root, entry.RelativePath)
		if err != nil || actual != entry {
			return nil, ErrInvalidEvidence
		}
		result[entry.RelativePath] = evidenceSnapshot{actual, identity}
	}
	return result, nil
}
func (state *unixEvidenceState) verifyEntries(entries map[string]evidenceSnapshot) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.fd < 0 || !state.rootMatchesLocked() {
		return ErrInvalidEvidence
	}
	for path, expected := range entries {
		actual, identity, err := inspectEvidenceRelative(state.fd, path)
		if err != nil || actual != expected.entry || identity != expected.identity {
			return ErrInvalidEvidence
		}
	}
	return nil
}
func (state *unixEvidenceState) rootMatchesLocked() bool {
	var held, path unix.Stat_t
	return unix.Fstat(state.fd, &held) == nil && unix.Lstat(state.root, &path) == nil && uint64(held.Dev) == state.identity.dev && uint64(held.Ino) == state.identity.ino && uint64(path.Dev) == state.identity.dev && uint64(path.Ino) == state.identity.ino
}
func inspectEvidenceRelative(root int, relative string) (Entry, evidenceIdentity, error) {
	parent, name, err := openEvidenceParent(root, relative)
	if err != nil {
		return Entry{}, evidenceIdentity{}, err
	}
	defer unix.Close(parent)
	var before unix.Stat_t
	if unix.Fstatat(parent, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 {
		return Entry{}, evidenceIdentity{}, ErrInvalidEvidence
	}
	entry, err := digestEvidenceFile(context.Background(), parent, name, relative, before.Size)
	if err != nil {
		return Entry{}, evidenceIdentity{}, err
	}
	var after unix.Stat_t
	if unix.Fstatat(parent, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || after.Nlink != 1 {
		return Entry{}, evidenceIdentity{}, ErrInvalidEvidence
	}
	return entry, evidenceIdentity{uint64(after.Dev), uint64(after.Ino)}, nil
}
func openEvidenceParent(root int, relative string) (int, string, error) {
	parts := strings.Split(relative, "/")
	if len(parts) == 0 {
		return -1, "", ErrInvalidEvidence
	}
	parent, err := unix.Dup(root)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(parent)
			return -1, "", ErrInvalidEvidence
		}
		next, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		_ = unix.Close(parent)
		if err != nil {
			return -1, "", err
		}
		parent = next
	}
	name := parts[len(parts)-1]
	if name == "" || name == "." || name == ".." {
		_ = unix.Close(parent)
		return -1, "", ErrInvalidEvidence
	}
	return parent, name, nil
}
func (state *unixEvidenceState) removeExpected(entry Entry, expected evidenceSnapshot) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.fd < 0 || !state.rootMatchesLocked() {
		return ErrInvalidEvidence
	}
	actual, identity, err := inspectEvidenceRelative(state.fd, entry.RelativePath)
	if err != nil || actual != entry || identity != expected.identity {
		return ErrInvalidEvidence
	}
	parent, name, err := openEvidenceParent(state.fd, entry.RelativePath)
	if err != nil {
		return ErrInvalidEvidence
	}
	defer unix.Close(parent)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrInvalidEvidence
	}
	holding := ".coverage-cleanup-" + hex.EncodeToString(nonce[:])
	if err := unix.Renameat2(parent, name, parent, holding, unix.RENAME_NOREPLACE); err != nil {
		return ErrInvalidEvidence
	}
	holdingEntry, holdingIdentity, err := inspectEvidenceRelative(parent, holding)
	if err != nil || holdingEntry.SHA256 != entry.SHA256 || holdingEntry.Size != entry.Size || holdingIdentity != expected.identity {
		_ = unix.Renameat2(parent, holding, parent, name, unix.RENAME_NOREPLACE)
		return ErrInvalidEvidence
	}
	if err := unix.Unlinkat(parent, holding, 0); err != nil {
		return ErrInvalidEvidence
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
