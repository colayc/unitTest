# Phase 5C：Coverage build、identity 与 CMake instrumentation 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 从已验证的 base Build Profile 派生独立、可锁定、可恢复的 coverage build，并为 clang-cl/Clang/GCC 生成固定 CMake instrumentation plan；本计划只证明 instrumented binary/profile capability，不生成最终 Coverage JSON。

**架构：** `coveragebuild` 组合 Phase 3 `build`/`cmake`/`toolchain`，但使用独立 identity、directory manifest 和 lease。Service-owned CMake include 是唯一 instrumentation 入口。configure 后检查 File API compile/link fragment，build 后固定 executable 与 coverage tool identity。

**依赖：** Phase 5A domain/config；Phase 5B bundle capability contract。

## 全局约束

- coverage directory 位于 Service data root，不能是 Workspace 普通 build directory、preset binaryDir 或用户绝对路径。
- 同一 coverage build identity 只允许一个 running owner；lock 不跨 identity 误互斥。
- Protocol/Workspace 不提供 CMake include path、compiler flags、link flags、response file 或 environment。
- Service include 保留已验证 preset top-level include semantics，但不能引入新路径来源。
- clang-cl/Clang 与 llvm-profdata/llvm-cov 必须同 installation/compatible version；GCC/gcov 必须同 Toolchain identity。
- instrumentation 被项目撤销或 binary 无 mapping 时，在 test execution 前失败。

---

### Task 1：Coverage build identity、manifest 与 directory lease

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/identity.go`
- 创建：`apps/test-service/internal/coveragebuild/identity_test.go`
- 创建：`apps/test-service/internal/coveragebuild/manifest.go`
- 创建：`apps/test-service/internal/coveragebuild/manifest_test.go`
- 创建：`apps/test-service/internal/coveragebuild/directory.go`
- 创建：`apps/test-service/internal/coveragebuild/directory_test.go`
- 创建：`apps/test-service/internal/coveragebuild/lease.go`
- 创建：`apps/test-service/internal/coveragebuild/lease_test.go`
- 创建：`apps/test-service/internal/coveragebuild/lease_windows_test.go`
- 创建：`apps/test-service/internal/coveragebuild/lease_unix_test.go`
- 修改：`apps/test-service/internal/runtime/data_dir.go`
- 修改：`apps/test-service/internal/runtime/data_dir_test.go`

**identity input：**

```go
type IdentityInput struct {
    WorkspaceIdentity, ProjectID, SourceDirectory string
    BaseProfile cmake.BuildProfile
    Toolchain toolchain.Instance
    CMake cmake.Installation
    Driver coveragedomain.Driver
    InstrumentationTemplateSHA256 string
}
```

- [ ] **Step 1：写出 canonical identity/directory/lease 失败测试**

覆盖：

- map/list stable order、path case/URI normalization；
- source content change 不改变 identity；
- profile/toolchain/CMake/driver/template 变化改变 identity；
- identity 不泄露 native path；
- directory 始终在 data root；
- manifest atomic publish 与 digest；
- junction/symlink/reparse/path escape 拒绝；
- same identity mutual exclusion、different identity independence；
- abandoned lock、owner mismatch、double release、root replacement；
- ordinary build directory 永不被 adopt/delete。

- [ ] **Step 2：运行 tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/runtime -run 'Identity|Manifest|Directory|Lease' -count=1
```

- [ ] **Step 3：实现 manifest-bound directory**

Directory 名仅使用 identity digest；manifest 保存 canonical public identity 与 owner metadata。递归 cleanup 只能作用于 Resolve 后再次确认位于 coverage root 的目录。

- [ ] **Step 4：运行全套/race**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/runtime -count=1
go test -race ./apps/test-service/internal/coveragebuild -count=1
```

- [ ] **Step 5：提交 identity/lease**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/runtime/data_dir.go apps/test-service/internal/runtime/data_dir_test.go
git commit -m "feat: isolate coverage build directories"
```

### Task 2：Service-owned CMake instrumentation template

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/template.go`
- 创建：`apps/test-service/internal/coveragebuild/template_test.go`
- 创建：`apps/test-service/internal/coveragebuild/testdata/llvm.cmake.golden`
- 创建：`apps/test-service/internal/coveragebuild/testdata/gcc.cmake.golden`
- 创建：`apps/test-service/internal/coveragebuild/testdata/injection.invalid.json`
- 修改：`apps/test-service/internal/cmake/profile.go`
- 修改：`apps/test-service/internal/cmake/profile_test.go`
- 修改：`apps/test-service/internal/cmake/presets.go`
- 修改：`apps/test-service/internal/cmake/presets_test.go`

- [ ] **Step 1：写出 deterministic template/preset composition 失败测试**

覆盖：

- LLVM compile/link profile generation 与 compile mapping；
- GCC compile/link `--coverage` 与 compile `-fprofile-abs-path`；
- C/C++ language generator expression；
- clang-cl MSVC frontend 参数形式；
- LF/UTF-8/fixed field order/template digest；
- existing validated `CMAKE_PROJECT_TOP_LEVEL_INCLUDES` 保留固定顺序；
- duplicate Service include、user override、semicolon/list injection 拒绝；
- raw flag/env/path field 不存在；
- base optimization 不被 template 改写。

- [ ] **Step 2：运行 template tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/cmake -run 'Coverage|TopLevelInclude|Template' -count=1
```

