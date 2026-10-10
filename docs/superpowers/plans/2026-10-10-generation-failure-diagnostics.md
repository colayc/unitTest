# Generation Failure Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给已有 Phase 10 生成失败收口保存安全、持久、原子的阶段／环节／原因诊断，不修复尚未定位的生成根因。

**Architecture:** 领域层定义 TG1 闭集编码；协调器和 taskstore 在原检查点事务内写入现有 task 错误字段；runtime 按错误身份和既有边界生成诊断。公开协议、Run／GenerationRecord、事件载荷、成功／取消路径不变；测试在已提交服务基线加精确 overlays 的隔离副本上运行。

**Tech Stack:** Go 1.26.0、现有 SQLite taskstore、Go testing／channel、Windows PowerShell；不新增依赖。

## Global Constraints

- 用户已于 2026-10-10 确认[书面设计](../specs/2026-10-10-generation-failure-diagnostics-design.md)。本计划尚未执行。
- 工作区 `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`；分支 `codex/phase10-production-generation`；规划基线 `ed9c3264d4b74260dff4a04ff9315ba5f3baedc6`，不是整个工作区干净候选。
- `error_code` 的唯一新格式为 `TG1:<stage>:<operation>:<reason>`，ASCII、大小写敏感、最长 128 字节，解析必须恰好得到四段，不接受空段、额外分隔符或非枚举值。
- 本轮新诊断写入的 `error_message` 始终为空；不清洗或改写旧任务字段。
- 不在 `Run`、GenerationRecord、候选、请求、事件载荷中新增字段。不更改 v1.5／v1.6 schema、SDK、扩展界面或任何对外响应。
- 禁止持久化原始错误、路径、源码、命令行、工具输出、连接信息或凭据；禁止对异常对象整体 JSON 序列化。
- 错误分类采用 `errors.Is`／受限内部类型，不匹配 `err.Error()` 的词语。
- 第一次成功提交的终态及诊断不可覆盖。已终结的幂等重复不新增 revision 或事件，也不补写第二个原因。
- 若竞争执行推进了非终态 revision，过期诊断入口返回冲突，不把旧阶段原因贴到新检查点上；不得另起无 CAS 的 UPDATE。
- 保留原 `Fail`／`CheckpointGeneration` 调用接口，未携带诊断的旧调用保持原行为。
- 空错误码、迁移产生的已有 `stale-snapshot` 等旧值继续可读，不重新解释为 TG1，不回填历史记录。
- 检查点对 minimized case／preview 的既有安全约束也不得为了强行保存诊断而放宽；旧约束拒绝的失败终结仍须拒绝。
- 不修复 checkpoint／coord.Get／NextStage 静默退出，不增加调度、自动重试或超时延长。
- 18 项冻结摘要以 `.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256` 为准；第二版十一项需求继续暂停。
- 不 reset、stash、删除、回滚或顺带暂存暂停中的修改；禁止修改 migration、sqlite.go、migrations.go、协议／生成模型、UI、native probe、fixture CMake 或第三方工具。
- 只跑新增及直接受影响的定点测试，不自动跑全量、广泛 race、UI、Linux／Windows native、hosted 或打包。
- 原生授权已经消耗；不修改旧失败证据，不追溯补出旧原因。本计划不授权推送、PR、合并、Release 或签名，不更新路线图为完成。

---

## File Map and Review Boundaries

三项依次执行；每项有独立测试、提交和审阅边界。Task 1 的领域契约可独立接受或拒绝；Task 2 的持久化契约不依赖 worker；Task 3 接入真实 worker。不要把导出、记账或最终综合检查拆成独立空壳任务。

| 任务 | 产品文件 | 新增私有测试文件 |
| --- | --- | --- |
| 1 | `apps/test-service/internal/testgendomain/failure.go`（新增） | 同目录 `failure_test.go` |
| 2 | `apps/test-service/internal/taskstore/test_generation.go`；`apps/test-service/internal/testgencoord/coordinator.go` | 各目录 `test_generation_failure_test.go`、`failure_test.go` |
| 3 | `apps/test-service/internal/runtime/test_generation_failure.go`（新增）；同目录 `test_generation.go`、`production_generation_driver.go` | 同目录 `test_generation_failure_test.go`、`production_generation_failure_test.go` |

不修改共享测试 fixture。直接复用已存在的 `generationRunFixture`／`generationRequestFixture`、`coordRequest`、`generationDriverFixture`／`generationPublisherFixture`、`productionPipelineFixture`、`productionTargetResolverFixture`／`productionValidatorFixture`，并在新测试文件内嵌包装。完整签名以每项 Interfaces 和代码为准。

当前只编写计划，不创建新执行状态目录。选择执行方式后，才初始化本计划状态目录 `.superpowers/sdd/2026-10-10-generation-failure-diagnostics/`。完成本计划后将验收接口交还[原 W4 计划](2026-10-08-windows-cmake-coverage-compatibility.md)，不重做已完成的 dispatch、digest 或 trace 前置任务。

### Task 1: Closed diagnostic value and canonical codec

**Files:**

- Create: `apps/test-service/internal/testgendomain/failure.go`
- Create/Test: `apps/test-service/internal/testgendomain/failure_test.go`

**Interfaces:**

- Consumes: 现有 `testgendomain.ErrInvalid`，不依赖其他内部包。
- Produces: `FailureDiagnostic{Stage, Operation, Reason string}`；`(FailureDiagnostic).Valid() bool`；`(FailureDiagnostic).Encode() (string, error)`；`ParseFailureDiagnostic(string) (FailureDiagnostic, bool)`。
- Produces for controller: 已提交领域源码／测试、隔离模块路径和 RED／GREEN receipt；后续项不得重做本项。

- [ ] **Step 1: Prepare one isolated committed service and bounded test controller**

这些命令只在实际选择执行方式后运行。恢复任务时读自己的账本并复用既有隔离目录，不再创建第二份导出或重复已完成测试。下面包含完整准备／overlay／测试命令；所有产品编辑仍通过 apply_patch 在原 linked worktree 完成，导出只用于验证。

