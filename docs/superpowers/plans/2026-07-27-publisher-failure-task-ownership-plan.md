# Publisher failure Task 所有权修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `Store.Create` 已提交而 Publisher panic 时，以 fail-closed terminal mutation 消除 persisted queued orphan，并明确区分 Publisher circuit 与 Store circuit。

**Architecture:** `start` 在 durable commit 后、任何 fallible publish 前，把同一个 execution decision 转交给 `activeTask` 并启动唯一的 plan-wide timeout。Publisher panic 只触发 Publisher circuit；创建路径先把 Task 原子结束为 first-cause 或 `infrastructure_failed`，把所有 pending Steps 标为 `skipped`，并只向 Store 写 durable terminal event。

**Tech Stack:** Go 1.26.5、SQLite TaskStore、内存 EventBroker、`context`、`sync.Map`、Go `testing` 与 race detector。

## Global Constraints

- 设计事实来源：`docs/superpowers/specs/2026-07-27-publisher-failure-task-ownership-design.md`。
- 实施基线：`cd0e355`；开始前必须确认 `git merge-base --is-ancestor cd0e355 HEAD` 成功。
- Protocol v1.1、`Publisher`、`ProcessFactory`、Store schema 和 migration 不变。
- Task timeout 仍是 Task 创建后只启动一次的 plan-wide budget，不得增加 Step timer。
- Publisher failure 不得调用 `Prepare`、`Start`、artifact writer 或 lease mutation。
- 已接受的 cancel、timeout、shutdown 保持 first-cause；只有没有更早原因时才使用 `infrastructure_failed`。
- fail-closed terminal event 只写入 Store，不得再次调用已故障的 Publisher。
- 第一次 terminal `ErrConflict` 只重试一次；第二次 conflict 进入 task-local recovery，不递归、不熔断 Store。
- terminal `ErrStorageUnavailable` 才触发 Store circuit，不得伪造 terminal state。
- 保持 round 1–3 的 nil Process、Close first-cause、deadline、queued Cancel、Cancel delivery dedup 和 terminal conflict 语义。
- 不实现 Task 4 的 Step events、Step artifact registry 或 restart replay。
- 所有 Markdown 叙述使用中文，English 技术名词保留原格式。

---

### Task 1：分离 Publisher circuit，并闭合 committed-create 所有权

**Files:**
- Modify: `apps/test-service/internal/task/manager.go:38-58`
- Modify: `apps/test-service/internal/task/manager.go:307-438`
- Modify: `apps/test-service/internal/task/manager.go:460-680`
- Modify: `apps/test-service/internal/task/manager.go:922-1017`
- Modify: `apps/test-service/internal/task/manager_execution.go:201-260`
- Modify: `apps/test-service/internal/task/manager_execution.go:512-652`
- Modify: `apps/test-service/internal/task/manager_test.go:218-227`
- Modify: `apps/test-service/internal/task/manager_test.go:751-1152`
- Modify: `apps/test-service/internal/task/manager_test.go:1226-1253`
- Modify: `apps/test-service/internal/task/manager_execution_test.go`
- Modify: `apps/test-service/internal/task/manager_cause_test.go`
- Update, ignored: `.superpowers/sdd/2026-07-26-phase3-multistep-task-engine-plan/task-3-report.md`
- Update, ignored: `.superpowers/sdd/2026-07-26-phase3-multistep-task-engine-plan/progress.md`

**Interfaces:**
- Consumes:

```go
type Store interface {
	Create(context.Context, Task, []StepSnapshot, EventDraft) (Task, []Event, error)
	Apply(context.Context, Mutation) (Task, []Event, error)
}

func (s *executionSignal) resolve(fallback Outcome) Outcome
func terminalStepMutations(
	current *activeTask,
	result ProcessResult,
	outcome Outcome,
	failPending bool,
	finishedAt time.Time,
) []StepMutation
```

- Produces:

```go
func (m *Manager) circuitFailed() bool
func (m *Manager) quiesceActive(active map[string]*activeTask)
func (m *Manager) tripPublisher(active map[string]*activeTask)
func (m *Manager) publishAll(events []Event) bool
func (m *Manager) publish(event Event) bool
func (m *Manager) persistCommittedCreateFailure(
	current *activeTask,
	active map[string]*activeTask,
) (Task, error)
```

- Preserves:

```go
type Publisher interface {
	Publish(Event)
}

type ProcessFactory interface {
	Prepare(context.Context, ProcessSpec, string, string) (ManagedProcess, error)
}
```

- [ ] **Step 1：确认基线、工作区和现有失败**

Run:

