# Phase 10 Production Generation Stage Checkpoints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复第一版生产生成链路的检查点错位，使验证、最小化、等待确认和零候选拒绝遵守原状态机，并可从持久化证据恢复。

**Architecture:** 保持通用执行器、协调器、taskstore 和发布器不变。生产验证返回已完成的 validating；最小化和新增 Preview 操作只读取已经提交的候选、源码与证据，不调用生成流水线。唯一候选的 ID 与预览仍在 awaiting_confirmation 检查点一起保存。

**Tech Stack:** Go 现有 runtime、testgendomain、testgencoord、testgenvalidate、artifactstore、SQLite taskstore；PowerShell 7；现有确定性 Go 测试夹具。无新依赖。

## Global Constraints

以下约束取自已确认设计 docs/superpowers/specs/2026-10-11-production-generation-stage-checkpoints-design.md，各任务均受约束。

- “持久化状态表示已经完成的工作；NextStage 决定下一项工作。”
- “ValidTransition、NextStage 和 taskstore 的检查点限制均保持原样。”
- “该返回不携带新的候选或产物，也不携带供执行器提前落库的最小化 ID 与预览。”
- “最小化和等待确认依赖已经提交的候选与验证证据，不依赖之前的流水线内存缓存。”
- “最小化和预览不新增外部工具进程，也不重复消耗生成候选与产物预算。”
- “历史 failed、rejected、accepted 或 cancelled 任务不修改、不复活。”
- “不将暂停的 V2 taskstore 修改及 migration 022 带入本轮测试。”
- “旧完整测试证据不整批重跑；并发与 CAS 验证也使用定点选择器。”
- “后续 native 必须另行取得有界授权，使用新构建、新工作区和新数据库，不修改旧尝试凭据或自动重试。”

本计划保存时不实施、不运行测试或构建。选择执行方式后，仅授权本地代码、独立测试及本地提交；不包含推送 GitHub 或 Gitee、PR、合并、workflow、Release、签名或 native。实际工具验证失败与无覆盖率增益的原因不属于此修复，不改变保留判定。

---

## 工作目录与保护

工作目录为 C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest，分支 codex/phase10-production-generation。已确认设计提交为 a39b7b3a4ac9e525d62e0672980a9f12ebd82ee7，产品代码基线为 e87c26ed82377295290e2fa579c8faf4f0bd768b。不要在 C:/codex_project/unitTest 主工作区实施，不创建或清理 worktree。

执行开始读取 HEAD，并确认设计提交为其祖先、设计文件与提交一致、九个将修改的产品与测试路径尚无用户改动，索引为空。保留已有十九条归组状态及全部展开的未跟踪文件，不 stash、reset、clean 或一并提交。

以下九个路径是完整实施所有权清单，简称 Owned。任务的 Files 再进一步限制当前任务可修改的子集。

| 路径（相对于工作目录） | 责任 |
| --- | --- |
| apps/test-service/internal/runtime/production_generation_validation.go | 仅修改验证阶段返回值，移除迁出的旧 Minimize |
| apps/test-service/internal/runtime/production_generation_driver.go | 内部接口、后期阶段路由，避免恢复时生成 |
| apps/test-service/internal/runtime/production_generation_checkpoint_evidence.go | 委托 SQLite 候选读取、严格恢复当前检查点证据 |
| apps/test-service/internal/runtime/production_generation_checkpoint_stages.go | 独立 Minimize 和 Preview |
| apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go | 真实执行链、持久化与恢复夹具及回归 |
| apps/test-service/internal/runtime/production_generation_checkpoint_security_test.go | 身份、损坏、取消、并发 CAS 回归 |
| apps/test-service/internal/runtime/production_generation_validation_test.go | 校验返回契约与真实归属的旧断言调整 |
| apps/test-service/internal/runtime/production_generation_driver_test.go | 内部接口夹具更新 |
| apps/test-service/internal/runtime/production_generation_failure_test.go | 内部接口及现有闭合诊断边界更新 |

不修改 production_generation_candidate_composition.go。新辅助文件可为已有 runtimeProductionGenerationArtifacts 类型增加一个委托方法，复用其 metadata 字段与 SQLite 的既有 ListGenerationCandidates 方法。

### 执行时准备独立第一版测试模块

准备工作归入 Task 1，不是独立功能任务。使用新建的私有目录 .superpowers/runtime/generation-stage-checkpoints-<fresh UUID>/module，并以 CreateNew 保存尝试凭据；不得覆盖已有目录、控制器或结果。目录实际名称在执行开始一次分配并记录，此后不重新分配以规避失败。

读取既有 native prepared.json 的 ModuleBeforeInstrumentation，共 954 个成员。种子目录为 .superpowers/runtime/production-context-digest-2fb27250e21441168359f42a6f54f698/module。用相对路径逐文件验证种子哈希与该名单一致，再复制。不能复制后来插入计时诊断的 955 文件模块，也不能从含 V2 的当前 apps/test-service 整树复制。

prepared.json、sealed.json、safe-result.json 的确切目录是 .superpowers/sdd/2026-10-11-production-context-tool-digest-scope/context-native-20261011125830。八个已完成优化文件由 prepared.json.ExpectedOverlay 列出，必须与原记录哈希相同，再以当前工作树字节覆盖并记录 SHA 256。随后只覆盖 Owned 中存在的文件。所有路径先检查完整绝对路径留在指定根内、没有重解析点和相对逃逸；复制后验证相同字节。不存在的未来新文件不生成空文件。

新控制器的独立模块准备使用以下内容；这是未来实施步骤，本轮不执行。Owned 使用上面九个精确路径组成的数组；重复同步也只允许这个数组，不收集全部 dirty 文件。

