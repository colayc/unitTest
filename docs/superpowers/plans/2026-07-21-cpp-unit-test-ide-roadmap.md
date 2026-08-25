# C/C++ 单元测试 IDE 实施路线图

**源设计规格：**`2026-07-18-code-oss-cpp-unit-test-ide-design.md`

**已选架构：**采用 Code-OSS 桌面客户端、TypeScript 内置扩展和独立打包的 Go 测试执行服务。TypeScript 客户端与 Go 服务通过每用户本地 IPC 端点交换版本化 JSON 消息。未来的浏览器客户端复用 TypeScript UI 与协议，但通过 HTTPS/WebSocket 连接远程服务。

**首个正式版本的平台矩阵：**

- Windows/MSVC：用于常规构建、测试和诊断。
- Windows/clang-cl：配合 llvm-profdata/llvm-cov 用于覆盖率构建。
- Linux/GCC：配合 gcovr。
- Linux/Clang：配合 llvm-cov。

## 拆分为独立计划的原因

产品包含可独立评审的协议、进程控制、工具链、框架、覆盖率、IDE、持久化、打包和发布工程子系统。以下每个阶段都以可运行、可测试的软件为终点，并在执行前获得各自的详细实施计划。

## 目标仓库结构

```text
apps/
├── code-oss-extension/       TypeScript built-in extension
└── test-service/             Go execution service and platform adapters
packages/
├── protocol-schema/          Versioned JSON Schema source of truth
├── protocol-models/          Generated TypeScript protocol models
├── test-client/              Transport-independent TypeScript client
├── testing-domain/           Shared client-side test state and projections
└── report-ui/                Web-compatible report and coverage UI
tools/
├── protocol-gen/             Deterministic TypeScript/Go model generation
├── service-probe/            End-to-end local-service diagnostic client
└── code-oss-patches/         Recorded product-shell patch manifest
testdata/
├── toolchains/               Golden compiler and CMake outputs
├── frameworks/               CppUTest and Unity sample projects
└── coverage/                 gcovr and llvm-cov fixtures
docs/
├── decisions/                Architecture decision records
└── superpowers/plans/        Approved implementation plans
```

## 阶段顺序

### Phase 1：仓库、协议 v1 与本地服务垂直切片

交付一个 TypeScript 探针：启动 Go 服务，通过 Windows Named Pipe 或 Linux Unix Socket 进行身份验证，读取服务能力，请求关闭服务，并验证两端生成的协议模型。

验收标准：

- 确定性的协议生成产生干净的 Git 输出。
- 契约测试、Go 单元测试、TypeScript 单元测试和 Windows/Linux 端到端测试均通过。
- 未经身份验证和格式错误的消息均以稳定的错误码被拒绝。

详细计划：`docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`

### Phase 2：任务引擎、进程控制与持久化

加入任务状态机、取消、超时、完整进程树终止、结构化事件排序、重连与重放、SQLite 历史记录、制品元数据、原子写入和启动清理。

依赖：Phase 1 的协议与传输层。

验收标准：可启动、观测和取消一条模拟命令；客户端重连后可恢复，并能记录而不会被误分类为测试失败。

### Phase 3：工作区、CMake 与工具链适配器

实现工作区配置 Schema、CMake Presets 发现、工具链能力检测、参数数组命令规划，以及结构化的 MSVC/GCC/Clang 诊断。

依赖：Phase 2 的任务/进程引擎。

验收标准：Windows/MSVC、Windows/clang-cl、Linux/GCC 和 Linux/Clang 示例项目可通过服务完成配置和构建，并产生基准诊断结果。

