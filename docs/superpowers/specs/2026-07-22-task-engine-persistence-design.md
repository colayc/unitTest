# Phase 2：任务引擎、进程控制与持久化设计

**日期：** 2026-07-22

**状态：** 已确认

**目标分支：** `codex/task-engine-persistence`

**基础分支：** `codex/foundation-protocol-service`

## 1. 背景

Phase 1 已建立版本化 JSON 协议、TypeScript 客户端、Go 本地服务、per-user IPC、token handshake、能力查询和安全关闭流程，但尚不执行任何工作区代码、构建工具或测试。

Phase 2 在该基础上增加与具体构建系统无关的任务运行底座：任务状态机、跨平台进程树控制、结构化事件、断线重连与重放、SQLite 历史记录和制品元数据。Phase 3 的 CMake、MSVC、GCC 和 Clang 适配器将建立在这些接口之上。

## 2. 已确认的决策

1. Phase 2 采用模拟命令垂直切片，不提前接入 CMake 或真实工具链。
2. 采用“内存运行时 + SQLite 事务日志”的混合架构。
3. 同一服务实例支持客户端断线重连和事件重放。
4. 服务重启后不重新附着或恢复原进程；未完成任务统一转为 `interrupted`。
5. Phase 2 使用独立叠加分支，避免继续扩大 Phase 1 的 Draft PR。

## 3. 目标

- 提交、查询、列举和取消受控模拟任务。
- 对取消和超时实施确定性的完整进程树终止。
- 以稳定状态机区分命令失败、基础设施失败、中断、取消和超时。
- 为实时事件和断线重放提供无永久缺口的全局顺序。
- 使用 SQLite 原子保存任务快照、事件、运行租约和制品元数据。
- 通过服务管理的 ID 安全读取制品。
- 服务启动时清理残留运行状态和临时制品。
- 保持协议 `1.0` 客户端的现有能力可用。

## 4. 非目标

- 任意外部命令或 Shell 字符串执行。
- CMake、MSVC、GCC、Clang 或工具链发现。
- CppUTest、CppUMock、Unity、CMock 或测试结果解析。
- 覆盖率采集与报告 UI。
- Code-OSS 集成。
- 服务重启后恢复原进程执行。
- 用户可配置的历史保留策略。

## 5. 总体架构

```text
TypeScript Test Client
        │ 请求、订阅游标
        ▼
Go Session / Router
        │
        ▼
TaskManager ──────► EventBroker ──────► 已认证客户端
    │
    ├─────────────► ProcessRunner
    │                  ├─ Windows Job Object
    │                  └─ Linux Process Group/Session
    │
    ├─────────────► SQLite TaskStore
    └─────────────► ArtifactStore
```

### 5.1 协议与客户端

`packages/protocol-schema` 继续作为唯一协议事实来源。所有 TypeScript 和 Go 模型均由 Schema 确定性生成，不直接编辑生成文件。

`packages/test-client` 负责：

- 协议版本协商。
- 任务提交、查询、列举和取消。
- 响应与事件的交错处理。
- 记录最后应用的全局事件序号。
- 断线后重新认证、订阅和去重重放事件。

### 5.2 Session / Router

Session 只负责认证、消息校验、协议版本约束和路由。任务不属于连接，Session 不保存任务状态或进程句柄。客户端断开不会取消任务。

### 5.3 TaskManager

`TaskManager` 是任务状态机的唯一写入者。启动、输出、取消、超时、进程退出和存储故障均进入同一个串行命令队列，以消除终止竞态。

TaskManager 只依赖抽象接口：

- `TaskStore`
- `ProcessRunner`
- `EventPublisher`
- `ArtifactStore`
- `Clock`
- `IDGenerator`

时间和 ID 通过接口注入，以支持确定性测试。

### 5.4 ProcessRunner

`ProcessRunner` 接受服务内部构造的 `ProcessSpec`，其中包含可执行文件、参数数组、工作目录、环境变量增量、超时和输出限制。协议层不能构造或传入 `ProcessSpec`。

### 5.5 TaskStore 与 ArtifactStore

SQLite 保存结构化历史和制品元数据；制品字节存放在独立目录。SQLite 不承担进程调度职责。

## 6. 任务模型

任务生命周期状态：

- `queued`
- `running`
- `cancelling`
- `finished`

终止任务在 `outcome` 中记录执行结果：

