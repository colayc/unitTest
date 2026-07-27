# Publisher failure 下的 Task 所有权与 fail-closed 设计

**日期：** 2026-07-27

**状态：** 已确认

**目标分支：** `codex/workspace-cmake-toolchains`

**设计基线：** `f3dc2bbb25dcc986905a919671f68ed82db56b72`

## 1. 背景

Phase 3A Task 3 已把单进程 Manager 扩展为 service-owned `ExecutionPlan` 的顺序多 Step 执行器，并完成了 plan-wide timeout、Cancel 抢占、first-cause、Store conflict 和 queued cancellation 的多轮修复。

第三轮 scoped review 发现一个仍未闭合的所有权窗口：

1. `Store.Create` 已在 SQLite transaction 中提交 Task、全部 Step snapshots 和 `task.created` event。
2. Manager 随后同步调用内存 `Publisher.Publish`。
3. 如果 Publisher panic，当前实现把它误当作 Store failure 并调用 `tripStorage`。
4. 新 Task 此时尚未加入 `active`，因此 `tripStorage` 看不到它。
5. `start` 的 defer 又会停止并删除临时 decision。

最终会留下一个持久化的 `queued` Task：全部 Steps 为 `pending`、没有 lease、没有 active owner、没有 decision，当前进程内也无法再取消。重启 recovery 最终可以处理它，但不能消除当前进程中的 ownership 缺口。

## 2. 已确认决策

1. 采用 fail-closed 语义。
2. Store 与 Publisher 是两个独立故障域，Publisher panic 不再伪装成 Store failure。
3. `Store.Create` 成功后，Manager 必须先接管 active ownership，再发布 `task.created`。
4. Publisher failure 后，新提交但尚未执行的 Task 必须优先持久化为 terminal；若没有更早的 stop cause，Outcome 为 `infrastructure_failed`。
5. 已接受的 cancel、timeout 或 shutdown 保持 first-cause，不被 Publisher failure 覆盖。
6. terminal event 只写入 durable Store，不再调用已经故障的 Publisher。
7. Manager 进入 unhealthy 并受控清理其他 active tasks。
8. 如果 fail-closed terminal mutation 也遇到真正 Store failure，则升级为 Store circuit，并保留明确的 recovery-required 状态。

## 3. 目标

- 消除 `Store.Create` commit 与 active ownership 之间的 persisted orphan 窗口。
- 保证每个已提交 Task 始终由 active owner、terminal state 或明确的 Store recovery 三者之一负责。
- 保持 plan-wide timeout 和 first-cause-wins。
- Publisher failure 后不启动任何 Process，不写 lease，不创建 artifact。
- durable event journal 保持完整，客户端重连后可以从 Store 重放事件。
- 保持 Manager Shutdown 有界完成。
- 保持 Protocol v1.1、`Publisher`、`ProcessFactory` 和 Store schema 不变。

## 4. 非目标

- 自动重建或热替换已经 panic 的 EventBroker。
- Publisher failure 后继续接受新任务。
- 在当前进程内恢复 live event subscription。
- 修改客户端协议错误类型。
- 提前实现 Task 4 的 Step events、Step artifact registry 或完整 restart recovery。
- 改变 Windows、Linux 或工具链适配器设计。

## 5. 核心不变量

### 5.1 持久化所有权不变量

Task 一旦由 `Store.Create` 成功提交，就必须立即处于以下三种状态之一：

1. Manager `active` 持有同一个 execution decision；
2. Task 已持久化为 `finished`；
3. Store 已真实不可用，Task 被明确交给 restart recovery。

不允许存在“已提交、非 terminal、Store 可用，但无 active owner 和 decision”的状态。

### 5.2 事件事实来源

SQLite event journal 是事件事实来源。Publisher 只是同进程的低延迟 live delivery：

- Store transaction 决定 Task 和 event 是否已发生；
- Publisher panic 不回滚 durable event；
- Publisher failure 后追加的 terminal event 仍写入 Store；
- 当前 Broker 不再可信，因此不再尝试 live publish；
- 服务重启和客户端重连通过 Store replay 重新获得完整事件序列。

### 5.3 first-cause 不变量

Publisher failure 参与统一 execution decision，但不能覆盖更早的停止原因：

