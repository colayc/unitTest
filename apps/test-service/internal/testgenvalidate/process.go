package testgenvalidate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
)

var ErrProcessRejected = errors.New("validation process rejected")

const maxCoverageProcessOutput = 32 << 20

// TrustedProcessPlan is resolved from fixed service/build/framework metadata,
// never from ValidationRequest. It may produce a CMake, test or collector plan.
type TrustedProcessPlan func(context.Context, Stage, Roots) (processcontrol.Spec, error)
type StageInterpreter func(Stage, Roots, []byte) (StageEvidence, error)
type LeaseWriter func(context.Context, task.ProcessLease) error

// PreparedProcessExecutor is the sole native-process adapter for validation.
// It delegates process-tree ownership to processcontrol's prepared lease.
type PreparedProcessExecutor struct {
	Runner                    processcontrol.Runner
	Plan                      TrustedProcessPlan
	Interpret                 StageInterpreter
	TaskID, ServiceInstanceID string
	ToolSHA256                map[string]string
	AllowedEnvironment        []string
	AllowedEnvUnset           []string
	RecordLease, ReleaseLease LeaseWriter
}

func (e PreparedProcessExecutor) Execute(ctx context.Context, stage Stage, roots Roots) (evidence StageEvidence, err error) {
	if ctx == nil || e.Runner == nil || e.Plan == nil || e.RecordLease == nil || e.ReleaseLease == nil || !validProcessIdentity(e.TaskID) || !validProcessIdentity(e.ServiceInstanceID) {
		return evidence, ErrProcessRejected
	}
	spec, err := e.Plan(ctx, stage, roots)
	if err != nil || !e.acceptSpec(spec, roots) {
		return evidence, ErrProcessRejected
	}
	spec.ClosedEnvironment = true
	process, err := e.Runner.Prepare(ctx, spec, e.TaskID, e.ServiceInstanceID)
	if err != nil || process == nil {
		return evidence, ErrProcessRejected
	}
	lease := process.Lease()
	closeProcess := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := process.Close(cleanupCtx); closeErr != nil {
			return ErrProcessRejected
		}
		if releaseErr := e.ReleaseLease(cleanupCtx, lease); releaseErr != nil {
			return ErrProcessRejected
		}
		return nil
	}
	stopProcess := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopErr := process.Terminate(cleanupCtx, 100*time.Millisecond); stopErr != nil {
			return ErrProcessRejected
		}
		return nil
	}
	if lease.TaskID != e.TaskID || lease.ServiceInstanceID != e.ServiceInstanceID || lease.HostPID <= 0 || lease.HostStartIdentity == "" {
		_ = stopProcess()
		_ = process.Close(context.Background())
		return evidence, ErrProcessRejected
	}
	if err := e.RecordLease(ctx, lease); err != nil {
		_ = stopProcess()
		_ = process.Close(context.Background())
		return evidence, ErrProcessRejected
	}
	if !e.acceptSpec(spec, roots) {
		_ = stopProcess()
		_ = closeProcess()
		return evidence, ErrProcessRejected
	}
	if err := process.Start(ctx); err != nil {
		_ = stopProcess()
		_ = closeProcess()
		return evidence, ErrProcessRejected
	}
	lease = process.Lease()
	if lease.TaskID != e.TaskID || lease.ServiceInstanceID != e.ServiceInstanceID || e.RecordLease(ctx, lease) != nil {
		_ = stopProcess()
		_ = closeProcess()
		return evidence, ErrProcessRejected
	}
	var output []byte
	outputLimit := maxStageOutput
	if stage == StageCoverage {
		outputLimit = maxCoverageProcessOutput
	}
	gotResult := false
	outputCh, doneCh := process.Output(), process.Done()
	for outputCh != nil || doneCh != nil {
		select {
		case <-ctx.Done():
			_ = stopProcess()
			_ = closeProcess()
			return evidence, ErrProcessRejected
		case chunk, ok := <-outputCh:
			if !ok {
				outputCh = nil
				continue
			}
			if len(chunk.Data) > outputLimit-len(output) {
				_ = stopProcess()
				_ = closeProcess()
				return evidence, ErrProcessRejected
			}
			output = append(output, chunk.Data...)
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			gotResult = true
			doneCh = nil
			if result.Err != nil || result.ExitCode != 0 || len(result.Children) != 0 {
				_ = stopProcess()
				_ = closeProcess()
				return evidence, ErrProcessRejected
			}
		}
	}
	if !gotResult {
		_ = stopProcess()
		_ = closeProcess()
		return evidence, ErrProcessRejected
	}
	if err := closeProcess(); err != nil {
		return evidence, err
	}
	evidence.Output = output
	if e.Interpret != nil {
		interpreted, err := e.Interpret(stage, roots, output)
		if err != nil {
			return StageEvidence{}, ErrProcessRejected
		}
		if len(interpreted.Output) > maxStageOutput || interpreted.ExitCode != 0 {
			return StageEvidence{}, ErrProcessRejected
		}
		if interpreted.Output != nil {
			evidence.Output = append([]byte(nil), interpreted.Output...)
		}
		evidence.DiscoveredCaseIDs = interpreted.DiscoveredCaseIDs
		evidence.CoverageJSON = interpreted.CoverageJSON
		if len(interpreted.CoverageDetailJSON) > 16<<20 {
			return StageEvidence{}, ErrProcessRejected
		}
		evidence.CoverageDetailJSON = append([]byte(nil), interpreted.CoverageDetailJSON...)
	}
	return evidence, nil
}

func validProcessIdentity(value string) bool {
	return validDigest(value) || validObjectID(value)
}

