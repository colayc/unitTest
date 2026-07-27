# Prepared Process durable lease 所有权设计

日期：2026-07-27
状态：待书面审阅
适用范围：Phase 3A Task 3、Publisher failure ownership architecture reset 的增量修复

## 1. 背景

现有 Task Manager 在 `ProcessFactory.Prepare` 返回 `ManagedProcess` 后，直到 Task/Step 进入 `running` 的同一个 `Store.Apply` 才写入 `ProcessLease`。

正常路径中，该顺序可以把以下状态原子提交：

- Task：`queued -> running`；
- 当前 Step：`pending -> running`；
- event：`task.started`；
- lease：当前 Process 的 recovery identity。

但是 cancel、timeout 或 shutdown 可以在 `Prepare` 阻塞期间先被 execution decision 接受。此时 `Prepare` 仍可能返回 non-nil Process，Manager 会接管该 Process 并进入 cleanup，但 Task 仍为 `queued`，尚未执行原来的 running mutation，也就没有 durable lease。

如果随后另一个已排队命令触发 Publisher circuit，Manager 会对该 Prepared Process 执行 bounded `Terminate/Close`。当 cleanup 失败时，只有两种旧选择：

1. 删除 active owner，让 Shutdown 收敛，但没有 lease 可供 restart recovery 查找；
2. 保留 active owner，避免丢失 Process，但 Manager 无法宣称 cleanup 已完成。

前者违反 Process ownership 与 restart recovery 不变量；后者不能满足 Publisher circuit 正常 cleanup 后的有界收敛目标。

## 2. 已确认决策

采用两阶段 lease：

1. `Prepare` 返回 non-nil Process 后，Manager 立即接管 Process；
2. 在任何后续 decision/error/circuit 分支之前，先为当前非 terminal Task 持久化 lease；
3. 首 Step 此时允许 Task 保持 `queued`；
4. pre-lease mutation 不改变 Task/Step 状态，不写 event，不调用 Publisher；
5. 后续 running mutation不再负责首次创建 lease；
6. `Start` 后仍使用现有 `UpdateLease` 更新可能变化的 Process identity；
7. 只有 durable lease 已存在时，circuit cleanup 的 Close error 才能释放 active owner并交给 restart recovery。

用户已明确接受：`queued` Task 可以在内部持有 recovery lease，但客户端仍只看到 `queued`，不会提前收到 `task.started`。

## 3. 目标

- 每个 non-nil Prepared Process 在进入后续可失败边界前都有明确 owner。
- Publisher/Store circuit cleanup 失败时，restart recovery 可以通过 durable lease 找回 Process identity。
- 保持 cancel、timeout、shutdown 的 first-cause-wins。
- 保持 `task.started` 与 Task/Step running transition 的既有可见语义。
- 保持 Manager 在拥有 durable recovery handoff 时有界收敛。
- 不修改 Protocol、Store schema、Publisher、ProcessFactory 或 Store interface。

## 4. 非目标

- 不新增第二套 recovery journal。
- 不自动恢复已经 panic 的 Publisher。
- 不改变客户端事件类型或顺序。
- 不实现 Task 4 的 recovery replay、Step events 或 Step artifact registry。
- 不改变 Windows、Linux 或工具链适配器设计。
- 不保证“Store 无法写入 lease且本地 cleanup 同时永久失败”时 Manager 可以虚假完成 cleanup。

## 5. 核心不变量

### 5.1 Process ownership

从 `Prepare` 返回 non-nil Process 开始，到 Process cleanup 成功或 restart recovery 接管为止，必须至少满足一项：

1. Manager `activeTask` 持有 Process owner；
2. durable `ProcessLease` 已存在，restart recovery 能找到 Process identity。

不允许存在“Process 仍可能存活、无 active owner、无 durable lease”的状态。

### 5.2 Task 可见状态

pre-lease mutation 只建立 recovery identity：

- Task status 保持当前值；
- 首 Step 通常仍为 `queued`；
- 后续 Step 之间 Task 可以保持 `running`；
- Step status 不变；
- `LastSequence` 不增加；
- 不写 durable event；
- 不调用 Publisher。

因此客户端不会把“Process 已 Prepared”误认为“命令已 Start”。

### 5.3 Circuit handoff