| 已接受的更早原因 | 最终 Outcome |
|---|---|
| cancel | `cancelled` |
| plan-wide timeout | `timed_out` |
| shutdown | `interrupted` |
| 无 | `infrastructure_failed` |

Task 尚未启动任何 Step，因此以上路径都把所有 `pending` Steps 标为 `skipped`。

## 6. 故障域

### 6.1 Store circuit

Store circuit 表示 durable state 无法可靠读取或写入：

- Manager 进入 unhealthy；
- active processes 进入 recovery-required cleanup；
- 不伪造 terminal state；
- restart recovery 成为 durable state 的唯一修复者。

### 6.2 Publisher circuit

Publisher circuit 表示内存 live delivery 不再可信，但 Store 仍可能可用：

- Manager 立即停止接受新任务；
- 当前已提交的新 Task 先执行 fail-closed terminal mutation；
- 其他 active tasks 进入受控 process cleanup，并由 restart recovery 完成 durable reconciliation；
- 不再调用 Publisher；
- 对外继续返回经过 sanitization 的 `ErrStorageUnavailable`，避免改变 v1.1 错误面。

Publisher circuit 与 Store circuit 必须分别记录，不能通过一个 `storageFailed` flag 混为同一原因。

## 7. 创建路径与所有权转交

`start` 的顺序固定如下：

1. 校验并 clone `StartRequest`。
2. 创建 execution decision，并在 `Store.Create` 前注册。
3. 调用 `Store.Create` 原子提交 Task、Steps 和 `task.created` event。
4. 如果返回 idempotent existing：
   - 不创建 active owner；
   - 停止并删除临时 decision；
   - 返回 existing Task。
5. 如果创建了新 Task：
   - 立即用同一个 decision 构造 `activeTask`；
   - 加入 `active`；
   - 把 decision ownership 从 start scope 转交给 active scope；
   - 启动唯一的 plan-wide timeout。
6. 调用 `publishAll(task.created)`。
7. 发布成功后才进入 boundary revalidation、`Prepare` 和 `Start`。
8. 发布失败时进入 fail-closed 路径，绝不触达 `ProcessFactory`。

`Store.Create` error、idempotent existing 和尚未完成 ownership transfer 的其他 early return，继续由精确的 `CompareAndDelete` 清理临时 decision。

## 8. fail-closed terminal mutation

Publisher failure 后，Manager 对当前新 Task 执行专用 terminal mutation：

- Expected Task status：`queued`；
- Task status：`finished`；
- Outcome：统一 decision 中的 first-cause；若为空则为 `infrastructure_failed`；
- 全部 `pending` Steps：`skipped`；
- Append durable `task.finished` event；
- 不写 Process lease；
- 不写 artifact；
- 不调用 Publisher。

mutation 成功后：

1. 停止 plan-wide timer 和 waiter；
2. 从 `active` 删除 Task；
3. 精确删除 execution decision；
4. 完成所有已注册的 Cancel callers；
5. 触发 Publisher circuit，清理其他 active tasks；
6. `Start` 返回经过 sanitization 的 `ErrStorageUnavailable`。

### 8.1 terminal conflict

第一次 `ErrConflict` 使用现有 task-local infrastructure terminal retry：

- 只重试一次；
- 不递归；
- 不触发 Store circuit；
- 第二次仍 conflict 时，停止当前内存 owner，并把持久状态明确交给 restart recovery。

### 8.2 terminal Store failure

如果 terminal mutation 返回 `ErrStorageUnavailable`：

- 不宣称 Task 已 finished；
- 升级为 Store circuit；
- 当前 Task 的持久状态保持非 terminal，并明确交给 restart recovery；
- 释放内存 Process/decision ownership时必须遵守现有 recovery-required 不变量；
- `Start` 返回 `ErrStorageUnavailable`。

## 9. 其他 publish 调用点

`publishAll` 只负责调用 Publisher、捕获 panic 并返回失败，不再自行调用 `tripStorage`。

其他阶段发生 Publisher failure 时：

- 已提交的 Task/Step mutation 保持为 durable truth；
- Manager 触发 Publisher circuit；
- 有 Process 的 Task 受控 Terminate/Close；
- 已 terminal 的 Task 正常释放 active ownership；
- 非 terminal Task 留给 restart recovery；
- 不重新发布，不伪造额外成功事件。

