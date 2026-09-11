# Phase 7 Batch A：Linux GCC Coverage Execution 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Linux x64 production Service 中用真实 GCC/G++/gcov 与 product-owned Python 3.14.6 + gcovr 8.6 完成 CppUTest、Unity CoverageRun，同时保持 Windows LLVM、Protocol v1.4 和 Coverage JSON v1 的行为不变。

**Architecture:** 保留一个 `coverageexec.Coordinator`，在其下引入工具无关的 coverage capability 与 Adapter Registry。Build Boundary、GCC Toolset、coverage execution owner 分别保有 Workspace、object directory、gcov、collector root 的独立 retained verifier；gcovr 只能在 `<executionRoot>/collector/gcovr` 中通过固定 bundled runner 执行。LLVM 继续走 merge + process normalize，GCC 走 gcovr aggregate + bounded pinned-output service normalize，二者最终进入同一 canonical report/publish 流程。

**Tech Stack:** Go 1.26.6、CMake、GCC/G++/gcov、Python 3.14.6、gcovr 8.6、CppUTest、Unity、TypeScript 6、Node.js 24.18.0、pnpm 11.4.0、GitHub Actions `ubuntu-24.04`、Linux network namespace、Protocol v1.4、Coverage JSON v1。

## Global Constraints

- 本计划以 `0da6001` 设计勘误为约束基线；先写失败测试，再做最小实现，再重构。
- 一个 CoverageRun 继续只拥有一个顶层 Task、一个关联 TestRun 和一个 CoverageReport；不得新增 Linux 专用 Coordinator 或 nested Task。
- Workspace、coverage object directory、gcov、collector root 必须由四个真实 owner 独立验证；不得使用磁盘根共同 anchor，不得复制输入来伪造共同 authority。
- descriptor/output 固定在 `<executionRoot>/collector/gcovr`；不得复用 `<coverageRoot>/<taskID>`，不得从 Protocol、Workspace config 或测试 metadata 接受 runner path/argv/env/cwd/output path。
- GCC coverage build/test 固定串行：build `Jobs=1`，embedded tests `MaxConcurrency=1`。
- `.gcno/.gcda` 只在 Service-owned isolated coverage build tree 内按 closed manifest 操作；不得递归删除未知文件、跟随 symlink、接受 hard link 或 special file。
- product runtime 只使用已锁定 coverage bundle；不得调用系统 Python、pip、site-packages、用户 gcovr config 或网络。
- gcovr JSON 只能从 retained output handle 读取；parser 不得按 pathname 重新打开文件。
- Windows LLVM 的 step 顺序、failure mapping、artifact bytes 和 required smoke 不得漂移。
- Linux Clang 本批保持精确 `unsupported`，不得产生占位或假 coverage report。
- 每个实现任务只创建本地提交。未经用户新的明确授权，不 push GitHub/Gitee、不创建或合并 PR、不发布 Release、不启用签名。
- 用户拥有的 `.merge-stash-20260903/` 不加入暂存、不修改、不删除。

---

## Task 1：建立工具无关 coverage contracts，并锁定 LLVM characterization

**Files:**

- Create: `apps/test-service/internal/coverageplatform/contracts.go`
- Create: `apps/test-service/internal/coverageplatform/contracts_test.go`
- Modify: `apps/test-service/internal/coveragellvm/toolset.go`
- Modify: `apps/test-service/internal/coveragellvm/toolset_windows_test.go`
- Modify: `apps/test-service/internal/coveragellvm/instrumentation.go`
- Modify: `apps/test-service/internal/coveragellvm/instrumentation_test.go`
- Modify: `apps/test-service/internal/coveragellvm/profile.go`
- Modify: `apps/test-service/internal/coveragellvm/profile_test.go`
- Modify: `apps/test-service/internal/testrun/embedded.go`
- Modify: `apps/test-service/internal/testrun/embedded_test.go`

**Interfaces:**

```go
package coverageplatform

type DirectoryVerifier interface {
    Path() string
    Verify() error
}

type Output interface {
    ReadAll() ([]byte, error)
}

type OwnershipClaim interface {
    Commit()
    Rollback()
}

type Toolset interface {
    Version() string
    Identity() string
    CCompiler() coveragerun.TrustedPath
    CXXCompiler() coveragerun.TrustedPath
    Tools() []coveragerun.TrustedPath
    Verify() error
    ClaimOwnership() (OwnershipClaim, error)
    Close() error
}

type CollectorExecution interface {
    ProcessSpec() task.ProcessSpec
    Verify() error
    VerifyAfter() error
    ValidateProcessTarget(string, []string, []string, []string, string) error
    PinnedOutput() (Output, error)
    Close() error
}

type Instrumentation struct {
    IncludePath string
    SHA256 string
    Fingerprint string
}
```

`testrun` 将 allocator contract 改为：

```go
type ProfileExpectation struct {
    InvocationID string
    Iteration int64
    Sequence int
    FileName string
}

type ProfileAllocator interface {
    Decorate(ProfileExpectation, task.ProcessSpec) (ProfileExpectation, task.ProcessSpec, error)
    Validate(ProfileExpectation, task.ProcessSpec, task.ProcessSpec) error
}
```

`EmbeddedRun` 只分配稳定的 `Sequence=index+1` 和 invocation identity。LLVM allocator 生成 `p-%06d-i-%06d-%%p-%%m.profraw`、返回补全后的 expectation，并验证恰好一个 `LLVM_PROFILE_FILE`；generic embedded runner 只验证 executable/argv/dir/batch 未改变，再调用 allocator 的 environment policy。

- [ ] **Step 1: 写失败的 contract 与 embedded allocator tests**

覆盖 typed-nil toolset/verifier、返回 slice 不可修改 owner、重复 Sequence、allocator 篡改 executable/argv/dir、LLVM 缺失或重复 `LLVM_PROFILE_FILE`、hostile casing env 和现有 wave ordering。

