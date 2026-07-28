# Close-before-terminalization 所有权设计

## 1. 背景

Phase 3A 已经建立：

- service-owned `ExecutionPlan` 与多 Step Task engine；
- Prepared Process pre-lease；
- Process tree `Terminate`/`Close`；
- Publisher/Store fail-closed；
- 显式 `Shutdown` 才能重试失败的 `Close`；
- restart recovery 通过 durable lease 找回 cleanup ownership；
- v1.1 live/replay compatibility projection。

现有实现仍有一个更广的 ownership 顺序缺口：

1. `Process.Start` 返回 error，或者当前 Step 的 `Process.Done` 返回 result；
2. Manager 立即调用 `finishExecution` 或 `persistSuccessfulStep`；
3. SQLite 先提交 terminal Task、terminal/成功 Step、Artifact/events，并执行 `DeleteLease=true`；
4. Manager 随后才异步调用 `Process.Close`；
5. 如果 `Close` 失败，Task 已经 terminal，或者中间 Step 已经完成且 lease 已删除；
6. 若 Service 在显式 `Shutdown` retry 前退出，restart recovery 看不到 durable lease，cleanup ownership 丢失。

这会产生两个错误的用户可见结果：

- cleanup 尚未确认时，客户端已经看到 `task.finished`、成功 outcome 或 terminal Artifact；
- cleanup 失败后，durable state 无法表达仍需恢复的 Process ownership。

本设计把 Process cleanup 纳入 Task/Step completion transaction 的前置条件。

## 2. 决策

采用统一的 **close-before-terminalization**：

> 只要当前 Task 仍持有 non-nil `ManagedProcess`，Manager 就必须先确认该 Process 的 `Close` 成功，才能提交当前 Step 完成、Task terminal、terminal Artifact/events 或 `DeleteLease=true`。

Process result 先暂存在 `activeTask` 的 runtime-only pending completion 中。Task、Step 和 durable lease 保持原状态，直到 `Close` 成功。

### 2.1 核心顺序

```text
Process result / Start error / Prepare cleanup cause
  -> stage pending completion
  -> Terminate（需要时）
  -> Close
  -> Close success
  -> atomic Step/Task mutation + events/artifact + DeleteLease
  -> next Step 或释放 active owner
```

`Close` failure 时：

```text
Close failure
  -> 固定 first-cause（没有更早 cause 时为 infrastructure_failed）
  -> Manager unhealthy
  -> Task/Step 保持 nonterminal
  -> durable lease 保留
  -> active owner 保留
  -> 仅显式 Shutdown 可在本进程 retry
  -> 或由 restart recovery接管
```

## 3. 目标与非目标

### 3.1 目标

1. 所有 Process completion 路径遵守同一 ownership invariant。
2. Start-error、Prepare-error、intermediate-Step success、final-Step success/failure、cancel、timeout 和 shutdown 使用同一 close-before-persist 顺序。
3. `Close` failure 前后始终至少存在一个 cleanup owner：
   - 当前 Manager 的 active owner；
   - 或 restart recovery 可见的 durable lease。
4. terminal Task、terminal Artifact 和 `task.finished` 只在 cleanup 成功后可见。
5. v1.1 payload、sequence、cursor 和 replay contract 不变。
6. 不新增 Protocol 状态，不新增数据库 migration。

### 3.2 非目标

- 不新增 terminal Task 的独立 cleanup 状态。
- 不让 `ActiveLeases` 返回 finished Task。
- 不把 pending Process result 写入 SQLite。
- 不改变 Process host、Windows Job Object、Linux Process Group 或 recovery probe 接口。
- 不实现后续 Workspace/CMake Toolchain planner。
- 不改变 GitHub/CI/发布架构。

## 4. 全局不变量

### 4.1 Durable ownership invariant

当 `current.process != nil && !current.closeComplete` 时：