circuit cleanup 的 Close error 只有在 `leasePersisted=true` 时才能：

- 停止 execution decision、timer 和 watcher；
- 删除 Manager active owner；
- 保持 durable Task nonterminal；
- 保留 durable lease；
- 由 restart recovery 完成 Process cleanup 与 Task reconciliation。

当 `leasePersisted=false` 时，删除 active owner是不合法的。

## 6. 组件与状态

### 6.1 `activeTask`

为当前 Process 增加 command-loop-owned 状态：

```go
leasePersisted bool
```

其语义只对应 `current.process`：

- 设置新 Process 时重置为 `false`；
- pre-lease mutation commit 后设置为 `true`；
- Process 被成功清理并释放、或 lease 被 terminal mutation 删除后重置为 `false`；
- 切换到下一 Step 的新 Process 时重新开始一轮。

不得用 Task status、`process != nil` 或 `recoveryRequired` 推断 lease 是否已持久化。

### 6.2 Store

复用现有：

```go
Store.Apply(context.Context, Mutation)
```

pre-lease mutation：

```go
Mutation{
    Task:     current.task,
    Expected: current.task.Status,
    PutLease: &lease,
}
```

不包含：

- Step mutations；
- event drafts；
- artifact；
- `DeleteLease`；
- Task status transition。

现有 SQLite Store 已允许 nonterminal Task 写 lease；`ActiveLeases` 已包含 `queued`、`running` 和 `cancelling` Task，因此不需要 schema 或 migration。

## 7. 正常数据流

### 7.1 `Prepare`

`startNextStep` 的新顺序：

1. 检查 execution decision；已有 cause 时不调用 `Prepare`。
2. 调用 `ProcessFactory.Prepare`。
3. 若返回 non-nil Process，立即把 Process 写入 `activeTask`，并把 `leasePersisted` 重置为 `false`。
4. 读取 `process.Lease()`。
5. 补齐 `TaskID` 与 `ServiceInstanceID`。
6. 用 pre-lease mutation 写入 durable lease。
7. commit 成功后更新 `current.task` 并设置 `leasePersisted=true`。
8. 再检查 execution decision 与 Prepare error。
9. 只有仍可继续时，执行 Task/Step running mutation。
10. 调用 `Start`。
11. `Start` 后继续使用 `UpdateLease` 更新 Process identity。

### 7.2 running mutation

running mutation继续原子提交：

- 首 Step：Task `queued -> running`；
- 当前 Step：`pending -> running`；
- `task.started` event（仅首 Step）；
- 当前 Step metadata。

它不再承担首次 `PutLease`，因为 Process recovery identity 已在前一 transaction 中提交。

### 7.3 multi-step

每个 Step 的 Process 独立执行两阶段 lease：

1. 上一个 Process cleanup 与 lease 删除完成；
2. 下一 Step `Prepare`；
3. 为新的 Process 写入 pre-lease；
4. 进行下一 Step running mutation；
5. `Start` 后更新 lease。

不得把上一个 Step 的 `leasePersisted` 状态沿用到新 Process。

## 8. decision 与 Prepare error

### 8.1 先到的 cancel、timeout、shutdown

如果 decision 在 `Prepare` 返回前已接受：

- non-nil Process 仍必须先完成 pre-lease；
- 不调用 `Start`；
- outcome 保持原 first-cause；
- 进入 bounded `Terminate/Close`；
- cleanup 与 terminal persistence 成功后删除 lease。

pre-lease 写入不得把 first-cause 改为 `infrastructure_failed`。

### 8.2 `Prepare` 返回 error

- `process == nil`：沿用既有 pending-Step infrastructure failure 处理。
- `process != nil`：Manager 必须先接管 Process并尝试 pre-lease，然后进入 cleanup；不得因为 error 而忽略 non-nil Process。

如果 execution decision 已有更早原因，Prepare error 不覆盖它。

## 9. pre-lease Store failure

### 9.1 `ErrConflict`

- 保持 task-local；
- 不调用 `Start`；
- `leasePersisted` 保持 `false`；
- Manager 保留 Process owner并执行 bounded cleanup；
- 不把 conflict 误判为 Store circuit。

### 9.2 `ErrStorageUnavailable`