- [ ] **Step 2: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/testrun ./apps/test-service/internal/coveragellvm -run 'Contract|Embedded|ProfileAllocator|Toolset|Instrumentation' -count=1
```

Expected: `coverageplatform` 不存在，且 `ProfileAllocator` 新签名导致编译失败。

- [ ] **Step 3: 实现最小 generic contracts 与 LLVM adapter compatibility**

`coveragellvm.Toolset` 实现 `coverageplatform.Toolset`：`CCompiler`、`CXXCompiler` 都返回 retained clang-cl；`Tools` 返回 compiler/profdata/cov 的新 slice；`ClaimOwnership` 返回 interface。将 `Instrumentation` 移入 `coverageplatform`，LLVM writer 返回该类型。更新所有 test fakes，禁止用 `any` 或空 interface 绕过类型边界。

- [ ] **Step 4: 运行 GREEN 与 Windows compile characterization**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/testrun ./apps/test-service/internal/coveragellvm -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/runtime -run 'LLVM|Windows|Embedded' -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coveragellvm -run 'Toolset|Profile|Instrumentation' -race -count=1
```

Expected: 全部 PASS；Windows LLVM expectation filename、env unset 和 tool ordering 与变更前一致。

- [ ] **Step 5: 提交 Task 1**

```powershell
git add -- apps/test-service/internal/coverageplatform apps/test-service/internal/coveragellvm apps/test-service/internal/testrun/embedded.go apps/test-service/internal/testrun/embedded_test.go
git diff --cached --check
git commit -m "refactor: generalize coverage platform contracts"
```

---

## Task 2：泛化 Build Boundary，并实现四个独立 verifier

**Files:**

- Modify: `apps/test-service/internal/build/verified_directory.go`
- Modify: `apps/test-service/internal/build/verified_directory_windows.go`
- Modify: `apps/test-service/internal/build/verified_directory_nonwindows.go`
- Modify: `apps/test-service/internal/build/boundary.go`
- Modify: `apps/test-service/internal/build/coordinator.go`
- Modify: `apps/test-service/internal/build/coverage_isolation_windows_test.go`
- Create: `apps/test-service/internal/build/coverage_capabilities_test.go`
- Modify: `apps/test-service/internal/coveragebundle/descriptor.go`
- Modify: `apps/test-service/internal/coveragebundle/descriptor_test.go`
- Modify: `apps/test-service/internal/coveragebundle/runner.go`
- Modify: `apps/test-service/internal/coveragebundle/runner_test.go`
- Modify: `apps/test-service/internal/coveragebundle/anchor_test.go`
- Modify: `apps/test-service/internal/coverageexec/boundary.go`
- Modify: `apps/test-service/internal/coverageexec/boundary_test.go`
- Modify: `apps/test-service/internal/coverageexec/model.go`

**Interfaces:**

```go
type PreparedBuild interface {
    testrun.PreparedBuild
    CoverageBinaryDir() string
    CoverageSourceRoot() coverageplatform.DirectoryVerifier
    CoverageObjectDirectory() coverageplatform.DirectoryVerifier
    AttachCoverageToolset(coverageplatform.Toolset) error
    AttachCoverageExecution(coverageplatform.CollectorExecution) error
    VerifyCoverageExecutionAfter() error
    PinnedCoverageOutput() (coverageplatform.Output, error)
}
```

```go
type DescriptorCapabilities struct {
    CollectorRoot coverageplatform.DirectoryVerifier
    Root coverageplatform.DirectoryVerifier
    ObjectDirectory coverageplatform.DirectoryVerifier
    GcovExecutable coveragerun.TrustedPath
}
```

`build.executionBoundary` 持有 `coverageplatform.Toolset` 和 `coverageplatform.CollectorExecution`。`CoverageSourceRoot`、`CoverageObjectDirectory` 返回只含 `Path/Verify` 的 non-owning view；view 的 `Close` 不存在。Boundary release 按 collector → toolset → directories 顺序关闭 owner。

- [ ] **Step 1: 写失败的 independent-capability tests**

建立四个互不相干的 temp roots，证明合法 verifier 能构建 descriptor；再分别测试 typed nil、path mismatch、Workspace/object/gcov/root owner 提前关闭、path replacement、共同祖先伪造、bare absolute path 和 `<executionRoot>/collector/gcovr` collision。测试必须断言失败不会关闭调用方 owner，且不会留下 descriptor/output 临时文件。

- [ ] **Step 2: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/coverageexec -run 'Independent|Capability|CollectorRoot|BoundaryOwnership' -count=1
```

Expected: 当前共同 `serviceanchor.Anchor` API 无法接受独立 verifier，测试失败。

- [ ] **Step 3: 实现 non-owning view 与 descriptor ownership 修正**

在 Build Boundary 初始化时 pin Workspace root，而不是只保存一次 `os.FileInfo`。将现有 coverage build directory pin 暴露为 non-owning view。`coveragebundle` 内部仍自行 pin collector child、descriptor 和 output，但只反复调用四个外部 verifier 的 exact `Path()`/`Verify()`；删除 `SameIssuer`/共同 parent 检查以及 `closeDescriptorCapabilities` 对外部 owner 的关闭。

将 runner API 改为固定 child ID：

```go
func PrepareRunner(
    pin Pin,
    input DescriptorInput,
    capabilities DescriptorCapabilities,
) (*PreparedExecution, error)
```

实现只接受 `capabilities.CollectorRoot.Path()/gcovr`，`DescriptorInput.OutputPath` 必须等于该 child 内固定 `coverage.json`。`PreparedExecution.PinnedOutput` 返回 `coverageplatform.Output`；`Close` 只关闭 bundle pin、自有 child/descriptor/output handles。

- [ ] **Step 4: 泛化 execution/build boundary target validation**

`coverageexec.executionBoundary.ValidateExecutable` 遍历 `adapter.Toolset().Tools()`。Build Coordinator 用 closed family switch 验证 clang-cl 或 GCC coverage capability；普通 GCC toolchain 可以继续 build，但没有完整 coverage capability 时不能创建 coverage build。Build Boundary attachment 验证 toolset identity/version、C/C++ compiler path 与当前 `toolchain.Instance` 精确匹配；collector process 只通过 attached execution 的完整 target validation。toolset claim commit 后所有权转给 Build Boundary，Adapter 只保留 non-owning reference，`Adapter.Close` 不得再次关闭 toolset。

- [ ] **Step 5: 运行 GREEN、race 和旧 anchor regression**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/coverageexec -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/coverageexec -run 'Capability|Boundary|Close|Replace' -count=1
```

