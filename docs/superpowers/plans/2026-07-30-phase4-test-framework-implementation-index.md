# Phase 4：测试框架发现与执行实施计划索引

> 实施时必须逐 Task 使用 red-green-refactor TDD，并更新 checkbox；每个 Task 以聚焦提交结束。

**目标：** 按已确认设计交付 Protocol v1.3、CTest-first Test Catalog、CppUTest/CppUMock 与 Unity/CMock Adapter、稳定测试 ID、结构化运行/失败重跑、结果持久化和四工具链 Hosted CI。

**架构：** Phase 4 拆成六个按依赖顺序执行的子计划。Phase 4A 建立 wire/domain/store 基础；4B 建立 CTest Catalog；4C 和 4D 分别实现两个框架；4E 把 discovery/run 接入 Task Engine 与 Protocol；4F 固定真实依赖和跨平台门禁。

**技术栈：** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、Go 1.26.5、SQLite、JSON Schema Draft 2020-12、CMake 4.3.4、CTest JSON v1、CppUTest、Unity、CMock、GitHub Actions。

**设计规格：** [2026-07-30-test-framework-discovery-execution-design.md](../specs/2026-07-30-test-framework-discovery-execution-design.md)

## 全局约束

- 基础提交固定为 `07e01ca33f60c3c209c010b1b8195e72be687b0d`，目标分支为 `codex/workspace-cmake-toolchains`。
- 采用 CTest-first 混合适配；CTest container 是唯一测试程序入口。
- Framework Adapter 只能来自 helper metadata 或 Workspace exact-name enum mapping。
- 未满足 case-level contract 时降级 Opaque container，不猜测、不中断其他 container。
- Protocol 永不接受 executable、command、Shell、raw args、任意 environment、working directory、hook 或 result path。
- Protocol v1.0、v1.1、v1.2 的 Schema、fixtures、生成模型和行为保持兼容。
- `packages/protocol-schema` 是 wire shape 的唯一事实来源；生成文件必须提交并通过 drift check。
- Test ID 只使用逻辑身份，不包含 path、profile、toolchain、compiler 或发现顺序。
- assertion failure 必须有 Adapter evidence，不能由 exit code 单独推断。
- Task outcome 与 TestRun/TestItem outcome 保持分层。
- CppUMock 归入 CppUTest Adapter，CMock 归入 Unity Adapter。
- Phase 4 不实现 coverage、JUnit/HTML export、Code-OSS UI 或 Mock generator。
- 产品运行时不联网下载测试框架；真实依赖下载仅限开发/CI bootstrap，必须固定 revision 和 SHA-256。
- 所有 Markdown 使用中文，English technical terms 保留 English 格式。
- 每份子计划完成后执行独立 diff review、安全边界检查和完整适用门禁。
- 每次开发提交均推送 GitHub 与 Gitee 的同名分支。

---

## 执行顺序

### 1. Phase 4A：Protocol v1.3 与测试领域基础

计划文件：[2026-07-30-phase4-protocol-test-domain-plan.md](./2026-07-30-phase4-protocol-test-domain-plan.md)

交付：

- Workspace config v2；
- Protocol v1.3 Schema、fixtures 和生成模型；
- TypeScript Client 测试领域 API；
- Go Test Domain、稳定 ID、selection 和 Catalog model；
- Catalog SQLite migration 与 repository。

完成门禁：Protocol compatibility、generated drift、Go/TypeScript unit tests 和完整 `pnpm verify` 通过。

### 2. Phase 4B：CTest Catalog 与 Opaque fallback

计划文件：[2026-07-30-phase4-ctest-catalog-plan.md](./2026-07-30-phase4-ctest-catalog-plan.md)

依赖：Phase 4A。

交付：

- CTest JSON v1 parser；
- `CTestExecutionDescriptor` 与 property compatibility；
- Framework Registry；
- Catalog Builder、revision、stale validation；
- Opaque container exact execution；
- Catalog 原子发布。

完成门禁：CTest golden、路径/metadata 安全测试、Catalog unit/integration tests 和 Go race tests 通过。

### 3. Phase 4C：CppUTest/CppUMock Adapter

计划文件：[2026-07-30-phase4-cpputest-adapter-plan.md](./2026-07-30-phase4-cpputest-adapter-plan.md)

依赖：Phase 4B。

交付：

- `-ln` case discovery；
- exact group/case planning；
- streaming result parser；
- ignored、assertion、memory leak 和 CppUMock failure；
- source location、partial result 和 capability degradation。

完成门禁：全部 parser Golden File、chunk/ANSI/CRLF tests、fake process integration 和 race tests 通过。

### 4. Phase 4D：Unity/CMock helper、runner 与 Adapter

计划文件：[2026-07-30-phase4-unity-adapter-plan.md](./2026-07-30-phase4-unity-adapter-plan.md)

依赖：Phase 4B。

交付：

- 产品自有 Go Unity runner generator；
- `sdk/cmake/UnitTestIDE.cmake`；
- deterministic manifest；
- `utide.runner.v1` list/exact-run control protocol；
- Unity/CMock result Adapter；
- helper/manifest/runner contract tests。

完成门禁：generator deterministic、CMake helper smoke、generated C compilation、Adapter parser 和 race tests 通过。

### 5. Phase 4E：Test Coordinator、运行结果与 Protocol 编排

计划文件：[2026-07-30-phase4-test-run-orchestration-plan.md](./2026-07-30-phase4-test-run-orchestration-plan.md)

依赖：Phase 4A–4D。

交付：

- testDiscovery/testRun Task；
- selection/filter/repeat/failedFromRun；
- Service-owned test `ExecutionPlan`；
- TestRun/TestItemResult persistence；
- result-aware Task completion；
- test events、artifacts、取消、超时、重连和重启恢复；
- Protocol Session/Runtime/Client 垂直切片。

完成门禁：Protocol E2E、deterministic framework fixture、recovery、完整 `pnpm verify` 和 race tests 通过。

### 6. Phase 4F：真实框架矩阵、安全与 Hosted CI

计划文件：[2026-07-30-phase4-native-framework-ci-plan.md](./2026-07-30-phase4-native-framework-ci-plan.md)

依赖：Phase 4E。

交付：

- 固定 CppUTest、Unity、CMock 依赖；
- `testdata/frameworks/` 真实工程；
- Windows/MSVC、Windows/clang-cl、Linux/GCC、Linux/Clang；
- pass/fail/skip/Mock/crash/timeout/malformed E2E；
- security regression 和 10,000 item backend benchmark；
- GitHub Hosted CI 报告与最终 Phase 4 状态。

完成门禁：Windows/Linux Hosted CI、完整 `pnpm verify`、native framework report、双远端 SHA 和 `git diff --exit-code` 全部通过。

## 提交与评审边界

- 每份子计划从上一份子计划的绿色提交开始。
- 每个 Task 的提交只包含该 Task 声明的文件；发现必须跨 Task 修改时先更新计划。
- 4C 与 4D 在逻辑上独立，但本分支按 4C → 4D 顺序执行，避免并发修改 Framework Registry。
- 每个计划结束时执行一次独立 diff review，重点检查越权 `ProcessSpec` 来源、错误语义和持久化终态。
- 4F 完成前聚合 PR 保持 Draft。
- Phase 4 只有在 GitHub 与 Gitee 同一分支指向同一绿色 commit 后才标记完成。