~~~powershell
$Root = (git rev-parse --show-toplevel).Trim()
$OldLedger = Join-Path $Root '.superpowers/sdd/2026-10-11-production-context-tool-digest-scope/context-native-20261011125830'
$Prepared = Get-Content -LiteralPath (Join-Path $OldLedger 'prepared.json') -Raw | ConvertFrom-Json
$Sealed = Get-Content -LiteralPath (Join-Path $OldLedger 'sealed.json') -Raw | ConvertFrom-Json
$SafeResult = Get-Content -LiteralPath (Join-Path $OldLedger 'safe-result.json') -Raw | ConvertFrom-Json
$Seed = Join-Path $Root '.superpowers/runtime/production-context-digest-2fb27250e21441168359f42a6f54f698/module'
$Owned = @(
  'apps/test-service/internal/runtime/production_generation_validation.go',
  'apps/test-service/internal/runtime/production_generation_driver.go',
  'apps/test-service/internal/runtime/production_generation_checkpoint_evidence.go',
  'apps/test-service/internal/runtime/production_generation_checkpoint_stages.go',
  'apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go',
  'apps/test-service/internal/runtime/production_generation_checkpoint_security_test.go',
  'apps/test-service/internal/runtime/production_generation_validation_test.go',
  'apps/test-service/internal/runtime/production_generation_driver_test.go',
  'apps/test-service/internal/runtime/production_generation_failure_test.go'
)
function SafeChild([string]$Base,[string]$Relative) {
  if ([IO.Path]::IsPathRooted($Relative)) { throw 'Absolute member rejected' }
  $Base = [IO.Path]::GetFullPath($Base).TrimEnd('\','/')
  $Path = [IO.Path]::GetFullPath((Join-Path $Base $Relative))
  if (-not $Path.StartsWith($Base+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) {
    throw 'Member escaped its root'
  }
  for ($Part=$Path; $Part; $Part=[IO.Path]::GetDirectoryName($Part)) {
    if ((Test-Path -LiteralPath $Part) -and
        ((Get-Item -LiteralPath $Part -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      throw 'Reparse point rejected'
    }
  }
  return $Path
}
function CheckRows($Rows) {
  foreach ($Row in $Rows) {
    $File = Get-Item -LiteralPath $Row.Path -Force
    if ($File.PSIsContainer -or ($File.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
        (Get-FileHash -LiteralPath $Row.Path -Algorithm SHA256).Hash -cne $Row.SHA256) {
      throw 'Protected file changed'
    }
  }
}
$ProtectedRows = @($Prepared.Protected)+@($Sealed.Source)+@($Sealed.Scripts)+@(
  $SafeResult.Evidence | ForEach-Object {
    [pscustomobject]@{Path=(SafeChild $OldLedger $_.Name);SHA256=$_.SHA256}
  }
)
if (@($Prepared.ModuleBeforeInstrumentation).Count -ne 954 -or
    @($Prepared.ExpectedOverlay).Count -ne 8 -or
    @($SafeResult.Evidence).Count -ne 21) { throw 'Unexpected source or evidence list' }
CheckRows $ProtectedRows
$Suffix = 'generation-stage-checkpoints-'+[Guid]::NewGuid().ToString('N')
$Scratch = SafeChild $Root ('.superpowers/runtime/'+$Suffix)
if (Test-Path -LiteralPath $Scratch) { throw 'Attempt directory already exists' }
[void](New-Item -ItemType Directory -Path $Scratch)
$Module = SafeChild $Scratch 'module'
$Ledger = SafeChild $Scratch 'ledger'
[void](New-Item -ItemType Directory -Path $Module)
[void](New-Item -ItemType Directory -Path $Ledger)
foreach ($Row in $Prepared.ModuleBeforeInstrumentation) {
  $Relative = [IO.Path]::GetRelativePath($Prepared.Module,$Row.Path)
  $From = SafeChild $Seed $Relative
  $To = SafeChild $Module $Relative
  if ((Get-FileHash -LiteralPath $From -Algorithm SHA256).Hash -cne $Row.SHA256) {
    throw 'Seed byte mismatch'
  }
  [void](New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($To)) -Force)
  Copy-Item -LiteralPath $From -Destination $To
  if ((Get-FileHash -LiteralPath $To -Algorithm SHA256).Hash -cne $Row.SHA256) {
    throw 'Module copy mismatch'
  }
}
foreach ($Row in $Prepared.ExpectedOverlay) {
  $From = SafeChild $Root $Row.Path
  if ((Get-FileHash -LiteralPath $From -Algorithm SHA256).Hash -cne $Row.SHA256) {
    throw 'Approved optimization changed'
  }
}
foreach ($Path in @($Prepared.ExpectedOverlay.Path)+$Owned) {
  $From = SafeChild $Root $Path
  if (-not (Test-Path -LiteralPath $From -PathType Leaf)) { continue }
  if (-not $Path.StartsWith('apps/test-service/',[StringComparison]::Ordinal)) { throw 'Overlay outside service' }
  $To = SafeChild $Module $Path.Substring('apps/test-service/'.Length)
  [void](New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($To)) -Force)
  Copy-Item -LiteralPath $From -Destination $To -Force
  if ((Get-FileHash -LiteralPath $To -Algorithm SHA256).Hash -cne
      (Get-FileHash -LiteralPath $From -Algorithm SHA256).Hash) { throw 'Overlay byte mismatch' }
}
CheckRows $ProtectedRows
~~~

准备与每次测试前后均核对：

1. 所有 Git 管理及非忽略未跟踪文件，除当前任务 Owned 子集外，存在性、字节、文件集合与执行前快照一致。
2. 原索引、原用户状态、分支及祖先关系不变；只能提交当前任务列出的路径。
3. 既有 native prepared.json 的 Protected、sealed.json 的 Source 与 Scripts，以及 safe-result.json 的二十一项 Evidence 保持原 SHA 256。旧 native 候选 HEAD 绑定属于旧证据，不能改写为本计划的新提交，也不能调用其 Build 或 Native。
4. 新模块没有 migration 022、casedesign V2 文件、生产计时观察器或未批准覆盖；原模块、工作区、数据库及服务均只读。

快照是新实施范围自己的生成元数据；不得修改旧 prepared.json、旧计划或旧控制器的 Owned 来扩大权限。用 apply_patch 创建未来执行脚本，机械生成的清单和结果单独保存。任何保护失败立即停下，不启动测试。

### 有界定点测试命令

所有命令在上述新模块内运行，不在混合 V2 的工作树直接执行 go test。Go 为 D:/Program Files/Go/bin/go.exe。通过下列 ProcessStartInfo 运行器设置子进程环境；不能修改用户全局环境。定义 RunStageTests 后，各任务列出准确选择器。

~~~powershell
function RunStageTests {
  param(
    [Parameter(Mandatory)][string]$Module,
    [Parameter(Mandatory)][string]$Ledger,
    [Parameter(Mandatory)][string]$Phase,
    [Parameter(Mandatory)][string]$Package,
    [Parameter(Mandatory)][string]$Regex,
    [Parameter(Mandatory)][ValidateSet('RED','GREEN')][string]$Expected
  )
  $ErrorActionPreference = 'Stop'
  $Allowed = @{
    'T1-RED' = @('./internal/runtime','^TestProductionGenerationValidatingCheckpoint$','RED')
    'T1-GREEN' = @('./internal/runtime','^TestProductionGenerationValidatingCheckpoint$','GREEN')
    'T1-COMPAT' = @('./internal/runtime','^TestProductionGenerationValidation(RetainsOnlyExecutedCoverageGain|FailureRetainsNothing)$','GREEN')
    'T2-RED' = @('./internal/runtime','^TestProductionGeneration(CompletedStageFlow|CheckpointColdResume|ZeroCandidateColdResume|CheckpointManagedReview|CheckpointEvidenceGate|CheckpointContextGate|CheckpointConcurrentCAS|CheckpointCancelDuringRead|MinimizedIDCheckpointPolicy)$','RED')
    'T2-GREEN' = @('./internal/runtime','^TestProductionGeneration(CompletedStageFlow|CheckpointColdResume)$','GREEN')
    'T2-BOUNDARIES' = @('./internal/runtime','^TestProductionGeneration(ZeroCandidateColdResume|CheckpointManagedReview|CheckpointEvidenceGate|CheckpointContextGate|CheckpointConcurrentCAS|CheckpointCancelDuringRead|MinimizedIDCheckpointPolicy)$','GREEN')
    'T2-COMPAT' = @('./internal/runtime','^TestProductionGeneration(ValidationRetainsOnlyExecutedCoverageGain|ValidationFailureRetainsNothing|DriverResolvesAndRunsFixedStages|DriverFailsClosedBeforeAnyStageProcess|FailureBoundariesPreserveIdentity)$','GREEN')
    'T2-EXECUTOR' = @('./internal/runtime','^TestGenerationFailureWorker(BoundaryFixtures|CancellationAndClose|StageContextPrecedence)$','GREEN')
    'T2-COORD' = @('./internal/testgencoord','^Test(MinimizedRealCaseIDsCheckpointAndResume|CheckpointRejectsExistingArtifactChangedBetweenStages)$','GREEN')
    'T2-STORE' = @('./internal/taskstore','^TestGeneration(RecoveryLeavesCompletedCheckpointIntact|RejectsTamperedCheckpointRecord)$','GREEN')
  }
  if (-not $Allowed.ContainsKey($Phase) -or $Package -cne $Allowed[$Phase][0] -or
      $Regex -cne $Allowed[$Phase][1] -or $Expected -cne $Allowed[$Phase][2]) {
    throw 'Phase is not an approved fixed test command'
  }
  $Attempt = Join-Path $Ledger ($Phase + '.attempt.json')
  $Stamp = [Text.Encoding]::UTF8.GetBytes(
    (@{Phase=$Phase;Package=$Package;Regex=$Regex;Expected=$Expected;
       Head=(git rev-parse HEAD).Trim();Utc=[DateTimeOffset]::UtcNow.ToString('o')} |
      ConvertTo-Json -Compress))
  $File = [IO.File]::Open($Attempt,[IO.FileMode]::CreateNew,
                         [IO.FileAccess]::Write,[IO.FileShare]::Read)
  try { $File.Write($Stamp) } finally { $File.Dispose() }
  $Info = [Diagnostics.ProcessStartInfo]::new()
  $Info.FileName = 'D:/Program Files/Go/bin/go.exe'
  $Info.WorkingDirectory = $Module
  $Info.UseShellExecute = $false
  $Info.CreateNoWindow = $true
  $Info.RedirectStandardOutput = $true
  $Info.RedirectStandardError = $true
  foreach ($Argument in @('test',$Package,'-count=1','-timeout=30s','-json',
                          '-run',$Regex)) { $Info.ArgumentList.Add($Argument) }
  foreach ($Entry in @{GOWORK='off';GOPROXY='off';GOSUMDB='off';
                       GOTOOLCHAIN='local';GOFLAGS='-mod=readonly'}.GetEnumerator()) {
    $Info.Environment[$Entry.Key] = $Entry.Value
  }
  $Process = [Diagnostics.Process]::new()
  $Process.StartInfo = $Info
  $Natural = $false
  $Drained = $false
  $Started = $false
  try {
    $Started = $Process.Start()
    if (-not $Started) { throw 'Go launch failed' }
    $Held = $Process.Handle
    $Birth = $Process.StartTime.ToUniversalTime().Ticks
    $Out = $Process.StandardOutput.ReadToEndAsync()
    $Err = $Process.StandardError.ReadToEndAsync()
    $Natural = $Process.WaitForExit(60000)
    if (-not $Natural) {
      if ($Process.StartTime.ToUniversalTime().Ticks -eq $Birth) {
        $Process.Kill($true)
        if (-not $Process.WaitForExit(5000)) { throw 'Owned Go exit unconfirmed' }
      }
      throw 'Bounded test inconclusive; no automatic retry'
    }
    $Drained = [Threading.Tasks.Task]::WaitAll(
      [Threading.Tasks.Task[]]@($Out,$Err),5000)
    if (-not $Drained) { throw 'Go output not drained' }
    $Stdout = $Out.GetAwaiter().GetResult()
    $Stderr = $Err.GetAwaiter().GetResult()
    [IO.File]::WriteAllText((Join-Path $Ledger ($Phase+'.stdout.jsonl')),$Stdout)
    [IO.File]::WriteAllText((Join-Path $Ledger ($Phase+'.stderr.log')),$Stderr)
    $Rows = @($Stdout -split '\r?\n' | Where-Object { $_ } |
      ForEach-Object { $_ | ConvertFrom-Json })
    $Starts = @($Rows | Where-Object {
      $_.Action -eq 'run' -and $_.Test -match $Regex -and $_.Test -notmatch '/'
    })
    $Passes = @($Rows | Where-Object {
      $_.Action -eq 'pass' -and $_.Test -match $Regex -and $_.Test -notmatch '/'
    })
    $Failures = @($Rows | Where-Object {
      $_.Action -eq 'fail' -and $_.Test -match $Regex -and $_.Test -notmatch '/'
    })
    if ($Starts.Count -eq 0) { throw 'No selected test actually ran' }
    if ($Expected -eq 'RED') {
      if ($Process.ExitCode -eq 0 -or $Failures.Count -eq 0) {
        throw 'No behavior RED'
      }
    } elseif ($Process.ExitCode -ne 0 -or $Failures.Count -ne 0 -or
              $Passes.Count -ne $Starts.Count) {
      throw 'Focused GREEN failed; stop'
    }
    [pscustomobject]@{Phase=$Phase;Natural=$Natural;Drained=$Drained;
      ExitCode=$Process.ExitCode;Roots=$Starts.Count;Passed=$Passes.Count;
      Failed=$Failures.Count;HandleHeld=($Held -ne [IntPtr]::Zero)}
  } finally {
    if ($Started -and -not $Process.HasExited) {
      # Process 对象由当前 Start 创建并持有；不重新查找或终止任意 PID。
      $Process.Kill($true)
      if (-not $Process.WaitForExit(5000)) { throw 'Owned Go cleanup exit unconfirmed' }
    }
    $Process.Dispose()
  }
}
~~~

将运行器与保护检查组装为新私有控制器时，Package 和 Regex 必须与各 Phase 的下列固定命令相符，不能接受任意 shell 或扩大的测试选择器。每个 Phase 凭据只能使用一次；无变更的失败不重新跑。预期 RED 仍必须检查具体断言，超时、编译错误、环境错误和 panic 不算有效 RED。Go 的六十秒外层等待通过工具的十秒 yield 分段返回，工作期间持续提供简短进度。

## Task 1 验证结束只提交 validating

**Files:**

- Create: apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go
- Modify: apps/test-service/internal/runtime/production_generation_validation.go，仅两处 Next 返回值
- Modify: apps/test-service/internal/runtime/production_generation_validation_test.go，仅验证返回状态的两处旧断言
- Private execution files: 新实施目录内的准备、保护和运行器，不入产品提交

**Interfaces:**

- Consumes: 现有 productionValidationFixture、productionGenerationDriver.RunStage、generationService.coord.CheckpointWithRecord、artifactstore.New、taskstore.Open。
- Produces: Validate(..., productionPipelineResult) 保留与不保留路径均返回 Next: StateValidating；保留路径原候选与两个产物不变。
- Produces for Task 2: stageCheckpointHarness、newStageCheckpointHarness、rendering、checkpoint、validate、reopen、statePath 与线程安全测试边界，以下完整夹具定义这些名字。

- [ ] **Step 1 编写真实适配器与 SQLite 回归**

新测试文件的 package 为 runtime；导入 context、encoding/json、path/filepath、reflect、strings、sync、testing、time，以及 artifactstore、task、taskstore、testgendomain、testgenvalidate 和 generationv15 协议包。只使用下面列出的已有夹具，不替换生产校验适配器。测试夹具的计数锁用于 Task 2 并发测试。

~~~go
type stageCheckpointResolver struct {
    mu sync.Mutex
    target generationTarget
    calls int
}
func (r *stageCheckpointResolver) Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
    return generationv15.TestGenerationTargetListV15{}, nil
}
func (r *stageCheckpointResolver) ResolveStart(ctx context.Context, _ generationv15.TestGenerationStartRequestV15) (generationTarget, error) {
    return r.ResolveRequest(ctx, r.target.request)
}
func (r *stageCheckpointResolver) ResolveRequest(ctx context.Context, request testgendomain.Request) (generationTarget, error) {
    if err := ctx.Err(); err != nil { return generationTarget{}, err }
    r.mu.Lock()
    defer r.mu.Unlock()
    r.calls++
    if !reflect.DeepEqual(request, r.target.request) {
        return generationTarget{}, testgendomain.ErrStaleSnapshot
    }
    return r.target, nil
}
type stageCheckpointAuthority struct {
    mu sync.Mutex
    *productionValidationAuthorityFixture
}
func (a *stageCheckpointAuthority) Prepare(ctx context.Context, b productionValidationBinding) (productionValidationInput, error) {
    if err := ctx.Err(); err != nil { return productionValidationInput{}, err }
    a.mu.Lock()
    defer a.mu.Unlock()
    return a.productionValidationAuthorityFixture.Prepare(ctx, b)
}
func (a *stageCheckpointAuthority) Verify(ctx context.Context, b productionValidationBinding, r []testgenvalidate.StageReceipt) error {
    if err := ctx.Err(); err != nil { return err }
    a.mu.Lock()
    defer a.mu.Unlock()
    return a.productionValidationAuthorityFixture.Verify(ctx, b, r)
}
type stageCheckpointHarness struct {
    data string
    meta *taskstore.Store
    content *artifactstore.Store
    adapter *productionGenerationValidation
    driver *productionGenerationDriver
    service *generationService
    target generationTarget
    pipeline productionPipelineResult
    authority *stageCheckpointAuthority
    publisher *generationPublisherFixture
}
func newStageCheckpointHarness(t *testing.T) *stageCheckpointHarness {
    t.Helper()
    a, _, target, pipeline, authority, _ := productionValidationFixture(t)
    target.request.ManagedGapID = ""
    target.request.SessionOwnerDigest = strings.Repeat("a",64)
    target.request.Budgets.WallTimeMS = 60000
    target.wallTime = time.Minute
    h := &stageCheckpointHarness{data:t.TempDir(),adapter:a,target:target,
        pipeline:pipeline,authority:&stageCheckpointAuthority{productionValidationAuthorityFixture:authority},
        publisher:&generationPublisherFixture{complete:true}}
    var err error
    h.meta, err = taskstore.Open(filepath.Join(h.data,"history.sqlite3"))
    if err != nil { t.Fatal(err) }
    h.content, err = artifactstore.New(filepath.Join(h.data,"artifacts"))
    if err != nil { _ = h.meta.Close(); t.Fatal(err) }
    h.adapter.authority = h.authority
    h.adapter.artifacts = runtimeProductionGenerationArtifacts{metadata:h.meta,content:h.content}
    h.connect(t)
    t.Cleanup(func() {
        if h.service != nil { h.service.Close() }
        if h.content != nil { _ = h.content.Close() }
        if h.meta != nil { _ = h.meta.Close() }
    })
    return h
}
func (h *stageCheckpointHarness) connect(t *testing.T) {
    t.Helper()
    h.driver = newProductionGenerationDriver(&stageCheckpointResolver{target:h.target},nil,h.adapter)
    var err error
    h.service, err = newGenerationService(GenerationServiceConfig{
        Store:h.meta,Driver:h.driver,Publisher:h.publisher,Trusted:true,CoverageReady:true,
        VerifySnapshot:func(ctx context.Context,r testgendomain.Request)(testgendomain.SnapshotIdentity,error) {
            if err := ctx.Err(); err != nil { return testgendomain.SnapshotIdentity{},err }
            if !reflect.DeepEqual(r,h.target.request) { return testgendomain.SnapshotIdentity{},testgendomain.ErrStaleSnapshot }
            return r.SnapshotIdentity(),nil
        },
        VerifyArtifact:func(ctx context.Context,a task.Artifact) error {
            switch a.Kind {
            case "test-generation-source": return h.content.VerifyGenerationSource(ctx,a)
            case "test-generation-evidence": return h.content.VerifyGenerationEvidence(ctx,a)
            default: return task.ErrInvalidArgument
            }
        },
        VerifyProcess:func(ctx context.Context,_ string,_ string) error { return ctx.Err() },
    })
    if err != nil { t.Fatal(err) }
}
func (h *stageCheckpointHarness) checkpoint(t *testing.T,r testgendomain.Run,result GenerationStageResult,stage testgendomain.State) testgendomain.Run {
    t.Helper()
    amount := h.driver.StageBudget(r,stage)
    amount.Events = 1
    if stage == testgendomain.StateBaseline { amount.Events = 2 }
    record := r.Record
    record.BudgetUsed.Candidates += amount.Candidates
    record.BudgetUsed.OutputBytes += amount.OutputBytes
    record.BudgetUsed.Artifacts += amount.Artifacts
    record.BudgetUsed.Events += amount.Events
    if result.MinimizedCaseIDs != nil { record.MinimizedCaseIDs = append([]string(nil),result.MinimizedCaseIDs...) }
    saved,err := h.service.coord.CheckpointWithRecord(context.Background(),r.ID,r.Revision,result.Next,result.Candidates,result.Artifacts,record)
    if err != nil { t.Fatal(err) }
    return saved
}
func (h *stageCheckpointHarness) rendering(t *testing.T) testgendomain.Run {
    t.Helper()
    r,err := h.service.coord.Start(context.Background(),h.target.request)
    if err != nil { t.Fatal(err) }
    for _,stage := range []testgendomain.State{
        testgendomain.StateBaseline,testgendomain.StateAnalyzing,
        testgendomain.StateSolving,testgendomain.StateRendering,
    } { r = h.checkpoint(t,r,GenerationStageResult{Next:stage},stage) }
    h.driver.cache[r.ID] = cachedProductionPipeline{request:r.Request,result:h.pipeline}
    return r
}
func (h *stageCheckpointHarness) validate(t *testing.T,r testgendomain.Run) testgendomain.Run {
    t.Helper()
    result,err := h.driver.RunStage(context.Background(),r,testgendomain.StateValidating,func(ctx context.Context) error {
        return h.service.coord.AuthorizeProcessLaunch(ctx,r.ID)
    })
    if err != nil { t.Fatal(err) }
    if result.Next != testgendomain.StateValidating { t.Fatalf("validation skipped checkpoint: %s",result.Next) }
    return h.checkpoint(t,r,result,testgendomain.StateValidating)
}
func (h *stageCheckpointHarness) reopen(t *testing.T) {
    t.Helper()
    h.service.Close()
    if err := h.content.Close(); err != nil { t.Fatal(err) }
    if err := h.meta.Close(); err != nil { t.Fatal(err) }
    var err error
    h.meta,err = taskstore.Open(filepath.Join(h.data,"history.sqlite3"))
    if err != nil { t.Fatal(err) }
    h.content,err = artifactstore.New(filepath.Join(h.data,"artifacts"))
    if err != nil { t.Fatal(err) }
    h.adapter = newProductionGenerationValidation(h.adapter.validator,h.authority,
        runtimeProductionGenerationArtifacts{metadata:h.meta,content:h.content})
    h.connect(t)
    if len(h.driver.cache) != 0 || len(h.adapter.records) != 0 || h.driver.pipeline != nil {
        t.Fatal("restart reused pipeline or adapter state")
    }
}
func statePath(t *testing.T,s *taskstore.Store,id string) []testgendomain.State {
    t.Helper()
    events,err := s.ReplayGenerationEvents(context.Background(),id,0,200)
    if err != nil { t.Fatal(err) }
    var path []testgendomain.State
    for _,e := range events {
        var value map[string]string
        if json.Unmarshal(e.Payload,&value) == nil && value["from"] != "" {
            from,to := testgendomain.State(value["from"]),testgendomain.State(value["to"])
            if !testgendomain.ValidTransition(from,to) { t.Fatal("invalid durable transition") }
            path = append(path,to)
        }
    }
    return path
}
func TestProductionGenerationValidatingCheckpoint(t *testing.T) {
    for _,retain := range []bool{true,false} {
        t.Run(map[bool]string{true:"retained",false:"not-retained"}[retain],func(t *testing.T) {
            h := newStageCheckpointHarness(t)
            if !retain { h.authority.planner.fail = testgenvalidate.StageCompile }
            r := h.validate(t,h.rendering(t))
            want := 0
            if retain { want = 1 }
            if r.State != testgendomain.StateValidating || r.CandidateCount != want ||
               len(r.ArtifactDigests) != 2*want || r.Record.Preview != nil ||
               len(r.Record.MinimizedCaseIDs) != 0 { t.Fatal("validating checkpoint differs") }
            candidates,err := h.meta.ListGenerationCandidates(context.Background(),r.ID)
            if err != nil || len(candidates) != want { t.Fatal("candidate not durably bound") }
            row,err := h.meta.Get(context.Background(),r.TaskID)
            if err != nil || row.ErrorCode != "" { t.Fatal("validation became infrastructure failure") }
        })
    }
}
~~~

testgenvalidate 用于阶段常量及 StageReceipt。Task 1 的 checkpoint 是检查生产返回契约与真实存储事务的夹具，不代替 Task 2 的真实 generationService.run 预算与预览执行验证。

- [ ] **Step 2 取得行为 RED**

~~~powershell
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T1-RED' -Package './internal/runtime' -Regex '^TestProductionGenerationValidatingCheckpoint$' -Expected RED
~~~

应实际运行 retained 和 not-retained，两者分别报 validation skipped checkpoint: minimizing 和 rejected。不能修改夹具让适配器返回预设 validating。

- [ ] **Step 3 最小修改两处返回与旧状态断言**

production_generation_validation.go 的不保留分支清理记录后返回：

~~~go
return GenerationStageResult{Next: testgendomain.StateValidating}, nil
~~~

成功分支保留原 candidate 与两个 artifact，仅改变 Next：

~~~go
return GenerationStageResult{Next:testgendomain.StateValidating,
    Candidates:[]testgendomain.Candidate{candidate},
    Artifacts:[]task.Artifact{artifact,evidenceArtifact}},nil
~~~

production_generation_validation_test.go 的 TestProductionGenerationValidationRetainsOnlyExecutedCoverageGain 和 TestProductionGenerationValidationFailureRetainsNothing 分别将 Next 判定改为 StateValidating；其他覆盖率、产物数量、执行凭据及未写工作区的断言不删除。此时先不改 Minimize。

- [ ] **Step 4 相同选择器 GREEN 与相邻契约**

~~~powershell
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T1-GREEN' -Package './internal/runtime' -Regex '^TestProductionGenerationValidatingCheckpoint$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T1-COMPAT' -Package './internal/runtime' -Regex '^TestProductionGenerationValidation(RetainsOnlyExecutedCoverageGain|FailureRetainsNothing)$' -Expected GREEN
~~~

同步本任务三个 Owned 文件后才运行；确认真实测试名、两个子分支、保存后的 validating 及错误码为空。记录 source SHA 与自然退出，不称全流程已修复。

- [ ] **Step 5 定点审查并本地提交**

~~~powershell
git add -- apps/test-service/internal/runtime/production_generation_validation.go apps/test-service/internal/runtime/production_generation_validation_test.go apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m "fix(runtime): checkpoint completed production validation"
~~~

审查范围只含上述三文件。RED、GREEN 与兼容证据及保护核对成功才提交。

## Task 2 持久化最小化与独立预览

**Files:**

- Create: apps/test-service/internal/runtime/production_generation_checkpoint_evidence.go
- Create: apps/test-service/internal/runtime/production_generation_checkpoint_stages.go
- Create: apps/test-service/internal/runtime/production_generation_checkpoint_security_test.go
- Modify: apps/test-service/internal/runtime/production_generation_driver.go
- Modify: apps/test-service/internal/runtime/production_generation_validation.go，移除旧 Minimize
- Modify: apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go，增加下列测试
- Modify: apps/test-service/internal/runtime/production_generation_validation_test.go，更新直接 Minimize 用法
- Modify: apps/test-service/internal/runtime/production_generation_driver_test.go，更新夹具内部接口
- Modify: apps/test-service/internal/runtime/production_generation_failure_test.go，更新夹具内部接口及 Preview 错误用例

**Interfaces:**

- Consumes: Task 1 的真实 validating 检查点、stageCheckpointHarness、SQLite 已有 ListGenerationCandidates、原 loadRecord 与 ValidateCandidate。
- Produces: Minimize(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error)。
- Produces: Preview(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error)。
- Produces: checkpointRecord(context.Context,testgendomain.Run,generationTarget,testgendomain.State) (productionValidatedRecord,bool,error)，bool 表示存在唯一正候选。
- Produces: runtimeProductionGenerationArtifacts.ListGenerationCandidates，委托 metadata，不增加数据库 API。
- Minimize 返回 StateMinimizing 或可信零候选 StateRejected，不带最小化 ID；Preview 返回 StateAwaitingConfirmation、真实集合和 ID。失败返回空 GenerationStageResult 与原错误。

- [ ] **Step 1 添加现有接口可运行的全链路 RED**

先在 Task 1 测试文件追加以下两个主回归，并同时写入本任务 Step 5、Step 6 的完整测试和最小化 ID 策略测试，再取得 RED；此时不修改产品代码或内部接口。全部新测试只调用已有 driver.RunStage 与 service.run，新增 Preview 尚未声明也必须能编译。外部阶段由已有 planner 夹具执行，生产适配器不能替换。

~~~go
func TestProductionGenerationCompletedStageFlow(t *testing.T) {
    for _,retain := range []bool{true,false} {
        t.Run(map[bool]string{true:"retained",false:"not-retained"}[retain],func(t *testing.T) {
            h := newStageCheckpointHarness(t)
            if !retain { h.authority.planner.fail = testgenvalidate.StageCompile }
            r := h.rendering(t)
            ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
            defer cancel()
            h.service.run(ctx,r.ID)
            after,err := h.meta.GetGeneration(ctx,r.ID)
            if err != nil { t.Fatal(err) }
            want := testgendomain.StateRejected
            events := int64(7)
            if retain { want,events = testgendomain.StateAwaitingConfirmation,8 }
            if after.State != want || after.Record.BudgetUsed.Events != events {
                row,_ := h.meta.Get(ctx,r.TaskID)
                t.Fatalf("flow state=%s diagnostic=%s",after.State,row.ErrorCode)
            }
            if after.Record.BudgetUsed.Candidates != r.Request.Budgets.CandidateCount {
                t.Fatal("candidate budget was repeated")
            }
            if retain {
                if after.Record.Preview == nil || len(after.Record.MinimizedCaseIDs) != 1 ||
                   h.publisher.plans != 1 || h.publisher.accepts != 0 ||
                   len(h.authority.planner.stages) != 6 { t.Fatal("preview or validation repeated") }
            } else if after.CandidateCount != 0 || len(after.ArtifactDigests) != 0 ||
                      h.publisher.plans != 0 || h.publisher.accepts != 0 {
                t.Fatal("rejection retained or published data")
            }
            path := statePath(t,h.meta,r.ID)
            tail := []testgendomain.State{testgendomain.StateValidating,testgendomain.StateRejected}
            if retain { tail = []testgendomain.State{testgendomain.StateValidating,
                testgendomain.StateMinimizing,testgendomain.StateAwaitingConfirmation} }
            if len(path)<len(tail) || !reflect.DeepEqual(path[len(path)-len(tail):],tail) {
                t.Fatal("completed stage path skipped")
            }
        })
    }
}
func TestProductionGenerationCheckpointColdResume(t *testing.T) {
    for _,completed := range []testgendomain.State{testgendomain.StateValidating,testgendomain.StateMinimizing} {
        t.Run(string(completed),func(t *testing.T) {
            h := newStageCheckpointHarness(t)
            r := h.validate(t,h.rendering(t))
            if completed == testgendomain.StateMinimizing {
                result,err := h.driver.RunStage(context.Background(),r,testgendomain.StateMinimizing,nil)
                if err != nil { t.Fatal(err) }
                r = h.checkpoint(t,r,result,testgendomain.StateMinimizing)
            }
            beforeStages := len(h.authority.planner.stages)
            h.reopen(t)
            ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
            defer cancel()
            if _,err := h.service.coord.Resume(ctx,r.TaskID); err != nil { t.Fatal(err) }
            h.service.run(ctx,r.ID)
            after,err := h.meta.GetGeneration(ctx,r.ID)
            if err != nil || after.State != testgendomain.StateAwaitingConfirmation ||
               len(after.Record.MinimizedCaseIDs)!=1 || after.Record.Preview==nil ||
               len(h.authority.planner.stages)!=beforeStages || h.publisher.plans!=1 ||
               len(h.driver.cache)!=0 { t.Fatal("cold checkpoint depended on generation cache") }
        })
    }
}
~~~

冷恢复夹具重开实际数据库和 artifactstore，创建新 driver 与 adapter，pipeline 为 nil，cache 均为空。固定测试权威的预期断言仍可留存；它不是产品运行候选缓存。

~~~powershell
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-RED' -Package './internal/runtime' -Regex '^TestProductionGeneration(CompletedStageFlow|CheckpointColdResume|ZeroCandidateColdResume|CheckpointManagedReview|CheckpointEvidenceGate|CheckpointContextGate|CheckpointConcurrentCAS|CheckpointCancelDuringRead|MinimizedIDCheckpointPolicy)$' -Expected RED
~~~

保留路径应在 minimizing 提前返回 awaiting_confirmation 被拒绝；不保留路径和冷恢复也不能到达预期状态。上下文及存储 ID 策略是既有约束，允许在 RED 阶段已经通过。必须确认两个主回归实际运行并因行为错误失败，不能把缺少方法、编译错误或单个通过子用例算作 RED。

- [ ] **Step 2 实现严格持久化读取辅助文件**

production_generation_checkpoint_evidence.go 使用 package runtime，导入 context、reflect、sort、testgendomain 和 task。复用现有 productionManagedCandidateReader 接口，不新增重叠接口。

~~~go
func (a runtimeProductionGenerationArtifacts) ListGenerationCandidates(ctx context.Context,id string) ([]testgendomain.Candidate,error) {
    if ctx==nil || a.metadata==nil { return nil,task.ErrStorageUnavailable }
    return a.metadata.ListGenerationCandidates(ctx,id)
}
func checkpointTargetMatches(record productionValidatedRecord,target generationTarget) bool {
    b := record.binding
    if b.ProjectID!=target.projectID || b.WorkspaceGeneration!=target.workspaceGeneration ||
       b.CoverageReportID!=target.coverageReportID || b.BaselineReportDigest!=target.request.BaselineReportDigest ||
       b.SourceRelativePath!=target.sourceRelativePath || b.SourceDigest!=target.sourceDigest ||
       record.set.TestTarget!=target.renderTarget.TestTarget ||
       record.set.ProductionTarget!=target.renderTarget.ProductionTarget ||
       record.set.FrameworkTarget!=target.renderTarget.FrameworkTarget ||
       !validProductionDigest(record.set.SymbolID) || record.candidate.Kind!=b.Kind {
        return false
    }
    selected := target.selectedFunctions()
    ids := make([]string,0,len(selected))
    knownSymbols := make([]string,0,len(selected))
    allKnown := true
    for _,f := range selected {
        ids = append(ids,f.functionID)
        if !validProductionDigest(f.gap.SymbolID) { allKnown=false
        } else { knownSymbols=append(knownSymbols,f.gap.SymbolID) }
    }
    sort.Strings(ids)
    sort.Strings(knownSymbols)
    if !reflect.DeepEqual(ids,b.CoverageFunctionIDs) ||
       len(b.TargetFunctionIDs)!=len(ids) { return false }
    previous := ""
    for _,id := range b.TargetFunctionIDs {
        if !validProductionDigest(id) || id<=previous { return false }
        previous=id
    }
    if allKnown && !reflect.DeepEqual(knownSymbols,b.TargetFunctionIDs) { return false }
    symbol := "fn:"+record.set.SymbolID
    if target.request.Scope==testgendomain.ScopeFile { symbol="file:"+target.fileID }
    return b.TargetSymbol==symbol && record.candidate.TargetSymbol==symbol
}
func (a *productionGenerationValidation) checkpointRecord(ctx context.Context,run testgendomain.Run,target generationTarget,state testgendomain.State) (productionValidatedRecord,bool,error) {
    if ctx==nil { return productionValidatedRecord{},false,task.ErrInvalidArgument }
    if err:=ctx.Err(); err!=nil { return productionValidatedRecord{},false,err }
    if !a.Ready() || !target.valid() || !reflect.DeepEqual(run.Request,target.request) ||
       run.State!=state || testgendomain.ValidateRun(run)!=nil ||
       run.Record.IsZero() || !run.Record.ValidFor(run.Request,run.CandidateCount) ||
       run.Record.Preview!=nil || len(run.Record.MinimizedCaseIDs)!=0 ||
       run.Record.BudgetUsed.Candidates!=run.Request.Budgets.CandidateCount {
        return productionValidatedRecord{},false,errProductionValidationUnavailable
    }
    reader,ok := a.artifacts.(productionManagedCandidateReader)
    if !ok { return productionValidatedRecord{},false,task.ErrStorageUnavailable }
    candidates,err := reader.ListGenerationCandidates(ctx,run.ID)
    if err!=nil { return productionValidatedRecord{},false,err }
    if err:=ctx.Err(); err!=nil { return productionValidatedRecord{},false,err }
    if len(candidates)!=run.CandidateCount || len(candidates)>1 {
        return productionValidatedRecord{},false,errProductionValidationUnavailable
    }
    if len(candidates)==0 {
        if state!=testgendomain.StateValidating || len(run.ArtifactDigests)!=0 {
            return productionValidatedRecord{},false,errProductionValidationUnavailable
        }
        a.mu.Lock()
        delete(a.records,run.ID)
        a.mu.Unlock()
        return productionValidatedRecord{},false,nil
    }
    if len(run.ArtifactDigests)!=2 || testgendomain.ValidateCandidate(candidates[0])!=nil {
        return productionValidatedRecord{},false,errProductionValidationUnavailable
    }
    record,err := a.loadRecord(ctx,run,candidates[0])
    if err!=nil { return productionValidatedRecord{},false,err }
    if record.artifact.ID==record.evidence.ID || !checkpointTargetMatches(record,target) {
        return productionValidatedRecord{},false,errProductionValidationUnavailable
    }
    if err:=a.ValidateCandidate(ctx,run,candidates[0]); err!=nil {
        return productionValidatedRecord{},false,err
    }
    if err:=ctx.Err(); err!=nil { return productionValidatedRecord{},false,err }
    return record,true,nil
}
~~~

每次调用 loadRecord，即使缓存存在也走原严格证据恢复，再调用 ValidateCandidate 保留原凭据重验与源码校验。拒绝路径要求真实候选查询为空、记录显示本次验证预算已经提交、无残留生成产物，不靠缓存判定。

真实 resolver 可能仅提供 linkage、在分析后才得到 IR symbol。此时用既有可信 CoverageFunctionIDs、当前源码及原 authority.Verify 核验，不为恢复重新分析或编造 symbol；已知 symbol 的情况额外要求精确集合匹配。

- [ ] **Step 3 分开最小化和预览操作**

从 production_generation_validation.go 删除旧 Minimize 整个方法，不改其他恢复与验证方法。在新 production_generation_checkpoint_stages.go 中导入 context、testgendomain，并写入：

~~~go
func (a *productionGenerationValidation) Minimize(ctx context.Context,run testgendomain.Run,target generationTarget) (GenerationStageResult,error) {
    _,retained,err := a.checkpointRecord(ctx,run,target,testgendomain.StateValidating)
    if err!=nil { return GenerationStageResult{},err }
    if !retained { return GenerationStageResult{Next:testgendomain.StateRejected},nil }
    return GenerationStageResult{Next:testgendomain.StateMinimizing},nil
}
func (a *productionGenerationValidation) Preview(ctx context.Context,run testgendomain.Run,target generationTarget) (GenerationStageResult,error) {
    record,retained,err := a.checkpointRecord(ctx,run,target,testgendomain.StateMinimizing)
    if err!=nil { return GenerationStageResult{},err }
    if !retained { return GenerationStageResult{},errProductionValidationUnavailable }
    set := cloneProductionCandidateSet(record.set)
    return GenerationStageResult{Next:testgendomain.StateAwaitingConfirmation,
        MinimizedCaseIDs:[]string{record.candidate.CaseID},PreviewSet:&set},nil
}
~~~

唯一候选规则已是现有生产行为，本轮没有新的覆盖率最小集合算法。ID 不提前写入 run.Record，PreviewSet 不是空占位，候选及产物不再提交一次。

- [ ] **Step 4 驱动在生成之前路由后期阶段**

修改 productionGenerationValidator 中 Minimize 的签名为三参数，增加同签名 Preview。其他 Ready、Validate、ValidateCandidate、CandidateSet 保留。

在 RunStage 完成 ctx 与 target 校验之后、driver.generate 之前插入：

~~~go
if err:=ctx.Err(); err!=nil { return GenerationStageResult{},err }
switch next {
case testgendomain.StateBaseline:
    return GenerationStageResult{Next:next},nil
case testgendomain.StateMinimizing:
    if driver.validator==nil || !driver.validator.Ready() {
        return GenerationStageResult{},withGenerationOperation("minimize_candidates",task.ErrStorageUnavailable)
    }
    value,err := driver.validator.Minimize(ctx,run,target)
    return value,withGenerationOperation("minimize_candidates",err)
case testgendomain.StateAwaitingConfirmation:
    if driver.validator==nil || !driver.validator.Ready() {
        return GenerationStageResult{},withGenerationOperation("preview_set",task.ErrStorageUnavailable)
    }
    value,err := driver.validator.Preview(ctx,run,target)
    return value,withGenerationOperation("preview_set",err)
case testgendomain.StateAnalyzing,testgendomain.StateSolving,
     testgendomain.StateRendering,testgendomain.StateValidating:
    // 这些阶段继续进入已有 generate 和 validating 授权逻辑。
default:
    return GenerationStageResult{},task.ErrInvalidArgument
}
~~~

删除后面旧 switch 的 StateMinimizing 分支及重复 baseline 分支，保留 generate、分析求解渲染返回和 validating 的 authorize 与 Validate。没有新增公开错误操作；preview_set 已在 FailureDiagnostic 闭合列表内。不要以设置返回值 Next 的方式容忍任意非法适配器结果。

更新 production_generation_driver_test.go：

~~~go
func (*productionValidatorFixture) Minimize(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error) {
    return GenerationStageResult{Next:testgendomain.StateMinimizing},nil
}
func (*productionValidatorFixture) Preview(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error) {
    return GenerationStageResult{Next:testgendomain.StateAwaitingConfirmation,
        MinimizedCaseIDs:[]string{},PreviewSet:&testgenpublish.CandidateSet{}},nil
}
~~~

这是旧驱动接口夹具的兼容修改；全流程回归仍用真实生产适配器，不能用这个空集合夹具替代它。

更新 diagnosticProductionValidator：

~~~go
func (v *diagnosticProductionValidator) Minimize(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error) {
    return GenerationStageResult{},v.cause
}
func (v *diagnosticProductionValidator) Preview(context.Context,testgendomain.Run,generationTarget) (GenerationStageResult,error) {
    return GenerationStageResult{},v.cause
}
~~~

在现有 TestProductionGenerationFailureBoundariesPreserveIdentity 表中增加 {"preview","preview_set",StateAwaitingConfirmation,task.ErrConflict}，验证错误身份、闭合诊断及不泄露原错误。其现有 resolver、authorize、pipeline、validate 和 minimize 用例不删除。

旧直接适配器测试在调用 Minimize 前设置可信测试检查点，再测试独立 Preview。为现有内存产物夹具提供以下只读候选包装，定义在新 stage_checkpoints_test.go：

~~~go
type stageFixtureCandidateStore struct {
    *productionGenerationArtifactFixture
    runID string
    values []testgendomain.Candidate
}
func (s stageFixtureCandidateStore) ListGenerationCandidates(ctx context.Context,id string) ([]testgendomain.Candidate,error) {
    if err:=ctx.Err(); err!=nil { return nil,err }
    if id!=s.runID { return nil,task.ErrNotFound }
    result := make([]testgendomain.Candidate,len(s.values))
    for i,c:=range s.values { result[i]=testgendomain.CloneCandidate(c) }
    return result,nil
}
~~~

将旧 TestProductionGenerationValidationRetainsOnlyExecutedCoverageGain 中原 Minimize 至产物引用追加的部分改为以下段落，原后续冷恢复和损坏测试保留：

~~~go
run.State,run.Revision,run.CandidateCount = testgendomain.StateValidating,6,1
run.Record = testgendomain.NewGenerationRecord(run.Request)
run.Record.BudgetUsed.Candidates = run.Request.Budgets.CandidateCount
for _,artifact:=range result.Artifacts {
    run.ArtifactDigests=append(run.ArtifactDigests,testgendomain.ArtifactRef{ID:artifact.ID,Digest:artifact.SHA256})
}
adapter.artifacts=stageFixtureCandidateStore{productionGenerationArtifactFixture:artifacts,runID:run.ID,values:result.Candidates}
minimized,err:=adapter.Minimize(context.Background(),run,target)
if err!=nil || minimized.Next!=testgendomain.StateMinimizing ||
   minimized.MinimizedCaseIDs!=nil || minimized.PreviewSet!=nil {
    t.Fatalf("minimize result=%+v err=%v",minimized,err)
}
run.State,run.Revision=testgendomain.StateMinimizing,7
preview,err:=adapter.Preview(context.Background(),run,target)
if err!=nil || preview.Next!=testgendomain.StateAwaitingConfirmation ||
   !reflect.DeepEqual(preview.MinimizedCaseIDs,[]string{candidate.CaseID}) ||
   preview.PreviewSet==nil { t.Fatalf("preview=%+v err=%v",preview,err) }
if err:=adapter.ValidateCandidate(context.Background(),run,candidate); err!=nil || authority.verify!=5 {
    t.Fatalf("candidate receipt not reverified: %v count=%d",err,authority.verify)
}
~~~

五次凭据重验包括两个 CandidateSet、Minimize、Preview 与显式 ValidateCandidate；新预览检查不能通过减少旧校验次数取得通过。

- [ ] **Step 5 在产品修改前写入恢复拒绝和 managed review 测试**

以下完整测试在 Step 1 的 RED 前写入 stage_checkpoints_test.go；走到此步骤只核对它们仍保留，不重复添加或重跑 RED：

~~~go
func TestProductionGenerationZeroCandidateColdResume(t *testing.T) {
    h:=newStageCheckpointHarness(t)
    h.authority.planner.fail=testgenvalidate.StageCompile
    r:=h.validate(t,h.rendering(t))
    h.reopen(t)
    ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
    defer cancel()
    if _,err:=h.service.coord.Resume(ctx,r.TaskID); err!=nil { t.Fatal(err) }
    h.service.run(ctx,r.ID)
    after,err:=h.meta.GetGeneration(ctx,r.ID)
    if err!=nil || after.State!=testgendomain.StateRejected ||
       after.CandidateCount!=0 || len(after.ArtifactDigests)!=0 ||
       h.publisher.plans!=0 { t.Fatal("zero candidate cold resume differs") }
}
func TestProductionGenerationCheckpointManagedReview(t *testing.T) {
    h:=newStageCheckpointHarness(t)
    h.service.Close()
    _,index,_,_,target,pipeline:=productionManagedFileFixture(t)
    target.request.Budgets=h.target.request.Budgets
    target.wallTime=time.Minute
    h.target,h.pipeline=target,pipeline
    h.connect(t)
    path,_,ok:=productionManagedSource(testgenpublish.CandidateSet{Files:pipeline.editSet.Files})
    if !ok { t.Fatal("managed source missing") }
    reviews:=&productionManagedReviewFixture{}
    materializer:=&productionManagedMaterializer{
        candidates:h.meta,evidence:h.adapter,
        current:productionManagedIndexFixture{index:index},
        preimages:&managedPublisherFixture{ready:true,preimages:map[string][]byte{path:[]byte{}}},
        registry:productionManagedRegistryFixture{},reviews:reviews,
    }
    h.driver.setManagedReviewFinalizer(materializer)
    r:=h.rendering(t)
    ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
    defer cancel()
    h.service.run(ctx,r.ID)
    after,err:=h.meta.GetGeneration(ctx,r.ID)
    if err!=nil || after.State!=testgendomain.StateAwaitingConfirmation ||
       reviews.write!=1 || !managedtest.ValidReviewDraft(reviews.draft) {
        t.Fatal("managed review not finalized after durable preview")
    }
    set,err:=materializer.ManagedCandidateSet(ctx,after,reviews.draft)
    if err!=nil || set.Managed==nil || len(set.Managed.Records)==0 {
        t.Fatal("maintainable managed cases absent")
    }
    for _,record:=range set.Managed.Records {
        if !managedtest.ValidRecord(record) { t.Fatal("invalid maintainable record") }
    }
}
~~~

增加 managedtest 和 testgenpublish 导入。测试接入真实 productionManagedMaterializer、真实已保存候选和验证适配器；review 写入边界使用既有受控夹具，不据此声称 UI、实际发布、安装包或 native 已通过。

- [ ] **Step 6 在产品修改前写入安全与并发定点测试**

以下 checkpoint_security_test.go 在 Step 1 的 RED 前写入；走到此步骤只核对它们仍保留，不重复添加或重跑 RED。导入 context、errors、os、path/filepath、reflect、strings、testing、time、task、testgendomain 和 testgenvalidate。每个子用例使用自己的 h 和数据库，不共享损坏的文件。阶段调用辅助函数只使用原 driver.RunStage 接口；在 RED 时不声明新的产品方法。

~~~go
func stageCheckpointCall(ctx context.Context,h *stageCheckpointHarness,r testgendomain.Run,next testgendomain.State) (GenerationStageResult,error) {
    h.driver.resolver=&stageCheckpointResolver{target:h.target}
    return h.driver.RunStage(ctx,r,next,nil)
}
func stageCheckpointInput(t *testing.T,h *stageCheckpointHarness,next testgendomain.State) testgendomain.Run {
    t.Helper()
    r:=h.validate(t,h.rendering(t))
    if next==testgendomain.StateAwaitingConfirmation {
        result,err:=stageCheckpointCall(context.Background(),h,r,testgendomain.StateMinimizing)
        if err!=nil || result.Next!=testgendomain.StateMinimizing ||
           result.MinimizedCaseIDs!=nil || result.PreviewSet!=nil {
            t.Fatalf("minimization skipped durable state: %+v %v",result,err)
        }
        r=h.checkpoint(t,r,result,testgendomain.StateMinimizing)
    }
    return r
}
func stageCheckpointArtifactFile(t *testing.T,h *stageCheckpointHarness,r testgendomain.Run,kind string) string {
    t.Helper()
    root:=filepath.Join(h.data,"artifacts")
    for _,ref:=range r.ArtifactDigests {
        artifact,err:=h.meta.GetArtifact(context.Background(),ref.ID)
        if err!=nil { t.Fatal(err) }
        if artifact.Kind!=kind { continue }
        file:=filepath.Join(root,filepath.FromSlash(artifact.RelativePath))
        relative,err:=filepath.Rel(root,file)
        if err!=nil || filepath.IsAbs(relative) || relative==".." ||
           strings.HasPrefix(relative,".."+string(filepath.Separator)) {
            t.Fatal("test mutation escaped owned artifacts")
        }
        info,err:=os.Lstat(file)
        if err!=nil || !info.Mode().IsRegular() { t.Fatal("owned regular artifact absent") }
        return file
    }
    t.Fatal("registered artifact kind absent")
    return ""
}
func TestProductionGenerationCheckpointEvidenceGate(t *testing.T) {
    mutations:=map[string]func(*stageCheckpointHarness,*testgendomain.Run){
        "count":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.CandidateCount=0},
        "unowned":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.ArtifactDigests=nil},
        "wrong-task":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.TaskID=strings.Repeat("f",32)},
        "wrong-run":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.ID=strings.Repeat("f",32)},
        "target":func(h *stageCheckpointHarness,_ *testgendomain.Run){h.target.sourceRelativePath="changed.c"},
        "wrong-state":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.State=testgendomain.StateRendering},
        "uncommitted-validation":func(_ *stageCheckpointHarness,r *testgendomain.Run){r.Record.BudgetUsed.Candidates=0},
        "stale-receipt":func(h *stageCheckpointHarness,_ *testgendomain.Run){
            h.authority.mu.Lock()
            h.authority.binding.SourceDigest=strings.Repeat("f",64)
            h.authority.mu.Unlock()
        },
    }
    for _,next:=range []testgendomain.State{testgendomain.StateMinimizing,testgendomain.StateAwaitingConfirmation} {
        for _,cold:=range []bool{false,true} {
            prefix:=string(next)+"/"+map[bool]string{false:"warm",true:"cold"}[cold]
            for name,mutate:=range mutations {
                t.Run(prefix+"/"+name,func(t *testing.T){
                    h:=newStageCheckpointHarness(t)
                    r:=stageCheckpointInput(t,h,next)
                    if cold { h.reopen(t) }
                    mutate(h,&r)
                    got,err:=stageCheckpointCall(context.Background(),h,r,next)
                    if err==nil || !reflect.DeepEqual(got,GenerationStageResult{}) {
                        t.Fatal("invalid checkpoint accepted")
                    }
                })
            }
            for _,kind:=range []string{"test-generation-source","test-generation-evidence"} {
                for _,missing:=range []bool{false,true} {
                    name:=prefix+"/"+kind+"/"+map[bool]string{false:"corrupt",true:"missing"}[missing]
                    t.Run(name,func(t *testing.T){
                        h:=newStageCheckpointHarness(t)
                        r:=stageCheckpointInput(t,h,next)
                        if cold { h.reopen(t) }
                        file:=stageCheckpointArtifactFile(t,h,r,kind)
                        var err error
                        if missing { err=os.Remove(file) } else { err=os.WriteFile(file,[]byte("changed"),0600) }
                        if err!=nil { t.Fatal(err) }
                        got,err:=stageCheckpointCall(context.Background(),h,r,next)
                        if err==nil || !reflect.DeepEqual(got,GenerationStageResult{}) {
                            t.Fatal("missing or changed positive evidence accepted")
                        }
                    })
                }
            }
        }
    }
    t.Run("zero-with-contradictory-artifact",func(t *testing.T){
        h:=newStageCheckpointHarness(t)
        h.authority.planner.fail=testgenvalidate.StageCompile
        r:=h.validate(t,h.rendering(t))
        if r.CandidateCount!=0 || len(r.ArtifactDigests)!=0 { t.Fatal("not a clean zero checkpoint") }
        r.ArtifactDigests=[]testgendomain.ArtifactRef{{ID:strings.Repeat("f",32),Digest:strings.Repeat("f",64)}}
        got,err:=stageCheckpointCall(context.Background(),h,r,testgendomain.StateMinimizing)
        if err==nil || !reflect.DeepEqual(got,GenerationStageResult{}) { t.Fatal("contradiction became normal rejection") }
    })
}
func TestProductionGenerationCheckpointContextGate(t *testing.T) {
    for _,expired:=range []bool{false,true} {
        h:=newStageCheckpointHarness(t)
        r:=h.validate(t,h.rendering(t))
        var ctx context.Context
        var cancel context.CancelFunc
        want:=context.Canceled
        if expired {
            ctx,cancel=context.WithDeadline(context.Background(),time.Now().Add(-time.Second))
            want=context.DeadlineExceeded
        } else {
            ctx,cancel=context.WithCancel(context.Background())
            cancel()
        }
        got,err:=stageCheckpointCall(ctx,h,r,testgendomain.StateMinimizing)
        cancel()
        if !errors.Is(err,want) || got.Next!="" { t.Fatal("context identity lost") }
        r.State=testgendomain.StateMinimizing
        got,err=stageCheckpointCall(ctx,h,r,testgendomain.StateAwaitingConfirmation)
        if !errors.Is(err,want) || got.Next!="" || got.PreviewSet!=nil { t.Fatal("cancelled preview accepted") }
    }
}
func TestProductionGenerationCheckpointConcurrentCAS(t *testing.T) {
    h:=newStageCheckpointHarness(t)
    r:=h.validate(t,h.rendering(t))
    ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
    defer cancel()
    ready:=make(chan struct{},2)
    release:=make(chan struct{})
    results:=make(chan error,2)
    for i:=0;i<2;i++ {
        go func(){
            result,err:=h.driver.RunStage(ctx,r,testgendomain.StateMinimizing,nil)
            ready<-struct{}{}
            <-release
            if err==nil {
                record:=r.Record
                record.BudgetUsed.Events++
                _,err=h.service.coord.CheckpointWithRecord(ctx,r.ID,r.Revision,result.Next,
                    result.Candidates,result.Artifacts,record)
            }
            results<-err
        }()
    }
    for i:=0;i<2;i++ {
        select { case <-ready: case <-ctx.Done(): close(release);t.Fatal("bounded worker rendezvous failed") }
    }
    close(release)
    successes,conflicts:=0,0
    for i:=0;i<2;i++ {
        select {
        case err:=<-results:
            if err==nil {successes++} else if errors.Is(err,task.ErrConflict){conflicts++}else{t.Fatal(err)}
        case <-ctx.Done(): t.Fatal("bounded CAS workers did not finish")
        }
    }
    after,err:=h.meta.GetGeneration(ctx,r.ID)
    if err!=nil || after.State!=testgendomain.StateMinimizing ||
       after.Revision!=r.Revision+1 || successes!=1 || conflicts!=1 ||
       len(after.Record.MinimizedCaseIDs)!=0 || h.publisher.plans!=0 ||
       len(h.authority.planner.stages)!=6 { t.Fatal("duplicate progress or process execution") }
}
~~~