- Task 必须保持 `queued`、`running` 或 `cancelling`；
- 对应 durable lease 必须继续存在；
- 不得提交 `DeleteLease=true`；
- 不得发布 terminal Task/Step event；
- 不得创建 terminal Task summary Artifact；
- active owner 不得释放，除非 circuit/recovery handoff 已确认 Task nonterminal 且 durable lease 存在。

### 4.2 Close-before-persistence invariant

以下 mutation 只能在当前 Process 的 `Close` 成功之后执行：

- intermediate Step：`running -> succeeded`；
- final/failed Step：`running/pending -> succeeded/failed/skipped`；
- Task：`queued/running/cancelling -> finished`；
- terminal Artifact 写入；
- `task.step_finished`、`artifact.created`、`task.finished` 写入；
- `DeleteLease=true`。

### 4.3 Failure ownership invariant

normal non-circuit `Close` failure：

- 不修改 durable Task/Step completion；
- 不删除 lease；
- 不释放 active owner；
- 不自动 retry；
- `closeFailed=true`；
- 没有更早 cause 时固定 `OutcomeInfrastructureFailed`；
- 已有 cancel、timeout 或 interrupted first-cause 时保留原 cause。

### 4.4 Crash invariant

Service 在以下任意窗口崩溃时：

- Process result 已到达但尚未 `Close`；
- `Close` 正在执行；
- `Close` 已失败；
- `Close` 已成功但 SQLite completion transaction 尚未提交；

SQLite 中仍保留 nonterminal Task 与 durable lease。restart recovery 使用既有 identity-aware cleanup，并把 Task 保守地完成为 `interrupted`。

pending Process result 是 runtime-only；崩溃后不尝试恢复原 success/command_failed result。

## 5. Runtime 状态模型

`activeTask` 新增 runtime-only pending completion：

```go
type pendingProcessCompletion struct {
	Result      ProcessResult
	FailPending bool
}
```

`activeTask` 增加：

```go
pendingCompletion *pendingProcessCompletion
```

它不进入：

- `Task` domain model；
- SQLite；
- Protocol；
- Artifact；
- event payload。

### 5.1 状态表

| 阶段 | Task/Step durable state | Lease | active owner | pending completion |
|---|---|---|---|---|
| Process 运行中 | queued/running/cancelling | 存在 | 存在 | 无 |
| result/Start error 已到达 | 保持 nonterminal | 存在 | 存在 | 存在 |
| Close 进行中 | 保持 nonterminal | 存在 | 存在 | 存在 |
| Close 首次失败 | 保持 nonterminal | 存在 | 存在 | 存在 |
| Close 成功、DB commit 前 | 保持 nonterminal | 存在 | 存在 | 存在 |
| intermediate commit 成功 | 当前 Step succeeded | 删除 | 存在并准备下一 Step | 清除 |
| terminal commit 成功 | Task finished | 删除 | 释放 | 清除 |

### 5.2 单一 completion 入口

所有持有 Process 的完成路径调用同一个 staging helper，例如：

```go
func (m *Manager) stageProcessCompletion(
	current *activeTask,
	result ProcessResult,
	failPending bool,
)
```

该 helper：

1. 只允许写入一次 pending completion；
2. 保存 `ProcessResult` 与 `failPending`；
3. 设置 `processCompleted=true`；
4. 不修改 Task/Step/lease；
5. 根据 Terminate 状态调用 `maybeStartClose`。

重复的 `Process.Done`、Terminate callback 或 Close callback 不得覆盖第一次 staged result。

## 6. Outcome 与 first-cause

cleanup 是 Task 生命周期的一部分。在 `Close` 成功并提交 completion 之前，Task 仍未完成。

### 6.1 Outcome 计算时点

最终 outcome 在 `Close` 成功后、completion transaction 之前计算：