Expected: 独立 roots PASS；owner 早关 fail closed；现有 Windows boundary tests 不变。

- [ ] **Step 6: 提交 Task 2**

```powershell
git add -- apps/test-service/internal/build apps/test-service/internal/coveragebundle apps/test-service/internal/coverageexec/boundary.go apps/test-service/internal/coverageexec/boundary_test.go apps/test-service/internal/coverageexec/model.go
git diff --cached --check
git commit -m "refactor: separate coverage capability ownership"
```

---

## Task 3：发现并固定 GCC/G++/gcov toolset identity

**Files:**

- Modify: `apps/test-service/internal/toolchain/model.go`
- Modify: `apps/test-service/internal/toolchain/gnu.go`
- Modify: `apps/test-service/internal/toolchain/gnu_test.go`
- Modify: `apps/test-service/internal/toolchain/registry.go`
- Modify: `apps/test-service/internal/toolchain/registry_test.go`
- Create: `apps/test-service/internal/coveragegcc/toolset.go`
- Create: `apps/test-service/internal/coveragegcc/toolset_unix.go`
- Create: `apps/test-service/internal/coveragegcc/toolset_windows.go`
- Create: `apps/test-service/internal/coveragegcc/toolset_test.go`
- Create: `apps/test-service/internal/coveragegcc/toolset_unix_test.go`
- Modify: `apps/test-service/internal/runtime/coverage_backend.go`
- Modify: `apps/test-service/internal/runtime/coverage_backend_test.go`

**Model additions:**

```go
type CoverageCapability struct {
    LLVMProfdata string
    LLVMCov string
    GCov string
    CompilerEvidence ExecutableEvidence
    CXXCompilerEvidence ExecutableEvidence
    ProfdataEvidence ExecutableEvidence
    CovEvidence ExecutableEvidence
    GCovEvidence ExecutableEvidence
    GCovVersion string
    ToolsetIdentity string
}

func GCCToolsetIdentity(version string, paths []string, evidence []ExecutableEvidence) string
```

Unix `ExecutableEvidence.FileIdentity` 获得明确 `unix:<device>:<inode>` closed validation；不得复用只接受 `windows:` 的 verifier。GCC adapter 固定调用 `gcc -print-prog-name=gcov`，只接受 canonical absolute result，或 compiler directory 下 basename `gcov` 的 deterministic resolution；随后固定调用 `gcov --version`，版本必须与 GCC/G++ 相同。

- [ ] **Step 1: 写 GCC discovery RED tests**

覆盖 exact probe 顺序、empty/multiline/NUL/invalid UTF-8、relative path escape、symlink、非 regular、oversize、gcc/g++/gcov version mismatch、三个 executable 任一在 probe 中被替换、C/C++ evidence 分离、stable identity 和 clone ownership。另加“gcov 不可用时普通 GCC toolchain 仍可发现，但 `Coverage` 为空”的测试。

- [ ] **Step 2: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/runtime -run 'GCC|GCov|ToolsetIdentity|CoverageSnapshot' -count=1
```

Expected: 缺少 GCC coverage evidence/toolset，测试失败。

- [ ] **Step 3: 实现 discovery、registry recomputation 与 retained toolset**

`coveragegcc.PinToolset(instance)` 只支持 Linux x64 GCC，重新打开并验证 gcc/g++/gcov 的 canonical path、native identity、SHA-256、version 与 `GCCToolsetIdentity`。`Tools()` 固定返回 gcc、g++、gcov。Windows stub 返回 `ErrUnsupportedPlatform` 且无 filesystem side effect。

Registry clone/validation 必须 family-specific recompute LLVM/GCC identity。`coverageToolchainSnapshot` 对 GCC 使用：compiler version、真实 gcov version、collector `gcovr/8.6` 和 GCC instrumentation fingerprint；不再把 compiler version 填到 collector version。

- [ ] **Step 4: 运行 GREEN、race、cross-platform compile**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/runtime -run 'GCC|GCov|Registry|CoverageSnapshot' -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragegcc -count=1
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go test ./apps/test-service/internal/coveragegcc -run '^$'
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED -ErrorAction SilentlyContinue
```

- [ ] **Step 5: 提交 Task 3**

```powershell
git add -- apps/test-service/internal/toolchain apps/test-service/internal/coveragegcc apps/test-service/internal/runtime/coverage_backend.go apps/test-service/internal/runtime/coverage_backend_test.go
git diff --cached --check
git commit -m "feat: retain GCC gcov coverage toolsets"
```

---

## Task 4：实现 GCC instrumentation 与 `.gcno/.gcda` closed lifecycle

**Files:**