```powershell
$ErrorActionPreference = 'Stop'
$Workspace = 'C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest'
$CodeBase = 'ed9c3264d4b74260dff4a04ff9315ba5f3baedc6'
$Git = 'D:/Program Files (x86)/Tools/Git/Git/cmd/git.exe'
$Go = 'D:/Program Files/Go/bin/go.exe'
$GoFmt = 'D:/Program Files/Go/bin/gofmt.exe'
$Owned = @(
 'apps/test-service/internal/testgendomain/failure.go',
 'apps/test-service/internal/testgendomain/failure_test.go',
 'apps/test-service/internal/taskstore/test_generation.go',
 'apps/test-service/internal/taskstore/test_generation_failure_test.go',
 'apps/test-service/internal/testgencoord/coordinator.go',
 'apps/test-service/internal/testgencoord/failure_test.go',
 'apps/test-service/internal/runtime/test_generation_failure.go',
 'apps/test-service/internal/runtime/test_generation_failure_test.go',
 'apps/test-service/internal/runtime/production_generation_failure_test.go',
 'apps/test-service/internal/runtime/test_generation.go',
 'apps/test-service/internal/runtime/production_generation_driver.go'
)
Set-Location -LiteralPath $Workspace
if ((& $Git branch --show-current).Trim() -ne 'codex/phase10-production-generation') { throw 'Unexpected branch' }
& $Git rev-parse --git-dir
& $Git rev-parse --git-common-dir
& $Git rev-parse HEAD
if (@(& $Git diff --cached --name-only).Count) { throw 'Existing staging must be preserved' }
$existing = @('apps/test-service/internal/taskstore/test_generation.go','apps/test-service/internal/testgencoord/coordinator.go','apps/test-service/internal/runtime/test_generation.go','apps/test-service/internal/runtime/production_generation_driver.go')
$changed = @(& $Git diff --name-only $CodeBase -- $existing)
if ($LASTEXITCODE -ne 0 -or $changed.Count) { throw 'Owned source changed: recover context before editing' }
foreach ($relative in $Owned) {
 if ($relative -notin $existing -and (Test-Path -LiteralPath (Join-Path $Workspace $relative))) { throw 'Prospective path exists: recover context, do not overwrite' }
}
function Assert-FailureFrozen {
 $rows = @(Get-Content -LiteralPath (Join-Path $Workspace '.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256'))
 if ($rows.Count -ne 18) { throw 'Unexpected frozen count' }
 foreach ($row in $rows) {
  if ($row -notmatch '^(.+) ([A-Fa-f0-9]{64})$') { throw 'Invalid frozen row' }
  $path = Join-Path $Workspace $Matches[1]; $expected = $Matches[2]
  if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ne $expected) { throw ('Frozen changed: ' + $path) }
 }
}
Assert-FailureFrozen
$root = Join-Path $Workspace '.superpowers/runtime'
foreach ($dir in @((Join-Path $Workspace '.superpowers'), $root)) {
 $item = Get-Item -LiteralPath $dir
 if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Linked scratch parent' }
}
$Scratch = Join-Path $root ('generation-failure-' + [Guid]::NewGuid().ToString('N'))
if (Test-Path -LiteralPath $Scratch) { throw 'Scratch collision' }
New-Item -ItemType Directory -Path $Scratch | Out-Null
$tree = @(& $Git ls-tree -r $CodeBase -- apps/test-service)
if ($LASTEXITCODE -ne 0 -or -not $tree.Count) { throw 'Invalid source tree' }
foreach ($entry in $tree) { if ($entry -notmatch '^100(644|755) blob ') { throw 'Source link/submodule rejected' } }
$Archive = Join-Path $Scratch 'committed-service.tar'
& $Git archive --format=tar -o $Archive $CodeBase apps/test-service
if ($LASTEXITCODE -ne 0) { throw 'Archive export failed' }
$members = @(tar -tf $Archive)
if ($LASTEXITCODE -ne 0 -or -not $members.Count) { throw 'Archive inventory failed' }
foreach ($member in $members) {
 if ($member -notmatch '^apps(/|$)' -or $member -match '(^|/)\.\.(/|$)|[:\\]') { throw 'Unsafe archive member' }
 $target = [IO.Path]::GetFullPath((Join-Path $Scratch $member))
 if (-not $target.StartsWith(([IO.Path]::GetFullPath($Scratch) + [IO.Path]::DirectorySeparatorChar), [StringComparison]::OrdinalIgnoreCase)) { throw 'Escaping archive member' }
}
tar -xf $Archive -C $Scratch
if ($LASTEXITCODE -ne 0) { throw 'Archive extraction failed' }
$Module = Join-Path $Scratch 'apps/test-service'
Get-FileHash -LiteralPath $Archive -Algorithm SHA256
function Sync-FailureOverlay([string[]]$Required) {
 foreach ($relative in $Required) {
  if ($relative -notin $Owned -or -not (Test-Path -LiteralPath (Join-Path $Workspace $relative) -PathType Leaf)) { throw ('Missing/unowned deliverable: ' + $relative) }
 }
 foreach ($relative in $Owned) {
  $source = Join-Path $Workspace $relative
  if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { continue }
  if ((Get-Item -LiteralPath $source).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Linked overlay rejected' }
  Copy-Item -LiteralPath $source -Destination (Join-Path $Scratch $relative) -Force
  $hash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
  if ((Get-FileHash -LiteralPath (Join-Path $Scratch $relative) -Algorithm SHA256).Hash -ne $hash) { throw 'Overlay mismatch' }
  [pscustomobject]@{Path=$relative; SHA256=$hash}
 }
 Assert-FailureFrozen
}
function Invoke-FailureTests([string]$Package, [string]$Pattern) {
 if ($Package -notin @('./internal/testgendomain','./internal/taskstore','./internal/testgencoord','./internal/runtime')) { throw 'Out-of-scope package' }
 $PSNativeCommandUseErrorActionPreference = $false
 $names = @('GOPROXY','GOSUMDB','GOWORK','GOFLAGS','GOTOOLCHAIN'); $old = @{}
 foreach ($name in $names) { $old[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
 $script:FailureTestExitCode = $null
 $receipt = Join-Path $Scratch ('tests-' + [Guid]::NewGuid().ToString('N') + '.jsonl')
 Push-Location -LiteralPath $Module
 try {
  [Environment]::SetEnvironmentVariable('GOPROXY','off','Process')
  [Environment]::SetEnvironmentVariable('GOSUMDB','off','Process')
  [Environment]::SetEnvironmentVariable('GOWORK','off','Process')
  [Environment]::SetEnvironmentVariable('GOFLAGS','-mod=readonly','Process')
  [Environment]::SetEnvironmentVariable('GOTOOLCHAIN','local','Process')
  & $Go test -json -count=1 -timeout=60s -run $Pattern $Package | Tee-Object -FilePath $receipt
  $script:FailureTestExitCode = $LASTEXITCODE
 } finally {
  foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name,$old[$name],'Process') }
  Pop-Location
 }
 [pscustomobject]@{Receipt=$receipt; Exit=$script:FailureTestExitCode; EnvironmentRestored=$true}
 if (Test-Path -LiteralPath $receipt) { Get-FileHash -LiteralPath $receipt -Algorithm SHA256 }
}
function Commit-FailureTask([string[]]$Paths,[string]$Message) {
 Assert-FailureFrozen
 if (-not $Paths.Count -or @($Paths | Where-Object { $_ -notin $Owned }).Count) { throw 'Invalid commit scope' }
 if (@(& $Git diff --cached --name-only).Count) { throw 'Pre-existing index must be preserved' }
 $parent=(& $Git rev-parse HEAD).Trim()
 & $Git add -- $Paths
 if ($LASTEXITCODE -ne 0) { throw 'Exact stage failed' }
 $actual=@(& $Git diff --cached --name-only | Sort-Object)
 $expected=@($Paths | Sort-Object)
 if ($actual.Count -ne $expected.Count -or @(Compare-Object $expected $actual).Count) { throw 'Unexpected staged paths' }
 & $Git diff --cached --check
 if ($LASTEXITCODE -ne 0) { throw 'Cached whitespace check failed' }
 & $Git commit -m $Message
 if ($LASTEXITCODE -ne 0) { throw 'Commit failed' }
 $committed=@(& $Git diff-tree --no-commit-id --name-only -r HEAD | Sort-Object)
 if ((& $Git rev-parse 'HEAD^').Trim() -ne $parent -or $committed.Count -ne $expected.Count -or @(Compare-Object $expected $committed).Count) { throw 'Commit scope/parent mismatch' }
 if (@(& $Git diff --cached --name-only).Count) { throw 'Postcommit index not empty' }
 Assert-FailureFrozen
 & $Git log -1 --format='%H %s'
}
```

记录导出与 overlay 摘要、命令／退出码、完整 JSON receipt 和环境恢复。依赖／工具缺失不是回归 RED，不自动下载。controller 给后续任务传递 `$Scratch`／`$Module` 和已完成摘要；全量测试不在本控制器中。

- [ ] **Step 2: Add the complete codec contract tests**

写入新 `failure_test.go`：

```go
package testgendomain

import (
 "strings"
 "testing"
)

func TestFailureDiagnosticCodecClosedValues(t *testing.T) {
 stages := strings.Fields("baseline analyzing solving rendering validating minimizing awaiting_confirmation unknown")
 operations := strings.Fields("ledger reserve_budget stage_run stage_context transition preview_set authoritative_set publish_plan preview_diff commit_budget finalize_review resume resolve_target authorize_process pipeline_generate validate_candidates minimize_candidates unknown")
 reasons := strings.Fields("timed_out cancelled budget_exceeded budget_invalid stale_snapshot invalid_argument storage_unavailable unavailable conflict invalid_transition invalid_preview unknown")
 for _, stage := range stages {
  for _, operation := range operations {
   for _, reason := range reasons {
    d := FailureDiagnostic{stage, operation, reason}
    raw, err := d.Encode()
    if err != nil || raw != "TG1:"+stage+":"+operation+":"+reason || len(raw) > 128 { t.Fatal("canonical closed value rejected") }
    got, ok := ParseFailureDiagnostic(raw)
    if !ok || got != d { t.Fatal("round trip changed value") }
   }
  }
 }
}

func TestFailureDiagnosticRejectsFreeTextAndLegacy(t *testing.T) {
 for _, d := range []FailureDiagnostic{
  {"", "stage_run", "unknown"}, {"failed", "stage_run", "unknown"},
  {"baseline", "FAKE_SECRET_PATH_MARKER", "unknown"},
  {"baseline", "stage_run", strings.Repeat("x", 129)},
  {"baseline", "stage_run", "UNKNOWN"}, {"baseline", "stage_run", "原因"},
 } {
  if d.Valid() { t.Fatal("invalid value considered valid") }
  if _, err := d.Encode(); err == nil { t.Fatal("free text encoded") }
 }
 for _, raw := range []string{
  "", "stale-snapshot", "TG2:baseline:stage_run:unknown", "TG1::stage_run:unknown",
  "TG1:baseline:stage_run:unknown:extra", "TG1:baseline:stage_run:FAKE_SECRET_PATH_MARKER",
  "TG1:failed:stage_run:unknown", "tg1:baseline:stage_run:unknown", strings.Repeat("x", 129),
 } {
  got, ok := ParseFailureDiagnostic(raw)
  if ok || got != (FailureDiagnostic{}) { t.Fatal("invalid/legacy value parsed or echoed") }
 }
}
```

- [ ] **Step 3: Verify RED, then add the exact closed codec**

Run：`Sync-FailureOverlay @('apps/test-service/internal/testgendomain/failure_test.go')`，再 `Invoke-FailureTests './internal/testgendomain' '^TestFailureDiagnostic'`。预计新增 API 尚不存在而编译失败；这是契约缺失 RED，不冒充此次 native 缺陷的行为 RED（后者在 Task 3）。

在新 `failure.go` 写入：

```go
package testgendomain

import "strings"

type FailureDiagnostic struct { Stage, Operation, Reason string }

func (d FailureDiagnostic) Valid() bool {
 switch d.Stage {
 case "baseline", "analyzing", "solving", "rendering", "validating", "minimizing", "awaiting_confirmation", "unknown":
 default: return false
 }
 switch d.Operation {
 case "ledger", "reserve_budget", "stage_run", "stage_context", "transition", "preview_set", "authoritative_set", "publish_plan", "preview_diff", "commit_budget", "finalize_review", "resume", "resolve_target", "authorize_process", "pipeline_generate", "validate_candidates", "minimize_candidates", "unknown":
 default: return false
 }
 switch d.Reason {
 case "timed_out", "cancelled", "budget_exceeded", "budget_invalid", "stale_snapshot", "invalid_argument", "storage_unavailable", "unavailable", "conflict", "invalid_transition", "invalid_preview", "unknown":
  return true
 default: return false
 }
}

func (d FailureDiagnostic) Encode() (string, error) {
 if !d.Valid() { return "", ErrInvalid }
 raw := "TG1:" + d.Stage + ":" + d.Operation + ":" + d.Reason
 if len(raw) > 128 { return "", ErrInvalid }
 return raw, nil
}

func ParseFailureDiagnostic(raw string) (FailureDiagnostic, bool) {
 if len(raw) > 128 { return FailureDiagnostic{}, false }
 parts := strings.Split(raw, ":")
 if len(parts) != 4 || parts[0] != "TG1" { return FailureDiagnostic{}, false }
 d := FailureDiagnostic{parts[1], parts[2], parts[3]}
 if !d.Valid() { return FailureDiagnostic{}, false }
 return d, true
}
```

- [ ] **Step 4: Verify GREEN and commit only the two owned files**

按以下顺序先格式化，再同步／运行 GREEN，最后暂存提交。预计 exit 0，两个入口及完整枚举组合实际通过；读完整 receipt，不从截断输出计数。只读自查领域无越层 import／原始 error 文本。

