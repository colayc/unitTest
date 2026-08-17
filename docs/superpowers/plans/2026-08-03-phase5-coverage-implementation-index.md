# Phase 5：覆盖率与报告管道实施计划索引

> 实施时必须逐 Task 使用 red-green-refactor TDD，并更新 checkbox；每个 Task 以聚焦提交结束。

**目标：** 按已确认设计交付 Protocol v1.4、独立 coverage build、Windows clang-cl/llvm-cov、Linux GCC/gcovr、Linux Clang/llvm-cov、统一 Coverage JSON、JUnit XML、单文件 HTML、恢复语义和真实跨平台 E2E。

**架构：** Phase 5 拆为六个按依赖顺序执行的子计划。5A 建立 wire/domain/store 基础；5B 建立离线 Python/gcovr bundle；5C 建立 coverage build 与 CMake 插桩；5D 实现 LLVM/GCC 采集与规范化；5E 生成报告并完成 durable artifact transaction；5F 接入 Runtime/Protocol 并固定真实平台矩阵。

**技术栈：** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、Go 1.26.5、SQLite、JSON Schema Draft 2020-12、CMake 4.3.4、Python 3.14.6、gcovr 8.6、LLVM source-based coverage、GCC gcov、GitHub Actions。

**设计规格：** [2026-08-03-coverage-report-pipeline-design.md](../specs/2026-08-03-coverage-report-pipeline-design.md)

## 全局约束

- Phase 5 产品代码基线固定为 `26f1aa6f28d42e226f51cce151728922c3313806`；实施从包含本计划的后续文档提交开始，目标分支为 `codex/workspace-cmake-toolchains`。
- coverage build 使用独立 Service-owned directory、manifest identity 和 lock namespace，不复用普通 build tree。
- Windows coverage 只代表 clang-cl/LLVM，不声明为 `cl.exe` coverage。
- Linux GCC 的 driver 为 `gcov`，gcovr 8.6 是固定 processor；Linux Clang 与 Windows clang-cl 使用 `llvm-profdata`/`llvm-cov`。
- Python 3.14.6、gcovr 8.6 及 dependency 在开发/CI prepare 阶段固定并校验；产品运行时不联网、不使用系统 Python/site-packages。
- Linux x64 Python/gcovr bundle 以 glibc 2.28 为最低 ABI 基线，由 digest-pinned `manylinux_2_28` compatible builder 生成；Phase 5 不承诺 musl。
- Protocol 永不接受 executable、Shell、raw args、任意 environment、working directory、hook、collector parameter、Python module/script 或 report template。
- `packages/coverage-schema` 是 Coverage JSON wire shape 的唯一事实来源；其版本独立于 Protocol。
- `packages/protocol-schema` 继续是 Protocol wire shape 的唯一事实来源；generated TypeScript/Go model 必须提交并通过 drift check。
- Coverage metric/provenance 来自统一 Coverage JSON；JUnit testcase outcome 只来自关联 TestRun。
- Phase 5 不实现 percentage threshold、Code-OSS UI、报告比较、源码 decoration 或最终安装包。
- raw `.profraw`、`.profdata`、`.gcda` 和第三方 JSON 只属于当前 Task，不成为公开 artifact。
- assertion failure、partial coverage、coverage infrastructure failure、cancel 和 timeout 必须保持不同领域语义。
- 所有 Markdown 使用中文，English technical terms 保留 English 格式。
- 每个子计划完成后执行独立 diff review、安全边界检查和完整适用门禁。
- 每次开发提交均推送 GitHub 与 Gitee 同名分支。

---

## 执行顺序

### 1. Phase 5A：Coverage contract、Protocol v1.4 与 persistence

计划文件：[2026-08-03-phase5-coverage-contract-domain-plan.md](./2026-08-03-phase5-coverage-contract-domain-plan.md)

交付：

- `packages/coverage-schema` 与 `packages/coverage-models`；
- Workspace config v3 coverage profile；
- Protocol v1.4 Schema、fixtures 和 generated model；
- Go CoverageRun/Report domain；
- SQLite migration 009、repository 和 Task/TestRun/CoverageRun 原子创建；
- TypeScript Client v1.4 API。

完成门禁：Coverage/Protocol schema、generated drift、migration、Go/TypeScript tests、v1.0–v1.3 compatibility 和完整 `pnpm verify` 通过。

### 2. Phase 5B：Offline Python/gcovr bundle

