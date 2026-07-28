package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/probe"
)

const (
	gnuProbeTimeout       = 5 * time.Second
	maxGNUProbeOutput     = 64 * 1024
	maxGNUProbeLines      = 128
	maxGNUCandiates       = 128
	maxGNUInstances       = 128
	maxGNUDiscoveryIssues = 128
	defaultGCCSysrootID   = "gcc-default-sysroot-v1"

	// GCC, Clang and the later clang-cl adapter are expected to be comfortably
	// below 512 MiB. The bound prevents a regular-file candidate from turning
	// discovery into unbounded disk I/O while still hashing every accepted byte.
	maxToolchainExecutableBytes int64 = 512 * 1024 * 1024
	executableDigestChunkBytes        = 64 * 1024
)

var (
	ErrInvalidToolchain   = errors.New("invalid toolchain")
	errExecutableTooLarge = errors.New("toolchain executable exceeds size limit")

	gccVersionPattern   = regexp.MustCompile(`(?i)\b(?:gcc|g\+\+|gnu compiler collection)\b[^\r\n]*?\b([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`)
	clangVersionPattern = regexp.MustCompile(`(?i)\bclang version ([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`)
	versionPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)
	triplePattern       = regexp.MustCompile(`^[A-Za-z0-9_+.]+(?:-[A-Za-z0-9_+.]+)+$`)
)

type gnuAdapter struct {
	runner     probe.Runner
	family     Family
	candidates []Candidate
	hostArch   string
}

type compilerDescriptor struct {
	version  string
	triple   string
	sdk      string
	identity string
}

type executableSnapshot struct {
	path     string
	file     *os.File
	info     os.FileInfo
	digest   string
	identity string
	maximum  int64
}

type toolchainProbeError struct {
	code string
	text string
}

func (probeError *toolchainProbeError) Error() string {
	return probeError.text
}

func (probeError *toolchainProbeError) Unwrap() error {
	return ErrInvalidToolchain
}

type discoveryIssuesError struct {
	issues []Issue
}

func (discoveryError *discoveryIssuesError) Error() string {
	return "toolchain discovery completed with issues"
}

func (discoveryError *discoveryIssuesError) ToolchainIssues() []Issue {
	return append([]Issue(nil), discoveryError.issues...)
}

func newGNUAdapter(
	runner probe.Runner,
	family Family,
	candidates []Candidate,
	hostArchitecture string,
) (*gnuAdapter, error) {
	if nilRunner(runner) {
		return nil, fmt.Errorf("%w: probe runner is nil", ErrInvalidToolchain)
	}
	if family != FamilyGCC && family != FamilyClang {
		return nil, fmt.Errorf("%w: unsupported GNU adapter family", ErrInvalidToolchain)
	}
	if hostArchitecture != "x86" && hostArchitecture != "x64" && hostArchitecture != "arm64" {
		return nil, fmt.Errorf("%w: unsupported host architecture", ErrInvalidToolchain)
	}
	if len(candidates) > maxGNUCandiates {
		return nil, fmt.Errorf("%w: too many candidates", ErrInvalidToolchain)
	}
	owned := append([]Candidate(nil), candidates...)
	sort.SliceStable(owned, func(left, right int) bool {
		if owned[left].Manual != owned[right].Manual {
			return owned[left].Manual
		}
		return lessStrings(
			[]string{
				owned[left].ID,
				identityPath(owned[left].CCompiler),
				identityPath(owned[left].CXXCompiler),
			},
			[]string{
				owned[right].ID,
				identityPath(owned[right].CCompiler),
				identityPath(owned[right].CXXCompiler),
			},
		)
	})
	return &gnuAdapter{
		runner: runner, family: family, candidates: owned, hostArch: hostArchitecture,
	}, nil
}

