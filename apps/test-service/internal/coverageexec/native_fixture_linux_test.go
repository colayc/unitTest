//go:build linux && native_llvm_fixture

package coverageexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--probe-supervisor" {
		status := os.NewFile(3, "probe-supervisor-status")
		if status == nil {
			os.Exit(2)
		}
		os.Exit(probe.RunSupervisor(os.Stdin, status, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// The separate coveragellvm tagged test checks native C/C++ totals. This
// fixture makes a real instrumented descendant and routes cancellation through
// the production processcontrol runner and coverage execution-root owner.
func TestNativeLinuxLLVMFixtureCancellationUsesProductionOwners(t *testing.T) {
	bundle := os.Getenv("UTIDE_NATIVE_LLVM_BUNDLE")
	if !filepath.IsAbs(bundle) {
		t.Fatal("UTIDE_NATIVE_LLVM_BUNDLE must name an approved absolute offline bundle")
	}
	clang := filepath.Join(filepath.Clean(bundle), "bin", "clang")
	info, err := os.Lstat(clang)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		t.Fatal("approved bundle lacks a regular bounded clang")
	}
	service := buildNativeFixtureService(t)
	executionParent := filepath.Join(t.TempDir(), "executions")
	if err := os.Mkdir(executionParent, 0o700); err != nil {
		t.Fatal(err)
	}
	const taskID = "11111111111111111111111111111111"
	owner, _, profileRoot, buildRoot, err := allocateExecutionRoots(executionParent, taskID)
	if err != nil {
		t.Fatal(err)
	}
	coverageExecution := &execution{root: owner}
	defer coverageExecution.Close()
	source := filepath.Join(buildRoot, "cancel.c")
	binary := filepath.Join(buildRoot, "cancel")
	program := "#include <stdio.h>\n#include <unistd.h>\n#include <sys/types.h>\nextern int __llvm_profile_write_file(void);\nint main(int argc, char **argv) { pid_t child = fork(); if (child < 0) return 2; if (child == 0) { for (;;) sleep(1); } FILE *f = fopen(argv[1], \"w\"); if (!f) return 3; fprintf(f, \"%ld\\n\", (long)child); fclose(f); __llvm_profile_write_file(); for (;;) sleep(1); }\n"
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: clang, Args: []string{"-fprofile-instr-generate", "-fcoverage-mapping", source, "-o", binary}, Env: []string{"PATH=" + filepath.Join(bundle, "bin") + ":/usr/bin:/bin"}, Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("approved clang failed to compile native cancellation fixture: exit=%d err=%v", result.ExitCode, err)
	}
	if current, err := os.Stat(clang); err != nil || !os.SameFile(info, current) {
		t.Fatal("approved clang changed during compile")
	}
	allocator, err := coveragellvm.NewProfileAllocator(profileRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closer, ok := allocator.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	pidFile := filepath.Join(buildRoot, "child.pid")
	_, spec, err := allocator.Decorate(testrun.ProfileExpectation{InvocationID: "native-cancellation", Iteration: 1, Sequence: 1}, task.ProcessSpec{Executable: binary, Args: []string{pidFile}, Dir: buildRoot})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := processcontrol.NewRunner(service).Prepare(ctx, processcontrol.Spec{Executable: spec.Executable, Args: spec.Args, Dir: spec.Dir, Env: spec.Env, EnvUnset: spec.EnvUnset}, taskID, "22222222222222222222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Terminate(context.Background(), 0); _ = process.Close(context.Background()) }()
	if err := process.Start(ctx); err != nil {
		t.Fatal(err)
	}
	child := waitNativeFixtureChild(t, pidFile)
	waitNativeFixtureProfile(t, profileRoot)
	cancel()
	select {
	case <-process.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("production process owner did not finish cancellation")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := process.Close(closeCtx); err != nil {
		t.Fatalf("production process owner close: %v", err)
	}
	if !waitNativeFixtureChildGone(child, 3*time.Second) {
		t.Fatal("production process owner left the descendant running")
	}
	if closer, ok := allocator.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			t.Fatalf("profile allocator close: %v", err)
		}
	}
	if err := coverageExecution.Close(); err != nil {
		t.Fatalf("production coverage root cleanup: %v", err)
	}
	if _, err := os.Stat(profileRoot); !os.IsNotExist(err) {
		t.Fatalf("production coverage root owner left profraw directory: %v", err)
	}
}

func buildNativeFixtureService(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("native Go toolchain is required to build the production process host")
	}
	service := filepath.Join(t.TempDir(), "unit-test-service")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOPROXY=") || strings.HasPrefix(entry, "GOSUMDB=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GOPROXY=off", "GOSUMDB=off")
	result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: goTool, Args: []string{"build", "-o", service, "../../cmd/unit-test-service"}, Dir: wd, Env: env, Timeout: 2 * time.Minute, MaxOutput: 1 << 20})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("offline production process host build failed: exit=%d err=%v", result.ExitCode, err)
	}
	return service
}

func waitNativeFixtureChild(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents))); parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native instrumented descendant did not start")
	return 0
}

func waitNativeFixtureProfile(t *testing.T, profileRoot string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(profileRoot)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".profraw") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native cancellation fixture did not produce profraw")
}

func waitNativeFixtureChildGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		state, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) || err == nil && strings.Contains(string(state), ") Z ") {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
