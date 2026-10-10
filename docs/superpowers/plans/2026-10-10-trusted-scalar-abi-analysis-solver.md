# Trusted Scalar ABI Analysis–Solver Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐首版真实分析器产生的基础整数 ABI 信息，取得真实 C/C++ 源码到求解器的行为 RED/GREEN，保留所有既有安全校验。

**Architecture:** 在分析包内增加私有、请求内有效的整数事实解析/应用组件；同一批准 Clang 通过 EOF 预处理探测提供事实。Analyze 以共同截止时间协调 ABI 与 AST 两次调用，在最终封装摘要前补齐类型；solver、oracle、协议和 bundle 信任根不变。

**Tech Stack:** Go 1.26.0+（本机工具路径 `D:/Program Files/Go/bin/go.exe`）、固定 Clang 22.1.8、C11/C++17、PowerShell 7、已有 probe.Runner/testgenbundle、Git。

## Global Constraints

- 本项属于第一版 Phase 10 / Windows CMake 兼容 W4 的分析器到求解器契约缺陷修复，不是第二版功能开发。
- 第二版十一项新增需求及其现有暂存代码继续暂停。
- 仅补齐 `char`、`signed char`、`unsigned char`、`short`、`unsigned short`、`int`、`unsigned int`、`long`、`unsigned long`、`long long`、`unsigned long long`。
- 不新支持 `size_t` 等别名、浮点类型、枚举、数组/记录布局、类、指针、模板或其他复杂类型。
- 探测 stdout/stderr 分别最多 64 KiB，AST 原有 16 MiB 输出上限保持不变。
- 沿用 IRVersion 1 和现有字段，不新增协议 ABI 字段。
- 不能延长既有 `0 < Timeout <= 30s` 条件。探测、AST 均只能使用共同截止时间的剩余预算。
- 不增加跨请求缓存、PATH 回退、工具下载、任意 target、宏表达式执行或自动重试。
- solver/oracle 安全检查、待测工程源码、头文件、CMake、协议、数据库 schema、bundle manifest 信任根不变。
- 测试隔离首版源码，不能把暂停迁移 022 和其他 V2 脏文件一起编译。
- 不自动运行完整 native、全量/race/UI/hosted、服务构建、推送、PR、合并、master 同步、Release、签名或 legal 审批。

---

## 批准、范围与文件结构

[书面设计](../specs/2026-10-10-trusted-scalar-abi-analysis-solver-design.md)已获用户“确认设计文档”。CODE_BASE 为 `c72693f4ba695479f1a23e9770bafce468cac254`，生产源码来自 `14f11e918dc57a438cc321a644c3cf4b27daad5c`。本轮只保存计划，不执行代码/测试、不初始化实施代理。

复用现有隔离目录 `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`，分支 `codex/phase10-production-generation`；默认 cwd `C:/codex_project/unitTest` 不是实施目录。开始执行时遵循 using-git-worktrees 的已存在 linked-worktree 路径，不另建 checkout，不 reset/stash 用户改动。原 W4 仍未完成，不重派已完成任务。

允许的源码/测试路径只有以下九处，按职责划分为两个可独立审查的任务：

| 路径 | 责任 | 任务 |
| --- | --- | --- |
| `apps/test-service/internal/testgenanalysis/scalar_abi.go`（新增） | 私有绑定、闭合宏解析、整数类型应用；无进程/solver 依赖 | 1 |
| `apps/test-service/internal/testgenanalysis/scalar_abi_test.go`（新增） | 合成事实的正反例、类型/绑定/摘要检查；共享同包测试宏 helper | 1 |
| `apps/test-service/internal/testgenanalysis/scalar_abi_probe.go`（新增） | 平台/语言绑定、固定探测 spec 与有界 runner 调用 | 2 |
| `apps/test-service/internal/testgenanalysis/analyzer.go` | 协调共同 deadline、ABI/AST、校验及最终封装 | 2 |
| `apps/test-service/internal/testgenanalysis/clang.go` | 只拒绝被消费的内建宏重定义 | 2 |
| `apps/test-service/internal/testgenanalysis/analyzer_test.go` | 使既有 fixtureRunner 按 ABI/AST 区分结果，保持安全断言 | 2 |
| `apps/test-service/internal/testgenanalysis/native_seed_contract_test.go` | observer 仍在首个进程边界停止，预算断言适应 ABI 探测 | 2 |
| `apps/test-service/internal/testgenanalysis/scalar_abi_analysis_test.go`（新增） | 协调、失败关闭、共同 deadline、并发、无缓存 | 2 |
| `apps/test-service/internal/runtime/production_analysis_solver_native_test.go`（新增） | 显式 opt-in 的真实 Clang → 正式绑定 → 真实 solver 回归 | 2 |

`ast.go`、`ir.go`、`safety.go`、solver、oracle、production_generation_pipeline.go 不改；复用现有 parseType/sealProgram/bindSourceExcerpts/bindProductionFunctions。未知整数别名仍无证明；只把受影响且原先 supported 的函数标记为 unsupported-type，不升级已有 unsupported 函数。

## 执行准备与定点命令约定（随 Task 1，不另拆任务）

执行选择后才创建 ignored `.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/`。用 apply_patch 保存控制器/进度/准备元数据；工具生成的 tar 导出、测试 stdout/receipt 属于派生产物。初始 index 必须为空。

准备控制器必须保留原 19 条 status、冻结清单 `.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256` 的 18 个字节绑定；逐次测试/提交前后复核。13 个既有独立源码绑定中只允许 analyzer.go/analyzer_test.go/native_seed_contract_test.go 三处因 Task 2 变化，其余十处保留。原始 DB/WAL/SHM、失败回执和旧 progress 不修改、不重新查询。

用以下已验证、不可修改的首版 service-only archive 创建一个新 scratch；不能复用旧诊断 scratch 中的两个私有失败测试。初始化只执行一次；实际生成的绝对 Scratch 路径、初始化 HEAD/status/保护文件哈希以 JSON 交接，通过 apply_patch 保存，后续读取同一准备记录。恢复上下文不能再次初始化或删旧目录。

```powershell
$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSVersion.Major -lt 7) { throw 'Controller requires PowerShell 7' }
$Workspace = [IO.Path]::GetFullPath('C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest')
if ((Get-Location).Path -ne $Workspace) { throw 'Use the existing isolated worktree' }
function Assert-ABIContained([string]$Path,[string]$Root) {
 $full = [IO.Path]::GetFullPath($Path)
 $base = [IO.Path]::GetFullPath($Root)
 if ($full -ne $base -and -not $full.StartsWith($base + [IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) { throw 'ABI path escaped root' }
 for ($current=$full; $current; $current=Split-Path -Parent $current) {
  if ((Test-Path -LiteralPath $current) -and ((Get-Item -LiteralPath $current).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'ABI path alias refused' }
  if ($current -eq $base) { return }
 }
 throw 'ABI root ancestry missing'
}
if (@(git diff --cached --name-only).Count) { throw 'Unexpected initial index' }
$InitialHead = (git rev-parse HEAD).Trim()
$OriginalStatus = @(git status --short)
if ($OriginalStatus.Count -ne 19) { throw 'Unexpected initial user status' }
$Frozen = foreach ($row in @(Get-Content -LiteralPath '.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256')) {
 if ($row -notmatch '^(.+)\s+([A-Fa-f0-9]{64})$') { throw 'Malformed frozen binding' }
 $path=$Matches[1]; $sha=$Matches[2]
 if ((Get-FileHash -LiteralPath $path).Hash -ne $sha) { throw 'Frozen file changed' }
 [pscustomobject]@{ Path=$path; SHA256=$sha }
}
if (@($Frozen).Count -ne 18) { throw 'Frozen count changed' }
$Archive = Join-Path $Workspace '.superpowers/runtime/w4-failure-service-20261010T0827065426983Z/committed-service.tar'
Assert-ABIContained $Archive $Workspace
if ((Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash -ne
    '77387A8EE77B79C109039729A08FF34AD0399DE13E88388B1641462E0B01778F') { throw 'Untrusted committed archive' }
git diff --quiet 14f11e918dc57a438cc321a644c3cf4b27daad5c c72693f4ba695479f1a23e9770bafce468cac254 -- apps/test-service
if ($LASTEXITCODE -ne 0) { throw 'Archive code differs from plan base' }
$RuntimeRoot = Join-Path $Workspace '.superpowers/runtime'
$Scratch = [IO.Path]::GetFullPath((Join-Path $RuntimeRoot ('scalar-abi-' + [guid]::NewGuid().ToString('N'))))
Assert-ABIContained $Scratch $Workspace
if (-not $Scratch.StartsWith($RuntimeRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
    (Test-Path -LiteralPath $Scratch)) { throw 'Unsafe or reused scratch' }
New-Item -ItemType Directory -Path $Scratch | Out-Null
tar -xf $Archive -C $Scratch
if ($LASTEXITCODE -ne 0) { throw 'Committed extraction failed' }
$ModuleRoot = Join-Path $Scratch 'apps/test-service'
if (Test-Path -LiteralPath (Join-Path $ModuleRoot 'internal/taskstore/migrations/022_case_definitions.sql')) { throw 'Paused migration leaked' }
$SeedFiles = @(
 'tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp',
 'tools/service-probe/fixtures/test-generation/cpputest/include/classifier.hpp',
 'tools/service-probe/fixtures/test-generation/cpputest/tests/classifier_test.cpp',
 'tools/service-probe/fixtures/test-generation/unity/src/classifier.c',
 'tools/service-probe/fixtures/test-generation/unity/include/classifier.h',
 'tools/service-probe/fixtures/test-generation/unity/tests/classifier_test.c'
)
foreach ($Relative in $SeedFiles) {
 $Destination = Join-Path $Scratch $Relative
 Assert-ABIContained (Join-Path $Workspace $Relative) $Workspace
 Assert-ABIContained $Destination $Scratch
 New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Destination) | Out-Null
 Copy-Item -LiteralPath (Join-Path $Workspace $Relative) -Destination $Destination
 if ((Get-FileHash -LiteralPath $Destination).Hash -ne (Get-FileHash -LiteralPath (Join-Path $Workspace $Relative)).Hash) { throw 'Seed copy changed' }
}
$ReadOnly = @($SeedFiles) + @(
 'tools/service-probe/fixtures/test-generation/cpputest/CMakeLists.txt',
 'tools/service-probe/fixtures/test-generation/unity/CMakeLists.txt',
 'apps/test-service/internal/runtime/production_generation_pipeline.go',
 'apps/test-service/internal/runtime/test_generation_failure.go'
)
$Bindings = foreach ($Path in $ReadOnly) { [pscustomobject]@{ Path=$Path; SHA256=(Get-FileHash -LiteralPath (Join-Path $Workspace $Path)).Hash } }
[pscustomobject]@{ Scratch=$Scratch; ModuleRoot=$ModuleRoot; SourceBase='14f11e918dc57a438cc321a644c3cf4b27daad5c'; InitialHead=$InitialHead; OriginalStatus=$OriginalStatus; Frozen=@($Frozen); Bindings=@($Bindings) } | ConvertTo-Json -Depth 5
```

