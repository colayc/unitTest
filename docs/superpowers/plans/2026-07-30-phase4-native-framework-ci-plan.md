# Phase 4F：真实框架矩阵、安全与 Hosted CI 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 使用固定版本的真实 CppUTest/CppUMock、Unity/CMock 工程验证 Windows/MSVC、Windows/clang-cl、Linux/GCC、Linux/Clang，并关闭 Phase 4 安全、性能和 Hosted CI 门禁。

**架构：** 开发/CI bootstrap 下载固定 upstream archives 并校验 SHA-256；fixture 在 Project build graph 中使用依赖。Service 仍不下载框架。`service-probe` 运行完整用户旅程并生成版本化 native framework report。

**依赖：** Phase 4E。

## 全局约束

- 产品 runtime 无网络下载路径。
- dependency lock 固定 version/revision、URL、SHA-256 和 license。
- CI bootstrap 可缓存，但 cache 内容每次仍校验 SHA-256。
- fixture 不依赖 GitHub token 或 Gitee。
- Windows 正式功能验证使用 MSVC；clang-cl 为独立 profile。
- Linux 分别验证 GCC 和 Clang。
- malformed fixture 不通过篡改 Service parser 产生，而通过真实可执行 fixture contract 产生。
- CI 报告失败时也必须上传。

---

### Task 1：Framework dependency lock、bootstrap 与 license

**文件：**

- 创建：`tools/framework-deps/dependencies.lock.json`
- 创建：`tools/framework-deps/prepare.mjs`
- 创建：`tools/framework-deps/prepare.test.mjs`
- 创建：`tools/framework-deps/README.md`
- 创建：`tools/framework-deps/licenses/CppUTest.txt`
- 创建：`tools/framework-deps/licenses/Unity.txt`
- 创建：`tools/framework-deps/licenses/CMock.txt`
- 修改：`package.json`
- 修改：`.gitignore`

**scripts：**

```json
{
  "prepare:framework-deps": "node tools/framework-deps/prepare.mjs",
  "test:framework-deps": "node --test tools/framework-deps/prepare.test.mjs"
}
```

- [ ] **Step 1：写出 offline/cache/checksum 失败测试**

覆盖：

- exact lock shape；
- HTTPS only；
- SHA-256 mismatch 删除临时文件并失败；
- cache hit 仍校验；
- archive path traversal 拒绝；
- symlink entry 拒绝；
- temp + atomic publish；
- license 必需；
- `--offline` 缺 cache 明确失败；
- runtime package/build 不调用 prepare script。

- [ ] **Step 2：运行 bootstrap tests 并确认失败**

```powershell
node --test tools/framework-deps/prepare.test.mjs
```

预期：FAIL。

- [ ] **Step 3：实现 deterministic bootstrap**

下载目录固定为 ignored `.framework-deps/`。Lock 更新必须显式修改文件和测试；禁止 `latest`。

- [ ] **Step 4：运行 tests 与实际 prepare**

```powershell
pnpm test:framework-deps
pnpm prepare:framework-deps
pnpm prepare:framework-deps --offline
```

预期：PASS，第二次不联网且输出 identity 相同。

- [ ] **Step 5：提交 dependency bootstrap**

```powershell
git add tools/framework-deps package.json .gitignore
git commit -m "build: pin test framework fixtures"
```

### Task 2：真实 CppUTest/CppUMock fixture

**文件：**

