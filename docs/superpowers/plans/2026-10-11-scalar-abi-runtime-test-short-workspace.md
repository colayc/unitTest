# Windows Scalar ABI Regression Short Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 仅修正真实整数交接回归的 Windows 临时目录布局，使两份原种子取得原 ABI 缺陷的合格行为 RED，随后恢复已有 Task 2。

**Architecture:** 在现有 opt-in 回归文件内增加测试私有目录 lease。Windows 在已验证 runtime 包目录下创建短的独立子目录，检查长度、身份和安全清理；Linux 保留 t.TempDir。生产运行器和全局 TestMain 不变。

**Tech Stack:** 现有 Go 工具 `D:/Program Files/Go/bin/go.exe`、gofmt、PowerShell 7、Go 标准库、原批准 Windows Clang bundle、现有首版隔离 scratch。

## Global Constraints

这些限制用于本补充的目录修正阶段（Step 1–6）。Step 7 不扩大此阶段权限，而是在该检查点合格后恢复原 Task 2 的独立文件作用域与既有全局限制。

- 本项只排除第一版 Phase 10 真实分析到求解回归的 Windows 测试目录障碍；第二版新增需求继续暂停。
- 根测试 `TestProductionAnalysisToSolverIntegerABI`、`cpputest`/`unity` 子测试、`scalarabi_integration` 编译标签及批准 bundle 的 opt-in 条件均不变。
- 不修改全局 TestMain、TEMP/TMP/GOTMPDIR、进程 cwd、生产 probe/winprocess、Analyzer、solver、种子字节、公开头文件、CMake、bundle、数据库或第二版文件。
- Linux 继续使用原 t.TempDir 逻辑，不启动 Linux 真工具、不下载工具。Task 1 不重新实施或重跑。
- 工作目录及实际源码、头文件路径的 UTF-16 长度均不得超过 240。240 是本测试的保守上限，不是新的 Windows 系统上限。
- 沿用每次 Analyze 最大 30 秒、AST 输出上限 16 MiB、solver 预算 5 秒/32 个候选/64 MiB/并发 1，以及原 Go 测试和外层控制器上限。
- 身份变化或无法安全清理时报告失败，不扩大删除范围；禁止按前缀批量删除，不加自动重试或单独 Clang 探测。
- 原 19 条脏/未跟踪状态、18 个冻结文件、10 个只读绑定保持不变；不重新初始化 scratch，不纳入暂停迁移 022。
- 不自动运行完整 native、全量/race/UI/hosted、服务构建、推送、PR、合并、master 同步、Release、签名或 legal 审批。

---

## 批准与执行上下文

[补充设计](../specs/2026-10-11-scalar-abi-runtime-test-short-workspace-design.md)已获用户书面确认，文档提交 `6608e36ce72d5c08341c36f1dcad6c32f7f3d33e`。本计划是[原计划](2026-10-10-trusted-scalar-abi-analysis-solver.md) Task 2 Step 1–2 的局部补充，不重开 Task 1；Task 1 提交 `2d76dd77` 的代码与验证继续继承。

执行目录仍是 `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`，分支 `codex/phase10-production-generation`。只读取当前 `.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/` 的 prepared.json、controller.ps1、owned-initial.json 和 progress.md。Scratch/ModuleRoot 以 prepared.json 为准，不能重跑 initialize.ps1。旧诊断控制器和回执保留、不修改、不执行。

开始时原新增回归 SHA-256 必须为 `7FA015EBA92A6B07E24B0A8B71BEE8492A4F0E671E1A67EBF097391E0DE6819A`；其四个已有 Task 2 分析源码仍与 owned-initial.json 相符。继承原 Assert-ABIProtected，并在每次调用前后对 runtime 包目录执行 Assert-ABIContained，拒绝重解析点。默认 cwd `C:/codex_project/unitTest` 不是执行目录。

本补充属于原暂停 Task 2 的恢复检查点，只有一个代码修改路径，不另派已完成 Task 1。执行方式获选择后才实施和运行；若继续子代理方式，向原 Task 2 实施代理交付本补充，并用新审查者核对本检查点，保持当前执行目录的账本。

### Task 2 插入步骤：短目录 lease 与真实双种子 RED

**Files:**

- Modify/Test: `apps/test-service/internal/runtime/production_analysis_solver_native_test.go`；只替换临时目录取得方式，增加同文件私有 helper/目录测试/闭合清理投影。其余分析、绑定、求解和断言不变。
- Create at execution only: 当前 SDD 目录内 `workspace-helper-controller.ps1`、`workspace-shortening-report.md` 和一次真实 RED 的私有 attempt receipt。
- Read-only: runtime_test.go、probe/winprocess、所有生产分析/solver 文件、种子/bundle/原 controller/prepared/诊断证据。修改权限不扩展到这些路径。

**Interfaces:**