增加 reflect 导入。上述表同时覆盖两个后期操作的热、冷恢复及源码/证据缺失与篡改；Preview 先保存真实 minimizing，再注入异常，避免错误阶段掩盖证据检查。只删除所属新临时目录内的单个文件，不递归删除，不改旧工作区或数据库。

测试 stage 内取消也使用候选读取边界：定义下列 wrapper，生产方法不增加测试钩子。cancel 在读取之后触发，验证必须保留 context.Canceled 且不能返回成功结果。

~~~go
type stageCancelAfterRead struct {
    runtimeProductionGenerationArtifacts
    cancel context.CancelFunc
}
func (s stageCancelAfterRead) ListGenerationCandidates(ctx context.Context,id string)([]testgendomain.Candidate,error){
    result,err:=s.metadata.ListGenerationCandidates(ctx,id)
    s.cancel()
    return result,err
}
func TestProductionGenerationCheckpointCancelDuringRead(t *testing.T){
    h:=newStageCheckpointHarness(t)
    r:=h.validate(t,h.rendering(t))
    ctx,cancel:=context.WithCancel(context.Background())
    defer cancel()
    h.adapter.artifacts=stageCancelAfterRead{
        runtimeProductionGenerationArtifacts:runtimeProductionGenerationArtifacts{metadata:h.meta,content:h.content},
        cancel:cancel,
    }
    value,err:=h.driver.RunStage(ctx,r,testgendomain.StateMinimizing,nil)
    if !errors.Is(err,context.Canceled) || value.Next!="" {t.Fatal("read cancellation became success")}
    after,getErr:=h.meta.GetGeneration(context.Background(),r.ID)
    if getErr!=nil || after.Revision!=r.Revision || h.publisher.plans!=0 {t.Fatal("cancelled read changed checkpoint")}
}
~~~