创建路径是唯一需要在 Publisher failure 后额外执行 fail-closed terminal mutation的路径，因为它尚未启动 Process，且可以安全地从 `queued` 原子结束。

## 10. 并发与生命周期

- execution decision 在 Task durable commit 前已注册。
- active ownership 在任何 fallible publish 前完成。
- plan-wide timeout 只启动一次，且覆盖 publish、Prepare、Start 和全部 Step。
- Cancel、timeout、shutdown 和 Publisher failure 都通过同一个 decision 仲裁。
- Publisher failure 处理运行在 Manager command loop 内，不新增无界后台 goroutine。
- Cancel waiter fan-out 继续使用 buffered reply channel，不因 caller context 已结束而阻塞。
- Manager Close 不等待 Publisher 恢复，只等待现有 active process cleanup 和 Store close。

## 11. 错误与安全

- 不向协议或日志暴露 panic value、路径、环境变量、token 或命令参数。
- 对外错误保持 `ErrStorageUnavailable`，避免新增 v1.1 error surface。
- durable Task 使用既有 `infrastructure_failed` Outcome 和稳定内部 error code。
- Publisher failure 不改变 simulation Request、Plan、ProcessSpec 或 Boundary。
- 不允许 fail-closed 路径调用任意外部 executable。

## 12. 测试设计

### 12.1 创建事件发布失败

确定性 Publisher panic 测试断言：

- `Store.Create` 已成功提交；
- Task 最终为 `finished/infrastructure_failed`；
- 所有 Steps 为 `skipped`；
- durable event 顺序为 `task.created`、`task.finished`；
- `Prepare=0`、`Start=0`；
- 无 lease、无 artifact；
- Manager unhealthy；
- Manager Close 有界完成。

### 12.2 first-cause

使用阻塞 Publisher 和可控 clock 覆盖：

- Cancel 先于 Publisher failure：最终 `cancelled`；
- timeout 先于 Publisher failure：最终 `timed_out`；
- Publisher failure 先发生：最终 `infrastructure_failed`；
- 后到原因不能改写 terminal Outcome。

### 12.3 Store failure 与 conflict

- fail-closed mutation 第一次 conflict 后成功：只发生一次有限 retry；
- 连续 conflict：进入 task-local recovery，不递归；
- terminal Store unavailable：升级 Store circuit，不伪造 finished；
- 其他 active Task 不被 task-local conflict 错误改写。

### 12.4 生命周期与回归

- idempotent existing 不保留临时 decision；
- Create failure 不保留 decision；
- Publisher failure 后 concurrent Cancel callers 全部有限返回；
- `active`、timer、waiter 和 decision 都被清理；
- round 1–3 的 nil Process、first-cause、deadline、queued Cancel、delivery dedup 和 terminal conflict 测试继续通过。

### 12.5 验证矩阵

至少运行：

```powershell
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure' -count=1
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure' -count=100
go test -race ./apps/test-service/internal/task -run '^(TestManagerPublisherFailure|TestManagerCancellation)' -count=1
go test ./apps/test-service/internal/task -count=1
go test ./apps/test-service/internal/task ./apps/test-service/internal/runtime ./apps/test-service/internal/session -count=1
go test ./apps/test-service/internal/... -count=1
git diff --check
```

## 13. 实施边界

预计只修改：

- `apps/test-service/internal/task/manager.go`
- `apps/test-service/internal/task/manager_execution.go`
- 对应的 Task 测试文件
- Phase 3A Task 3 实施报告和 ledger

不修改：

- Protocol schema 和生成模型；
- Store schema 或 migration；
- Runtime/Session 对外接口；
- `Publisher` 和 `ProcessFactory` 接口；
- Task 4 recovery、Step events 或 artifacts。

## 14. 完成标准

1. 不再存在 Publisher failure 导致的 persisted queued orphan。
2. 每个已提交 Task 满足 active、terminal 或 Store recovery 三选一所有权不变量。
3. Publisher 与 Store circuit 在实现和测试中明确区分。
4. fail-closed 路径保持 first-cause、Step 状态和 durable event 原子性。
5. Publisher failure 不启动 Process，不产生 lease 或 artifact。
6. Manager unhealthy 与 Shutdown 语义可重复、有限且无泄漏。
7. focused、stress、race 和全回归全部通过。
