# ADR 0003：Service-owned ExecutionPlan

## 状态

已接受。

## 上下文

Phase 3A 把 Phase 2 的单进程 simulation 执行器扩展为顺序执行 Step 的任务引擎，
但 Protocol v1.1 的公开能力仍然只有受控 simulation。若 Protocol payload 能直接
提供 executable、args、environment、working directory 或 Plan，客户端就可以绕过
Service 对工具、目录和凭据的信任边界。若把完整 Plan 或 environment 持久化到
SQLite，运行时秘密和机器相关路径还会进入持久化历史，并可能在重启后被错误复用。

同时，内部 journal 已经需要记录 `task.step_started`、`task.step_finished` 和带
`stepId` 的 `task.output`。Protocol v1.1 的 event enum 和 client cursor contract
不能因此改变：client 仍按全局 `sequence` 严格检测 gap，simulation 的 wire
Task Snapshot、event 名称和 output payload 必须保持兼容。

## 决策

执行边界固定为：

```text
Protocol payload
  → Runtime typed request
  → Service-owned ExecutionPlan
  → Process Controller
```

Protocol v1.1 `tasks/start` 只接受 `idempotencyKey`、`scenario` 和 `timeoutMs`。
Session 使用 strict decoder 拒绝其他字段，再把结构化 simulation 值传给
`Runtime.StartSimulation`。Runtime 调用 Service 内部的
`NewSimulationStartRequest`，由 Service 根据自身 executable、simulation data
directory 和固定参数构造 `ExecutionPlan`、`ProcessSpec` 及 runtime-only
`ExecutionBoundary`。Manager 在创建 Task 前和启动每个 Step 前校验 Plan 与
boundary；只有通过校验的 Service-owned Plan 才能到达 Process Controller。
Protocol 和 client 不能提交或修改 executable、args、environment、working
directory、Step 或 Plan。

SQLite 保存规范化的结构化 request、Task/Step snapshot、Plan fingerprint、process
lease 和 event journal，但不保存完整 `ExecutionPlan`、`ProcessSpec` 或
environment。environment 只存在于当前 Service 进程持有的 runtime Plan 中，不进入
Task Snapshot、event、artifact 或数据库。服务重启不会根据 SQLite 重新附着旧进程，
也不会复用旧 environment。

simulation 是多步骤引擎的兼容垂直切片。它在内部使用一个 `simulation` Step，并把
Step lifecycle 写入 internal journal；对 Protocol v1.1：

- `task.step_started` 和 `task.step_finished` 投影为相同 `sequence`、相同 `taskId`
  的 compatibility `task.output`；
- compatibility output payload 严格为
  `{"stream":"service","text":"","truncated":false}`；
- internal actual output 可以包含 `stepId`，但 v1.1 wire payload 只保留
  `stream`、`text`、`truncated`；
- event 不被过滤、不重编号，也不建立另一套 cursor domain，因此 client 的
  `event.sequence == lastSequence + 1` gap detection 保持不变；
- malformed 或 ambiguous internal output 必须在 subscription acknowledgement
  之后 fail closed，不能静默跳过 sequence。

Protocol v1.1 的 event enum、Task Snapshot Schema 和 generated artifacts 保持
不变。只有 Phase 3C 的 Protocol v1.2 才公开 build request、build Task kind、
Step snapshot 和 Step event；Phase 3A 不提供 workspace inspect 或 CMake build
API。

安装后的产品 runtime 不依赖 GitHub 或公网。GitHub 只用于源代码托管、PR、CI 和
发布协作；Service 运行时通过本地 Named Pipe 或 Unix Socket 服务客户端，并只启动
由本地 Service 验证的进程。普通产品运行不下载 Plan、toolchain 或命令，也不需要
GitHub token 或网络连接。

## 后果

公开输入与进程执行之间存在不可绕过的 Service ownership 边界；Protocol 演进不会
把命令执行权隐式交给 client。SQLite 可以提供 durable Task、Step 和 event history，
同时避免持久化 environment 和完整机器相关 Plan。v1.1 client 在 success、cancel、
reconnect 与 crash recovery 中继续看到旧 event enum、无 gap/duplicate 的 cursor
及三字段 output。

代价是 queued Task 在 Service 重启后不能仅靠数据库中的 fingerprint 恢复执行；
未来 build Task 必须由 Service 基于当前 workspace、toolchain 和 boundary 重新验证并
重新构造 Plan。面向用户的 build/Step 语义也必须等待 Protocol v1.2，而不能提前泄漏到
v1.1。
