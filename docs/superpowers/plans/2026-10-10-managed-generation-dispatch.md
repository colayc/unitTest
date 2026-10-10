# Managed Generation Dispatch Correction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 Phase 10 v1.6 受管生成只创建任务却未启动后台执行的缺陷，并以真实阶段入口证明交接。

**Architecture:** `StartManaged` 成功持久化后调用已有 `generationService.launch`，不新增调度器。复用 worker、预算、进程授权、幂等与取消/关闭机制；测试在已提交源码隔离副本上覆盖本轮精确文件，不编译暂停中的第二版修改。

**Tech Stack:** Go 1.26.0 模块、现有 SQLite taskstore、Go testing/channel/sync、Windows PowerShell；无新增依赖。

## Global Constraints

- 用户于 2026-10-10 回复“确认”，批准[书面设计](../specs/2026-10-10-managed-generation-dispatch-design.md)。此计划尚未执行。
- 工作区 `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`；分支 `codex/phase10-production-generation`；已提交源码基线 `6da036d5976de588949308f905ee85d5cdd722db`。
- 必须先成功持久化，再使用返回的真实 run ID 交接。
- 保持现有解析、基准验证、请求准备、请求字段检查及持久化前二次索引/目标核验，顺序不变。
- 响应仍投影 `coord.Start` 返回的快照，允许返回 `queued`。
- 已终结或等待确认的 run 重放不得重新执行生成阶段；允许产生立即退出的短暂 worker。
- RPC 调用结束不取消已交接的工作。
- 不添加自动重试、定时扫描、超时延长或隐式服务重启。
- 不修改 session、协议、coordinator、taskstore、`test_generation.go` 或其他产品文件，不新增 migration、UI、原生探针和 SDK。
- 第二版十一项需求继续暂停；冻结文件清单为 `.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256`，18 个摘要全部保留。
- 定点测试不得默认编译混有暂停中第二版修改的脏模块；不暂存、回滚、删除或 stash 这些修改。
- 初始验证不自动运行全量测试、广泛 race、UI、Linux/Windows native、hosted matrix 或打包。
- 原 1200 秒原生授权已经消耗；后续真实原生尝试需另行明确授权预算/停止规则。
- 本计划不授权推送、PR、合并、Release 或签名，不更新整体路线图为完成。
- 不承诺消除仍存在的 27–41 秒覆盖率明细耗时，不捆绑 checkpoint/CAS 静默退出的独立风险。

---

## File Map and Task Boundary

一个交接点及其回归构成一个独立可验收任务，不拆成互相依赖的空壳任务。

| 文件 | 责任与限制 |
| --- | --- |
| `apps/test-service/internal/runtime/managed_runtime_backend.go` | 唯一产品修改：成功 `coord.Start` 后调用已有启动器 |
| `apps/test-service/internal/runtime/managed_runtime_dispatch_test.go` | 新增私有受控阶段 fixture、启动/重放/拒绝/取消/关闭回归 |
| `apps/test-service/internal/runtime/managed_runtime_backend_test.go` | 仅在实际失败证明确有需要时适配现有启动用例的等待/收尾；不得重写组件测试 |

只读接口来源：[后台执行与生命周期](../../../apps/test-service/internal/runtime/test_generation.go)、[已有 worker fixture](../../../apps/test-service/internal/runtime/test_generation_test.go)、[受管 fixture](../../../apps/test-service/internal/runtime/managed_runtime_backend_test.go)、[索引 fixture](../../../apps/test-service/internal/runtime/managed_reads_test.go)、[取消 facade](../../../apps/test-service/internal/runtime/managed_runtime_reads.go)。它们不是额外修改许可。

执行后回到[原 W4 计划](2026-10-08-windows-cmake-coverage-compatibility.md)；不重做已完成的前置任务。本计划的 SDD 状态目录仅在选择执行方式后初始化为 `.superpowers/sdd/2026-10-10-managed-generation-dispatch/`，当前规划阶段不创建或访问该目录。

### Task 1: Durable managed start hands off to the existing worker

**Files:**

- Create/Test: `apps/test-service/internal/runtime/managed_runtime_dispatch_test.go`
- Modify: `apps/test-service/internal/runtime/managed_runtime_backend.go`，`StartManaged` 的 `coord.Start` 成功分支，基线行 189–200。
- Conditional Modify/Test: `apps/test-service/internal/runtime/managed_runtime_backend_test.go`，只允许直接受影响的启动测试确定性收尾。