控制器还须拒绝 RuntimeRoot/Scratch/源或目标祖先中的 ReparsePoint。元数据来自上一步实际输出，不能重新计算 UUID 当作同一次准备。每次测试只用 Copy-Item 机械同步九处白名单中实际存在的文件到 scratch 同路径，并比较原始 SHA-256；不复制整个脏模块，不覆盖/删除 V2 文件。`$ModuleRoot` 指准备记录的实际值；不手填猜测路径。

将总表九路径作为 `$Owned` 常量数组；其初始 SHA/新增状态按实际情况记录，不把 Task 2 合法编辑当成只读文件变化。每次测试前后使用以下保护函数（`$Prepared` 来自 apply_patch 保存的完整准备 JSON），同步同样使用它。函数不删除任何文件。

```powershell
function Assert-ABIProtected {
 if ((git branch --show-current).Trim() -ne 'codex/phase10-production-generation') { throw 'Branch changed' }
 git merge-base --is-ancestor $Prepared.InitialHead HEAD
 if ($LASTEXITCODE -ne 0) { throw 'Execution lineage changed' }
 if (@(git diff --cached --name-only).Count) { throw 'Unexpected index outside commit gate' }
 $status = @(git status --short | Where-Object { $_.Substring(3) -notin $Owned })
 if (@(Compare-Object @($Prepared.OriginalStatus) $status).Count) { throw 'Original user state changed' }
 foreach ($file in (@($Prepared.Frozen) + @($Prepared.Bindings))) {
  if ((Get-FileHash -LiteralPath (Join-Path $Workspace $file.Path)).Hash -ne $file.SHA256) { throw 'Protected bytes changed' }
 }
}
function Sync-ABIOwned {
 Assert-ABIProtected
 foreach ($relative in $Owned) {
  $source = Join-Path $Workspace $relative
  if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { continue }
  $destination = Join-Path $Scratch $relative
  Assert-ABIContained $source $Workspace
  Assert-ABIContained $destination $Scratch
  Copy-Item -LiteralPath $source -Destination $destination
  if ((Get-FileHash -LiteralPath $source).Hash -ne (Get-FileHash -LiteralPath $destination).Hash) { throw 'Owned overlay mismatch' }
 }
}
```

控制器定义以下 `Run-ABITest`，完整代码以 apply_patch 保存后使用。只允许文中列出的 Package/Regex/Native 组合；stdout/stderr 只保留 ignored 私有日志，对外仅投影 Action/Test 及真实退出码。单次普通测试 binary 上限 60 秒、整个 Go 进程上限 90 秒；真实双种子 binary 上限 90 秒、含编译的整个 Go 进程上限 150 秒。超时是失败，不是跳过；不自动重试。进程树 Kill 是收敛尝试，不把尚未证实的后代退出写成成功。

```powershell
function Run-ABITest([string]$Package, [string]$Regex, [switch]$Native) {
 $allowed = @(
  'IntegerABIFactsWidthsAndSignedness','IntegerABIFactsRejectMalformed','IntegerABIFactsRejectMissingOrDuplicate','IntegerABIFactsRejectIdentity',
  'IntegerABIFactsBindingIsolation','IntegerABIFactsApplyAndSeal','IntegerABIFactsUnprovenTypes','ReservedIntegerABIMacroSet',
  'AnalyzeIntegerABIProcessBoundary','AnalyzeIntegerABISpoofingBeforeLaunch','AnalyzeIntegerABIFailClosed','AnalyzeIntegerABIToolAndSourceChanges',
  'AnalyzeIntegerABISharedDeadline','AnalyzeIntegerABINoCrossRequestCache','AnalyzeIntegerABIConcurrentIsolation','AnalyzeIntegerABIDigestContainsTypes',
  'AnalyzeUsesOnlyVerifiedFixedCompilerWithCleanEnvironment','AnalyzeSuppressesImplicitHostHeaders','AnalyzeRejectsInjectedArgumentsBeforeLaunch',
  'AnalyzeRejectsStaleSourceAndCancellation','AnalyzeRedactsCompilerFailure','AnalyzeRejectsIncludedHeaderAndForeignASTExcerpt',
  'NativeGenerationSeedMatchesFixedAnalyzer','NativeGenerationSeedDirectiveReintroductionRejected'
 )
 if ($Native) {
  if ($Package -ne './internal/runtime' -or $Regex -ne '^TestProductionAnalysisToSolverIntegerABI$') { throw 'Unauthorized real-tool selector' }
  $roots = @('TestProductionAnalysisToSolverIntegerABI')
 } else {
  $match = [regex]::Match($Regex,'^\^Test(?:\(([A-Za-z0-9|]+)\)|([A-Za-z0-9]+))\$$')
  if ($Package -ne './internal/testgenanalysis' -or -not $match.Success) { throw 'Unauthorized unit selector' }
  $names = if ($match.Groups[1].Success) { @($match.Groups[1].Value -split '\|') } else { @($match.Groups[2].Value) }
  foreach ($name in $names) { if ($name -notin $allowed) { throw 'Unauthorized test name' } }
  $roots = @($names | ForEach-Object { 'Test' + $_ })
 }
 Sync-ABIOwned
 $psi = [Diagnostics.ProcessStartInfo]::new()
 $psi.FileName = 'D:/Program Files/Go/bin/go.exe'
 $psi.WorkingDirectory = $ModuleRoot
 $psi.UseShellExecute = $false
 $psi.CreateNoWindow = $true
 $psi.RedirectStandardOutput = $true
 $psi.RedirectStandardError = $true
 foreach ($pair in @{ GOWORK='off'; GOPROXY='off'; GOSUMDB='off'; GOFLAGS='-mod=readonly'; GOTOOLCHAIN='local' }.GetEnumerator()) {
  $psi.Environment[$pair.Key] = $pair.Value
 }
 $psi.Environment.Remove('UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT') | Out-Null
 $arguments = @('test','-json','-count=1','-run',$Regex)
 if ($Native) {
  $arguments += @('-tags=scalarabi_integration','-timeout=90s')
  $psi.Environment['UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT'] = Join-Path $Workspace '.superpowers/runtime/product-bundles/windows-x64/bundles/testgen'
 } else { $arguments += '-timeout=60s' }
 foreach ($arg in ($arguments + @($Package))) { $psi.ArgumentList.Add($arg) }
 $process = [Diagnostics.Process]::new()
 $process.StartInfo = $psi
 if (-not $process.Start()) { throw 'Go test did not start' }
 $out = $process.StandardOutput.ReadToEndAsync()
 $err = $process.StandardError.ReadToEndAsync()
 $limit = if ($Native) { 150000 } else { 90000 }
 if (-not $process.WaitForExit($limit)) {
  try { $process.Kill($true) } catch { }
  $confirmed = $process.WaitForExit(15000)
  $process.Dispose()
  throw "Go test timeout; held-process-exit-confirmed=$confirmed; no automatic retry"
 }
 $code = $process.ExitCode
 if (-not [Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($out,$err),5000)) {
  $process.StandardOutput.Dispose(); $process.StandardError.Dispose(); $process.Dispose()
  throw 'Test output did not converge; no qualification result'
 }
 $raw = $out.GetAwaiter().GetResult()
 $stderr = $err.GetAwaiter().GetResult()
 $process.Dispose()
 $token = [guid]::NewGuid().ToString('N')
 [IO.File]::WriteAllText((Join-Path $Scratch ($token + '.stdout.jsonl')), $raw)
 [IO.File]::WriteAllText((Join-Path $Scratch ($token + '.stderr.log')), $stderr)
 $events = foreach ($line in ($raw -split '\r?\n')) {
  if ($line) {
   try { $entry = $line | ConvertFrom-Json -ErrorAction Stop } catch { throw 'Malformed test event; inspect privately' }
   if ($entry.Action -in @('run','pass','fail','skip') -and $entry.Test) {
    [pscustomobject]@{ Action = $entry.Action; Test = $entry.Test }
   }
  }
 }
 foreach ($root in $roots) {
  if (@($events | Where-Object { $_.Test -eq $root -and $_.Action -eq 'run' }).Count -ne 1) { throw 'Missing selected test or compile failure; not a behavioral result' }
  if ($code -eq 0 -and @($events | Where-Object { $_.Test -eq $root -and $_.Action -eq 'pass' }).Count -ne 1) { throw 'No complete selected GREEN' }
 }
 if ($code -eq 0 -and @($events | Where-Object { $_.Action -in @('fail','skip') }).Count) { throw 'Failure or skip cannot qualify GREEN' }
 if ($Native -and $code -eq 0) {
  foreach ($child in @('cpputest','unity')) {
   if (@($events | Where-Object { $_.Test -eq ('TestProductionAnalysisToSolverIntegerABI/' + $child) -and $_.Action -eq 'pass' }).Count -ne 1) { throw 'Real dual-seed evidence incomplete' }
  }
 }
 Assert-ABIProtected
 [pscustomobject]@{ GoExitCode=$code; Package=$Package; Events=@($events); Native=[bool]$Native } | ConvertTo-Json -Depth 4
}
```