- Create: `apps/test-service/internal/coverageplatform/instrumentation.go`
- Create: `apps/test-service/internal/coverageplatform/instrumentation_windows.go`
- Create: `apps/test-service/internal/coverageplatform/instrumentation_nonwindows.go`
- Create: `apps/test-service/internal/coverageplatform/instrumentation_test.go`
- Modify: `apps/test-service/internal/coveragellvm/instrumentation.go`
- Create: `apps/test-service/internal/coveragegcc/instrumentation.go`
- Create: `apps/test-service/internal/coveragegcc/instrumentation_test.go`
- Create: `apps/test-service/internal/coveragegcc/allocator.go`
- Create: `apps/test-service/internal/coveragegcc/allocator_test.go`
- Create: `apps/test-service/internal/coveragegcc/evidence.go`
- Create: `apps/test-service/internal/coveragegcc/evidence_unix.go`
- Create: `apps/test-service/internal/coveragegcc/evidence_windows.go`
- Create: `apps/test-service/internal/coveragegcc/evidence_test.go`
- Create: `apps/test-service/internal/coveragegcc/evidence_unix_test.go`

**Instrumentation contract:**

```cmake
cmake_minimum_required(VERSION 3.25)
if(NOT CMAKE_C_COMPILER_ID STREQUAL "GNU" OR NOT CMAKE_CXX_COMPILER_ID STREQUAL "GNU")
  message(FATAL_ERROR "unit-test-ide coverage requires GCC and G++")
endif()
add_compile_options("$<$<COMPILE_LANGUAGE:C,CXX>:--coverage>" "$<$<COMPILE_LANGUAGE:C,CXX>:-O0>" "$<$<COMPILE_LANGUAGE:C,CXX>:-g>")
add_link_options("--coverage")
```

GCC allocator 不生成 per-test filename，不添加 `GCOV_PREFIX`/`GCOV_PREFIX_STRIP`；它清除大小写敏感的 GCOV/GCOVR hostile variables，并由 `Validate` 证明其余 process target 不变。

Evidence state 使用 `openat`/`fstatat`/`unlinkat`（`golang.org/x/sys/unix`）相对 retained object root 操作：

```go
type Manifest struct {
    Notes []Entry
    Data []Entry
    PartialReasons []coveragedomain.CompletenessReason
}

type Entry struct {
    RelativePath string
    SHA256 string
    Size int64
}
```

`.gcda` completeness 是 object-scoped：由 sealed `.gcno` set 推导 expected data set，不能把一个 object 的缺失虚构成某个 invocation 的 per-test profile 缺失；只有 crash/timeout 的实际 invocation outcome 继续贡献对应 partial reason。

- [ ] **Step 1: 写 instrumentation/allocator RED tests**

断言 exact bytes/SHA-256/fingerprint、atomic exclusive publication、root replacement、symlink destination、mode 和 duplicate publication。allocator tests 断言没有 LLVM/GCOV 控制变量、没有 per-test file、Sequence 稳定且串行输入不变。

- [ ] **Step 2: 写 evidence lifecycle RED tests**

使用真实目录树覆盖：零 `.gcno`、unknown extension、nested depth/count/bytes overflow、symlink、FIFO/special、hard link、case-sensitive duplicate、stale `.gcda` pre-test cleanup、unexpected `.gcda` post-test、expected missing data、root replacement、cancel。Windows stub 只允许 compile，调用必为 unsupported。

- [ ] **Step 3: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/coveragegcc -run 'Instrumentation|Allocator|Evidence|Manifest|Cleanup' -count=1
```

- [ ] **Step 4: 提取 shared atomic publisher 并实现 GCC lifecycle**

将 LLVM 的安全原子 instrumentation publication 提取到 `coverageplatform`，保持 LLVM bytes/fingerprint 不变。GCC `PrepareTests` 在 build 成功、首个测试前 seal `.gcno` 并删除 closed expected set 中的 stale `.gcda`；`SealEvidence` 在测试后只接受由 `.gcno` 推导的 `.gcda` 集合，按 relative path 排序、固定预算、保留 handle 和 digest。cleanup 只能 unlink manifest 中明确列出的 relative child。

- [ ] **Step 5: 运行 GREEN 与 race**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/coveragellvm -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/coveragegcc -run 'Evidence|Close|Replace|Cancel' -count=1
```

- [ ] **Step 6: 提交 Task 4**

```powershell
git add -- apps/test-service/internal/coverageplatform apps/test-service/internal/coveragegcc apps/test-service/internal/coveragellvm/instrumentation.go apps/test-service/internal/coveragellvm/instrumentation_test.go
git diff --cached --check
git commit -m "feat: seal GCC coverage evidence"
```

---

## Task 5：将 product-owned gcovr runner 接到 generic Boundary

**Files:**

- Modify: `apps/test-service/internal/coveragebundle/resolver.go`
- Modify: `apps/test-service/internal/coveragebundle/resolver_test.go`
- Modify: `apps/test-service/internal/coveragebundle/manifest.go`
- Modify: `apps/test-service/internal/coveragebundle/manifest_test.go`
- Modify: `apps/test-service/internal/coveragebundle/runner.go`
- Modify: `apps/test-service/internal/coveragebundle/runner_test.go`
- Create: `apps/test-service/internal/coveragegcc/collector.go`
- Create: `apps/test-service/internal/coveragegcc/collector_test.go`
- Modify: `apps/test-service/internal/build/boundary.go`
- Modify: `apps/test-service/internal/build/coverage_capabilities_test.go`
- Modify: `apps/test-service/internal/coverageexec/execution_directory_windows.go`
- Modify: `apps/test-service/internal/coverageexec/execution_directory_nonwindows.go`
- Modify: `apps/test-service/internal/coverageexec/boundary.go`
- Modify: `apps/test-service/internal/coverageexec/boundary_test.go`

**Closed bundle/collector contract:**

```go
const RequiredPythonVersion = "3.14.6"
const RequiredGCovrVersion = "8.6"

func ResolveExact(bundleRoot string) (coveragebundle.Pin, error)

func PrepareCollector(
    bundle coveragebundle.Pin,
    collectorRoot coverageplatform.DirectoryVerifier,
    sourceRoot coverageplatform.DirectoryVerifier,
    objectRoot coverageplatform.DirectoryVerifier,
    gcov coveragerun.TrustedPath,
) (coverageplatform.CollectorExecution, error)
```