**Interfaces:**

- Consumes: `(*ManagedRuntimeProvider).StartManaged(context.Context, string, generationv16.TestGenerationStartRequestV16) (generationv16.TestGenerationRunV16, error)`。
- Consumes: `(*generationService).launch(string)`、`(*generationService).Close()`、`(*ManagedRuntimeProvider).CancelManagedRun(context.Context, string, string) (generationv16.TestGenerationRunV16, error)`。
- Consumes: `GenerationDriver.RunStage(context.Context, testgendomain.Run, testgendomain.State, func(context.Context) error) (GenerationStageResult, error)`。
- Consumes: `managedBackendFixture(*testing.T) (*ManagedRuntimeProvider, *managedReadFixture, *managedBackendDriverFixture)` 和 `awaitingManagedRun(*testing.T, *ManagedRuntimeProvider, *managedReadFixture, string) testgendomain.Run`。
- Produces: 原 API/DTO 不变；新增文件内私有 `newManagedDispatchFixture(*testing.T, testgendomain.Scope) *managedDispatchFixture` 与五个完整回归入口。

- [ ] **Step 1: Prepare an isolated committed source and record protections**

只在开始执行任务时运行以下准备。若本任务已有隔离路径及完成记录，恢复同一路径/阶段，不重新导出或重跑已完成 RED/GREEN。

```powershell
$ErrorActionPreference = 'Stop'
$Workspace = 'C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest'
$CodeBase = '6da036d5976de588949308f905ee85d5cdd722db'
$Go = 'D:/Program Files/Go/bin/go.exe'
$Owned = @(
    'apps/test-service/internal/runtime/managed_runtime_backend.go',
    'apps/test-service/internal/runtime/managed_runtime_dispatch_test.go',
    'apps/test-service/internal/runtime/managed_runtime_backend_test.go'
)
Set-Location -LiteralPath $Workspace
if ((git branch --show-current).Trim() -ne 'codex/phase10-production-generation') { throw 'Unexpected branch' }
git rev-parse --git-dir
git rev-parse --git-common-dir
git rev-parse HEAD
if (@(git diff --cached --name-only).Count -ne 0) { throw 'Pre-existing staging' }
if (-not (Test-Path -LiteralPath $Go -PathType Leaf)) { throw 'Recorded Go executable unavailable' }
if (Test-Path -LiteralPath $Owned[1]) { throw 'New test path exists: recover task context first' }
$changes = @(git diff --name-only $CodeBase -- $Owned[0] $Owned[2])
if ($LASTEXITCODE -ne 0 -or $changes.Count) { throw 'Owned baseline changed: recover context first' }

function Assert-DispatchFrozen {
    $manifest = Join-Path $Workspace '.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256'
    $rows = @(Get-Content -LiteralPath $manifest)
    if ($rows.Count -ne 18) { throw 'Unexpected frozen manifest size' }
    foreach ($row in $rows) {
        if ($row -notmatch '^(.+) ([A-Fa-f0-9]{64})$') { throw 'Invalid frozen manifest entry' }
        $path = Join-Path $Workspace $Matches[1]
        $expected = $Matches[2]
        if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ne $expected) { throw ('Frozen file changed: ' + $path) }
    }
}
Assert-DispatchFrozen

$RuntimeRoot = Join-Path $Workspace '.superpowers/runtime'
foreach ($dir in @((Join-Path $Workspace '.superpowers'), $RuntimeRoot)) {
    $item = Get-Item -LiteralPath $dir
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Scratch parent must be a direct directory' }
}
$Scratch = Join-Path $RuntimeRoot ('managed-dispatch-' + [Guid]::NewGuid().ToString('N'))
if (Test-Path -LiteralPath $Scratch) { throw 'Scratch collision' }
$null = New-Item -ItemType Directory -Path $Scratch
$Archive = Join-Path $Scratch 'committed-service.tar'
$tree = @(git ls-tree -r $CodeBase -- apps/test-service)
if ($LASTEXITCODE -ne 0 -or -not $tree.Count) { throw 'Empty or invalid source tree' }
foreach ($entry in $tree) { if ($entry -notmatch '^100(644|755) blob ') { throw 'Linked/submodule source entry rejected' } }
git archive --format=tar -o $Archive $CodeBase apps/test-service
if ($LASTEXITCODE -ne 0) { throw 'Archive export failed' }
$members = @(tar -tf $Archive)
if ($LASTEXITCODE -ne 0 -or -not $members.Count) { throw 'Cannot inventory archive' }
foreach ($member in $members) {
    if ($member -notmatch '^apps(/|$)' -or $member -match '(^|/)\.\.(/|$)|[:\\]') { throw 'Unsafe archive member' }
    $destination = [IO.Path]::GetFullPath((Join-Path $Scratch $member))
    if (-not $destination.StartsWith(([IO.Path]::GetFullPath($Scratch) + [IO.Path]::DirectorySeparatorChar), [StringComparison]::OrdinalIgnoreCase)) { throw 'Archive path escapes scratch' }
}
tar -xf $Archive -C $Scratch
if ($LASTEXITCODE -ne 0) { throw 'Archive extraction failed' }
$Module = Join-Path $Scratch 'apps/test-service'
Get-FileHash -LiteralPath $Archive -Algorithm SHA256

function Sync-DispatchOverlay {
    foreach ($relative in $Owned) {
        $source = Join-Path $Workspace $relative
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw ('Missing owned overlay: ' + $relative) }
        if ((Get-Item -LiteralPath $source).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Linked overlay file rejected' }
        Copy-Item -LiteralPath $source -Destination (Join-Path $Scratch $relative) -Force
        $a = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
        $b = (Get-FileHash -LiteralPath (Join-Path $Scratch $relative) -Algorithm SHA256).Hash
        if ($a -ne $b) { throw 'Overlay hash mismatch' }
        [PSCustomObject]@{ Path = $relative; SHA256 = $a }
    }
    Assert-DispatchFrozen
}

function Invoke-DispatchTests([string]$Pattern) {
    $names = @('GOPROXY', 'GOSUMDB', 'GOWORK', 'GOFLAGS', 'GOTOOLCHAIN')
    $old = @{}
    foreach ($name in $names) { $old[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    $script:DispatchTestExitCode = $null
    Push-Location -LiteralPath $Module
    try {
        [Environment]::SetEnvironmentVariable('GOPROXY', 'off', 'Process')
        [Environment]::SetEnvironmentVariable('GOSUMDB', 'off', 'Process')
        [Environment]::SetEnvironmentVariable('GOWORK', 'off', 'Process')
        [Environment]::SetEnvironmentVariable('GOFLAGS', '-mod=readonly', 'Process')
        [Environment]::SetEnvironmentVariable('GOTOOLCHAIN', 'local', 'Process')
        & $Go test -v -count=1 -timeout=60s -run $Pattern ./internal/runtime
        $script:DispatchTestExitCode = $LASTEXITCODE
    } finally {
        foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $old[$name], 'Process') }
        Pop-Location
    }
}
```