```powershell
git merge-base --is-ancestor cd0e355 HEAD
git status --short
go test ./apps/test-service/internal/task -run '^TestManagerPublisherPanicTripsCircuitAfterCommittedEvent$' -count=1
```

Expected:

- ancestor check exit `0`；
- tracked worktree clean；
- 现有测试 PASS，但只验证 `unhealthy` 和 `Prepare=0`，没有验证 Store 中的 Task 已 terminal。

- [ ] **Step 2：把 committed-create fail-closed 行为写成失败测试**

在 `manager_test.go` 将 `TestManagerPublisherPanicTripsCircuitAfterCommittedEvent` 改名为 `TestManagerPublisherFailureFailClosedAfterCommittedCreate`，并完整断言 durable state：

```go
func TestManagerPublisherFailureFailClosedAfterCommittedCreate(t *testing.T) {
	f := newManagerFixture(t)
	f.publisher.panicType = task.EventTaskCreated
	key := testID(31)

	_, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: key,
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Second,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start error = %v, want ErrStorageUnavailable", err)
	}
	stored, findErr := f.store.FindByIdempotencyKey(context.Background(), key)
	if findErr != nil {
		t.Fatal(findErr)
	}
	if stored.Status != task.StatusFinished || stored.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("stored task = %#v, want finished/infrastructure_failed", stored)
	}
	assertStepStatuses(t, stored, task.StepSkipped)
	events := f.store.eventsForTask(stored.ID)
	if got := eventTypes(events); !reflect.DeepEqual(got, []task.EventType{
		task.EventTaskCreated,
		task.EventTaskFinished,
	}) {
		t.Fatalf("durable event types = %v", got)
	}
	if f.processes.prepareCount() != 0 || f.process.startCalls() != 0 {
		t.Fatalf("process calls after publisher failure: prepare=%d start=%d",
			f.processes.prepareCount(), f.process.startCalls())
	}
	if len(f.artifacts.summariesCopy()) != 0 || len(f.store.artifactsCopy()) != 0 {
		t.Fatal("publisher fail-closed path created an artifact")
	}
	if lease := f.store.lease(stored.ID); lease.TaskID != "" {
		t.Fatalf("publisher fail-closed path created lease %#v", lease)
	}
	if f.manager.Healthy() {
		t.Fatal("manager remained healthy after publisher failure")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown after publisher failure = %v", err)
	}
}
```

在 `fakeStore` 增加只读测试 helper：

```go
func (s *fakeStore) eventsForTask(id string) []task.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []task.Event
	for _, event := range s.eventsValue {
		if event.TaskID == id {
			result = append(result, event)
		}
	}
	return result
}

func eventTypes(events []task.Event) []task.EventType {
	result := make([]task.EventType, len(events))
	for index := range events {
		result[index] = events[index].Type
	}
	return result
}
```

- [ ] **Step 3：运行 focused test，确认因 persisted queued orphan 正确 RED**

Run:

```powershell
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailureFailClosedAfterCommittedCreate$' -count=1
```

Expected: FAIL；`stored.Status == queued`、`stored.Outcome == ""`，durable events 只有 `task.created`。

- [ ] **Step 4：写 cancel、timeout、shutdown first-cause 失败测试**

扩展 `recordingPublisher`，允许在阻塞释放后 panic：

```go
type recordingPublisher struct {
	mu                  sync.Mutex
	value               []task.Event
	panicType           task.EventType
	panicAfterBlockType task.EventType
	blockType           task.EventType
	blockEntered        chan task.Event
	block               <-chan struct{}
}
```

在 `Publish` 完成现有 block 后执行：

```go
if event.Type == panicAfterBlockType {
	panic("publisher failure after block")
}
```

新增下列测试：

```go
func TestManagerPublisherFailurePreservesAcceptedCancellation(t *testing.T)
func TestManagerPublisherFailurePreservesClaimedTimeout(t *testing.T)
func TestManagerPublisherFailurePreservesClaimedShutdown(t *testing.T)
```

三个测试都使用阻塞的 `task.created` Publisher。Cancel 测试使用 `newObservedCancelContext` 等待公开调用进入 wait select 后再释放 Publisher；timeout 测试先确认 `f.clock.afterCalls(timeout)==1`，再 `f.clock.fire(t, timeout)`；shutdown 测试先启动 `Shutdown`，确认 Manager 已进入 closing，再释放 Publisher。最终分别断言：

```go
assertPublisherFailureOutcome(t, stored, task.OutcomeCancelled)
assertPublisherFailureOutcome(t, stored, task.OutcomeTimedOut)
assertPublisherFailureOutcome(t, stored, task.OutcomeInterrupted)
```