- `succeeded`
- `command_failed`
- `cancelled`
- `timed_out`
- `interrupted`
- `infrastructure_failed`

Phase 2 不定义 `test_failed`。测试断言失败属于 Phase 4 的测试领域结果，不得由命令退出码或基础设施错误推断。

### 6.1 合法迁移

```text
queued ───────────────► running
  │                        │
  │ 启动前取消             ├─► cancelling ─► finished/cancelled
  │                        ├─► finished/succeeded
  │                        ├─► finished/command_failed
  │                        ├─► finished/timed_out
  │                        └─► finished/infrastructure_failed
  └──────────────────────► finished/cancelled

queued/running/cancelling ──服务重启恢复──► finished/interrupted
```

`finished` 为不可变终止状态。对已完成任务再次取消时返回当前快照，不产生新的状态迁移。

### 6.2 终止竞态

- 用户取消和超时通过同一 TaskManager 队列排序。
- 最先被 TaskManager 接受的终止原因决定最终 `outcome`。
- 因终止操作产生的非零退出码不能覆盖 `cancelled` 或 `timed_out`。
- 进程已自然退出后到达的取消请求不改变结果。
- 进程启动、I/O 或进程管理失败归类为 `infrastructure_failed`。
- 主进程退出后，ProcessRunner 必须先终止或确认 Job/Process Group 中没有剩余后代，再允许任务进入 `finished`。

## 7. 模拟任务

Phase 2 使用当前 Go 服务可执行文件的内部 `--task-fixture` 模式创建确定性子进程。支持的固定场景至少包括：

- 成功退出。
- 非零退出。
- 持续运行直至取消或超时。
- 生成孙进程并持续运行。
- 向 stdout 和 stderr 输出确定性内容。

模拟任务参数由服务根据枚举场景构造。客户端不能提供程序路径、Shell 文本、任意参数、任意环境变量或任意工作目录。

## 8. 协议设计

### 8.1 版本协商

Phase 2 引入协议 `1.1`：

- 新客户端先使用 `1.1` envelope 发起 handshake，并声明支持的协议版本。
- 服务选择双方支持的最高版本并返回 `negotiatedProtocolVersion`。
- 如果旧服务以 `UNSUPPORTED_PROTOCOL` 拒绝 `1.1`，新客户端使用完全符合现有 Schema、且不包含 `1.1` 字段的 `1.0` handshake 重试。
- `1.0` 客户端仍可执行原有 handshake、`capabilities/get` 和 `shutdown`；新服务按协商版本返回严格的 `1.0` 响应形状，不向 `additionalProperties: false` 的旧模型添加字段。
- handshake 完成后的每条消息必须使用已协商版本。
- `1.1` 方法在较低协商版本下返回 `PROTOCOL_FEATURE_UNAVAILABLE`。

### 8.2 请求方法

- `tasks/start`
- `tasks/get`
- `tasks/list`
- `tasks/cancel`
- `events/subscribe`
- `artifacts/list`
- `artifacts/read`

`tasks/start` 包含客户端生成的幂等键。相同幂等键和相同规范化请求返回原任务；相同幂等键与不同请求返回 `IDEMPOTENCY_CONFLICT`。

`tasks/list` 和 `artifacts/list` 使用不透明游标与服务限制的页大小。`artifacts/read` 接收 `artifactId`、字节偏移和请求长度，返回受消息尺寸约束的 Base64 分块及 `nextOffset`/`eof`；客户端从元数据获得总大小和 SHA-256，并在组装完成后校验。

### 8.3 事件

事件 envelope 增加全局 `sequence`、事件类型、关联任务 ID、时间戳和版本化 payload。Phase 2 事件类型包括：

- `task.created`
- `task.started`
- `task.output`
- `task.cancellation_requested`
- `task.finished`
- `artifact.created`

每个事件先在 SQLite 事务中获得全局单调递增的 `sequence`，提交后才能广播。`task.output` 使用固定上限的分块，整个消息仍必须遵守协议最大消息尺寸。

### 8.4 重连与重放

`events/subscribe` 接收客户端最后成功应用的 `sequence`。服务按以下步骤建立无缺口订阅：

1. 在 EventBroker 注册带上限的临时缓冲订阅。
2. 读取 SQLite 已提交事件的当前水位。
3. 重放游标之后至水位之间的事件。
4. 接续缓冲区中水位之后的新事件。
5. 切换为实时发送。

