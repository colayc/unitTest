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

	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
)

var ErrProcessRejected = errors.New("validation process rejected")

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
	RecordLease, ReleaseLease LeaseWriter
}

func (e PreparedProcessExecutor) Execute(ctx context.Context, stage Stage, roots Roots) (evidence StageEvidence, err error) {
	if ctx == nil || e.Runner == nil || e.Plan == nil || e.RecordLease == nil || e.ReleaseLease == nil || !validDigest(e.TaskID) || !validDigest(e.ServiceInstanceID) {
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
			if len(chunk.Data) > maxStageOutput-len(output) {
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
		if len(interpreted.Output) != 0 || interpreted.ExitCode != 0 {
			return StageEvidence{}, ErrProcessRejected
		}
		evidence.DiscoveredCaseIDs = interpreted.DiscoveredCaseIDs
		evidence.CoverageJSON = interpreted.CoverageJSON
	}
	return evidence, nil
}

func (e PreparedProcessExecutor) acceptSpec(spec processcontrol.Spec, roots Roots) bool {
	if spec.Executable == "" || !filepath.IsAbs(spec.Executable) || filepath.Clean(spec.Executable) != spec.Executable || len(spec.Args) > 256 || len(spec.Batch) != 0 || len(spec.LaunchPlan) != 0 || len(spec.LaunchInputs) != 0 || !pathWithinRoots(spec.Dir, roots) || !directDirectory(spec.Dir) || !scanStageRoots(roots) {
		return false
	}
	expected, ok := e.ToolSHA256[spec.Executable]
	if !ok || !validDigest(expected) || !directRegularPath(spec.Executable) {
		return false
	}
	file, err := os.Open(spec.Executable)
	if err != nil {
		return false
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	_ = file.Close()
	if err != nil || n > 256<<20 || hex.EncodeToString(hash.Sum(nil)) != expected {
		return false
	}
	for _, arg := range spec.Args {
		if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
			return false
		}
	}
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
	return true
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
