# Close-before-terminalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让所有持有 non-nil `ManagedProcess` 的 Step/Task completion 在 `Close` 成功后才持久化并删除 durable lease，彻底关闭 Start-error、intermediate-Step 和 final-Step 的 cleanup ownership 缺口。

**Architecture:** `activeTask` 保存 runtime-only pending Process result。`Process.Done`、Start error 与 Prepared Process cleanup只 stage completion并启动有界 cleanup；`closeResultCommand` 成功后才计算 outcome，并在单一入口提交 intermediate Step或terminal Task mutation。`Close` failure保留 nonterminal Task、durable lease与active owner，仅显式 `Shutdown`授权同进程retry。

**Tech Stack:** Go 1.26.5、SQLite、TypeScript 6.0.3、Node.js 24.18.x、pnpm 11.4.0、Go race detector、Node test runner。

## Global Constraints

- 设计依据：`docs/superpowers/specs/2026-07-28-close-before-terminalization-design.md`。
- 当 `current.process != nil && !current.closeComplete` 时，不得持久化 Step/Task completion、terminal Artifact/events或`DeleteLease=true`。
- Process result、Start error和Prepared cleanup cause只产生 runtime-only pending completion。
- normal non-circuit `Close` failure保持 Task nonterminal、durable lease和active owner；普通 callback、Cancel、timeout与Process Done不得retry。
- 显式 `Shutdown`是本进程唯一失败`Close` retry authority。
- earlier cancel、timeout或interrupted first-cause不得被cleanup failure覆盖；无earlier cause的cleanup failure固定为`infrastructure_failed`。
- intermediate Step只有在当前 Process `Close`成功后才能提交`StepSucceeded`和启动下一 Step。
- crash发生在result、Close或completion transaction窗口时，restart recovery必须看到nonterminal Task与durable lease，并保守完成为`interrupted`。
- 不新增Protocol状态、SQLite migration、`task.Store`接口或Process host接口。
- 不修改`packages/protocol-schema/src`、`packages/protocol-schema/generated`、`apps/test-service/internal/protocol`或TypeScript v1.1 payload。
- v1.1 output payload严格保持`{stream,text,truncated}`，event taskId/sequence不得drop或renumber。
- SQLite不得保存environment、`ProcessSpec`、runtime-only boundary或pending Process result。
- Windows MSVC、Windows clang-cl/llvm-cov、Linux GCC与Linux Clang扩展边界不得回退。
- GitHub不是产品runtime依赖；production仍只允许local IPC，不得增加TCP/HTTP/DNS或启动时下载。
- 所有Markdown叙述使用中文，English技术名词保持English格式。
- 严格TDD：生产代码之前必须有覆盖该行为的RED，报告记录RED命令、预期原因和实际输出。
- 不修改或重写任何已发布migration，特别是`002_multistep_tasks.sql`和`003_normalize_simulation_requests.sql`。

---

## File Map

- Create: `apps/test-service/internal/task/manager_completion.go`
  - pending completion staging；
  - Close成功后的outcome计算；
  - intermediate/terminal completion commit；
  - 当前Process runtime state reset。
- Create: `apps/test-service/internal/task/manager_completion_test.go`
  - Start-error、final-Step、intermediate-Step的close-before-terminalization直接回归。
- Modify: `apps/test-service/internal/task/manager.go`
  - `activeTask.pendingCompletion`；
  - processDone/closeResult/shutdown routing；
  - `canRemove` ownership条件。
- Modify: `apps/test-service/internal/task/manager_execution.go`
  - Start/Prepare/Done路径改为stage而非提前persist；
  - `persistSuccessfulStep`与terminal persistence只从Close成功入口调用。
- Modify: `apps/test-service/internal/task/manager_execution_test.go`
  - first-cause、boundary revalidation、Step Store/Publisher failure的时序断言。
- Modify: `apps/test-service/internal/task/manager_cause_test.go`
  - cause-before-unhealthy、circuit handoff和retry authorization。
- Modify: `apps/test-service/internal/task/manager_test.go`
  - fake Store/Process的lease、event、Artifact和Shutdown retry集成断言。
- Modify: `apps/test-service/internal/taskstore/sqlite_test.go`
  - nonterminal Task + durable lease的restart recovery组合回归。
- Modify: `docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md`
  - 把所有Process completion统一到Close成功后。
- Modify: `docs/superpowers/plans/2026-07-27-prepared-process-lease-ownership-plan.md`
  - 修正旧实现步骤与验收矩阵。