其中：

```go
func assertPublisherFailureOutcome(t *testing.T, stored task.Task, want task.Outcome) {
	t.Helper()
	if stored.Status != task.StatusFinished || stored.Outcome != want {
		t.Fatalf("stored task = %#v, want finished/%s", stored, want)
	}
	for index, step := range stored.Steps {
		if step.Status != task.StepSkipped {
			t.Fatalf("step[%d] = %s, want skipped", index, step.Status)
		}
	}
}
```

在 `manager_cause_test.go` 增加不依赖 goroutine 调度的 decision barrier：

```go
func TestPersistCommittedCreateFailurePreservesClaimedCause(t *testing.T) {
	for _, want := range []Outcome{OutcomeCancelled, OutcomeTimedOut, OutcomeInterrupted} {
		t.Run(string(want), func(t *testing.T) {
			manager, current, active, store := newCauseBarrierFixture()
			current.task.Status = StatusQueued
			current.task.StartedAt = nil
			current.task.Steps = []StepSnapshot{
				{ID: "first", Kind: StepSimulation, Status: StepPending},
				{ID: "second", Kind: StepSimulation, Status: StepPending},
			}
			store.task = current.task
			manager.executionSignals.Store(current.task.ID, current.execution)
			current.execution.claim(want)

			finished, err := manager.persistCommittedCreateFailure(current, active)
			if err != nil {
				t.Fatal(err)
			}
			if finished.Outcome != want || store.task.Outcome != want {
				t.Fatalf("outcomes = memory:%s store:%s, want %s",
					finished.Outcome, store.task.Outcome, want)
			}
			if len(active) != 0 {
				t.Fatalf("active tasks = %d, want 0", len(active))
			}
		})
	}
}

func TestManagerShutdownClaimsVisibleDecisionsBeforeCommandDelivery(t *testing.T) {
	manager := &Manager{
		shutdownSignal: make(chan struct{}, 1),
		stopped:        make(chan struct{}),
	}
	manager.healthy.Store(true)
	signal := newExecutionSignal()
	manager.executionSignals.Store("task", signal)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Shutdown(ctx) }()

	select {
	case <-signal.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not claim visible execution decision")
	}
	requested, _ := signal.state()
	if requested != OutcomeInterrupted {
		t.Fatalf("shutdown cause = %s, want interrupted", requested)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v, want context canceled", err)
	}
}
```

- [ ] **Step 5：运行 first-cause tests，确认旧实现正确 RED**

Run:

```powershell
go test ./apps/test-service/internal/task -run '^(TestManagerPublisherFailurePreserves|TestPersistCommittedCreateFailurePreservesClaimedCause|TestManagerShutdownClaimsVisibleDecisionsBeforeCommandDelivery)$' -count=1
```

Expected: FAIL；旧实现删除 decision 并留下 `queued` Task，或者 Publisher failure 把已接受原因丢失。

- [ ] **Step 6：写 conflict、Store unavailable 和 no-side-effect 失败测试**

新增：

```go
func TestManagerPublisherFailureRetriesTerminalConflictOnce(t *testing.T)
func TestManagerPublisherFailureRepeatedConflictUsesRecovery(t *testing.T)
func TestManagerPublisherFailureTerminalStoreUnavailableTripsStoreCircuit(t *testing.T)
func TestManagerPublisherFailureQuiescesOtherActiveTaskForRecovery(t *testing.T)
```

夹具配置和精确期望：

```go
// one transient conflict, then success
f.store.failApplyAt = 1
f.store.failApplyFor = 1
f.store.failApplyErr = task.ErrConflict
// want: applyCount()==2, finished/infrastructure_failed, no artifact/lease/process

// two conflicts
f.store.failApplyAt = 1
f.store.failApplyFor = 2
f.store.failApplyErr = task.ErrConflict
// want: applyCount()==2, durable task remains queued for restart recovery,
// Manager unhealthy, Shutdown completes, no artifact/lease/process

// actual Store failure
f.store.failApply = task.ErrStorageUnavailable
// want: applyCount()==1, durable task remains queued, no false terminal event,
// Manager unhealthy, Shutdown completes, no artifact/lease/process
```

`TestManagerPublisherFailureQuiescesOtherActiveTaskForRecovery` 先启动一个 `ScenarioHang` Task，再让第二个 Task 的 `task.created` publish panic。断言：

```go
// second task: finished/infrastructure_failed, all Steps skipped
// first task: receives exactly one Terminate and Close, durable status remains
// running for restart recovery; it is not falsely terminalized
// Manager: unhealthy, Shutdown completes after process cleanup
```