事件交付为“至少一次”。客户端必须按 `sequence` 去重，不能假设断线前后的严格一次交付。

## 9. 跨平台进程树控制

### 9.1 Windows

- 通过原生 `CreateProcess` 创建暂停进程。
- 在恢复主线程前，将进程加入专属 Job Object。
- Job Object 设置 `KILL_ON_JOB_CLOSE`。
- 取消和超时使用 `TerminateJobObject` 终止整个进程树。
- 不能安全加入 Job Object 时启动失败并记录为 `infrastructure_failed`，不得降级为无进程树保护的执行。

### 9.2 Linux

- 在新的 Process Group/Session 中启动进程。
- 设置父进程死亡信号，并记录 PID、PGID 和进程启动标识。
- Linux 将父进程死亡信号绑定到创建子进程的 OS thread；Process Host 和 target 必须由专用的 locked OS thread 启动，并在对应子进程完成 `Wait`、确认已回收后才释放该 thread。
- Process Host 继承 control pipe 后，Linux CLI 必须创建带 `CLOEXEC`、`O_NONBLOCK` 且已注册 Go netpoll 的专用 fd 副本，再把该副本的所有权交给 Host 状态机。不得假设从另一个 goroutine 对普通 blocking fd 调用 `Close` 一定能中断正在执行的 `read`。
- 取消时先向整个进程组发送 `SIGTERM`。
- 超过内部宽限期后向整个进程组发送 `SIGKILL`。
- 启动清理只有在 PID 与启动标识仍匹配时才终止残留进程，避免 PID 重用造成误杀。

Linux 的保证面向由服务启动、未主动逃离所属 Session/Process Group 的任务进程。需要对抗恶意逃逸的执行沙箱不属于本阶段范围。

### 9.3 安全启动屏障

任务启动顺序为：

1. 事务性创建 `queued` 任务和 `task.created` 事件。
2. 创建暂停或受启动屏障约束的进程。
3. 事务性写入运行租约、`running` 状态和 `task.started` 事件。
4. 允许进程真正执行。

任一步失败都必须关闭进程控制对象，并尽可能写入 `finished/infrastructure_failed`。

## 10. SQLite 持久化

SQLite 使用单写入者模型，并启用 WAL、外键约束和事务。使用不依赖 CGO 的 SQLite 驱动，以保持 Windows/Linux 构建和交叉验证简单；具体依赖及固定版本在实施计划中确定。

核心表：

- `tasks`：任务 ID、幂等键、规范化请求摘要、状态、结果、时间戳和最后事件序号。
- `task_events`：全局序号、事件 ID、任务 ID、类型、发生时间、payload 版本和 JSON 数据。
- `artifacts`：制品 ID、任务 ID、类型、相对路径、MIME 类型、大小、SHA-256 和完成状态。
- `process_leases`：任务 ID、平台进程标识、启动标识和服务实例 ID。
- `schema_migrations`：版本、摘要和应用时间。

每次状态迁移在一个事务中完成：

1. 校验当前状态。
2. 更新任务快照。
3. 追加事件。
4. 更新任务的最后事件序号。
5. 更新相关租约或制品元数据。

事务失败时不广播事件。关键持久化在任务运行期间失败时，服务终止对应进程树、拒绝新任务并进入不健康状态，避免产生不可追踪的后台进程。

## 11. 制品

- SQLite 与制品根目录分离。
- 客户端只能传入 `artifactId`，不能传入磁盘路径。
- 服务从数据库读取相对路径，规范化后确认目标仍在制品根目录内。
- 必须防止通过 `..`、绝对路径、重解析点或符号链接越界访问。
- 文件先写入同目录临时文件，再计算摘要、刷新并原子重命名。
- 文件完成后才将制品元数据事务性标记为可读。

Phase 2 为每个完成任务生成一个确定性的 JSON 摘要制品，以验证制品创建、列举、读取和摘要校验链路。真实日志、JUnit、HTML 和覆盖率制品在后续阶段接入。

## 12. 启动恢复与关闭

服务启动顺序：

1. 验证 per-user 数据目录权限。
2. 取得单实例锁。
3. 打开 SQLite 并执行版本化迁移。
4. 校验并清理身份仍匹配的残留进程租约。
5. 将所有 `queued`、`running` 和 `cancelling` 任务改为 `finished/interrupted`，追加终止事件。
6. 删除未完成制品及无主临时文件。
7. 恢复完成后才创建可连接的 IPC 端点。