并发 fixture 的计数必须受已提供的 resolver 与 authority 锁保护。不要用 time.Sleep 维持交错，不开启真实工具进程；CAS 的任意一个获胜，另外一个必须冲突。本计划不自动运行 -race 或 Linux/native；需要额外工具链的检测另行限定范围。

- [ ] **Step 7 定点 GREEN 与既有闭合约束**

先按 Task 2 同步完整 Owned，再运行：

~~~powershell
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-GREEN' -Package './internal/runtime' -Regex '^TestProductionGeneration(CompletedStageFlow|CheckpointColdResume)$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-BOUNDARIES' -Package './internal/runtime' -Regex '^TestProductionGeneration(ZeroCandidateColdResume|CheckpointManagedReview|CheckpointEvidenceGate|CheckpointContextGate|CheckpointConcurrentCAS|CheckpointCancelDuringRead|MinimizedIDCheckpointPolicy)$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-COMPAT' -Package './internal/runtime' -Regex '^TestProductionGeneration(ValidationRetainsOnlyExecutedCoverageGain|ValidationFailureRetainsNothing|DriverResolvesAndRunsFixedStages|DriverFailsClosedBeforeAnyStageProcess|FailureBoundariesPreserveIdentity)$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-EXECUTOR' -Package './internal/runtime' -Regex '^TestGenerationFailureWorker(BoundaryFixtures|CancellationAndClose|StageContextPrecedence)$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-COORD' -Package './internal/testgencoord' -Regex '^Test(MinimizedRealCaseIDsCheckpointAndResume|CheckpointRejectsExistingArtifactChangedBetweenStages)$' -Expected GREEN
RunStageTests -Module $Module -Ledger $Ledger -Phase 'T2-STORE' -Package './internal/taskstore' -Regex '^TestGeneration(RecoveryLeavesCompletedCheckpointIntact|RejectsTamperedCheckpointRecord)$' -Expected GREEN
~~~