func (e PreparedProcessExecutor) acceptSpec(spec processcontrol.Spec, roots Roots) bool {
	if spec.Executable == "" || len(spec.Args) > 256 || len(spec.Batch) != 0 || len(spec.LaunchPlan) > 64 || len(spec.LaunchInputs) > 128 || !pathWithinRoots(spec.Dir, roots) || !directDirectory(spec.Dir) || !scanStageRoots(roots) {
		return false
	}
	if !verifiedPinnedTool(spec.Executable, e.ToolSHA256) {
		return false
	}
	seenLaunch := make(map[string]struct{}, len(spec.LaunchPlan))
	for _, path := range spec.LaunchPlan {
		if _, duplicate := seenLaunch[path]; duplicate || !verifiedPinnedTool(path, e.ToolSHA256) {
			return false
		}
		seenLaunch[path] = struct{}{}
	}
	seenInputs := make(map[string]struct{}, len(spec.LaunchInputs))
	for _, state := range spec.LaunchInputs {
		if _, duplicate := seenInputs[state.Path]; duplicate || !verifiedPinnedTool(state.Path, e.ToolSHA256) ||
			state.SHA256 != e.ToolSHA256[state.Path] || cmake.VerifyLaunchInput(state, 256<<20) != nil {
			return false
		}
		seenInputs[state.Path] = struct{}{}
	}
	for _, arg := range spec.Args {
		if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
			return false
		}
	}
	if e.AllowedEnvironment != nil || e.AllowedEnvUnset != nil {
		if !exactTrustedEnvironment(spec.Env, spec.EnvUnset, e.AllowedEnvironment, e.AllowedEnvUnset) {
			return false
		}
	} else {
		for _, entry := range spec.Env {
			key, value, ok := strings.Cut(entry, "=")
			if !ok || (key != "LLVM_PROFILE_FILE" && key != "TEMP" && key != "TMP") || !pathWithinRoots(value, roots) || !within(roots.Artifacts, value) || !safeEnvironmentDestination(key, value) {
				return false
			}
		}
		for _, key := range spec.EnvUnset {
			if key != "LLVM_PROFILE_FILE" && key != "GCOV_PREFIX" && key != "GCOV_PREFIX_STRIP" {
				return false
			}
		}
	}
	return true
}

func exactTrustedEnvironment(actual, actualUnset, allowed, allowedUnset []string) bool {
	actualValues, ok := canonicalEnvironmentSnapshot(actual, actualUnset)
	if !ok {
		return false
	}
	allowedValues, ok := canonicalEnvironmentSnapshot(allowed, allowedUnset)
	if !ok || len(actualValues) != len(allowedValues) {
		return false
	}
	for key, value := range allowedValues {
		if actualValues[key] != value {
			return false
		}
	}
	return true
}

func canonicalEnvironmentSnapshot(environment, unset []string) (map[string]string, bool) {
	if len(environment)+len(unset) > 256 {
		return nil, false
	}
	result := make(map[string]string, len(environment)+len(unset))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		canonical := strings.ToUpper(key)
		if !ok || !validEnvironmentKey(key) || len(entry) > 32767 || strings.ContainsRune(entry, '\x00') || forbiddenValidationEnvironment(canonical) {
			return nil, false
		}
		if _, duplicate := result[canonical]; duplicate {
			return nil, false
		}
		result[canonical] = "set=" + value
	}
	for _, key := range unset {
		canonical := strings.ToUpper(key)
		if !validEnvironmentKey(key) || strings.ContainsRune(key, '\x00') || forbiddenValidationEnvironment(canonical) {
			return nil, false
		}
		if _, duplicate := result[canonical]; duplicate {
			return nil, false
		}
		result[canonical] = "unset"
	}
	return result, true
}

func validEnvironmentKey(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character == '_' || character >= '0' && character <= '9' && index > 0 {
			continue
		}
		return false
	}
	return true
}

func forbiddenValidationEnvironment(key string) bool {
	return key == "UNIT_TEST_SERVICE_TOKEN" || key == "UNIT_TEST_IDE_TOKEN" || key == "UNIT_TEST_IDE_STATUS_HANDLE" ||
		key == "LD_PRELOAD" || strings.HasPrefix(key, "DYLD_")
}

func verifiedPinnedTool(path string, tools map[string]string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || !directRegularPath(path) {
		return false
	}
	expected, ok := tools[path]
	if !ok || !validDigest(expected) {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	_ = file.Close()
	return err == nil && n <= 256<<20 && hex.EncodeToString(hash.Sum(nil)) == expected
}

func scanStageRoots(roots Roots) bool {
	for _, root := range []string{roots.Build, roots.Artifacts} {
		if !directDirectory(root) {
			return false
		}
		count := 0
		err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
			if walkErr != nil || count >= 100000 {
				return ErrProcessRejected
			}
			count++
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return ErrProcessRejected
			}
			return nil
		})
		if err != nil {
			return false
		}
	}
	return true
}

func safeEnvironmentDestination(key, path string) bool {
	if key == "TEMP" || key == "TMP" {
		return directDirectory(path)
	}
	if key != "LLVM_PROFILE_FILE" || !directDirectory(filepath.Dir(path)) {
		return false
	}
	info, err := os.Lstat(path)
	return os.IsNotExist(err) || err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && singlyLinked(path)
}

func pathWithinRoots(path string, roots Roots) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for _, root := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if root != "" && (path == root || within(root, path)) {
			return true
		}
	}
	return false
}

func directRegularPath(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if next := filepath.Dir(parent); next == parent {
			return true
		}
	}
}