- 创建：`testdata/frameworks/cpputest/CMakeLists.txt`
- 创建：`testdata/frameworks/cpputest/CMakePresets.json`
- 创建：`testdata/frameworks/cpputest/.unit-test-ide.json`
- 创建：`testdata/frameworks/cpputest/src/production.cpp`
- 创建：`testdata/frameworks/cpputest/src/production.hpp`
- 创建：`testdata/frameworks/cpputest/tests/all_tests.cpp`
- 创建：`testdata/frameworks/cpputest/tests/pass_tests.cpp`
- 创建：`testdata/frameworks/cpputest/tests/fail_tests.cpp`
- 创建：`testdata/frameworks/cpputest/tests/mock_tests.cpp`
- 创建：`testdata/frameworks/cpputest/tests/crash_tests.cpp`
- 创建：`testdata/frameworks/cpputest/tests/timeout_tests.cpp`
- 创建：`testdata/frameworks/cpputest/expected/catalog.json`
- 创建：`testdata/frameworks/cpputest/expected/results.json`

- [ ] **Step 1：写出 fixture structure/golden 失败测试**

在 `service-probe` 中先定义 expected logical IDs、groups、case names、source lines 和 outcomes。Fixture 覆盖 pass、fail、IGNORE_TEST、CppUMock mismatch、crash、timeout。

- [ ] **Step 2：配置/构建并确认 fixture 测试失败**

```powershell
pnpm prepare:cmake-bundle
pnpm prepare:framework-deps
pnpm --filter @unit-test-ide/service-probe test:e2e:native
```

预期：FAIL，fixture/runner 尚未接入。

- [ ] **Step 3：实现 portable CMake fixture**

不使用平台专用 sleep/crash API；通过小型跨平台 fixture helper 实现可控 timeout/crash。正式 CTest entry 由 `unit_test_ide_add_cpputest` 注册。

- [ ] **Step 4：运行本机可用工具链**

```powershell
pnpm test:e2e:native
```

预期：当前平台 required toolchain 全部 PASS。

- [ ] **Step 5：提交真实 CppUTest fixture**

```powershell
git add testdata/frameworks/cpputest tools/service-probe
git commit -m "test: add native cpputest fixtures"
```

### Task 3：真实 Unity/CMock fixture

**文件：**

- 创建：`testdata/frameworks/unity/CMakeLists.txt`
- 创建：`testdata/frameworks/unity/CMakePresets.json`
- 创建：`testdata/frameworks/unity/.unit-test-ide.json`
- 创建：`testdata/frameworks/unity/src/production.c`
- 创建：`testdata/frameworks/unity/src/production.h`
- 创建：`testdata/frameworks/unity/tests/test_pass.c`
- 创建：`testdata/frameworks/unity/tests/test_fail.c`
- 创建：`testdata/frameworks/unity/tests/test_cmock.c`
- 创建：`testdata/frameworks/unity/tests/test_crash.c`
- 创建：`testdata/frameworks/unity/tests/test_timeout.c`
- 创建：`testdata/frameworks/unity/mocks/MockDependency.c`
- 创建：`testdata/frameworks/unity/mocks/MockDependency.h`
- 创建：`testdata/frameworks/unity/mocks/cmock-generation.json`
- 创建：`testdata/frameworks/unity/expected/catalog.json`
- 创建：`testdata/frameworks/unity/expected/results.json`

- [ ] **Step 1：写出 helper/manifest/golden 失败测试**

Expected 覆盖 pass、TEST_IGNORE、assertion failure、CMock expectation failure、crash、timeout、source location 和 cross-platform stable IDs。

- [ ] **Step 2：运行 native E2E 并确认失败**

```powershell
pnpm prepare:framework-deps
pnpm test:e2e:native
```

预期：FAIL，Unity fixture 尚未完成。

- [ ] **Step 3：实现 helper-driven Unity fixture**

Fixture 显式 include `sdk/cmake/UnitTestIDE.cmake`，由产品 generator 生成 runner；CMock 源码使用 lock 中固定版本预生成并提交，`cmock-generation.json` 记录 generator version、input hash 和 output hash。Service 与 CI 运行阶段都不调用 CMock generator。

- [ ] **Step 4：运行本机可用工具链**

```powershell
pnpm test:e2e:native
```

预期：当前平台 required toolchain 全部 PASS。

- [ ] **Step 5：提交真实 Unity/CMock fixture**