计划文件：[2026-08-03-phase5-coverage-bundle-plan.md](./2026-08-03-phase5-coverage-bundle-plan.md)

依赖：Phase 5A。

交付：

- Python 3.14.6、gcovr 8.6 和 dependency lock；
- Windows x64 embedded bundle 与 Linux x64 runtime bundle；
- manifest、SHA-256、license/NOTICE 和 prepare cache；
- Go bundle resolver/verifier；
- isolated fixed runner、no-network 与 injection tests。

完成门禁：bundle prepare/check、离线 runner、tamper/path boundary、license gate、Windows/Linux smoke 和完整 `pnpm verify` 通过。

### 3. Phase 5C：Coverage build、identity 与 CMake instrumentation

计划文件：[2026-08-03-phase5-coverage-build-plan.md](./2026-08-03-phase5-coverage-build-plan.md)

依赖：Phase 5A、5B 的 bundle capability contract。

交付：

- coverage build identity、manifest、directory lease；
- CMake top-level include template；
- clang-cl/Clang 与 GCC 固定 instrumentation；
- File API/post-build verification；
- Coverage Build Coordinator 与 ExecutionBoundary pin。

完成门禁：identity/lease、template Golden、真实 compiler fixture、普通/coverage directory isolation、tamper negative tests 和 Go race tests 通过。

### 4. Phase 5D：LLVM/GCC collection 与 Coverage JSON normalization

计划文件：[2026-08-03-phase5-coverage-collection-plan.md](./2026-08-03-phase5-coverage-collection-plan.md)

依赖：Phase 5A–5C。

交付：

- LLVM profile allocation、merge、export 与 bounded parser；
- GCC `.gcda` lifecycle、fixed gcovr runner 与 bounded parser；
- Windows/Linux path normalization 与 source digest；
- common line/branch/function summary；
- deterministic Coverage JSON writer。

完成门禁：LLVM/gcovr Golden、malformed/oversized/overflow、path escape、partial/missing profile、deterministic digest 和 race tests 通过。

### 5. Phase 5E：JUnit/HTML renderer 与 artifact terminal transaction

计划文件：[2026-08-03-phase5-coverage-reporting-plan.md](./2026-08-03-phase5-coverage-reporting-plan.md)

依赖：Phase 5A、5D。

交付：

- `packages/report-ui` static asset；
- deterministic JUnit XML；
- CSP-bound single-file HTML；
- `coverage-json`、`junit-xml`、`coverage-html` artifact；
- report staging/validation；
- CoverageRun/TestRun/Task/report terminal transaction 与 fault injection。

完成门禁：escaping/CSP/no-network、deterministic artifact、source digest/stale、artifact ownership、publisher failure 和完整 report tests 通过。

### 6. Phase 5F：Runtime、recovery、native matrix 与 Hosted CI

计划文件：[2026-08-03-phase5-coverage-runtime-ci-plan.md](./2026-08-03-phase5-coverage-runtime-ci-plan.md)

依赖：Phase 5A–5E。

交付：

- CoverageCoordinator 与 Service-owned continuation；
- Runtime/Session/Server/TypeScript Client v1.4 垂直切片；
- queued/running recovery 与 startup cleanup；
- deterministic coverage fixture；
- Windows clang-cl、Linux GCC、Linux Clang 的 CppUTest/Unity E2E；
- Hosted CI report、security review、Phase 5 completion 状态。

完成门禁：Protocol/Service E2E、三 coverage 配置 native matrix、完整 `pnpm verify`、Go race/vet、bundle/report gate、双远端 SHA 和 `git diff --exit-code` 全部通过。

## 提交与评审边界

- 每份子计划从前一份子计划的绿色提交开始。
- 每个 Task 的提交只包含该 Task 声明的文件；发现必须跨 Task 修改时先更新计划。
- 5B 可与 5A 的后半段逻辑独立，但本分支按 5A → 5B 顺序执行，避免 manifest/model 并行漂移。
- 5C 完成前不运行真实 coverage test；5D 完成前不生成产品报告；5E 完成前不开放 Protocol start handler。
- 每个计划结束时执行一次独立 diff review，重点检查 `ProcessSpec` 来源、Python isolation、路径边界、parser 上限和终态 ownership。
- 5F 完成前聚合 PR 保持 Draft。
- Phase 5 只有在 GitHub 与 Gitee 同一分支指向同一绿色 commit 后才标记完成。