上面的函数给出真实 GoExitCode，不能把控制器自然 exit 0 当作测试 PASS。GREEN 必须检查指定顶级测试各出现 run/pass，失败/skip 为零；真实测试还必须出现 cpputest/unity 两个子测试 run/pass。RED 必须是文中指定行为断言失败，排除编译失败、缺工具、预算耗尽和零测试。stdout 的 Output 字段和 stderr 不回显。子进程环境不改变父会话，不需要向用户展示环境变量。发现预算/清理异常时停止本门槛，不扩大测试范围。调用 shell 工具最多等待 30 秒即返回运行 session，后续以不超过 30 秒的轮询保持用户更新，不把控制器的整体 WaitForExit 当成模型侧一次长阻塞。

### Task 1: Closed integer ABI facts and source-derived type application

**Files:** Create/Test `apps/test-service/internal/testgenanalysis/scalar_abi.go` and `scalar_abi_test.go`。只阅读现有 `ir.go`、`ast.go`、`safety.go`。

**Interfaces:**

- Consumes: `Type`、`Program`、`Diagnostic`、`parseType(string, map[string]TypeKind) Type`、`sealProgram(*Program) error`、`validSHA(string) bool`、`languageModes`。
- Produces: `integerABIBinding`、`integerABIFacts`、三个私有包函数及 binding.valid 方法；无 exported API。
- `decodeIntegerABIFacts(output []byte, binding integerABIBinding) (integerABIFacts, error)` 只在全部校验成功时设置 verified。
- `applyIntegerABIFacts(program *Program, facts integerABIFacts, binding integerABIBinding) error` 先核对事实/binding，再修改类型并清空旧 Digest，由调用者最后 seal。
- `reservedIntegerABIMacro(name string) bool` 与解析器消费集合完全相同。
- `integerABITestMacros(platform string, unsignedChar bool) []byte`、`integerABITestBinding(platform string) integerABIBinding` 是 Task 2 同包测试可用的合成事实 helper；不得给真实交接回归使用。

- [ ] **Step 1: 准备隔离源码和可编译的行为 RED**

按上节准备一次 scratch/保护记录。先写测试，再增加以下仅声明/失败关闭的生产骨架，避免用 undefined symbol 冒充行为 RED。所有新增/编辑用 apply_patch。

```go
type integerABIBinding struct { manifest, compile, standard, platform string }
func (b integerABIBinding) valid() bool {
    return validSHA(b.manifest) && validSHA(b.compile) && languageModes[b.standard] &&
        (b.platform == "windows-x64" || b.platform == "linux-x64")
}
type integerABIWidths struct { char, short, int_, long, longLong int }
type integerABIFacts struct {
    binding integerABIBinding
    widths integerABIWidths
    charSigned bool
    verified bool
}
const maxIntegerABIBytes = 64 << 10
var errIntegerABI = errors.New("unproven integer ABI")
func decodeIntegerABIFacts(output []byte, binding integerABIBinding) (integerABIFacts, error) {
    return integerABIFacts{}, errIntegerABI
}
func applyIntegerABIFacts(program *Program, facts integerABIFacts, binding integerABIBinding) error {
    return errIntegerABI
}
func reservedIntegerABIMacro(name string) bool { return false }
```

测试 helper 生成以下完整合成输出（使用 fmt.Sprintf 构造 long 的三项），不是复制真实工具日志。Windows long 使用 32/4/2147483647L，Linux long 使用 64/8/9223372036854775807L；两个平台共同字段如下，另分别加 `_WIN32=1`/`_WIN64=1` 或 `__linux__=1` 的 `#define` 行。unsignedChar 为真时加 `#define __CHAR_UNSIGNED__ 1`。每行以换行结束。Binding 的 manifest/compile 分别用 strings.Repeat("b",64)/strings.Repeat("c",64)，standard 为 `-std=c11`。

```text
#define __clang__ 1
#define __clang_major__ 22
#define __clang_minor__ 1
#define __clang_patchlevel__ 8
#define __x86_64__ 1
#define __CHAR_BIT__ 8
#define __SCHAR_MAX__ 127
#define __SHRT_WIDTH__ 16
#define __SIZEOF_SHORT__ 2
#define __SHRT_MAX__ 32767
#define __INT_WIDTH__ 32
#define __SIZEOF_INT__ 4
#define __INT_MAX__ 2147483647
#define __LONG_WIDTH__ 32
#define __SIZEOF_LONG__ 4
#define __LONG_MAX__ 2147483647L
#define __LLONG_WIDTH__ 64
#define __SIZEOF_LONG_LONG__ 8
#define __LONG_LONG_MAX__ 9223372036854775807LL
#define __SIZEOF_POINTER__ 8
#define __POINTER_WIDTH__ 64
```

核心行为断言放入 `TestIntegerABIFactsWidthsAndSignedness`，枚举两个平台 × 两种 plain char，使用实际 parseType 构造参数/返回/局部类型，再调用 apply，而不是只比较手写常量。

```go
func TestIntegerABIFactsWidthsAndSignedness(t *testing.T) {
    for _, platform := range []string{"windows-x64", "linux-x64"} {
        for _, unsignedChar := range []bool{false, true} {
            t.Run(fmt.Sprintf("%s/%t", platform, unsignedChar), func(t *testing.T) {
                binding := integerABITestBinding(platform)
                facts, err := decodeIntegerABIFacts(integerABITestMacros(platform, unsignedChar), binding)
                if err != nil { t.Fatal("valid compiler facts rejected") }
                p := Program{Version: IRVersion, Functions: []Function{{
                    SymbolID: strings.Repeat("a", 64), ReturnType: parseType("int", nil),
                    Parameters: []Parameter{{Name: "x", Type: parseType("char", nil)}},
                    LocalTypes: []Type{parseType("long", nil), parseType("unsigned int", nil)},
                    Decision: Decision{Kind: DecisionSupported, Reason: ReasonNone},
                }}}
                if applyIntegerABIFacts(&p, facts, binding) != nil { t.Fatal("proven facts not applied") }
                f := p.Functions[0]
                wantLong := 32
                if platform == "linux-x64" { wantLong = 64 }
                if f.ReturnType.BitWidth != 32 || !f.ReturnType.Signed || !f.ReturnType.Proven ||
                   f.Parameters[0].Type.BitWidth != 8 || f.Parameters[0].Type.Signed == unsignedChar ||
                   f.LocalTypes[0].BitWidth != wantLong || f.LocalTypes[1].Signed {
                    t.Fatal("source-derived integer ABI missing or inconsistent")
                }
            })
        }
    }
}
```