- [ ] **Step 7：运行 Store boundary tests，确认旧实现正确 RED**

Run:

```powershell
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure(Retries|Repeated|TerminalStore|Quiesces)' -count=1
```

Expected: FAIL；旧实现从未尝试 fail-closed `Store.Apply`，所以 `applyCount()==0`。

- [ ] **Step 8：实现 Publisher 与 Store circuit 分离**

在 `Manager` 增加 command-loop-owned flag：

```go
publisherFailed bool
```

新增：

```go
func (m *Manager) circuitFailed() bool {
	return m.storageFailed || m.publisherFailed
}

func (m *Manager) quiesceActive(active map[string]*activeTask) {
	for taskID, current := range active {
		current.recoveryRequired = true
		m.stopActive(current)
		if current.process == nil {
			delete(active, taskID)
			continue
		}
		if current.processCompleted {
			m.maybeStartClose(current)
		} else if !current.terminating {
			m.terminate(current)
		}
	}
}

func (m *Manager) tripPublisher(active map[string]*activeTask) {
	if m.publisherFailed {
		return
	}
	m.publisherFailed = true
	m.healthy.Store(false)
	if !m.storageFailed {
		m.quiesceActive(active)
	}
}
```

把 `tripStorage` 改为设置 `storageFailed` 后调用同一个 `quiesceActive`。把 command loop 中用于阻止正常完成、继续 Step 和输出持久化的 `m.storageFailed` 判断改为 `m.circuitFailed()`；`cancel` 在 circuit 已失败时直接返回 `ErrStorageUnavailable`，不能再次写 Store 或 Publish。

让 `Shutdown` 在线性化 `closing/healthy` 后直接 claim 当前 decisions：

```go
m.executionSignals.Range(func(_, value any) bool {
	value.(*executionSignal).claim(OutcomeInterrupted)
	return true
})
```

保持 first-cause：已有 cancel/timeout 不会被 shutdown 覆盖。

- [ ] **Step 9：让 publish 只检测 Publisher failure**

删除 `publish`/`publishAll` 的 `active` 参数和 `tripStorage` side effect：

```go
func (m *Manager) publishAll(events []Event) bool {
	for _, event := range events {
		if !m.publish(event) {
			return false
		}
	}
	return true
}

func (m *Manager) publish(event Event) (ok bool) {
	ok = true
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	m.publisher.Publish(event)
	return ok
}
```

逐一更新 `rg -n 'publishAll\\(' apps/test-service/internal/task` 找到的调用点：

- committed-create 路径：先执行 Step 10 的 fail-closed helper，再 `tripPublisher`；
- 已持久化 running/cancelling/output/terminal 路径：调用 `tripPublisher(active)` 并返回 `ErrStorageUnavailable`；
- `publishCommitted` 保持 centralized wrapper，但内部 circuit 改为 Publisher circuit。

- [ ] **Step 10：在 publish 前转交 active ownership 并 arm 唯一 timeout**

在 `start` 中把 ownership transfer 移到 `Store.Create` 新建成功后、`publishAll` 前：

```go
current := &activeTask{
	task: stored, plan: request.Plan, boundary: request.Boundary,
	timerStop: make(chan struct{}), timeoutStop: make(chan struct{}),
	watcherStop: make(chan struct{}), execution: execution,
}
active[stored.ID] = current
executionRetained = true
m.armTimeout(current)

if !m.publishAll(events) {
	terminal, terminalErr := m.persistCommittedCreateFailure(current, active)
	m.tripPublisher(active)
	if terminalErr != nil {
		return taskResponse{task: current.task, err: ErrStorageUnavailable}
	}
	return taskResponse{task: terminal, err: ErrStorageUnavailable}
}
```

idempotent existing、Create error 和 ownership transfer 前 early return 继续由现有 `defer` 执行 `stop` 与 `CompareAndDelete`。

- [ ] **Step 11：实现无 artifact、无 republish 的 fail-closed mutation**

在 `manager_execution.go` 新增：