- Consumes: 原回归已有 ownFile（goruntime.Caller）、source/header、spec.file/spec.header、原 Run-ABITest 和 Sync-ABIOwned。
- Produces: `scalarABIWorkspaceForTest(t *testing.T, parent string, relatives []string) string`。
- Produces: `scalarABIPackageParent() (string,error)`、`scalarABIWorkspaceCanonical(path string) bool`、`scalarABIWorkspaceWithinLimit(path string) bool`。
- Produces: `scalarABINewWorkspace(parent string, relatives []string) (scalarABIWorkspaceLease,error)`、`(*scalarABIWorkspaceLease).Close() error`；lease 字段 path/parent 为 string，identity/parentIdentity 为 os.FileInfo。
- Produces (PowerShell): `Assert-WorkspaceFixSources`、`Run-WorkspaceHelper -Phase red|green`；新模块加载只定义函数并加载原 controller，不启动 Go。
- Uses: 原 root 与两个子例；新目录测试根仅 `TestScalarABIWorkspaceParentBoundary`、`TestScalarABIWorkspaceSafetyLifecycle`。新 helper 控制器不扩大原控制器白名单。

- [ ] **Step 1：添加可运行的目录行为 RED，不接触真实工具**

先仅添加下面的 root 与薄 wrapper。wrapper 初始保持旧 t.TempDir 行为，原真实回归此时也仍用原 t.TempDir。Windows 上应在父目录断言失败，不能把缺函数或编译错误当 RED。新增 root 不打开 bundle、不调用 Analyzer/solver。import 尚不需要改变。

```go
func scalarABIWorkspaceForTest(t *testing.T, parent string, relatives []string) string {
    t.Helper()
    return t.TempDir()
}

func TestScalarABIWorkspaceParentBoundary(t *testing.T) {
    _, file, _, ok := goruntime.Caller(0)
    if !ok { t.Fatal("workspace_parent_identity_failed") }
    parent := filepath.Dir(file)
    work := scalarABIWorkspaceForTest(t, parent, []string{"src/classifier.cpp", "include/classifier.hpp"})
    if goruntime.GOOS == "windows" && filepath.Dir(work) != parent {
        t.Fatal("workspace_parent_boundary")
    }
}
```

- [ ] **Step 2：保存单一 helper 控制器并运行一次上述 RED**

用 apply_patch 从以下完整块保存新脚本，不复制或字符串替换旧诊断脚本。先通过 PowerShell Parser 检查：语法错误为零，red/green 正则字面量完整，Process.Start 的调用点只有一个；检查不执行脚本。控制器仅启动 Go 测试，两个选择器均排除真实工具 root；Go 自身编译测试不构成一次 Clang 探测。