func (adapter *gnuAdapter) Discover(ctx context.Context) ([]Instance, error) {
	if adapter == nil || ctx == nil {
		return nil, fmt.Errorf("%w: adapter or context is nil", ErrInvalidToolchain)
	}
	instances := make([]Instance, 0, len(adapter.candidates))
	issues := make([]Issue, 0)
	descriptors := make(map[string]struct{})
	for _, candidate := range adapter.candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		instance, err := adapter.Probe(ctx, candidate)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			code := "TOOLCHAIN_PROBE_FAILED"
			var typed *toolchainProbeError
			if errors.As(err, &typed) && typed.code != "" {
				code = typed.code
			}
			appendIssue(&issues, Issue{
				Code:     code,
				Message:  candidateIssueMessage(adapter.family, code),
				Blocking: false,
			})
			if len(issues) >= maxGNUDiscoveryIssues {
				break
			}
			continue
		}
		key := descriptorKey(instance)
		if _, duplicate := descriptors[key]; duplicate {
			continue
		}
		descriptors[key] = struct{}{}
		instances = append(instances, instance)
		if len(instances) >= maxGNUInstances {
			appendIssue(&issues, Issue{
				Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
				Message:  fmt.Sprintf("%s discovery exceeded %d instances", adapter.family, maxGNUInstances),
				Blocking: false,
			})
			break
		}
	}
	sort.Slice(instances, func(left, right int) bool {
		a, b := instances[left], instances[right]
		return lessStrings(
			[]string{
				string(a.Family), a.TargetTriple, a.Version, identityPath(a.CCompiler),
				identityPath(a.Coverage.LLVMProfdata), identityPath(a.Coverage.LLVMCov),
				identityPath(a.Coverage.GCov), a.ID,
			},
			[]string{
				string(b.Family), b.TargetTriple, b.Version, identityPath(b.CCompiler),
				identityPath(b.Coverage.LLVMProfdata), identityPath(b.Coverage.LLVMCov),
				identityPath(b.Coverage.GCov), b.ID,
			},
		)
	})
	if len(issues) != 0 {
		sort.Slice(issues, func(left, right int) bool {
			return lessStrings(
				[]string{issues[left].Code, issues[left].Message},
				[]string{issues[right].Code, issues[right].Message},
			)
		})
		return cloneInstances(instances), &discoveryIssuesError{issues: append([]Issue(nil), issues...)}
	}
	return cloneInstances(instances), nil
}

func (adapter *gnuAdapter) Probe(ctx context.Context, candidate Candidate) (Instance, error) {
	if adapter == nil || ctx == nil || adapter.runner == nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "adapter is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if candidate.Family != adapter.family {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "candidate family does not match adapter")
	}
	if candidate.Manual && !validInstanceID(candidate.ID) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "manual candidate id is invalid")
	}

	cCompiler, err := openExecutableSnapshot(ctx, candidate.CCompiler)
	if err != nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "C compiler executable is invalid")
	}
	defer cCompiler.Close()
	cxxCompiler, err := openExecutableSnapshot(ctx, candidate.CXXCompiler)
	if err != nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "C++ compiler executable is invalid")
	}
	defer cxxCompiler.Close()
	verifyCompilers := func() error {
		if err := cCompiler.Verify(ctx); err != nil {
			if isContextError(err) {
				return err
			}
			return errors.New("C compiler executable changed")
		}
		if err := cxxCompiler.Verify(ctx); err != nil {
			if isContextError(err) {
				return err
			}
			return errors.New("C++ compiler executable changed")
		}
		return nil
	}

	cDescriptor, err := adapter.probeCompiler(ctx, cCompiler.path, verifyCompilers)
	if err != nil {
		return Instance{}, err
	}
	cxxDescriptor, err := adapter.probeCompiler(ctx, cxxCompiler.path, verifyCompilers)
	if err != nil {
		return Instance{}, err
	}
	if cDescriptor.version != cxxDescriptor.version {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "C and C++ compiler version mismatch")
	}
	if cDescriptor.triple != cxxDescriptor.triple {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "C and C++ compiler target triple mismatch")
	}
	if identityPath(cDescriptor.sdk) != identityPath(cxxDescriptor.sdk) ||
		cDescriptor.identity != cxxDescriptor.identity {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "C and C++ compiler SDK identity mismatch")
	}
	targetArchitecture, err := architectureFromTriple(cDescriptor.triple)
	if err != nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler target architecture is unsupported")
	}

	generators, err := adapter.probeGenerator(ctx, candidate, verifyCompilers)
	if err != nil {
		return Instance{}, err
	}
	if err := verifyCompilers(); err != nil {
		if isContextError(err) {
			return Instance{}, err
		}
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler executable verification failed")
	}
	if cDescriptor.sdk == "" {
		if cDescriptor.identity != defaultGCCSysrootID {
			return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler SDK identity changed")
		}
	} else {
		currentSDK, currentSDKIdentity, err := canonicalDirectoryIdentity(cDescriptor.sdk)
		if err != nil || identityPath(currentSDK) != identityPath(cDescriptor.sdk) ||
			currentSDKIdentity != cDescriptor.identity {
			return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler SDK identity changed")
		}
	}

	instance := Instance{
		Family:             adapter.family,
		CCompiler:          cCompiler.path,
		CXXCompiler:        cxxCompiler.path,
		Version:            cDescriptor.version,
		TargetTriple:       cDescriptor.triple,
		HostArchitecture:   adapter.hostArch,
		TargetArchitecture: targetArchitecture,
		Sysroot:            cDescriptor.sdk,
		Environment:        []string{},
		Generators:         generators,
	}
	if candidate.Manual {
		instance.ID = candidate.ID
	} else {
		instance.ID, err = automaticToolchainID(
			instance,
			cCompiler.identity,
			cxxCompiler.identity,
			cDescriptor.identity,
		)
		if err != nil {
			return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "construct automatic toolchain id")
		}
	}
	return instance, nil
}

