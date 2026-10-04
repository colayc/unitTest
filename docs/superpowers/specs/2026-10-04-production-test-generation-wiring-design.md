# Phase 10：自动生成测试用例生产接通设计

**日期：**2026-10-04  
**状态：**已确认  
**前置设计：**

- `docs/superpowers/specs/2026-09-27-offline-coverage-guided-test-generation-design.md`
- `docs/superpowers/specs/2026-09-28-managed-generated-tests-and-detailed-coverage-design.md`

## 1. 背景与当前缺口

Phase 10 已经具备 Protocol v1.5/v1.6、覆盖率明细、离线 Clang 分析、安全子集、输入求解、测试渲染、隔离验证、原子发布、受管测试和 Code-OSS 交互模块。然而这些模块尚未组成正式产品可用的生产后端：

- `unit-test-service` 启动时没有向 runtime 提供 `GenerationFactory`；
- runtime 只有测试夹具能够构造 generation driver，生产代码没有对应实现；
- 基础 generation service 的 `ManagedTestsReady()` 固定返回 `false`；
- 正式包把工具放在 `app/bundles/*`，现有服务仍存在从服务可执行文件目录推导 bundle 的假设；
- 因此发布流水线能够验证包结构和既有能力，但安装后的产品不会广告可用的测试生成功能。

本设计只补齐这些生产接通缺口，不重新定义已经确认的生成算法、受管文件格式或覆盖率模型。

## 2. 决策与目标

采用**现有 Go 本地服务内的原生生产接通方案**。不增加独立服务、不使用云端或本地 LLM，也不引入 Mock/Stub、CppUMock 或 CMock。

首版生产能力必须作为一个完整闭环交付：

1. 完全离线分析 C/C++ 源码；
2. 支持 CMake 项目及 Unity、CppUTest；
3. 可按文件、函数和 coverage gap 生成；
4. 候选必须真实编译、执行并证明目标覆盖率提升；
5. 生成文件可重复维护，保留手写内容并处理三方冲突；
6. 用户可查看项目、文件、函数、行和分支覆盖率；
7. 用户明确确认前，workspace 保持零写入；
8. 安装包内所有依赖经过固定摘要和闭集清单验证。

## 3. 未采用方案

### 3.1 只接通 Protocol v1.5

该方案可以较快提供“生成—预览—接受”，但不能满足长期维护、三方冲突处理以及文件/函数级覆盖率要求。正式产品不得以此作为完成状态。

### 3.2 新增独立 generation daemon

独立进程会增加安装、权限、生命周期、IPC 和攻击面，也违背“Code-OSS + 内置扩展 + 现有本地服务”的产品边界，因此不采用。

### 3.3 由扩展直接组装命令或写测试文件

扩展不是 workspace identity、coverage evidence 或写入资格的权威来源。它只能提交服务端签发的闭集 ID、展示结果和收集用户确认。

## 4. 包内资源路径契约

### 4.1 单一解析者

内置扩展根据已验证的安装布局解析以下绝对目录：

- `bundles/cmake`；
- `bundles/coverage`；
- `bundles/testgen`；
- 现有 framework bundle 或 runtime 所需的产品固定目录。

扩展把这些目录作为专用启动参数传给 `unit-test-service`。服务不得通过 `PATH`、环境变量、当前工作目录或用户配置寻找替代工具。

### 4.2 正式模式与开发模式

正式模式只接受位于产品安装根目录内的规范化直接目录。路径穿越、symlink、junction、reparse point、UNC/device path、大小写别名和越界路径全部拒绝。

开发模式可以显式传入测试工具目录，但必须继续执行同一 manifest、READY、摘要、平台和许可清单校验；开发覆盖不能在正式模式生效。

### 4.3 启动校验

服务在打开 generation backend 前完成：

1. 规范化并锚定 bundle root；
2. 验证闭集文件清单、大小和 SHA-256；
3. 验证平台、架构、版本和资源目录；
4. 验证必需许可证文件；
5. 固定工具文件身份，避免校验后替换；
6. 将验证后的只读对象交给 production driver。