```powershell
git add testdata/frameworks/unity tools/service-probe
git commit -m "test: add native unity cmock fixtures"
```

### Task 4：native framework E2E、报告与失败场景

**文件：**

- 创建：`tools/service-probe/src/native-framework.ts`
- 创建：`tools/service-probe/src/native-framework.test.ts`
- 创建：`tools/service-probe/src/native-framework-report.ts`
- 创建：`tools/service-probe/src/native-framework-report.test.ts`
- 创建：`tools/service-probe/src/native-framework-run.ts`
- 修改：`tools/service-probe/package.json`
- 修改：`package.json`

**旅程：**

```text
inspect
→ discover
→ get catalog
→ run all
→ run single
→ filter
→ repeat
→ failedFromRun
→ cancel
→ timeout
→ stale rediscovery
→ reconnect/replay
```

- [ ] **Step 1：写出 report Schema 和 scenario 失败测试**

报告至少包含：

- platform/toolchain/compiler identity；
- framework dependency identity；
- Catalog revision；
- stable ID digest；
- scenario outcomes；
- source location digest；
- artifact digest；
- security/degradation evidence；
- startedAt/finishedAt；
- overall status。

- [ ] **Step 2：运行 report tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/service-probe test
```

预期：FAIL。

- [ ] **Step 3：实现 native runner 与 atomic report**

每个 toolchain 使用隔离 build/data/work directory。Windows MSVC 和 clang-cl 不复用 build tree；Linux GCC/Clang 同理。

每个平台原子写入：

```text
.native-e2e/artifacts/<platform>/framework-report.json
```

- [ ] **Step 4：运行完整 native E2E**

```powershell
pnpm prepare:cmake-bundle
pnpm prepare:framework-deps
pnpm test:e2e:native
```

预期：required toolchain 全部 PASS，报告原子生成。

- [ ] **Step 5：提交 native framework runner**

```powershell
git add tools/service-probe package.json
git commit -m "test: verify native framework workflows"
```

### Task 5：malformed、安全回归与 10,000 item benchmark

**文件：**

- 创建：`testdata/frameworks/failures/malformed-list/CMakeLists.txt`
- 创建：`testdata/frameworks/failures/malformed-list/main.cpp`
- 创建：`testdata/frameworks/failures/malformed-result/CMakeLists.txt`
- 创建：`testdata/frameworks/failures/malformed-result/main.c`
- 创建：`testdata/frameworks/failures/wrapper/CMakeLists.txt`
- 创建：`testdata/frameworks/failures/wrapper/wrapper.cmake`
- 创建：`testdata/frameworks/failures/external-command/CMakeLists.txt`
- 创建：`apps/test-service/internal/testdiscovery/security_test.go`
- 创建：`apps/test-service/internal/testrun/security_test.go`
- 创建：`apps/test-service/internal/testdomain/catalog_benchmark_test.go`
- 创建：`tools/service-probe/src/native-framework-security.test.ts`

- [ ] **Step 1：写出攻击/边界失败测试**

覆盖：

- Protocol raw execution fields；
- CTest regex metacharacter；
- helper sidecar traversal；
- executable symlink/junction replacement；
- result file link swap；
- wrapper 冒充 direct executable；
- external command discover-only blocked；
- oversized JSON/control/output；
- duplicate identity；
- stale executable same mtime/different content；
- 100,000 item hard limit；
- 10,000 item Catalog benchmark。

- [ ] **Step 2：运行 security/benchmark 并确认失败**

```powershell
go test ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/testrun -run 'Security|Escape|Oversize|Stale' -count=1
go test ./apps/test-service/internal/testdomain -run '^$' -bench Catalog10000 -benchmem -count=3
```

预期：security tests FAIL；benchmark 尚不存在。

- [ ] **Step 3：实现缺失边界并记录非阻断 baseline**

Benchmark 输出写入 CI report metadata，只比较逻辑规模和 allocations 的大幅回归，不设置依赖 runner 速度的脆弱绝对时限。

- [ ] **Step 4：运行 security/race/benchmark**

```powershell
go test ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/testrun ./apps/test-service/internal/testdomain -count=1
go test -race ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/testrun -count=1
go test ./apps/test-service/internal/testdomain -run '^$' -bench Catalog10000 -benchmem -count=3
```

预期：PASS。

- [ ] **Step 5：提交安全与性能基线**

```powershell
git add testdata/frameworks/failures apps/test-service/internal/testdiscovery apps/test-service/internal/testrun apps/test-service/internal/testdomain tools/service-probe
git commit -m "test: harden framework discovery boundaries"
```

### Task 6：GitHub Hosted CI、报告门禁与 Phase 4 收尾

**文件：**

- 修改：`.github/workflows/foundation.yml`
- 修改：`docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- 修改：`docs/superpowers/specs/2026-07-30-test-framework-discovery-execution-design.md`
- 修改：`docs/superpowers/plans/2026-07-30-phase4-test-framework-implementation-index.md`
- 修改：`README.md`