T2-RED 选择全部九个新测试 root，在新增生产代码前取得行为 RED；Task 2 完整 GREEN 需覆盖全部新增安全测试，不能仅使用两个主回归宣告完成。严格存储、通用执行器与诊断测试不改断言。统计实际 root 与子用例，不预填通过数。

最小化 ID 策略使用独立测试，不插入 CompletedStageFlow，不提前代替其真实验证阶段。在 Step 1 的 RED 之前写入 stage_checkpoints_test.go：

~~~go
func TestProductionGenerationMinimizedIDCheckpointPolicy(t *testing.T) {
    h:=newStageCheckpointHarness(t)
    validated:=h.validate(t,h.rendering(t))
    candidates,err:=h.meta.ListGenerationCandidates(context.Background(),validated.ID)
    if err!=nil || len(candidates)!=1 {t.Fatal("missing positive candidate")}
    record:=validated.Record
    record.MinimizedCaseIDs=[]string{candidates[0].CaseID}
    _,err=h.service.coord.CheckpointWithRecord(context.Background(),validated.ID,validated.Revision,
        testgendomain.StateMinimizing,nil,nil,record)
    if err==nil {t.Fatal("database allowed early minimized IDs")}
    after,getErr:=h.meta.GetGeneration(context.Background(),validated.ID)
    if getErr!=nil || after.State!=validated.State || after.Revision!=validated.Revision ||
       len(after.Record.MinimizedCaseIDs)!=0 {t.Fatal("rejected early ID checkpoint changed state")}
}
~~~