```powershell
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'controller.ps1')
function Assert-WorkspaceFixSources {
    Assert-ABIProtected
    foreach($item in (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'owned-initial.json') -Raw | ConvertFrom-Json)) {
        if($item.Exists -and (Get-FileHash -LiteralPath (Join-Path $Workspace $item.Path)).Hash -ne $item.SHA256){throw 'Earlier Task2 integration refused'}
    }
    $fixed=@{
        'apps/test-service/internal/testgenanalysis/scalar_abi.go'='CB1375EE2A7E43D196AFB155E6F8B9E3902FF8E7F200E33EEF90C0A55DFCDEFB'
        'apps/test-service/internal/testgenanalysis/scalar_abi_test.go'='D3B3C581C2048D39EC2A2A0FB4BFE442548D52FF8257B9E876EE951379700D34'
    }
    foreach($path in $fixed.Keys){if((Get-FileHash -LiteralPath (Join-Path $Workspace $path)).Hash -ne $fixed[$path]){throw 'Task1 changed'}}
    foreach($name in @('scalar_abi_probe.go','scalar_abi_analysis_test.go')) {
        if(Test-Path -LiteralPath (Join-Path $Workspace ('apps/test-service/internal/testgenanalysis/'+$name))){throw 'Earlier Task2 integration refused'}
    }
    foreach($file in @($Prepared.Bindings | Where-Object {$_.Path -match 'tools/service-probe/fixtures/test-generation/.+/(src|include|tests)/'})) {
        $copy=Join-Path $Scratch $file.Path
        Assert-ABIContained $copy $Scratch
        if((Get-FileHash -LiteralPath $copy).Hash -ne $file.SHA256){throw 'Scratch seed changed'}
    }
}
function Run-WorkspaceHelper {
param([Parameter(Mandatory=$true)][ValidateSet('red','green')][string]$Phase)
if(-not $IsWindows){throw 'Windows helper qualification only'}
Assert-WorkspaceFixSources
Assert-ABIProtected
Assert-ABIContained (Join-Path $ModuleRoot 'internal/runtime') $Workspace
if (Test-Path -LiteralPath (Join-Path $ModuleRoot 'internal/taskstore/migrations/022_case_definitions.sql')) { throw 'Paused migration leaked' }
$Regex=if($Phase -eq 'red'){'^TestScalarABIWorkspaceParentBoundary$'}else{'^Test(ScalarABIWorkspaceParentBoundary|ScalarABIWorkspaceSafetyLifecycle)$'}
$Roots=@(if($Phase -eq 'red'){'TestScalarABIWorkspaceParentBoundary'}else{'TestScalarABIWorkspaceParentBoundary';'TestScalarABIWorkspaceSafetyLifecycle'})
Sync-ABIOwned
$file=Join-Path $Workspace 'apps/test-service/internal/runtime/production_analysis_solver_native_test.go'
$sha=(Get-FileHash -LiteralPath $file).Hash
$base=Join-Path $Scratch ('workspace-helper-'+$Phase+'-'+$sha)
if (Test-Path -LiteralPath ($base+'.attempt.json')) { throw 'Same code already attempted; no retry' }
$runtimeParent=Join-Path $ModuleRoot 'internal/runtime'
$beforeDirs=@(Get-ChildItem -LiteralPath $runtimeParent -Directory -Filter '.abi-*' | Select-Object -ExpandProperty Name)
[IO.File]::WriteAllText(($base+'.attempt.json'),([pscustomobject]@{Phase=$Phase;Regex=$Regex;SHA256=$sha} | ConvertTo-Json))
$psi=[Diagnostics.ProcessStartInfo]::new()
$psi.FileName='D:/Program Files/Go/bin/go.exe'; $psi.WorkingDirectory=$ModuleRoot
$psi.UseShellExecute=$false; $psi.CreateNoWindow=$true
$psi.RedirectStandardOutput=$true; $psi.RedirectStandardError=$true
foreach($pair in @{GOWORK='off';GOPROXY='off';GOSUMDB='off';GOFLAGS='-mod=readonly';GOTOOLCHAIN='local'}.GetEnumerator()) {$psi.Environment[$pair.Key]=$pair.Value}
$psi.Environment.Remove('UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT') | Out-Null
$psi.Environment.Remove('UNIT_TEST_IDE_SCALAR_ABI_DIAG_BUNDLE_ROOT') | Out-Null
foreach($arg in @('test','-json','-count=1','-run',$Regex,'-tags=scalarabi_integration','-timeout=60s','./internal/runtime')) {$psi.ArgumentList.Add($arg)}
$p=[Diagnostics.Process]::new(); $p.StartInfo=$psi
if(-not $p.Start()){throw 'Go did not start; no retry'}
$out=$p.StandardOutput.ReadToEndAsync(); $err=$p.StandardError.ReadToEndAsync()
if(-not $p.WaitForExit(90000)){
    try{$p.Kill($true)}catch{}
    $closed=$p.WaitForExit(15000); $p.Dispose()
    throw "Helper timeout; exit-confirmed=$closed; no retry"
}
$code=$p.ExitCode
if(-not [Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($out,$err),5000)){
    $p.StandardOutput.Dispose(); $p.StandardError.Dispose(); $p.Dispose()
    throw 'Output did not converge; no qualification'
}
$raw=$out.GetAwaiter().GetResult(); $stderr=$err.GetAwaiter().GetResult(); $p.Dispose()
[IO.File]::WriteAllText(($base+'.stdout.jsonl'),$raw)
[IO.File]::WriteAllText(($base+'.stderr.log'),$stderr)
$entries=@(foreach($line in ($raw -split '\r?\n')){if($line){$line | ConvertFrom-Json -ErrorAction Stop}})
$events=@($entries | Where-Object {$_.Test -and $_.Action -in @('run','pass','fail','skip')} | ForEach-Object {[pscustomobject]@{Action=$_.Action;Test=$_.Test}})
foreach($root in $Roots){if(@($events | Where-Object {$_.Test -eq $root -and $_.Action -eq 'run'}).Count -ne 1){throw 'Missing selected root; not behavioral evidence'}}
if(@($events | Where-Object {$_.Action -eq 'skip'}).Count){throw 'Skipped helper evidence'}
if($Phase -eq 'red'){
    $hits=@($entries | Where-Object {$_.Test -eq $Roots[0] -and $_.Output -match '\bworkspace_parent_boundary\b'}).Count
    if($code -ne 1 -or $hits -ne 1 -or @($events | Where-Object {$_.Test -eq $Roots[0] -and $_.Action -eq 'fail'}).Count -ne 1){throw 'Not prescribed parent-boundary RED'}
}else{
    if($code -ne 0 -or @($events | Where-Object {$_.Action -eq 'fail'}).Count){throw 'Not complete helper GREEN'}
    foreach($root in $Roots){if(@($events | Where-Object {$_.Test -eq $root -and $_.Action -eq 'pass'}).Count -ne 1){throw 'Missing helper PASS'}}
    foreach($child in @('bounds','isolation_and_bytes','unsafe_parent_and_relative','failure_cleanup','identity_change')) {
        $name='TestScalarABIWorkspaceSafetyLifecycle/'+$child
        foreach($action in @('run','pass')) {if(@($events | Where-Object {$_.Test -eq $name -and $_.Action -eq $action}).Count -ne 1){throw 'Missing helper child evidence'}}
    }
}
if((Get-FileHash -LiteralPath $file).Hash -ne $sha){throw 'Test bytes changed during run'}
$afterDirs=@(Get-ChildItem -LiteralPath $runtimeParent -Directory -Filter '.abi-*' | Select-Object -ExpandProperty Name)
if([string]::Join('|',[string[]]@($beforeDirs | Sort-Object)) -ne [string]::Join('|',[string[]]@($afterDirs | Sort-Object))){throw 'Workspace cleanup inventory changed'}
Assert-ABIProtected
Assert-WorkspaceFixSources
Assert-ABIContained (Join-Path $ModuleRoot 'internal/runtime') $Workspace
[pscustomobject]@{Phase=$Phase;GoExitCode=$code;Events=$events;SHA256=$sha;Protected=$true} | ConvertTo-Json -Depth 4
}
```