状态：已于 2026-07-29 完成。固定 Hosted CI 运行 [`30464939729`](https://github.com/colayc/unitTest/actions/runs/30464939729) 已验证四种 compiler family 的 generated fallback、Preset、诊断、取消/超时、路径边界与恢复场景，并通过 Protocol compatibility、完整 `pnpm verify` 和双平台报告门禁。

### Phase 4：测试框架发现与执行

实现 CTest 容器发现、CppUTest/CppUMock 和 Unity/CMock 适配器、稳定的测试 ID、过滤、单个/全部/失败重跑、结果解析和源码位置。

依赖：Phase 3 的构建适配器。

验收标准：两个框架都能在支持矩阵上发现并运行单个测试，包括失败、崩溃、跳过、超时和格式错误输出的测试夹具。

状态：已于 2026-08-01 完成。提交 `69dd3d8` 已完成 CTest、CppUTest/CppUMock、Unity/CMock、Protocol v1.3、TestRun recovery 和 deterministic E2E，并同步到 GitHub 与 Gitee。

### Phase 5：覆盖率与报告管道

实现 Windows clang-cl/llvm-cov、Linux GCC/gcovr 和 Linux Clang/llvm-cov 适配器；规范化路径；合并 profile；导出版本化 JSON、JUnit XML 和 HTML 制品；并强制执行 LLVM 工具版本兼容性。

依赖：Phase 4 的框架执行能力。

验收标准：在所有已声明的覆盖率配置中，源码、行、分支和汇总覆盖率均可复现，且每份报告都显示实际工具链。

详细设计：`docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md`

状态：设计已于 2026-08-03 确认，详细实施计划已完成，待确认后执行 Phase 5A。

实施计划索引：`docs/superpowers/plans/2026-08-03-phase5-coverage-implementation-index.md`

### Phase 6：Code-OSS 外壳与内置扩展

创建精简的 Code-OSS fork、产品品牌、更新/遥测配置、扩展源策略、内置扩展注册、服务生命周期管理器、Testing API 集成、工作区信任门禁，以及诊断/源码导航。

依赖：稳定的 Phase 1 协议，以及 Phase 3/4 的执行 API。

验收标准：桌面 IDE 能打开示例工作区，未受信任时拒绝执行，受信任后发现测试，并通过外部 Go 服务运行测试。

首个 Vertical Slice 状态：已实现。独立 Code-OSS Extension package 已完成 Workspace Trust gate、真实 Go Service lifecycle、Windows Named Pipe/Linux Unix Socket platform contract、现有 Protocol Client 接入、`workspace/inspect` 命令，以及由生产 `activate()` 成功完成 marker 驱动的 Extension Development Host smoke harness。Windows Named Pipe 已由本地 runtime smoke 实际验证；Code-OSS 信任丢失通过 Extension Host teardown/deactivation path 清理 Service；Linux Unix Socket evidence 继续由 Linux CI 提供。

Phase 6B 状态：实现与本地 Windows 验证已就绪，等待 Hosted CI 双平台实跑后确认完成。共享 TypeScript `TestingApiAdapter` 已提供 trusted 单根 workspace 的 Refresh、稳定 Test Item tree、默认 Run Profile、root/container/item selection、test event 投影与最终 TestRun 收敛；真实 smoke 覆盖 `workspace/inspect → discoverTests → catalog → runTests → item result`。相同 `catalogRevision` 保留 Test Item object identity，10,000 item 基准记录环境、elapsed time 和零 replacement。Windows job 验证 Named Pipe，Linux job 必须实际验证 Unix Socket；Linux evidence 不得由 Windows 或 cross-compile 代替。两个 job 都只有在真实 Service smoke 通过后才构成 Phase 6B runtime evidence，未提供 `CODE_OSS_EXECUTABLE` 的 host harness 仅记为明确 SKIP，不记 PASS。

详细设计：`docs/superpowers/specs/2026-08-16-phase6-code-oss-extension-design.md`

实施计划：`docs/superpowers/plans/2026-08-16-phase6-code-oss-extension-plan.md`

后续子阶段：Coverage UI、source decoration、完整 Code-OSS fork/branding、built-in registration 与 desktop packaging 尚未实现；Phase 6B Testing API 集成就绪不表示这些 UI 与产品打包工作已完成。

### Phase 7：产品测试 UX、Mock 配置、历史记录与报告

加入运行配置、过滤器、失败详情、进度/日志流、Mock/Stub 配置、覆盖率树和源码装饰、本地历史记录、制品浏览，以及兼容 Web 的报告视图。

当前进度：Coverage Report Extension 的 command/controller/viewer 与 Go Service production `coverage/runs/start`、report persistence、共享 execution coordinator 已接通。Windows required-PASS CI 现在运行真实 `clang-cl/llvm-profdata/llvm-cov` Named Pipe + Protocol v1.4 smoke：关联 TestRun 必须为 `failed`、CoverageRun 必须为 `available`，Coverage JSON/JUnit/HTML 三种 artifact 必须完成 chunk、size、SHA-256、shared decoder、strict XML tokenizer 与无网络 viewer 校验。smoke 首先用独立 Go preflight 复用 production discovery evidence 与 LLVM retained-identity/digest verifier；unavailable non-required 在任何 WFP/Service/native side effect 前精确 SKIP，required 则失败，verified 才进入 guardian。执行期间由 Windows Filtering Platform (WFP) dynamic session guardian 覆盖整个 native 子进程树的 outbound connect；guardian 只有在 owner identity、IPv4/IPv6 filter audit 和 release/exit 路径都闭合后才算通过。成功 evidence 改为闭集 strict JSON，只保留 `schemaVersion/outcome/reason/toolchainDigest/guardianOutcome/filterAuditOutcome/startedAt/finishedAt`，仍然在 assertion、Service/fixture/offline teardown 与临时文件回读全部完成后才原子发布。旧 PowerShell/marker 逻辑不再参与生产 boundary，只保留一次性 legacy residue cleanup：仅删除已知历史 rule group 与 canonical residue，遇到未知 state、live legacy creator 或无法证明归属时直接 fail-closed。未准备 verified toolset 的本机运行只能明确 SKIP，不能记为 PASS；Windows Hosted job 缺 toolset、WFP boundary 或 report 会失败，失败 smoke 不上传 coverage evidence。Linux job 继续运行真实 Unix Socket Service smoke，但不运行或上传 native coverage report；Linux GCC/Clang coverage execution 尚未实现，cross-compile 不构成 native PASS。

GitHub Actions 的 PASS/report 是开发验收证据，GitHub 与 Gitee 只承担源码托管、协作和开发分发；production Service 的 coverage execution 不访问这两个平台或其他网络服务。Windows required-PASS gate 完成不表示 Linux coverage 或完整 Phase 7 UX 已完成。

依赖：Phase 5 的报告能力和 Phase 6 的扩展集成。

验收标准：完整的主要用户旅程无需终端命令即可完成，并在 10,000 个测试项时保持响应。

### Phase 8：打包、更新、许可与加固

产出已签名或已校验摘要的 Windows/Linux 安装包，捆绑 Go 服务和获准工具，锁定兼容组件版本，保护每用户 IPC/token 处理，脱敏日志，校验制品路径，生成第三方声明，并测试回滚。

依赖：Phase 1-7。

验收标准：干净机器安装、升级、回滚、卸载清理、许可证审查和高风险安全门禁均通过。

当前状态：Phase 8 的确定性 staging、Windows MSIX/Linux AppImage 摘要校验、许可审计、安装/升级/回滚/卸载 smoke 与 fail-closed 发布资格门禁已实现。Phase 8 仍未完成：本地 Windows 工作区不能替代原生 Linux AppImage 与干净机器 CI 证据，测试签名路径也不能替代正式 Windows 签名证据；第三方声明仍须附加最终许可证/法务审查记录。资格报告会明确保留这些缺失原因，只有同一源码提交的双平台原生证据、摘要、签名要求、生命周期和许可记录全部闭合时才允许发布制品。

### Phase 9：完整矩阵、性能与发布资格确认

运行单元、契约、集成、端到端、故障注入、性能和 Code-OSS 上游合并回归测试套件。为发现、过滤、内存、启动、取消和报告生成建立经过硬件确认的基线。

依赖：所有前置阶段。

验收标准：源设计规格中的每项发布门禁和成功标准均有自动化结果或明确签署的人工记录。

## 跨阶段规则

- 每个阶段在编写生产代码前都以详细计划和失败测试开始。
- JSON Schema 始终是协议的唯一事实来源；生成文件会提交，并在 CI 中检查漂移。
- 服务绝不接受 Shell 命令字符串；适配器输出可执行文件与参数数组。
- 平台特定代码位于 Go 或 TypeScript 接口之后，并配有 Windows/Linux 实现和契约测试。
- 工作区在客户端边界使用 URI；原生路径仅在服务内部解析。
- 每个任务以聚焦的提交和独立的验证命令结束。
- 浏览器/云端能力是兼容性约束，不是首个正式版本的交付物。

## 源设计规格覆盖索引

| 规格领域 | 负责阶段 |
|---|---|
| 产品外壳、品牌、扩展策略与上游维护 | 6, 8, 9 |
| 协议、传输、版本兼容性与数据模型 | 1, 2 |
| 任务状态、错误、取消、重连、历史记录与制品 | 2 |
| 工作区、CMake、编译器、诊断与配置 | 3 |
| CppUTest/CppUMock、Unity/CMock、发现、ID、Mock/Stub | 4, 7 |
| Windows/Linux 覆盖率、源码映射、JUnit、HTML 与报告 | 5, 7 |
| 工作区信任、IPC 安全、路径校验、签名与脱敏 | 1, 6, 8 |
| 浏览器/云端兼容性约束 | 1, 6 |
| 单元、契约、集成、端到端、性能与发布门禁 | 每个阶段，第 9 阶段汇总 |
