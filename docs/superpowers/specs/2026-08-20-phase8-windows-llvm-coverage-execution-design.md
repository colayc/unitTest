# Phase 8：Windows LLVM Coverage Execution Coordinator 设计

**日期：** 2026-08-20

**状态：** 已确认，实施计划已编写

**目标分支：** `master`

**基础提交：** `831acfc`

## 1. 目标

Phase 8 将 Phase 7 已落盘的 durable queued `CoverageRun` 接到真实执行链路。第一批交付实现共享 `Coverage Execution Coordinator` 与 Windows `clang-cl/llvm-cov` Adapter，使 production Service 能把 queued run 推进到真实 `finished`，生成 Coverage JSON v1、JUnit XML 和单文件 HTML，并由 Code-OSS Extension 通过现有 Protocol v1.4 读取。

本阶段同时冻结 Linux Adapter 接口和共享 Coordinator 边界，但不注册 Linux GCC/Clang 的生产执行实现。后续批次在同一 Coordinator 上分别接入 Linux GCC/gcovr 与 Linux Clang/llvm-cov。

## 2. 已确认决策

1. 采用分阶段交付：共享 Coordinator + Windows LLVM 为第一批，Linux GCC/Clang 为后续批次。
2. Windows coverage 只支持已验证的 `clang-cl`、`llvm-profdata` 和 `llvm-cov`，三者必须来自同一 LLVM installation。
3. 每个 CoverageRun 只创建一个顶层 Task；configure、build、test、collect、normalize、report 和 publish 是该 Task 的 Service-owned continuation。
4. Protocol 不接收 executable、argv、environment、cwd、原生绝对路径或 collector 参数。
5. queued aggregate 已由 Phase 7 原子持久化；本阶段只领取并推进它，不创建第二个 CoverageRun 或 TestRun。
6. raw `.profraw`、`.profdata` 与 LLVM export JSON 只存在于 Task 临时目录，不作为长期 artifact 发布。
7. Coverage JSON v1 是 coverage metric、源码映射和 tool provenance 的唯一事实来源；JUnit 的 testcase outcome 只来自关联 TestRun。
8. assertion failure 不等于 coverage infrastructure failure。测试正常退出并产生完整 profile 时，CoverageRun 可以为 `available`，即使 TestRun 为 `failed`。
9. 本阶段不增加 coverage threshold、增量比较、macOS、MSVC `cl.exe` 原生覆盖率或新的测试框架。
10. 产品运行时不依赖网络、GitHub、Gitee、系统 Python package 或用户提供的 coverage config。

## 3. 总体架构

```text
Protocol v1.4 coverage/runs/start
              │
              ▼
Runtime Coverage Backend
              │ durable queued aggregate
              ▼
Coverage Execution Coordinator
  ├─ acquire build lease
  ├─ generate instrumentation include
  ├─ CMake configure/build
  ├─ execute associated TestRun
  ├─ collect profraw
  ├─ llvm-profdata merge
  ├─ llvm-cov export
  ├─ normalize to Coverage JSON v1
  ├─ render JSON / JUnit / single-file HTML
  └─ atomic publish of run/report/artifacts/events
```

`Runtime` 在 trusted workspace 打开成功后构造 Coordinator，并向它提供已验证的 workspace/build/test/store/ArtifactStore/process dependencies。Session 继续只依赖 `session.CoverageBackend`，不感知具体工具或进程计划。

## 4. 组件边界

### 4.1 `coverageexec.Coordinator`

Coordinator 是 queued CoverageRun 的唯一 production consumer。它负责：

- 按稳定顺序领取 queued run；
- 取得 coverage build identity 对应的独占 lease；
- 构造并推进唯一 execution plan；
- 在每个 continuation wave 前复验 trust、workspace generation、catalog revision、profile、toolchain identity 和 execution boundary；
- 处理取消、timeout、Service restart 与失败收敛；
- 在 publish 阶段提交唯一终态事务。

Coordinator 不解析 LLVM JSON，不拼接任意用户输入的命令参数，也不直接把 native path 写入 Protocol/domain output。

### 4.2 `coverageexec.Planner`

Planner 只接收 Phase 7 已解析并验证的 immutable input。它输出固定步骤、Service-owned directories、instrumentation manifest 与 `ProcessSpec`。Planner 计算的 coverage build identity 包含：

- workspace root identity、project ID 与 project source directory；
- base Build Profile canonical snapshot；
- toolchain/compiler/SDK identity；
- CMake installation、generator 与 configuration；
- LLVM driver identity；
- instrumentation template version 与 digest。