执行前先对新脚本做 Parser::ParseFile 检查，不执行函数；除加载原 controller 和函数定义外，不允许任何顶级 helper 调用或进程启动。运行使用以下精确 worktree-relative 路径，不能把 shell cwd 切到 SDD：

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w 'apps/test-service/internal/runtime/production_analysis_solver_native_test.go'
. '.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/workspace-helper-controller.ps1'
Assert-WorkspaceFixSources
Run-WorkspaceHelper -Phase red
```

预期自然 Go exit 1、选定 root run/fail 各一次、固定断言一次，无 skip。只接受这一次规定的行为 RED；其他错误停止，不得自动再次执行。记录该 RED 后继续 Step 3，不把它当真实 ABI RED。

- [ ] **Step 3：先补齐目录测试，再实现最小 lease 和 Windows wrapper**

新增标准库 import `bytes`、`errors`、`strings`、`unicode/utf16`。下列测试与 helper 同文件；不新增生产 hook。测试先写入，然后用下方已定义接口完成 helper。所有测试使用已验证包目录，不创建权限敏感链接，不修改环境，不调用 Clang。

最终 helper 代码中的 scalarABIWorkspaceForTest 必须替换 Step 1 的薄 wrapper，不得把两份同名函数一起保留。

```go
func TestScalarABIWorkspaceSafetyLifecycle(t *testing.T) {
    parent, err := scalarABIPackageParent()
    if err != nil { t.Fatal("workspace_test_parent_failed") }
    beforeCWD, err := os.Getwd()
    if err != nil { t.Fatal("workspace_test_cwd_failed") }
    before := map[string]string{}
    for _, key := range []string{"TEMP", "TMP", "GOTMPDIR"} { before[key] = os.Getenv(key) }
    relatives := []string{"src/classifier.cpp", "include/classifier.hpp"}
    makeLease := func(t *testing.T) *scalarABIWorkspaceLease {
        t.Helper()
        lease, err := scalarABINewWorkspace(parent, relatives)
        if lease.path != "" { t.Cleanup(func(){if lease.Close()!=nil {t.Error("workspace_test_cleanup_failed")}}) }
        if err != nil { t.Fatal("workspace_valid_directory_refused") }
        return &lease
    }
    t.Run("bounds", func(t *testing.T) {
        for _, value := range []string{strings.Repeat("a",240),strings.Repeat("😀",120)} {
            if !scalarABIWorkspaceWithinLimit(value) {t.Fatal("workspace_limit_rejected_boundary")}
        }
        for _, value := range []string{strings.Repeat("a",241),strings.Repeat("😀",121)} {
            if scalarABIWorkspaceWithinLimit(value) {t.Fatal("workspace_limit_accepted_overflow")}
        }
    })
    t.Run("isolation_and_bytes", func(t *testing.T) {
        first, second := makeLease(t), makeLease(t)
        if first.path==second.path || filepath.Dir(first.path)!=parent || filepath.Dir(second.path)!=parent {t.Fatal("workspace_not_isolated")}
        for _, lease := range []*scalarABIWorkspaceLease{first,second} {
            if !scalarABIWorkspaceWithinLimit(lease.path) {t.Fatal("workspace_root_too_long")}
            for _, relative := range relatives {
                path:=filepath.Join(lease.path,filepath.FromSlash(relative))
                if !scalarABIWorkspaceWithinLimit(path) {t.Fatal("workspace_input_too_long")}
                if os.MkdirAll(filepath.Dir(path),0700)!=nil {t.Fatal("workspace_input_parent_failed")}
                input:=[]byte("exact test input\n")
                if os.WriteFile(path,input,0600)!=nil {t.Fatal("workspace_input_copy_failed")}
                got,err:=os.ReadFile(path)
                if err!=nil || !bytes.Equal(got,input) {t.Fatal("workspace_input_changed")}
            }
            path:=lease.path
            if lease.Close()!=nil {t.Fatal("workspace_normal_cleanup_failed")}
            if _,err:=os.Lstat(path); !os.IsNotExist(err) {t.Fatal("workspace_normal_cleanup_leaked")}
        }
    })
    t.Run("unsafe_parent_and_relative", func(t *testing.T) {
        for _, value := range []string{"relative",parent+string(filepath.Separator)+".",parent+strings.Repeat("a",241)} {
            lease,err:=scalarABINewWorkspace(value,relatives)
            if lease.path!="" {t.Cleanup(func(){if lease.Close()!=nil {t.Error("workspace_rejection_cleanup_failed")}})}
            if err==nil {t.Fatal("workspace_unsafe_parent_accepted")}
        }
        lease,err:=scalarABINewWorkspace(parent,[]string{"../escape"})
        if lease.path!="" {t.Cleanup(func(){if lease.Close()!=nil {t.Error("workspace_rejection_cleanup_failed")}})}
        if err==nil {t.Fatal("workspace_relative_escape_accepted")}
        oversized,err:=scalarABINewWorkspace(parent,[]string{strings.Repeat("a",241)})
        if oversized.path!="" {t.Cleanup(func(){if oversized.Close()!=nil {t.Error("workspace_rejection_cleanup_failed")}})}
        if err==nil || oversized.path=="" || oversized.Close()!=nil {t.Fatal("workspace_postcreate_rejection_failed")}
    })
    t.Run("failure_cleanup", func(t *testing.T) {
        lease:=makeLease(t); path:=lease.path
        cleanupFailed:=false
        failure:=func()error{
            defer func(){cleanupFailed=lease.Close()!=nil}()
            ctx,cancel:=context.WithCancel(context.Background()); cancel()
            return ctx.Err()
        }()
        if !errors.Is(failure,context.Canceled) || cleanupFailed {t.Fatal("workspace_failure_cleanup_failed")}
        if _,err:=os.Lstat(path); !os.IsNotExist(err) {t.Fatal("workspace_failure_cleanup_leaked")}
    })
    t.Run("identity_change", func(t *testing.T) {
        first,second:=makeLease(t),makeLease(t)
        originalPath:=first.path; moved:=originalPath+".saved"
        if filepath.Dir(moved)!=parent {t.Fatal("workspace_move_escaped_parent")}
        if _,err:=os.Lstat(moved); !os.IsNotExist(err) {t.Fatal("workspace_move_target_exists")}
        if os.Rename(first.path,moved)!=nil {t.Fatal("workspace_identity_move_failed")}
        first.path=moved
        if filepath.Dir(second.path)!=parent || filepath.Dir(originalPath)!=parent {t.Fatal("workspace_replacement_escaped_parent")}
        if _,err:=os.Lstat(originalPath); !os.IsNotExist(err) {t.Fatal("workspace_replacement_target_exists")}
        if os.Rename(second.path,originalPath)!=nil {t.Fatal("workspace_identity_replacement_failed")}
        second.path=originalPath
        stale:=*first; stale.path=originalPath
        if stale.Close()==nil {t.Fatal("workspace_changed_identity_deleted")}
        current,err:=os.Lstat(second.path)
        if err!=nil || !os.SameFile(current,second.identity) {t.Fatal("workspace_replacement_changed")}
    })
    afterCWD,err:=os.Getwd()
    if err!=nil || afterCWD!=beforeCWD {t.Fatal("workspace_global_cwd_changed")}
    for key,value:=range before {if os.Getenv(key)!=value {t.Fatal("workspace_global_environment_changed")}}
}
```

identity_change 实际重命名两个自有空目录，把另一个身份放到旧路径，核对旧 lease 拒绝删除；移动前核对目标为已验证父目录的直接子目录且不存在，更新每个自有 lease 的实际位置以完成安全 cleanup。canonical 解析覆盖链接拒绝，祖先重解析点保护由原控制器的实际检查承担；本项不增加特权 junction/native 测试。

```go
var errScalarABIWorkspace = errors.New("scalar ABI test workspace refused")