- [ ] **Step 3：实现 fixed template writer**

Template 写入 coverage directory 的 Service-owned subdirectory，使用 atomic temp+rename、close、SHA-256 和 immutable file snapshot。Planner 以单独 argv item 传递完整 cache variable，不使用 Shell quoting。

- [ ] **Step 4：运行 CMake/profile 全套**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/cmake -count=1
git diff --check
```

- [ ] **Step 5：提交 instrumentation template**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/cmake
git commit -m "feat: generate coverage cmake instrumentation"
```

### Task 3：LLVM coverage configure/build planner 与 tool pin

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/llvm.go`
- 创建：`apps/test-service/internal/coveragebuild/llvm_test.go`
- 创建：`apps/test-service/internal/coveragebuild/tool_pin.go`
- 创建：`apps/test-service/internal/coveragebuild/tool_pin_test.go`
- 修改：`apps/test-service/internal/toolchain/model.go`
- 修改：`apps/test-service/internal/toolchain/clangcl_windows.go`
- 修改：`apps/test-service/internal/toolchain/clangcl_windows_test.go`
- 修改：`apps/test-service/internal/toolchain/discover_unix.go`
- 修改：`apps/test-service/internal/toolchain/discover_unix_test.go`
- 修改：`apps/test-service/internal/build/boundary.go`
- 修改：`apps/test-service/internal/build/planner_test.go`

- [ ] **Step 1：写出 LLVM plan/version/pin 失败测试**

覆盖：

- clang-cl/Clang family only；
- compiler/profdata/cov same installation root；
- compatible complete version；
- lld-link/profile runtime capability；
- missing/mismatched tool safe downgrade；
- no PATH fallback；
- compiler/tool file replacement 与 directory replacement；
- boundary 未 adopt/released/tampered 拒绝；
- public projection 只暴露 driver/version，不暴露 tool path。

- [ ] **Step 2：运行 tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/toolchain ./apps/test-service/internal/build -run 'LLVM|Coverage|ToolPin' -count=1
```

- [ ] **Step 3：实现 LLVM planner/pin**

Planner 复用 Phase 3 CMake executable/configure/build argv；仅追加 Service include 与独立 binaryDir。Tool pin 生命周期覆盖 configure、build、test、merge、export，最终 terminal 后释放。

- [ ] **Step 4：运行 Windows/Linux compile tests**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/toolchain ./apps/test-service/internal/build -count=1
go test -race ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/build -count=1
```

- [ ] **Step 5：提交 LLVM coverage planner**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/toolchain apps/test-service/internal/build
git commit -m "feat: plan llvm coverage builds"
```

### Task 4：GCC coverage configure/build planner 与 gcov identity

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/gcc.go`
- 创建：`apps/test-service/internal/coveragebuild/gcc_test.go`
- 修改：`apps/test-service/internal/toolchain/discover_unix.go`
- 修改：`apps/test-service/internal/toolchain/discover_unix_test.go`
- 修改：`apps/test-service/internal/toolchain/gnu.go`
- 修改：`apps/test-service/internal/toolchain/gnu_test.go`
- 修改：`apps/test-service/internal/build/boundary.go`
- 修改：`apps/test-service/internal/build/planner_test.go`

- [ ] **Step 1：写出 GCC/gcov pairing 和 serial lease 失败测试**

覆盖：

- GCC family only；
- `gcov --version` 与 compiler family/version/target compatible；
- gcov path 来自同 toolchain root/candidate contract；
- no PATH late binding；
- driver 为 `gcov`、processor capability 引用 Phase 5B bundle；
- GCC run concurrency 固定为 1；
- gcov/bundle replacement 拒绝；
- optimized base profile 产生 warning 而非改写 optimization。

- [ ] **Step 2：运行 tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/toolchain ./apps/test-service/internal/build -run 'GCC|GCov|Coverage' -count=1
```

- [ ] **Step 3：实现 GCC planner/pin**

Planner 把 gcov identity 与 bundle manifest digest 纳入 build/request snapshot，但不把 gcovr path 交给 Workspace/Protocol。

