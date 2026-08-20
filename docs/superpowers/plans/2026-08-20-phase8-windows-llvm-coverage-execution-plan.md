# Phase 8：Windows LLVM Coverage Execution Coordinator 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Phase 7 的 durable queued `CoverageRun` 接到共享执行协调器和 Windows `clang-cl/llvm-profdata/llvm-cov` 真实链路，原子生成 Coverage JSON v1、JUnit XML 和单文件 HTML。

**Architecture:** 新建 `coverageexec` 作为 queued run 的唯一 execution owner，以 Task Engine continuation 串联 coverage build、内嵌 TestRun、LLVM profile merge/export、normalization、report 和 terminal publish。Windows `coveragellvm` 只接受同一 installation 的 retained tool capabilities；`taskstore` 继续作为 Task/TestRun/CoverageRun/Report/artifact 的唯一终态事务 owner。Linux 本批只编译并验证共享接口，不注册 native adapter。

**Tech Stack:** Go 1.26.6、SQLite、CMake、Windows `clang-cl`/`llvm-profdata`/`llvm-cov`、TypeScript 6、Node.js 24.18.0、pnpm 11.4.0、Protocol v1.4。

## Global Constraints

- 一个 CoverageRun 只创建一个顶层 Task、一个关联 TestRun 和一个最终 CoverageReport；不得创建 nested Task。
- Windows coverage 只接受同一 LLVM installation、版本一致且持久 pin 的 `clang-cl`、`llvm-profdata`、`llvm-cov`。
- Protocol、workspace config 和 test metadata 不得提供 executable、argv、environment、cwd、native output path 或 collector 参数。
- raw `.profraw`、`.profdata` 和 LLVM export JSON 只存在于 Task-owned 临时目录，不注册为 artifact。
- Coverage JSON v1 是 coverage metric、source mapping 和 tool provenance 的唯一事实来源；JUnit outcome 只来自关联 TestRun。
- assertion failure 不阻止 coverage report；正常退出但缺 expected profile 是 infrastructure failure。
- crash、test timeout 或失败 invocation 缺 profile 可产生 `partial`，且缺失 evidence 不计为 uncovered。
- cancel、Task timeout、trust loss 和 Service restart 后不得继续启动下一阶段；不得发布半成品 report。
- 产品 runtime 不依赖网络、GitHub、Gitee、系统 Python package 或用户 coverage config。
- Windows 第一批必须有真实 clang-cl smoke；Linux fake/cross compile 不得声明 native coverage PASS。
- 每个开发提交都推送 `github master` 和 `origin master`，两端必须指向相同 commit。

---

## 文件结构与职责

- `internal/task`：支持无 shell 的 bounded service action，并把 coverage completion 交给现有 Store transaction。
- `internal/artifactstore`：接受并原子发布三个 coverage report artifact，不接受 raw profile。
- `internal/taskstore`：恢复 running coverage、写 coverage domain events、保持 durable-before-publish。
- `internal/coveragellvm`：Windows LLVM retained toolset、instrumentation include、profile allocation 和 collector invocation。
- `internal/coverageparser/llvm`：只解析 bounded LLVM export JSON，不访问 filesystem。
- `internal/coveragenormalize`：将 LLVM object 与 verified workspace sources 转为 canonical Coverage JSON v1。
- `internal/testrun`：在既有 Coverage Task 内准备并解释 test waves，不创建第二个 Task。
- `internal/coveragereport`：从 canonical Coverage JSON 与 completed TestRun 生成 deterministic JUnit/HTML。
- `internal/coverageexec`：领取 queued run，串联所有阶段并准备唯一 terminal completion。
- `internal/runtime`：只在 trusted Windows runtime 注册 Windows adapter；负责 queued resume/restart cleanup。
- `apps/code-oss-extension/test` 与 `.github/workflows/foundation.yml`：真实 Protocol/Named Pipe/report artifact smoke 和 CI evidence。

---

### Task 1：Task Engine 的 service action 与 coverage completion 接口

**Files:**
- Modify: `apps/test-service/internal/task/plan.go:29-156`
- Modify: `apps/test-service/internal/task/ports.go:28-196`
- Modify: `apps/test-service/internal/task/manager.go:65-164, 533-690`
- Modify: `apps/test-service/internal/task/manager_execution.go:112-174, 359-520`
- Modify: `apps/test-service/internal/task/manager_completion.go:20-156`
- Modify: `apps/test-service/internal/task/manager_artifacts.go:47-160`
- Test: `apps/test-service/internal/task/plan_test.go`
- Test: `apps/test-service/internal/task/continuation_test.go`
- Test: `apps/test-service/internal/task/manager_execution_test.go`
- Test: `apps/test-service/internal/task/manager_completion_test.go`

**Interfaces:**
- Consumes: existing `task.ExecutionPlan`, `PlanContinuation`, `ResultInterpreter`, `CompletionPreparer` and `task.CoverageCompletion`.
- Produces:

```go
type ServiceAction string

const (
    ServiceActionCoverageReport  ServiceAction = "coverage-report"
    ServiceActionCoveragePublish ServiceAction = "coverage-publish"
)

type ServiceActionExecutor interface {
    ExecuteServiceAction(context.Context, Task, ExecutionStep) (StepResult, error)
}

// ExecutionStep contains exactly one of Process or Action.
type ExecutionStep struct {
    ID      string
    Kind    StepKind
    Process ProcessSpec
    Action  ServiceAction
    Public  CommandSummary
    State   json.RawMessage
    DiagnosticParser diagnostic.Parser
}

type DomainCompletion struct {
    TestRun  *testdomain.TestRun
    Coverage *CoverageCompletion
    Events   []DomainEvent
}
```

- `StartRequest` and `ResumeRequest` gain `ActionExecutor ServiceActionExecutor`; clone helpers preserve the interface without persisting it.
- `ServiceAction` is runtime-only. Its value enters `FingerprintPlan`, but no callback, path or state pointer enters SQLite or Protocol.

- [ ] **Step 1: Write failing service-action and completion tests**

Add table tests proving mutual exclusion and closed action values:

```go
func TestValidatePlanAcceptsOnlyClosedServiceActions(t *testing.T) {
    action := task.ExecutionStep{
        ID: "coverage-report", Kind: task.StepCoverageReport,
        Action: task.ServiceActionCoverageReport,
        Public: task.CommandSummary{Executable: "coverage-report"},
    }
    plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{action}}
    plan.Fingerprint = task.FingerprintPlan(plan)
    if err := task.ValidatePlan(plan, actionBoundary{}); err != nil { t.Fatal(err) }

    both := action
    both.Process = validProcessSpec()
    assertInvalidPlan(t, both)
    unknown := action
    unknown.Action = "workspace-script"
    assertInvalidPlan(t, unknown)
}
```

Add Manager tests that hold an action callback, cancel or shut down the Manager, then assert: the step was durably `running` before callback entry; callback ran outside the command loop; no `ProcessFactory.Prepare` or lease was used; cancellation context reached the callback; completion ran once; and a `KindCoverageRun` terminal mutation carries both `FinishRun` and `FinishCoverage`.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/task -run 'ServiceAction|CoverageCompletion|Continuation' -count=1
```

Expected: compile failures for missing `ServiceAction`, `ServiceActionExecutor`, `ActionExecutor` and `DomainCompletion.Coverage`.

- [ ] **Step 3: Implement bounded asynchronous service actions**

Implement these invariants in `task`:

```go
func validExecutionStep(step ExecutionStep, boundary ExecutionBoundary) bool {
    hasProcess := step.Process.Executable != "" || len(step.Process.Batch) != 0
    hasAction := step.Action != ""
    if hasProcess == hasAction { return false }
    if hasAction {
        return validServiceAction(step.Kind, step.Action) && step.DiagnosticParser == nil
    }
    return validProcessSpec(step.Process, boundary)
}
```

`Manager.startNextStep` must first persist Task/Step `running`; for an action it then starts one goroutine with `current.execution.ctx` and sends an `actionDoneCommand`. The command loop converts action errors to `OutcomeInfrastructureFailed`, applies `PlanContinuation`, and advances through the same `persistSuccessfulStep` path used by processes. It must never fabricate a lease or call process close/terminate.

Extend `resumableQueuedKind` and `validateStartRequest` to accept `KindCoverageRun` only when `TestRun` is present and a completion preparer is attached. In `persistFinished`, pass `completion.Coverage` as `Mutation.FinishCoverage`; reject a coverage task whose completion has no finished TestRun/CoverageRun pair.

- [ ] **Step 4: Run task package and race tests**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/task -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/task -run 'ServiceAction|CoverageCompletion|Cancel|Shutdown' -count=1
```

Expected: all tests PASS; race detector reports no data races.

- [ ] **Step 5: Commit and push Task Engine support**

```powershell
git add apps/test-service/internal/task
git diff --cached --check
git commit -m "feat: run coverage service actions in task engine"
git push github master
git push origin master
```

---

### Task 2：Coverage artifact、domain events 与 restart terminal transaction

**Files:**
- Modify: `apps/test-service/internal/artifactstore/store.go:46-220, 944-961`
- Modify: `apps/test-service/internal/artifactstore/task_sink.go:19-425`
- Modify: `apps/test-service/internal/task/model.go:31-88`
- Modify: `apps/test-service/internal/task/ports.go:158-245`
- Modify: `apps/test-service/internal/taskstore/recovery.go:105-220`
- Modify: `apps/test-service/internal/taskstore/artifacts.go:13-93`
- Modify: `apps/test-service/internal/server/server.go:339-441`
- Test: `apps/test-service/internal/artifactstore/task_sink_test.go`
- Test: `apps/test-service/internal/artifactstore/store_test.go`
- Create: `apps/test-service/internal/taskstore/recovery_coverage_test.go`
- Test: `apps/test-service/internal/taskstore/coverage_reports_test.go`
- Create: `apps/test-service/internal/server/v14_projection_test.go`

**Interfaces:**
- Consumes: Task 1 `DomainCompletion.Coverage` and existing `taskstore.validateTerminalMutation`.
- Produces:

```go
type CoverageArtifactSink interface {
    ArtifactSink
    CommitBlob(context.Context, string, string, []byte) error
}

const (
    EventCoverageRunStarted       EventType = "coverage.run.started"
    EventCoverageBuildFinished    EventType = "coverage.build.finished"
    EventCoverageCollectionStarted EventType = "coverage.collection.started"
    EventCoverageReportAvailable EventType = "coverage.report.available"
    EventCoverageRunFinished      EventType = "coverage.run.finished"
)
```

- `CoverageArtifactSink` is an optional extension implemented by `artifactstore.taskSink`, so existing Task/TestRun test sinks do not gain an unrelated method. `CommitBlob` accepts only `coverage-json`, `junit-xml`, `coverage-html` for `KindCoverageRun`.
- `RecoverInterrupted` leaves queued coverage unchanged; running/cancelling coverage becomes Task `interrupted`, TestRun `interrupted`, CoverageRun `unavailable/service_restarted` in one transaction.
- `server.toProtocolEvent` emits coverage domain event names only for Protocol v1.4; v1.1-v1.3 project them to the existing empty compatibility output while preserving monotonic sequence numbers.

- [ ] **Step 1: Write failing artifact/recovery/event tests**

Create tests with these exact report bodies:

```go
coverageJSON := []byte("{\"schemaVersion\":\"1.0\"}\n")
junitXML := []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><testsuites></testsuites>\n")
coverageHTML := []byte("<!doctype html><meta charset=\"utf-8\"><title>Coverage</title>\n")
```

Assert `CommitBlob` rejects empty bytes, over-limit bytes, duplicate kind/ID, coverage kind on non-coverage Task, raw `.profraw`/`.profdata`, and JUnit/HTML passed to `CommitJSON`. Assert a valid report-bearing sink finalizes exactly one artifact per public kind with correct MIME, extension, lowercase SHA-256 and owner Task ID. An unavailable completion may finalize only bounded stdout/stderr/diagnostics; it must not contain a public coverage artifact.

