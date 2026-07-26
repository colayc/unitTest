# Phase 3：Workspace、CMake 与 Toolchain 实施计划索引

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan set task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 按已确认设计交付 Protocol v1.2、单 workspace Service、CMake/Toolchain discovery、多步骤构建任务、结构化 diagnostics 和 Windows/Linux 真实 E2E。

**架构：** Phase 3 被拆成四个按顺序执行、各自可测试和评审的子计划。前两个计划分别建立通用 Task Engine 与内部 discovery 能力；第三个计划把它们接入 Protocol/TypeScript Client；第四个计划固定 CMake bundle 并完成真实平台矩阵。

**技术栈：** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、Go 1.26.5、SQLite、JSON Schema Draft 2020-12、CMake 4.3.4、GitHub Actions。

## 全局约束

- 基础提交固定为 `f5190ef9230469e913f8f66725c0c46e2936d9bf`，目标分支为 `codex/workspace-cmake-toolchains`。
- 一个 Service 实例固定绑定一个受信任的 workspace root。
- Protocol 永不接受 executable、Shell、原始 args、任意 environment map 或 working directory。
- `packages/protocol-schema` 是 Protocol wire shape 的唯一事实来源；生成文件必须提交并通过漂移检查。
- Protocol v1.0/v1.1 的 Schema、fixtures、生成模型和行为保持兼容。
- 产品运行时不联网；CMake bundle 由构建/发布准备阶段取得并校验。
- 产品不内置 compiler；支持 Windows MSVC、Windows clang-cl、Linux GCC 和 Linux Clang。
- Phase 3 不实现 Code-OSS UI、测试框架执行或覆盖率采集。
- 所有 Markdown 文档使用中文，English technical terms 保留 English 格式。
- 每个实现任务遵循 red-green-refactor TDD，并以聚焦提交结束。

---

## 执行顺序

### 1. Phase 3A：多步骤 Task Engine

计划文件：[2026-07-26-phase3-multistep-task-engine-plan.md](./2026-07-26-phase3-multistep-task-engine-plan.md)

交付：

- 通用 `ExecutionPlan` 与 Step 状态；
- SQLite `tasks` migration 和 `task_steps`；
- simulation 垂直切片迁移到多步骤引擎；
- Step events、输出归属、恢复与通用 artifacts；
- Protocol v1.1 行为不变。

完成门禁：完整 `pnpm verify` 在 Windows 上通过，Ubuntu Hosted CI 通过。

### 2. Phase 3B：Workspace、CMake 与 Toolchain Discovery

计划文件：[2026-07-26-phase3-workspace-cmake-toolchains-plan.md](./2026-07-26-phase3-workspace-cmake-toolchains-plan.md)

依赖：Phase 3A。

交付：

- workspace root、路径边界和配置 Schema；
- CMake resolver、Preset、File API 和 Build Profile；
- MSVC、clang-cl、GCC、Clang Adapter；
- Workspace Inspector 与结构化 diagnostic parser；
- 全部能力保持 Go 内部接口，不开放 Protocol。

完成门禁：Go unit/integration tests、race tests 和既有 `pnpm verify` 通过。

### 3. Phase 3C：Protocol v1.2 与 Build Orchestration

计划文件：[2026-07-26-phase3-protocol-build-orchestration-plan.md](./2026-07-26-phase3-protocol-build-orchestration-plan.md)

依赖：Phase 3A、Phase 3B。

交付：

- Protocol v1.2 Schema 与生成模型；
- `workspace/inspect`、`cmake/targets/list` 和 `cmakeBuild`；
- TypeScript Client 强类型 API；
- Runtime、CLI、Build Coordinator 与 diagnostic events/artifacts；
- v1.0/v1.1 回归保持绿色。

完成门禁：Protocol contract、Go/TypeScript tests、service-probe E2E 和完整 `pnpm verify` 通过。

### 4. Phase 3D：Bundled CMake 与 Native E2E

计划文件：[2026-07-26-phase3-native-e2e-bundling-plan.md](./2026-07-26-phase3-native-e2e-bundling-plan.md)

依赖：Phase 3C。

交付：

- 固定 CMake 4.3.4 Windows/Linux x64 bundle manifest；
- 离线运行所需的 bundle layout 与 license；
- CMake sample workspaces 和 golden diagnostics；
- Windows/MSVC、Windows/clang-cl、Linux/GCC、Linux/Clang 真实构建；
- 固定 GitHub Hosted Runner labels 和 Phase 3 CI 门禁。

完成门禁：Windows 与 Ubuntu Native E2E、完整 `pnpm verify`、Hosted CI 和 `git diff --exit-code` 全部通过。

## 提交与评审边界

- 每份子计划从前一份计划的绿色提交开始。
- 每个 Task 的提交必须只包含该 Task 列出的文件。
- 每份子计划完成后执行一次独立代码评审，再进入下一份。
- Phase 3D 完成后执行全规格覆盖审查、安全边界审查和跨平台 CI 审查。
- 在所有门禁通过前保持聚合 PR 为 Draft。