```powershell
& $GoFmt -w (Join-Path $Workspace 'apps/test-service/internal/testgendomain/failure.go') (Join-Path $Workspace 'apps/test-service/internal/testgendomain/failure_test.go')
Sync-FailureOverlay @('apps/test-service/internal/testgendomain/failure.go','apps/test-service/internal/testgendomain/failure_test.go')
Invoke-FailureTests './internal/testgendomain' '^TestFailureDiagnostic'
if ($FailureTestExitCode -ne 0) { throw 'Codec GREEN failed' }
Assert-FailureFrozen
Commit-FailureTask @('apps/test-service/internal/testgendomain/failure.go','apps/test-service/internal/testgendomain/failure_test.go') 'feat: define closed generation failure diagnostics'
```

提交前检查预期两文件 scope，成功后提供真实提交／父提交／最终源文件摘要和 receipt。代码格式调整在 GREEN 前完成；若 Step 4 最初运行 GREEN 后又改变实质代码，需要针对受影响测试重新验证，不复用旧 GREEN。

### Task 2: Atomic failure persistence with observed revision and legacy compatibility

**Files:**

- Modify: `apps/test-service/internal/taskstore/test_generation.go`，`CheckpointGeneration`（基线行 252–400）的入口和 task UPDATE；复用单一事务。
- Modify: `apps/test-service/internal/testgencoord/coordinator.go`，`Fail`／`terminalize` 邻近（基线行 152–178），新增独立诊断入口，不改原方法。
- Create/Test: `apps/test-service/internal/taskstore/test_generation_failure_test.go`
- Create/Test: `apps/test-service/internal/testgencoord/failure_test.go`

**Interfaces:**

- Consumes: Task 1 的 `testgendomain.FailureDiagnostic.Valid/Encode`；现有 `Store.GetGenerationByTask/Get/ReplayGenerationEvents`、`generationRunFixture()`、`generationRequestFixture()`、`openTestStore(*testing.T)`、`coordRequest()`。
- Produces: `(*Store).CheckpointGenerationWithFailure(context.Context, int64, testgendomain.Run, testgendomain.FailureDiagnostic) (testgendomain.Run, error)`，诊断分支不接收新增候选／产物。
- Produces: `(*Coordinator).FailWithDiagnostic(context.Context, string, string, int64, testgendomain.FailureDiagnostic) (testgendomain.Run, error)`，两个 string 顺序为 runID、taskID，int64 是观察到的 revision。
- Produces: 只有合法失败转换才写 TG1；task/message/event 原子性、历史值、幂等终态、过期非终态拒绝和存储安全约束的真实 receipts。

- [ ] **Step 1: Add atomic/rollback/reopen/storage-boundary tests**

在新 taskstore 测试文件写入完整代码（`Store.db` 只用于临时测试库的故障注入，不改生产接口）：

```go
package taskstore

import (
 "context"
 "errors"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "unit-test-ide.local/test-service/internal/task"
 "unit-test-ide.local/test-service/internal/testgendomain"
)

func diagnosticFailedNext(r testgendomain.Run) testgendomain.Run {
 next := testgendomain.CloneRun(r)
 next.State = testgendomain.StateFailed; next.Revision++
 now := time.Now().UTC(); next.FinishedAt = &now
 return next
}

func diagnosticBaseline(t *testing.T, s *Store) testgendomain.Run {
 t.Helper()
 r, err := s.CreateGeneration(context.Background(), generationRunFixture())
 if err != nil { t.Fatal(err) }
 next := r; next.State = testgendomain.StateBaseline; next.Revision++
 r, err = s.CheckpointGeneration(context.Background(), r.Revision, next, nil, nil)
 if err != nil { t.Fatal(err) }
 return r
}

func TestGenerationFailureStoreAtomicRestart(t *testing.T) {
 ctx := context.Background(); path := filepath.Join(t.TempDir(), "failure.sqlite")
 s, err := Open(path); if err != nil { t.Fatal(err) }
 defer func() { if s!=nil { _ = s.Close() } }()
 r := diagnosticBaseline(t, s)
 d := testgendomain.FailureDiagnostic{"analyzing", "resolve_target", "stale_snapshot"}
 failed, err := s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d)
 if err != nil || failed.State != testgendomain.StateFailed { t.Fatal("valid failure did not commit") }
 if err := s.Close(); err != nil { t.Fatal(err) }
 s, err = Open(path); if err != nil { t.Fatal(err) }
 row, err := s.Get(ctx, r.TaskID)
 if err != nil || row.ErrorCode != "TG1:analyzing:resolve_target:stale_snapshot" || row.ErrorMessage != "" || row.Outcome != task.OutcomeInfrastructureFailed || row.ActiveStep != "failed" { t.Fatal("reopened task diagnosis differs") }
 run, err := s.GetGeneration(ctx, r.ID)
 if err != nil || run.Revision != r.Revision+1 || run.State != testgendomain.StateFailed { t.Fatal("reopened run differs") }
 events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
 if err != nil || len(events) != 5 || events[4].Type != task.EventTaskFinished || row.LastSequence != 5 { t.Fatal("terminal journal differs") }
 if _, err := s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); !errors.Is(err, task.ErrConflict) { t.Fatal("stale storage writer accepted") }
}

func TestGenerationFailureStoreRollsBackWholeTransaction(t *testing.T) {
 ctx := context.Background(); s := openTestStore(t); r := diagnosticBaseline(t, s)
 _, err := s.db.Exec(`CREATE TRIGGER diagnostic_finish_fault BEFORE INSERT ON test_generation_events WHEN NEW.event_type='task.finished' BEGIN SELECT RAISE(ABORT,'diagnostic-fixture-fault'); END`)
 if err != nil { t.Fatal(err) }
 d := testgendomain.FailureDiagnostic{"analyzing", "stage_run", "unknown"}
 if _, err = s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); err == nil { t.Fatal("injected transaction fault ignored") }
 run, err := s.GetGeneration(ctx, r.ID)
 if err != nil || run.State != r.State || run.Revision != r.Revision || run.LastSequence != r.LastSequence { t.Fatal("run partially committed") }
 row, err := s.Get(ctx, r.TaskID)
 if err != nil || row.ErrorCode != "" || row.ErrorMessage != "" || row.FinishedAt != nil || row.LastSequence != r.LastSequence { t.Fatal("task partially committed") }
 events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
 if err != nil || len(events) != 3 { t.Fatal("private journal partially committed") }
 if _, err = s.db.Exec(`DROP TRIGGER diagnostic_finish_fault`); err != nil { t.Fatal(err) }
 if _, err = s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); err != nil { t.Fatal("valid retry after fixture fault rejected") }
}

func TestGenerationFailureStoreInvalidAndLegacy(t *testing.T) {
 for _, legacy := range []string{"", "stale-snapshot"} {
  t.Run("legacy-"+legacy, func(t *testing.T) {
   s := openTestStore(t); r := diagnosticBaseline(t, s); ctx := context.Background()
   if _, err := s.db.Exec(`UPDATE tasks SET error_code=?,error_message='legacy-fixture' WHERE task_id=?`, legacy, r.TaskID); err != nil { t.Fatal(err) }
   if _, err := s.CheckpointGeneration(ctx, r.Revision, diagnosticFailedNext(r), nil, nil); err != nil { t.Fatal(err) }
   row, err := s.Get(ctx, r.TaskID)
   if err != nil || row.ErrorCode != legacy || row.ErrorMessage != "legacy-fixture" { t.Fatal("old interface rewrote legacy fields") }
  })
 }
 for _, tc := range []struct{name string; nextState testgendomain.State; d testgendomain.FailureDiagnostic}{
  {"invalid-value", testgendomain.StateFailed, testgendomain.FailureDiagnostic{"analyzing", "FAKE_SECRET_PATH_MARKER", "unknown"}},
  {"nonfailed", testgendomain.StateAnalyzing, testgendomain.FailureDiagnostic{"analyzing", "stage_run", "unknown"}},
 } {
  t.Run(tc.name, func(t *testing.T) {
   s := openTestStore(t); r := diagnosticBaseline(t, s); next := diagnosticFailedNext(r)
   next.State = tc.nextState
   if next.State != testgendomain.StateFailed { next.FinishedAt = nil }
   if _, err := s.CheckpointGenerationWithFailure(context.Background(), r.Revision, next, tc.d); !errors.Is(err, task.ErrInvalidArgument) { t.Fatal("invalid diagnosis write accepted") }
   row, err := s.Get(context.Background(), r.TaskID)
   if err != nil || row.LastSequence != r.LastSequence || row.ErrorCode != "" { t.Fatal("invalid write changed task") }
  })
 }
}

func TestGenerationFailureStorePreservesMinimizedConstraint(t *testing.T) {
 s := openTestStore(t); ctx := context.Background(); original := generationRunFixture()
 original.Record = testgendomain.NewGenerationRecord(original.Request)
 r, err := s.CreateGeneration(ctx, original); if err != nil { t.Fatal(err) }
 for _, state := range []testgendomain.State{testgendomain.StateBaseline,testgendomain.StateAnalyzing,testgendomain.StateSolving,testgendomain.StateRendering} {
  next := r; next.State = state; next.Revision++
  r, err = s.CheckpointGeneration(ctx,r.Revision,next,nil,nil); if err != nil { t.Fatal(err) }
 }
 a := task.Artifact{ID:strings.Repeat("4",32),TaskID:r.TaskID,Kind:"test-generation-source",RelativePath:"tasks/"+r.TaskID+"/"+strings.Repeat("4",32)+".source",MIMEType:"application/octet-stream",Size:12,SHA256:strings.Repeat("e",64),CreatedAt:r.CreatedAt}
 c := testgendomain.Candidate{CaseID:strings.Repeat("5",32),Kind:testgendomain.KindVerified,TargetSymbol:"fn:classify",Assertions:[]testgendomain.Assertion{{Kind:testgendomain.AssertionIndependentOracle,EvidenceDigest:strings.Repeat("6",64)}},StagedSourceArtifact:testgendomain.ArtifactRef{ID:a.ID,Digest:a.SHA256},CodeDigest:strings.Repeat("7",64),PlannedEdits:[]testgendomain.PlannedEdit{{Path:"tests/classify.c",Operation:testgendomain.EditCreate,AfterDigest:strings.Repeat("8",64)}}}
 next := testgendomain.CloneRun(r); next.State=testgendomain.StateValidating; next.Revision++; next.CandidateCount=1; next.ArtifactDigests=[]testgendomain.ArtifactRef{{ID:a.ID,Digest:a.SHA256}}
 r, err = s.CheckpointGeneration(ctx,r.Revision,next,[]testgendomain.Candidate{c},[]task.Artifact{a}); if err != nil { t.Fatal(err) }
 for _, state := range []testgendomain.State{testgendomain.StateMinimizing,testgendomain.StateAwaitingConfirmation} {
  next = testgendomain.CloneRun(r); next.State=state; next.Revision++
  if state==testgendomain.StateAwaitingConfirmation { next.Record.MinimizedCaseIDs=[]string{c.CaseID} }
  r,err=s.CheckpointGeneration(ctx,r.Revision,next,nil,nil);if err != nil { t.Fatal(err) }
 }
 d:=testgendomain.FailureDiagnostic{"awaiting_confirmation","finalize_review","unknown"}
 if _,err=s.CheckpointGenerationWithFailure(ctx,r.Revision,diagnosticFailedNext(r),d);!errors.Is(err,task.ErrInvalidArgument) { t.Fatal("minimized-record safety weakened") }
 after,err:=s.GetGeneration(ctx,r.ID)
 if err != nil || after.Revision!=r.Revision || after.State!=r.State { t.Fatal("rejected terminalization changed run") }
 row,err:=s.Get(ctx,r.TaskID);if err != nil || row.ErrorCode!="" { t.Fatal("rejected terminalization wrote diagnosis") }
}
```