1. 若 execution signal 已有 cancel、timeout、interrupted 或 infrastructure cause，使用固定 cause；
2. 否则 `terminationFailed` 或 `result.Err != nil` 为 `infrastructure_failed`；
3. 否则 exit code `0` 为 `succeeded`；
4. 其他 exit code 为 `command_failed`。

### 6.2 cleanup failure 优先级

`Close` failure：

- 若已有 cancel、timeout 或 interrupted，保留该 first-cause；
- 若没有更早 cause，将 outcome 固定为 `infrastructure_failed`；
- 原本暂存的 success 或 command_failed 不得掩盖 cleanup failure。

因此：

| Process result | 更早 cause | Close result | 最终 outcome |
|---|---|---|---|
| success | 无 | success | succeeded |
| command failed | 无 | success | command_failed |
| Start error | 无 | success | infrastructure_failed |
| 任意 | cancelled | success/failure | cancelled |
| 任意 | timed_out | success/failure | timed_out |
| success/command failed | 无 | failure，retry 后 success | infrastructure_failed |

### 6.3 cleanup 期间的 Cancel/timeout/Shutdown

在 `Close` completion transaction 前：

- total timeout 继续生效；
- Cancel 仍可成为 first-cause；
- Service `Shutdown` 仍可成为 interrupted cause；
- intermediate Step cleanup 期间出现这些 cause 时，不启动下一 Step；
- `Close` 成功后直接 terminalize 整个 Task；
- `Close` 已失败时，显式 `Shutdown` 只负责授权 retry，不覆盖已固定 cause。

## 7. 各入口的数据流

### 7.1 Intermediate Step success

1. 收到 exit code `0`；
2. flush 当前 Step output；
3. stage result；
4. 保留 running Step 与 lease；
5. `Close` 成功；
6. 若此时无 cancel/timeout/interrupted/infrastructure cause：
   - 原子提交 `StepRunning -> StepSucceeded`；
   - 发布 internal `task.step_finished`；
   - `DeleteLease=true`；
   - 清除当前 Process runtime state；
   - `nextStep++`；
   - Prepare 下一 Step。
7. 若 cleanup 期间出现 cause，跳过后续 Step并完成整个 Task。

### 7.2 Final Step success/command failure

1. flush output；
2. stage result；
3. 保留 running Step、nonterminal Task 与 lease；
4. `Close` 成功；
5. 原子提交：
   - terminal Step mutations；
   - terminal Task；
   - Task summary Artifact；
   - `task.step_finished`；
   - `artifact.created`；
   - `task.finished`；
   - `DeleteLease=true`。
6. 发布 committed events；
7. 释放 active owner。

### 7.3 Start error

`Process.Start` 可能在返回 error 前创建部分系统资源，因此 Process 仍需要 cleanup：

1. 固定 `infrastructure_failed`；
2. stage synthetic `ProcessResult{Err: startErr}`；
3. 执行有界 `Terminate`；
4. Terminate 完成后执行 `Close`；
5. `Close` 成功后才提交 terminal Task/Step/Artifact/events并删除 lease；
6. `Close` 失败则保持 nonterminal Task、lease 与 active owner。

### 7.4 Prepare error + non-nil Process

保留已经实现的 pre-lease 顺序，并改用统一 staged completion：

1. `Prepare` 返回 Process；
2. 持久化 pre-lease；
3. `Prepare` 同时返回 error或已有 execution cause；
4. stage infrastructure/cause completion，必要时 `failPending=true`；
5. Terminate/Close；
6. cleanup 成功后 terminalize。

### 7.5 Process-free failure

当 `current.process == nil` 时，没有 cleanup ownership：

- validation、plan 或 Prepare 返回 nil Process 的 failure 可以立即 terminalize；
- 不需要 staged completion；
- 不需要 durable lease；
- 既有 Publisher/Store fail-closed 规则保持不变。

## 8. Close callback

### 8.1 Close success

`closeResultCommand{err:nil}`：