type scalarABIWorkspaceLease struct {
    path, parent string
    identity, parentIdentity os.FileInfo
}

func scalarABIWorkspaceWithinLimit(path string) bool {
    return len(utf16.Encode([]rune(path)))<=240
}

func scalarABIWorkspaceCanonical(path string) bool {
    if !filepath.IsAbs(path) || filepath.Clean(path)!=path {return false}
    info,err:=os.Lstat(path)
    if err!=nil || !info.IsDir() || info.Mode()&os.ModeSymlink!=0 {return false}
    resolved,err:=filepath.EvalSymlinks(path)
    if err!=nil {return false}
    if goruntime.GOOS=="windows" {return strings.EqualFold(filepath.Clean(resolved),path)}
    return filepath.Clean(resolved)==path
}

func scalarABIPackageParent() (string,error) {
    _,file,_,ok:=goruntime.Caller(0)
    if !ok {return "",errScalarABIWorkspace}
    parent:=filepath.Dir(file)
    cwd,err:=os.Getwd()
    if err!=nil || cwd!=parent || !scalarABIWorkspaceCanonical(parent) {return "",errScalarABIWorkspace}
    return parent,nil
}

func scalarABINewWorkspace(parent string, relatives []string) (scalarABIWorkspaceLease,error) {
    var lease scalarABIWorkspaceLease
    expected,err:=scalarABIPackageParent()
    if err!=nil || parent!=expected || !scalarABIWorkspaceWithinLimit(parent) {return lease,errScalarABIWorkspace}
    for _,relative:=range relatives {
        local:=filepath.FromSlash(relative)
        if !filepath.IsLocal(local) || filepath.Clean(local)!=local {return lease,errScalarABIWorkspace}
    }
    parentInfo,err:=os.Lstat(parent)
    if err!=nil || !os.SameFile(parentInfo,parentInfo) {return lease,errScalarABIWorkspace}
    path,err:=os.MkdirTemp(parent,".abi-")
    if err!=nil {return lease,errScalarABIWorkspace}
    lease=scalarABIWorkspaceLease{path:path,parent:parent,parentIdentity:parentInfo}
    info,err:=os.Lstat(path)
    if err!=nil || !os.SameFile(info,info) {return lease,errScalarABIWorkspace}
    lease.identity=info
    if filepath.Dir(path)!=parent || !scalarABIWorkspaceCanonical(path) || !scalarABIWorkspaceWithinLimit(path) {
        return lease,errScalarABIWorkspace
    }
    for _,relative:=range relatives {
        if !scalarABIWorkspaceWithinLimit(filepath.Join(path,filepath.FromSlash(relative))) {return lease,errScalarABIWorkspace}
    }
    return lease,nil
}