源码内容不进入 build identity，因此相同配置允许增量 build；toolchain、profile、CMake、driver 或 template 变化会生成新的隔离目录。

### 4.3 `coveragellvm.Adapter`

Windows LLVM Adapter 只接受已验证的 `clang-cl` toolchain snapshot 与 capability pin。它负责：

- 生成固定、只读且带 digest 的 CMake instrumentation include；
- 通过 `CMAKE_PROJECT_TOP_LEVEL_INCLUDES` 注入固定 clang-cl compile/link coverage options；
- 为每个测试 invocation 生成唯一、Service-owned `LLVM_PROFILE_FILE` pattern；
- 调用同一 LLVM installation 中的 `llvm-profdata merge`；
- 调用 `llvm-cov export` 输出受限 JSON；
- 验证 profile manifest、binary fingerprint、tool versions 与 LLVM JSON limits；
- 将 LLVM JSON 解析为有界内部 `CoverageSnapshot`。

Adapter 不从 `PATH` 补全工具，不接受 workspace 提供的 LLVM 参数，不读取 workspace 提供的 LLVM config/plugin。

### 4.4 现有共享组件

- `coveragerun`：复用 plan、collector、state、finalize、report 与 process-result 语义，并补齐 production Task Engine 接线。
- `coveragenormalize`：处理 URI、source digest、include/exclude glob、排序、计数上限和跨工具统一指标。
- `coveragemodel/v1`：验证并序列化 canonical Coverage JSON v1。
- `taskstore`：保存 CoverageRun、TestRun、Report metadata、状态和 artifact 关联。
- `artifactstore`：保存最终 Coverage JSON、JUnit XML 和单文件 HTML，并验证 size/digest/closed-set contract。
- `processcontrol`：执行固定 `ProcessSpec`，管理 Windows Job Object、取消和 timeout。

### 4.5 Linux Adapter 接口

共享 Coordinator 只依赖以下逻辑能力，不依赖 LLVM/GCC 具体类型：

- 生成 instrumentation contract；
- 构造受控 collector plan；
- 验证 profile manifest；
- 解析工具输出为 `CoverageSnapshot`；
- 返回工具 provenance 与 completeness evidence。

第一批只注册 Windows LLVM 实现。Linux 平台遇到 queued coverage execution 时必须明确保持不可执行状态并返回稳定 capability/unavailable 语义，不得调用系统工具或生成假报告。

## 5. 执行状态机

Coordinator 使用持久化阶段游标推进：

```text
queued
→ configure
→ build
→ test
→ collect
→ normalize
→ report
→ publish
→ finished
```

每个阶段满足以下约束：

- 输入引用上一个阶段已验证的 immutable output；
- native path 只存在于 Service 内部 execution descriptor；
- 阶段完成后先验证 output identity/digest，再记录游标；
- continuation 重新进入 Task Engine 时再次经过 `ExecutionBoundary`；
- publish 前的中间状态不会向 Protocol 暴露 reportId、summary 或 artifact ID。

Service restart 行为：

- 尚未进入 execution 的 queued run 可以重新领取；
- 已进入 configure 之后的 running run 首期收敛为 `unavailable/service_restarted`；
- 不复用可能不完整的 `.profraw`、`.profdata` 或 LLVM JSON；
- 已完成 publish 事务的 run 保持 immutable，不重复发布。

## 6. 数据流与所有权

1. Runtime resolver 生成 verified immutable request 与 toolchain snapshot。
2. Phase 7 queued backend 原子创建 Task、CoverageRun、TestRun 与初始事件。
3. Coordinator 领取 run，并为 coverage build identity 取得 lease。
4. Planner 创建 Task-owned execution root、instrumentation include 与 descriptor。
5. Build/Test 继续使用已有 build/test coordinator contract；测试选择来自 persisted selection snapshot。
6. Adapter 只从 Task-owned profile root 读取 closed-set raw profile，并生成 bounded LLVM export。
7. Normalizer 读取 parsed snapshot 与 verified workspace sources，生成 Coverage JSON v1。
8. Renderer 从 canonical Coverage JSON 与 persisted TestRun 生成 JUnit/HTML。
9. ArtifactStore 先写入三个 immutable artifacts。
10. taskstore 在单一事务中发布 report metadata、artifact refs、summary、CoverageRun/TestRun/Task 终态和有序 events。
11. publish 成功后清理 raw profile 与 native intermediate files；失败时不发布半成品 report。