Seed SQLite with queued and running coverage tasks. After `RecoverInterrupted`, assert queued remains queued; running becomes `CoverageRun{Outcome: unavailable, Reason: service_restarted}`, associated TestRun is interrupted/incomplete, no CoverageReport exists, and event order is test-run-finished → coverage-run-finished → task-finished. Projection tests cover all five Protocol v1.4 coverage events and their v1.1-v1.3 compatibility output.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/taskstore ./apps/test-service/internal/server -run 'Coverage|Restart|Artifact|EventV14' -count=1
```

Expected: compile failure for `CommitBlob` and coverage event constants, followed by failing recovery assertions.

- [ ] **Step 3: Implement closed report artifacts and recovery**

Use exact descriptors:

```go
case "coverage-json": return "application/json", ".coverage.json", true
case "junit-xml": return "application/xml", ".junit.xml", true
case "coverage-html": return "text/html", ".coverage.html", true
```

`taskSink.pendingArtifacts` for `KindCoverageRun` allows exactly two sets:

1. non-report terminal: stream/diagnostic artifacts only;
2. report terminal: stream/diagnostic artifacts plus exactly one `coverage-json`, `junit-xml`, `coverage-html`.

Keep `taskstore.validateCoverageArtifacts` as the final closed-set authority. Extend recovery in the same SQL transaction that updates Task/TestRun; construct a canonical finished `coveragedomain.Run`, update its sequence to the final event sequence, and never insert a report row or public artifact.

- [ ] **Step 4: Run storage/artifact/session race gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/taskstore ./apps/test-service/internal/server -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/artifactstore ./apps/test-service/internal/taskstore -run 'Coverage|Restart|Artifact' -count=1
```

Expected: PASS, including fault injection before temp publish, after link, before DB commit and duplicate terminal replay.

- [ ] **Step 5: Commit and push durable coverage completion support**

```powershell
git add apps/test-service/internal/artifactstore apps/test-service/internal/task apps/test-service/internal/taskstore apps/test-service/internal/server
git diff --cached --check
git commit -m "feat: persist coverage reports atomically"
git push github master
git push origin master
```

---

### Task 3：Windows LLVM retained toolset、instrumentation 与 isolated build plan

**Files:**
- Create: `apps/test-service/internal/coveragellvm/toolset.go`
- Create: `apps/test-service/internal/coveragellvm/toolset_windows.go`
- Create: `apps/test-service/internal/coveragellvm/toolset_nonwindows.go`
- Create: `apps/test-service/internal/coveragellvm/toolset_windows_test.go`
- Create: `apps/test-service/internal/coveragellvm/instrumentation.go`
- Create: `apps/test-service/internal/coveragellvm/instrumentation_test.go`
- Modify: `apps/test-service/internal/build/model.go:17-33`
- Modify: `apps/test-service/internal/build/planner.go:18-169`
- Modify: `apps/test-service/internal/build/coordinator.go:143-239, 303-430`
- Modify: `apps/test-service/internal/build/boundary.go:17-330`
- Test: `apps/test-service/internal/build/planner_test.go`
- Test: `apps/test-service/internal/build/coordinator_test.go`

**Interfaces:**
- Consumes: verified `toolchain.Instance`, `build.PreparedPlan`, `task.ManagedExecutionBoundary` and Service data root.
- Produces:

```go
type pinnedTool struct {
    path string
    file *os.File
    info os.FileInfo
    sha256 string
}

type Toolset struct {
    compiler pinnedTool
    profdata pinnedTool
    cov pinnedTool
    version string
    closeOnce sync.Once
    closeErr error
}

func PinToolset(toolchain.Instance) (*Toolset, error)
func (t *Toolset) Compiler() coveragerun.TrustedPath
func (t *Toolset) Profdata() coveragerun.TrustedPath
func (t *Toolset) Cov() coveragerun.TrustedPath
func (t *Toolset) Version() string
func (t *Toolset) Verify() error
func (t *Toolset) Close() error

type Instrumentation struct {
    IncludePath string
    SHA256 string
    Fingerprint string
}

func WriteInstrumentation(taskRoot string) (Instrumentation, error)

type CoverageOptions struct {
    BinaryDir string
    TopLevelInclude cmake.FingerprintFile
    InstrumentationFingerprint string
}
```

- `build.StartRequest` gains runtime-only `Coverage *CoverageOptions`; it is accepted only by `PreparePlan`, never by normal `Start` or request JSON encoding.
- `PreparedPlan` exposes `CoverageBinaryDir()` and `AttachCoverageToolset(*coveragellvm.Toolset) error`; successful attach transfers ownership to the boundary.

- [ ] **Step 1: Write failing Windows toolset and planner tests**

Cover direct regular files, same-directory rule, exact basenames, compiler/profdata/cov version equality, replacement while pinned, reparse/junction/hardlink aliases, close idempotency and non-Windows `ErrUnsupportedPlatform`.

Golden instrumentation content must be byte-identical LF text:

```cmake
cmake_minimum_required(VERSION 3.25)
if(NOT CMAKE_CXX_COMPILER_ID MATCHES "Clang")
  message(FATAL_ERROR "unit-test-ide coverage requires clang-cl")
endif()
add_compile_options("$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>" "$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>")
add_link_options("-fprofile-instr-generate")
```

Planner tests assert preset and generated profiles both add exactly:

```text
-B <service-owned coverage binary dir>
-DCMAKE_PROJECT_TOP_LEVEL_INCLUDES:FILEPATH=<service-owned include>
```

and never reuse the base profile `BinaryDir`. `ConfigureFingerprint` must change when instrumentation digest/template version/tool identity changes and remain stable when ordinary source content changes.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/build -run 'LLVM|Coverage|Instrumentation' -count=1
```

Expected: missing package/API compile failures.

- [ ] **Step 3: Implement fail-closed pinning and typed CMake injection**

On Windows open each tool with `FILE_FLAG_OPEN_REPARSE_POINT`, `GENERIC_READ`, and share-read only; retain the handle and compare volume serial/file index before and after every validation. Hash the opened handle, not a reopened path. `PinToolset` accepts only `FamilyClangCL`, non-empty `Coverage.LLVMProfdata`/`LLVMCov`, same canonical parent and matching `Instance.Version` established by the current discovery snapshot.

Create the instrumentation include under a fresh owner-only Task root using create-exclusive → write → sync → close → digest → atomic rename. `build` may append only the two typed coverage arguments above; it must not expose a generic extra-args field.

- [ ] **Step 4: Run Windows tests, race and Linux cross-compile**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/build -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/build -run 'LLVM|Coverage|Boundary' -count=1
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; $env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -c ./apps/test-service/internal/coveragellvm
```