- 触发 Store circuit；
- 不调用 `Start`；
- `leasePersisted` 保持 `false`；
- Manager 保留 Process owner并执行 bounded cleanup；
- 不伪造 durable terminal state或 lease。

### 9.3 其他错误

非法 lease或 Process adapter contract violation按 infrastructure failure 处理，不得继续 `Start`。只在错误明确表示 Store 不可用时触发 Store circuit。

### 9.4 lease 未提交且 cleanup 失败

当 durable lease 不存在且本地 cleanup 也失败：

- 不删除 active owner；
- 不宣称 restart recovery 已接管；
- 不写虚假 terminal state；
- 后续显式 Shutdown 可以触发受控 cleanup retry；
- `Shutdown(ctx)` 可以按 caller deadline 返回；
- Manager 的 `stopped` 只有在 Process ownership 真正释放后才能关闭。

这是“不丢 Process owner”优先于“无条件完成 Shutdown”的 fail-safe 选择。若未来要求该双重故障下也能重启恢复，必须另行设计独立于主 Store 的 durable recovery journal。

## 10. circuit Close error

### 10.1 durable lease 已存在

当 `circuitFailed() || recoveryRequired` 且 Close 返回 error：

- 不调用 `finishAfterCloseFailure`；
- 不写 Store；
- 不创建 artifact；
- 不调用 Publisher；
- 不重复 Close；
- 停止内存 watchers；
- 删除 active owner；
- 保留 durable Task 与 lease给 restart recovery。

### 10.2 durable lease 不存在

- 不得执行 recovery handoff；
- 保留 active owner；
- 标记 cleanup attempt 失败；
- 只允许通过现有显式 Shutdown retry 机制再次尝试；普通 timeout、cancel、termination result、process done与其他 cleanup command不得重试已失败的 Close；
- caller context 保持等待上界。

### 10.3 非 circuit Close error

任何非 circuit、非 recovery 的 Close failure 都使用同一 ownership 规则，不再按 `leasePersisted` 分叉：

- 若尚无 first-cause，首次 Close error必须先用 `OutcomeInfrastructureFailed` 固定 outcome，并把对应 pending Step 标为待失败；已有 cancel、timeout或 interrupted cause不被覆盖；
- 只有 first-cause 与 `failPendingStep` 固定后才能对外发布 unhealthy；
- Task 保持 nonterminal，不调用 `finishAfterCloseFailure`，不写 terminal Store mutation、artifact或 Publisher event；
- 保留 active owner并设置 `closeFailed=true`；
- durable lease 已存在时继续保留，并确保 `ActiveLeases` 能观察该 nonterminal Task；durable lease 不存在时也不得释放 active owner；
- `closeFailed=true` 是普通 Close启动路径的 retry gate；timeout、cancel、termination result、process done、Publisher callback和其他普通 command都不得发起第二次 Close；
- 后续显式 `Shutdown(ctx)` 是唯一的同进程 retry入口；
- retry成功后才按已固定的 first-cause完成 terminal persistence，并用同一个原子 mutation设置 `DeleteLease=true`，随后释放 active owner；
- retry再次失败时继续保留 owner/lease与 nonterminal Task，不伪造 terminal success。

若进程在显式 retry 前退出，durable lease 因 Task 仍 nonterminal而继续进入 restart recovery。只有 circuit/recovery handoff 才允许在“Task nonterminal且 durable lease已存在”时把 active owner交给 recovery。

## 11. restart recovery

Runtime 启动时继续按现有顺序：

1. `ActiveLeases` 读取 `queued`、`running`、`cancelling` Task 的 lease；
2. Process runner 按 lease执行 cleanup；
3. `RecoverInterrupted` 把非 terminal Task 标记为 `finished/interrupted`；
4. 删除已恢复 Task 的 lease；
5. 清理无引用 artifact。

本设计不新增 recovery API，只确保 Prepared Process 在需要 handoff 前已经进入现有 recovery input。

## 12. 测试设计

### 12.1 pre-lease 可见性

确定性阻塞 running transition，断言：

- `Prepare` 已返回 non-nil Process；
- Task 仍为 `queued`；
- durable lease 已存在；
- Task/Step 状态未变；
- durable/published events 仍只有 `task.created`；
- `Start` 尚未调用。

### 12.2 first-cause