func (adapter *gnuAdapter) probeCompiler(
	ctx context.Context,
	executable string,
	verify func() error,
) (compilerDescriptor, error) {
	var versionArgument, tripleArgument, sdkArgument string
	switch adapter.family {
	case FamilyGCC:
		versionArgument = "--version"
		tripleArgument = "-dumpmachine"
		sdkArgument = "--print-sysroot"
	case FamilyClang:
		versionArgument = "--version"
		tripleArgument = "--print-target-triple"
		sdkArgument = "--print-resource-dir"
	default:
		return compilerDescriptor{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "unsupported compiler family")
	}

	versionOutput, err := adapter.runProbe(ctx, executable, versionArgument, verify)
	if err != nil {
		return compilerDescriptor{}, err
	}
	version, err := parseCompilerVersion(adapter.family, versionOutput)
	if err != nil {
		return compilerDescriptor{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler version output is malformed")
	}
	tripleOutput, err := adapter.runProbe(ctx, executable, tripleArgument, verify)
	if err != nil {
		return compilerDescriptor{}, err
	}
	triple, err := parseSingleLine(tripleOutput, 256)
	if err != nil || !triplePattern.MatchString(triple) {
		return compilerDescriptor{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler target triple output is malformed")
	}
	sdkOutput, err := adapter.runProbe(ctx, executable, sdkArgument, verify)
	if err != nil {
		return compilerDescriptor{}, err
	}
	sdkPath, defaultSysroot, err := parseOptionalSingleLine(sdkOutput, 4096)
	if err != nil || adapter.family == FamilyClang && defaultSysroot {
		return compilerDescriptor{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler SDK output is malformed")
	}
	canonicalSDK := ""
	sdkIdentity := defaultGCCSysrootID
	if !defaultSysroot {
		canonicalSDK, sdkIdentity, err = canonicalDirectoryIdentity(sdkPath)
		if err != nil {
			return compilerDescriptor{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "compiler SDK path is invalid")
		}
	}
	return compilerDescriptor{
		version:  version,
		triple:   triple,
		sdk:      canonicalSDK,
		identity: sdkIdentity,
	}, nil
}

func (adapter *gnuAdapter) probeGenerator(
	ctx context.Context,
	candidate Candidate,
	verifyCompilers func() error,
) ([]string, error) {
	if candidate.Ninja != "" {
		if adapter.validBuildTool(ctx, candidate.Ninja, "--version", Family("ninja"), verifyCompilers) {
			return []string{"Ninja"}, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if candidate.Make != "" {
		if adapter.validBuildTool(ctx, candidate.Make, "--version", Family("make"), verifyCompilers) {
			return []string{"Unix Makefiles"}, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return nil, invalidProbe("BUILD_TOOL_NOT_FOUND", "no verified native build tool")
}

func (adapter *gnuAdapter) validBuildTool(
	ctx context.Context,
	path string,
	argument string,
	kind Family,
	verifyCompilers func() error,
) bool {
	snapshot, err := openExecutableSnapshot(ctx, path)
	if err != nil {
		return false
	}
	defer snapshot.Close()
	verify := func() error {
		if err := verifyCompilers(); err != nil {
			return err
		}
		return snapshot.Verify(ctx)
	}
	output, err := adapter.runProbe(ctx, snapshot.path, argument, verify)
	if err != nil {
		return false
	}
	line, err := parseFirstLine(output, 1024)
	if err != nil {
		return false
	}
	switch kind {
	case Family("ninja"):
		return versionPattern.MatchString(line)
	case Family("make"):
		return strings.HasPrefix(line, "GNU Make ") &&
			versionPattern.MatchString(strings.TrimPrefix(line, "GNU Make "))
	default:
		return false
	}
}

func (adapter *gnuAdapter) runProbe(
	ctx context.Context,
	executable string,
	argument string,
	verify func() error,
) ([]byte, error) {
	if err := verify(); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe executable verification failed")
	}
	result, err := adapter.runner.Run(ctx, probe.Spec{
		Executable: executable,
		Args:       []string{argument},
		Env:        []string{},
		Timeout:    gnuProbeTimeout,
		MaxOutput:  maxGNUProbeOutput,
	})
	if verifyErr := verify(); verifyErr != nil {
		if isContextError(verifyErr) {
			return nil, verifyErr
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe executable verification failed")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe runner failed")
	}
	if result.ExitCode != 0 {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe exited unsuccessfully")
	}
	if len(result.Stdout)+len(result.Stderr) > maxGNUProbeOutput ||
		!utf8.Valid(result.Stdout) || !utf8.Valid(result.Stderr) ||
		bytesContainNUL(result.Stdout) || bytesContainNUL(result.Stderr) {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe output is invalid")
	}
	if len(strings.TrimSpace(string(result.Stderr))) != 0 {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe wrote unexpected diagnostics")
	}
	if lineCount(result.Stdout) > maxGNUProbeLines {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "probe output has too many lines")
	}
	return append([]byte(nil), result.Stdout...), nil
}

func openExecutableSnapshot(ctx context.Context, path string) (*executableSnapshot, error) {
	return openExecutableSnapshotWithLimit(ctx, path, maxToolchainExecutableBytes)
}

func openExecutableSnapshotWithLimit(
	ctx context.Context,
	path string,
	maximum int64,
) (*executableSnapshot, error) {
	return openExecutableSnapshotWithLimitAndHook(ctx, path, maximum, nil)
}

func openExecutableSnapshotWithLimitAndHook(
	ctx context.Context,
	path string,
	maximum int64,
	beforeDigest func(),
) (*executableSnapshot, error) {
	if ctx == nil {
		return nil, errors.New("snapshot context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maximum <= 0 {
		return nil, errors.New("invalid executable size limit")
	}
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("executable path must be absolute")
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("inspect executable: %w", err)
	}
	if !pathInfo.Mode().IsRegular() ||
		runtime.GOOS != "windows" && pathInfo.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("path is not a regular executable")
	}
	file, err := os.Open(canonical)
	if err != nil {
		return nil, fmt.Errorf("open executable: %w", err)
	}
	fail := func(err error) (*executableSnapshot, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) {
		return fail(fmt.Errorf("executable identity changed while opening"))
	}
	if info.Size() < 0 || info.Size() > maximum {
		return fail(errExecutableTooLarge)
	}
	if beforeDigest != nil {
		beforeDigest()
	}
	digest, _, err := digestOpenFile(ctx, file, maximum)
	if err != nil {
		return fail(err)
	}
	identityInput := struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}{Path: identityPath(canonical), SHA256: digest}
	encoded, err := json.Marshal(identityInput)
	if err != nil {
		return fail(err)
	}
	identitySum := sha256.Sum256(encoded)
	snapshot := &executableSnapshot{
		path: canonical, file: file, info: info, digest: digest,
		identity: hex.EncodeToString(identitySum[:]), maximum: maximum,
	}
	if err := snapshot.Verify(ctx); err != nil {
		return fail(err)
	}
	return snapshot, nil
}

func (snapshot *executableSnapshot) Close() error {
	if snapshot == nil || snapshot.file == nil {
		return nil
	}
	return snapshot.file.Close()
}

func (snapshot *executableSnapshot) Verify(ctx context.Context) error {
	if snapshot == nil || snapshot.file == nil {
		return errors.New("snapshot is closed")
	}
	if ctx == nil {
		return errors.New("snapshot context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pathInfo, err := os.Stat(snapshot.path)
	if err != nil {
		return err
	}
	handleInfo, err := snapshot.file.Stat()
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() || !handleInfo.Mode().IsRegular() ||
		!os.SameFile(snapshot.info, pathInfo) || !os.SameFile(snapshot.info, handleInfo) {
		return errors.New("path now names a different executable")
	}
	if handleInfo.Size() < 0 || handleInfo.Size() > snapshot.maximum {
		return errExecutableTooLarge
	}
	if handleInfo.Size() != snapshot.info.Size() {
		return errors.New("executable size changed")
	}
	digest, _, err := digestOpenFile(ctx, snapshot.file, snapshot.maximum)
	if err != nil {
		return err
	}
	if digest != snapshot.digest {
		return errors.New("executable content changed")
	}
	return nil
}

func digestOpenFile(ctx context.Context, file *os.File, maximum int64) (string, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	digest, count, err := digestBounded(ctx, file, maximum)
	if err != nil {
		return "", count, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", count, err
	}
	return digest, count, nil
}

func digestBounded(
	ctx context.Context,
	reader io.Reader,
	maximum int64,
) (string, int64, error) {
	if ctx == nil {
		return "", 0, errors.New("digest context is nil")
	}
	if maximum <= 0 {
		return "", 0, errors.New("invalid digest size limit")
	}
	hash := sha256.New()
	limited := io.LimitReader(reader, maximum+1)
	buffer := make([]byte, executableDigestChunkBytes)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		count, readErr := limited.Read(buffer)
		total += int64(count)
		if total > maximum {
			return "", total, errExecutableTooLarge
		}
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", total, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return hex.EncodeToString(hash.Sum(nil)), total, nil
		}
		if readErr != nil {
			return "", total, readErr
		}
		if count == 0 {
			return "", total, io.ErrNoProgress
		}
	}
}

func canonicalDirectoryIdentity(path string) (string, string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", "", errors.New("directory path must be absolute")
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", "", errors.New("path is not a directory")
	}
	osIdentity, err := directoryOSIdentity(info)
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(struct {
		Path       string `json:"path"`
		OSIdentity string `json:"osIdentity"`
	}{Path: identityPath(canonical), OSIdentity: osIdentity})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(encoded)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func canonicalExistingPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if runtime.GOOS != "windows" {
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			return "", fmt.Errorf("resolve symlinks: %w", err)
		}
		cleaned = resolved
	}
	canonical, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	return canonical, nil
}

func parseCompilerVersion(family Family, output []byte) (string, error) {
	line, err := parseFirstLine(output, 4096)
	if err != nil {
		return "", err
	}
	var pattern *regexp.Regexp
	switch family {
	case FamilyGCC:
		pattern = gccVersionPattern
	case FamilyClang:
		pattern = clangVersionPattern
	default:
		return "", errors.New("unsupported compiler family")
	}
	match := pattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return "", errors.New("unrecognized version banner")
	}
	return match[1], nil
}