记录真实 HEAD、隔离目录、archive/三个 overlay 摘要、实际命令/起止/退出码和环境恢复。环境/依赖缺失不是回归 RED，不自动联网安装或改依赖。此隔离导出不是新的 Git 分支/干净工作区，也不是原生服务构建。所有源码修改仍在原 linked worktree 用 `apply_patch` 完成。

- [ ] **Step 2: Add the controlled worker fixture and primary behavioral regression**

在新增 `_test.go` 内写入下列完整代码。测试私有包装复用已有 fixture 的非阶段方法；不修改共享 fixture。阶段不执行 C/C++ 工具：真实 coordinator/process-authorization gate 之后只受控完成 baseline，然后在 analyzing 等待取消。

```go
package runtime

import (
    "context"
    "errors"
    "reflect"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
    "unit-test-ide.local/test-service/internal/testgendomain"
)

type managedDispatchEntry struct {
    Run testgendomain.Run
    Next testgendomain.State
    Deadline time.Time
    Done <-chan struct{}
    Err error
}

type managedDispatchDriver struct {
    *generationDriverFixture
    provider *ManagedRuntimeProvider
    entered chan managedDispatchEntry
    releaseBaseline chan struct{}
    calls atomic.Int64
}

func (d *managedDispatchDriver) RunStage(ctx context.Context, run testgendomain.Run, next testgendomain.State, authorize func(context.Context) error) (GenerationStageResult, error) {
    d.calls.Add(1)
    entry := managedDispatchEntry{Next: next}
    deadline, ok := ctx.Deadline()
    entry.Deadline = deadline
    if !ok {
        entry.Err = errors.New("stage lacks deadline")
    } else if err := authorize(ctx); err != nil {
        entry.Err = err
    } else {
        stored, err := d.provider.coord.Get(ctx, run.ID)
        entry.Run, entry.Err = stored, err
        if err == nil {
            item, readErr := d.provider.store.Get(ctx, run.TaskID)
            entry.Err = readErr
            if readErr == nil && (item.ID != stored.TaskID || stored.ID != run.ID || stored.Revision != run.Revision || !reflect.DeepEqual(stored.Request, run.Request)) {
                entry.Err = errors.New("stage entered without exact durable run/task")
            }
        }
    }
    d.provider.mu.Lock()
    execution := d.provider.running[run.ID]
    if execution != nil { entry.Done = execution.done }
    d.provider.mu.Unlock()
    if entry.Done == nil && entry.Err == nil { entry.Err = errors.New("stage lacks registered worker") }
    d.entered <- entry
    if entry.Err != nil { return GenerationStageResult{}, entry.Err }
    if next == testgendomain.StateBaseline {
        select {
        case <-d.releaseBaseline:
            return GenerationStageResult{Next: next}, nil
        case <-ctx.Done():
            return GenerationStageResult{}, ctx.Err()
        }
    }
    <-ctx.Done()
    return GenerationStageResult{}, ctx.Err()
}

type managedDispatchFixture struct {
    provider *ManagedRuntimeProvider
    reads *managedReadFixture
    prepared *managedBackendDriverFixture
    worker *managedDispatchDriver
    owner string
    input generationv16.TestGenerationStartRequestV16
}

func newManagedDispatchFixture(t *testing.T, scope testgendomain.Scope) *managedDispatchFixture {
    t.Helper()
    provider, reads, prepared := managedBackendFixture(t)
    report := reads.index.ReportID
    f := &managedDispatchFixture{provider: provider, reads: reads, prepared: prepared, owner: strings.Repeat("6", 64)}
    f.input = generationv16.TestGenerationStartRequestV16{
        IdempotencyKey: strings.Repeat("1", 32), ProjectID: reads.index.ProjectID,
        WorkspaceGeneration: reads.index.WorkspaceGeneration, CoverageReportID: &report,
        Scope: generationv16.TestGenerationScopeV16(scope), Framework: generationv16.Auto,
        Budgets: generationv16.TestGenerationBudgetsV16{WallTimeMS: 60000, CandidateCount: 2, MemoryMiB: 64, Concurrency: 1},
    }
    switch scope {
    case testgendomain.ScopeCoverageGap:
        f.input.CoverageGapID = &reads.index.Gaps[0].ID
    case testgendomain.ScopeSymbol:
        f.input.FunctionID = &reads.index.Files[0].Functions[0].ID
    case testgendomain.ScopeFile:
        f.input.FileID = &reads.index.Files[0].ID
    default:
        t.Fatal("unsupported dispatch fixture scope")
    }
    prepared.start = f.prepare
    f.worker = &managedDispatchDriver{generationDriverFixture: &generationDriverFixture{}, provider: provider,
        entered: make(chan managedDispatchEntry, 16), releaseBaseline: make(chan struct{})}
    provider.driver = f.worker
    return f
}

func (f *managedDispatchFixture) prepare(_ context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
    hash := strings.Repeat("d", 64)
    scope := testgendomain.Scope(input.Scope)
    request := testgendomain.Request{
        SessionOwnerDigest: owner, IdempotencyKey: input.IdempotencyKey,
        ProjectID: input.ProjectID, WorkspaceGeneration: input.WorkspaceGeneration,
        Scope: scope, CoverageReportID: *input.CoverageReportID, Framework: testgendomain.Framework(input.Framework),
        Budgets: testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency},
        Goals: testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent},
        SourceDigest: target.SourceDigest, CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash,
        CMakeTargetDigest: hash, FrameworkBundleDigest: hash, AnalyzerBundleDigest: hash,
        BaselineReportDigest: hash, ProcessOwnerDigest: hash,
    }
    if scope == testgendomain.ScopeCoverageGap { request.ManagedGapID = target.GapID } else { request.ManagedTargetID = target.SelectionID(scope) }
    return request, nil
}

func waitManagedDispatchDone(t *testing.T, done <-chan struct{}) {
    t.Helper()
    select {
    case <-done:
    case <-time.After(5 * time.Second):
        t.Fatal("managed dispatch worker did not settle")
    }
}

func waitManagedDispatchWorkers(t *testing.T, provider *ManagedRuntimeProvider) {
    t.Helper()
    done := make(chan struct{})
    go func() { provider.wg.Wait(); close(done) }()
    waitManagedDispatchDone(t, done)
}

func waitManagedDispatchEntry(t *testing.T, f *managedDispatchFixture) managedDispatchEntry {
    t.Helper()
    select {
    case entry := <-f.worker.entered:
        if entry.Err != nil { t.Fatalf("actual stage entry failed: %v", entry.Err) }
        return entry
    case <-time.After(5 * time.Second):
        t.Fatalf("managed start persisted but never entered RunStage; entries=%d", f.worker.calls.Load())
        return managedDispatchEntry{}
    }
}

func cancelManagedDispatch(t *testing.T, f *managedDispatchFixture, runID string, done <-chan struct{}) {
    t.Helper()
    result, err := f.provider.CancelManagedRun(context.Background(), f.owner, runID)
    if err != nil || testgendomain.State(result.State) != testgendomain.StateCancelled { t.Fatalf("cancel state=%s err=%v", result.State, err) }
    waitManagedDispatchDone(t, done)
    f.provider.mu.Lock()
    remaining, pending := len(f.provider.running), len(f.provider.cancelPending)
    f.provider.mu.Unlock()
    if remaining != 0 || pending != 0 { t.Fatalf("worker registrations leaked: running=%d pending=%d", remaining, pending) }
}

func TestManagedRuntimeDispatchStartsAfterPersistence(t *testing.T) {
    for _, scope := range []testgendomain.Scope{testgendomain.ScopeCoverageGap, testgendomain.ScopeSymbol, testgendomain.ScopeFile} {
        t.Run(string(scope), func(t *testing.T) {
            f := newManagedDispatchFixture(t, scope)
            f.input.Goals = generationv16.TestGenerationGoalsV16{FunctionPercent: 20, LinePercent: 30, BranchPercent: 40}
            ctx, cancelRequest := context.WithCancel(context.Background())
            defer cancelRequest()
            started, err := f.provider.StartManaged(ctx, f.owner, f.input)
            if err != nil { t.Fatal(err) }
            first := waitManagedDispatchEntry(t, f)
            target, err := f.provider.ResolveManagedStart(context.Background(), f.owner, f.input)
            if err != nil { t.Fatal(err) }
            expected, err := f.prepare(context.Background(), f.owner, f.input, target)
            if err != nil { t.Fatal(err) }
            if first.Run.ID != started.RunID || first.Run.TaskID != started.TaskID || first.Next != testgendomain.StateBaseline || !reflect.DeepEqual(first.Run.Request, expected) {
                t.Fatal("stage did not bind exact started run/request")
            }
            if first.Run.State != testgendomain.StateQueued || !first.Deadline.After(time.Now()) || first.Deadline.After(first.Run.CreatedAt.Add(time.Duration(expected.Budgets.WallTimeMS)*time.Millisecond)) {
                t.Fatal("first stage lost durable queued checkpoint or bounded deadline")
            }
            cancelRequest()
            close(f.worker.releaseBaseline)
            second := waitManagedDispatchEntry(t, f)
            if second.Next != testgendomain.StateAnalyzing || second.Run.State != testgendomain.StateBaseline || second.Run.Revision <= first.Run.Revision {
                t.Fatal("released actual baseline did not commit progress independently of request context")
            }
            cancelManagedDispatch(t, f, started.RunID, second.Done)
        })
    }
}
```

