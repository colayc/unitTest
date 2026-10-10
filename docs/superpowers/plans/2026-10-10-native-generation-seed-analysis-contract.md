# Native Generation Seed Analysis Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正两份真实原生生成样例与固定分析器的前置契约，取得真实行为 RED/GREEN，保持所有生产安全规则不变。

**Architecture:** 仅移除 CppUTest/Unity 实现文件开头的自身头文件 include；公开头文件、函数体、测试入口及 CMake 字节保留。在现有 testgenanalysis 包中新建离线回归文件，读取真实仓库样例并调用真实 Analyzer.Analyze，通过已有测试 runner 的调用计数验证前置边界，不伪造 Clang 分析或覆盖率成功。

**Tech Stack:** Go 1.26.0+（当前本机 1.26.6）、C++17/C11、PowerShell、Git archive、已有 fixtureAnalysis/fixtureBundle/fixtureRunner。

## Global Constraints

- 本项属于第一版 Phase 10 / Windows CMake 兼容 W4 的验收样例修正，不是第二版功能开发。
- 后续实施仅允许以下三处源码/测试改动：两份 classifier 实现和新增 native_seed_contract_test.go；完整路径见 Task 1。
- 函数体逐字节保留；不改变参数、返回类型、C++ noexcept、两个判断及 -1/0/1 返回值。
- 所有 CMake 文件、头文件、测试入口、框架配方、编译参数、正式生成流水线与暂存 helper 均保持字节不变。
- 源码指令、越界 include、外来 AST 摘录仍拒绝；不允许 PATH 回退、下载工具、直接复用历史权限或放开宏。
- 本次不修改分析器、TG1 编码、错误分类、协议、数据库 schema、日志输出、资源预算或超时。
- 使用从提交导出的首版源码和明确列出的本项改动进行离线、精确包/测试名验证，不编译带暂停迁移 022 的脏模块。
- 单次 Go 定点测试最多 60 秒；不运行全量、race、UI、hosted、工具下载、真实 CMake/Clang 或 native。
- 既有冻结 18 个文件、19 条脏/未跟踪状态和原始证据均保持不变。
- 再次运行 Windows native 必须取得新的、一次性、有界授权，不能复用旧许可或自动延长 1200 秒/15 秒边界。
- 本设计不授权 GitHub/Gitee 推送、PR、合并、master 同步、Release、签名或 legal 审批。

---

## 批准、基准与执行选择

[批准设计](../specs/2026-10-10-native-generation-seed-analysis-contract-design.md)已获用户“确认”。CODE_BASE 为 cb52f80eb276eecb14f3aa097818b49963802d53，其生产代码来自 f57b81eb。原 W4 依赖及正式门槛见[原实施计划](2026-10-08-windows-cmake-coverage-compatibility.md)，不重派 W1–W3 或其它已完成任务。

工作目录必须是 C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest，分支 codex/phase10-production-generation；用户默认 cwd C:/codex_project/unitTest 不是本项目录。执行前按 using-git-worktrees 复用现有隔离 worktree，不另建 checkout，不 stash/reset 用户工作。

本计划仅保存文档，不执行其中的命令、不初始化本项 SDD、不启动代理。用户选择执行方式后再开展一个完整、独立可验收的 Task 1；准备/证据步骤随该任务，不人为拆出仅搭脚手架或仅添加失败测试的任务。

### Task 1: Align both real native seeds with fixed analyzer preflight

**Files:**

- Create/Test: apps/test-service/internal/testgenanalysis/native_seed_contract_test.go
- Modify: tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp:1
- Modify: tools/service-probe/fixtures/test-generation/unity/src/classifier.c:1
- Read-only: apps/test-service/internal/testgenanalysis/analyzer.go and analyzer_test.go
- Read-only: two include/classifier headers, two tests/classifier_test entry points, all fixture CMake and framework recipes
- Controller/evidence only: .superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1, prepared.json, execution handoff
- Derived archive/export/receipts only: fresh .superpowers/runtime/native-seed-contract-<generated UUID> directory

**Interfaces:**