func (lease *scalarABIWorkspaceLease) Close() error {
    if lease.path=="" {return nil}
    parent,err:=scalarABIPackageParent()
    if err!=nil || parent!=lease.parent || filepath.Dir(lease.path)!=parent || !strings.HasPrefix(filepath.Base(lease.path),".abi-") {
        return errScalarABIWorkspace
    }
    parentInfo,err:=os.Lstat(parent)
    if err!=nil || lease.parentIdentity==nil || !os.SameFile(parentInfo,lease.parentIdentity) {return errScalarABIWorkspace}
    info,err:=os.Lstat(lease.path)
    if err!=nil || lease.identity==nil || !os.SameFile(info,lease.identity) || !scalarABIWorkspaceCanonical(lease.path) {
        return errScalarABIWorkspace
    }
    if os.RemoveAll(lease.path)!=nil {return errScalarABIWorkspace}
    if _,err:=os.Lstat(lease.path); !os.IsNotExist(err) {return errScalarABIWorkspace}
    lease.path=""
    return nil
}

func scalarABIWorkspaceForTest(t *testing.T, parent string, relatives []string) string {
    t.Helper()
    if goruntime.GOOS!="windows" {return t.TempDir()}
    lease,err:=scalarABINewWorkspace(parent,relatives)
    if lease.path!="" {
        t.Cleanup(func(){
            if lease.Close()!=nil {t.Error("scalar_abi_workspace_cleanup=failed")}else{t.Log("scalar_abi_workspace_cleanup=ok")}
        })
    }
    if err!=nil {t.Fatal("scalar_abi_workspace_setup=failed")}
    if len(relatives)==2 {
        t.Logf("scalar_abi_workspace_lengths=%d/%d/%d",len(utf16.Encode([]rune(lease.path))),
            len(utf16.Encode([]rune(filepath.Join(lease.path,filepath.FromSlash(relatives[0]))))),
            len(utf16.Encode([]rune(filepath.Join(lease.path,filepath.FromSlash(relatives[1]))))))
    }
    return lease.path
}
```

当前固定 Go 的 Windows FileInfo 懒加载身份（本机标准库 os/types_windows.go 的 loadFileId/sameFile）；创建时的 SameFile(info,info) 必须成功，以立即取得并固定父目录/新目录的文件身份，不能等到 cleanup 才首次计算。实际身份替换测试独立验证该条件。factory 返回有 path 的 error 时，调用方也必须先登记 cleanup 再报告准备失败，不能丢掉 lease。若无法取得目录身份，Close 失败关闭并报告清理失败，不对未知身份目录删除；不伪称无泄漏。普通 success/Fatal/cancel 收尾使用同一 callback；强行终止整个 Go 进程的限制不变。

在原真实回归中只将 `work := t.TempDir()` 替换为：

```go
work := scalarABIWorkspaceForTest(t, filepath.Dir(ownFile),
    []string{"src/"+spec.file,"include/"+spec.header})