任何一步失败都只产生固定、去路径化原因码，且不开放生成能力。

## 5. 生产组合边界

### 5.1 ProductionGenerationFactory

runtime 新增产品自有的 production composition。它只接收经过验证的窄接口和对象：

- task store 与 workspace snapshot authority；
- coverage backend、CoverageDetailIndex 和 source-attested baseline；
- 固定 testgen、coverage、CMake 和 framework bundle；
- process owner、budget、artifact 和 snapshot verifier；
- 原子 publisher 与 managed registry/review store。

IPC 请求不能提供可执行文件、argv、插件、输出目录、源字节或任意命令。

### 5.2 ProductionGenerationDriver

driver 实现现有 `GenerationDriver` 和 managed production 所需的窄接口，按固定顺序执行：

1. 解析服务端签发的 file/function/gap ID；
2. 绑定当前 workspace generation、编译输入、目标和 coverage baseline；
3. 使用已验证 Clang bundle 生成稳定 IR；
4. 对安全可生成子集执行有界求解；
5. 生成确定性的 Unity 或 CppUTest 测试；
6. 在服务拥有的隔离根执行 configure、compile/link、候选测试、回归测试和 coverage；
7. 验证断言证据、process ownership、artifact provenance 和 coverage delta；
8. 去重并最小化候选；
9. 形成摘要绑定的 preview 或 managed review。

无法证明安全、无法建立断言、执行失败、覆盖率无提升或任一指标回退的候选都不得进入预览。

### 5.3 受管 v1.6 组合

production factory 在基础 generation service 上构造 `ManagedRuntimeProvider`，接通：

- current CoverageDetailIndex；
- durable ManagedTestRegistry；
- review draft 和三方比较；
- selected-output validator；
- baseline 与 receipt verifier；
- managed atomic publisher；
- managed production driver。

基础 service 不再代表正式产品的最终 generation backend；正式 server 接收完成组合后的 provider。

## 6. 原子能力门禁

正式产品以完整 v1.6 为 readiness 边界。

只有以下条件全部为真时，backend 才同时广告兼容的 v1.5 和完整 v1.6 generation capability：

- workspace 为受信任的单根目录；
- coverage backend 和当前 CoverageDetailIndex 可用；
- testgen、coverage、CMake 和 framework bundle 均验证通过；
- production driver、validator、publisher 和 verifier 均 ready；
- managed registry、review store、baseline、receipt 和 managed publisher 均 ready；
- SQLite 迁移和恢复完成。

任一条件失败时不广告 generation capability，不允许降级为残缺的正式 v1.5。既有测试、发现和覆盖率能力仍按各自门禁运行。

## 7. 用户数据流

1. 用户在受信任的单根 workspace 运行测试并取得当前 coverage report。
2. 服务建立项目、文件、函数、行、分支和 coverage-gap 权威索引。
3. 用户从命令或覆盖率树选择文件、函数或 gap；扩展只提交服务端 ID。
4. 服务运行离线分析、求解、渲染和隔离验证。
5. 服务返回可验证候选、覆盖率 before/after/delta、断言来源和计划修改。
6. 扩展展示普通 diff 或受管块三方 review。
7. 用户确认最新 preview/review digest。
8. publisher 原子写入测试文件和允许修改的测试 CMake 文件。
9. 服务重新运行测试和覆盖率，成功后更新 ManagedTestRegistry 和 CoverageDetailIndex。

取消、信任撤销、workspace 切换、服务重启或任一 identity 漂移都会使当前预览失效。

## 8. 文件维护规则

- 一个源文件映射到一个 `tests/generated/..._test.*` 文件；
- 用例和受管块 ID 由稳定语义身份生成，不依赖行号；
- 相同输入重复生成必须得到相同顺序和字节，不追加重复用例；
- 受管块外字节逐字节保留；
- 用户修改受管块后进入 `conflicted`，必须显式三方选择；
- 源函数变化后标记 `stale`，重新验证前不自动更新；
- 源函数消失后标记 `orphaned`，不自动删除；
- 发布崩溃必须可恢复或回滚；重复接受必须幂等。