1. 设置 `closeComplete=true`；
2. 若处于 circuit/recovery handoff，只按既有 recovery规则释放 owner；
3. 否则调用单一 `commitClosedCompletion`；
4. `commitClosedCompletion` 重新读取 execution cause并计算 outcome；
5. intermediate success 或 terminal completion 只能在这里写入。

### 8.2 Close failure

`closeResultCommand{err:non-nil}`：

1. 调用 `recordCloseFailure`；
2. 设置：
   - `closeStarted=false`
   - `closeComplete=false`
   - `closeFailed=true`
3. 不调用 Step/Task persistence；
4. normal path保留 owner；
5. circuit/recovery path只有在 Task nonterminal且 lease durable时才能 handoff；
6. 普通 callback、Cancel、timeout和Process Done不触发 retry。

### 8.3 显式 Shutdown retry

`Shutdown` 是本进程唯一 retry authority：

- 若 `closeFailed=true`，调用 `retryClose`；
- retry success 进入同一个 `commitClosedCompletion`；
- retry failure继续保留 pending completion、lease与 active owner；
- Manager 只有在所有 owner完成或安全 handoff后才能关闭 `stopped`。

## 9. Store/Publisher failure

### 9.1 Close success后 Store commit失败

此时 cleanup 已经成功，但 SQLite mutation 未提交：

- Task/Step仍为 nonterminal；
- durable lease仍存在；
- Manager进入 storage circuit；
- 可以把 owner安全交给 restart recovery；
- recovery cleanup必须保持 identity-aware/idempotent；
- recovery将 Task完成为 `interrupted`。

即使实际 Process已经关闭，重复 cleanup也必须被视为安全的“目标已不存在”。

### 9.2 Store commit成功后 Publisher失败

此时：

- cleanup 已经成功；
- Step/Task mutation与 lease删除已经 durable；
- Publisher circuit停止后续执行；
- committed events可通过 replay恢复；
- 不存在 Process ownership丢失。

对于 intermediate Step，若 Step success已经提交但下一 Step尚未开始，restart recovery按既有 nonterminal Task规则完成为 `interrupted`。

## 10. Recovery

不修改 `ActiveLeases` 查询：

```sql
WHERE tasks.status IN ('queued','running','cancelling')
```

因为 close-before-terminalization 保证：

- 仍需 cleanup 的 Process，其 Task一定 nonterminal；
- terminal Task一定已经完成 cleanup并删除 lease。

不新增 database migration。现有 `RecoverInterrupted` 继续：

1. 枚举 nonterminal Task；
2. 使用 durable lease执行 identity-aware cleanup；
3. 把 Task原子完成为 `interrupted`；
4. 删除 lease；
5. 生成既有 recovery events/Artifact。

## 11. Protocol 与兼容性

不修改：

- Protocol Schema；
- generated protocol models；
- TypeScript Client；
- v1.1 output payload `{stream,text,truncated}`；
- cursor gap detection；
- event sequence规则；
- Task/Step公开状态枚举。

可观察变化只有时序：

- `task.step_finished`、Artifact和`task.finished`在 `Close` 成功后发布；
- sequence内容和相对顺序保持不变；
- `Close` failure时客户端不会看到错误的成功/终态；
- reconnect/replay读取相同 durable event stream。

## 12. 实现边界

建议新增：

- `apps/test-service/internal/task/manager_completion.go`
  - pending completion staging；
  - Close成功后的 outcome计算；
  - intermediate/terminal commit路由；
  - runtime state reset。

修改：

- `manager.go`
  - `activeTask` pending字段；
  - processDone/closeResult/shutdown路由；
  - `canRemove`条件。
- `manager_execution.go`
  - Start/Prepare/Done路径不再在 Close前持久化 completion；
  - 复用新的 staging/commit helper。
- Manager/TaskStore tests
  - durable可见性、event/artifact时序、first-cause、retry和recovery。
