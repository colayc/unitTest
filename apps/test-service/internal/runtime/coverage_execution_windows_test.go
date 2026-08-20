//go:build windows

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coverageexec"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type retainedConstructorController struct {
	coverageexec.TaskController
	mu       sync.Mutex
	requests []task.ResumeRequest
	cancels  int
}

func (controller *retainedConstructorController) ResumeQueued(_ context.Context, request task.ResumeRequest) (task.Task, error) {
	controller.mu.Lock()
	controller.requests = append(controller.requests, request)
	controller.mu.Unlock()
	return request.Task, nil
}

func (controller *retainedConstructorController) Cancel(_ context.Context, id string) (task.Task, error) {
	controller.mu.Lock()
	controller.cancels++
	controller.mu.Unlock()
	return task.Task{ID: id, Status: task.StatusFinished, Outcome: task.OutcomeCancelled}, nil
}

func (controller *retainedConstructorController) snapshot() ([]task.ResumeRequest, int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return append([]task.ResumeRequest(nil), controller.requests...), controller.cancels
}

type retainedConstructorBuild struct {
	instance toolchain.Instance
	mu       sync.Mutex
	requests []build.StartRequest
	attached []string
	paths    []string
}

func (preparer *retainedConstructorBuild) PreparePlan(_ context.Context, request build.StartRequest) (coverageexec.PreparedBuild, error) {
	preparer.mu.Lock()
	preparer.requests = append(preparer.requests, request)
	preparer.mu.Unlock()
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{
		{ID: "configure", Kind: task.StepConfigure},
		{ID: "build", Kind: task.StepBuild},
	}}
	plan.Fingerprint = task.FingerprintPlan(plan)
	binaryDir := ""
	if request.Coverage != nil {
		binaryDir = request.Coverage.BinaryDir
	}
	return &retainedConstructorPreparedBuild{
		owner: preparer, instance: preparer.instance,
		workspaceGeneration: request.WorkspaceGeneration,
		project:             workspace.ProjectConfig{ID: request.ProjectID},
		profile:             cmake.BuildProfile{ID: request.BuildProfileID, ProjectID: request.ProjectID},
		plan:                plan, coverageBinaryDir: binaryDir,
	}, nil
}

func (preparer *retainedConstructorBuild) snapshot() ([]build.StartRequest, []string, []string) {
	preparer.mu.Lock()
	defer preparer.mu.Unlock()
	return append([]build.StartRequest(nil), preparer.requests...), append([]string(nil), preparer.attached...), append([]string(nil), preparer.paths...)
}

type retainedConstructorPreparedBuild struct {
	owner               *retainedConstructorBuild
	instance            toolchain.Instance
	workspaceGeneration string
	project             workspace.ProjectConfig
	profile             cmake.BuildProfile
	plan                task.ExecutionPlan
	coverageBinaryDir   string
}

func (prepared *retainedConstructorPreparedBuild) Plan() task.ExecutionPlan { return prepared.plan }
func (*retainedConstructorPreparedBuild) Boundary() task.ExecutionBoundary {
	return retainedConstructorBoundary{}
}
func (prepared *retainedConstructorPreparedBuild) WorkspaceGeneration() string {
	return prepared.workspaceGeneration
}
func (prepared *retainedConstructorPreparedBuild) Project() workspace.ProjectConfig {
	return prepared.project
}
func (prepared *retainedConstructorPreparedBuild) Profile() cmake.BuildProfile {
	return prepared.profile
}
func (prepared *retainedConstructorPreparedBuild) Toolchain() toolchain.Instance {
	return prepared.instance
}
func (*retainedConstructorPreparedBuild) Targets() []cmake.Target { return []cmake.Target{} }
func (*retainedConstructorPreparedBuild) AllowTestExecutable(cmake.FingerprintFile) error {
	return nil
}
func (*retainedConstructorPreparedBuild) ReleaseIfUnadopted() {}
func (prepared *retainedConstructorPreparedBuild) CoverageBinaryDir() string {
	return prepared.coverageBinaryDir
}
func (prepared *retainedConstructorPreparedBuild) AttachCoverageToolset(toolset *coveragellvm.Toolset) error {
	prepared.owner.mu.Lock()
	defer prepared.owner.mu.Unlock()
	prepared.owner.attached = append(prepared.owner.attached, toolset.Identity())
	prepared.owner.paths = append(prepared.owner.paths,
		toolset.Compiler().Path(), toolset.Profdata().Path(), toolset.Cov().Path(),
	)
	return nil
}

type retainedConstructorBoundary struct{}

func (retainedConstructorBoundary) ValidateExecutable(string) error       { return nil }
func (retainedConstructorBoundary) ValidateWorkingDirectory(string) error { return nil }

type retainedConstructorTests struct {
	coverageexec.EmbeddedTestPreparer
}