- [ ] **Step 2: Add coordinator identity/CAS/idempotency/concurrency tests**

新 coordinator `failure_test.go`：

```go
package testgencoord

import (
 "context"
 "errors"
 "path/filepath"
 "testing"

 "unit-test-ide.local/test-service/internal/task"
 "unit-test-ide.local/test-service/internal/taskstore"
 "unit-test-ide.local/test-service/internal/testgendomain"
)

func diagnosticCoordinator(t *testing.T) (*Coordinator,*taskstore.Store,testgendomain.Run) {
 t.Helper(); s,err:=taskstore.Open(filepath.Join(t.TempDir(),"diagnostic.sqlite"));if err!=nil { t.Fatal(err) }
 t.Cleanup(func(){_ = s.Close()})
 c:=New(s,func(_ context.Context,r testgendomain.Request)(testgendomain.SnapshotIdentity,error){return r.SnapshotIdentity(),nil})
 c.verifyProcess=func(context.Context,string,string)error{return nil}
 r,err:=c.Start(context.Background(),coordRequest());if err!=nil { t.Fatal(err) }
 return c,s,r
}

func TestFailureDiagnosticCoordinatorCASAndIdentity(t *testing.T) {
 c,s,r:=diagnosticCoordinator(t);ctx:=context.Background();d:=testgendomain.FailureDiagnostic{"baseline","stage_run","unknown"}
 if _,err:=c.FailWithDiagnostic(ctx,r.TaskID,r.TaskID,r.Revision,d);!errors.Is(err,task.ErrConflict){t.Fatal("wrong run identity accepted")}
 advanced,err:=c.Checkpoint(ctx,r.ID,r.Revision,testgendomain.StateBaseline,nil,nil);if err!=nil{t.Fatal(err)}
 if _,err=c.FailWithDiagnostic(ctx,r.ID,r.TaskID,r.Revision,d);!errors.Is(err,task.ErrConflict){t.Fatal("stale nonterminal revision accepted")}
 row,err:=s.Get(ctx,r.TaskID);if err!=nil||row.ErrorCode!=""{t.Fatal("stale diagnosis written")}
 failed,err:=c.FailWithDiagnostic(ctx,r.ID,r.TaskID,advanced.Revision,d);if err!=nil||failed.State!=testgendomain.StateFailed{t.Fatal("valid failure rejected")}
 other:=testgendomain.FailureDiagnostic{"baseline","stage_run","conflict"}
 replay,err:=c.FailWithDiagnostic(ctx,r.ID,r.TaskID,r.Revision,other)
 if err!=nil||replay.Revision!=failed.Revision||replay.LastSequence!=failed.LastSequence{t.Fatal("terminal replay wrote progress")}
 row,err=s.Get(ctx,r.TaskID);if err!=nil||row.ErrorCode!="TG1:baseline:stage_run:unknown"{t.Fatal("first diagnosis overwritten")}
}

func TestFailureDiagnosticCoordinatorKeepsExistingTerminals(t *testing.T) {
 for _, state:=range []testgendomain.State{testgendomain.StateAccepted,testgendomain.StateRejected,testgendomain.StateCancelled,testgendomain.StateFailed}{
  t.Run(string(state),func(t *testing.T){
   c,s,r:=diagnosticCoordinator(t);ctx:=context.Background();var terminal testgendomain.Run;var err error
   switch state {
   case testgendomain.StateCancelled:terminal,err=c.Cancel(ctx,r.TaskID)
   case testgendomain.StateFailed:terminal,err=c.Fail(ctx,r.TaskID)
   default:
    for _,stage:=range []testgendomain.State{testgendomain.StateBaseline,testgendomain.StateAnalyzing,testgendomain.StateSolving,testgendomain.StateRendering,testgendomain.StateValidating}{
     r,err=c.Checkpoint(ctx,r.ID,r.Revision,stage,nil,nil);if err!=nil{t.Fatal(err)}
    }
    if state==testgendomain.StateAccepted {
     for _,stage:=range []testgendomain.State{testgendomain.StateMinimizing,testgendomain.StateAwaitingConfirmation}{r,err=c.Checkpoint(ctx,r.ID,r.Revision,stage,nil,nil);if err!=nil{t.Fatal(err)}}
    }
    terminal,err=c.Checkpoint(ctx,r.ID,r.Revision,state,nil,nil)
   }
   if err!=nil{t.Fatal(err)}
   d:=testgendomain.FailureDiagnostic{"unknown","resume","unknown"}
   again,err:=c.FailWithDiagnostic(ctx,r.ID,r.TaskID,r.Revision,d)
   if err!=nil||again.State!=terminal.State||again.Revision!=terminal.Revision||again.LastSequence!=terminal.LastSequence{t.Fatal("existing terminal overwritten")}
   row,err:=s.Get(ctx,r.TaskID);if err!=nil||row.ErrorCode!=""{t.Fatal("historic terminal backfilled")}
  })
 }
}

func TestFailureDiagnosticCoordinatorConcurrentFirstCommit(t *testing.T) {
 c,s,r:=diagnosticCoordinator(t);gate:=make(chan struct{});results:=make(chan error,2)
 for _,reason:=range []string{"unknown","conflict"}{
  go func(reason string){<-gate;_,err:=c.FailWithDiagnostic(context.Background(),r.ID,r.TaskID,r.Revision,testgendomain.FailureDiagnostic{"baseline","stage_run",reason});results<-err}(reason)
 }
 close(gate)
 for i:=0;i<2;i++{if err:=<-results;err!=nil&&!errors.Is(err,task.ErrConflict){t.Fatal("unexpected concurrent error")}}
 after,err:=s.GetGeneration(context.Background(),r.ID)
 if err!=nil||after.Revision!=2||after.LastSequence!=3||after.State!=testgendomain.StateFailed{t.Fatal("more than one terminal commit")}
 row,err:=s.Get(context.Background(),r.TaskID)
 if err!=nil||(row.ErrorCode!="TG1:baseline:stage_run:unknown"&&row.ErrorCode!="TG1:baseline:stage_run:conflict"){t.Fatal("concurrent diagnosis inconsistent")}
}
```

- [ ] **Step 3: Record missing-API RED, then share the existing store transaction**

先同步 Task 1 成果和两个新测试文件。分别运行 `Invoke-FailureTests './internal/taskstore' '^TestGenerationFailureStore'` 与 `Invoke-FailureTests './internal/testgencoord' '^TestFailureDiagnosticCoordinator'`，记录新增方法不存在的 RED；不误称原子性行为已经测到。Task 3 另有真实旧行为 RED。

把原 `CheckpointGeneration` 的完整函数体移为 private `checkpointGeneration`（参数末尾增加 `failure *testgendomain.FailureDiagnostic`）；不复制事务、不改变原有验证／SQL／事件流程。两个 public wrappers 的精确代码：

```go
func (s *Store) CheckpointGeneration(ctx context.Context, expected int64, next testgendomain.Run, candidates []testgendomain.Candidate, artifacts []task.Artifact) (testgendomain.Run,error) {
 return s.checkpointGeneration(ctx,expected,next,candidates,artifacts,nil)
}
func (s *Store) CheckpointGenerationWithFailure(ctx context.Context,expected int64,next testgendomain.Run,failure testgendomain.FailureDiagnostic)(testgendomain.Run,error){
 return s.checkpointGeneration(ctx,expected,next,nil,nil,&failure)
}
```

private 函数签名为 `func (s *Store) checkpointGeneration(ctx context.Context, expected int64, next testgendomain.Run, candidates []testgendomain.Candidate, artifacts []task.Artifact, failure *testgendomain.FailureDiagnostic) (testgendomain.Run,error)`。在原入口验证后、`BeginTx` 前插入：

```go
failureCode := ""
if failure != nil {
 if next.State != testgendomain.StateFailed { return testgendomain.Run{},task.ErrInvalidArgument }
 var encodeErr error
 failureCode,encodeErr=failure.Encode()
 if encodeErr!=nil { return testgendomain.Run{},task.ErrInvalidArgument }
}
```