- [ ] **Step 4：运行 Linux-focused tests/race**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/toolchain ./apps/test-service/internal/build -count=1
go test -race ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/toolchain -count=1
```

- [ ] **Step 5：提交 GCC coverage planner**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/toolchain apps/test-service/internal/build
git commit -m "feat: plan gcc coverage builds"
```

### Task 5：File API/post-build instrumentation verification

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/verify.go`
- 创建：`apps/test-service/internal/coveragebuild/verify_test.go`
- 创建：`apps/test-service/internal/coveragebuild/testdata/fileapi/llvm-target.json`
- 创建：`apps/test-service/internal/coveragebuild/testdata/fileapi/gcc-target.json`
- 创建：`apps/test-service/internal/coveragebuild/testdata/fileapi/uninstrumented-target.json`
- 修改：`apps/test-service/internal/cmake/fileapi.go`
- 修改：`apps/test-service/internal/cmake/fileapi_test.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main_test.go`

- [ ] **Step 1：写出 compile/link/mapping verification 失败测试**

覆盖：

- 每个目标 C/C++ compile group 包含对应 instrumentation；
- link fragment/profile runtime 存在；
- mixed instrumented/uninstrumented source 给出 stable diagnostic；
- target option 撤销 `-fno-profile-instr-generate` 等拒绝；
- stale File API reply/build manifest 拒绝；
- binary fingerprint 与 CMake target artifact 一致；
- empty mapping/uninstrumented binary 不运行 test；
- path/native command 不出现在 public diagnostic。

- [ ] **Step 2：运行 verifier tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/cmake ./apps/test-service/cmd/cmake-fixture -run 'Coverage|Instrumentation|FileAPI' -count=1
```

- [ ] **Step 3：实现 bounded verification**

File API parser 只增加 compile/link fragment projection，不把任意 fragment 变成 executable。Verification 根据 Adapter contract exact-match token；未知/复杂 fragment 只用于 diagnostic，不回传 Protocol。

- [ ] **Step 4：运行真实 fixture**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/cmake ./apps/test-service/cmd/cmake-fixture -count=1
git diff --check
```

- [ ] **Step 5：提交 instrumentation verification**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/cmake apps/test-service/cmd/cmake-fixture
git commit -m "feat: verify coverage instrumentation"
```

### Task 6：Coverage Build Coordinator 与 Task integration

**文件：**

- 创建：`apps/test-service/internal/coveragebuild/coordinator.go`
- 创建：`apps/test-service/internal/coveragebuild/coordinator_test.go`
- 修改：`apps/test-service/internal/build/coordinator.go`
- 修改：`apps/test-service/internal/build/coordinator_test.go`
- 修改：`apps/test-service/internal/task/plan.go`
- 修改：`apps/test-service/internal/task/plan_test.go`
- 修改：`apps/test-service/internal/task/continuation_test.go`

- [ ] **Step 1：写出 prepare/build/lease/boundary ownership 失败测试**

覆盖：

- prepare 不创建嵌套 Task；
- plan steps 只有 Service-generated configure/build/verify；
- persist-before-start；
- cancel/timeout/restart release lease/pin exactly once；
- build success 后 target/Catalog source 可刷新；
- continuation single/batch 不能绕过 boundary；
- same identity concurrent request queued/conflict 语义固定；
- ordinary build coordinator behavior 不变。

- [ ] **Step 2：运行 coordinator tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/build ./apps/test-service/internal/task -run 'Coordinator|Continuation|Boundary|Lease' -count=1
```

- [ ] **Step 3：实现 coverage build vertical slice**

Coordinator 只暴露内部 `PreparePlan`/prepared handle；Protocol Session 尚不注册 coverage start。Prepared handle 持有 lease、tool pin、template pin 和 build directory ownership。

- [ ] **Step 4：运行 Phase 5C 完整门禁**

```powershell
go test ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/build ./apps/test-service/internal/cmake ./apps/test-service/internal/toolchain ./apps/test-service/internal/task ./apps/test-service/cmd/cmake-fixture -count=1
go test -race ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/build ./apps/test-service/internal/task -count=1
pnpm verify
git diff --check
```

- [ ] **Step 5：提交 Coverage Build Coordinator**

```powershell
git add apps/test-service/internal/coveragebuild apps/test-service/internal/build apps/test-service/internal/task
git commit -m "feat: coordinate coverage builds"
```

## Phase 5C 完成检查

- [ ] 独立 build identity/directory/lease
- [ ] deterministic Service-owned CMake template
- [ ] LLVM planner 与 tool pin
- [ ] GCC/gcov planner 与 serial ownership
- [ ] File API/post-build verification
- [ ] Coverage Build Coordinator 与 ExecutionBoundary
- [ ] 完整 Phase 5C 门禁与独立 security review