## 7. 错误与完成语义

### 7.1 `available`

- configure/build/instrumentation verification 成功；
- 所有正常完成的 invocation 都产生预期 profile；
- merge/export/normalize/report/publish 成功；
- TestRun 可以是 `passed` 或 `failed`。

### 7.2 `partial`

- 至少一个有效 profile 可生成报告；
- crash、test timeout 或其他已记录 test outcome 导致部分 invocation 没有 profile；
- 报告 completeness 必须列出缺失 evidence，不能把缺失计为 uncovered。

### 7.3 `unavailable`

以下任一情况使 Task 为 `infrastructure_failed`，CoverageRun 为 `unavailable`，且不发布 report：

- instrumentation 未生效或 target 缺少 coverage mapping；
- toolchain/driver/binary/profile identity 不匹配；
- 正常退出的 instrumented invocation 缺少预期 profile；
- `llvm-profdata`、`llvm-cov`、parser、normalizer、renderer、ArtifactStore 或终态事务失败；
- Service 在 running 阶段重启。

### 7.4 取消与 timeout

- 用户取消：Task `cancelled`，CoverageRun `cancelled/user_cancelled`。
- Task timeout：Task `timed_out`，CoverageRun `cancelled/task_timed_out`。
- trust loss：立即阻止新的 continuation，并终止当前进程树；不继续 merge/report。

## 8. 安全边界

- Protocol、workspace config 和 test metadata 均不能提供 executable、argv、environment、cwd 或 native output path。
- 工具 executable 必须由 pinned toolchain capability 或 verified bundle capability提供。
- execution roots、profile roots、descriptor、instrumentation include 和 outputs 均为 Service-owned、owner-only、closed-set directories/files。
- 进程启动前后复验 capability identity；不得通过路径 reopen 绕过 retained capability。
- `LLVM_PROFILE_FILE` 只指向 Task-owned profile root，pattern 不含用户文本。
- LLVM JSON parser 使用固定 byte/depth/item/string/count limits，并拒绝 duplicate/unknown fields。
- Coverage source URI 必须为 workspace-relative canonical POSIX path；source digest 来自 verified file content。
- HTML 使用固定 build-time assets、无网络 CSP，不嵌入 native paths、token、环境变量或 raw diagnostics。

## 9. 验证策略

### 9.1 单元测试

- Planner 的固定步骤、参数、环境清理、路径边界、build identity 与 deterministic manifest。
- LLVM Adapter 的 instrumentation template、profile pattern、tool identity、merge/export args 与 JSON limits。
- Coordinator 的阶段转换、幂等领取、取消、timeout、restart、partial/unavailable 和 publish transaction。
- Normalizer/renderer 的 deterministic JSON/JUnit/HTML、source digest、include/exclude 与 redaction。

### 9.2 Coordinator 集成测试

使用受控 fake process runner 和真实 SQLite/ArtifactStore，验证：

- configure → build → test → collect → normalize → report → publish 顺序；
- 每个 continuation 都经过 execution boundary；
- 同一 idempotency key 不重复执行或发布；
- 任一阶段失败只产生一个 canonical 终态；
- publish 前崩溃不留下可查询 report；
- running run 在 restart recovery 中收敛为 `unavailable/service_restarted`。

### 9.3 Windows 真实 smoke

CI 与本机 fixture 使用已验证的 `clang-cl`/`llvm-profdata`/`llvm-cov`：

- 构建最小 CMake project；
- 执行包含 passed 与 failed case 的测试；
- 产生真实 `.profraw` 并 merge/export；
- 验证 Coverage JSON、JUnit、HTML、summary、tool versions、source mapping 与 artifact digest；
- 通过 Protocol v1.4 查询 finished CoverageRun/Report，并由 Code-OSS 打开 HTML artifact；
- 测试运行期间禁用网络并注入 hostile environment，证明执行只依赖 pinned capabilities。

实现后的 smoke 使用仓库 Go 1.26.6 构建 Service，并通过真实 trusted Windows Named Pipe 与 TypeScript Protocol Client v1.4 完成 generation/profile/catalog discovery、coverage start 和有界 finished 轮询。fixture 的一个 case 覆盖 `math.cpp` 的部分 branch，另一个 case 在执行 instrumented code 后正常退出为 assertion failure，因此验收必须同时看到 CoverageRun `available`、TestRun `failed`、非零且含 uncovered branch 的 summary。三种 public artifact ID 必须互异；Client 必须读取全部 chunks 并复核 metadata size/SHA-256/kind，随后使用共享 Coverage JSON v1 decoder、逐字符 strict JUnit XML tokenizer 和 Extension 无网络 HTML viewer adapter。Tokenizer 验证 XML well-formedness、合法 builtin/numeric entity、quoted 且不重复的 attribute、nesting/单根、closed JUnit element/attribute/count schema；DOCTYPE、ENTITY、CDATA、comment 与额外 processing instruction fail-closed。