func TestDefaultWindowsCoverageConstructorUsesCurrentRetainedToolchainWithRealCoordinator(t *testing.T) {
	base := t.TempDir()
	workspacePath := filepath.Join(base, "workspace")
	if err := os.MkdirAll(filepath.Join(workspacePath, ".unit-test-ide"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildProfileID := stringsOf('7', 64)
	configJSON := `{"version":3,"coverageProfiles":[{"id":"coverage-default","baseBuildProfileId":"` + buildProfileID + `","include":["**"],"exclude":[]}]}`
	if err := os.WriteFile(filepath.Join(workspacePath, ".unit-test-ide", "workspace.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := workspace.OpenRoot(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	executionRoot := filepath.Join(base, "coverage-executions")
	if err := os.Mkdir(executionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sqlite, err := taskstore.Open(filepath.Join(base, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	createdAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: buildProfileID, Revision: stringsOf('6', 64), GeneratedAt: createdAt,
		Containers: []testdomain.Container{}, Items: []testdomain.Item{}, Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &productionUnsupportedStore{Store: sqlite, catalog: catalog}
	instance := retainedLLVMToolchainFixture(t)
	toolchainSnapshot, err := coverageToolchainSnapshot(instance, "windows")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{stringsOf('1', 32), stringsOf('2', 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey: stringsOf('3', 32), WorkspaceGeneration: stringsOf('5', 64),
			ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: catalog.Revision,
			Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 1, Timeout: time.Minute,
		},
		Selection:      testdomain.SelectionSnapshot{Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{}},
		BuildProfileID: buildProfileID, ToolchainID: instance.ID, Toolchain: toolchainSnapshot,
		CreatedAt: createdAt, NewID: func() string { id := ids[0]; ids = ids[1:]; return id },
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := aggregate.Persist(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	controller := &retainedConstructorController{}
	buildPreparer := &retainedConstructorBuild{instance: instance}
	executor, err := newRuntimeCoverageExecutor(coverageExecutionConfig{
		Platform: "windows", Tasks: controller, Store: store, Build: buildPreparer,
		Tests: retainedConstructorTests{}, WorkspaceRoot: workspaceRoot, ExecutionRoot: executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	platform, ok := executor.(*platformCoverageExecutor)
	if !ok || !platform.native {
		t.Fatalf("Windows default coverage executor = %#v", executor)
	}
	if _, ok := platform.coordinator.(*coverageexec.Coordinator); !ok {
		t.Fatalf("Windows default coordinator = %T, want real *coverageexec.Coordinator", platform.coordinator)
	}
	if _, err := executor.Resume(context.Background(), persisted); err != nil {
		t.Fatal(err)
	}
	requests, attached, paths := buildPreparer.snapshot()
	if len(requests) != 2 || requests[0].Coverage != nil || requests[1].Coverage == nil ||
		len(attached) != 1 || attached[0] != instance.Coverage.ToolsetIdentity ||
		!reflect.DeepEqual(paths, []string{instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov}) {
		t.Fatalf("retained production wiring: requests=%#v attached=%v paths=%v", requests, attached, paths)
	}
	controllerRequests, cancels := controller.snapshot()
	if len(controllerRequests) != 1 || cancels != 0 || controllerRequests[0].Task.ID != persisted.ID {
		t.Fatalf("real Coordinator requests/cancels = %#v/%d", controllerRequests, cancels)
	}
	if err := executor.Close(); err != nil {
		t.Fatal(err)
	}
	_, cancels = controller.snapshot()
	if cancels != 1 {
		t.Fatalf("real Coordinator active cancellation calls = %d, want 1", cancels)
	}
}

func retainedLLVMToolchainFixture(t *testing.T) toolchain.Instance {
	t.Helper()
	root := filepath.Join(t.TempDir(), "LLVM", "bin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(root, "clang-cl.exe"),
		filepath.Join(root, "llvm-profdata.exe"),
		filepath.Join(root, "llvm-cov.exe"),
	}
	for index, path := range paths {
		contents := makeRepeatedBytes(byte('a'+index), 128)
		if err := os.WriteFile(path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	for index, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &info)
		_ = windows.CloseHandle(handle)
		if err != nil {
			t.Fatal(err)
		}
		evidence[index] = toolchain.ExecutableEvidence{
			FileIdentity: fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow),
			SHA256:       hex.EncodeToString(sum[:]),
		}
	}
	instance := toolchain.Instance{
		ID: "inspector-retained-clang-cl", Family: toolchain.FamilyClangCL,
		CCompiler: paths[0], CXXCompiler: paths[0], Version: "20.1.8", TargetArchitecture: "amd64",
		Coverage: toolchain.CoverageCapability{
			LLVMProfdata: paths[1], LLVMCov: paths[2], CompilerEvidence: evidence[0],
			ProfdataEvidence: evidence[1], CovEvidence: evidence[2],
		},
	}
	instance.Coverage.ToolsetIdentity = toolchain.LLVMToolsetIdentity(instance.Version, paths, evidence)
	return instance
}