- Modify: `docs/superpowers/plans/2026-07-26-phase3-multistep-task-engine-plan.md`
  - 修正Step completion、Artifact和lease transaction顺序。
- Modify: `docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md`
  - 引用最终cleanup/terminal visibility不变量。

---

### Task 1: 统一 Process completion barrier

**Files:**
- Create: `apps/test-service/internal/task/manager_completion.go`
- Create: `apps/test-service/internal/task/manager_completion_test.go`
- Modify: `apps/test-service/internal/task/manager.go:124-148,495-638,991-1038`
- Modify: `apps/test-service/internal/task/manager_execution.go:274-507,510-643,692-754`
- Modify: `apps/test-service/internal/task/manager_execution_test.go`
- Modify: `apps/test-service/internal/task/manager_cause_test.go`
- Modify: `apps/test-service/internal/task/manager_test.go`
- Modify: `apps/test-service/internal/taskstore/sqlite_test.go`
- Modify: `docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md`
- Modify: `docs/superpowers/plans/2026-07-27-prepared-process-lease-ownership-plan.md`
- Modify: `docs/superpowers/plans/2026-07-26-phase3-multistep-task-engine-plan.md`
- Modify: `docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md`

**Interfaces:**
- Consumes:
  - `activeTask.process ManagedProcess`
  - `activeTask.execution *executionSignal`
  - `Manager.persistSuccessfulStep`
  - `Manager.persistTerminal`
  - `Manager.startNextStep`
  - `Manager.maybeStartClose`
  - `Mutation.DeleteLease`
- Produces:

```go
type pendingProcessCompletion struct {
	Result      ProcessResult
	FailPending bool
}

func (m *Manager) stageProcessCompletion(
	current *activeTask,
	result ProcessResult,
	failPending bool,
)

func processCompletionOutcome(
	current *activeTask,
	pending pendingProcessCompletion,
) Outcome

func (m *Manager) commitClosedCompletion(
	current *activeTask,
	active map[string]*activeTask,
) error

func resetClosedProcess(current *activeTask)
```

`activeTask`新增：

```go
pendingCompletion *pendingProcessCompletion
```

- [ ] **Step 1: 记录当前baseline**

使用指定portable runtime：

```powershell
$env:Path='C:\codex_project\unitTest\.worktrees\foundation-protocol-service\.superpowers\runtime\go1.26.5\go\bin;'+$env:Path
$env:GOPROXY='off'
$env:GOCACHE=(Join-Path (Get-Location) '.superpowers\sdd\2026-07-28-close-before-terminalization-plan\.gocache')
go version
go test ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
```

Expected:

```text
go version go1.26.5 windows/amd64
ok unit-test-ide.local/test-service/internal/task
ok unit-test-ide.local/test-service/internal/taskstore
```

- [ ] **Step 2: 写Start-error与final-Step的RED tests**

在`manager_completion_test.go`增加确定性测试。使用现有`newManagerFixture`、`fakeProcess`、`eventTypes`与`assertStepStatuses`，不要复制新的Manager fake。

```go
func TestManagerStartErrorCloseFailureDefersTerminalVisibility(t *testing.T) {
	f := newManagerFixture(t)
	f.process.startErr = errors.New("start allocated resources before failing")
	f.process.closeErr = errors.New("close failed")

	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(140),
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.awaitTerminate(t, 1)
	f.awaitProcessClose(t, f.process)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.Outcome != "" ||
		stored.Steps[0].Status != task.StepRunning {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if got := eventTypes(f.store.eventsForTask(started.ID)); !reflect.DeepEqual(got, []task.EventType{
		task.EventTaskCreated,
		task.EventTaskStarted,
		task.EventTaskStepStarted,
	}) {
		t.Fatalf("events before Close retry = %v", got)
	}
	if artifacts := f.store.artifactsCopy(); len(artifacts) != 0 {
		t.Fatalf("terminal artifacts before Close retry = %#v", artifacts)
	}
}

func TestManagerFinalStepCloseFailureDefersTerminalVisibility(t *testing.T) {
	f := newManagerFixture(t)
	f.process.closeErr = errors.New("close failed after exit zero")

	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(141),
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.Outcome != "" ||
		stored.Steps[0].Status != task.StepRunning {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if len(f.store.artifactsCopy()) != 0 {
		t.Fatal("Close failure published a terminal artifact")
	}
}
```

- [ ] **Step 3: 运行Start-error/final-Step tests并确认RED**

