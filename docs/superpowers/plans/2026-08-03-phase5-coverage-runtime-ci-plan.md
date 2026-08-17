# Phase 5F：Runtime、recovery、native matrix 与 Hosted CI 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 把 Phase 5A–5E 接入 Task Engine、Runtime、Protocol v1.4 和 TypeScript Client，完成可取消、可恢复、可重放的“Run with Coverage”垂直切片，并在 Windows clang-cl、Linux GCC、Linux Clang 上固定真实 E2E。

**架构：** `coveragerun.Coordinator` 是唯一 coverage continuation provider，组合 Coverage Build、Phase 4 Test Coordinator、Collector、Normalizer 和 Report Service。queued recovery 只保存结构化 request；running recovery 不复用中间 profile。Session 不接受任何 process detail。

**依赖：** Phase 5A–5E。

## 全局约束

- 一个 CoverageRun 只创建一个顶层 Task、一个关联 TestRun 和一个最终 CoverageReport。
- Runtime-only lease/pin/parser/renderer/continuation 不进入 SQLite request JSON。
- Protocol v1.0–v1.3 隐藏 coverage methods/entity/task/owned TestRun，其他 sequence 保持单调并允许跳号。
- cancel/timeout 后不再启动 merge/normalize/report process。
- running Service restart → interrupted/unavailable；queued 只有完整 revalidation 后 resume。
- native product runtime 不联网；coverage bundle 在 CI prepare step 完成后进入 no-network execution phase。
- 最终三配置必须覆盖 CppUTest 与 Unity；Mock assertion 仍沿用 Phase 4 语义。

---

### Task 1：CoverageCoordinator 与 Service-owned execution plan

**文件：**

- 创建：`apps/test-service/internal/coveragerun/coordinator.go`
- 创建：`apps/test-service/internal/coveragerun/coordinator_test.go`
- 创建：`apps/test-service/internal/coveragerun/continuation.go`
- 创建：`apps/test-service/internal/coveragerun/continuation_test.go`
- 创建：`apps/test-service/internal/coveragerun/finalizer.go`
- 创建：`apps/test-service/internal/coveragerun/finalizer_test.go`
- 修改：`apps/test-service/internal/coveragebuild/coordinator.go`
- 修改：`apps/test-service/internal/coveragecollect/llvm.go`
- 修改：`apps/test-service/internal/coveragecollect/gcc.go`
- 修改：`apps/test-service/internal/coveragenormalize/normalize.go`
- 修改：`apps/test-service/internal/coveragereport/service.go`
- 修改：`apps/test-service/internal/testrun/coordinator.go`
- 修改：`apps/test-service/internal/task/plan.go`
- 修改：`apps/test-service/internal/task/continuation_test.go`

**plan phases：**

```text
configureCoverage → buildCoverage → verifyInstrumentation
→ refreshCatalog → runTests
→ collectProfiles → normalizeCoverage → renderReports
```

- [ ] **Step 1：写出 orchestration/outcome/boundary 失败测试**

覆盖：

- atomic Task/CoverageRun/TestRun creation；
- build 后 refresh Catalog、stable selection rebind；
- LLVM bounded waves/GCC serial；
- assertion failure 继续 collect；
- crash/timeout partial；
- normal missing profile unavailable；
- cancel/Task timeout 不继续；
- each continuation revalidates executable/dir/template/tool pin；
- plan durable persist before process start；
- no nested Task；
- finalizer exactly once；
- cleanup/lease/pin release exactly once；
- Protocol-like malicious request 不能影响 `ProcessSpec`。