- [ ] **Step 1：先更新 CI report gate tests**

Windows 必须报告 `msvc,clang-cl`；Linux 必须报告 `gcc,clang`。两个 framework、所有 required scenarios 和 stable ID cross-platform digest 必须存在。

- [ ] **Step 2：本地运行完整门禁**

```powershell
pnpm install --frozen-lockfile
pnpm prepare:cmake-bundle
pnpm prepare:framework-deps
pnpm verify
pnpm test:e2e:native
git diff --check
git status --short
```

预期：全部 PASS。

- [ ] **Step 3：更新 Hosted workflow**

两个 verify job 都增加 framework dependency cache/prepare 和 native framework report upload。Artifact：

```text
native-framework-windows-<attempt>
native-framework-linux-<attempt>
```

`if: always()` 上传报告，最后执行 `git diff --exit-code`。

新增 `verify-framework-matrix` job：

1. `needs: [verify-windows, verify-linux]`；
2. 下载两个 native framework artifacts；
3. 校验 report Schema、required toolchain、framework 和 scenario；
4. 比较相同逻辑 case 的 stable ID digest；
5. 校验两个报告使用同一 source commit；
6. 任一缺失、失败或 digest 漂移都阻断 PR。

- [ ] **Step 4：推送 GitHub/Gitee 并等待 Hosted CI**

```powershell
git push github codex/workspace-cmake-toolchains
git push origin codex/workspace-cmake-toolchains
gh run list --branch codex/workspace-cmake-toolchains --limit 5
gh run watch <run-id> --exit-status
```

预期：Windows/Linux jobs 全绿；下载并核对 report digest。

- [ ] **Step 5：更新中文状态文档并提交**

只有 Hosted run 固定 URL、job conclusion 和 artifact digest 已核验后，才把 Phase 4 标记完成。

```powershell
git add .github/workflows/foundation.yml docs README.md
git commit -m "docs: record phase 4 completion"
git push github codex/workspace-cmake-toolchains
git push origin codex/workspace-cmake-toolchains
```

## Phase 4F 完成检查

- [ ] dependency revision/SHA/license 固定
- [ ] 产品 runtime 无网络下载
- [ ] Windows MSVC/clang-cl 全场景
- [ ] Linux GCC/Clang 全场景
- [ ] CppUTest/CppUMock + Unity/CMock
- [ ] pass/fail/skip/Mock/crash/timeout/malformed
- [ ] stable ID cross-platform digest
- [ ] security regression
- [ ] 10,000 item backend baseline
- [ ] Protocol v1.0–v1.3 compatibility
- [ ] `pnpm verify`、native E2E、race
- [ ] Windows/Linux Hosted CI 与报告 digest
- [ ] GitHub/Gitee 分支 SHA 一致
- [ ] 独立全规格、安全和跨平台评审