```powershell
go test ./apps/test-service/internal/task -run 'TestManager(StartErrorCloseFailure|FinalStepCloseFailure)DefersTerminalVisibility$' -count=1
```

Expected RED:

```text
Start-error case observes finished/infrastructure_failed with no lease
Final-Step case observes finished/succeeded with no lease
```

失败必须来自提前terminalization/lease deletion，而不是test timeout、channel deadlock或fixture拼写。

- [ ] **Step 4: 写intermediate-Step与next-Step barrier RED**

```go
func TestManagerIntermediateStepCloseFailureKeepsRunningStepAndLease(t *testing.T) {
	f := newManagerFixture(t)
	first := f.process
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{first, second}
	t.Cleanup(func() {
		second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
	})
	first.closeErr = errors.New("close failed after intermediate success")

	started, err := f.manager.Start(
		context.Background(),
		twoStepStartRequest(testID(142), time.Minute, fixedBoundary{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	first.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, first)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.ActiveStep != "first" ||
		stored.Steps[0].Status != task.StepRunning ||
		stored.Steps[1].Status != task.StepPending {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if got := f.processes.prepareCount(); got != 1 || second.startCalls() != 0 {
		t.Fatalf("next Step started before cleanup: Prepare=%d Start=%d", got, second.startCalls())
	}
}
```

- [ ] **Step 5: 运行intermediate-Step test并确认RED**

```powershell
go test ./apps/test-service/internal/task -run 'TestManagerIntermediateStepCloseFailureKeepsRunningStepAndLease$' -count=1
```

Expected RED:

```text
stored first Step is succeeded, ActiveStep is empty, and the durable lease is absent
```

- [ ] **Step 6: 写Shutdown retry与outcome RED matrix**

在`manager_completion_test.go`增加table-driven test。每个case第一次`Close`失败，然后清除`closeErr`并显式`Shutdown`：

```go
func TestManagerCloseBeforeTerminalizationOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name        string
		result      task.ProcessResult
		claim       task.Outcome
		wantOutcome task.Outcome
	}{
		{
			name: "success cleanup failure",
			result: task.ProcessResult{ExitCode: 0},
			wantOutcome: task.OutcomeInfrastructureFailed,
		},
		{
			name: "command failure cleanup failure",
			result: task.ProcessResult{ExitCode: 7},
			wantOutcome: task.OutcomeInfrastructureFailed,
		},
		{
			name: "cancel first cause",
			result: task.ProcessResult{Err: context.Canceled},
			claim: task.OutcomeCancelled,
			wantOutcome: task.OutcomeCancelled,
		},
		{
			name: "timeout first cause",
			result: task.ProcessResult{Err: context.DeadlineExceeded},
			claim: task.OutcomeTimedOut,
			wantOutcome: task.OutcomeTimedOut,
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			releaseClose := make(chan struct{})
			f.process.closeBlock = releaseClose
			f.process.closeErr = errors.New("first Close failed")
			started, err := f.manager.Start(context.Background(), task.StartRequest{
				IdempotencyKey: testID(byte(143 + index)),
				Scenario: task.ScenarioSuccess,
				Timeout: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			f.process.complete(tt.result)
			f.awaitProcessClose(t, f.process)
			switch tt.claim {
			case task.OutcomeCancelled:
				if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
			case task.OutcomeTimedOut:
				f.clock.fire(t, time.Minute)
			}
			close(releaseClose)
			f.awaitUnhealthy(t)

			beforeRetry, err := f.store.Get(context.Background(), started.ID)
			if err != nil || beforeRetry.Status == task.StatusFinished {
				t.Fatalf("Task before retry = %#v, %v", beforeRetry, err)
			}
			if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
				t.Fatalf("lease before retry = %#v", lease)
			}
			f.process.mu.Lock()
			f.process.closeErr = nil
			f.process.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := f.manager.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
			if finished.Outcome != tt.wantOutcome {
				t.Fatalf("outcome = %s, want %s", finished.Outcome, tt.wantOutcome)
			}
			if mutation := f.store.lastMutation(); !mutation.DeleteLease {
				t.Fatalf("terminal mutation did not delete lease: %#v", mutation)
			}
		})
	}
}
```

- [ ] **Step 7: 运行outcome matrix并确认RED**

```powershell
go test ./apps/test-service/internal/task -run 'TestManagerCloseBeforeTerminalizationOutcomeMatrix$' -count=1
```

Expected RED:

```text
success/command-failed cases are already terminal before retry
cancel/timeout cases observe an absent durable lease
```