```

源码读取/写入、真实摘要、编译 args、Analyze/bind/Solve、位宽及向量断言逐项保留。不能把 solver 的预期 Fatal 移到前面以伪造 RED。只有 Windows 新 helper 才输出上述三个长度和清理类别。

- [ ] **Step 4：运行一次 helper GREEN，不重复原 Task 1**

运行以下命令；要求两个新 root 及 SafetyLifecycle 的五个子例全部 run/pass，实际 Go exit 0、零 fail/skip，控制器和报告同时核对。检查 `.abi-` 子目录的前后集合没有新遗留；只读枚举，不按前缀删除。任一准备、拒绝、释放、身份或环境断言失败均停止，不启动真实工具。

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w 'apps/test-service/internal/runtime/production_analysis_solver_native_test.go'
. '.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/workspace-helper-controller.ps1'
Assert-WorkspaceFixSources
Run-WorkspaceHelper -Phase green
```

在一次真实工具调用之前，按执行方式做规格、代码质量两阶段预审，核对 Step 6 所列 diff 条件和 helper 证据。若预审发现本项代码问题，由实施者修正；仅对实际变化的新源码 SHA 做必要 helper GREEN，不重复旧 RED，不用只改注释或文件盐绕过同代码尝试保护。原记录保留。预审及最终 helper GREEN 必须绑定即将运行真实 RED 的同一源码；不把此预审当已取得真实资格。

- [ ] **Step 5：只运行一次原真实双种子 RED**

先保存唯一 `.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/workspace-real-red.attempt.json`，绑定本次 regression SHA、HEAD、批准 bundle 路径/摘要及固定选择器；若已有则拒绝再次启动。回执为控制器派生产物，报告/账本使用 apply_patch；不修改旧 receipt。真实调用只有原控制器命令：

```powershell
. '.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/workspace-helper-controller.ps1'
Assert-WorkspaceFixSources
Assert-ABIContained (Join-Path $ModuleRoot 'internal/runtime') $Workspace
$attempt=Join-Path $Workspace '.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/workspace-real-red.attempt.json'
if(Test-Path -LiteralPath $attempt){throw 'Already attempted real RED; no retry'}
$bundleRoot=Join-Path $Workspace '.superpowers/runtime/product-bundles/windows-x64/bundles/testgen'
Assert-ABIContained $bundleRoot $Workspace
$manifest=(Get-FileHash -LiteralPath (Join-Path $bundleRoot 'manifest.json')).Hash.ToLowerInvariant()
if($manifest -ne 'e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345'){throw 'Approved bundle identity mismatch'}
$sha=(Get-FileHash -LiteralPath (Join-Path $Workspace 'apps/test-service/internal/runtime/production_analysis_solver_native_test.go')).Hash
[IO.File]::WriteAllText($attempt,([pscustomobject]@{Head=(git rev-parse HEAD).Trim();SHA256=$sha;BundleRoot=$bundleRoot;ManifestSHA256=$manifest;Regex='^TestProductionAnalysisToSolverIntegerABI$';Attempted=$true} | ConvertTo-Json))
$beforeLogs=@(Get-ChildItem -LiteralPath $Scratch -Filter '*.stdout.jsonl' -File | Select-Object -ExpandProperty Name)
$runtimeParent=Join-Path $ModuleRoot 'internal/runtime'
$beforeDirs=@(Get-ChildItem -LiteralPath $runtimeParent -Directory -Filter '.abi-*' | Select-Object -ExpandProperty Name)
$result=Run-ABITest './internal/runtime' '^TestProductionAnalysisToSolverIntegerABI$' -Native | ConvertFrom-Json -ErrorAction Stop
$newLogs=@(Get-ChildItem -LiteralPath $Scratch -Filter '*.stdout.jsonl' -File | Where-Object {$_.Name -notin $beforeLogs})
if($newLogs.Count -ne 1){throw 'Real run log identity ambiguous; no retry'}
if($result.GoExitCode -ne 1){throw 'Not prescribed real RED; no retry'}
if((Get-FileHash -LiteralPath (Join-Path $Workspace 'apps/test-service/internal/runtime/production_analysis_solver_native_test.go')).Hash -ne $sha){throw 'Regression changed during real RED'}
$afterDirs=@(Get-ChildItem -LiteralPath $runtimeParent -Directory -Filter '.abi-*' | Select-Object -ExpandProperty Name)
if([string]::Join('|',[string[]]@($beforeDirs | Sort-Object)) -ne [string]::Join('|',[string[]]@($afterDirs | Sort-Object))){throw 'Real workspace cleanup inventory changed'}
Assert-WorkspaceFixSources
```

执行前确认 attempt 尚不存在再写入，不能执行新旧脚本的重复尾部；Run-ABITest 的自然 Go exit 与外层 shell exit 分开记录。只在私有 JSONL 内解析固定 Fatal `real analyzed integers cannot be solved`、`scalar_abi_workspace_cleanup=ok` 及三个整数长度，不输出原始日志。资格门槛全部满足才解除较早边界：

- 实际 Go exit 1，root 与 cpputest/unity 各 run/fail 一次，无 skip。
- 每个子例的既有 solver Fatal 恰好一次；任何其他 Fatal、准备/分析/绑定/预算错误都不算目标 RED。
- 每个子例长度三元组恰好一次、均为 1–240；cleanup=ok 各一次、无 cleanup=failed。
- 本次标准输出/错误日志、回执、回归代码和批准工具绑定哈希保留，输入源码/头文件/driver 六个绑定未变，未变分析源码/Task 1/冻结绑定通过。
- 所有新自有目录已释放；无完整 native、oracle/render、生成用例编译、CMake 或 Linux 调用。