CompletedStageFlow 的两个分支均从 rendering 运行真实执行器，完整经过实际验证；保留分支最终事件预算为八，拒绝分支为七。此独立策略测试的失败检查点不增加修订号或事件预算。

- [ ] **Step 8 范围审查与本地提交**

检查 Task 1、Task 2 全部保护、RED/GREEN、root 选择器、候选与事件预算、可维护 review、无重分析或重新验证以及取消/CAS。确认通用执行器、状态图、taskstore、发布器与旧证据字节未变。

~~~powershell
git add -- apps/test-service/internal/runtime/production_generation_driver.go apps/test-service/internal/runtime/production_generation_validation.go apps/test-service/internal/runtime/production_generation_checkpoint_evidence.go apps/test-service/internal/runtime/production_generation_checkpoint_stages.go apps/test-service/internal/runtime/production_generation_stage_checkpoints_test.go apps/test-service/internal/runtime/production_generation_checkpoint_security_test.go apps/test-service/internal/runtime/production_generation_validation_test.go apps/test-service/internal/runtime/production_generation_driver_test.go apps/test-service/internal/runtime/production_generation_failure_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m "fix(runtime): resume generation checkpoints from durable evidence"
~~~

提交前索引必须只含这九个路径的实际变更，不能使用 git add .。提交后索引为空，原用户状态保留；总体工作区并不干净，不作错误声明。

## 计划自查与交付

保存计划时自查三项：设计每一项是否有对应任务、所有方法签名与数据字段是否一致、是否存在未填写占位。实施代码块是尚未执行的工程指导，不是编译或测试通过证明；执行时必须取得所列真实行为证据。

Task 1 对应 validating 的候选事务与零候选完成；Task 2 对应最小化、独立预览、严格证据恢复、正负冷恢复、managed review、损坏/归属/取消/CAS、预算和原约束回归。任何必须改状态图、数据库或公共接口的发现停止实施并重新确认，不在此计划中临时扩大任务。

完成本地实施后交付实际提交号、变更范围与定点证据，说明真实 native 仍待单独授权；不将定点通过升级为 Phase 10 整体完成。下一步的外部推送、CI、PR 或有界 native 都需相应授权。

计划保存后的执行选择为：1 子代理逐任务实施并审查，2 当前会话顺序实施并在任务边界检查。选择前不派发子代理、不生成执行控制器、不运行任何测试。
