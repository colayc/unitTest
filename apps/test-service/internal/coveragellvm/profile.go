package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
)

const (
	maxProfileBytes = int64(512 * 1024 * 1024)
	maxProfileCount = 250
)

var ErrInvalidProfiles = errors.New("invalid LLVM profile evidence")

type profileAllocator struct {
	mu     sync.Mutex
	root   *instrumentationRootPin
	closed bool
}

func NewProfileAllocator(
	profileRoot string,
) (testrun.ProfileAllocator, error) {
	if !validProfileRootPath(profileRoot) {
		return nil, ErrInvalidProfiles
	}
	root, err := pinInstrumentationRoot(profileRoot)
	if err != nil {
		return nil, errors.Join(ErrInvalidProfiles, err)
	}
	entries, err := os.ReadDir(profileRoot)
	if err != nil || len(entries) != 0 {
		_ = root.file.Close()
		return nil, ErrInvalidProfiles
	}
	return &profileAllocator{root: root}, nil
}

func (allocator *profileAllocator) Decorate(
	expectation testrun.ProfileExpectation,
	spec task.ProcessSpec,
) (task.ProcessSpec, error) {
	if allocator == nil || !validProfileExpectation(expectation) ||
		len(spec.Batch) != 0 {
		return task.ProcessSpec{}, ErrInvalidProfiles
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.closed || allocator.root == nil ||
		verifyInstrumentationRoot(allocator.root) != nil {
		return task.ProcessSpec{}, ErrInvalidProfiles
	}
	result := cloneProfileProcessSpec(spec)
	environment := make([]string, 0, len(result.Env)+1)
	unsafeNames := make([]string, 0)
	for _, entry := range result.Env {
		name, _, found := strings.Cut(entry, "=")
		if !found || !validProfileEnvironmentName(name) ||
			strings.ContainsRune(entry, '\x00') {
			return task.ProcessSpec{}, ErrInvalidProfiles
		}
		if hostileProfileEnvironmentKey(name) {
			unsafeNames = append(unsafeNames, name)
			continue
		}
		environment = append(environment, entry)
	}
	unset, err := sanitizedProfileUnset(
		result.EnvUnset,
		append(unsafeNames, inheritedHostileProfileEnvironmentNames()...),
		false,
	)
	if err != nil || len(environment)+1+len(unset) > 256 {
		return task.ProcessSpec{}, ErrInvalidProfiles
	}
	result.Env = append(
		environment,
		"LLVM_PROFILE_FILE="+filepath.Join(
			allocator.root.path,
			expectation.FileName,
		),
	)
	result.EnvUnset = unset
	return result, nil
}

func (allocator *profileAllocator) Close() error {
	if allocator == nil {
		return nil
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.closed {
		return nil
	}
	allocator.closed = true
	if allocator.root == nil || allocator.root.file == nil {
		return nil
	}
	err := allocator.root.file.Close()
	allocator.root.file = nil
	return err
}

type ManifestEntry struct {
	Expectation testrun.ProfileExpectation
	Path        string
	SHA256      string
	Size        int64
}

type Manifest struct {
	Entries        []ManifestEntry
	PartialReasons []coveragedomain.CompletenessReason
	state          *manifestState
}

type manifestState struct {
	mu             sync.Mutex
	root           string
	rootPin        *instrumentationRootPin
	snapshots      []*sealedProfile
	partialReasons []coveragedomain.CompletenessReason
	closed         bool
}

type sealedProfile struct {
	entry    ManifestEntry
	file     *os.File
	info     os.FileInfo
	identity sealedFileIdentity
}

func SealProfiles(
	profileRoot string,
	expectations []testrun.ProfileExpectation,
	outcomes []testrun.InvocationOutcome,
) (Manifest, error) {
	if !validProfileRootPath(profileRoot) ||
		len(expectations) == 0 ||
		len(expectations) > maxProfileCount ||
		len(outcomes) != len(expectations) {
		return Manifest{}, ErrInvalidProfiles
	}
	expected, err := expectedProfileSet(expectations)
	if err != nil {
		return Manifest{}, err
	}
	outcomeSet, err := profileOutcomeSet(outcomes, expected)
	if err != nil {
		return Manifest{}, err
	}
	rootPin, err := pinInstrumentationRoot(profileRoot)
	if err != nil {
		return Manifest{}, errors.Join(ErrInvalidProfiles, err)
	}
	state := &manifestState{root: profileRoot, rootPin: rootPin}
	fail := func(cause error) (Manifest, error) {
		_ = state.closeLocked()
		return Manifest{}, errors.Join(ErrInvalidProfiles, cause)
	}
	entries, err := os.ReadDir(profileRoot)
	if err != nil {
		return fail(err)
	}
	matched := make(map[string]struct{}, len(expectations))
	for _, directoryEntry := range entries {
		name := directoryEntry.Name()
		key := ""
		for candidateKey, expectation := range expected {
			if matchProfileFile(expectation.FileName, name) {
				if key != "" {
					return fail(errors.New("profile name matches multiple expectations"))
				}
				key = candidateKey
			}
		}
		if key == "" {
			return fail(errors.New("profile root contains an unknown entry"))
		}
		if _, duplicate := matched[key]; duplicate {
			return fail(errors.New("expectation produced multiple profiles"))
		}
		path := filepath.Join(profileRoot, name)
		file, info, identity, err := openSealedProfile(path)
		if err != nil {
			return fail(err)
		}
		if info.Size() <= 0 || info.Size() > maxProfileBytes {
			_ = file.Close()
			return fail(errors.New("profile size is outside the allowed range"))
		}
		digest, err := digestSealedProfile(file, info.Size())
		if err != nil || verifySealedProfile(
			path,
			file,
			info,
			identity,
		) != nil {
			_ = file.Close()
			return fail(errors.New("profile changed while sealing"))
		}
		entry := ManifestEntry{
			Expectation: expected[key],
			Path:        path,
			SHA256:      digest,
			Size:        info.Size(),
		}
		state.snapshots = append(state.snapshots, &sealedProfile{
			entry: entry, file: file, info: info, identity: identity,
		})
		matched[key] = struct{}{}
	}
	if err := verifyInstrumentationRoot(rootPin); err != nil {
		return fail(err)
	}
	partial := make(map[coveragedomain.CompletenessReason]struct{})
	for key := range expected {
		if _, exists := matched[key]; exists {
			continue
		}
		outcome := outcomeSet[key]
		reason, ok := missingProfileReason(outcome)
		if !ok {
			return fail(errors.New("normally exited invocation has no profile"))
		}
		partial[reason] = struct{}{}
	}
	sort.Slice(state.snapshots, func(left, right int) bool {
		return state.snapshots[left].entry.Path <
			state.snapshots[right].entry.Path
	})
	reasons := canonicalProfileReasons(partial)
	state.partialReasons = append(
		[]coveragedomain.CompletenessReason(nil),
		reasons...,
	)
	manifest := Manifest{
		Entries:        make([]ManifestEntry, len(state.snapshots)),
		PartialReasons: append([]coveragedomain.CompletenessReason(nil), reasons...),
		state:          state,
	}
	for index, snapshot := range state.snapshots {
		manifest.Entries[index] = snapshot.entry
	}
	if err := manifest.Verify(); err != nil {
		return fail(err)
	}
	return manifest, nil
}

func (manifest Manifest) Verify() error {
	if manifest.state == nil {
		return ErrInvalidProfiles
	}
	state := manifest.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.rootPin == nil ||
		verifyInstrumentationRoot(state.rootPin) != nil ||
		len(manifest.Entries) != len(state.snapshots) ||
		!reflect.DeepEqual(manifest.PartialReasons, state.partialReasons) {
		return ErrInvalidProfiles
	}
	for index, snapshot := range state.snapshots {
		if snapshot == nil || snapshot.file == nil ||
			!reflect.DeepEqual(manifest.Entries[index], snapshot.entry) ||
			verifySealedProfile(
				snapshot.entry.Path,
				snapshot.file,
				snapshot.info,
				snapshot.identity,
			) != nil {
			return ErrInvalidProfiles
		}
	}
	return nil
}

func (manifest *Manifest) Close() error {
	if manifest == nil || manifest.state == nil {
		return nil
	}
	state := manifest.state
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.closeLocked()
}

func (state *manifestState) closeLocked() error {
	if state == nil || state.closed {
		return nil
	}
	state.closed = true
	var result error
	for _, snapshot := range state.snapshots {
		if snapshot != nil && snapshot.file != nil {
			result = errors.Join(result, snapshot.file.Close())
			snapshot.file = nil
		}
	}
	if state.rootPin != nil && state.rootPin.file != nil {
		result = errors.Join(result, state.rootPin.file.Close())
		state.rootPin.file = nil
	}
	return result
}

func (manifest Manifest) profileRoot() (string, error) {
	if err := manifest.Verify(); err != nil {
		return "", err
	}
	return manifest.state.root, nil
}

func expectedProfileSet(
	values []testrun.ProfileExpectation,
) (map[string]testrun.ProfileExpectation, error) {
	result := make(map[string]testrun.ProfileExpectation, len(values))
	names := make(map[string]struct{}, len(values))
	for _, expectation := range values {
		if !validProfileExpectation(expectation) {
			return nil, ErrInvalidProfiles
		}
		key := profileExpectationKey(
			expectation.InvocationID,
			expectation.Iteration,
		)
		if _, duplicate := result[key]; duplicate {
			return nil, ErrInvalidProfiles
		}
		if _, duplicate := names[expectation.FileName]; duplicate {
			return nil, ErrInvalidProfiles
		}
		result[key] = expectation
		names[expectation.FileName] = struct{}{}
	}
	return result, nil
}

func profileOutcomeSet(
	values []testrun.InvocationOutcome,
	expected map[string]testrun.ProfileExpectation,
) (map[string]testrun.InvocationOutcome, error) {
	result := make(map[string]testrun.InvocationOutcome, len(values))
	for _, outcome := range values {
		key := profileExpectationKey(outcome.InvocationID, outcome.Iteration)
		if _, exists := expected[key]; !exists ||
			outcome.Crashed && outcome.TimedOut {
			return nil, ErrInvalidProfiles
		}
		if _, duplicate := result[key]; duplicate {
			return nil, ErrInvalidProfiles
		}
		result[key] = outcome
	}
	if len(result) != len(expected) {
		return nil, ErrInvalidProfiles
	}
	return result, nil
}

func missingProfileReason(
	outcome testrun.InvocationOutcome,
) (coveragedomain.CompletenessReason, bool) {
	switch {
	case outcome.TimedOut:
		return coveragedomain.CompletenessReasonTestTimedOut, true
	case outcome.Crashed:
		return coveragedomain.CompletenessReasonTestCrashed, true
	case outcome.ExitCode != 0:
		return coveragedomain.CompletenessReasonProfileMissingForFailedInvocation, true
	default:
		return "", false
	}
}

func canonicalProfileReasons(
	values map[coveragedomain.CompletenessReason]struct{},
) []coveragedomain.CompletenessReason {
	order := []coveragedomain.CompletenessReason{
		coveragedomain.CompletenessReasonProfileMissingForFailedInvocation,
		coveragedomain.CompletenessReasonTestCrashed,
		coveragedomain.CompletenessReasonTestTimedOut,
	}
	result := make([]coveragedomain.CompletenessReason, 0, len(values))
	for _, reason := range order {
		if _, exists := values[reason]; exists {
			result = append(result, reason)
		}
	}
	return result
}

func validProfileExpectation(value testrun.ProfileExpectation) bool {
	if value.InvocationID == "" || len(value.InvocationID) > 128 ||
		value.Iteration < 1 || value.Iteration > MaxProfileIteration ||
		len(value.FileName) != len("p-000001-i-000001-%p-%m.profraw") ||
		!strings.HasPrefix(value.FileName, "p-") ||
		value.FileName[8:11] != "-i-" ||
		value.FileName[17:] != "-%p-%m.profraw" ||
		parseSixProfileDigits(value.FileName[11:17]) != value.Iteration ||
		parseSixProfileDigits(value.FileName[2:8]) < 1 {
		return false
	}
	for _, character := range value.InvocationID {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

const MaxProfileIteration int64 = 100

func parseSixProfileDigits(value string) int64 {
	if len(value) != 6 {
		return -1
	}
	var result int64
	for _, character := range value {
		if character < '0' || character > '9' {
			return -1
		}
		result = result*10 + int64(character-'0')
	}
	return result
}

func matchProfileFile(pattern, name string) bool {
	if !validProfileExpectation(testrun.ProfileExpectation{
		InvocationID: "profile", Iteration: parseSixProfileDigits(pattern[11:17]), FileName: pattern,
	}) || len(name) > 255 || !strings.HasPrefix(name, pattern[:17]+"-") ||
		!strings.HasSuffix(name, ".profraw") {
		return false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(name, pattern[:17]+"-"), ".profraw")
	process, module, found := strings.Cut(middle, "-")
	if !found || process == "" || module == "" {
		return false
	}
	for _, character := range process {
		if character < '0' || character > '9' {
			return false
		}
	}
	for _, character := range module {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func profileExpectationKey(invocationID string, iteration int64) string {
	return invocationID + "\x00" + string([]byte{
		byte(iteration >> 56), byte(iteration >> 48),
		byte(iteration >> 40), byte(iteration >> 32),
		byte(iteration >> 24), byte(iteration >> 16),
		byte(iteration >> 8), byte(iteration),
	})
}

func digestSealedProfile(file *os.File, size int64) (string, error) {
	if file == nil || size <= 0 || size > maxProfileBytes {
		return "", ErrInvalidProfiles
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maxProfileBytes+1))
	if err != nil || count != size {
		return "", ErrInvalidProfiles
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validProfileRootPath(value string) bool {
	return value != "" && filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func cloneProfileProcessSpec(value task.ProcessSpec) task.ProcessSpec {
	result := value
	result.Args = append([]string(nil), value.Args...)
	result.Env = append([]string(nil), value.Env...)
	result.EnvUnset = append([]string(nil), value.EnvUnset...)
	result.Batch = append([]task.ProcessBatchItem(nil), value.Batch...)
	return result
}

func sanitizedProfileUnset(
	existing,
	additional []string,
	includeProfileFile bool,
) ([]string, error) {
	fixed := []string{
		"LLVM_PROFILE_MERGE_FILE",
		"GCOV_PREFIX", "GCOV_PREFIX_STRIP",
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP", "PYTHONUSERBASE",
		"PYTHONWARNINGS", "PYTHONBREAKPOINT", "PYTHONINSPECT",
		"PYTHONHASHSEED", "PYTHONUTF8",
		"PIP_CONFIG_FILE", "PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL",
		"PIP_TRUSTED_HOST", "VIRTUAL_ENV", "CONDA_PREFIX", "CONDA_DEFAULT_ENV",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY",
		"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
		"APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
		"REGISTRY_REDIRECT", "REGISTRY_USER",
	}
	if includeProfileFile {
		fixed = append(fixed, "LLVM_PROFILE_FILE")
	}
	values := append(append(append([]string(nil), existing...), additional...), fixed...)
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, name := range values {
		if !validProfileEnvironmentName(name) {
			return nil, ErrInvalidProfiles
		}
		if !includeProfileFile && strings.EqualFold(name, "LLVM_PROFILE_FILE") {
			continue
		}
		key := strings.ToUpper(name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	sort.SliceStable(result, func(left, right int) bool {
		leftUpper := strings.ToUpper(result[left])
		rightUpper := strings.ToUpper(result[right])
		if leftUpper != rightUpper {
			return leftUpper < rightUpper
		}
		return result[left] < result[right]
	})
	if len(result) > 256 {
		return nil, ErrInvalidProfiles
	}
	return result, nil
}

func inheritedHostileProfileEnvironmentNames() []string {
	result := make([]string, 0)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found || !validProfileEnvironmentName(name) ||
			strings.EqualFold(name, "LLVM_PROFILE_FILE") ||
			!hostileProfileEnvironmentKey(name) {
			continue
		}
		result = append(result, name)
	}
	return result
}

func hostileProfileEnvironmentKey(value string) bool {
	upper := strings.ToUpper(value)
	return upper == "LLVM_PROFILE_FILE" ||
		upper == "LLVM_PROFILE_MERGE_FILE" ||
		strings.HasPrefix(upper, "GCOV_") ||
		strings.HasPrefix(upper, "PYTHON") ||
		strings.HasPrefix(upper, "PIP_") ||
		strings.HasPrefix(upper, "CONDA_") ||
		upper == "VIRTUAL_ENV" ||
		strings.HasSuffix(upper, "_PROXY") ||
		upper == "HOME" || upper == "USERPROFILE" ||
		upper == "HOMEDRIVE" || upper == "HOMEPATH" ||
		upper == "APPDATA" || upper == "LOCALAPPDATA" ||
		upper == "PROGRAMDATA" ||
		strings.HasPrefix(upper, "REGISTRY_")
}

func validProfileEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range []byte(value) {
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character == '_' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func profilePathKey(value string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(value)
	}
	return value
}

var _ testrun.ProfileAllocator = (*profileAllocator)(nil)