测试内无 `t.Parallel`，`PrepareManaged` 计数只在调用线程改变；实际阶段观测使用 channel/atomic。现有 fixture 的 LIFO cleanup 已先 `base.Close` 再 `store.Close`；必须保留该顺序。

- [ ] **Step 3: Run only the primary gap regression and capture genuine RED**

```powershell
Sync-DispatchOverlay
Invoke-DispatchTests '^TestManagedRuntimeDispatchStartsAfterPersistence$/^coverage-gap$'
if ($DispatchTestExitCode -ne 1) { throw 'Expected behavioral RED exit1; inspect actual result' }
```

Expected: `coverage-gap` 在 5 秒阶段等待后失败，消息为 `managed start persisted but never entered RunStage; entries=0`。此时产品入口仍与基线一致。任何编译错误、依赖缺失、不同失败或意外通过都必须先解释，不计为 TDD RED，不自动重跑 native。

- [ ] **Step 4: Add the single production handoff and run primary GREEN**

只在 `managed_runtime_backend.go` 的成功分支增加这一行（完整上下文如下），其余准备、二次核验、返回 DTO 不变：

```go
current, err := p.generationService.coord.Start(ctx, request)
if err != nil {
    return generationv16.TestGenerationRunV16{}, err
}
p.generationService.launch(current.ID)
count := int64(current.CandidateCount)
```