```go
func (m *Manager) persistCommittedCreateFailure(
	current *activeTask,
	active map[string]*activeTask,
) (Task, error) {
	outcome := current.execution.resolve(OutcomeInfrastructureFailed)
	for attempt := 0; attempt < 2; attempt++ {
		finishedAt := m.clock.Now()
		finished, err := ApplyTransition(current.task, Transition{
			From: current.task.Status, To: StatusFinished, Outcome: outcome, At: finishedAt,
			ErrorCode: outcomeErrorCode(outcome), ErrorMessage: outcomeErrorMessage(outcome),
		})
		if err != nil {
			return current.task, err
		}
		steps := terminalStepMutations(current, ProcessResult{}, outcome, false, finishedAt)
		stored, _, err := m.store.Apply(context.Background(), Mutation{
			Task: finished, Expected: StatusQueued, Steps: steps,
			Events: []EventDraft{
				eventDraft(current.task.ID, EventTaskFinished, finishedAt, map[string]any{"outcome": outcome}),
			},
		})
		if err == nil {
			current.task = stored
			m.stopActive(current)
			delete(active, current.task.ID)
			return stored, nil
		}
		if errors.Is(err, ErrConflict) && attempt == 0 {
			continue
		}
		current.recoveryRequired = true
		m.stopActive(current)
		delete(active, current.task.ID)
		if !errors.Is(err, ErrConflict) {
			m.tripStorage(active)
		}
		return current.task, err
	}
	panic("unreachable")
}
```

该 helper 的两个 conflict attempts 使用相同的 resolved first-cause；不得通过 `replaceOutcome` 把已经接受的 cancel/timeout/shutdown 改成 `infrastructure_failed`。它不得调用 `ArtifactWriter`、`publishAll` 或写 lease。

- [ ] **Step 12：运行 focused GREEN，并修复实现而不是放宽断言**

Run:

```powershell
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure' -count=1
```

Expected: PASS；无 panic、hang、goroutine leak、额外 artifact 或 Process side effect。

- [ ] **Step 13：运行定向 stress 和 race**

Run:

```powershell
go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure' -count=100
go test -race ./apps/test-service/internal/task -run '^(TestManagerPublisherFailure|TestManagerCancellation|TestManagerCloseFailure)' -count=1
go test ./apps/test-service/internal/task -run '^TestManagerCloseFailurePreservesTotalTimeoutCause$' -count=1000
```

Expected: PASS；first-cause、Cancel waiter、Publisher block/panic 和 Manager Close 无偶发失败。

- [ ] **Step 14：运行 Task 3 与全 internal 回归**

Run:

```powershell
go test ./apps/test-service/internal/task -count=1
go test ./apps/test-service/internal/task ./apps/test-service/internal/runtime ./apps/test-service/internal/session -count=1
go test ./apps/test-service/internal/... -count=1
git diff --check
```

Expected: 全部 PASS；`git diff --check` 无输出。

- [ ] **Step 15：做范围与不变量审计**

Run:

```powershell
git diff --stat cd0e355..HEAD
git diff -- apps/test-service/internal/task/ports.go packages/protocol-schema apps/test-service/internal/taskstore
rg -n 'publisherFailed|storageFailed|circuitFailed|publishAll\\(' apps/test-service/internal/task
```

Expected:

- 生产修改只在 `manager.go` 和 `manager_execution.go`；
- 测试修改只在 Task 测试文件；
- `ports.go`、Protocol schema、TaskStore schema/migrations 无 diff；
- 每个 `publishAll` failure call site 明确进入 Publisher circuit；
- 没有 Task 4 event/artifact/recovery 实现。

- [ ] **Step 16：更新中文 SDD 证据**

在 `task-3-report.md` 追加 `## 审查修复 round 4：Publisher failure 所有权`，记录：

- 根因：Store commit、active ownership 和 Publisher circuit 的错误顺序；
- focused RED 原始输出；
- fail-closed ownership/circuit 设计；
- first-cause、conflict、Store unavailable 的 GREEN 输出；
- stress、race、三包、全 internal 和 diff check 的 fresh 结果。

在 `progress.md` 追加：

```text
Task 3: architecture reset approved (design cd0e355)
Task 3: fix round 4/5 (publisher ownership finding addressed; review pending)
```

- [ ] **Step 17：提交独立修复**

Run:

```powershell
git status --short
git add apps/test-service/internal/task
git diff --cached --check
git commit -m "fix: fail closed after publisher failure"
git status --short
```

Expected:

- commit 只包含 Task 生产代码和测试；
- ignored report/ledger 不进入 commit；
- final tracked worktree clean。

## Task 1 完成门槛

- focused RED/GREEN chronology 完整；
- Publisher/Store circuit 源码上明确分离；
- committed Task 不再形成 persisted queued orphan；
- fail-closed mutation 无 artifact、lease、Process 或 republish；
- cancel、timeout、shutdown first-cause 均受控；
- transient conflict 只重试一次，真实 Store failure 不伪造 terminal；
- stress、race、Task、三包、全 internal 和 diff check 全部 fresh PASS；
- 独立 scoped reviewer 裁决 clean。