仅将原 task UPDATE 替换为下列完整语句；其后的 RowsAffected、事件、sequence 和 Commit 保留。原 `getGeneration` 已核验持久化 task kind 为 test_generation；附加 SQL kind 条件为最后写入边界：

```go
result,e=tx.ExecContext(ctx,`UPDATE tasks SET status=?,outcome=?,started_at=COALESCE(started_at,?),finished_at=?,active_step=?,error_code=CASE WHEN ? THEN ? ELSE error_code END,error_message=CASE WHEN ? THEN '' ELSE error_message END WHERE task_id=? AND last_sequence=? AND kind='test_generation'`,status,outcome,started,finished,string(next.State),failure!=nil,failureCode,failure!=nil,next.TaskID,current.LastSequence)
```

原入口任何 null/invalid 状态检查、minimized record／候选／产物约束都不删除。诊断失败用 nil candidates/artifacts，所以没有新的部分产物路径；旧调用布尔 false 保留原错误字段。

- [ ] **Step 4: Add the observed-identity coordinator entry without rewriting legacy terminalization**

在 `Fail` 旁增加以下完整方法，原 `Fail`／`Cancel`／`terminalize` 不修改：

```go
func (c *Coordinator) FailWithDiagnostic(ctx context.Context,runID,taskID string,expected int64,d testgendomain.FailureDiagnostic)(testgendomain.Run,error){
 if c==nil||c.store==nil||ctx==nil { return testgendomain.Run{},ErrUnavailable }
 if expected<1||!d.Valid() { return testgendomain.Run{},task.ErrInvalidArgument }
 for attempts:=0;attempts<3;attempts++ {
  r,err:=c.store.GetGenerationByTask(ctx,taskID)
  if err!=nil { return testgendomain.Run{},err }
  if r.ID!=runID||r.TaskID!=taskID { return testgendomain.Run{},task.ErrConflict }
  if testgendomain.IsTerminal(r.State) { return r,nil }
  if r.Revision!=expected { return testgendomain.Run{},task.ErrConflict }
  next:=testgendomain.CloneRun(r);next.State=testgendomain.StateFailed;next.Revision++
  now:=time.Now().UTC();next.FinishedAt=&now
  saved,err:=c.store.CheckpointGenerationWithFailure(ctx,expected,next,d)
  if errors.Is(err,task.ErrConflict) { continue }
  return saved,err
 }
 return testgendomain.Run{},task.ErrConflict
}
```

`errors`／`time` 已在 coordinator imports。此失败入口不新做 snapshot/tool hash/进程启动，不比原 `Fail` 多做昂贵索引发现；stale workspace 仍能按原存储允许的安全失败终结。三次 CAS retry 只是继承原有有界终结模式，非终态 revision 一旦推进立即拒绝旧诊断。

- [ ] **Step 5: Verify focused GREEN/legacy gates and commit exact Task 2 paths**

先用 `$GoFmt -w` 格式化四个 Task 2 文件，再同步 Task 1／2 的六文件成果；分别运行：

```powershell
Invoke-FailureTests './internal/taskstore' '^TestGenerationFailureStore|^TestGenerationPersistenceAndRevisionCAS$|^TestGenerationRejectsPersistedRequestOrStatusDrift$|^TestGenerationRejectsTamperedCheckpointRecord$'
Invoke-FailureTests './internal/testgencoord' '^TestFailureDiagnosticCoordinator|^TestFailTerminalizesStaleRunWithoutLaunchingOrChangingOwnership$|^TestCancelOwnershipIdempotencyAndStaleIdentity$|^TestRestartResumesFromEachCompletedStage$'
```

要求两个实际 exit 0；完整 receipt 包含 rollback、reopen、四个 terminal 分支及并发结果。核对 transaction 共用、无第二次 UPDATE、新 store 入口无法写非失败／非生成任务、旧 `Fail` 方法未改变。只暂存本任务四路径，路径清单确认后提交：

```powershell
Assert-FailureFrozen
Commit-FailureTask @('apps/test-service/internal/taskstore/test_generation.go','apps/test-service/internal/testgencoord/coordinator.go','apps/test-service/internal/taskstore/test_generation_failure_test.go','apps/test-service/internal/testgencoord/failure_test.go') 'fix: persist generation failure diagnostics atomically'
```

提供实际提交／摘要／receipts；不把 codec／store 测试称作真实用例生成验收。

### Task 3: Preserve safe boundary evidence from the real worker and production driver

**Files:**

- Create: `apps/test-service/internal/runtime/test_generation_failure.go`
- Modify: 同目录 `test_generation.go` 的 `run`／`fail`／`ResumeAll`（基线行 758–970），仅现有 fail 收口；同目录 `production_generation_driver.go` 的 `generate`／`RunStage`（基线行 156–229），仅现有错误返回。
- Create/Test: 同目录 `test_generation_failure_test.go`、`production_generation_failure_test.go`

**Interfaces:**

- Consumes: Task 1 `FailureDiagnostic`，Task 2 `Coordinator.FailWithDiagnostic(ctx,runID,taskID,revision,diagnostic)`，现有 `generationService.launch/run/Close` 和 `generationExecution.done`。
- Consumes: 现有 `GenerationDriver.RunStage(ctx,run,next,authorize)`，`GenerationStageResult{Next,Candidates,Artifacts,MinimizedCaseIDs,PreviewSet}` 和 `productionPipelineFixture(t) (*productionGenerationPipeline,generationTarget,*pipelineAnalyzerFixture,*pipelineOracleFixture)`。
- Produces: private `generationFailureDiagnostic(stage,operation string,cause error) testgendomain.FailureDiagnostic`，`withGenerationOperation(string,error) error`，`generationOperationError.Error/Unwrap`。
- Produces: `(*generationService).fail(testgendomain.Run,testgendomain.FailureDiagnostic)`；不增加 public API。真实 worker 返回失败后，task 字段保存 TG1，未知错误不格式化；取消／关闭和被旧安全约束拒绝的状态不强行失败。

- [ ] **Step 1: Add only the existing-API primary worker regression and controlled fixture**

先在新 runtime `test_generation_failure_test.go` 写入以下完整代码，不先加入依赖新 runtime helper 的分类／driver 测试。Task 1／2 已完成，所以主 RED 必须是执行器仍丢失原因，不能是编译失败。