- [ ] **Step 8: 建立runtime-only pending completion**

在`manager_completion.go`实现：

```go
package task

type pendingProcessCompletion struct {
	Result      ProcessResult
	FailPending bool
}

func (m *Manager) stageProcessCompletion(
	current *activeTask,
	result ProcessResult,
	failPending bool,
) {
	if current.pendingCompletion != nil {
		return
	}
	current.pendingCompletion = &pendingProcessCompletion{
		Result:      result,
		FailPending: failPending,
	}
	current.processCompleted = true
	m.maybeStartClose(current)
}

func processCompletionOutcome(
	current *activeTask,
	pending pendingProcessCompletion,
) Outcome {
	if cause := current.execution.currentCause(); cause != "" {
		return cause
	}
	if current.terminationFailed || pending.Result.Err != nil {
		return OutcomeInfrastructureFailed
	}
	if pending.Result.ExitCode == 0 {
		return OutcomeSucceeded
	}
	return OutcomeCommandFailed
}
```

在`activeTask`增加`pendingCompletion *pendingProcessCompletion`。`setCurrentProcess`必须清零该字段，确保下一 Step不会复用上一 Process result。

- [ ] **Step 9: 把所有Process完成入口改为stage**

修改`manager_execution.go`：

1. `Process.Start` error：

```go
if startErr != nil {
	cause := current.execution.resolve(OutcomeInfrastructureFailed)
	current.cleanupWithoutDone = true
	m.terminate(current)
	m.stageProcessCompletion(
		current,
		ProcessResult{Err: startErr},
		cause == OutcomeInfrastructureFailed,
	)
	return nil
}
```

2. `cleanupPreparedProcess`：

```go
func (m *Manager) cleanupPreparedProcess(current *activeTask) {
	current.cleanupWithoutDone = true
	outcome := current.execution.resolve(OutcomeInfrastructureFailed)
	m.terminate(current)
	m.stageProcessCompletion(
		current,
		ProcessResult{Err: errors.New("step preparation failed")},
		outcome == OutcomeInfrastructureFailed,
	)
}
```

3. `finish`只flush output并stage `ProcessResult`。删除它在Close前调用`persistSuccessfulStep`或`finishExecution`的分支。

4. 持有non-nil Process的cancel、timeout、shutdown和conflict路径不得直接调用terminal persistence；它们只固定cause、Terminate并等待/stage completion。

Process-free failure继续使用即时terminalization。

- [ ] **Step 10: 让Close success成为唯一commit入口**

在`manager_completion.go`实现：

```go
func (m *Manager) commitClosedCompletion(
	current *activeTask,
	active map[string]*activeTask,
) error {
	if current.pendingCompletion == nil || !current.closeComplete {
		return ErrConflict
	}
	pending := *current.pendingCompletion
	outcome := processCompletionOutcome(current, pending)
	if outcome == OutcomeSucceeded &&
		current.nextStep+1 < len(current.plan.Steps) {
		if err := m.persistSuccessfulStep(current, pending.Result, active); err != nil {
			return err
		}
		current.nextStep++
		resetClosedProcess(current)
		return m.startNextStep(current, active)
	}
	finished, err := m.persistTerminal(
		current,
		pending.Result,
		outcome,
		pending.FailPending,
		true,
		active,
	)
	if err != nil {
		return err
	}
	current.task = finished
	current.leasePersisted = false
	current.pendingCompletion = nil
	m.stopActive(current)
	return nil
}
```

`resetClosedProcess`必须：

```go
func resetClosedProcess(current *activeTask) {
	current.process = nil
	current.leasePersisted = false
	current.pendingCompletion = nil
	current.processCompleted = false
	current.terminating = false
	current.terminationComplete = false
	current.terminationFailed = false
	current.closeStarted = false
	current.closeComplete = false
	current.closeFailed = false
	current.cleanupWithoutDone = false
	current.failPendingStep = false
}
```

在`closeResultCommand` success分支：

- `recoveryRequired`/circuit handoff继续使用既有路径；
- normal path调用`commitClosedCompletion`；
- commit error进入既有Store/Publisher circuit处理；
- 不再在`nextStep < len(plan.Steps)`时未经completion commit直接Prepare下一 Step。

- [ ] **Step 11: 收紧persistence与owner removal guard**

实施以下结构约束：

```go
func (m *Manager) persistSuccessfulStep(...) error {
	if current.process != nil && !current.closeComplete {
		return ErrConflict
	}
	// existing atomic Step mutation + DeleteLease
}
```