- [ ] **Step 2: 运行同一核心测试，确认正例行为失败**

同步两文件后运行 `Run-ABITest './internal/testgenanalysis' '^TestIntegerABIFactsWidthsAndSignedness$'`。要求真实 exit 1、四个子例在 valid compiler facts rejected 断言失败，代码可编译。此时不能改变 solver 或既有 Analyzer 以消除 RED。

- [ ] **Step 3: 实现闭合事实解析**

保留 Step 1 类型。宏白名单由上面共同字段加 `_WIN32`、`_WIN64`、`__linux__`、`__CHAR_UNSIGNED__` 构成，全部包括在 reservedIntegerABIMacro 中，不另接受版本字符串、别名或任意表达式。字段依据是[固定版本内建宏实现](https://github.com/llvm/llvm-project/blob/ca7933e47d3a3451d81e72ac174dcb5aa28b59d1/clang/lib/Frontend/InitPreprocessor.cpp)及其 [x86_64 身份宏](https://github.com/llvm/llvm-project/blob/ca7933e47d3a3451d81e72ac174dcb5aa28b59d1/clang/lib/Basic/Targets/X86.cpp)，不是本轮新工具运行结果。

```go
const integerABIMacroNames = " __clang__ __clang_major__ __clang_minor__ __clang_patchlevel__ __x86_64__ __CHAR_BIT__ __SCHAR_MAX__ __SHRT_WIDTH__ __SIZEOF_SHORT__ __SHRT_MAX__ __INT_WIDTH__ __SIZEOF_INT__ __INT_MAX__ __LONG_WIDTH__ __SIZEOF_LONG__ __LONG_MAX__ __LLONG_WIDTH__ __SIZEOF_LONG_LONG__ __LONG_LONG_MAX__ __SIZEOF_POINTER__ __POINTER_WIDTH__ _WIN32 _WIN64 __linux__ __CHAR_UNSIGNED__ "
func reservedIntegerABIMacro(name string) bool {
    return name != "" && !strings.ContainsAny(name, " \t\r\n") &&
        strings.Contains(integerABIMacroNames, " "+name+" ")
}
var integerABILiteral = regexp.MustCompile(`(?i)^(0|[1-9][0-9]*)(U|L|LL|UL|LU|ULL|LLU)?$`)
func integerABINumber(raw string) (*big.Int, error) {
    if len(raw) > 32 { return nil, errIntegerABI }
    match := integerABILiteral.FindStringSubmatch(raw)
    if match == nil { return nil, errIntegerABI }
    n, ok := new(big.Int).SetString(match[1], 10)
    if !ok { return nil, errIntegerABI }
    return n, nil
}
```

decode 的完整判定顺序：

1. Binding 的两个摘要必须 validSHA；standard 必须 languageModes 中合法；platform 仅 windows-x64/linux-x64。output 非空、长度不超过 maxIntegerABIBytes、末尾换行、无 NUL。
2. 使用 bufio.Scanner 和 64 KiB 上限逐行读取，按 `#define` 后的 identifier/value 分开；未知宏忽略，已消费宏只允许一条，消费行必须是无参数对象宏，value 经 integerABINumber，未知后缀/表达式/空值拒绝。Scanner.Err 不得忽略。
3. __clang__=1、版本 22/1/8、__x86_64__=1、字符 8 位、指针 8 字节/64 位。Windows 必须 _WIN32=_WIN64=1 且没有 __linux__；Linux 必须 __linux__=1 且没有 _WIN32/_WIN64。
4. char/short/int/long/longLong 五种基础宽度仅 8/16/32/64，按此顺序不递减；后四种各自 `__SIZEOF_*__ * __CHAR_BIT__ == *_WIDTH__`。五个 signed maximum 均等于 `(1 << (width-1)) - 1`，用 math/big 比较，先限定 width 再位移。必需宏缺一即拒绝，不能把缺值当零。
5. plain char 的 optional __CHAR_UNSIGNED__ 必须为 1；缺省只在其余事实完全有效时解释为 signed。基础 signed/unsigned 对使用同一已证明宽度和语言规定的符号性。不按 platform 设置 long 宽度。
6. 返回带原 Binding、闭合 widths、charSigned 和 verified=true 的值。errors 全部使用 errIntegerABI，不携带 raw。

数值/最大值检查采用下面实际数学关系，不用字符串后缀判断位宽：

```go
maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(width-1)), big.NewInt(1))
if actualMaximum.Cmp(maximum) != 0 { return integerABIFacts{}, errIntegerABI }
if byteSize*charWidth != width { return integerABIFacts{}, errIntegerABI }
```

width/byteSize 是经范围检查后转成 int 的局部数值；输入 big.Int 非 IsInt64、非正或超 64 直接拒绝后才转换，防止整数转换溢出。required 和 optional 键集合在本步骤确定，不动态从输出扩展。

- [ ] **Step 4: 应用事实，不扩大原类型支持范围**

apply 先拒绝 nil program、未 verified facts、binding 不相等/不合法；在此之前不修改 Program。复用 parseType 的 exact Spelling，不去除 const、重写别名或递归证明数组/记录。以下是整数分派，最后总是用本次事实覆盖值，不信任此前填入的 width：

```go
func applyIntegerABIFacts(program *Program,facts integerABIFacts,binding integerABIBinding) error {
if program == nil || !facts.verified || !binding.valid() || facts.binding != binding { return errIntegerABI }
enrich := func(typ *Type) bool {
    if typ.Kind != TypeInteger { return false }
    width, signed, unsupported := 0, false, false
    switch typ.Spelling {
    case "char": width, signed = facts.widths.char, facts.charSigned
    case "signed char": width, signed = facts.widths.char, true
    case "unsigned char": width, signed = facts.widths.char, false
    case "short": width, signed = facts.widths.short, true
    case "unsigned short": width, signed = facts.widths.short, false
    case "int": width, signed = facts.widths.int_, true
    case "unsigned int": width, signed = facts.widths.int_, false
    case "long": width, signed = facts.widths.long, true
    case "unsigned long": width, signed = facts.widths.long, false
    case "long long": width, signed = facts.widths.longLong, true
    case "unsigned long long": width, signed = facts.widths.longLong, false
    default: typ.BitWidth, typ.Signed, typ.Proven = 0, false, false; unsupported = true
    }
    if !unsupported { typ.BitWidth, typ.Signed, typ.Proven = width, signed, true }
    return unsupported
}
for i := range program.Functions {
    f := &program.Functions[i]
    unsupported := enrich(&f.ReturnType)
    for j := range f.Parameters { if enrich(&f.Parameters[j].Type) { unsupported = true } }
    for j := range f.LocalTypes { if enrich(&f.LocalTypes[j]) { unsupported = true } }
    if unsupported && f.Decision.Kind == DecisionSupported {
        f.Decision = Decision{DecisionUnsupported, ReasonUnsupportedType}
        program.Diagnostics = append(program.Diagnostics, Diagnostic{f.SymbolID, ReasonUnsupportedType})
    }
}
program.Digest = ""
return nil
}
```

typ 为 *Type，width/signed/unsupported 是每个类型独立变量，不能把上一类型的 unsupported 状态带入下一类型。原 unsupported reason 和非整数 Type 全部保留。

- [ ] **Step 5: 写出负例、绑定和摘要回归，再运行定点 GREEN**

新增以下测试，每一项必须是实际断言：

```go
// TestIntegerABIFactsRejectMalformed
bad := bytes.Replace(integerABITestMacros("windows-x64", false), []byte("__INT_WIDTH__ 32"), []byte("__INT_WIDTH__ (16+16)"), 1)
if _, err := decodeIntegerABIFacts(bad, integerABITestBinding("windows-x64")); err == nil { t.Fatal("macro expression accepted") }
// TestIntegerABIFactsRejectMissingOrDuplicate
duplicate := append(integerABITestMacros("windows-x64", false), []byte("#define __INT_WIDTH__ 32\n")...)
if _, err := decodeIntegerABIFacts(duplicate, integerABITestBinding("windows-x64")); err == nil { t.Fatal("duplicate fact accepted") }
// TestIntegerABIFactsBindingIsolation
binding := integerABITestBinding("windows-x64")
facts, err := decodeIntegerABIFacts(integerABITestMacros("windows-x64", false), binding)
if err != nil { t.Fatal("test facts invalid") }
other := binding; other.compile = strings.Repeat("d", 64)
p := Program{Version: IRVersion, Digest: strings.Repeat("e", 64)}
if applyIntegerABIFacts(&p, facts, other) == nil || p.Digest != strings.Repeat("e",64) { t.Fatal("cross-request facts applied") }
```

完整负例表另包括：逐个移除 required 宏、不同数字的重复宏、空/超 64 KiB/NUL/缺末尾换行、非整数字面量/超过 32 字符、宽度 0/128、范围不匹配、字节/宽度不匹配、非 x64、版本不是 22.1.8、混合平台及无平台、__CHAR_UNSIGNED__ 为 0。用 bytes.Replace 生成变体，不修改真实 bundle/源码。

`TestIntegerABIFactsApplyAndSeal` 构造 parseType 的十一种整数并核对 width/signed/Proven；apply 前后分别 sealProgram，摘要必须改变，再 seal 一次必须稳定。`TestIntegerABIFactsUnprovenTypes` 要求 size_t 不获证明且原 supported 函数变 unsupported-type；bool/void/floating/record/pointer 值保持 DeepEqual，已有 unsupported reason 不变。`TestReservedIntegerABIMacroSet` 枚举全部白名单及 PROJECT_FEATURE/_DEBUG/空/带空白的反例。

同步 Task 1 文件，运行：

```powershell
Run-ABITest './internal/testgenanalysis' '^Test(IntegerABIFactsWidthsAndSignedness|IntegerABIFactsRejectMalformed|IntegerABIFactsRejectMissingOrDuplicate|IntegerABIFactsRejectIdentity|IntegerABIFactsBindingIsolation|IntegerABIFactsApplyAndSeal|IntegerABIFactsUnprovenTypes|ReservedIntegerABIMacroSet)$'
```

将版本/平台/x64 负例放入明确的 `TestIntegerABIFactsRejectIdentity`。要求全部指定 root run/pass，真实 exit 0，失败/skip 为零；不要添加重复第二次同命令“保险”运行。

- [ ] **Step 6: 审查并仅提交 Task 1 两文件**

按执行方式进行规格/代码审查；只有实际执行方式允许时才派审查代理。保留准备信息、RED/GREEN 和保护核对的闭合证据。gofmt 仅本项文件；若格式化发生在上一步验证之后，重新验证只针对改变的测试包，不假借全量验证。

```powershell
git add -- apps/test-service/internal/testgenanalysis/scalar_abi.go apps/test-service/internal/testgenanalysis/scalar_abi_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m 'fix: prove source-derived integer ABI facts'
```

提交前确认只有这两条路径在 index，冻结 18、其余十个独立绑定及原 status 完整；本任务未接入 Analyze，不能宣称生产生成已修复。

### Task 2: Trusted probe, shared deadline and real analyzer–solver regression

**Files:** Create scalar_abi_probe.go/scalar_abi_analysis_test.go/production_analysis_solver_native_test.go；Modify analyzer.go/clang.go/analyzer_test.go/native_seed_contract_test.go，路径见总表。Read-only bundle/probe/solver/runtime pipeline。

**Interfaces:**

- Consumes Task 1 的 decodeIntegerABIFacts/applyIntegerABIFacts/reservedIntegerABIMacro、binding.valid、绑定/事实类型和测试 helper。
- Consumes `VerifiedBundle`、`probe.Spec/Runner/Result`、`AnalysisRequest`、`Analyzer.Analyze`、`verifySource`、`decodeASTWithSourcePath`、`bindSourceExcerpts`。
- Produces `integerABIBindingFor(request AnalysisRequest, manifest string, args []string) (integerABIBinding,error)`；仅 amd64 + windows/linux，standard 来自已归一化 args 的唯一 -std。
- Produces `(a Analyzer) probeIntegerABI(ctx context.Context, request AnalysisRequest, binding integerABIBinding) (integerABIFacts,error)`，ctx 必须有本次共同 deadline。
- Uses unchanged `bindProductionFunctions(analysis.Program, []generationFunctionTarget) ([]generationFunctionTarget,error)` and `solver.Solver.Solve(context.Context, analysis.Program, solver.CoverageGap, solver.Budget) ([]solver.InputVector,[]solver.Diagnostic,error)`。
- Produces opt-in root `TestProductionAnalysisToSolverIntegerABI`（cpputest/unity 两子测试）；无新的生产 RPC/版本/工具选择入口。

- [ ] **Step 1: 先写真实交接回归，用真实当前生产者取得行为 RED**

新增 runtime 测试文件使用 `//go:build scalarabi_integration` 加空行和 `package runtime`。import context/crypto/sha256/encoding/hex/encoding/json/math/big/os/path/filepath/标准 runtime 别名 goruntime/testing/time，以及 analysis/testgenbundle/solver；使用现有 runtime 私有绑定函数。编译 tag 使普通测试不启动工具；带 tag 时缺环境变量、路径不合法或 bundle.Open 失败一律 Fatal，不 Skip/不回退。

真实根测试的核心完整流程如下；compile digest 是测试拥有的实际输入/参数描述的新摘要，不复用历史 native compile/report ID，也不宣称经过 CMake catalog 验证。

```go
func TestProductionAnalysisToSolverIntegerABI(t *testing.T) {
    root := os.Getenv("UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT")
    if !filepath.IsAbs(root) || filepath.Clean(root) != root { t.Fatal("approved bundle required") }
    bundle, err := testgenbundle.Open(root)
    if err != nil { t.Fatal("approved bundle verification failed") }
    analyzer, err := analysis.NewAnalyzer(bundle)
    if err != nil { t.Fatal("trusted analyzer unavailable") }
    _, ownFile, _, ok := goruntime.Caller(0)
    if !ok { t.Fatal("test repository identity unavailable") }
    repository := filepath.Clean(filepath.Join(filepath.Dir(ownFile), "../../../.."))
    specs := []struct{ framework, file, header, standard string }{
        {"cpputest", "classifier.cpp", "classifier.hpp", "-std=c++17"},
        {"unity", "classifier.c", "classifier.h", "-std=c11"},
    }
    for _, spec := range specs {
        t.Run(spec.framework, func(t *testing.T) {
            fixture := filepath.Join(repository, "tools/service-probe/fixtures/test-generation", spec.framework)
            source, err := os.ReadFile(filepath.Join(fixture, "src", spec.file))
            if err != nil { t.Fatal("real seed missing") }
            header, err := os.ReadFile(filepath.Join(fixture, "include", spec.header))
            if err != nil { t.Fatal("real seed header missing") }
            work := t.TempDir()
            if os.MkdirAll(filepath.Join(work,"src"),0700) != nil || os.MkdirAll(filepath.Join(work,"include"),0700) != nil { t.Fatal("isolated seed root unavailable") }
            if os.WriteFile(filepath.Join(work,"src",spec.file),source,0600) != nil ||
               os.WriteFile(filepath.Join(work,"include",spec.header),header,0600) != nil { t.Fatal("isolated seed copy unavailable") }
            sourceSHA := sha256.Sum256(source)
            args := []string{spec.standard, "-Iinclude"}
            descriptor, err := json.Marshal(struct{ Source string; Args []string }{hex.EncodeToString(sourceSHA[:]),args})
            if err != nil { t.Fatal("compile input descriptor unavailable") }
            compileSHA := sha256.Sum256(descriptor)
            request := analysis.AnalysisRequest{WorkspaceRoot:work, SourceRelative:"src/"+spec.file,
                SourceDigest:hex.EncodeToString(sourceSHA[:]), CompileSnapshotDigest:hex.EncodeToString(compileSHA[:]),
                Arguments:args, Timeout:30*time.Second}
            program, err := analyzer.Analyze(context.Background(),request)
            if err != nil || len(program.Functions)!=1 || len(program.Diagnostics)!=0 { t.Fatal("real analysis rejected seed") }
            f := program.Functions[0]
            if f.Name!="classify" || len(f.Parameters)!=1 || f.LinkageName=="" { t.Fatal("real function identity incomplete") }
            gap := solver.CoverageGap{Kind:solver.GapFunction, CompileSnapshot:request.CompileSnapshotDigest, AnalyzerVersion:"clang-ast-ir-v1"}
            selected, err := bindProductionFunctions(program,[]generationFunctionTarget{{linkageName:f.LinkageName,gap:gap}})
            if err!=nil || len(selected)!=1 || selected[0].gap.SymbolID!=f.SymbolID { t.Fatal("real function binding rejected") }
            vectors, diagnostics, err := (solver.Solver{}).Solve(context.Background(),program,selected[0].gap,
                solver.Budget{WallTime:5*time.Second,CandidateLimit:32,MemoryBytes:64<<20,Concurrency:1})
            if err!=nil || len(vectors)==0 || len(diagnostics)!=0 { t.Fatal("real analyzed integers cannot be solved") }
            if f.ReturnType.Kind!=analysis.TypeInteger || f.ReturnType.BitWidth!=32 || !f.ReturnType.Signed || !f.ReturnType.Proven ||
               f.Parameters[0].Type.Kind!=analysis.TypeInteger || f.Parameters[0].Type.BitWidth!=32 || !f.Parameters[0].Type.Signed || !f.Parameters[0].Type.Proven ||
               len(program.Digest)!=64 { t.Fatal("real integer proof incomplete") }
            minimum := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1),uint(f.Parameters[0].Type.BitWidth-1)))
            maximum := new(big.Int).Sub(new(big.Int).Neg(new(big.Int).Set(minimum)),big.NewInt(1))
            for _, vector := range vectors {
                if len(vector.Inputs)!=1 || vector.Inputs[0].Name!=f.Parameters[0].Name || vector.Inputs[0].Value.Kind!=analysis.TypeInteger { t.Fatal("invalid real input shape") }
                value, ok := new(big.Int).SetString(vector.Inputs[0].Value.Integer,10)
                if !ok || value.Cmp(minimum)<0 || value.Cmp(maximum)>0 { t.Fatal("real input exceeds proven range") }
            }
        })
    }
}
```

32 位 signed int 是当前批准两个 x64 bundle 的真实回归预期，不是生产代码宽度表。保持 source/header 的前后保护哈希一致。测试文件中的 os.WriteFile 仅产生测试自有临时输入，不修改仓库种子或正常应用文件。

- [ ] **Step 2: 执行一次真实 RED 并记录确切断言**

同步新 runtime 测试，运行 `Run-ABITest './internal/runtime' '^TestProductionAnalysisToSolverIntegerABI$' -Native`。要求两个子例均真正 Analyze/bind 后在 real analyzed integers cannot be solved 失败；Go exit 1。若任一子例在工具/分析/绑定/预算边界失败，停止当前生产改动，记录新边界并定点诊断；不能拿缺工具或历史诊断当新的 RED。不要先运行额外 clang 命令。

- [ ] **Step 3: 编写协调与安全测试，先观察行为 RED**

scalar_abi_analysis_test.go 明确八个 root：`TestAnalyzeIntegerABIProcessBoundary`、`TestAnalyzeIntegerABISpoofingBeforeLaunch`、`TestAnalyzeIntegerABIFailClosed`、`TestAnalyzeIntegerABIToolAndSourceChanges`、`TestAnalyzeIntegerABISharedDeadline`、`TestAnalyzeIntegerABINoCrossRequestCache`、`TestAnalyzeIntegerABIConcurrentIsolation`、`TestAnalyzeIntegerABIDigestContainsTypes`。

先增加以下断言，使用当前 fixtureAnalysis 和一个记录全部 spec 的本地 runner（实现 `Run(context.Context,probe.Spec)(probe.Result,error)`，ABI 返回 Task 1 宏、AST 返回 scalarAST），不改变生产协调逻辑。

```go
type integerABIRecordingRunner struct {
    mu sync.Mutex
    specs []probe.Spec
    deadlines []time.Time
    hook func(context.Context,probe.Spec,int) (probe.Result,error)
}
func (r *integerABIRecordingRunner) Run(ctx context.Context,spec probe.Spec) (probe.Result,error) {
    deadline,_ := ctx.Deadline()
    r.mu.Lock()
    index := len(r.specs)
    r.specs=append(r.specs,spec); r.deadlines=append(r.deadlines,deadline)
    r.mu.Unlock()
    if r.hook!=nil { return r.hook(ctx,spec,index) }
    for _, arg := range spec.Args {
        if arg=="-dM" { return probe.Result{ExitCode:0,Stdout:integerABITestMacros(runtime.GOOS+"-x64",false)},nil }
    }
    return probe.Result{ExitCode:0,Stdout:[]byte(scalarAST)},nil
}
type integerABIControlledBundle struct {
    fixtureBundle
    check func() error
}
func (b *integerABIControlledBundle) Verify() error { return b.check() }
```

测试 package 是 testgenanalysis，标准库 runtime 名不冲突；另 import sync/sync/atomic。runner.hook 在测试启动前设置一次，记录读取发生在 Analyze 返回或所有 goroutine 完成后。ProcessBoundary 的核心断言为：

```go
analyzer,request,_,_ := fixtureAnalysis(t)
runner := &integerABIRecordingRunner{}
analyzer.runner=runner
program, err := analyzer.Analyze(context.Background(),request)
specs := runner.specs
if err != nil || len(specs)!=2 { t.Fatal("ABI and AST must share one analysis") }
if specs[0].MaxOutput!=maxIntegerABIBytes || specs[1].MaxOutput!=maxASTBytes { t.Fatal("incorrect closed process bounds") }
if program.Functions[0].ReturnType.BitWidth==0 { t.Fatal("source-derived integer proof absent") }
```

recording runner 的 specs 是本次测试局部变量，不存到生产对象。宏伪造测试枚举 Task 1 白名单，用 `request.Arguments=[]string{"-std=c11","-D"+name+"=1"}`，要求调用数 0；正常 PROJECT_FEATURE=1 必须合法。RED 命令如下，只运行三个明确 root，在原单进程/接受内建宏/独立 timeout 行为上失败；其他负例在完成步骤 4 后验证。

```powershell
Run-ABITest './internal/testgenanalysis' '^Test(AnalyzeIntegerABIProcessBoundary|AnalyzeIntegerABISpoofingBeforeLaunch|AnalyzeIntegerABISharedDeadline)$'
```

- [ ] **Step 4: 实现探测、参数拒绝和 Analyze 协调**

scalar_abi_probe.go 的 binding constructor 根据 runtime.GOARCH==amd64、runtime.GOOS windows/linux 产生平台值；归一化 args 中唯一 -std 必须 languageModes 中合法。填入 request.CompileSnapshotDigest 和 bundle.ManifestSHA256，不从 stdout/工作区选择平台或工具。

```go
func integerABIBindingFor(request AnalysisRequest,manifest string,args []string) (integerABIBinding,error) {
    if runtime.GOARCH!="amd64" { return integerABIBinding{},errIntegerABI }
    platform := ""
    switch runtime.GOOS {
    case "windows": platform="windows-x64"
    case "linux": platform="linux-x64"
    default: return integerABIBinding{},errIntegerABI
    }
    standard := ""
    for _, arg := range args {
        if languageModes[arg] {
            if standard!="" { return integerABIBinding{},errIntegerABI }
            standard=arg
        }
    }
    binding := integerABIBinding{manifest:manifest,compile:request.CompileSnapshotDigest,standard:standard,platform:platform}
    if !binding.valid() { return integerABIBinding{},errIntegerABI }
    return binding,nil
}
func (a Analyzer) probeIntegerABI(ctx context.Context, request AnalysisRequest, binding integerABIBinding) (integerABIFacts,error) {
    if err:=ctx.Err(); err!=nil { return integerABIFacts{},err }
    deadline, ok := ctx.Deadline()
    if !ok { return integerABIFacts{},errIntegerABI }
    language := "c"
    if binding.standard=="-std=c++17" || binding.standard=="-std=c++20" { language="c++" }
    remaining := time.Until(deadline)
    if remaining<=0 { return integerABIFacts{},context.DeadlineExceeded }
    result, err := a.runner.Run(ctx,probe.Spec{
        Executable:a.bundle.ClangPath(), Dir:request.WorkspaceRoot, Env:[]string{},
        Args:[]string{"--no-default-config","-dM","-E","-x",language,binding.standard,
            "-nostdinc","-nostdinc++","-resource-dir",a.bundle.ResourceDir(),"-"},
        Timeout:remaining,MaxOutput:maxIntegerABIBytes,
    })
    if ctx.Err()!=nil { return integerABIFacts{},ctx.Err() }
    if err!=nil || result.ExitCode!=0 { return integerABIFacts{},errIntegerABI }
    return decodeIntegerABIFacts(result.Stdout,binding)
}
```

clang.go 的 define 分支在追加参数前提取名字，拒绝消费宏；保持普通 define、include、标准及其它拒绝语法不变：

```go
name, _, _ := strings.Cut(strings.TrimPrefix(arg,"-D"),"=")
if reservedIntegerABIMacro(name) { return nil,errors.New("compiler ABI macro redefinition is unsupported") }
result = append(result,arg)
```

Analyzer.Analyze 替换方法如下，原有辅助函数/常量不变。此段与 scalar_abi_probe.go 使用的 helper 在前述 Task 中已定义，原来的错误、路径、来源和摘要条件保留：

```go
func (a Analyzer) Analyze(ctx context.Context,request AnalysisRequest) (Program,error) {
    if err:=ctx.Err(); err!=nil { return Program{},err }
    if a.bundle==nil || a.runner==nil || !validSHA(a.bundle.ManifestSHA256()) ||
       !validSHA(request.SourceDigest) || !validSHA(request.CompileSnapshotDigest) ||
       request.Timeout<=0 || request.Timeout>30*time.Second { return Program{},errors.New("invalid analysis identity or budget") }
    analysisContext,cancel := context.WithTimeout(ctx,request.Timeout)
    defer cancel()
    if !filepath.IsAbs(request.WorkspaceRoot) || filepath.Clean(request.WorkspaceRoot)!=request.WorkspaceRoot || !safeRelativeSource(request.SourceRelative) {
        return Program{},errors.New("invalid trusted source root or URI")
    }
    if a.bundle.Verify()!=nil { return Program{},errors.New("fixed Clang bundle verification failed") }
    args,err := normalizedArguments(request.Arguments,request.WorkspaceRoot)
    if err!=nil { return Program{},err }
    sourcePath := filepath.Join(request.WorkspaceRoot,filepath.FromSlash(request.SourceRelative))
    sourceBytes,err := verifySource(sourcePath,request.WorkspaceRoot,request.SourceDigest)
    if err!=nil { return Program{},err }
    if hasSourceDirective(sourceBytes) { return Program{},errors.New("source-directed includes or macros are not supported by fixed analysis") }
    binding,err := integerABIBindingFor(request,a.bundle.ManifestSHA256(),args)
    if err!=nil { return Program{},errIntegerABI }
    facts,err := a.probeIntegerABI(analysisContext,request,binding)
    if err!=nil { return Program{},err }
    if err:=analysisContext.Err(); err!=nil { return Program{},err }
    if a.bundle.Verify()!=nil { return Program{},errors.New("fixed Clang bundle changed during analysis") }
    if err:=analysisContext.Err(); err!=nil { return Program{},err }
    deadline,_ := analysisContext.Deadline()
    remaining := time.Until(deadline)
    if remaining<=0 { return Program{},context.DeadlineExceeded }
    astArgs := append([]string{"--no-default-config","-Xclang","-ast-dump=json","-fsyntax-only","-nostdinc","-nostdinc++","-isystem",filepath.Join(a.bundle.ResourceDir(),"include")},args...)
    astArgs = append(astArgs,"-resource-dir",a.bundle.ResourceDir(),sourcePath)
    result,err := a.runner.Run(analysisContext,probe.Spec{Executable:a.bundle.ClangPath(),Args:astArgs,
        Dir:request.WorkspaceRoot,Env:[]string{},Timeout:remaining,MaxOutput:maxASTBytes})
    if err:=analysisContext.Err(); err!=nil { return Program{},err }
    if err!=nil { return Program{},errors.New("fixed Clang analysis process failed") }
    if result.ExitCode!=0 { return Program{},errors.New("fixed Clang rejected source") }
    if a.bundle.Verify()!=nil { return Program{},errors.New("fixed Clang bundle changed during analysis") }
    afterBytes,err := verifySource(sourcePath,request.WorkspaceRoot,request.SourceDigest)
    if err!=nil { return Program{},err }
    if !bytes.Equal(sourceBytes,afterBytes) { return Program{},errors.New("source snapshot changed during analysis") }
    program,err := decodeASTWithSourcePath(strings.NewReader(string(result.Stdout)),maxASTBytes,request.SourceDigest,sourcePath,request.WorkspaceRoot)
    if err!=nil { return Program{},errors.New("fixed Clang emitted unsupported AST") }
    if err:=applyIntegerABIFacts(&program,facts,binding); err!=nil { return Program{},errIntegerABI }
    if err:=bindSourceExcerpts(&program,sourceBytes); err!=nil { return Program{},errors.New("fixed Clang source range is incomplete") }
    if err:=analysisContext.Err(); err!=nil { return Program{},err }
    return program,nil
}
```

共同 analysisContext 在合法请求 Timeout 确认后、前置 bundle/source 等操作开始前建立，避免在探测之后才开始计时。每条调用前检查取消和 remaining>0；若前置哈希耗尽预算，禁止启动进程。第二次 bundle.Verify 是新 AST 调用前必要保护，第三次为原有结束验证；fixture 的正常 Analyze 验证计数从 2 改为 3，不删除任何验证来优化时间。original ctx 的更早 deadline 由 WithTimeout 自然继承。固定 `--no-default-config` 的用途见 [Clang 参数文档](https://clang.llvm.org/docs/ClangCommandLineReference.html)，禁止把该服务端选项反向开放给编译快照。

现有 AST decode 会产生暂时摘要；apply 将它清空，最终 bindSourceExcerpts 重算类型/摘录摘要。不可将 AST 暂时摘要交给 solver。仅从最终 program 成功返回，不在失败时返回部分 Program。

- [ ] **Step 5: 修正同包夹具和旧 observer，保留其原证明边界**

fixtureRunner 增加 `abiResult probe.Result`、`specs []probe.Spec`；fixtureAnalysis 用 host platform 的 integerABITestMacros 初始化 abiResult，result 仍为 scalarAST。Run 同步记录 calls/spec/specs，共同 err 优先，然后按 `-dM` 与 AST 选择输出：

```go
func (r *fixtureRunner) Run(_ context.Context,s probe.Spec) (probe.Result,error) {
    r.calls++; r.spec=s; r.specs=append(r.specs,s)
    if r.err!=nil { return probe.Result{},r.err }
    for _, arg := range s.Args { if arg=="-dM" { return r.abiResult,nil } }
    return r.result,nil
}
```

TestAnalyzeUsesOnlyVerifiedFixedCompilerWithCleanEnvironment 改为 launches==2、验证计数 3、两条精确 args/同 bundle/空 Env/正确资源/remaining Timeout>0 且 <=request.Timeout；AST 保持原摘录/Decision 断言并增加真实整数字段断言。旧 ForeignAST 测试设置 result.Stdout 仍只影响 AST，ABI fixture 不能遮掩外来摘录失败。旧 RedactsCompilerFailure 的 AST stderr 不得泄漏；另测试 ABI stderr。

nativeSeed observer 的 runner.err 仍在首条调用立即停止，所以 calls==1 不变；spec 现在为固定 EOF ABI spec、MaxOutput==maxIntegerABIBytes，Timeout 为剩余量而非精确 request.Timeout。保持 err 必须非 nil、重新加入三种源码指令必须 calls==0、真实 seed/header/driver 合约不变。此 GREEN 仍只证明 preflight，不把它叫真实 Clang 成功。

- [ ] **Step 6: 完成八项安全回归并做一次定点 GREEN**

为步骤 3 的八个 root 实现以下明确行为，不增加生产测试 hook：

- ProcessBoundary：ABI 无任何 -D/-I/sourcePath，stdin 为最后的 `-`，两调用均 --no-default-config；标准分别验证 c11/c17/c++17/c++20，fixed compiler/resource/empty Env。
- SpoofingBeforeLaunch：所有消费宏重定义、@rsp/-target/-m32/-funsigned-char/-include/-resource-dir 均零调用拒绝；PROJECT_FEATURE/_DEBUG 的普通合法 define 保留。
- FailClosed：ABI runner error/nonzero/ErrOutputLimit/错误宏/重复宏/缺宏分别不启动 AST、不返回 Program；AST nonzero/来源错误仍拒绝；两阶段含 secret marker 的 stderr 不出现在 err.Error。
- ToolAndSourceChanges：测试自有 bundle Verify 回调在第二次验证失败必须无 AST；runner ABI 或 AST 后改测试源字节必须最终拒绝；不修改真实 bundle。Verify 计数失败时允许少于成功路径，不能跳过核验。
- SharedDeadline：记录两次 ctx.Deadline 完全相等；ABI runner 内等待 25ms，request.Timeout=250ms，AST spec.Timeout 必须更小且不大于 deadline 剩余；取消在 ABI 返回时触发，AST 调用数为零。另使用较早 parent deadline，不能被请求 30s 延长。定时只用于这几个有界测试，不延长真实预算。
- NoCrossRequestCache：同一 Analyzer 连续两个请求实际 runner calls==4；第二请求使用不同 CompileSnapshotDigest，事实不得越过 binding。
- ConcurrentIsolation：两个不同 compile digest 请求并发；测试自有 counting bundle 用 atomic 计数、recording runner 用 mutex 保存 specs，禁止共享原 fixture 的非线程安全 `*int`。四次调用、两完整结果、事实绑定互不混用，测试无跨请求的生产字段。只用 Go 普通定点测试，不默认 -race。
- DigestContainsTypes：最终返回的 Program 经 sealProgram 再封装摘要稳定；deep copy 后把参数 width 清零再 seal，摘要必须不同。Deep copy 用 json.Marshal/Unmarshal 的测试内闭合 IR，不打印其内容；也验证无取消后成功结果。

除 ProcessBoundary 的步骤 3 断言外，使用以下实际断言/调用模式完成安全测试，变体按上述八项的明确输入表构造，不能只写注释或无操作成功用例：

```go
// FailClosed / ToolAndSourceChanges: after a single-case fixture has returned.
if err==nil || len(program.Functions)!=0 || program.Digest!="" { t.Fatal("unproven partial program escaped") }
if strings.Contains(err.Error(),"secret-marker") { t.Fatal("tool output leaked") }
// FailClosed ABI variants must not launch an AST.
for _, spec := range runner.specs {
    if slices.Contains(spec.Args,"-ast-dump=json") { t.Fatal("AST launched after rejected ABI") }
}
// SharedDeadline: request.Timeout=250ms and ABI hook waits 25ms before returning valid macros.
if len(runner.specs)!=2 || runner.deadlines[0].IsZero() || runner.deadlines[0]!=runner.deadlines[1] ||
   runner.specs[1].Timeout>=runner.specs[0].Timeout || runner.specs[1].Timeout>225*time.Millisecond {
    t.Fatal("analysis budget renewed")
}
// NoCrossRequestCache uses the same analyzer for two complete requests.
if _,err:=analyzer.Analyze(context.Background(),request); err!=nil { t.Fatal("first request failed") }
request.CompileSnapshotDigest=strings.Repeat("d",64)
if _,err:=analyzer.Analyze(context.Background(),request); err!=nil || len(runner.specs)!=4 { t.Fatal("cross-request cache or binding mix") }
// ConcurrentIsolation uses a synchronized verifier, not fixtureBundle's non-atomic pointer.
var checks atomic.Int64
analyzer.bundle=&integerABIControlledBundle{fixtureBundle:analyzer.bundle.(fixtureBundle),check:func() error { checks.Add(1); return nil }}
results:=make(chan error,2)
for _, digest := range []string{strings.Repeat("c",64),strings.Repeat("d",64)} {
    req:=request; req.CompileSnapshotDigest=digest
    go func(req AnalysisRequest) { _,err:=analyzer.Analyze(context.Background(),req); results<-err }(req)
}
firstErr,secondErr:=<-results,<-results
if firstErr!=nil || secondErr!=nil { t.Fatal("parallel request binding failed") }
if len(runner.specs)!=4 || checks.Load()!=6 { t.Fatal("parallel proof/process isolation failed") }
```

每个段落属于独立测试初始化，不能把同一个 runner 的旧 specs/calls 带到下一段的计数断言。AST 失败变体不使用“未启动 AST”的 ABI 断言。SharedDeadline 增加小于 25ms 的提前父 deadline 用例，并检查返回 errors.Is(context.DeadlineExceeded)、无第二条 spec；取消用例检查 errors.Is(context.Canceled)，无第二条 spec。DigestContainsTypes 使用 deep-copy 后的参数 Type.BitWidth=0 和现有 sealProgram，断言旧摘要与新摘要不等，未改副本的再封装摘要相等。

这些测试可用同包 fixture 或独立测试 runner，但不能把合成事实作为真实交接证据。先 gofmt 九文件中实际存在者，机械同步白名单，运行：

```powershell
Run-ABITest './internal/testgenanalysis' '^Test(AnalyzeIntegerABIProcessBoundary|AnalyzeIntegerABISpoofingBeforeLaunch|AnalyzeIntegerABIFailClosed|AnalyzeIntegerABIToolAndSourceChanges|AnalyzeIntegerABISharedDeadline|AnalyzeIntegerABINoCrossRequestCache|AnalyzeIntegerABIConcurrentIsolation|AnalyzeIntegerABIDigestContainsTypes|AnalyzeUsesOnlyVerifiedFixedCompilerWithCleanEnvironment|AnalyzeSuppressesImplicitHostHeaders|AnalyzeRejectsInjectedArgumentsBeforeLaunch|AnalyzeRejectsStaleSourceAndCancellation|AnalyzeRedactsCompilerFailure|AnalyzeRejectsIncludedHeaderAndForeignASTExcerpt|NativeGenerationSeedMatchesFixedAnalyzer|NativeGenerationSeedDirectiveReintroductionRejected)$'
```

要求这十六个顶级测试全部 run/pass、真实 exit 0、无 fail/skip。更新共享夹具的理由是其真实调用契约改变；不重跑 unrelated package/full suite。Task 1 代码未变化时其 GREEN 可继承同一次执行绑定，不重复同命令；若被修改，只重跑受影响的 Task 1 精确集合。

- [ ] **Step 7: 同一真实交接测试取得 GREEN，不再运行外部 native**

运行与 Step 2 完全相同的 `Run-ABITest './internal/runtime' '^TestProductionAnalysisToSolverIntegerABI$' -Native`。要求实际 Analyze、正式 bind、真实 solver、参数/返回 int 事实、最终摘要和向量范围通过；root 与两个子例全部 run/pass，Go exit 0。测试 tag 的 native 字样仅指真实工具交接，绝不等同完整 service-probe native 验收。

真实 Windows GREEN 不代表 Linux 真工具验证。Linux 的事实解析有 Task 1 合成正反例，但真实 Linux bundle 交接尚未执行；保留此缺口，后续授权环境才能验证，不现在触发 hosted 或下载 Linux 工具。若 GREEN 在后续不同边界失败，报告新的闭合证据，保持 solver/oracle 安全检查，不改种子/延时/重跑全套碰运气。

- [ ] **Step 8: 审查、单范围提交与 W4 交接**

最终规格核对：十一种基础整数；版本/平台/范围证明；消费宏拒绝；source/bundle/AST/excerpt 前后校验；共同 <=30s deadline；64 KiB/16 MiB 输出；无事实缓存；最终摘要；真正 RED/GREEN；V2/协议/求解器无变化。完成当前执行方式对应的规格和代码审查，只修已确认的本项问题。

只暂存 Task 2 七文件（Task 1 两文件已单独提交），先核对 index 与 whitelist 完全相同再 commit：

```powershell
git add -- apps/test-service/internal/testgenanalysis/scalar_abi_probe.go apps/test-service/internal/testgenanalysis/scalar_abi_analysis_test.go apps/test-service/internal/testgenanalysis/analyzer.go apps/test-service/internal/testgenanalysis/clang.go apps/test-service/internal/testgenanalysis/analyzer_test.go apps/test-service/internal/testgenanalysis/native_seed_contract_test.go apps/test-service/internal/runtime/production_analysis_solver_native_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m 'fix: bind trusted integer ABI to real analysis and solver'
```

ignored 新 SDD 记录 Task 1/2 实际提交、文件哈希、RED/GREEN 测试身份/退出码、编译器绑定、budget、原 status/frozen/只读路径检查和仍未执行的 Linux/完整 native。禁止改写旧失败回执或把 local proof 写成 W4/Phase10 完成；不携带 raw logs/READY/环境/源码/协议 body。

此时停止实施，给用户提供实际局部证据与下一门槛建议：另行授权基于新提交的首版 service-only build 和一次有界 Windows native。旧 native/构建许可已消耗；不自动继续，不推送/PR/合并/Release/签名。

## 计划自审与执行交接

设计 §1–2 的原因/方案边界由真实 Task 2 RED 及 Task 1 非猜测事实覆盖；§3 整数范围由 Task 1 十一种映射和未证明类型回归覆盖；§4–5 的调用、安全、身份、摘要、预算由 Task 1 闭合解析 + Task 2 协调覆盖；§6 的合成/真实区分和两种子交接由各自独立命令覆盖；§7 的保护/限制由每次 guard、提交 whitelist 和末尾停止点覆盖。

本计划保存时无实现、无测试结果、无 native/remote；不能标记任何任务完成。用户选择：1，subagent-driven（每任务新实施代理及两阶段审查）；2，本会话 inline executing-plans。选择后才初始化 Task 1 的执行记录；本计划本身不授权现在创建代理。