```go
package runtime

import (
 "context"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "unit-test-ide.local/test-service/internal/task"
 "unit-test-ide.local/test-service/internal/taskstore"
 "unit-test-ide.local/test-service/internal/testgencoord"
 "unit-test-ide.local/test-service/internal/testgendomain"
 "unit-test-ide.local/test-service/internal/testgenpublish"
)

type generationFailureWorkerDriver struct {
 generationDriverFixture
 failAt testgendomain.State
 cause error
 entered chan struct{}
 release chan struct{}
 waitDeadline bool
 result *GenerationStageResult
 budget *testgencoord.BudgetAmount
}
func (d *generationFailureWorkerDriver) StageBudget(r testgendomain.Run,next testgendomain.State)testgencoord.BudgetAmount{
 if next==d.failAt&&d.budget!=nil{return *d.budget}
 return d.generationDriverFixture.StageBudget(r,next)
}
func (d *generationFailureWorkerDriver) RunStage(ctx context.Context,r testgendomain.Run,next testgendomain.State,authorize func(context.Context)error)(GenerationStageResult,error){
 if next!=d.failAt{return d.generationDriverFixture.RunStage(ctx,r,next,authorize)}
 if err:=authorize(ctx);err!=nil{return GenerationStageResult{},err}
 if _,ok:=ctx.Deadline();!ok{panic("missing stage deadline")}
 if d.entered!=nil{close(d.entered)}
 if d.release!=nil{select{case<-d.release:case<-ctx.Done():return GenerationStageResult{},ctx.Err()}}
 if d.waitDeadline{<-ctx.Done();return GenerationStageResult{},task.ErrConflict}
 if d.result!=nil{return *d.result,nil}
 return GenerationStageResult{},d.cause
}

func newGenerationFailureWorker(t *testing.T,d *generationFailureWorkerDriver,wallMS int64)(*generationService,*taskstore.Store,testgendomain.Run){
 t.Helper();s,err:=taskstore.Open(filepath.Join(t.TempDir(),"failure.sqlite"));if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){_ = s.Close()})
 service,err:=newGenerationService(GenerationServiceConfig{
  Store:s,Driver:d,Publisher:&generationPublisherFixture{complete:d.complete},Trusted:true,CoverageReady:true,
  VerifySnapshot:func(_ context.Context,r testgendomain.Request)(testgendomain.SnapshotIdentity,error){return r.SnapshotIdentity(),nil},
  VerifyArtifact:func(context.Context,task.Artifact)error{return nil},VerifyProcess:func(context.Context,string,string)error{return nil},
 })
 if err!=nil{t.Fatal(err)};t.Cleanup(service.Close)
 digest:=strings.Repeat("a",64)
 request:=testgendomain.Request{IdempotencyKey:strings.Repeat("1",32),WorkspaceGeneration:digest,ProjectID:"core",Scope:testgendomain.ScopeWorkspace,Framework:testgendomain.FrameworkAuto,
  Goals:testgendomain.Goals{FunctionPercent:100,LinePercent:100,BranchPercent:100},Budgets:testgendomain.Budgets{WallTimeMS:wallMS,CandidateCount:4,MemoryMiB:256,Concurrency:1},
  CompileSnapshotDigest:digest,CoverageSnapshotDigest:digest,SourceDigest:digest,CMakeTargetDigest:digest,FrameworkBundleDigest:digest,AnalyzerBundleDigest:digest,BaselineReportDigest:digest,ProcessOwnerDigest:digest,SessionOwnerDigest:strings.Repeat("f",64),
 }
 r,err:=service.coord.Start(context.Background(),request);if err!=nil{t.Fatal(err)}
 return service,s,r
}

func generationFailureDone(t *testing.T,s *generationService,r testgendomain.Run,d *generationFailureWorkerDriver)<-chan struct{}{
 t.Helper();s.launch(r.ID)
 select{case<-d.entered:case<-time.After(5*time.Second):t.Fatal("worker stage not entered")}
 s.mu.Lock();execution:=s.running[r.ID];s.mu.Unlock()
 if execution==nil{t.Fatal("worker registration missing while stage blocked")}
 return execution.done
}
func waitGenerationFailureDone(t *testing.T,done <-chan struct{}){
 t.Helper();select{case<-done:case<-time.After(5*time.Second):t.Fatal("worker did not finish")}
}

func TestGenerationFailureWorkerPersistsAfterBaseline(t *testing.T){
 d:=&generationFailureWorkerDriver{failAt:testgendomain.StateAnalyzing,cause:task.ErrStorageUnavailable,entered:make(chan struct{}),release:make(chan struct{})}
 s,store,r:=newGenerationFailureWorker(t,d,60000);done:=generationFailureDone(t,s,r,d)
 baseline,err:=store.GetGeneration(context.Background(),r.ID)
 if err!=nil||baseline.State!=testgendomain.StateBaseline||baseline.Revision!=2{t.Fatal("failure stage began before baseline checkpoint")}
 close(d.release);waitGenerationFailureDone(t,done)
 after,err:=store.GetGeneration(context.Background(),r.ID)
 if err!=nil||after.State!=testgendomain.StateFailed||after.Revision!=3||after.CandidateCount!=0{t.Fatal("worker failure lifecycle differs")}
 row,err:=store.Get(context.Background(),r.TaskID)
 if err!=nil{t.Fatal(err)}
 if row.ErrorCode!="TG1:analyzing:stage_run:storage_unavailable"{t.Fatalf("worker dropped safe failure diagnostic: %q",row.ErrorCode)}
 if row.ErrorMessage!=""{t.Fatal("raw error message persisted")}
 events,err:=store.ReplayGenerationEvents(context.Background(),r.ID,0,200)
 if err!=nil||len(events)!=5||events[4].Type!=task.EventTaskFinished{t.Fatal("worker terminal events differ")}
}

func TestGenerationFailureWorkerCancellationAndClose(t *testing.T){
 for _,cancelRun:=range []bool{true,false}{
  name:="close";if cancelRun{name="cancel"}
  t.Run(name,func(t *testing.T){
   d:=&generationFailureWorkerDriver{failAt:testgendomain.StateBaseline,cause:task.ErrStorageUnavailable,entered:make(chan struct{}),release:make(chan struct{})}
   s,store,r:=newGenerationFailureWorker(t,d,60000);done:=generationFailureDone(t,s,r,d)
   if cancelRun{
    ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
    if _,err:=s.CancelTestGeneration(ctx,r.Request.SessionOwnerDigest,r.ID);err!=nil{t.Fatal("registered worker cancellation rejected")}
   }else{s.Close()}
   waitGenerationFailureDone(t,done)
   after,err:=store.GetGeneration(context.Background(),r.ID)
   if err!=nil||after.State==testgendomain.StateFailed{t.Fatal("cancel/close converted to failed")}
   if cancelRun&&after.State!=testgendomain.StateCancelled{t.Fatal("cancelled terminal missing")}
   row,err:=store.Get(context.Background(),r.TaskID);if err!=nil||row.ErrorCode!=""{t.Fatal("cancel/close wrote failure diagnosis")}
  })
 }
}

type generationFailurePlanPublisher struct{*generationPublisherFixture;cause error;badDiff bool}
func(p *generationFailurePlanPublisher)Plan(ctx context.Context,set testgenpublish.CandidateSet)(testgenpublish.PublishPlan,error){
 if p.cause!=nil{return testgenpublish.PublishPlan{},p.cause}
 plan,err:=p.generationPublisherFixture.Plan(ctx,set);if p.badDiff{plan.Diff=""};return plan,err
}

func TestGenerationFailureWorkerBoundaryFixtures(t *testing.T){
 cases:=[]struct{name,code string;driver *generationFailureWorkerDriver;publisher *generationFailurePlanPublisher}{
  {"reserve-exceeded","TG1:baseline:reserve_budget:budget_exceeded",&generationFailureWorkerDriver{failAt:testgendomain.StateBaseline,budget:&testgencoord.BudgetAmount{MemoryMiB:257}},nil},
  {"reserve-invalid","TG1:baseline:reserve_budget:budget_invalid",&generationFailureWorkerDriver{failAt:testgendomain.StateBaseline,budget:&testgencoord.BudgetAmount{MemoryMiB:-1}},nil},
  {"invalid-transition","TG1:baseline:transition:invalid_transition",&generationFailureWorkerDriver{failAt:testgendomain.StateBaseline,result:&GenerationStageResult{Next:testgendomain.StateAccepted}},nil},
  {"preview-missing","TG1:awaiting_confirmation:preview_set:invalid_preview",&generationFailureWorkerDriver{generationDriverFixture:generationDriverFixture{complete:true},failAt:testgendomain.StateAwaitingConfirmation,result:&GenerationStageResult{Next:testgendomain.StateAwaitingConfirmation}},nil},
  {"set-missing","TG1:awaiting_confirmation:authoritative_set:conflict",&generationFailureWorkerDriver{generationDriverFixture:generationDriverFixture{complete:true},failAt:testgendomain.StateAwaitingConfirmation,result:&GenerationStageResult{Next:testgendomain.StateAwaitingConfirmation,PreviewSet:&testgenpublish.CandidateSet{}}},nil},
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  s,store,r:=newGenerationFailureWorker(t,tc.driver,60000)
  if tc.publisher!=nil{s.publisher=tc.publisher}
  s.run(context.Background(),r.ID)
  after,err:=store.GetGeneration(context.Background(),r.ID)
  if err!=nil||after.State!=testgendomain.StateFailed{t.Fatal("controlled boundary did not fail")}
  row,err:=store.Get(context.Background(),r.TaskID);if err!=nil||row.ErrorCode!=tc.code||row.ErrorMessage!=""{t.Fatal("wrong controlled boundary diagnosis")}
 })}
}
```

这些 private fixtures 无运行期共享计数修改，entered/release/done 控制真实 primary worker；边界 fixtures 直接调用原 `run`，不冒充受管启动交接验收。`s.Close` 在 store.Close 前清理；超时只是测试上限，不改产品预算。

- [ ] **Step 2: Capture genuine old-worker behavioral RED**

同步已完成 Task 1／2 所有文件和刚新增的 worker 测试；仅运行 `Invoke-FailureTests './internal/runtime' '^TestGenerationFailureWorkerPersistsAfterBaseline$'`。预计编译成功、baseline／failed 生命周期通过，但 `worker dropped safe failure diagnostic: ""` 失败。若是编译／环境失败，不进入实现；先修正测试准备。不要为了制造 RED 回滚已提交产品，也不重复 dispatch 修正。

- [ ] **Step 3: Add the complete safe mapper and internal boundary wrapper**

新 `test_generation_failure.go`：

```go
package runtime

import(
 "context"
 "errors"
 "unit-test-ide.local/test-service/internal/task"
 "unit-test-ide.local/test-service/internal/testgencoord"
 "unit-test-ide.local/test-service/internal/testgendomain"
)

type generationOperationError struct{operation string;cause error}
func(e *generationOperationError)Error()string{return "generation operation failed"}
func(e *generationOperationError)Unwrap()error{return e.cause}
func withGenerationOperation(operation string,cause error)error{
 if cause==nil{return nil}
 if !(testgendomain.FailureDiagnostic{"unknown",operation,"unknown"}).Valid(){return cause}
 return &generationOperationError{operation:operation,cause:cause}
}
func generationFailureDiagnostic(stage,operation string,cause error)testgendomain.FailureDiagnostic{
 if !(testgendomain.FailureDiagnostic{stage,"unknown","unknown"}).Valid(){stage="unknown"}
 if !(testgendomain.FailureDiagnostic{"unknown",operation,"unknown"}).Valid(){operation="unknown"}
 if operation=="stage_run"{
  var boundary *generationOperationError
  if errors.As(cause,&boundary)&&boundary!=nil&&(testgendomain.FailureDiagnostic{"unknown",boundary.operation,"unknown"}).Valid(){operation=boundary.operation}
 }
 reason:="unknown"
 switch{
 case errors.Is(cause,context.DeadlineExceeded),errors.Is(cause,testgencoord.ErrBudgetTimeout):reason="timed_out"
 case errors.Is(cause,context.Canceled):reason="cancelled"
 case errors.Is(cause,testgencoord.ErrBudgetExceeded):reason="budget_exceeded"
 case errors.Is(cause,testgencoord.ErrBudgetInvalid):reason="budget_invalid"
 case errors.Is(cause,testgendomain.ErrStaleSnapshot):reason="stale_snapshot"
 case errors.Is(cause,task.ErrInvalidArgument),errors.Is(cause,testgendomain.ErrInvalid):reason="invalid_argument"
 case errors.Is(cause,task.ErrStorageUnavailable):reason="storage_unavailable"
 case errors.Is(cause,testgencoord.ErrUnavailable),errors.Is(cause,errProductionGenerationUnavailable):reason="unavailable"
 case errors.Is(cause,task.ErrConflict):reason="conflict"
 }
 return testgendomain.FailureDiagnostic{Stage:stage,Operation:operation,Reason:reason}
}
```

内部 wrapper 的 Error 返回固定文本，Unwrap 保留 `errors.Is` 身份；mapper 不格式化对象、不复制 cause 到诊断。stage_context 明确传入时，不受嵌套 driver 环节覆盖。

- [ ] **Step 4: Thread diagnostics through exactly the existing fail sites**

替换 `s.fail` 的签名／调用，保留原 guard、后台 context 和事件发布：