同一短目录回归到达 solver 才能确认先前路径障碍已排除；这仍是 ABI 缺陷的 RED，不是整条功能 GREEN。不同较早失败立即交回控制器，不启动生产集成或追加探测。

- [ ] **Step 6：独立审查此检查点并单文件提交**

审查 diff 与当前原文件：只有 wrapper/lease/目录测试/闭合投影，原 seed/args/budgets/Analyze/bind/solver/断言不变，Linux 原行为保留，无共享目录、全局环境修改、按前缀删除或 raw 日志。Step 4 预审依次完成规格与代码质量门槛；此步骤在相同源码 SHA 上补充核对真实 RED、cleanup 和保护证据。执行方式允许子代理审查时使用独立新审查者；不让控制器代修原实施代理代码，不重跑相同代码的已记录测试。若真实 RED 后才发现必须改变代码的问题，停止资格提交并报告；新的真实调用必须另获 scoped 授权，不重复已消耗的一次许可。

只有 helper GREEN、合格双种子 RED、保护/cleanup 及审查均通过，才提交新增回归文件（其中包含原 Task 2 尚未提交的真实回归及本目录修正）：

```powershell
$file='apps/test-service/internal/runtime/production_analysis_solver_native_test.go'
git add -- $file
if($LASTEXITCODE -ne 0){throw 'Staging failed'}
git diff --cached --check
if($LASTEXITCODE -ne 0){throw 'Whitespace check failed'}
$staged=@(git diff --cached --name-only)
if($LASTEXITCODE -ne 0 -or $staged.Count -ne 1 -or $staged[0] -ne $file){throw 'Scope mismatch; no commit'}
git commit -m 'test: bound Windows integer ABI regression workspaces'
if($LASTEXITCODE -ne 0){throw 'Checkpoint commit failed'}
Assert-WorkspaceFixSources
```

更新当前账本和 workspace-shortening-report.md：实际 RED/GREEN/root/子例、自然 Go exit、三个长度、清理证据、输入/工具/代码/日志摘要、提交、保护和局部验收限制。不能把当前目录修正提交标记为 Task 2 完成。

- [ ] **Step 7：恢复原 Task 2，仅执行剩余明确门槛**

本检查点合格后，由控制器向原 Task 2 实施代理传递新提交及审查/资格记录，原计划 Step 1–2 以本次真实双种子 RED 作为唯一合格证据；保留旧失败记录但不重复此 RED。不再重新生成 scratch、不重派 Task 1。

随后执行原计划 Task 2 Step 3–8：三个协调 root 的行为 RED、六个分析包生产/测试路径的受限集成、十六个协调/安全 root 的一次 GREEN、同一真实双种子的一次 GREEN、两阶段审查及提交。此阶段恢复原 controller 的原 guard，不继续使用只允许尚未集成源码的 Assert-WorkspaceFixSources；该新 guard 专属于本目录检查点。保留共同 <=30s 截止时间、64 KiB/16 MiB 输出、前后 source/bundle/AST 校验、无事实缓存和完整类型摘要。该次真实 GREEN 是 ABI 集成后原有验收门槛，不是目录修正失败的自动重试。

原 Task 2 Step 8 暂存七路径命令可沿用，但实际 staged whitelist 改为六个分析路径；本回归文件已经在本检查点提交，未再修改时不会出现在 index。若后续确有与原计划一致的必要回归改动，必须由控制器列明理由并把此第七路径加入该次实际 whitelist，不能误提交其他文件。本检查点之后才允许这些原 Task 2 分析改动，不能提前集成以让目录验证跳过预期 RED。

原 Task 2 GREEN 若在新边界失败，按既有规则停下，不修改 solver/oracle 或种子。整个 Task 2 完成后再按原停止点请求服务构建/一次完整 Windows native 的新授权；本计划不授权那些运行或任何 remote 写操作。

## 自审与执行交接

补充设计的局部 scope/Windows-only 由 Step 1/3/6 覆盖；规范父目录、身份、长度、独立输入、释放、环境不变由 helper 与五子例及控制器 guard 覆盖；唯一真实双种子、闭合证据、原预算、较早失败停止由 Step 5 覆盖；Task 2 恢复且不重复 Task 1/RED、最终 staged 范围由 Step 6–7 覆盖。本文所有接口及代码中的方法在本 task 或既有控制器中定义，无新增依赖或未定义接口。

计划保存阶段只新增并本地提交本文，没有实施、Go/Clang/测试、scratch 初始化或代理派发。后续选择：1，继续原子代理实施并在此检查点独立审查（推荐）；2，切换本会话 inline executing-plans，保留当前准备/证据和审查门槛。选择后才执行本补充。