Expected: Windows tests PASS; Linux package compiles and exposes no native toolset.

- [ ] **Step 5: Commit and push Windows LLVM planning support**

```powershell
git add apps/test-service/internal/coveragellvm apps/test-service/internal/build
git diff --cached --check
git commit -m "feat: prepare isolated clang-cl coverage builds"
git push github master
git push origin master
```

---

### Task 4：Bounded LLVM parser 与 canonical Coverage JSON v1 normalization

**Files:**
- Create: `apps/test-service/internal/coverageparser/llvm/model.go`
- Create: `apps/test-service/internal/coverageparser/llvm/parser.go`
- Create: `apps/test-service/internal/coverageparser/llvm/parser_test.go`
- Create: `apps/test-service/internal/coverageparser/llvm/testdata/simple.json`
- Create: `apps/test-service/internal/coverageparser/llvm/testdata/branches.json`
- Create: `apps/test-service/internal/coverageparser/llvm/testdata/windows-path.json`
- Create: `apps/test-service/internal/coverageparser/llvm/testdata/malformed.json`
- Create: `apps/test-service/internal/coveragenormalize/llvm.go`
- Create: `apps/test-service/internal/coveragenormalize/llvm_test.go`
- Create: `apps/test-service/internal/coveragenormalize/writer.go`
- Create: `apps/test-service/internal/coveragenormalize/writer_test.go`
- Create: `apps/test-service/internal/coveragenormalize/testdata/coverage-v1.golden.json`

**Interfaces:**
- Consumes: existing `coveragenormalize.Limits`, `GlobMatcher`, `CollectSources` and generated `coveragemodel/v1` validator.
- Produces:

```go
package llvm

type Export struct {
    Version string
    Files []File
}

type File struct {
    NativePath string
    Functions Metric
    Lines []Line
}

type Limits struct {
    MaxInputBytes int64
    MaxDepth int64
    MaxFiles int64
    MaxFunctions int64
    MaxLines int64
    MaxBranches int64
    MaxStringBytes int64
}

func Parse(io.Reader, Limits) (Export, error)

package coveragenormalize

type LLVMInput struct {
    Export llvm.Export
    WorkspaceRoot string
    Matcher *GlobMatcher
    Toolchain coveragedomain.ToolchainSnapshot
    Completeness coveragedomain.Completeness
    Limits Limits
}

func NormalizeLLVM(LLVMInput) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error)
func EncodeCanonical(coveragemodelv1.CoverageDocumentV1) ([]byte, error)
```

- [ ] **Step 1: Write parser/normalizer Golden and negative tests**

Parser tests cover chunk sizes 1..257, LLVM version, `data/files/functions/segments/branches`, Windows paths kept internal, duplicate or unknown JSON fields, invalid/negative/floating/unsafe integers, excessive depth/files/functions/lines/branches/string/input bytes, invalid UTF-8 and unsupported major version.

Normalizer tests prove: include/exclude and mandatory `.git`/data/build exclusion; source identity and SHA-256; duplicate physical source rejection; line/branch/function totals; stable URI/line ordering; available/partial completeness; no native path/time/run ID/percentage; and repeated input yields byte-identical bytes matching the golden file.

- [ ] **Step 2: Run parser/normalizer tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/coveragenormalize -run 'LLVM|Canonical|Golden|Limit' -count=1
```

Expected: missing package/functions compile failures.

- [ ] **Step 3: Implement streaming bounded parse and normalization**

Use `json.Decoder.Token` with an explicit object-field seen set and depth counter. Reject duplicate fields even if Go would otherwise overwrite them. Do not return a partially filled `Export` on error.

Map LLVM executable segments to common line counts with these rules: one physical source/line appears once; covered means execution count > 0; line count is the maximum applicable segment count; branches aggregate true/false counters without regions/template/MC/DC duplication; function identity is file + start line + mangled name and is counted once. Build the generated `CoverageDocumentV1`, call `coveragemodelv1.Validate`, marshal the struct in field order, append one LF, decode it again, and require deep equality.

- [ ] **Step 4: Run parser fuzz-style, race and generated contract gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragemodel/... -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/coveragenormalize -count=1
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' tools/coverage-gen/generate.mjs --check
```

Expected: PASS and generated contract unchanged.

- [ ] **Step 5: Commit and push LLVM parsing/normalization**

```powershell
git add apps/test-service/internal/coverageparser/llvm apps/test-service/internal/coveragenormalize
git diff --cached --check
git commit -m "feat: normalize llvm coverage exports"
git push github master
git push origin master
```

---

### Task 5：内嵌 TestRun 与 closed profile manifest

**Files:**
- Create: `apps/test-service/internal/coveragellvm/profile.go`
- Create: `apps/test-service/internal/coveragellvm/profile_test.go`
- Create: `apps/test-service/internal/coveragellvm/collector.go`
- Create: `apps/test-service/internal/coveragellvm/collector_test.go`
- Create: `apps/test-service/internal/testrun/embedded.go`
- Create: `apps/test-service/internal/testrun/embedded_test.go`
- Modify: `apps/test-service/internal/testrun/coordinator.go:124-169, 329-787`
- Modify: `apps/test-service/internal/testrun/planner.go:32-70, 498-715`
- Modify: `apps/test-service/internal/runtime/test_runtime.go:31-145`
- Test: `apps/test-service/internal/testrun/coordinator_test.go`

**Interfaces:**
- Consumes: Task 3 `coveragellvm.Toolset`, build `PreparedPlan`, persisted `TestRun`, existing framework adapters/interpreter and Task 4 parser output.
- Produces:

```go
package testrun

type ProfileExpectation struct {
    InvocationID string
    Iteration int64
    FileName string
}

type InvocationOutcome struct {
    InvocationID string
    Iteration int64
    ExitCode int
    Crashed bool
    TimedOut bool
}

type ProfileAllocator interface {
    Decorate(ProfileExpectation, task.ProcessSpec) (task.ProcessSpec, error)
}

type EmbeddedRequest struct {
    TaskID string
    Run testdomain.TestRun
    PreparedBuild PreparedBuild
    Catalog testdomain.Catalog
    Allocator ProfileAllocator
    MaxConcurrency int
}

type EmbeddedRun interface {
    Steps() []task.ExecutionStep
    Interpret(context.Context, task.Task, task.ExecutionStep, task.ProcessResult) (task.StepVerdict, error)
    ObserveOutput(context.Context, task.Task, task.ExecutionStep, task.ProcessOutput) error
    DrainDomainEvents() []task.DomainEvent
    Finish(context.Context, time.Time, task.Outcome) (testdomain.TestRun, error)
    Expectations() []ProfileExpectation
}

func (c *Coordinator) PrepareEmbedded(context.Context, EmbeddedRequest) (EmbeddedRun, error)

package coveragellvm

type ManifestEntry struct {
    Expectation testrun.ProfileExpectation
    Path string
    SHA256 string
    Size int64
}

type Manifest struct {
    Entries []ManifestEntry
}

func NewProfileAllocator(profileRoot string) (testrun.ProfileAllocator, error)
func SealProfiles(profileRoot string, []testrun.ProfileExpectation, []testrun.InvocationOutcome) (Manifest, error)
func BuildCollectorInvocation(toolset *Toolset, manifest Manifest, binaries []coveragerun.TrustedPath) (merge task.ProcessSpec, export task.ProcessSpec, err error)
```

- [ ] **Step 1: Write failing embedded-run/profile tests**

Tests must prove:

- `PrepareEmbedded` never calls `TaskStarter.Start` or creates another Task/TestRun;
- selection/catalog/profile/workspace generation are revalidated before planning;
- existing framework parsers still persist each TestItem result/event;
- each invocation/iteration gets exactly one stable file name such as `p-000001-i-000001-%p-%m.profraw`;
- inherited/user `LLVM_PROFILE_FILE`, `LLVM_PROFILE_MERGE_FILE`, GCOV/Python/proxy/home/registry variables are removed before the Service value is appended;
- concurrent invocations cannot collide;
- only expected regular `.profraw` files under the retained profile root enter the manifest;
- unknown, duplicate, hardlink, symlink/reparse, outside, oversized or replaced profiles fail closed;
- normal exit without profile is infrastructure failure; failed/crashed/timed-out invocation without profile records the exact partial reason;
- merge args are stable `merge -sparse <sorted profiles> -o <owned profdata>`; export args are stable `export -format=text -instr-profile=<owned profdata> <primary> -object <additional...>`.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/testrun ./apps/test-service/internal/runtime -run 'Embedded|Profile|Collector|LLVM' -count=1
```

Expected: missing embedded/profile APIs and failing nested-task assertions.

- [ ] **Step 3: Implement embedded planning and closed profile ownership**

Factor existing `runExecution` planning/interpreter logic behind `EmbeddedRun`; keep `StartRun` behavior unchanged. The embedded variant receives the already persisted run ID and must not call `CreateRun`, `CreateTestTask` or Task Manager `Start`. It may call existing result repository methods and emit bounded domain events for the owner Coverage Task.

The allocator sets exactly one Service-owned environment entry after sanitization:

```go
"LLVM_PROFILE_FILE=" + filepath.Join(profileRoot, expectation.FileName)
```

`SealProfiles` opens the pinned profile root, enumerates a closed set once, validates file identity/size/digest against expectations and returns handles or immutable snapshots used by collector construction. No recursive workspace search is allowed.

- [ ] **Step 4: Run full embedded/collector race gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/testrun ./apps/test-service/internal/runtime -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/testrun -run 'Embedded|Profile|Collector' -count=1
```

Expected: PASS; cancellation and replacement tests release every retained handle once.

- [ ] **Step 5: Commit and push embedded TestRun collection**

```powershell
git add apps/test-service/internal/coveragellvm apps/test-service/internal/testrun apps/test-service/internal/runtime/test_runtime.go
git diff --cached --check
git commit -m "feat: collect coverage from embedded test runs"
git push github master
git push origin master
```

---

### Task 6：Coverage report renderer 与 immutable report set

**Files:**
- Create: `apps/test-service/internal/coveragereport/model.go`
- Create: `apps/test-service/internal/coveragereport/junit.go`
- Create: `apps/test-service/internal/coveragereport/junit_test.go`
- Create: `apps/test-service/internal/coveragereport/html.go`
- Create: `apps/test-service/internal/coveragereport/html_test.go`
- Create: `apps/test-service/internal/coveragereport/report.go`
- Create: `apps/test-service/internal/coveragereport/report_test.go`
- Create: `apps/test-service/internal/coveragereport/testdata/junit.golden.xml`
- Create: `apps/test-service/internal/coveragereport/testdata/report.golden.html`

**Interfaces:**
- Consumes: Task 4 canonical `CoverageDocumentV1` bytes, source bindings and Task 5 completed TestRun.
- Produces:

```go
type Input struct {
    CoverageJSON []byte
    Document coveragemodelv1.CoverageDocumentV1
    TestRun testdomain.TestRun
    Sources []coveragenormalize.SourceBinding
}

type Set struct {
    CoverageJSON []byte
    JUnitXML []byte
    CoverageHTML []byte
    Summary coveragedomain.Summary
    Sources []coveragedomain.SourceSnapshot
}

func Render(Input) (Set, error)
func Validate(Set) error
```

- [ ] **Step 1: Write deterministic JUnit/HTML/report-set tests**

JUnit tests cover pass/failure/error/skip/not-run, repeat iteration, assertion/mock/source location, XML escaping/control characters, bounded diagnostic text, stable ordering and no timestamp/duration/native path/token/environment/command. Coverage percentages must never change testcase outcome.