func parseFirstLine(output []byte, maximum int) (string, error) {
	text := strings.TrimSpace(string(output))
	if text == "" || len(text) > maximum {
		return "", errors.New("empty or oversized output")
	}
	line := text
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("empty first line")
	}
	return line, nil
}

func parseSingleLine(output []byte, maximum int) (string, error) {
	text := strings.TrimSpace(string(output))
	if text == "" || len(text) > maximum || strings.ContainsAny(text, "\r\n") {
		return "", errors.New("expected one bounded non-empty line")
	}
	return text, nil
}

func parseOptionalSingleLine(output []byte, maximum int) (string, bool, error) {
	if len(output) > maximum {
		return "", false, errors.New("output exceeds limit")
	}
	if strings.TrimSpace(string(output)) == "" {
		return "", true, nil
	}
	value, err := parseSingleLine(output, maximum)
	return value, false, err
}

func architectureFromTriple(triple string) (string, error) {
	architecture := strings.ToLower(strings.SplitN(triple, "-", 2)[0])
	switch architecture {
	case "x86_64", "amd64":
		return "x64", nil
	case "i386", "i486", "i586", "i686", "x86":
		return "x86", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported target architecture")
	}
}

func automaticToolchainID(
	instance Instance,
	cIdentity string,
	cxxIdentity string,
	sdkIdentity string,
) (string, error) {
	input := struct {
		Family             Family `json:"family"`
		CIdentity          string `json:"cIdentity"`
		CXXIdentity        string `json:"cxxIdentity"`
		Version            string `json:"version"`
		TargetTriple       string `json:"targetTriple"`
		HostArchitecture   string `json:"hostArchitecture"`
		TargetArchitecture string `json:"targetArchitecture"`
		SDKIdentity        string `json:"sdkIdentity"`
	}{
		Family:             instance.Family,
		CIdentity:          cIdentity,
		CXXIdentity:        cxxIdentity,
		Version:            instance.Version,
		TargetTriple:       instance.TargetTriple,
		HostArchitecture:   instance.HostArchitecture,
		TargetArchitecture: instance.TargetArchitecture,
		SDKIdentity:        sdkIdentity,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return string(instance.Family) + "-" + hex.EncodeToString(sum[:]), nil
}

func candidateIssueMessage(family Family, code string) string {
	if code == "BUILD_TOOL_NOT_FOUND" {
		return fmt.Sprintf("%s candidate has no verified Ninja or Make build tool", family)
	}
	return fmt.Sprintf("%s candidate probe failed", family)
}

func invalidProbe(code, text string) error {
	return &toolchainProbeError{code: code, text: ErrInvalidToolchain.Error() + ": " + text}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func nilRunner(runner probe.Runner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func bytesContainNUL(value []byte) bool {
	for _, character := range value {
		if character == 0 {
			return true
		}
	}
	return false
}

func lineCount(value []byte) int {
	if len(value) == 0 {
		return 0
	}
	return strings.Count(string(value), "\n") + 1
}