- [ ] **Step 2：运行 coordinator tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragereport ./apps/test-service/internal/testrun ./apps/test-service/internal/task -run 'Coordinator|Coverage|Continuation|Finalizer|Boundary' -count=1
```

- [ ] **Step 3：实现单 owner orchestration**

Coordinator 只持有 immutable request snapshot 和 runtime handles。Step observer/interpreter 继续由 Task Manager 调用；Coordinator 不直接写 Task store，不直接发布 event。

- [ ] **Step 4：运行领域全套/race**

```powershell
go test ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragebuild ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragereport ./apps/test-service/internal/testrun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragereport ./apps/test-service/internal/task -count=1
```

- [ ] **Step 5：提交 CoverageCoordinator**

```powershell
git add apps/test-service/internal/coveragerun apps/test-service/internal/coveragebuild apps/test-service/internal/coveragecollect apps/test-service/internal/coveragenormalize apps/test-service/internal/coveragereport apps/test-service/internal/testrun apps/test-service/internal/task
git commit -m "feat: orchestrate coverage runs"
```

### Task 2：Session v1.4 routing、projection 与 TypeScript vertical slice

**文件：**

- 修改：`apps/test-service/internal/protocol/envelope.go`
- 修改：`apps/test-service/internal/protocol/envelope_test.go`
- 修改：`apps/test-service/internal/session/session.go`
- 创建：`apps/test-service/internal/session/v14_test.go`
- 修改：`apps/test-service/internal/server/server.go`
- 创建：`apps/test-service/internal/server/v14_projection_test.go`
- 修改：`packages/test-client/src/client.ts`
- 修改：`packages/test-client/src/client.test.ts`
- 修改：`packages/test-client/src/subscription.ts`
- 修改：`tools/service-probe/src/probe.ts`
- 修改：`tools/service-probe/src/probe.test.ts`

- [ ] **Step 1：写出 v1.4 routing/auth/projection 失败测试**

覆盖：

- negotiation 1.4→1.0；
- strict decode/local decoder；
- start/get/list/report；
- idempotency；
- Workspace authorization/trust；
- invalid profile/project/selection；
- unsafe field rejection；
- coverage lifecycle event replay；
- artifacts read；
- v1.0–v1.3 hide coverage task/owned TestRun；
- sequence monotonic safe gap；
- cancel ownership；
- error code/message redaction。

- [ ] **Step 2：运行 Session/Client/Probe tests 并确认失败**

```powershell
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session ./apps/test-service/internal/server -run 'V14|Coverage|Projection' -count=1
pnpm --filter @unit-test-ide/test-client test
pnpm --filter @unit-test-ide/service-probe test
```

- [ ] **Step 3：实现 version-aware routing/projection**

Session 从 Runtime facade 获取结构化 Coverage service；不 import `processcontrol`/`processhost`，不组装 args/env/path。旧版本 projection 不虚构 coverage 为 cmakeBuild/testRun。

- [ ] **Step 4：运行 Protocol/Client 全套**

```powershell
pnpm check:coverage-generated
pnpm check:protocol-generated
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
pnpm --filter @unit-test-ide/coverage-schema test
pnpm --filter @unit-test-ide/coverage-models test
pnpm --filter @unit-test-ide/test-client test
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session ./apps/test-service/internal/server -count=1
```

- [ ] **Step 5：提交 Protocol vertical slice**

```powershell
git add apps/test-service/internal/protocol apps/test-service/internal/session apps/test-service/internal/server packages/test-client tools/service-probe/src/probe.ts tools/service-probe/src/probe.test.ts
git commit -m "feat: expose protocol v1.4 coverage runs"
```

### Task 3：Runtime composition、queued/running recovery 与 startup cleanup

**文件：**

- 创建：`apps/test-service/internal/runtime/coverage_runtime.go`
- 修改：`apps/test-service/internal/runtime/runtime.go`
- 修改：`apps/test-service/internal/runtime/runtime_test.go`
- 修改：`apps/test-service/internal/runtime/data_dir.go`
- 修改：`apps/test-service/internal/runtime/data_dir_test.go`
- 修改：`apps/test-service/internal/taskstore/recovery.go`
- 创建：`apps/test-service/internal/taskstore/recovery_coverage_test.go`
- 修改：`apps/test-service/internal/taskstore/queued_builds.go`
- 修改：`apps/test-service/internal/taskstore/queued_builds_test.go`
- 创建：`apps/test-service/internal/coveragerun/resume.go`
- 创建：`apps/test-service/internal/coveragerun/resume_test.go`
- 创建：`apps/test-service/internal/coveragecollect/startup_cleanup.go`
- 创建：`apps/test-service/internal/coveragecollect/startup_cleanup_test.go`
- 创建：`apps/test-service/internal/coveragereport/startup_cleanup.go`
- 创建：`apps/test-service/internal/coveragereport/startup_cleanup_test.go`

- [ ] **Step 1：写出 dependency rollback/recovery/cleanup 失败测试**

覆盖：

- open order 与 reverse close rollback；
- bundle missing/tampered capability downgrade；
- queued request reload 后重新验证 config generation/profile/toolchain/CMake/bundle/Catalog；
- stale queued request terminal infrastructure failure；
- running → interrupted + unavailable/service_restarted；
- completed item result 保留、未完成 not_run/service_restarted；
- report staging/raw profile 不复用；
- report staging cleanup 与 raw profile cleanup 共享 owner/published-state 判断，失败或重启产生的孤儿不得被误发布；
- startup cleanup only orphan owned data；
- published artifact/active owner/ordinary build 不删除；
- cleanup cancel/timeout/idempotence；
- resumed plan uses fresh runtime boundary/targets, not persisted `ProcessSpec`。

- [ ] **Step 2：运行 Runtime/recovery tests 并确认失败**

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/internal/taskstore ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragereport -run 'Coverage|Recovery|Resume|Cleanup|Rollback' -count=1
```