HTML tests require exact CSP:

```text
default-src 'none'; img-src data:; style-src 'sha256-<fixed-css-digest>'; script-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'
```

The HTML contains only escaped summary/provenance/file/line/source text, fixed embedded CSS, no script, external URL, form, frame, font or source map. A stale/oversized/binary source becomes metadata-only rather than failing the complete report. Repeated render must match the golden files byte-for-byte.

- [ ] **Step 2: Run renderer tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragereport -count=1
```

Expected: package/API missing compile failure.

- [ ] **Step 3: Implement pure deterministic renderers**

Decode and validate `CoverageJSON` before rendering; require it to equal `Document`. Render JUnit with `encoding/xml` tokens and explicit stable sorting. Render HTML with `html/template` only for text/attribute contexts; embed one compile-time CSS constant whose SHA-256 is asserted by tests. `Validate` rechecks JSON schema, XML well-formedness with DTD/entity rejection, HTML doctype/CSP/no-network structure and per-artifact byte limits.

- [ ] **Step 4: Run report package race and malicious-input gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/coveragemodel/... -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coveragereport -count=1
```

Expected: PASS; malicious `</style>`, XML entity, Unicode separator, native path and token fixtures remain inert/redacted.

- [ ] **Step 5: Commit and push report renderers**

```powershell
git add apps/test-service/internal/coveragereport
git diff --cached --check
git commit -m "feat: render offline coverage report sets"
git push github master
git push origin master
```

---

### Task 7：`coverageexec.Coordinator` 全链路编排与唯一终态

**Files:**
- Create: `apps/test-service/internal/coverageexec/model.go`
- Create: `apps/test-service/internal/coverageexec/planner.go`
- Create: `apps/test-service/internal/coverageexec/planner_test.go`
- Create: `apps/test-service/internal/coverageexec/boundary.go`
- Create: `apps/test-service/internal/coverageexec/boundary_test.go`
- Create: `apps/test-service/internal/coverageexec/coordinator.go`
- Create: `apps/test-service/internal/coverageexec/coordinator_test.go`
- Create: `apps/test-service/internal/coverageexec/completion.go`
- Create: `apps/test-service/internal/coverageexec/completion_test.go`
- Modify: `apps/test-service/internal/coveragerun/state.go:17-117`
- Modify: `apps/test-service/internal/coveragerun/report.go:14-73`
- Test: `apps/test-service/internal/coveragerun/state_test.go`

**Interfaces:**
- Consumes: Task 1 action executor, Task 2 artifact/completion contract, Task 3 build/toolset, Task 4 normalizer, Task 5 embedded test run and Task 6 report set.
- Produces:

```go
type TaskResumer interface {
    ResumeQueued(context.Context, task.ResumeRequest) (task.Task, error)
}

type Store interface {
    task.CoverageRepository
    task.TestRunRepository
    Get(context.Context, string) (task.Task, error)
    GetRunForTask(context.Context, string) (testdomain.TestRun, error)
}

type BuildPreparer interface {
    PreparePlan(context.Context, build.StartRequest) (*build.PreparedPlan, error)
}

type EmbeddedTestPreparer interface {
    PrepareEmbedded(context.Context, testrun.EmbeddedRequest) (testrun.EmbeddedRun, error)
}

type AdapterInput struct {
    Toolchain toolchain.Instance
    TaskRoot string
    ProfileRoot string
}

type PreparedAdapter interface {
    Toolset() *coveragellvm.Toolset
    Instrumentation() coveragellvm.Instrumentation
    Allocator() testrun.ProfileAllocator
    SealProfiles([]testrun.ProfileExpectation, []testrun.InvocationOutcome) (coveragellvm.Manifest, error)
    Collector(coveragellvm.Manifest, []coveragerun.TrustedPath) (merge, export task.ProcessSpec, err error)
    Close() error
}

type Adapter interface {
    Prepare(context.Context, AdapterInput) (PreparedAdapter, error)
}

type Config struct {
    Tasks TaskResumer
    Store Store
    Build BuildPreparer
    Tests EmbeddedTestPreparer
    Adapter Adapter
    WorkspaceRoot workspace.Root
    ExecutionRoot string
    Clock task.Clock
    NewID task.IDGenerator
}

type liveExecution interface {
    task.PlanContinuation
    task.ResultInterpreter
    task.ResultOutputObserver
    task.DomainEventSource
    task.ServiceActionExecutor
    task.CompletionPreparer
    io.Closer
}

type Coordinator struct {
    config Config
    mu sync.Mutex
    executions map[string]liveExecution
    closed bool
}

func NewCoordinator(Config) (*Coordinator, error)
func (c *Coordinator) Resume(context.Context, task.Task) (task.Task, error)
func (c *Coordinator) FinishUnsupported(context.Context, task.Task) (task.Task, error)
func (c *Coordinator) Close() error
```

`Coordinator`'s per-run execution implements `task.PlanContinuation`, `task.ResultInterpreter`, `task.ResultOutputObserver`, `task.DomainEventSource`, `task.ServiceActionExecutor` and `task.CompletionPreparer`.

- [ ] **Step 1: Write failing orchestration/outcome/ownership tests**

Using a real temporary SQLite Store/ArtifactStore and fake process factory, assert exact sequence:

```text
coverage-configure → coverage-build → coverage-test wave(s)
→ coverage-merge → coverage-normalize(llvm-cov export)
→ coverage-report(service action) → coverage-publish(service action)
```

Cover:

- stable queued claim order and idempotent duplicate `Resume`;
- one Task/one TestRun/one CoverageRun/no nested task;
- each appended continuation revalidates workspace generation, catalog revision, coverage profile, toolchain ID/version, instrumentation digest and retained boundary;
- assertion failure returns a failed TestRun but continues to `available` CoverageRun/Task succeeded;
- crash/test-timeout/failed-invocation missing profile yields exact `partial` reason;
- normal-exit missing profile, build/merge/parser/normalizer/renderer/artifact/transaction failures yield exact unavailable reason and no report;
- cancel/Task timeout/trust loss stops current process tree and does not invoke later adapter/action;
- duplicate completion is idempotent; conflicting completion fails closed;
- publish writes artifacts before one SQLite terminal transaction; broker sees events only after commit;
- raw profiles/profdata/export are removed only after terminal ownership is settled; cleanup never follows a replaced path.