## 9. 错误、安全和隐私

- 协议、日志和 receipts 不包含源码、生成文件正文、绝对路径、环境变量、token 或无界进程输出；
- 子进程只能由固定计划创建，并继承最小闭集环境；
- 所有阶段受时间、内存、输出、候选数和进程树预算约束；
- snapshot、compile input、toolchain、baseline、artifact 或 process owner 漂移时立即终止；
- publisher 是唯一 workspace 写边界；生产源码永不修改；
- generation 不发起网络请求，工具准备后的 native evidence 运行在断网边界内；
- 面向用户的错误使用稳定原因码和可执行建议，敏感细节只在内存中参与校验。

## 10. 验证策略

### 10.1 本地快速层

- bundle 路径、锚定、篡改和正式/开发模式测试；
- production driver 各阶段顺序、预算、取消和漂移测试；
- runtime/session capability 原子门禁测试；
- managed registry、三方 review、幂等发布和恢复测试；
- Code-OSS 启动参数、能力隐藏、命令、diff 和覆盖率树测试；
- Go race 测试及生成文件一致性检查。

### 10.2 真实集成层

- 使用固定 Clang、CMake、coverage 和 framework bundle；
- 对 Unity 与 CppUTest fixture 完成“基线—生成—编译—执行—覆盖率提升—接受—重新运行”；
- 验证生成结果不包含 Mock/Stub/CppUMock/CMock；
- 验证正式安装布局，无需复制 bundle 或设置 `PATH`。

### 10.3 Hosted native matrix

必须覆盖：

- Windows MSVC + Unity/CppUTest；
- Windows clang-cl + Unity/CppUTest；
- Linux GCC + Unity/CppUTest；
- Linux Clang + Unity/CppUTest。

每个 required block 必须产生路径脱敏、摘要绑定的 generation、execution、coverage、mutation、fault/security 和 performance evidence。静态单元测试不能替代这些结果。

### 10.4 正式包验收

从 release-shaped staged root 启动真实 `Code - OSS.exe` 和内置扩展，验证：

1. 无开发参数、无手工复制 bundle、无系统工具回退；
2. v1.6 capability 可见；
3. 可生成、预览、确认、执行并更新文件/函数覆盖率；
4. 重启后受管状态和 coverage identity 保持一致；
5. 信任撤销后能力立即关闭且不再启动进程或写入。

## 11. 实施批次

1. **资源与门禁：**统一 package bundle roots、启动参数、验证和 capability 原子门禁。
2. **v1.5 生产驱动：**组成分析、求解、渲染、隔离验证和 publisher，但不作为可发布完成点。
3. **v1.6 受管闭环：**构造 ManagedRuntimeProvider，接通 registry、review、selected validation 和 managed publication。
4. **扩展与安装包：**接通正式启动参数、错误展示和 release-shaped smoke。
5. **证据与收紧：**完成本地、race、真实 fixture、hosted native matrix 和发布门禁记录。

每一批必须保持 fail-closed；中间提交不得让正式包广告不完整 generation capability。

## 12. 完成标准

本设计只有在以下条件全部满足时才算实施完成：

- 正式安装包无需手工环境修补即可广告 v1.6；
- 文件、函数和 coverage gap 三种入口均能生成至少一个真实 fixture；
- 候选通过真实编译、执行、断言和 coverage delta 验证；
- 重复生成、手写内容保留、stale、conflicted 和 orphaned 行为通过；
- 一键执行后项目、文件和函数覆盖率可见且一致；
- 八个 native toolchain/framework blocks 全部闭合；
- packaged smoke、fault/security、mutation 和 performance evidence 全部绑定当前提交；
- 没有网络、Mock/Stub、系统工具回退、隐式写入或未验证 bundle；
- 文档不再把基础模块或静态测试描述为“生产自动生成已可用”。

正式 Windows 签名、最终第三方 license/legal 人工审批和 GitHub Release 仍保持为独立、延期处理的发布门禁，不属于本轮代码接通范围。