```powershell
Sync-DispatchOverlay
Invoke-DispatchTests '^TestManagedRuntimeDispatchStartsAfterPersistence$'
if ($DispatchTestExitCode -ne 0) { throw 'Primary GREEN failed; retain output and diagnose before broadening scope' }
```

Expected: 三种 report-bound scope 均真正进入 baseline，释放后读取到 durable baseline revision，调用 context 取消不终止该 worker，显式取消最终释放登记。不能把 `StartManaged` 返回 `queued` 判为失败。

- [ ] **Step 5: Add complete replay/error/lifecycle guards without new product behavior**

将以下代码追加到同一新测试文件。等待确认 helper 只用于无阶段重执行分支，不作为生成成功证据。存储失败使用独立已关闭的临时 store 作为 coordinator 依赖；产品/provider 的健康门槛及原真实 store 不被关闭。恢复 coordinator 后同键能启动，证明被拒绝请求没有占用正常 store 的幂等键。

追加之前将新文件的 import block 替换为以下完整内容；不在 Step 2 提前引入只由此步骤使用的包，以免主要 RED 变成 unused-import 编译失败：

```go
import (
    "context"
    "errors"
    "path/filepath"
    "reflect"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
    "unit-test-ide.local/test-service/internal/task"
    "unit-test-ide.local/test-service/internal/taskstore"
    "unit-test-ide.local/test-service/internal/testgencoord"
    "unit-test-ide.local/test-service/internal/testgendomain"
)
```

