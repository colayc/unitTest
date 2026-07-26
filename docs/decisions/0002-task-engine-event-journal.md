# ADR 0002：任务引擎与事件日志

## 状态

已接受。

## 上下文

Phase 2 需要在 Windows 和 Linux 上可靠运行受控模拟任务，并支持取消、超时、断线重连、事件重放、历史查询和制品读取。运行中的进程控制需要低延迟的内存状态，而任务快照与事件又必须在服务或客户端中断后可查询。SQLite 若同时充当调度队列和运行状态协调者，会把进程生命周期竞态转移到数据库锁与轮询；完整 Event Sourcing 则会增加快照重建、事件版本迁移和运维复杂度，超出本阶段需要。

## 决策

采用“内存运行时 + SQLite 事务日志”的混合架构。`TaskManager` 是运行状态的唯一写入者，串行处理启动、输出、取消、超时、进程退出和存储故障。SQLite 在同一事务中保存任务快照、追加事件、运行租约和 Artifact metadata，但不作为调度队列；制品字节位于独立的 `artifacts/` 目录。

事件采用至少一次交付。服务以全局单调递增的 `sequence` 建立持久化水位、重放与实时事件之间的无缺口切换，客户端按 `sequence` 去重。同一服务实例内，客户端断线重连后可继续重放；服务重启不会重新附着原进程，所有未终止任务恢复为 `finished/interrupted`。

Windows 使用 Job Object，并设置 `KILL_ON_JOB_CLOSE`；无法安全加入 Job Object 时失败关闭。Linux 使用新的 Process Group/Session，取消时先发送 `SIGTERM`，宽限期后发送 `SIGKILL`，并以 PID 与启动标识共同校验残留进程。取消、超时、主进程退出和服务关闭都必须清理完整进程树。

SQLite 驱动固定为 `modernc.org/sqlite` 1.54.0，并固定 `modernc.org/libc` 1.74.1。这一组合不依赖 CGO，使 Windows 与 Linux 使用同一构建方式和依赖图。

## 后果

运行状态与持久化职责边界清晰：`TaskManager` 决定迁移，SQLite 提供事务一致的历史，EventBroker 提供至少一次交付。客户端必须容忍重复事件并按 `sequence` 去重；服务启动必须先完成租约清理和 `interrupted` 恢复，之后才能开放 IPC。

该方案不提供完整 Event Sourcing 的任意时点重建能力，也不允许多个服务实例通过 SQLite 竞争调度。Phase 2 仅执行五种服务内置 simulation，不接受程序路径、Shell 字符串、任意参数、任意环境变量或任意工作目录；真实构建系统与测试框架由后续阶段接入。