`persistTerminal(... deleteLease=true ...)`的所有non-nil Process caller必须已经`closeComplete=true`。

`canRemove`改为：

```go
func (m *Manager) canRemove(current *activeTask) bool {
	if current.recoveryRequired {
		return current.closeComplete
	}
	return current.closeComplete &&
		current.task.Status == StatusFinished &&
		current.pendingCompletion == nil
}
```

`cleanupWithoutDone`不得单独允许owner removal。intermediate success commit后使用`resetClosedProcess`，不删除active Task。

- [ ] **Step 12: 运行核心与outcome tests并确认GREEN**

```powershell
go test ./apps/test-service/internal/task -run 'TestManager(StartErrorCloseFailure|FinalStepCloseFailure)DefersTerminalVisibility$|TestManagerIntermediateStepCloseFailureKeepsRunningStepAndLease$|TestManagerCloseBeforeTerminalizationOutcomeMatrix$' -count=1
```

Expected:

```text
ok unit-test-ide.local/test-service/internal/task
```

- [ ] **Step 13: 更新既有Step/Store/Publisher tests到新时序**

修改这些tests：

- `TestManagerRevalidatesBoundaryBeforePreparingNextStep`
- `TestManagerCloseFailurePreservesCancellationCause`
- `TestManagerLeaseFreeCloseFailureClaimsInfrastructureCauseBeforeShutdown`
- `TestManagerCloseFailurePreservesTotalTimeoutCause`
- `TestManagerStepFinishedStoreFailureRetainsLeaseAndOwnerWithoutStartingNextStep`
- `TestManagerStepFinishedPublisherFailureRetainsOwnerWithoutStartingNextStep`

精确变化：

1. `Close` block期间，当前 Step仍`running`，lease仍存在，`task.step_finished`未提交。
2. release `Close`后才允许boundary revalidation、Store failure或Publisher failure发生。
3. cleanup failure把尚未完成的current Step记为`failed`，而不是先`StepSucceeded`。
4. Store failure发生在Close success之后：durable Task仍nonterminal、lease仍存在，Process已经close；restart recovery接管。
5. Publisher failure发生在Close success和Store commit之后：Step mutation已durable、lease已删除、Process已close、下一 Step不启动。

- [ ] **Step 14: 增加restart recovery组合test**

在`taskstore/sqlite_test.go`增加：

```go
func TestRecoverInterruptedOwnsDeferredCompletionLease(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	value := newTask(140, 141, now)
	value.Steps = []task.StepSnapshot{{
		ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending,
	}}
	created, _, err := store.Create(
		ctx,
		value,
		value.Steps,
		draft(value.ID, task.EventTaskCreated, now),
	)
	if err != nil {
		t.Fatal(err)
	}
	running := startStoredStep(t, store, created, 0, now.Add(time.Second), 4242)

	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 || leases[0].TaskID != running.ID {
		t.Fatalf("deferred completion leases = %#v, %v", leases, err)
	}
	if _, err := store.RecoverInterrupted(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Get(ctx, running.ID)
	if err != nil ||
		finished.Status != task.StatusFinished ||
		finished.Outcome != task.OutcomeInterrupted {
		t.Fatalf("recovered Task = %#v, %v", finished, err)
	}
	leases, err = store.ActiveLeases(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatalf("leases after recovery = %#v, %v", leases, err)
	}
}
```

该test与Manager核心tests共同证明“result已到达但completion未提交”的crash recovery组合；SQLite不保存pending result，restart outcome固定为`interrupted`。

- [ ] **Step 15: 运行focused failure/recovery suite**

```powershell
go test ./apps/test-service/internal/task -run 'TestManager(StartErrorCloseFailure|FinalStepCloseFailure|IntermediateStepCloseFailure|CloseBeforeTerminalization|RevalidatesBoundary|CloseFailurePreserves|LeaseFreeCloseFailure|StepFinishedStoreFailure|StepFinishedPublisherFailure)' -count=1
go test ./apps/test-service/internal/taskstore -run 'TestRecoverInterruptedOwnsDeferredCompletionLease$|TestRecoverInterruptedFinishesAllActiveTasksAndDeletesLeases$' -count=1
```

Expected:

```text
ok unit-test-ide.local/test-service/internal/task
ok unit-test-ide.local/test-service/internal/taskstore
```

- [ ] **Step 16: 运行stress与race**