```go
func TestManagedRuntimeDispatchActiveReplayDoesNotDuplicateStage(t *testing.T) {
    f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
    started, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
    if err != nil { t.Fatal(err) }
    entry := waitManagedDispatchEntry(t, f)
    replay, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
    if err != nil || replay.RunID != started.RunID || replay.TaskID != started.TaskID { t.Fatalf("replay identity changed: %v", err) }
    cancelManagedDispatch(t, f, started.RunID, entry.Done)
    waitManagedDispatchWorkers(t, f.provider)
    if got := f.worker.calls.Load(); got != 1 { t.Fatalf("active replay executed %d stages before baseline release", got) }
}

func TestManagedRuntimeDispatchStoppedReplayDoesNotExecute(t *testing.T) {
    for _, state := range []testgendomain.State{testgendomain.StateAwaitingConfirmation, testgendomain.StateCancelled} {
        t.Run(string(state), func(t *testing.T) {
            f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
            run := awaitingManagedRun(t, f.provider, f.reads, f.owner)
            if state == testgendomain.StateCancelled {
                var err error
                run, err = f.provider.coord.Cancel(context.Background(), run.TaskID)
                if err != nil { t.Fatal(err) }
            }
            replay, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
            if err != nil || replay.RunID != run.ID || replay.TaskID != run.TaskID || testgendomain.State(replay.State) != state { t.Fatalf("stopped replay state=%s err=%v", replay.State, err) }
            waitManagedDispatchWorkers(t, f.provider)
            if f.worker.calls.Load() != 0 { t.Fatal("stopped replay entered actual stage") }
        })
    }
}

func TestManagedRuntimeDispatchRejectedStartDoesNotExecuteOrReserveKey(t *testing.T) {
    prepareErr := errors.New("controlled preparation failure")
    cases := []struct {
        name string
        want error
        change func(*testing.T, *managedDispatchFixture)
    }{
        {"unknown-gap", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) { unknown := strings.Repeat("e", 32); f.input.CoverageGapID = &unknown }},
        {"unbound-report", task.ErrStorageUnavailable, func(_ *testing.T, f *managedDispatchFixture) { f.input.CoverageReportID = nil }},
        {"prepare-error", prepareErr, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (testgendomain.Request, error) { return testgendomain.Request{}, prepareErr }
        }},
        {"source-mismatch", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); r.SourceDigest = strings.Repeat("e", 64); return r, err }
        }},
        {"fresh-source-changed", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); f.reads.index.Files[0].SourceSHA256 = strings.Repeat("e", 64); return r, err }
        }},
        {"fresh-report-changed", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); f.reads.index.ReportID = strings.Repeat("e", 32); return r, err }
        }},
        {"owner-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); r.SessionOwnerDigest = strings.Repeat("7", 64); return r, err }
        }},
        {"budget-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); r.Budgets.WallTimeMS++; return r, err }
        }},
        {"goals-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
            f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) { r, err := f.prepare(ctx, owner, input, target); r.Goals.LinePercent = 1; return r, err }
        }},
        {"persist-failure", task.ErrStorageUnavailable, func(t *testing.T, f *managedDispatchFixture) {
            broken, err := taskstore.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
            if err != nil { t.Fatal(err) }
            if err := broken.Close(); err != nil { t.Fatal(err) }
            f.provider.coord = testgencoord.NewWithProcessVerifier(broken,
                func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) { return r.SnapshotIdentity(), nil },
                func(context.Context, task.Artifact) error { return nil },
                func(context.Context, string, string) error { return nil })
        }},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
            input, coord := f.input, f.provider.coord
            source, report := f.reads.index.Files[0].SourceSHA256, f.reads.index.ReportID
            tc.change(t, f)
            if _, err := f.provider.StartManaged(context.Background(), f.owner, f.input); !errors.Is(err, tc.want) { t.Fatalf("rejected start=%v want=%v", err, tc.want) }
            waitManagedDispatchWorkers(t, f.provider)
            if f.worker.calls.Load() != 0 { t.Fatal("rejected request entered stage") }
            f.input, f.provider.coord = input, coord
            f.reads.index.Files[0].SourceSHA256, f.reads.index.ReportID = source, report
            f.prepared.start = f.prepare
            started, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
            if err != nil { t.Fatalf("rejection reserved idempotency key: %v", err) }
            entry := waitManagedDispatchEntry(t, f)
            cancelManagedDispatch(t, f, started.RunID, entry.Done)
        })
    }
}

func TestManagedRuntimeDispatchCloseCancelsAndJoinsRegisteredWorker(t *testing.T) {
    f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
    if _, err := f.provider.StartManaged(context.Background(), f.owner, f.input); err != nil { t.Fatal(err) }
    entry := waitManagedDispatchEntry(t, f)
    closed := make(chan struct{})
    go func() { f.provider.Close(); close(closed) }()
    waitManagedDispatchDone(t, closed)
    waitManagedDispatchDone(t, entry.Done)
    f.provider.mu.Lock()
    remaining := len(f.provider.running)
    f.provider.mu.Unlock()
    if remaining != 0 || f.worker.calls.Load() != 1 { t.Fatal("Close failed to settle the registered stage") }
}
```