真实执行前必须建立双层 offline boundary：现有 Node HTTP(S) guard 拦截测试 host API；随机唯一、仅在测试时窗作用于隔离 runner 的 Windows Firewall all-program outbound `Block/Any` rule 在 OS 层覆盖 Service、CMake/Ninja、compiler/linker、fixture test、`llvm-profdata` 与 `llvm-cov` 全子进程树。Rule 必须在 ActiveStore 中复核 direction/action/profile 及 application/address/port closed filters，全部 Firewall profile 必须 enabled；独立 PID watchdog 通过 exclusive readiness marker 证明已锁定 Node PID，再与正常 teardown 和 CI `always()` group cleanup 构成三层撤销。查询权限错误不得当成 rule 不存在；创建、审计或撤销失败均使 required CI 失败，绝不能回退为 Node-only evidence，且任何路径都不得留下专属 firewall state。

本机未形成 verified CMake/clang-cl coverage capability 时，只允许在 native execution 前产生一个精确的 `SKIP: verified clang-cl coverage toolset is unavailable`，`pass` 为 0，且不生成 evidence file；该结果不是 native PASS。Windows CI 设置 `UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=clang-cl`，把相同情况提升为失败。成功证据要等 assertion/viewer、Service shutdown、fixture cleanup 和 offline boundary cleanup 全部结束后才可发布；exclusive 临时文件必须 flush、回读并验证 canonical bytes，最后一步才 atomic rename。任一 assertion、回读或 teardown fault 都必须保持 final report 不存在。`.native-e2e/artifacts/windows/coverage-execution-report.json` 的 strict JSON closed shape 只含 platform/architecture、compiler/driver/collector 版本、run/TestRun outcome、summary、三种 artifact digest/size 与 duration，不含 token、environment、ID、workspace/data/tool path 或 raw profile 名称；workflow 仅在 success 时上传，并继续以 `if-no-files-found: error` 约束缺报告的伪成功。

### 9.4 Linux evidence boundary

第一批 Linux CI 只验证共享 Coordinator、Adapter 接口、Go test/race/vet、Linux cross/runtime static contract 与既有 Unix Socket Service smoke；它不运行 Windows native coverage script，也不上传 `coverage-execution-report.json`。不得把交叉编译、Unix Socket 基础 smoke 或 fake runner 计为 Linux coverage runtime PASS。Linux GCC/Clang Adapter 实现后，分别增加真实 Unix Socket + compiler + collector smoke。

GitHub Actions 及其 artifact 只是开发验收 evidence，GitHub/Gitee 只是源码托管、协作和开发分发渠道。production Runtime 不读取 CI report，也不依赖这些平台或网络完成 coverage execution。

## 10. 完成标准

- trusted Windows production Service 能把 queued CoverageRun 推进到 `finished`。
- Code-OSS 能刷新真实 run、读取 report metadata 并打开真实单文件 HTML。
- Coverage JSON v1、JUnit XML、HTML 与 metadata 的 digest/size/identity 一致。
- 工具 provenance 包含真实 compiler、driver、collector 与版本，不包含 executable path。
- assertion failure 与 coverage infrastructure failure 按本设计独立收敛。
- cancel、timeout、trust loss 与 Service restart 不产生半提交 report。
- 全量 Go/TypeScript 测试、affected race/vet、Windows real smoke 与 Git diff checks 通过。
- GitHub 与 Gitee `master` 指向相同最终提交。

## 11. 后续批次

Phase 8 后续批次按共享 Adapter 接口依次增加：

1. Linux GCC：pinned bundled Python/gcovr + verified `gcov`，串行 test invocation，真实 Linux runtime smoke。
2. Linux Clang：verified `llvm-profdata`/`llvm-cov`，复用 LLVM parser 与 normalizer，真实 Linux runtime smoke。
3. 跨平台完成门禁：Windows LLVM、Linux GCC、Linux Clang 三套报告结果进入同一 Protocol/Code-OSS UX，不改变 Coverage JSON v1 contract。