```go
func(s *generationService)fail(run testgendomain.Run,d testgendomain.FailureDiagnostic){
 s.mu.Lock();cancelling:=s.cancelPending[run.ID];s.mu.Unlock()
 if cancelling{return}
 if run.State==testgendomain.StateCancelled||testgendomain.IsTerminal(run.State){return}
 failed,err:=s.coord.FailWithDiagnostic(context.Background(),run.ID,run.TaskID,run.Revision,d)
 if err==nil{s.publishNewEvents(context.Background(),run,failed)}
}
```

各原收口位置按下表替换；表中语句是该分支完整的新调用，前后释放、return、`ctx.Err()==nil` 判断等原语句不动。

| 原分支 | 精确新调用 |
| --- | --- |
| `generationLedger` 返回 err | `s.fail(run, generationFailureDiagnostic(string(next), "ledger", err))` |
| `ledger.Reserve` 返回 err | `s.fail(run, generationFailureDiagnostic(string(next), "reserve_budget", err))` |
| 非法 `ValidTransition` | `s.fail(run, testgendomain.FailureDiagnostic{Stage: string(next), Operation: "transition", Reason: "invalid_transition"})` |
| nil PreviewSet | `s.fail(run, testgendomain.FailureDiagnostic{Stage: string(next), Operation: "preview_set", Reason: "invalid_preview"})` |
| `authoritativeSet` 的 setErr | `s.fail(run, generationFailureDiagnostic(string(next), "authoritative_set", setErr))` |
| `publisher.Plan` 的 planErr | `s.fail(run, generationFailureDiagnostic(string(next), "publish_plan", planErr))` |
| 空／越长／哈希不符的 Diff | `s.fail(run, testgendomain.FailureDiagnostic{Stage: string(next), Operation: "preview_diff", Reason: "invalid_preview"})` |
| `reservation.Commit` 的 err | `s.fail(run, generationFailureDiagnostic(string(next), "commit_budget", err))` |
| run 中 managed review finalizer 的 err | `s.fail(committed, generationFailureDiagnostic("awaiting_confirmation", "finalize_review", err))` |
| ResumeAll 中 review finalizer 的 err | `s.fail(run, generationFailureDiagnostic("awaiting_confirmation", "finalize_review", err))` |
| ResumeAll 中 resumeErr | `s.fail(run, generationFailureDiagnostic("unknown", "resume", resumeErr))` |

stage 返回错误分支用以下完整块取代原 `if stageErr != nil || stageContextErr != nil`，保留 context 优先序及原资源释放：

```go
if stageErr!=nil||stageContextErr!=nil{
 d:=generationFailureDiagnostic(string(next),"stage_run",stageErr)
 if stageContextErr!=nil{d=generationFailureDiagnostic(string(next),"stage_context",stageContextErr)}
 cancel();reservation.Release()
 if ctx.Err()==nil{s.fail(run,d)}
 return
}
```

不改变 `CheckpointWithRecord` 出错后的静默 return，不将 coord.Get／NextStage 错误变成失败，也不改变 ledger 预算和 preview/checkpoint 流程。

- [ ] **Step 5: Mark production driver return boundaries, preserving cache and authorization**

`generate` 中只替换三个原错误 return；缓存命中／加锁、request 相等判断和成功缓存写入不动：

```go
if driver.pipeline==nil||authorize==nil{return productionPipelineResult{},withGenerationOperation("pipeline_generate",task.ErrStorageUnavailable)}
if err:=authorize(ctx);err!=nil{return productionPipelineResult{},withGenerationOperation("authorize_process",err)}
result,err:=driver.pipeline.Generate(ctx,target)
if err!=nil{return productionPipelineResult{},withGenerationOperation("pipeline_generate",err)}
```

`RunStage` 中保留 nil context、deadline fallback、baseline 直接完成和 generate 原调用；target resolve 返回改为 `withGenerationOperation("resolve_target",err)`。switch 的 validating／minimizing 完整替换代码：

```go
case testgendomain.StateValidating:
 if driver.validator==nil||!driver.validator.Ready()||authorize==nil{return GenerationStageResult{},withGenerationOperation("validate_candidates",task.ErrStorageUnavailable)}
 if err:=authorize(ctx);err!=nil{return GenerationStageResult{},withGenerationOperation("authorize_process",err)}
 validated,validationErr:=driver.validator.Validate(ctx,run,target,result)
 return validated,withGenerationOperation("validate_candidates",validationErr)
case testgendomain.StateMinimizing:
 if driver.validator==nil||!driver.validator.Ready(){return GenerationStageResult{},withGenerationOperation("minimize_candidates",task.ErrStorageUnavailable)}
 minimized,minimizeErr:=driver.validator.Minimize(ctx,run,target,result)
 return minimized,withGenerationOperation("minimize_candidates",minimizeErr)
```

`RunStage` 对 `driver.generate` 返回的错误保持直接传播，不能再加外层 pipeline marker 吞掉 authorize_process。不要改 validator/pipeline 内部语义或额外执行任何工具。

- [ ] **Step 6: Add mapper privacy/priority and real context/boundary checks**

在 worker 测试 imports 增加 `bytes`、`errors`，然后追加以下完整代码：

```go
type diagnosticNoFormatError struct{}
func(diagnosticNoFormatError)Error()string{panic("FAKE_SECRET_PATH_SOURCE_COMMAND_MARKER must not be formatted")}

func TestGenerationFailureMapperIdentityPriorityPrivacy(t *testing.T){
 cases:=[]struct{cause error;reason string}{
  {context.DeadlineExceeded,"timed_out"},{testgencoord.ErrBudgetTimeout,"timed_out"},{context.Canceled,"cancelled"},
  {testgencoord.ErrBudgetExceeded,"budget_exceeded"},{testgencoord.ErrBudgetInvalid,"budget_invalid"},
  {testgendomain.ErrStaleSnapshot,"stale_snapshot"},{task.ErrInvalidArgument,"invalid_argument"},{testgendomain.ErrInvalid,"invalid_argument"},
  {task.ErrStorageUnavailable,"storage_unavailable"},{testgencoord.ErrUnavailable,"unavailable"},{errProductionGenerationUnavailable,"unavailable"},
  {task.ErrConflict,"conflict"},{diagnosticNoFormatError{},"unknown"},{nil,"unknown"},
 }
 for _,tc:=range cases{
  got:=generationFailureDiagnostic("analyzing","stage_run",tc.cause)
  if got.Reason!=tc.reason||!got.Valid(){t.Fatal("error identity classification differs")}
  if tc.cause!=nil{
   wrapped:=withGenerationOperation("resolve_target",tc.cause)
   if !errors.Is(wrapped,tc.cause){t.Fatal("wrapper lost error identity")}
   got=generationFailureDiagnostic("analyzing","stage_run",wrapped)
   if got.Operation!="resolve_target"||got.Reason!=tc.reason{t.Fatal("safe wrapped boundary lost")}
  }
 }
 ordered:=[]struct{first,second error;reason string}{
  {context.DeadlineExceeded,context.Canceled,"timed_out"},
  {context.Canceled,testgencoord.ErrBudgetExceeded,"cancelled"},
  {testgencoord.ErrBudgetExceeded,testgencoord.ErrBudgetInvalid,"budget_exceeded"},
  {testgencoord.ErrBudgetInvalid,testgendomain.ErrStaleSnapshot,"budget_invalid"},
  {testgendomain.ErrStaleSnapshot,task.ErrInvalidArgument,"stale_snapshot"},
  {task.ErrInvalidArgument,task.ErrStorageUnavailable,"invalid_argument"},
  {task.ErrStorageUnavailable,testgencoord.ErrUnavailable,"storage_unavailable"},
  {testgencoord.ErrUnavailable,task.ErrConflict,"unavailable"},
 }
 for _,tc:=range ordered{if got:=generationFailureDiagnostic("baseline","stage_run",errors.Join(tc.second,tc.first));got.Reason!=tc.reason{t.Fatal("classification priority differs")}}
 sanitized:=generationFailureDiagnostic("failed","FAKE_SECRET_PATH_MARKER",diagnosticNoFormatError{})
 if sanitized!=(testgendomain.FailureDiagnostic{"unknown","unknown","unknown"}){t.Fatal("invalid metadata not reduced to unknown")}
 cause:=withGenerationOperation("resolve_target",context.DeadlineExceeded)
 if got:=generationFailureDiagnostic("analyzing","stage_context",cause);got.Operation!="stage_context"{t.Fatal("context boundary overridden")}
 if withGenerationOperation("stage_run",nil)!=nil{t.Fatal("nil error became failure")}
}

func TestGenerationFailureWorkerUnknownNeverFormatsRawError(t *testing.T){
 d:=&generationFailureWorkerDriver{failAt:testgendomain.StateAnalyzing,cause:diagnosticNoFormatError{},entered:make(chan struct{}),release:make(chan struct{})}
 s,store,r:=newGenerationFailureWorker(t,d,60000);done:=generationFailureDone(t,s,r,d)
 close(d.release);waitGenerationFailureDone(t,done)
 row,err:=store.Get(context.Background(),r.TaskID)
 if err!=nil||row.ErrorCode!="TG1:analyzing:stage_run:unknown"||row.ErrorMessage!=""{t.Fatal("unknown diagnosis differs")}
 events,err:=store.ReplayGenerationEvents(context.Background(),r.ID,0,200);if err!=nil{t.Fatal(err)}
 for _,event:=range events{if bytes.Contains(event.Payload,[]byte("FAKE_SECRET")){t.Fatal("raw marker reached journal")}}
}

func TestGenerationFailureWorkerStageContextPrecedence(t *testing.T){
 d:=&generationFailureWorkerDriver{failAt:testgendomain.StateBaseline,waitDeadline:true,entered:make(chan struct{})}
 s,store,r:=newGenerationFailureWorker(t,d,3000);done:=generationFailureDone(t,s,r,d)
 waitGenerationFailureDone(t,done)
 row,err:=store.Get(context.Background(),r.TaskID)
 if err!=nil||row.ErrorCode!="TG1:baseline:stage_context:timed_out"{t.Fatal("stage context lost to returned conflict")}
}

func TestGenerationFailureWorkerPublishBoundaries(t *testing.T){
 for _,tc:=range []struct{name,code string;planErr error;badDiff bool}{
  {"plan","TG1:awaiting_confirmation:publish_plan:conflict",task.ErrConflict,false},
  {"diff","TG1:awaiting_confirmation:preview_diff:invalid_preview",nil,true},
 }{t.Run(tc.name,func(t *testing.T){
  d:=&generationFailureWorkerDriver{generationDriverFixture:generationDriverFixture{complete:true},failAt:testgendomain.StateAwaitingConfirmation,result:&GenerationStageResult{Next:testgendomain.StateAwaitingConfirmation,MinimizedCaseIDs:[]string{strings.Repeat("5",32)},PreviewSet:&testgenpublish.CandidateSet{}}}
  s,store,r:=newGenerationFailureWorker(t,d,60000)
  s.publisher=&generationFailurePlanPublisher{generationPublisherFixture:&generationPublisherFixture{complete:true},cause:tc.planErr,badDiff:tc.badDiff}
  s.run(context.Background(),r.ID)
  row,err:=store.Get(context.Background(),r.TaskID);if err!=nil||row.ErrorCode!=tc.code{t.Fatal("publish boundary diagnosis differs")}
 })}
}

func TestGenerationFailureExplicitAdapterBoundaries(t *testing.T){
 for _,tc:=range []struct{stage,operation string;cause error}{
  {"baseline","ledger",testgencoord.ErrBudgetInvalid},
  {"analyzing","commit_budget",testgencoord.ErrBudgetTimeout},
  {"awaiting_confirmation","finalize_review",task.ErrConflict},
  {"unknown","resume",testgendomain.ErrStaleSnapshot},
 }{t.Run(tc.operation,func(t *testing.T){
  d:=&generationFailureWorkerDriver{};s,store,r:=newGenerationFailureWorker(t,d,60000)
  diagnostic:=generationFailureDiagnostic(tc.stage,tc.operation,tc.cause)
  s.fail(r,diagnostic)
  row,err:=store.Get(context.Background(),r.TaskID);raw,encodeErr:=diagnostic.Encode()
  if err!=nil||encodeErr!=nil||row.ErrorCode!=raw{t.Fatal("explicit adapter lost safe metadata")}
 })}
}
```