- [ ] **Step 2: Run coordinator tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun -run 'Coordinator|Planner|Completion|Outcome|Boundary' -count=1
```

Expected: missing `coverageexec` package/API compile failures.

- [ ] **Step 3: Implement phased execution and completion projection**

`Resume` must:

1. reload Task/CoverageRun/TestRun and require matching queued identities;
2. re-inspect immutable workspace/profile/toolchain/catalog input;
3. allocate owner-only execution/profile/build roots;
4. prepare coverage build and attach toolset/boundary ownership;
5. call `TaskResumer.ResumeQueued` with only the first configure/build step plus continuation callbacks;
6. keep all native paths and handles in the live execution object.

After build, prepare the embedded TestRun and append bounded waves. After the last test wave, seal profiles and append merge/export. The export interpreter buffers stdout with `Limits.MaxInputBytes`, parses and normalizes from the captured bytes, then appends report/publish actions.

`PrepareCompletion` requires the supplied sink to implement `task.CoverageArtifactSink`, commits `Set` through `CommitBlob`, builds canonical finished TestRun/Run/Report, and returns:

```go
task.DomainCompletion{
    TestRun: &finishedTestRun,
    Coverage: &task.CoverageCompletion{
        Run: finishedCoverageRun,
        Expected: priorCoverageStatus,
        Report: reportOrNil,
    },
    Events: coverageEvents,
}
```

For cancellation/timeout/infrastructure failure it returns no report/public artifact. Map failed phase to the existing closed `coveragedomain.Reason` values; do not add a Protocol enum in this task.

- [ ] **Step 4: Run full coordinator/store/task race gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -run 'Coverage|Coordinator|Completion|Cancel|Restart' -count=1
```

Expected: PASS with no goroutine/handle/task-root leak on success, failure, cancel or panic recovery.

- [ ] **Step 5: Commit and push execution coordinator**

```powershell
git add apps/test-service/internal/coverageexec apps/test-service/internal/coveragerun
git diff --cached --check
git commit -m "feat: execute queued Windows coverage runs"
git push github master
git push origin master
```

---

### Task 8：Runtime production wiring、queued resume 与 Linux explicit unsupported path

**Files:**
- Modify: `apps/test-service/internal/runtime/runtime.go:40-152, 238-480, 494-633, 866-981`
- Modify: `apps/test-service/internal/runtime/coverage_backend.go:22-102`
- Create: `apps/test-service/internal/runtime/coverage_execution.go`
- Create: `apps/test-service/internal/runtime/coverage_execution_test.go`
- Modify: `apps/test-service/internal/runtime/coverage_backend_test.go`
- Modify: `apps/test-service/internal/runtime/runtime_test.go`
- Modify: `apps/test-service/internal/server/server_test.go`
- Modify: `docs/development.md`

**Interfaces:**
- Consumes: Task 7 `coverageexec.Coordinator` and existing Phase 7 `coveragecoord.QueuedBackend`.
- Produces:

```go
type coverageExecutor interface {
    Resume(context.Context, task.Task) (task.Task, error)
    FinishUnsupported(context.Context, task.Task) (task.Task, error)
    Close() error
}

type queuedCoverageBackend struct {
    queue coverageQueue
    repository coverageRepository
    resolve coverageStartResolver
    executor coverageExecutor
}
```

- [ ] **Step 1: Write failing trusted/untrusted/resume tests**

Add runtime tests proving:

- trusted Windows production Runtime constructs the queue backend and real executor from current Inspector/CMake/toolchain/TestCoordinator/Store/ArtifactStore/Task Manager;
- `StartCoverageRun` persists first, then asks executor to resume; a resume failure reloads and returns canonical state rather than inventing IDs;
- untrusted Runtime has no coverage backend, executor, LLVM pin, directory or process side effect;
- queued Windows coverage is resumed at startup in created-time/ID order after build/test recovery;
- running coverage was already terminalized by `RecoverInterrupted` and is never resumed;
- Linux constructs no native adapter, calls no compiler/collector, and terminalizes a queued request as `unavailable/instrumentation_failed` with Task `infrastructure_failed` and no report/artifact;
- shutdown closes executor before test/build/data guards and is idempotent.

- [ ] **Step 2: Run runtime/server tests and verify RED**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/runtime ./apps/test-service/internal/server -run 'Coverage|Queued|Trusted|Unsupported|Shutdown' -count=1
```

Expected: backend remains queued and tests fail because no executor is attached.

- [ ] **Step 3: Wire production dependencies and recovery order**

Extend `runtime.dependencies` with a constructor seam for `coverageexec.Coordinator`. Build the executor only after trusted build/test coordinators exist. Pass the exact `toolchain.Instance` selected during current Inspector revalidation; do not reconstruct tools from persisted public provenance.

Startup order is fixed:

```text
cleanup process leases → RecoverInterrupted → artifact orphan cleanup
→ resume queued builds → resume queued tests → resume queued coverage
```

Shutdown order is fixed:

```text
stop accepting coverage starts → cancel/close coverage executor
→ manager shutdown → test resources → broker/store/artifact/instance/data guards
```

Update `docs/development.md` to state that Windows clang-cl runs can now finish, Linux GCC/Clang execution remains the next batch, and GitHub/Gitee are development distribution only rather than product runtime dependencies.

- [ ] **Step 4: Run runtime full/race/vet and Linux cross gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/runtime ./apps/test-service/internal/server ./apps/test-service/internal/session -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/coverageexec -run 'Coverage|Queued|Shutdown|Cancel' -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' vet ./apps/test-service/internal/runtime ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragellvm
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; $env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test -c ./apps/test-service/internal/runtime
```

Expected: PASS; Linux output is compile/static evidence only.

- [ ] **Step 5: Commit and push Runtime wiring**

```powershell
git add apps/test-service/internal/runtime apps/test-service/internal/server docs/development.md
git diff --cached --check
git commit -m "feat: run production Windows coverage from runtime"
git push github master
git push origin master
```