`ResolveExact` 接收已经选定的平台 bundle 根：production 为 verified Service executable 产品树中的 `<product>/bundles/coverage`，开发/CI 为 `.superpowers/runtime/coverage-bundle/linux-x64`。它不再要求调用方把该目录伪装成旧的 `<productRoot>/coverage-bundle/<platform>` 布局；manifest 内 platform 仍必须与当前 GOOS/GOARCH 一致。保留现有 `Resolve(productRoot)` 作为兼容 wrapper，并使两条路径进入同一个 closed-layout verifier。

Coordinator 的 `executionRootOwner` 以 handle-relative exclusive mkdir 创建并 retain `<executionRoot>/collector`，暴露 non-owning verifier。GCC collector 固定 runner process：

```text
<bundle-python> -I -S <gcovr-runner.pyz> <collector/gcovr/descriptor.json>
```

descriptor 固定 output 为 `collector/gcovr/coverage.json`。runner args/env/dir 任一变化都由 `ValidateProcessTarget` 拒绝。

- [ ] **Step 1: 写 runner/collector RED tests**

覆盖 bundle manifest 版本不等于 Python 3.14.6 或 gcovr 8.6、READY/closed tree/digest mutation、hostile PYTHON/PIP/CONDA/proxy/loader/GCOV/GCOVR env、固定 argv、child collision、四 owner 中任一早关、collector exit 后 output symlink/replace/oversize、pinned read ABA，以及失败时不关闭外部 verifier。

- [ ] **Step 2: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/build ./apps/test-service/internal/coverageexec -run 'Runner|Collector|PinnedOutput|Independent|BundleVersion' -count=1
```

- [ ] **Step 3: 实现 dedicated collector child 与 generic attachment**

扩展 fixed unset list 覆盖 dynamic loader 与 GCOV/GCOVR controls，但保持 Windows bundle tests 的排序确定性。`PreparedExecution.Verify/VerifyAfter/PinnedOutput` 每次验证 bundle pin、collector child 和全部 non-owning verifier。Build Boundary 只在 collector 完整验证后接管所有权；attach 失败仍由调用方关闭。

- [ ] **Step 4: 运行 GREEN 与 race**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/build ./apps/test-service/internal/coverageexec -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build -run 'Runner|Collector|Ownership|Close' -count=1
```

- [ ] **Step 5: 提交 Task 5**

```powershell
git add -- apps/test-service/internal/coveragebundle apps/test-service/internal/coveragegcc/collector.go apps/test-service/internal/coveragegcc/collector_test.go apps/test-service/internal/build apps/test-service/internal/coverageexec
git diff --cached --check
git commit -m "feat: execute gcovr with independent capabilities"
```

---

## Task 6：实现 gcovr 8.6 bounded parser 与 GCC normalizer

**Files:**

- Create: `apps/test-service/internal/coverageparser/gcovr/model.go`
- Create: `apps/test-service/internal/coverageparser/gcovr/parser.go`
- Create: `apps/test-service/internal/coverageparser/gcovr/parser_test.go`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/simple.json`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/branches.json`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/functions.json`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/empty.json`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/malformed.json`
- Create: `apps/test-service/internal/coverageparser/gcovr/testdata/duplicate.json`
- Modify: `apps/test-service/internal/coveragenormalize/llvm.go`
- Create: `apps/test-service/internal/coveragenormalize/shared.go`
- Create: `apps/test-service/internal/coveragenormalize/gcc.go`
- Create: `apps/test-service/internal/coveragenormalize/gcc_test.go`
- Create: `apps/test-service/internal/coveragenormalize/testdata/gcc-coverage-v1.golden.json`
- Modify: `apps/test-service/internal/coveragenormalize/writer_test.go`

**Parser model:**

```go
type Export struct {
    FormatVersion string
    Files []File
}

type File struct {
    RelativePath string
    Functions Metric
    Lines []Line
}

type Line struct {
    Number int64
    Count int64
    Branches Metric
}
```

Parser 使用 `json.Decoder.Token` 和 bounded reader，启用 `UseNumber`，拒绝 duplicate/unknown field、trailing value、invalid UTF-8、非整数、负数、overflow、过深嵌套和超过 `coveragenormalize.DefaultLimits()` 的 counts。只接受 gcovr 8.6 实际 schema/format version；fixture 必须由锁定 bundle 的真实 gcovr output 归一化得到并随测试提交。

**Normalizer API:**

```go
type GCCInput struct {
    Export coverageparsergcovr.Export
    WorkspaceRoot string
    Matcher *GlobMatcher
    Toolchain coveragedomain.ToolchainSnapshot
    Completeness coveragedomain.Completeness
    Limits Limits
}

func NormalizeGCC(GCCInput) (coveragemodelv1.CoverageDocumentV1, []SourceBinding, error)
```

- [ ] **Step 1: 写 parser RED tests**

测试 simple/branches/functions/empty/malformed/duplicate/unknown/unsupported version/size/depth/files/functions/lines/branches/string overflow，以及 Linux relative forward-slash path 的 `.`、`..`、absolute、backslash、NUL 拒绝。

- [ ] **Step 2: 写 normalizer RED/golden tests**

覆盖 include/exclude、Linux case-sensitive path、source symlink escape、hard-link duplicate source identity、source digest mutation、sort、summary overflow、GCC provenance、partial completeness。对同一逻辑数据不同输入顺序连续编码两次，断言 canonical Coverage JSON byte-identical。

- [ ] **Step 3: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageparser/gcovr ./apps/test-service/internal/coveragenormalize -run 'GCovr|GCC|Canonical|Golden' -count=1
```

- [ ] **Step 4: 实现 parser，并从 LLVM normalizer 提取共享 helper**