```powershell
go test ./apps/test-service/internal/task -run 'TestManager(StartErrorCloseFailure|FinalStepCloseFailure|IntermediateStepCloseFailure|CloseBeforeTerminalization|CloseFailurePreserves|LeaseFreeCloseFailure)' -count=100
go test ./apps/test-service/internal/task -run 'TestManagerCloseFailurePreservesTotalTimeoutCause$' -count=1000
go test -race ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
```

Expected:

```text
all commands exit 0
no data race
```

- [ ] **Step 17: 同步中文设计与实施文档**

更新四份文档：

1. `prepared-process-lease-ownership-design.md`
   - 所有non-nil Process completion都先Close；
   - 删除任何“Process result后可先terminalize/StepSucceeded”的旧描述；
   - 加入pending completion与crash window。
2. `prepared-process-lease-ownership-plan.md`
   - 替换旧production pseudocode；
   - 更新normal Close、Store/Publisher、restart test matrix。
3. `phase3-multistep-task-engine-plan.md`
   - intermediate Step顺序改为`result -> Close -> StepSucceeded/DeleteLease -> next Step`；
   - final Step顺序改为`result -> Close -> terminal Artifact/events/DeleteLease`。
4. `workspace-cmake-toolchains-design.md`
   - 加入“cleanup是completion的一部分”的最终架构引用。

运行：

```powershell
rg -n 'terminalize.*before Close|StepSucceeded.*before Close|Close后再删除lease|Close成功后' docs/superpowers
git diff --check
```

Expected：

- 不存在与新Spec矛盾的正向旧裁决；
- `git diff --check`无输出；
- Markdown叙述中文，English技术名词保持English格式。

- [ ] **Step 18: 运行完整verification**

```powershell
go test ./apps/test-service/... -count=1
go test -race ./apps/test-service/... -count=1
pnpm verify
git diff --check
git status --short
```

Expected：

- 所有Go packages PASS；
- race detector无data race；
- workspace、Protocol generated check、TypeScript、Go normal/race与service-probe E2E 17/17全部PASS；
- `git diff --check`无输出；
-提交后tracked worktree clean。

- [ ] **Step 19: 审计禁止修改的边界**

```powershell
git diff --name-only 7a27a870ed4abb4fdc747dc2f142dcba98b676b1..HEAD -- `
  packages/protocol-schema/src `
  packages/protocol-schema/generated `
  apps/test-service/internal/protocol `
  apps/test-service/internal/taskstore/migrations
```

Expected：

```text
no output
```

同时确认没有新增network/runtime download import：

```powershell
pnpm test:workspace
```

Expected：

```text
8/8 workspace smoke tests PASS
```

- [ ] **Step 20: Commit**

```powershell
git add -- `
  apps/test-service/internal/task/manager_completion.go `
  apps/test-service/internal/task/manager_completion_test.go `
  apps/test-service/internal/task/manager.go `
  apps/test-service/internal/task/manager_execution.go `
  apps/test-service/internal/task/manager_execution_test.go `
  apps/test-service/internal/task/manager_cause_test.go `
  apps/test-service/internal/task/manager_test.go `
  apps/test-service/internal/taskstore/sqlite_test.go `
  docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md `
  docs/superpowers/plans/2026-07-27-prepared-process-lease-ownership-plan.md `
  docs/superpowers/plans/2026-07-26-phase3-multistep-task-engine-plan.md `
  docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md
git commit -m "fix: close processes before persisting completion"
```

Commit前再次确认：

```powershell
git diff --cached --check
git status --short
```

Expected：

- staged范围只包含本Task intentional files；
-没有untracked临时overlay、test binary、cache或log。

---

## Completion Checklist

- [ ] Start-error不会在Close成功前terminalize或删除lease。
- [ ] final-Step result不会在Close成功前发布Task/Step terminal visibility。
- [ ] intermediate-Step result不会在Close成功前提交StepSucceeded或启动下一 Step。
- [ ] normal Close failure保留nonterminal Task、durable lease、pending completion与active owner。
- [ ] 普通command不retry，显式Shutdown retry成功后才commit completion。
- [ ] cleanup failure且无earlier cause最终为infrastructure_failed。
- [ ] cancel/timeout/interrupted earlier first-cause保持不变。
- [ ] Close success后Store failure仍由durable lease/restart recovery覆盖。
- [ ] Store commit后Publisher failure没有Process ownership缺口。
- [ ] crash recovery把未持久化pending result保守完成为interrupted。
- [ ] v1.1 payload、sequence、replay与17项E2E无回归。
- [ ] Protocol/generated/migrations production paths无diff。
- [ ] full normal/race/verify门禁PASS。