- Consumes: Analyzer.Analyze(context.Context, AnalysisRequest) (Program, error).
- Consumes: fixtureAnalysis(*testing.T) (Analyzer, AnalysisRequest, *fixtureRunner, *int); existing same-package fixtureRunner.calls/spec/err and fixtureBundle.
- Consumes: hasSourceDirective([]byte) bool, unchanged and unexported.
- Produces: unchanged C++ int classify(int value) noexcept and C int classify(int value) signatures.
- Produces: TestNativeGenerationSeedMatchesFixedAnalyzer and TestNativeGenerationSeedDirectiveReintroductionRejected; no new public API/type.

- [ ] **Step 1: Prepare one committed-source export with protection bindings**

执行选择后，使用 apply_patch 保存下面完整控制器到指定 ignored SDD 路径。首次 dot-source 只导出一次 CODE_BASE 并输出实际元数据 JSON；使用 apply_patch 将该输出原样保存到同目录 prepared.json，之后才能继续。后续调用只读取并复用同一个导出。不能因遗失上下文重新导出、清除旧记录或改旧 SDD；仅借用冻结清单叶文件。

~~~powershell
$ErrorActionPreference = 'Stop'
$Workspace = [IO.Path]::GetFullPath('C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest')
if ((Get-Item -LiteralPath '.').FullName -ne $Workspace) { throw 'Use the existing isolated worktree directory' }
$Plan = Join-Path $Workspace '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract'
$MetadataPath = Join-Path $Plan 'prepared.json'
$Git = 'D:/Program Files (x86)/Tools/Git/Git/cmd/git.exe'
$Go = 'D:/Program Files/Go/bin/go.exe'
$CodeBase = 'cb52f80eb276eecb14f3aa097818b49963802d53'
$Seeds = @(
 'tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp',
 'tools/service-probe/fixtures/test-generation/unity/src/classifier.c'
)
$NewTest = 'apps/test-service/internal/testgenanalysis/native_seed_contract_test.go'
$Owned = @($Seeds) + @($NewTest)
$OldHashes = @(
 'CD67C3D1DDF2A62E7D46DA309CE938EE6680D54E2CC3F31DC0DCFEB8C871147A',
 '9567B3383192447E9F685DDC4B3223619AFA3B26A1B376D0DA92D9A6FD5D8DBF'
)
function Assert-SeedDirect([string]$Path,[string]$Root) {
 $full = [IO.Path]::GetFullPath($Path)
 $base = [IO.Path]::GetFullPath($Root)
 if (-not $full.StartsWith($base + [IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) {
  throw 'Controller path escaped its root'
 }
 for ($current=$full; $current -ne $base; $current=Split-Path -Parent $current) {
  if (-not $current) { throw 'Controller ancestry missing' }
  if ((Test-Path -LiteralPath $current) -and
      ((Get-Item -LiteralPath $current).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Alias refused' }
 }
}
function Assert-SeedFrozen {
 $leaf = Join-Path $Workspace '.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/frozen-files.sha256'
 $rows = @(Get-Content -LiteralPath $leaf)
 if ($rows.Count -ne 18) { throw 'Frozen manifest changed' }
 foreach ($row in $rows) {
  if ($row -notmatch '^(.+) ([A-Fa-f0-9]{64})$') { throw 'Invalid frozen binding' }
  $path=$Matches[1]; $hash=$Matches[2]
  if ((Get-FileHash -LiteralPath (Join-Path $Workspace $path) -Algorithm SHA256).Hash -ne $hash) {
   throw 'Frozen source changed'
  }
 }
}
function Get-SeedBodyHash([string]$Path) {
 $bytes=[IO.File]::ReadAllBytes($Path)
 if (@($bytes | Where-Object { $_ -gt 127 }).Count) { throw 'Seed no longer ASCII' }
 $start=[Text.Encoding]::ASCII.GetString($bytes).IndexOf('int classify(int value)',[StringComparison]::Ordinal)
 if ($start -lt 0) { throw 'Seed function missing' }
 $hash=[Security.Cryptography.SHA256]::Create()
 try { return [BitConverter]::ToString($hash.ComputeHash([byte[]]$bytes[$start..($bytes.Length-1)])).Replace('-','') }
 finally { $hash.Dispose() }
}
Assert-SeedFrozen
if (-not (Test-Path -LiteralPath $MetadataPath -PathType Leaf)) {
 if (@(& $Git diff --cached --name-only).Count) { throw 'Pre-existing index' }
 $parent=(& $Git rev-parse HEAD).Trim()
 & $Git merge-base --is-ancestor $CodeBase $parent
 if ($LASTEXITCODE -ne 0) { throw 'Execution baseline diverged' }
 $allowedDocs=@(
  'docs/superpowers/specs/2026-10-10-native-generation-seed-analysis-contract-design.md',
  'docs/superpowers/plans/2026-10-10-native-generation-seed-analysis-contract.md'
 )
 if (@(& $Git diff --name-only $CodeBase $parent | Where-Object { $_ -notin $allowedDocs }).Count) {
  throw 'Unplanned committed source changes'
 }
 if (Test-Path -LiteralPath (Join-Path $Workspace $NewTest)) { throw 'Regression already exists' }
 $status=@(& $Git status --porcelain=v1)
 if ($status.Count -ne 19) { throw 'Workspace baseline changed' }
 for ($i=0; $i -lt $Seeds.Count; $i++) {
  if ((Get-FileHash -LiteralPath (Join-Path $Workspace $Seeds[$i]) -Algorithm SHA256).Hash -ne $OldHashes[$i]) {
   throw 'Old seed changed before RED'
  }
 }
 if (@(& $Git ls-tree -r $CodeBase | Where-Object { $_ -match '^(120000|160000) ' }).Count) {
  throw 'Unsupported archive links'
 }
 $scratch=Join-Path $Workspace ('.superpowers/runtime/native-seed-contract-'+[Guid]::NewGuid().ToString('N'))
 Assert-SeedDirect $scratch $Workspace
 if (Test-Path -LiteralPath $scratch) { throw 'Runtime collision' }
 $null=New-Item -ItemType Directory -Path $scratch
 $archive=Join-Path $scratch 'committed.tar'
 & $Git archive --format=tar --output=$archive $CodeBase
 if ($LASTEXITCODE -ne 0) { throw 'Archive failed' }
 $export=Join-Path $scratch 'export'
 $null=New-Item -ItemType Directory -Path $export
 & 'C:/Windows/system32/tar.exe' -xf $archive -C $export
 if ($LASTEXITCODE -ne 0) { throw 'Extraction failed' }
 $module=Join-Path $export 'apps/test-service'
 if (Test-Path -LiteralPath (Join-Path $module 'internal/taskstore/migrations/022_case_definitions.sql')) {
  throw 'Paused migration exported'
 }
 for ($i=0; $i -lt $Seeds.Count; $i++) {
  if ((Get-FileHash -LiteralPath (Join-Path $export $Seeds[$i]) -Algorithm SHA256).Hash -ne $OldHashes[$i]) {
   throw 'Archived old seed differs'
  }
 }
 $protected=@(
  'apps/test-service/internal/testgenanalysis/analyzer.go',
  'apps/test-service/internal/testgenanalysis/analyzer_test.go',
  'apps/test-service/internal/runtime/production_generation_pipeline.go',
  'apps/test-service/internal/runtime/test_generation_failure.go'
 )
 foreach ($framework in @('cpputest','unity')) {
  $fixture='tools/service-probe/fixtures/test-generation/'+$framework
  $protected+=@(& $Git ls-files -- ($fixture+'/include') ($fixture+'/tests') ($fixture+'/CMakeLists.txt') ($fixture+'/windows'))
 }
 $bindings=foreach ($path in @($protected | Sort-Object -Unique)) {
  [pscustomobject]@{path=$path;sha256=(Get-FileHash -LiteralPath (Join-Path $Workspace $path) -Algorithm SHA256).Hash}
 }
 $bodies=foreach ($seed in $Seeds) {
  [pscustomobject]@{path=$seed;sha256=(Get-SeedBodyHash (Join-Path $Workspace $seed))}
 }
 [pscustomobject]@{codeBase=$CodeBase;executionParent=$parent;scratch=$scratch;archive=$archive;
  archiveSHA256=(Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash;
  export=$export;module=$module;status=$status;protected=$bindings;bodies=$bodies} | ConvertTo-Json -Depth 6 -Compress
 return
}
$meta=Get-Content -LiteralPath $MetadataPath -Raw | ConvertFrom-Json
if ($meta.codeBase -cne $CodeBase) { throw 'Prepared baseline differs' }
$Scratch=[string]$meta.scratch
$Export=Join-Path $Scratch 'export'
$Module=Join-Path $Export 'apps/test-service'
$Archive=Join-Path $Scratch 'committed.tar'
$prefix=Join-Path $Workspace '.superpowers/runtime/native-seed-contract-'
if (-not $Scratch.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or
    $meta.export -ne $Export -or $meta.module -ne $Module -or $meta.archive -ne $Archive) {
 throw 'Prepared layout differs'
}
foreach ($path in @($Scratch,$Export,$Module,$Archive)) { Assert-SeedDirect $path $Workspace }
if (-not (Test-Path -LiteralPath $Module -PathType Container) -or
    (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash -ne $meta.archiveSHA256) {
 throw 'Prepared export unavailable; recover context, do not recreate'
}
function Assert-SeedGuards {
 Assert-SeedFrozen
 foreach ($binding in $meta.protected) {
  if ((Get-FileHash -LiteralPath (Join-Path $Workspace $binding.path) -Algorithm SHA256).Hash -ne $binding.sha256) {
   throw 'Protected source changed'
  }
 }
 foreach ($body in $meta.bodies) {
  if ((Get-SeedBodyHash (Join-Path $Workspace $body.path)) -ne $body.sha256) { throw 'Function bytes changed' }
 }
 if (@(& $Git diff --cached --name-only).Count) { throw 'Unexpected staged work' }
 $remaining=@(& $Git status --porcelain=v1 | Where-Object { $_.Substring(3) -notin $Owned })
 if ($remaining.Count -ne @($meta.status).Count -or @(Compare-Object @($meta.status) $remaining -CaseSensitive).Count) {
  throw 'Unrelated status changed'
 }
}
function Sync-SeedOverlay([string[]]$Paths) {
 Assert-SeedGuards
 foreach ($path in $Paths) {
  if ($path -notin $Owned) { throw 'Unowned overlay' }
  $source=Join-Path $Workspace $path; $destination=Join-Path $Export $path
  Assert-SeedDirect $source $Workspace
  Assert-SeedDirect $destination $Export
  if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'Overlay source missing' }
  Copy-Item -LiteralPath $source -Destination $destination -Force
  if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne
      (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash) { throw 'Overlay mismatch' }
 }
}
function Invoke-SeedTests([ValidateSet('RED','GREEN')][string]$Phase) {
 Assert-SeedGuards
 if ((& $Git rev-parse HEAD).Trim() -ne $meta.executionParent) { throw 'Execution parent changed' }
 $pattern=if ($Phase -eq 'RED') { '^TestNativeGenerationSeedMatchesFixedAnalyzer$' } else {
  '^(TestNativeGenerationSeedMatchesFixedAnalyzer|TestNativeGenerationSeedDirectiveReintroductionRejected|TestAnalyzeRejectsIncludedHeaderAndForeignASTExcerpt|TestAnalyzeSuppressesImplicitHostHeaders)$'
 }
 $script:SeedReceipt=Join-Path $Scratch ($Phase.ToLowerInvariant()+'-'+[Guid]::NewGuid().ToString('N')+'.jsonl')
 $names=@('GOPROXY','GOSUMDB','GOWORK','GOFLAGS','GOTOOLCHAIN'); $old=@{}
 foreach ($name in $names) { $old[$name]=[Environment]::GetEnvironmentVariable($name,'Process') }
 $PSNativeCommandUseErrorActionPreference=$false
 Push-Location -LiteralPath $Module
 try {
  [Environment]::SetEnvironmentVariable('GOPROXY','off','Process')
  [Environment]::SetEnvironmentVariable('GOSUMDB','off','Process')
  [Environment]::SetEnvironmentVariable('GOWORK','off','Process')
  [Environment]::SetEnvironmentVariable('GOFLAGS','-mod=readonly -buildvcs=false','Process')
  [Environment]::SetEnvironmentVariable('GOTOOLCHAIN','local','Process')
  & $Go test -json -count=1 -timeout=60s -run $pattern ./internal/testgenanalysis | Tee-Object -FilePath $script:SeedReceipt
  $script:SeedExit=$LASTEXITCODE
 } finally {
  foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name,$old[$name],'Process') }
  Pop-Location
 }
 [pscustomobject]@{phase=$Phase;package='./internal/testgenanalysis';pattern=$pattern;exitCode=$script:SeedExit;
  receipt=$script:SeedReceipt;environmentRestored=$true;
  sha256=(Get-FileHash -LiteralPath $script:SeedReceipt -Algorithm SHA256).Hash} | ConvertTo-Json -Compress
}
function Confirm-SeedReceipt([ValidateSet('RED','GREEN')][string]$Phase) {
 $rows=@(Get-Content -LiteralPath $script:SeedReceipt | ForEach-Object { $_ | ConvertFrom-Json })
 $failures=@($rows | Where-Object { $_.Action -eq 'fail' -and $_.Test })
 $skips=@($rows | Where-Object { $_.Action -eq 'skip' })
 if ($Phase -eq 'RED') {
  $expected=@('TestNativeGenerationSeedMatchesFixedAnalyzer',
   'TestNativeGenerationSeedMatchesFixedAnalyzer/cpputest','TestNativeGenerationSeedMatchesFixedAnalyzer/unity')
  $actual=@($failures | ForEach-Object { $_.Test } | Sort-Object)
  if ($script:SeedExit -ne 1 -or $actual.Count -ne 3 -or
      @(Compare-Object ($expected | Sort-Object) $actual).Count -or $skips.Count) {
   throw 'RED is not the genuine two-seed behavior failure'
  }
  $boundary=@($rows | Where-Object { $_.Action -eq 'output' -and $_.Output -match 'native seed blocked before runner: calls=0; want 1' })
  if ($boundary.Count -ne 2) { throw 'Runner-boundary RED proof missing' }
 } else {
  $top=@('TestNativeGenerationSeedMatchesFixedAnalyzer','TestNativeGenerationSeedDirectiveReintroductionRejected',
   'TestAnalyzeRejectsIncludedHeaderAndForeignASTExcerpt','TestAnalyzeSuppressesImplicitHostHeaders')
  $expected=@($top) + @(
   'TestNativeGenerationSeedMatchesFixedAnalyzer/cpputest',
   'TestNativeGenerationSeedMatchesFixedAnalyzer/unity',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/cpputest/hash',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/cpputest/digraph',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/cpputest/trigraph',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/unity/hash',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/unity/digraph',
   'TestNativeGenerationSeedDirectiveReintroductionRejected/unity/trigraph'
  )
  $passed=@($rows | Where-Object { $_.Action -eq 'pass' -and $_.Test } | ForEach-Object { $_.Test } | Sort-Object)
  $packages=@($rows | Where-Object { $_.Action -eq 'pass' -and -not $_.Test })
  if ($script:SeedExit -ne 0 -or $failures.Count -or $skips.Count -or $packages.Count -ne 1 -or
      $passed.Count -ne 12 -or @(Compare-Object ($expected | Sort-Object) $passed).Count) {
   throw 'Selected GREEN incomplete'
  }
 }
 [pscustomobject]@{phase=$Phase;verified=$true;receipt=$script:SeedReceipt}
}
Assert-SeedGuards
~~~

首次及后续 dot-source 的命令相同：

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
~~~

应确认 CODE_BASE、两个旧种子哈希、19 条原始状态及 18/18 冻结保护；暂停迁移 022 不在导出模块中。prepared.json 必须保存第一次真实输出，不手工猜 UUID/归档哈希。执行途中恢复上下文时读取该记录，不重建现场。

- [ ] **Step 2: Add the complete real-seed regression and format only that file**

使用 apply_patch 新增以下完整测试文件；此时不修改两份种子。expectedBody 仅断言真实读取的函数契约，不作为分析器输入替身。已有观察 runner 计数后立即返回测试错误，不启动 Clang、不提供成功 AST。

~~~go
package testgenanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type nativeSeedSpec struct {
	framework, fileName, standard, signature, headerName, driverName string
}

func nativeSeedSpecs() []nativeSeedSpec {
	return []nativeSeedSpec{
		{"cpputest", "classifier.cpp", "-std=c++17", "int classify(int value) noexcept", "classifier.hpp", "classifier_test.cpp"},
		{"unity", "classifier.c", "-std=c11", "int classify(int value)", "classifier.h", "classifier_test.c"},
	}
}

func nativeSeedRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(file) {
		t.Fatal("native seed repository location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	if _, err := os.Stat(filepath.Join(root, "apps", "test-service", "go.mod")); err != nil {
		t.Fatal("native seed repository root invalid")
	}
	return root
}

func nativeSeedBytes(t *testing.T, spec nativeSeedSpec) []byte {
	t.Helper()
	fixture := filepath.Join(nativeSeedRoot(t), "tools", "service-probe", "fixtures", "test-generation", spec.framework)
	source, err := os.ReadFile(filepath.Join(fixture, "src", spec.fileName))
	if err != nil {
		t.Fatal("native seed source unavailable")
	}
	text := strings.ReplaceAll(string(source), "\r\n", "\n")
	start := strings.Index(text, spec.signature)
	expectedBody := spec.signature + " {\n  if (value < 0) return -1;\n  if (value == 0) return 0;\n  return 1;\n}\n"
	if start < 0 || text[start:] != expectedBody {
		t.Fatal("native seed function contract changed")
	}
	header, err := os.ReadFile(filepath.Join(fixture, "include", spec.headerName))
	if err != nil || !strings.Contains(string(header), spec.signature+";") {
		t.Fatal("native seed public header contract changed")
	}
	driver, err := os.ReadFile(filepath.Join(fixture, "tests", spec.driverName))
	if err != nil || !strings.Contains(string(driver), "#include \""+spec.headerName+"\"") {
		t.Fatal("native seed test entry no longer uses its public header")
	}
	return source
}

func nativeSeedAnalysis(t *testing.T, spec nativeSeedSpec, source []byte) (Analyzer, AnalysisRequest, *fixtureRunner) {
	t.Helper()
	analyzer, request, runner, _ := fixtureAnalysis(t)
	request.SourceRelative = spec.fileName
	request.Arguments = []string{spec.standard}
	sum := sha256.Sum256(source)
	request.SourceDigest = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(request.WorkspaceRoot, request.SourceRelative), source, 0600); err != nil {
		t.Fatal("native seed temporary source unavailable")
	}
	runner.err = errors.New("test stops immediately after analyzer preflight")
	return analyzer, request, runner
}

func TestNativeGenerationSeedMatchesFixedAnalyzer(t *testing.T) {
	for _, spec := range nativeSeedSpecs() {
		t.Run(spec.framework, func(t *testing.T) {
			source := nativeSeedBytes(t, spec)
			analyzer, request, runner := nativeSeedAnalysis(t, spec, source)
			_, err := analyzer.Analyze(context.Background(), request)
			if runner.calls != 1 {
				t.Fatalf("native seed blocked before runner: calls=%d; want 1", runner.calls)
			}
			if err == nil {
				t.Fatal("preflight observer must not fabricate successful analysis")
			}
			if hasSourceDirective(source) {
				t.Fatal("native seed contains a forbidden source directive")
			}
			if !strings.HasPrefix(string(source), spec.signature+" {") {
				t.Fatal("native seed has an unexpected prefix")
			}
			if runner.spec.Executable != analyzer.bundle.ClangPath() ||
				runner.spec.Dir != request.WorkspaceRoot || len(runner.spec.Env) != 0 ||
				runner.spec.Timeout != request.Timeout || runner.spec.MaxOutput != maxASTBytes {
				t.Fatal("native seed observer received an unexpected process boundary")
			}
		})
	}
}

func TestNativeGenerationSeedDirectiveReintroductionRejected(t *testing.T) {
	for _, spec := range nativeSeedSpecs() {
		source := nativeSeedBytes(t, spec)
		markers := []struct{ name, prefix string }{
			{"hash", "#include \"" + spec.headerName + "\"\n"},
			{"digraph", "%:include \"" + spec.headerName + "\"\n"},
			{"trigraph", "??=include \"" + spec.headerName + "\"\n"},
		}
		for _, marker := range markers {
			t.Run(spec.framework+"/"+marker.name, func(t *testing.T) {
				input := append([]byte(marker.prefix), source...)
				analyzer, request, runner := nativeSeedAnalysis(t, spec, input)
				_, err := analyzer.Analyze(context.Background(), request)
				if err == nil || runner.calls != 0 {
					t.Fatal("reinjected source directive reached the runner")
				}
				if !hasSourceDirective(input) {
					t.Fatal("directive reintroduction test input is invalid")
				}
			})
		}
	}
}
~~~

~~~powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w 'apps/test-service/internal/testgenanalysis/native_seed_contract_test.go'
if ($LASTEXITCODE -ne 0) { throw 'Regression formatting failed' }
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
Sync-SeedOverlay @($NewTest)
~~~

只复制新增测试到原导出；不要先复制新种子。所有测试在 Export/apps/test-service 运行，不在脏工作区模块运行。

- [ ] **Step 3: Execute genuine RED once and inspect the complete receipt**

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
for ($i=0; $i -lt $Seeds.Count; $i++) {
 if ((Get-FileHash -LiteralPath (Join-Path $Export $Seeds[$i]) -Algorithm SHA256).Hash -ne $OldHashes[$i]) {
  throw 'RED no longer uses unchanged old seeds'
 }
}
Invoke-SeedTests 'RED'
Confirm-SeedReceipt 'RED'
~~~

预期已编译并执行，退出 1；cpputest/unity 两子测试在真实 Analyze 调用之后报告 calls=0; want 1，根测试失败。不能接受编译失败、样例缺失、仓库根错误或其它断言充当 RED。完整读取 JSONL，保留实际命令、身份、路径和摘要；不为再次确认重复运行。

- [ ] **Step 4: Remove only the two leading includes, preserve function bytes**

用 apply_patch 删除两份实现文件第一条 include 及紧邻空行。CppUTest 内容为：

~~~cpp
int classify(int value) noexcept {
  if (value < 0) return -1;
  if (value == 0) return 0;
  return 1;
}
~~~

Unity 内容为：

~~~c
int classify(int value) {
  if (value < 0) return -1;
  if (value == 0) return 0;
  return 1;
}
~~~

不要整体格式化种子；保留原始函数行尾。若编辑工具归一化剩余函数字节，按原字节恢复后再继续；不能改保护规则或忽略摘要差异。

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
Assert-SeedGuards
Sync-SeedOverlay $Owned
~~~

函数尾部原始摘要必须仍匹配准备记录；只覆盖同一导出目录的三处允许路径。源文件变化产生新来源摘要/工作区代次，不能重标旧报告、函数位置、任务 ID 或 TG1 证据。共享种子会影响 Linux 的样例字节，但不改变 Linux 生产逻辑或自动运行其 native。

- [ ] **Step 5: Run the exact GREEN selection and verify all required identities**

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
Invoke-SeedTests 'GREEN'
Confirm-SeedReceipt 'GREEN'
Assert-SeedGuards
~~~

预期退出 0，无 fail/skip；两个真实样例进入观察 runner 各一次，六个重新加入指令的子例均在 runner 前被拒绝，已有 include/外来 AST 与 implicit host header 两门槛通过。完整审查回执中的子测试身份，不仅查看退出码。

若失败，只分析该回执的实际原因；不跑全量、改生产分析器、扩大白名单或启动 native。审查复用已完整验证的回执，不自动重复测试。GREEN 只证明前置契约，不证明 Clang 分析、生成/执行候选或覆盖率成功。

- [ ] **Step 6: Review exact diff and preserve the local evidence handoff**

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
Assert-SeedGuards
& $Git diff --check -- $Seeds
if ($LASTEXITCODE -ne 0) { throw 'Seed whitespace diff failed' }
& $Git diff -- $Seeds
Get-Content -LiteralPath (Join-Path $Workspace $NewTest) -Raw
foreach ($path in $Owned) {
 [pscustomobject]@{path=$path;sha256=(Get-FileHash -LiteralPath (Join-Path $Workspace $path) -Algorithm SHA256).Hash}
}
~~~

阅读两个仅删除 include 的差异及完整新测试。用 apply_patch 保存本项 ignored execution handoff：实际 CODE_BASE/执行父提交，同一个导出/归档路径与哈希，旧/新种子与未变函数体摘要，RED 和最新 GREEN 回执路径/哈希/实际测试身份，环境恢复、冻结 18 和原始 19 状态保护结果。记录“局部契约通过，W4/Phase10 未完成”，不能写成全部 unknown 已解决。保留旧失败证据，不读/改旧数据库。

- [ ] **Step 7: Commit exactly the three implementation files locally**

~~~powershell
. '.superpowers/sdd/2026-10-10-native-generation-seed-analysis-contract/seed-controller.ps1'
Assert-SeedGuards
$parent=(& $Git rev-parse HEAD).Trim()
if ($parent -ne $meta.executionParent) { throw 'Commit parent changed' }
& $Git add -- $Owned
if ($LASTEXITCODE -ne 0) { throw 'Exact staging failed' }
$expected=@($Owned | Sort-Object)
$actual=@(& $Git diff --cached --name-only | Sort-Object)
if ($actual.Count -ne 3 -or @(Compare-Object $expected $actual -CaseSensitive).Count) {
 throw 'Staged scope mismatch'
}
& $Git diff --cached --check
if ($LASTEXITCODE -ne 0) { throw 'Staged whitespace failed' }
& $Git commit -m 'fix: align native generation seeds with fixed analyzer contract'
if ($LASTEXITCODE -ne 0) { throw 'Local implementation commit failed' }
$committed=@(& $Git diff-tree --no-commit-id --name-only -r HEAD | Sort-Object)
if ((& $Git rev-parse 'HEAD^').Trim() -ne $parent -or $committed.Count -ne 3 -or
    @(Compare-Object $expected $committed -CaseSensitive).Count) {
 throw 'Actual commit scope mismatch'
}
Assert-SeedGuards
& $Git log -1 --format='%H %s'
~~~

所选执行技能的实际审查门槛通过后才宣称本项完成。父提交、保护或 staged 集合不符则停止，不 reset/stash/check out 用户工作。不自动推送或发布。

- [ ] **Step 8: Return control to W4 and stop before runtime/remote gates**

仅更新原 W4 ignored ledger，记录本项局部结果、实际三文件提交及依赖，不重派已完成任务。先保留历史 TG1 unknown 的内层原因不确定性；此项确定修正样例与首版支持子集的冲突，不能声称一般带 include 用户项目已支持生成。

下一独立门槛是重新授权的一次有界 Windows native，验证双框架真实生成、应用、执行、文件/函数覆盖率及维护复跑。本任务不能执行它。原行 5/5、分支基线 2/4→4/4 仍是未来真实验收目标，不是定点回归结果；不得依赖旧行号或报告硬编码通过。

Linux native、hosted、打包、GitHub/Gitee、PR/merge、Release/签名仍未授权，Phase 8 延后项与 V2 状态不变。

## 完成判定与计划自查

设计三文件范围、真实样例输入、生产 Analyze 前置路径、RED/GREEN、三种指令反例、两个已有安全门槛、原函数/保护文件/暂停 V2 保护和精确本地提交均映射到步骤 1–7。步骤 8 保留 W4 的正式验收与审批，不自动启动新运行。

所有测试和控制器接口已完整定义；元数据由首次真实输出生成，不凭空填写。没有新生产接口、清洗后的分析输入、夹具豁免或包外测试 API。本计划保存期间只校对文档/语法，不执行以上控制器、编译、测试或实施命令。

执行选择：1 Subagent-Driven（推荐），使用 subagent-driven-development 做本项及两阶段审查；2 Inline Execution，使用 executing-plans 在当前会话实施并设检查点。未选择前不初始化现场或执行。