只提取 source binding、completeness、provenance、metric/summary overflow 和 canonical validation；LLVM-specific export validation 保留在 `llvm.go`。GCC normalizer 不接受 native absolute path，必须将 parser 的 canonical relative path绑定到 retained Workspace source evidence。

- [ ] **Step 5: 运行 GREEN、fuzz seed 与 regression**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageparser/gcovr ./apps/test-service/internal/coveragenormalize -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/coveragenormalize -run 'LLVM|Golden|Canonical' -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverageparser/gcovr -run '^TestParse' -count=25
```

- [ ] **Step 6: 提交 Task 6**

```powershell
git add -- apps/test-service/internal/coverageparser/gcovr apps/test-service/internal/coveragenormalize
git diff --cached --check
git commit -m "feat: normalize bounded gcovr coverage"
```

---

## Task 7：泛化 Coordinator collector/normalizer，并注册 Linux GCC Adapter

**Files:**

- Modify: `apps/test-service/internal/task/plan.go`
- Modify: `apps/test-service/internal/task/plan_test.go`
- Modify: `apps/test-service/internal/coverageexec/model.go`
- Modify: `apps/test-service/internal/coverageexec/planner.go`
- Modify: `apps/test-service/internal/coverageexec/planner_test.go`
- Modify: `apps/test-service/internal/coverageexec/coordinator.go`
- Modify: `apps/test-service/internal/coverageexec/coordinator_test.go`
- Modify: `apps/test-service/internal/coverageexec/orchestration_windows_test.go`
- Modify: `apps/test-service/internal/coverageexec/orchestration_faults_windows_test.go`
- Create: `apps/test-service/internal/coverageexec/orchestration_linux_test.go`
- Modify: `apps/test-service/internal/runtime/coverage_execution.go`
- Modify: `apps/test-service/internal/runtime/coverage_execution_test.go`
- Modify: `apps/test-service/internal/runtime/coverage_execution_windows_test.go`
- Create: `apps/test-service/internal/runtime/coverage_execution_linux_test.go`
- Modify: `apps/test-service/internal/runtime/runtime.go`
- Modify: `apps/test-service/internal/runtime/runtime_test.go`

**Generic adapter contract:**

```go
type CollectionPlan struct {
    Aggregate task.ProcessSpec
    Normalize *task.ProcessSpec
}

type NormalizeInput struct {
    ProcessOutput []byte
    PinnedOutput coverageplatform.Output
    WorkspaceRoot string
    Matcher *coveragenormalize.GlobMatcher
    Toolchain coveragedomain.ToolchainSnapshot
    Completeness coveragedomain.Completeness
    Limits coveragenormalize.Limits
}

type PreparedAdapter interface {
    Toolset() coverageplatform.Toolset
    Instrumentation() coverageplatform.Instrumentation
    Allocator() testrun.ProfileAllocator
    PrepareTests(context.Context, PreparedBuild) error
    SealEvidence([]testrun.ProfileExpectation, []testrun.InvocationOutcome) ([]coveragedomain.CompletenessReason, error)
    PrepareCollector(context.Context, PreparedBuild, coverageplatform.DirectoryVerifier, []coveragerun.TrustedPath) (CollectionPlan, error)
    Normalize(context.Context, NormalizeInput) (coveragemodelv1.CoverageDocumentV1, []coveragenormalize.SourceBinding, error)
    Close() error
}
```

新增唯一 service action：

```go
ServiceActionCoverageNormalize ServiceAction = "coverage-normalize"
```

LLVM `CollectionPlan` 含 profdata aggregate 和 llvm-cov normalize process；GCC 含 gcovr aggregate 且 `Normalize=nil`，planner 为后者创建 `StepCoverageNormalize + ServiceActionCoverageNormalize`。step vocabulary/state machine 不改变。

- [ ] **Step 1: 写 planner/coordinator RED tests**

Characterize LLVM 仍为 configure/build/test/merge/normalize/report/publish 且 normalize stdout bounded。新增 GCC 测试断言同一 step 顺序、gcovr 为真正 merge process、normalize 为 service action、pinned output read 前后验证、no stdout dependency、serial tests、collector child 不冲突。

- [ ] **Step 2: 写 failure mapping RED tests**

断言 missing/untrusted `.gcda` → `profile_collection_failed`；gcovr nonzero/crash/timeout → `merge_failed`；pinned output、parser、normalizer、owner early close → `normalization_failed`；assertion failure + valid coverage 仍 `available`；crash/timeout + closed partial evidence 为 `partial`；cancel/trust loss 不启动下一阶段。

- [ ] **Step 3: 运行 RED**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/task ./apps/test-service/internal/coverageexec ./apps/test-service/internal/runtime -run 'CoverageNormalize|GCC|LLVM|FailureMapping|Registry' -count=1
```

- [ ] **Step 4: 实现 generic coordinator 与 platform registry**

在 build 成功后、embedded tests 准备前调用 `PrepareTests`。测试结束后调用 `SealEvidence`，再用 `<executionRoot>/collector` verifier 调 `PrepareCollector`。GCC service normalize 从 `PreparedBuild.PinnedCoverageOutput()` 读取，LLVM 继续使用 captured process stdout。两者都由 adapter `Normalize` 返回同一 document/bindings。

`coverageExecutionConfig` 增加内部 `CoverageBundleRoot string` seam，值必须是 absolute canonical exact bundle root。production bootstrap 从 verified Service executable 的安装树推导 `<product>/bundles/coverage`；tests/CI 注入 exact `.superpowers/runtime/coverage-bundle/linux-x64`。两者统一调用 `coveragebundle.ResolveExact`，且不增加 Protocol/Workspace setting。Registry 只映射 Windows x64 clang-cl → LLVM、Linux x64 GCC → GCC；Linux Clang/MSVC/其它组合 explicit unsupported。