服务正常关闭时停止接收新任务，取消所有活动任务，等待进程树退出并完成持久化，然后关闭数据库和 IPC。

Phase 2 不自动删除已完成历史。保留策略和用户管理入口在 Phase 7 设计。

## 13. 错误、安全与流量控制

新增稳定错误码：

- `INVALID_TASK_SPEC`
- `TASK_NOT_FOUND`
- `IDEMPOTENCY_CONFLICT`
- `EVENT_CURSOR_INVALID`
- `ARTIFACT_NOT_FOUND`
- `ARTIFACT_NOT_READY`
- `STORAGE_UNAVAILABLE`
- `SERVICE_UNHEALTHY`
- `SUBSCRIBER_TOO_SLOW`
- `PROTOCOL_FEATURE_UNAVAILABLE`

规则：

- 稳定错误码与用户显示文本分离。
- 数据库、磁盘和进程管理错误不得归类为 `command_failed` 或测试失败。
- 每个订阅者使用有界队列；慢客户端不能阻塞 TaskManager。
- 队列溢出时尽力发送可重试错误并断开，客户端从最后序号恢复。
- 输出事件限制单块大小、单任务总量和速率；超限后截断并产生结构化诊断。
- 日志和错误不得包含 token、完整环境变量、SQLite 连接信息或内部绝对路径。
- 所有 Phase 2 方法仍受 Phase 1 的 per-user IPC、token handshake 和消息大小限制保护。
- `capabilities/get` 明确报告任务、重放、持久化和平台进程树控制能力；客户端不得仅根据操作系统推断。

## 14. 测试策略

### 14.1 单元测试

- 状态机的全部合法与非法迁移。
- 取消、超时和进程退出竞态。
- 幂等任务创建与冲突检测。
- 事件序号、快照更新和事务回滚。
- 迁移摘要和重复执行。
- 制品路径规范化、越界与链接检查。
- 输出分块、总量限制和订阅队列溢出。

### 14.2 协议契约测试

- Schema 和生成模型保持确定性。
- 所有新请求、响应、错误和事件提供 Golden File。
- `1.0` 客户端仍能 handshake、查询能力和关闭服务。
- `1.1` 客户端可以处理响应与事件交错。
- 重放中的重复事件可按序号安全去重。

### 14.3 集成与故障注入

- 成功退出、非零退出、持续运行、stdout/stderr 和孙进程场景。
- Windows Job Object 终止孙进程。
- Linux Process Group 的 `SIGTERM`/`SIGKILL` 升级与孙进程清理。
- 客户端断开期间任务继续运行。
- 重放与实时切换期间没有永久事件缺口。
- 服务重启把活动任务改为 `interrupted`。
- 进程启动、SQLite 写入和制品提交失败均正确归类。
- 慢订阅者断开不影响任务执行和其他客户端。

Windows 和 Linux GitHub Actions 均运行协议、Go、TypeScript 和端到端测试。

## 15. 验收场景

1. 启动一个输出日志、生成孙进程并持续运行的模拟任务。
2. 客户端观察到连续的有序事件后主动断开。
3. 任务保持运行，客户端使用最后应用序号重新连接。
4. 客户端恢复全部缺失事件且无永久缺口。
5. 客户端发出取消请求。
6. 服务终止完整进程树，任务到达 `finished/cancelled`。
7. SQLite 中的任务快照、事件历史和最后序号一致。
8. 摘要制品可按 ID 读取并通过 SHA-256 校验。
9. 服务重启后任务仍可查询，但不会恢复执行。
10. 整个流程不产生 `test_failed`。

## 16. 完成门禁

- `pnpm check:protocol-generated`
- `pnpm build`
- `pnpm test`
- `pnpm test:e2e`
- Windows 与 Linux CI 全部通过。
- 工作树无生成差异或未提交文件。
- Phase 2 代码评审确认没有绕过 ProcessRunner、TaskManager 或 ArtifactStore 边界。

## 17. 后续阶段接口

Phase 3 通过内部任务规划接口把 CMake 和编译器调用转换为 `ProcessSpec`，复用本设计的任务状态、事件、取消、超时、持久化和制品能力。协议不暴露平台命令行细节，UI 领域模型也不依赖 Go、SQLite 或本机路径。