以上保障测试不要求全部在旧行为下失败：主要启动回归已经有独立 RED。没有修改产品关闭状态机；不测试 Start-after-Close 或并发 Start/Close 的新语义。测试中无真实候选、原生 receipt 或覆盖率增长声明。

- [ ] **Step 6: Run the one bounded integration selection and handle only demonstrated fixture impact**

先用项目已有 Go formatter 整理本轮新测试文件，不格式化无关文件；随后执行一条选择性验证。既有受管 component 测试也纳入，覆盖人工 waiting helper 不被误改。`TestManagedRuntimeDispatch` 前缀同时覆盖已通过的主入口，这是必要的单次任务整合，不再额外重复同一组合。

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w (Join-Path $Workspace $Owned[1])
if ($LASTEXITCODE -ne 0) { throw 'Owned test formatting failed' }
Sync-DispatchOverlay
Invoke-DispatchTests '^TestManaged(Runtime|Run|Start|Review)'
if ($DispatchTestExitCode -ne 0) { throw 'Bounded managed regression failed; retain exact failing test before any change' }
Assert-DispatchFrozen
git diff --check -- $Owned[0] $Owned[2]
if ($LASTEXITCODE -ne 0) { throw 'Owned source whitespace error' }
```

Expected: 五个新增测试入口及所列子用例、选择匹配的既有用例无失败；记录 `-v` 输出实际命中的测试/子用例、跳过和退出码，不把计划说明当 receipt。不得把 zero-tests/skip 当成功。若 Windows 条件跳过出现，逐项报告，不自动改 Linux 路径或补跑 native。

若确切的既有启动测试因新增 worker 与手工状态收尾冲突而失败，只能在声明的 `managed_runtime_backend_test.go` 做确定性 barrier/取消后等待；记录测试名、失败与最小修正，再重跑该失败项，最后一次必要的整合选择。不要因尚未观察到的风险提前改 fixture；现有 `awaitingManagedRun` 直接调用 coordinator，不会因这个入口修正自动启动。

若观察到的是现有“只验证准备/拒绝”的用例需要在成功启动后立即收尾，允许的最小适配是以下完整分支；`Close` 自身同步取消并等待，不能替换成 sleep。仅用于该 fixture 最后一次成功启动之后（后续只有拒绝断言或用例结束），不在同一已关闭 fixture 上再启动新任务，也不改成功条件或隐藏真实阶段失败：

```go
if _, err := provider.StartManaged(context.Background(), owner, input); err != nil {
    t.Fatal(err)
}
provider.Close()
```

若实际失败不能由这种现有生命周期收尾解释，报告具体失败，不自行发明更多 fixture/产品改动。该条件适配没有被本计划预先判定为必要。

若需要新增产品同步/关闭/CAS机制或扩大路径范围，停止并向控制器报告真实证据；不能以测试适配掩盖真实错误。

- [ ] **Step 7: Verify exact scope, commit locally, review and hand back to W4**

```powershell
Set-Location -LiteralPath $Workspace
Assert-DispatchFrozen
$tracked = @(git diff --name-only -- $Owned)
git diff -- $Owned[0] $Owned[2]
if (@(git diff --cached --name-only).Count) { throw 'Unexpected staging before owned commit' }
git add -- $Owned[0] $Owned[1]
if ($LASTEXITCODE -ne 0) { throw 'Exact staging failed' }
if ($tracked -contains $Owned[2]) {
    git add -- $Owned[2]
    if ($LASTEXITCODE -ne 0) { throw 'Approved fixture staging failed' }
}
$staged = @(git diff --cached --name-only)
if ($staged.Count -lt 2 -or $staged.Count -gt 3) { throw 'Unexpected staged count' }
foreach ($path in $staged) { if ($Owned -notcontains $path) { throw ('Out-of-scope staging: ' + $path) } }
git diff --cached --check
if ($LASTEXITCODE -ne 0) { throw 'Staged scope has whitespace errors' }
git diff --cached --stat
git commit -m 'fix: dispatch durable managed generation runs'
if ($LASTEXITCODE -ne 0) { throw 'Owned local commit failed' }
git show --stat --oneline HEAD
if (@(git diff --cached --name-only).Count) { throw 'Index not empty after owned commit' }
Assert-DispatchFrozen
```

提交前审查产品 diff 只有一处 `launch(current.ID)`，不顺带清理已有过时注释或修正其他风险。独立任务审阅采用用户选择的执行技能：先 spec compliance、再 code quality；两轮只读审阅依赖真实 RED/GREEN 输出和精确 diff，不自动补跑测试。若发现问题，只修正任务允许的路径并运行实际受影响项。

报告在本任务 SDD 目录中记录真实源码提交、base/overlay/archive 摘要、RED/GREEN/整合退出码、实际命中/跳过、冻结18状态与未通过门槛。一任务方案不再另设重复全分支审阅/全局 suite；原 W4 的后续原生/分支整体门槛保持独立。

控制器回到原 W4 ledger，把本修正作为已审阅的有界前置结果交接。不得因本任务通过就宣布 W4/Phase 10、可发布安装包或自动生成完整真实链路完成。下一项如需真实 native，先报告修复提交与新的服务构建必要性，并请求一次明确预算的授权；此计划到此停止。

## Plan Self-Review / Execution Handoff

执行之前核对：设计的启动顺序、精确身份、三个 scope、active/stopped replay、source/report/owner/budgets/goals/storage 失败、request context 独立、真实 progress、Cancel/Close、环境/资源恢复与暂停第二版隔离均落在 Task 1；不新增未审批生产边界。

计划完成后提供两种执行方式：1，子代理逐任务实施及两阶段审阅（推荐，沿用此前选择）；2，当前会话使用 executing-plans 实施。规划本轮不启动实施代理、不运行上述准备/测试/构建命令，不续用旧 native 授权。