- [ ] **Step 5: 运行 GREEN、race 与全 coverage packages**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/task ./apps/test-service/internal/coverageexec ./apps/test-service/internal/runtime -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/runtime -run 'Coverage|Cancel|Close|Resume' -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/coverage... -count=1
```

- [ ] **Step 6: 提交 Task 7**

```powershell
git add -- apps/test-service/internal/task/plan.go apps/test-service/internal/task/plan_test.go apps/test-service/internal/coverageexec apps/test-service/internal/runtime
git diff --cached --check
git commit -m "feat: execute Linux GCC coverage runs"
```

---

## Task 8：增加 CppUTest 与 Unity production Linux coverage smoke

**Files:**

- Modify: `apps/code-oss-extension/test/coverage-service-smoke-support.ts`
- Modify: `apps/code-oss-extension/test/coverage-service-smoke-support.test.ts`
- Modify: `apps/code-oss-extension/test/coverage-service-smoke.test.ts`
- Modify: `apps/code-oss-extension/package.json`
- Modify: `apps/code-oss-extension/test/fixtures/coverage/CMakeLists.txt`
- Modify: `apps/code-oss-extension/test/fixtures/coverage/src/math.cpp`
- Modify: `apps/code-oss-extension/test/fixtures/coverage/test/math_test.cpp`
- Create: `apps/code-oss-extension/test/fixtures/coverage-unity/CMakeLists.txt`
- Create: `apps/code-oss-extension/test/fixtures/coverage-unity/src/math.c`
- Create: `apps/code-oss-extension/test/fixtures/coverage-unity/test/test_math.c`
- Create: `apps/code-oss-extension/test/fixtures/coverage-unity/unit-test-ide.json`
- Modify: `tools/service-probe/src/test-framework-fixture.ts`
- Modify: `tools/service-probe/src/test-framework-fixture.test.ts`

**Smoke cases:**

1. GCC + CppUTest assertion failure，TestRun=`failed`、CoverageRun=`available`。
2. GCC + Unity passing run，CoverageRun=`available`。
3. test-only injection 覆盖 crash、timeout/cancel、missing data、malformed pinned JSON；production Workspace schema 不出现 injection 字段。
4. 两次相同成功运行的 Coverage JSON bytes/SHA-256 完全相同。

- [ ] **Step 1: 写 fixture/support RED tests**

扩展 strict evidence schema，记录无路径的 toolchain digest、bundle digest、framework、run/report outcome、known line/branch/function counts、artifact digest、determinism 和 timestamps。拒绝 additional properties、native paths、env、argv、secret-like keys。

- [ ] **Step 2: 运行 RED**

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm --filter code-oss-extension test
```

- [ ] **Step 3: 实现共享 smoke harness 与 Unity fixture**

保留 Windows Named Pipe/WFP 流程；Linux 使用 Unix Socket，显式传入 test-only bundle seam。每个 artifact 都走 Protocol v1.4 chunk/size/SHA-256 校验、shared Coverage JSON decoder、strict JUnit tokenizer 和 offline HTML metadata 检查。

- [ ] **Step 4: 运行 TypeScript GREEN 与 Linux compile preparation**

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm --filter code-oss-extension test
pnpm --filter code-oss-extension build
```

Expected: Windows 上 unit tests PASS；Linux native smoke 只由明确的 Linux entrypoint 执行，非 Linux 不伪造 PASS。

- [ ] **Step 5: 提交 Task 8**

```powershell
git add -- apps/code-oss-extension/test apps/code-oss-extension/package.json tools/service-probe/src/test-framework-fixture.ts tools/service-probe/src/test-framework-fixture.test.ts
git diff --cached --check
git commit -m "test: cover Linux GCC frameworks end to end"
```

---

## Task 9：建立 Linux native offline boundary 与 closed evidence

**Files:**

- Create: `tools/service-probe/src/linux-offline-boundary.ts`
- Create: `tools/service-probe/src/linux-offline-boundary.test.ts`
- Create: `tools/service-probe/src/linux-offline-boundary.e2e.ts`
- Create: `tools/service-probe/src/linux-offline-boundary.e2e.test.ts`
- Modify: `tools/service-probe/src/native-network-guard.ts`
- Modify: `tools/service-probe/src/native-network-guard.test.ts`
- Modify: `tools/service-probe/package.json`
- Modify: `tools/service-probe/tsconfig.json`
- Modify: `apps/code-oss-extension/test/coverage-service-smoke.test.ts`
- Create: `tools/service-probe/testdata/linux-offline-report.valid.json`
- Create: `tools/service-probe/testdata/linux-offline-report.invalid.json`

**Boundary contract:**

Bootstrap 完成后，以 `unshare --net --fork` 建立没有 external interface/route 的 network namespace；在同一 isolated process tree 中启动 Service、CMake、GCC/G++、CppUTest/Unity fixture、gcov、bundled Python/gcovr。先在 namespace 内证明 loopback/Unix socket 可用，再证明外部 TCP/UDP route/connect 不可用。namespace 建立或 audit 失败必须 FAIL，不得 SKIP 成功。

- [ ] **Step 1: 写 schema/sequencing RED tests**

覆盖 boundary 必须在 Service 前建立、所有 descendants 继承同一 net namespace、teardown 等待完整进程树、外部 route/connect probe、Unix Socket 可用、signal/cancel 清理、unknown process/evidence fields 和 native-path leakage。

- [ ] **Step 2: 运行 RED**

```powershell
pnpm --filter @unit-test-ide/service-probe test
```

- [ ] **Step 3: 实现 Linux boundary driver**

禁止 shell-composed user input；用固定 executable/argv 和已有 native process runner。以 current unprivileged UID/GID 执行 namespace child；需要的 `sudo` 仅在 CI bootstrap/runner policy 中固定配置。证据原子写入 `.native-e2e/artifacts/linux/linux-gcc-coverage-report.json`，closed schema 不含 path、env、argv、stdout/stderr 或 token。

- [ ] **Step 4: Linux 本机/CI 命令**

```bash
pnpm install --frozen-lockfile
pnpm prepare:coverage-bundle
node tools/service-probe/build-service.mjs
pnpm --filter @unit-test-ide/service-probe test
pnpm --filter code-oss-extension test:coverage-service-smoke:linux
```

Expected: CppUTest、Unity、determinism、offline audit 全部 PASS；边界不可建立则命令非零退出。

- [ ] **Step 5: 提交 Task 9**

```powershell
git add -- tools/service-probe apps/code-oss-extension/test/coverage-service-smoke.test.ts apps/code-oss-extension/package.json
git diff --cached --check
git commit -m "test: enforce offline Linux coverage execution"
```

---

## Task 10：新增 `coverage-linux-gcc` required check，完成全量验收与 roadmap 证据

**Files:**

- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- Modify: `docs/superpowers/specs/2026-09-06-phase7-linux-gcc-coverage-execution-design.md`
- Create: `docs/superpowers/evidence/2026-09-06-phase7-linux-gcc-coverage-acceptance.md`

**CI job:**

新增独立 job ID/name `coverage-linux-gcc`，`runs-on: ubuntu-24.04`，不依赖 packaging/release inputs。步骤固定为 checkout、Node 24.18.0、pnpm 11.4.0、Go 1.26.6、frozen install、CMake bundle、coverage bundle、Go unit/race/vet、TypeScript tests、native CppUTest/Unity offline smoke、determinism、leak audit。`if: always()` 上传 `linux-gcc-coverage-report.json`，`if-no-files-found: error`；artifact 上传不替代 job PASS。

- [ ] **Step 1: 写 workflow/static RED tests**

在现有 workflow validation tests 中断言 job 名唯一、无 `continue-on-error`、无 success SKIP、在 native execution 前完成 bundle bootstrap、offline boundary 包住 Service 整棵进程树、evidence always 上传且 schema validator 执行。断言现有 `verify-windows`、`verify-linux`、package 和 unsigned qualification job 未删除。

- [ ] **Step 2: 实现 CI job**

关键 commands：

```bash
go test ./apps/test-service/... -count=1
go test -race ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coverageexec -count=1
go vet ./apps/test-service/...
pnpm --filter @unit-test-ide/service-probe test
pnpm --filter code-oss-extension test:coverage-service-smoke:linux
```

- [ ] **Step 3: 运行本地可执行回归**

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/... -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go vet ./apps/test-service/...
pnpm verify
pnpm --filter @unit-test-ide/service-probe test
pnpm --filter code-oss-extension test
git diff --check
git status --short
```