最后一个测试仅证明 adapter／持久化边界，不证明 ledger／commit 超时／恢复／finalizer 的每种实际生产时序。它避免为了触发困难时序而给产品新增可变 clock／hook；真实 worker 的阶段/context/取消测试和完整调用位置审阅是额外证据。Task 2 的 minimized-record 拒绝测试覆盖 finalizer 原状态无法终结的限制，不能声称该限制已修好。

- [ ] **Step 7: Add production boundary tests using existing pure fixtures**

新 runtime `production_generation_failure_test.go`：

```go
package runtime

import(
 "context"
 "errors"
 "testing"
 "time"
 analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
 "unit-test-ide.local/test-service/internal/task"
 "unit-test-ide.local/test-service/internal/testgendomain"
)

type diagnosticProductionResolver struct{productionTargetResolverFixture;cause error}
func(r *diagnosticProductionResolver)ResolveRequest(context.Context,testgendomain.Request)(generationTarget,error){
 if r.cause!=nil{return generationTarget{},r.cause};return r.target,nil
}
type diagnosticProductionAnalyzer struct{cause error}
func(a *diagnosticProductionAnalyzer)Analyze(context.Context,analysis.AnalysisRequest)(analysis.Program,error){return analysis.Program{},a.cause}
type diagnosticProductionValidator struct{productionValidatorFixture;cause error}
func(v *diagnosticProductionValidator)Validate(context.Context,testgendomain.Run,generationTarget,productionPipelineResult)(GenerationStageResult,error){return GenerationStageResult{},v.cause}
func(v *diagnosticProductionValidator)Minimize(context.Context,testgendomain.Run,generationTarget,productionPipelineResult)(GenerationStageResult,error){return GenerationStageResult{},v.cause}

func TestProductionGenerationFailureBoundariesPreserveIdentity(t *testing.T){
 for _,tc:=range []struct{name,operation string;stage testgendomain.State;cause error}{
  {"resolve","resolve_target",testgendomain.StateAnalyzing,testgendomain.ErrStaleSnapshot},
  {"authorize","authorize_process",testgendomain.StateAnalyzing,task.ErrConflict},
  {"pipeline","pipeline_generate",testgendomain.StateAnalyzing,task.ErrInvalidArgument},
  {"validate","validate_candidates",testgendomain.StateValidating,task.ErrConflict},
  {"minimize","minimize_candidates",testgendomain.StateMinimizing,testgendomain.ErrStaleSnapshot},
  {"validator-unavailable","validate_candidates",testgendomain.StateValidating,task.ErrStorageUnavailable},
 }{t.Run(tc.name,func(t *testing.T){
  pipeline,target,_,_:=productionPipelineFixture(t)
  resolver:=&diagnosticProductionResolver{productionTargetResolverFixture:productionTargetResolverFixture{target:target}}
  validator:=&diagnosticProductionValidator{cause:tc.cause}
  driver:=newProductionGenerationDriver(resolver,pipeline,validator)
  run:=testgendomain.Run{ID:"11111111111111111111111111111111",TaskID:"22222222222222222222222222222222",Request:target.request,CreatedAt:time.Now()}
  authorize:=func(context.Context)error{return nil}
  switch tc.name{
  case "resolve":resolver.cause=tc.cause
  case "authorize":authorize=func(context.Context)error{return tc.cause}
  case "pipeline":pipeline.analyzer=&diagnosticProductionAnalyzer{cause:tc.cause}
  default:
   driver.cache[run.ID]=cachedProductionPipeline{request:run.Request,result:productionPipelineResult{}}
   if tc.name=="validator-unavailable"{driver.validator=nil}
  }
  _,err:=driver.RunStage(context.Background(),run,tc.stage,authorize)
  if !errors.Is(err,tc.cause){t.Fatal("boundary wrapper lost original identity")}
  got:=generationFailureDiagnostic(string(tc.stage),"stage_run",err)
  if got.Operation!=tc.operation{t.Fatal("production boundary misidentified")}
 })}
}
```

validation/minimization 的纯边界 fixture 预置 driver 已有 request-bound cache，只测试错误传播；不能当作 analyzer/validation 工具实际执行成功。已有 `TestProductionGenerationDriverResolvesAndRunsFixedStages` 另验证成功流程的 resolver／authorizer／analyzer 次数不变。

- [ ] **Step 8: Verify focused GREEN, exact source scope and complete receipts**

先 `$GoFmt -w` 五个 Task 3 路径，再同步全部 11 个 owned 文件；运行如下选择一次：

```powershell
Invoke-FailureTests './internal/runtime' '^TestGenerationFailure|^TestProductionGenerationFailure|^TestProductionGenerationDriverResolvesAndRunsFixedStages$|^TestProductionGenerationDriverFailsClosedBeforeAnyStageProcess$|^TestManagedRuntimeDispatch'
```

预计 exit 0，真实 primary、unknown/no-format、context、取消／关闭、控制边界、pure driver 及已提交 dispatch 直接相关回归均实际运行。用完整 JSON receipt 的 Test run/pass/fail 逐一核对新增入口与所有子测试；不预先杜撰 pass 数，不对截断输出做成功断言。包编译会包含既有测试源码，不等于运行全量测试。

自查 `rg -n 's\.fail\(' apps/test-service/internal/runtime/test_generation.go`：全部 12 个原收口有受限诊断，原资源释放/parent guard 保留；`CheckpointWithRecord` 出错 return 不变。检查生成错误字段未进 protocol／DTO／event payload、没有新增 log/error 格式化／migration。确认 Task 1／2 已审阅成果与本轮 overlay 摘要一致。

- [ ] **Step 9: Commit only Task 3 and hand evidence back to W4**

```powershell
Assert-FailureFrozen
Commit-FailureTask @('apps/test-service/internal/runtime/test_generation_failure.go','apps/test-service/internal/runtime/test_generation.go','apps/test-service/internal/runtime/production_generation_driver.go','apps/test-service/internal/runtime/test_generation_failure_test.go','apps/test-service/internal/runtime/production_generation_failure_test.go') 'fix: retain safe generation worker failure evidence'
```

提交前必须核对恰好五路径，不能顺带提交已有 W4／第二版工作。记录实际提交、父提交、11 文件摘要、冻结18/18、索引为空但整体工作区仍脏、完整定点 RED/GREEN 和审阅结果。controller 回原 W4，仅交接“安全失败诊断契约已验收”和实际限制；不自动 build service／native，也不访问原失败数据库补写。

## Final Controller Check and Execution Handoff

三项完成后，只检查已有有效 receipts／提交范围／源码摘要／冻结文件和审阅结论，不为了汇总再无条件重跑三个包。若修正改变已验证接口，只重测直接受影响的选择并说明原因。缺失／截断证据先修复记录方式，不把盲目重跑 native 当诊断。

验收映射：spec §1 的真实旧 worker 漏码由 Task 3 primary RED/GREEN证明；§2 的分层/内部字段由 Task 1/2/3 共同交付；§3 闭集/隐私/旧记录由 Task 1 与 Task 2/3 测试；§4 事务/并发/失败边界由 Task 2 与 Task 3；§5 文件范围由每项精确提交及 controller guards；§6 定点隔离/生命周期由准备控制器和上述测试；§7 新 native/远端/第二版门槛保持未授权。

当前步骤只保存计划和书面批准状态，不运行本文中的代码或测试。执行选择：1）Subagent-Driven，按任务实施并分别做规格／质量审阅（推荐）；2）Inline Execution，在当前会话按 executing-plans 逐项实施并设置审阅检查点。用户选择后再进入对应执行技能；不要创建用户侧新 chat，也不要先启动实现代理。