---

### Task 9：真实 Windows clang-cl Protocol smoke、Code-OSS artifact 打开与 CI 门禁

**Files:**
- Create: `apps/code-oss-extension/test/fixtures/coverage/CMakeLists.txt`
- Create: `apps/code-oss-extension/test/fixtures/coverage/src/math.cpp`
- Create: `apps/code-oss-extension/test/fixtures/coverage/test/math_test.cpp`
- Create: `apps/code-oss-extension/test/coverage-service-smoke.test.ts`
- Modify: `apps/code-oss-extension/test/service-smoke.test.ts`
- Modify: `apps/code-oss-extension/package.json`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- Modify: `docs/superpowers/specs/2026-08-20-phase8-windows-llvm-coverage-execution-design.md`

**Interfaces:**
- Consumes: production Runtime/Protocol v1.4 and existing Extension `CoverageController`, artifact chunk/digest verifier and no-network viewer.
- Produces: `pnpm --filter code-oss-extension test:coverage-service-smoke` and CI artifact `.native-e2e/artifacts/windows/coverage-execution-report.json`.

- [ ] **Step 1: Write the real smoke and verify initial failure**

The fixture contains two deterministic test cases: one passes through `math.cpp` covered and uncovered branches; one assertion fails after executing instrumented code. The smoke must:

1. build the Service fixture with the repo-pinned Go toolchain;
2. start the real Windows Named Pipe Service in a trusted workspace;
3. discover the current workspace generation, coverage profile and catalog revision;
4. call Protocol v1.4 `coverage/runs/start` through the real TypeScript Client;
5. poll `coverage/runs/get` with a bounded timeout until `finished`;
6. require CoverageRun `available`, associated TestRun `failed`, non-zero coverage summary and three distinct artifact IDs;
7. fetch CoverageReport and all artifacts through protocol chunks, verify size/SHA-256/kind;
8. decode Coverage JSON v1, parse JUnit, and open the HTML through the real Extension coverage viewer adapter;
9. assert provenance `windows/x64/clang-cl/llvm-cov` with equal non-empty versions and no executable path;
10. assert no token, environment, absolute workspace/data/tool path or raw profile name appears in Protocol/report bytes.

Run before production fixture wiring:

```powershell
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' apps/code-oss-extension/node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --force
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' --test apps/code-oss-extension/dist/test/coverage-service-smoke.test.js
```

Expected: FAIL because the fixture/profile or production execution is not yet connected; missing LLVM must be an explicit `SKIP: verified clang-cl coverage toolset is unavailable`, never PASS.

- [ ] **Step 2: Add the CI script and artifact report**

Add package script:

```json
"test:coverage-service-smoke": "pnpm run build && node --test dist/test/coverage-service-smoke.test.js"
```

In `verify-windows`, after `prepare:cmake-bundle` and Service build, run the new script with `UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=clang-cl`. The script writes strict JSON containing platform, tool versions, run outcome, TestRun outcome, summary, three artifact digests and duration; it contains no native path. Upload it with `if-no-files-found: error`.

In `verify-linux`, run shared Go/TypeScript tests but do not run or upload a native coverage execution report. Keep existing Unix Socket service smoke as Linux runtime evidence only.

- [ ] **Step 3: Run fresh local Windows vertical-slice gates**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path (Get-Location) '.gocache-phase8'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/... -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path (Get-Location) '.gocache-phase8'); & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime -count=1
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' apps/code-oss-extension/node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --force
$env:COREPACK_HOME=(Join-Path (Get-Location) '.superpowers\runtime\corepack'); & (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd') pnpm --filter code-oss-extension test
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' --test apps/code-oss-extension/dist/test/coverage-service-smoke.test.js
git diff --check
```

Expected: all Go/Extension suites PASS; real Windows coverage smoke PASS when verified LLVM exists, otherwise emits one explicit SKIP and the CI Windows job remains responsible for required PASS.

- [ ] **Step 4: Run root generated/build verification and review the evidence boundary**

Run with pinned pnpm 11.4.0:

```powershell
$env:COREPACK_HOME=(Join-Path (Get-Location) '.superpowers\runtime\corepack'); & (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd') pnpm check:protocol-generated
$env:COREPACK_HOME=(Join-Path (Get-Location) '.superpowers\runtime\corepack'); & (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd') pnpm check:coverage-generated
$env:COREPACK_HOME=(Join-Path (Get-Location) '.superpowers\runtime\corepack'); & (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd') pnpm build
```

Document exact PASS/SKIP boundaries in `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`: Windows native PASS is required; Linux native coverage remains unimplemented; GitHub Actions results are evidence, not a product runtime dependency.

- [ ] **Step 5: Commit, push both remotes and verify remote equality**

```powershell
git add apps/code-oss-extension .github/workflows/foundation.yml docs
git diff --cached --check
git commit -m "test: verify Windows LLVM coverage execution"
git push github master
git push origin master
git rev-parse master
git ls-remote github refs/heads/master
git ls-remote origin refs/heads/master
```

Expected: all three hashes are identical and the tracked worktree is clean.

---

## Final Verification

- [ ] `git status --short --branch` shows clean `master`.
- [ ] Full Go suite passes with Go 1.26.6 and workspace-local `GOCACHE`.
- [ ] Affected `coverageexec`, `coveragellvm`, `task`, `taskstore`, `artifactstore`, `runtime` race suites pass.
- [ ] `go vet` passes for affected packages; Linux amd64 CGO-disabled cross compilation passes without claiming native coverage.
- [ ] Extension compiled suite and existing real Service smoke pass.
- [ ] Windows clang-cl coverage smoke produces a real finished run, failed TestRun, available CoverageRun, CoverageReport and three verified artifacts.
- [ ] Cancel, timeout, trust loss, restart, missing profile and every report failure point publish no partial report.
- [ ] Protocol/domain/artifacts contain no native path, executable, argv, environment, token or raw profile name.
- [ ] `check:protocol-generated`, `check:coverage-generated`, root build and `git diff --check` pass.
- [ ] GitHub and Gitee `master` point to the same final commit.