Expected: Windows 可运行回归 PASS；唯一未跟踪项仍可为用户自有 `.merge-stash-20260903/`。

- [ ] **Step 4: 提交 CI gate（仍不推送）**

```powershell
git add -- .github/workflows/foundation.yml docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md docs/superpowers/specs/2026-09-06-phase7-linux-gcc-coverage-execution-design.md docs/superpowers/evidence/2026-09-06-phase7-linux-gcc-coverage-acceptance.md
git diff --cached --check
git commit -m "ci: require Linux GCC coverage execution"
```

- [ ] **Step 5: 用户授权后才推送并创建 PR**

在执行任何远端写入前，向用户报告本地 branch、HEAD、测试结果和 diff stat，并取得包含 GitHub/Gitee scope 的明确授权。授权后普通推送同一 branch 到 GitHub `colayc/unitTest` 与 Gitee `yc1211/unit-test`，创建 GitHub PR，暂不合并；不得 force push。

- [ ] **Step 6: 观察 PR checks 并修复真实失败**

要求 `coverage-linux-gcc`、现有 Windows LLVM、foundation、package/unsigned qualification 适用检查全部成功。任何失败按 `superpowers:systematic-debugging` 查根因，新增 regression test 后修复；不得通过放宽 required、改成 SKIP、`continue-on-error` 或删除 evidence 解决。

- [ ] **Step 7: 用户再次授权后才合并和同步**

合并前使用 `superpowers:verification-before-completion`。只有用户明确授权合并时才合并 GitHub PR；只有 GitHub master checks 成功后，才把 GitHub `master` 普通同步到 Gitee `master`。随后配置 `coverage-linux-gcc` 为 `master` required check，并重新运行验证。仍不发布 Release、不启用签名。

---

## Final Acceptance Checklist

- [ ] Linux x64 GCC CoverageRun 不再 `unsupported`；Linux Clang 仍精确 `unsupported`。
- [ ] GCC/G++/gcov/gcovr bundle 均有可重复验证的 path-free provenance。
- [ ] CppUTest 与 Unity 都通过 production Service + Protocol v1.4 生成 Coverage JSON/JUnit/HTML。
- [ ] `.gcno/.gcda` stale/unknown/symlink/hard-link/special/escape 全部 fail closed。
- [ ] 四个独立 verifier 与 `collector/gcovr` 生命周期、close order、ABA protection 有测试证明。
- [ ] missing evidence、gcovr failure、parser/normalizer failure 的 domain reason 映射准确。
- [ ] 两次相同运行的 canonical Coverage JSON byte-identical。
- [ ] Linux network namespace 覆盖完整 native process tree，失败不能降级为 SKIP。
- [ ] `coverage-linux-gcc` 为成功的 required check；Windows LLVM 与其余 foundation/unsigned checks 继续成功。
- [ ] Protocol、artifact metadata、logs、stderr 和 CI evidence 无 native path/environment/secret 泄漏。
- [ ] 工作树除明确用户文件外干净；GitHub/Gitee 指向同一已验证提交。
- [ ] 没有发布 GitHub Release、没有启用签名；正式签名和第三方 license/legal 人工审批继续保留到真实公开发布前。