- Prepared Process design与Phase 3A plan
  - 用本设计替换旧的“部分路径 close-before-terminalization”描述。

不修改：

- `task.Store` interface；
- SQLite schema；
- Process host接口；
- Protocol production paths。

## 13. 测试矩阵

### 13.1 必需 RED→GREEN 回归

1. Start error + first Close failure：
   - Task nonterminal；
   - Step未 terminal；
   - terminal Artifact/events不存在；
   - `ActiveLeases` 返回 lease；
   - Manager unhealthy；
   - Close只调用一次。
2. final Step success + first Close failure：
   - 不得提前出现 succeeded Task/Step；
   - 不得出现 terminal Artifact/events；
   - lease仍可恢复。
3. intermediate Step success + first Close failure：
   - 当前 Step仍 running；
   - 下一 Step没有 Prepare；
   - lease保留；
   - Shutdown retry成功后因 cleanup failure完成为 infrastructure_failed。
4. command_failed + Close failure：
   - retry成功后 outcome为 infrastructure_failed。
5. cancel/timeout first-cause + Close failure：
   - retry成功后保留 cancelled/timed_out。
6. Close success：
   - intermediate Step提交后启动下一 Step；
   - final Step保持既有 event/Artifact相对顺序；
   - lease原子删除。
7. crash before retry：
   - restart recovery看见 lease；
   - cleanup执行一次；
   - Task完成为 interrupted。
8. Close success后 Store failure：
   - durable lease仍存在；
   - Task nonterminal；
   - recovery安全。
9. Store成功后 Publisher failure：
   - cleanup已完成；
   - committed state可 replay；
   - 无 lease残留。

### 13.2 压力与 race

- ownership/cause tests：`-count=100`；
- 关键 first-cause tests：`-count=1000`；
- 受影响 Task/TaskStore packages：`-race -count=1`；
-全 Go：normal与race；
-完整 `pnpm verify`；
-service-probe E2E：17/17；
-`git diff --check`；
-tracked worktree clean。

## 14. 验收条件

1. 任何 non-nil Process 的 completion mutation都发生在该 Process `Close` success之后。
2. normal Close failure不会留下 terminal Task或已完成 Step与缺失 lease的组合。
3. explicit Shutdown retry成功后，completion与`DeleteLease`在同一 Store mutation中提交。
4. retry前 crash可由 restart recovery找回 cleanup ownership。
5. intermediate Step只有在 cleanup成功后才能启动下一 Step。
6. earlier cancel/timeout/interrupted first-cause不被 cleanup failure覆盖。
7. success/command_failed在 cleanup failure且无 earlier cause时升级为 infrastructure_failed。
8. Protocol Schema/generated paths无diff。
9. v1.1 event payload、sequence与replay无回归。
10. 全量 normal/race/E2E门禁通过。

## 15. 被否决的替代方案

### 15.1 Terminal Task + 独立 cleanup state

该方案允许 Task先 terminal，再新增 cleanup state或 terminal lease recovery。

未采用原因：

- 需要新的数据库状态模型和migration；
- `task.finished`与真正资源清理完成脱钩；
- 客户端可能先看到 success，随后 cleanup失败；
- recovery必须处理 terminal Task lease，与当前清晰的不变量冲突；
- 增加 Protocol是否暴露 cleanup状态的长期设计负担。

### 15.2 仅修 Start-error 与 final-Step

未采用原因：

- intermediate Step同样先删除 lease再 Close；
- Prepare/cancel/timeout/circuit会继续形成分支式语义；
- 后续新增入口容易再次绕过 ownership invariant。

统一 staging/commit入口更容易通过代码结构和测试强制执行。

## 16. 最终裁决

Task/Step completion 的定义包括 Process cleanup：

> `Process.Done` 或 `Process.Start` error只产生 provisional completion。只有 `Process.Close` 成功并完成原子 persistence 后，completion 才是 durable、可发布、可对客户端宣称的结果。