分别覆盖 cancel、timeout、shutdown 在 `Prepare` 返回前先到：

- pre-lease commit；
- `Start=0`；
- bounded cleanup；
- durable outcome 保持 `cancelled`、`timed_out`、`interrupted`；
- terminal mutation删除 lease。

### 12.3 Publisher circuit handoff

构造 Task A：

- `Prepare` 返回 Process；
- decision 已接受，Task 保持 `queued`；
- pre-lease 已提交。

再由 Task B 触发 Publisher circuit，并令 A 的 Terminate/Close 失败。断言：

- A durable Task 保持 nonterminal；
- A lease仍存在并能由 `ActiveLeases` 返回；
- 无 `task.finished`、artifact或 republish；
- Close 不无界重试；
- Manager active owner安全释放；
- Shutdown 有界收敛。

### 12.4 restart integration

用真实 Store/Runtime recovery 证明：

- queued Task 的 lease会传给 runner cleanup；
- cleanup 后 `RecoverInterrupted` 把 Task 标记为 `finished/interrupted`；
- lease被删除；
- 不需要 Protocol、schema 或新 event。

### 12.5 pre-lease failure

分别注入 `ErrConflict` 与 `ErrStorageUnavailable`：

- `Start=0`；
- 无 durable lease时 active owner不会被 Close error 分支删除；
- 不写 artifact、terminal event或 Publisher；
- Shutdown deadline可控；
- cleanup 后不残留 active owner。

### 12.6 回归

- running Task/Step transition；
- multi-step lease替换；
- `UpdateLease`；
- Publisher committed-create fail-closed；
- Shutdown register-after-sweep；
- queued Cancel；
- Close first-cause与 timeout；
- FIFO command-loop ordering：Close error先于已 claim timeout的 `timeoutCommand`消费，Close仍只调用一次且 Task保持 nonterminal；显式 Shutdown才调用第二次 Close并完成 `timed_out`；
- 同 package内用受控 cause-resolution barrier确定性证明 cause/`failPendingStep`在 unhealthy publication之前固定；外部测试只负责 outcome与 Step状态；
- nil Process；
- transient/repeated conflict；
- normal Close failure：durable lease与 lease-free路径都保留 owner和 nonterminal Task；只有显式 Shutdown Close retry成功后才按原 first-cause/outcome完成，durable路径同时原子删除 lease；
- `Prepare` 返回 non-nil Process与 error、pre-lease成功、首次 Close失败时，断言 queued/nonterminal、无 terminal event/artifact、Manager unhealthy、active owner与 `ActiveLeases` lease仍存在、Close仅一次；
- 上述显式 retry再次失败时仍保持 owner/lease与 nonterminal Task；restart recovery能取得 queued prepared lease并完成 interrupted cleanup；
- focused stress、`-race`、Task 包、Runtime/Session、全 internal 与 `git diff --check`。

## 13. 兼容与范围

保持不变：

- Protocol v1.1；
- Store schema 与 migration；
- `Store`、`Publisher`、`ProcessFactory`、`ManagedProcess` interface；
- 客户端 event 类型、payload 与顺序；
- Windows MSVC、Windows clang-cl、Linux GCC、Linux Clang 工具链设计。

允许修改：

- Task Manager 的 Process/lease lifecycle；
- Task、TaskStore 与 Runtime recovery 的回归测试；
- 中文设计、实施计划与 SDD 报告。

## 14. 完成标准

1. 所有 non-nil Prepared Process 在进入后续 failure boundary 前都有 durable lease或仍由 Manager 持有。
2. `queued` pre-lease 不产生客户端可见 running 状态或新 event。
3. Publisher circuit Close failure 只有在 durable lease 存在时才释放 active owner。
4. restart recovery 能清理 queued Prepared Process。
5. pre-lease Store failure 不启动 Process command、不丢 owner、不伪造 recovery handoff。
6. first-cause、plan-wide timeout 与最终 outcome无回归；所有 normal Close failure都保持 active owner与 nonterminal Task，直到显式 Shutdown Close retry成功；durable lease在 retry成功的 terminal mutation中原子删除。
7. Protocol、interface、schema、Task 4 范围保持不变。
8. focused、stress、race、Task、Runtime/Session、全 internal 与 diff check 全部 fresh PASS。