- [ ] **Step 3：实现 runtime composition/recovery**

Runtime 顺序：data root → stores/artifacts → Workspace/CMake/Toolchain → coverage bundle → build/test/coverage services → Session。Bundle unavailable 只移除对应 capability；普通 build/test 服务仍可启动。

- [ ] **Step 4：运行全套/race**

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/internal/taskstore ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragereport -count=1
go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragereport -count=1
```

- [ ] **Step 5：提交 runtime/recovery**

```powershell
git add apps/test-service/internal/runtime apps/test-service/internal/taskstore apps/test-service/internal/coveragerun apps/test-service/internal/coveragecollect apps/test-service/internal/coveragereport
git commit -m "feat: recover coverage run state"
```

### Task 4：Deterministic coverage fixture 与 service-probe E2E

**文件：**

- 创建：`tools/service-probe/src/coverage-fixture.ts`
- 创建：`tools/service-probe/src/coverage-fixture.test.ts`
- 创建：`testdata/coverage/CMakeLists.txt`
- 创建：`testdata/coverage/include/calculator.h`
- 创建：`testdata/coverage/src/calculator.cpp`
- 创建：`testdata/coverage/tests/cpputest_tests.cpp`
- 创建：`testdata/coverage/tests/unity_tests.c`
- 创建：`testdata/coverage/expected/common-summary.json`
- 创建：`testdata/coverage/expected/source-lines.json`
- 修改：`tools/service-probe/src/probe.test.ts`
- 修改：`tools/service-probe/package.json`
- 修改：`apps/test-service/cmd/cmake-fixture/main.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main_test.go`

- [ ] **Step 1：写出 fixture/report/replay 失败测试**

覆盖：

- CppUTest/Unity pass + assertion failure；
- deterministic branches/line counts；
- selected test changes expected coverage；
- report tool provenance；
- JSON/JUnit/HTML read/validate；
- repeated fixture digest identical；
- source digest/stale behavior；
- disconnect cursor replay；
- cancel before collection；
- crash/timeout partial；
- normal missing profile unavailable；
- no runtime network。

- [ ] **Step 2：运行 fixture/probe tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm test:e2e
```

- [ ] **Step 3：实现 portable deterministic fixture**

Fixture source 使用明确 executable line/branch，避免 compiler-specific undefined behavior。Common Golden 只比较公共 metric；tool-specific metadata 只验证 driver/version contract。

- [ ] **Step 4：运行 local supported E2E**

```powershell
pnpm prepare:coverage-bundle
pnpm --filter @unit-test-ide/service-probe test
pnpm test:e2e
git diff --check
```

- [ ] **Step 5：提交 deterministic coverage E2E**

```powershell
git add tools/service-probe testdata/coverage apps/test-service/cmd/cmake-fixture
git commit -m "test: exercise coverage reports end to end"
```

### Task 5：Native coverage matrix、security regression 与 Hosted CI

**文件：**

- 创建：`tools/service-probe/src/native-coverage.ts`
- 创建：`tools/service-probe/src/native-coverage.test.ts`
- 创建：`tools/service-probe/src/native-coverage-report.ts`
- 修改：`tools/service-probe/src/native-run.ts`
- 修改：`tools/service-probe/package.json`
- 修改：`.github/workflows/foundation.yml`
- 修改：`package.json`

- [ ] **Step 1：写出 matrix/report/security 失败测试**

Matrix：

```text
Windows x64: clang-cl + llvm-profdata + llvm-cov
Linux x64:   GCC + gcov + bundled gcovr
Linux x64:   Clang + llvm-profdata + llvm-cov
```

每个配置运行 CppUTest 与 Unity，并覆盖 available/assertion/partial/cancel/missing profile/version mismatch/stale executable。Security fixture 逐字段注入 command/args/env/cwd/driver/python/script/plugin/template，断言 process starter 未调用。

- [ ] **Step 2：运行 native tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm test:e2e:native
```

- [ ] **Step 3：接入 Hosted CI**

Windows job prepare Windows bundle、验证 clang-cl LLVM version match；Ubuntu job prepare Linux bundle、分别运行 GCC/Clang。进入 service execution 前启用 no-network guard。上传 bounded `coverage-native-report-<platform>.json` 与 JSON/JUnit/HTML sample artifact，不上传 raw profile/native path/token。

- [ ] **Step 4：运行 static/security/platform gates**

```powershell
pnpm check:coverage-generated
pnpm check:protocol-generated
pnpm test:coverage-bundle
pnpm --filter @unit-test-ide/service-probe test
go vet ./apps/test-service/...
git diff --check
```

- [ ] **Step 5：提交 Hosted coverage matrix**

```powershell
git add tools/service-probe .github/workflows/foundation.yml package.json
git commit -m "test: verify native coverage matrix"
```

### Task 6：完整 Phase 5 门禁、评审与状态收尾

**文件：**

- 修改：`docs/superpowers/plans/2026-08-03-phase5-coverage-implementation-index.md`
- 修改：本计划及 Phase 5A–5E checkbox
- 修改：`docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- 创建：`docs/reports/phase5-coverage-validation.md`

- [ ] **Step 1：运行完整本地门禁**

```powershell
pnpm prepare:coverage-bundle
pnpm check:coverage-bundle
pnpm check:coverage-generated
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
pnpm test:e2e:native
go vet ./apps/test-service/...
git diff --check
```

- [ ] **Step 2：运行 Linux cross-build/static gate**

```powershell
$env:GOOS='linux'
$env:GOARCH='amd64'
$env:CGO_ENABLED='0'
go build ./apps/test-service/...
go vet ./apps/test-service/...
```

- [ ] **Step 3：完成四项独立评审**

1. Protocol → `ProcessSpec` injection review；
2. Python/gcovr bundle/import/network review；
3. profile/parser/path/limit review；
4. HTML/XML escaping/CSP/artifact transaction review。

发现问题必须先新增 regression test、修复并重跑适用全套门禁。

- [ ] **Step 4：核验 Hosted CI 与双远端**

固定 Windows/Linux run URL、commit SHA、bundle/report digest 和 artifact retention。确认：

```powershell
git ls-remote github refs/heads/codex/workspace-cmake-toolchains
git ls-remote origin refs/heads/codex/workspace-cmake-toolchains
```

- [ ] **Step 5：提交 Phase 5 completion**

```powershell
git add docs
git commit -m "docs: complete phase 5 coverage pipeline"
git push github codex/workspace-cmake-toolchains
git push origin codex/workspace-cmake-toolchains
```

## Phase 5 完成检查

- [ ] Protocol v1.4/Coverage JSON v1/Workspace config v3
- [ ] Offline Python 3.14.6/gcovr 8.6 bundle
- [ ] 独立 coverage build 与 instrumentation verification
- [ ] LLVM/GCC collection 与 deterministic normalization
- [ ] JSON/JUnit/HTML artifact pipeline
- [ ] TestRun/CoverageRun/Task outcome 分层
- [ ] cancel/timeout/reconnect/restart recovery
- [ ] Windows clang-cl、Linux GCC、Linux Clang native matrix
- [ ] CppUTest/Unity coverage
- [ ] 完整 `pnpm verify`、race/vet、Hosted CI
- [ ] 四项独立安全/架构评审
- [ ] GitHub/Gitee 同一绿色 commit
