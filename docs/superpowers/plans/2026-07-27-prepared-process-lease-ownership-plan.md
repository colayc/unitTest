# Prepared Process durable lease 所有权实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: 使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 按 Task 执行本计划。所有执行步骤使用 checkbox（`- [ ]`）跟踪。

**Goal:** 在 `Prepare` 返回 non-nil Process 后、任何后续 decision/error/circuit 边界之前持久化 recovery lease，消除无 active owner且无 durable lease 的 Prepared Process。

**Architecture:** 复用现有 `Store.Apply` 实现两阶段 lease。第一阶段只为当前 nonterminal Task upsert lease，不改变 Task/Step、不写 event、不调用 Publisher；第二阶段继续执行既有 running mutation。`activeTask.leasePersisted` 明确记录当前 Process 是否可以安全交给 restart recovery。

**Tech Stack:** Go 1.26.5、SQLite TaskStore、Go `context`/`sync`、现有 Task Manager command loop、Windows/Linux Process recovery contract。

## Global Constraints

- 实施基线必须包含设计提交 `676fed0ae50a5b3092a44212b5804edf943e1c65`。
- Protocol v1.1 保持不变。
- Store schema 与 migration 保持不变。
- `Store`、`Publisher`、`ProcessFactory`、`ManagedProcess` interface 保持不变。
- 客户端 event 类型、payload 与顺序保持不变。
- `task.created` Publisher failure 仍发生在 `Prepare` 之前，不得创建 Process、lease 或 artifact。
- pre-lease mutation 不改变 Task/Step 状态，不写 event，不调用 Publisher。
- 首 Step 允许 `queued` Task 持有内部 recovery lease，但不得提前发布 `task.started`。
- cancel、timeout、shutdown 与 infrastructure failure 继续使用同一个 first-cause decision。
- circuit Close error 只有在当前 Process 的 durable lease 已存在时才能释放 active owner。
- lease 未提交且 cleanup 失败时必须保留 active owner；`Shutdown(ctx)` 可以按 caller deadline 返回，不能虚假宣称 cleanup 完成。
- ownership safety裁决：当 `process != nil && leasePersisted=false` 时，任何 Close error都保留 active owner；normal非 circuit路径若尚无 cause，首次 Close error必须在对外发布 unhealthy之前 claim `infrastructure_failed`，已有 cause不被覆盖；普通 timeout、cancel、termination result、process done与其他 cleanup command不得重试已失败的 Close，只有后续显式 Shutdown可以 retry；terminal visibility延迟到该 retry成功，first-cause与最终 outcome保持不变。
- 不实现第二套 recovery journal、Task 4 recovery replay、Step events 或 Step artifact registry。
- Markdown 叙述使用中文，English 技术名词保留原格式。

---

## 文件结构与责任

- Modify: `apps/test-service/internal/task/manager.go`
  - 为 `activeTask` 增加 `leasePersisted`；
  - 在 circuit/recovery Close error 时按 durable lease决定 handoff或保留 owner；
  - 下一 Step 更换 Process 时清理 per-process lease ownership 状态。
- Modify: `apps/test-service/internal/task/manager_execution.go`
  - `Prepare` 后立即接管 non-nil Process；
  - 增加无 event的 pre-lease mutation；
  - running mutation不再承担首次 `PutLease`；
  - 保持 Prepare error、first-cause 与 Store failure cleanup。
- Modify: `apps/test-service/internal/task/manager_test.go`
  - 扩展 fake ProcessFactory，使 Prepare error 可以同时返回 non-nil Process；
  - 增加 pre-lease、first-cause、Publisher circuit handoff、pre-lease failure 与 multi-step 回归测试。
- Modify: `apps/test-service/internal/task/manager_execution_test.go`
  - 调整受新增 pre-lease transaction影响的多 Step、Store failure与 conflict测试；
  - 明确区分 pre-lease mutation和 running mutation。
- Modify: `apps/test-service/internal/task/manager_cause_test.go`
  - 对 pre-lease helper 的 first-cause与 error classification 做同包确定性验证。
- Modify: `apps/test-service/internal/taskstore/sqlite_test.go`
  - 证明 queued Task lease会被 `ActiveLeases` 返回，并由 `RecoverInterrupted` 删除。
- Modify: `apps/test-service/internal/runtime/runtime_test.go`
  - 证明 Runtime 在 Manager 启动前清理 queued Prepared lease并完成 interrupted recovery。
- Update ignored evidence:
  - `.superpowers/sdd/2026-07-27-publisher-failure-task-ownership-plan/task-1-report.md`
  - `.superpowers/sdd/2026-07-27-publisher-failure-task-ownership-plan/progress.md`
  - `.superpowers/sdd/2026-07-26-phase3-multistep-task-engine-plan/task-3-report.md`
  - `.superpowers/sdd/2026-07-26-phase3-multistep-task-engine-plan/progress.md`

---

### Task 1：为 Prepared Process 建立 durable recovery ownership

**Files:**

- Modify: `apps/test-service/internal/task/manager.go:117-145`
- Modify: `apps/test-service/internal/task/manager.go:548-586`
- Modify: `apps/test-service/internal/task/manager_execution.go:271-376`
- Modify: `apps/test-service/internal/task/manager_execution.go:386-407`
- Modify: `apps/test-service/internal/task/manager_test.go`
- Modify: `apps/test-service/internal/task/manager_cause_test.go`
- Modify: `apps/test-service/internal/task/manager_execution_test.go`
- Modify: `apps/test-service/internal/taskstore/sqlite_test.go`
- Modify: `apps/test-service/internal/runtime/runtime_test.go`

**Interfaces:**

- Consumes:

```go
type Store interface {
    Apply(context.Context, Mutation) (Task, []Event, error)
    UpdateLease(context.Context, ProcessLease) error
    ActiveLeases(context.Context) ([]ProcessLease, error)
    RecoverInterrupted(context.Context, time.Time) ([]Event, error)
}

type ManagedProcess interface {
    Lease() ProcessLease
    Start(context.Context) error
    Terminate(context.Context, time.Duration) error
    Close(context.Context) error
}
```

- Produces以下字段和 helper：

```go
// activeTask 中紧邻 process ManagedProcess：
leasePersisted bool

func (m *Manager) persistPreparedLease(
    current *activeTask,
    active map[string]*activeTask,
) error
```

- Ownership contract:

```text
process != nil && active owner即将释放
    => leasePersisted == true
    => Store.ActiveLeases 能返回该 ProcessLease
```

- [ ] **Step 1：确认基线、工作树和指定 runtime**

Run:

```powershell
git merge-base --is-ancestor 676fed0ae50a5b3092a44212b5804edf943e1c65 HEAD
git status --short
$go = 'C:\codex_project\unitTest\.worktrees\foundation-protocol-service\.superpowers\runtime\go1.26.5\go\bin\go.exe'
$env:GOCACHE = (Join-Path (Get-Location) '.superpowers\runtime\gocache-prepared-process-lease')
& $go version
& $go test ./apps/test-service/internal/task -count=1
```

Expected:

```text
merge-base exit 0
git status 无输出
go version go1.26.5 windows/amd64
internal/task PASS
```

- [ ] **Step 2：把正常路径旧测试改成 pre-lease RED**

更新 `TestManagerPersistsPreparedLeaseBeforeStartAndRefreshesItAfterStart`，直接检查第一条 mutation：

```go
mutation := f.store.firstMutation()
if mutation.Task.Status != task.StatusQueued ||
    mutation.Expected != task.StatusQueued ||
    len(mutation.Steps) != 0 ||
    len(mutation.Events) != 0 ||
    mutation.PutLease == nil ||
    mutation.PutLease.TaskID != started.ID ||
    mutation.PutLease.TargetProcessGroup != 0 {
    t.Fatalf("prepared lease mutation = %#v", mutation)
}
if got := eventTypes(f.store.eventsForTask(started.ID)); !reflect.DeepEqual(got, []task.EventType{
    task.EventTaskCreated,
    task.EventTaskStarted,
}) {
    t.Fatalf("durable events = %v", got)
}
if got := f.publisher.types(); !reflect.DeepEqual(got, []task.EventType{
    task.EventTaskCreated,
    task.EventTaskStarted,
}) {
    t.Fatalf("published events = %v", got)
}
lease := f.store.lease(started.ID)
if lease.TargetProcessGroup != 42 ||
    lease.HostPID != 41 ||
    lease.ServiceInstanceID != testID(99) {
    t.Fatalf("refreshed lease = %#v", lease)
}
```

该 RED 不依赖 sleep。旧实现的第一条 mutation 同时把 Task/Step改为 running 并写 `task.started`，因此 `mutation.Task.Status == running` 且 `len(mutation.Events) == 1`。

- [ ] **Step 3：运行正常路径 RED**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^TestManagerPersistsPreparedLeaseBeforeStartAndRefreshesItAfterStart$' -count=1 -v
```

Expected:

```text
FAIL
prepared lease mutation 的 Task.Status 为 running，或 Events 非空
```

- [ ] **Step 4：增加 Prepare first-cause 与 non-nil error RED**

扩展 `fakeProcessFactory`：

```go
type fakeProcessFactory struct {
    mu sync.Mutex
    next *fakeProcess
    prepareErr error
    prepareBlockAt int
    prepareBlock <-chan struct{}
    prepareEntered chan struct{}
    prepareCanceled chan struct{}
    processOnPrepareError bool
}
```

在 `Prepare` 持锁读取配置时同时捕获：

```go
processOnPrepareError := f.processOnPrepareError
```

把 `Prepare` 的 error return改为：

```go
if prepareErr != nil {
    if processOnPrepareError && process != nil {
        process.mu.Lock()
        process.lease.TaskID = taskID
        process.lease.ServiceInstanceID = serviceID
        process.mu.Unlock()
        return process, prepareErr
    }
    return nil, prepareErr
}
```

新增表驱动测试：

```go
func TestManagerPersistsPreparedLeaseBeforeHandlingClaimedCause(t *testing.T) {
    tests := []struct {
        name string
        claim func(*managerFixture, string)
        want task.Outcome
    }{
        {name: "cancel", want: task.OutcomeCancelled},
        {name: "timeout", want: task.OutcomeTimedOut},
        {name: "shutdown", want: task.OutcomeInterrupted},
    }
    // 每个 case：
    // 1. block Prepare并启动 Start；
    // 2. 用现有 public Cancel、fake clock或 Shutdown claim cause；
    // 3. release Prepare，使其返回 non-nil Process；
    // 4. 断言第一条 Apply 是 queued/no-event PutLease；
    // 5. 断言 Start calls=0；
    // 6. 等待 bounded Terminate/Close；
    // 7. 断言 terminal outcome 等于 want，lease 已删除。
}
```

新增：

```go
func TestManagerPrepareErrorWithProcessPersistsLeaseBeforeCleanup(t *testing.T) {
    f := newManagerFixture(t)
    f.processes.prepareErr = errors.New("prepare returned a cleanup handle")
    f.processes.processOnPrepareError = true

    accepted, err := f.manager.Start(context.Background(), task.StartRequest{
        IdempotencyKey: testID(96),
        Scenario: task.ScenarioSuccess,
        Timeout: time.Second,
    })
    if err != nil {
        t.Fatal(err)
    }
    if f.process.startCalls() != 0 {
        t.Fatalf("Start calls = %d, want 0", f.process.startCalls())
    }
    if accepted.Status != task.StatusQueued {
        t.Fatalf("accepted task = %#v, want queued until cleanup", accepted)
    }
    first := f.store.firstMutation()
    if first.Task.Status != task.StatusQueued ||
        len(first.Events) != 0 ||
        first.PutLease == nil {
        t.Fatalf("first mutation = %#v, want queued pre-lease", first)
    }
    f.awaitTerminate(t, 1)
    f.awaitProcessClose(t, f.process)
    finished := f.awaitTask(t, accepted.ID, task.StatusFinished)
    if finished.Outcome != task.OutcomeInfrastructureFailed {
        t.Fatalf("outcome = %s", finished.Outcome)
    }
}
```

测试 cleanup synchronization 必须使用现有 `prepareEntered`、`prepareCanceled`、Terminate/Close counters和 channel barrier；不得新增固定 sleep。

- [ ] **Step 5：运行 first-cause/non-nil error RED**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^(TestManagerPersistsPreparedLeaseBeforeHandlingClaimedCause|TestManagerPrepareErrorWithProcessPersistsLeaseBeforeCleanup)$' -count=1 -v
```

Expected:

```text
FAIL
旧实现遇到 claimed cause 时没有 queued pre-lease；
旧 fake ProcessFactory 的 Prepare error 路径不会把 non-nil Process交给 Manager。
```

- [ ] **Step 6：增加 queued Prepared Process 的 Publisher circuit RED**

在 `TestManagerPublisherFailureCloseErrorHandsOffRecoveryWithoutTerminalization` 旁新增：

```go
func TestManagerPublisherFailureCloseErrorHandsOffQueuedPreparedLease(t *testing.T)
```

测试顺序必须通过 channel barrier固定：

1. Task A 的 `Prepare` 进入 block。
2. 把 Task B `Start` command 排在 Task A cancel-delivery command 前。
3. claim Task A cancellation。
4. release Task A Prepare，使其返回 non-nil Process。
5. Task A 先提交 queued pre-lease，但不进入 running。
6. Task B 的 `task.created` Publish panic，触发 Publisher circuit。
7. Task A `Terminate` 和 `Close` 返回 error。

最终断言：

```go
durableA, err := f.store.Get(context.Background(), taskAID)
if err != nil ||
    durableA.Status != task.StatusQueued ||
    durableA.Outcome != "" {
    t.Fatalf("prepared task after handoff = %#v, %v", durableA, err)
}
lease := f.store.lease(taskAID)
if lease.TaskID != taskAID || lease.HostPID == 0 {
    t.Fatalf("queued recovery lease = %#v", lease)
}
if got := eventTypes(f.store.eventsForTask(taskAID)); !reflect.DeepEqual(got, []task.EventType{
    task.EventTaskCreated,
}) {
    t.Fatalf("prepared task durable events = %v", got)
}
var publishedA []task.Event
for _, event := range f.publisher.events() {
    if event.TaskID == taskAID {
        publishedA = append(publishedA, event)
    }
}
if len(publishedA) != 1 || publishedA[0].Type != task.EventTaskCreated {
    t.Fatalf("prepared task published events = %#v", publishedA)
}
if len(f.artifacts.summariesCopy()) != 0 {
    t.Fatal("prepared recovery handoff created terminal artifact")
}
if f.process.startCalls() != 0 ||
    f.process.terminateCalls() != 1 ||
    f.process.closeCalls() != 1 {
    t.Fatalf("process calls: start=%d terminate=%d close=%d",
        f.process.startCalls(), f.process.terminateCalls(), f.process.closeCalls())
}
```

若现有 fixture无法证明 Task B command 已先入队，在 `manager_cause_test.go` 使用同包 `startCommand`/`taskIDCommand` 和容量明确的 `manager.commands` 构造相同顺序；不得用调度概率或 `time.Sleep`。

- [ ] **Step 7：运行 Publisher circuit queued handoff RED**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailureCloseErrorHandsOffQueuedPreparedLease$' -count=1 -v
```

Expected:

```text
FAIL
旧实现 durable Task A 没有 lease；或当前 f923f8b Close-error handoff 在无 lease时删除 active owner。
```

- [ ] **Step 8：增加 pre-lease Store failure RED**

为 fake Store增加精确 mutation predicate failure，不再依赖全局 `failApply`：

```go
type fakeStore struct {
    mu sync.Mutex
    tasks map[string]task.Task
    leases map[string]task.ProcessLease
    mutations []task.Mutation
    applyCalls int
    failApplyMatch func(task.Mutation) error
}
```

在 `Apply` 的 mutation commit前调用：

```go
if s.failApplyMatch != nil {
    if err := s.failApplyMatch(mutation); err != nil {
        return task.Task{}, nil, err
    }
}
```

新增：

```go
func TestManagerPreparedLeaseConflictKeepsOwnerUntilCleanup(t *testing.T)
func TestManagerPreparedLeaseStoreFailureKeepsOwnerWhenCloseFails(t *testing.T)
```

识别 pre-lease mutation的 predicate：

```go
func isPreparedLeaseMutation(m task.Mutation) bool {
    return m.PutLease != nil &&
        m.Task.Status == m.Expected &&
        len(m.Steps) == 0 &&
        len(m.Events) == 0 &&
        len(m.Artifacts) == 0 &&
        !m.DeleteLease
}
```

`ErrConflict + 首次 Close error` case 断言：

```text
Start=0
Manager 不因 conflict进入 Store circuit
Terminate/Close 被调用
首次 Close error 后 active owner仍存在
首次 Close error 后无 task.started、artifact、task.finished Publisher side effect
显式 Shutdown retry的 Close成功后 Task才进入 finished/infrastructure_failed
最终 active owner释放
```

`ErrStorageUnavailable + Close error` case 断言：

```go
if f.manager.Healthy() {
    t.Fatal("manager remained healthy after prepared lease Store failure")
}
if lease := f.store.lease(taskID); lease.TaskID != "" {
    t.Fatalf("failed pre-lease unexpectedly persisted %#v", lease)
}
shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
defer cancel()
if err := f.manager.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
    t.Fatalf("Shutdown error = %v, want deadline while no durable handoff exists", err)
}
if f.process.closeCalls() != 1 {
    t.Fatalf("Close calls = %d, want one bounded attempt", f.process.closeCalls())
}
```

测试结束 cleanup必须把 fake Close error清空，调用第二次显式 Shutdown并证明最终收敛，避免测试留下 goroutine：

```go
f.process.mu.Lock()
f.process.closeErr = nil
f.process.mu.Unlock()
retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
defer cancelRetry()
if err := f.manager.Shutdown(retry); err != nil {
    t.Fatalf("retry Shutdown = %v", err)
}
```

- [ ] **Step 9：运行 pre-lease failure RED**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^TestManagerPreparedLease(Conflict|StoreFailure)' -count=1 -v
```

Expected:

```text
FAIL
生产代码尚无独立 pre-lease mutation和 leasePersisted ownership gate。
```

- [ ] **Step 10：增加 TaskStore queued lease recovery contract RED/GREEN guard**

在 `TestRecoverInterruptedFinishesAllActiveTasksAndDeletesLeases` 中给 queued Task写入 lease：

```go
queuedLease := task.ProcessLease{
    TaskID: queued.ID,
    HostPID: 43,
    HostStartIdentity: "queued-prepared",
    ServiceInstanceID: id(94),
}
if _, _, err := store.Apply(ctx, task.Mutation{
    Task: queued,
    Expected: task.StatusQueued,
    PutLease: &queuedLease,
}); err != nil {
    t.Fatal(err)
}
```

在调用 `RecoverInterrupted` 前先断言：

```go
leases, err := store.ActiveLeases(ctx)
if err != nil || len(leases) != 2 {
    t.Fatalf("ActiveLeases before recovery = %#v, %v", leases, err)
}
```

recovery后同时检查 queued/running lease均被物理删除：

```go
for _, taskID := range []string{queued.ID, running.ID} {
    var count int
    if err := store.db.QueryRow(
        `SELECT COUNT(*) FROM process_leases WHERE task_id=?`,
        taskID,
    ).Scan(&count); err != nil || count != 0 {
        t.Fatalf("physical lease count for %s = %d, %v", taskID, count, err)
    }
}
```

Run:

```powershell
& $go test ./apps/test-service/internal/taskstore -run '^TestRecoverInterruptedFinishesAllActiveTasksAndDeletesLeases$' -count=1 -v
```

Expected: PASS。该测试锁定现有 Store/schema已经支持 queued lease；若失败，先确认是测试错误，禁止通过 migration扩大范围。

- [ ] **Step 11：增加 Runtime queued Prepared lease recovery integration**

新增 seed helper：

```go
func seedQueuedPreparedTask(t *testing.T, database string, lease task.ProcessLease) {
    t.Helper()
    store, err := taskstore.Open(database)
    if err != nil {
        t.Fatal(err)
    }
    defer store.Close()
    now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
    created := task.Task{
        ID: interruptedTaskID,
        IdempotencyKey: "queued-prepared-recovery-key",
        RequestHash: strings.Repeat("c", 64),
        Kind: task.KindSimulation,
        Request: json.RawMessage(`{"scenario":"hang"}`),
        Scenario: task.ScenarioHang,
        Timeout: time.Minute,
        Status: task.StatusQueued,
        CreatedAt: now,
    }
    created, _, err = store.Create(
        context.Background(),
        created,
        nil,
        task.EventDraft{
            TaskID: created.ID,
            Type: task.EventTaskCreated,
            At: now,
            Payload: []byte(`{"status":"queued"}`),
        },
    )
    if err != nil {
        t.Fatal(err)
    }
    lease.TaskID = created.ID
    if _, _, err := store.Apply(context.Background(), task.Mutation{
        Task: created,
        Expected: task.StatusQueued,
        PutLease: &lease,
    }); err != nil {
        t.Fatal(err)
    }
}
```

新增：

```go
func TestOpenCleansQueuedPreparedLeaseBeforeInterruptedRecovery(t *testing.T) {
    root := filepath.Join(t.TempDir(), "data")
    layout, err := PrepareDataDir(root)
    if err != nil {
        t.Fatal(err)
    }
    seedQueuedPreparedTask(t, layout.Database, testLease())
    runner := &recordingRunner{}
    active, err := Open(Config{
        DataDir: root,
        ServiceExecutable: os.Args[0],
        Platform: platformForTest(),
        Clock: task.RealClock{},
        NewID: task.NewID,
        TerminationGrace: time.Millisecond,
        dependencies: testDependencies(runner, nil),
    })
    if err != nil {
        t.Fatal(err)
    }
    defer active.Close()
    cleaned := runner.cleanupLeases()
    if len(cleaned) != 1 || cleaned[0].TaskID != interruptedTaskID {
        t.Fatalf("queued prepared cleanup leases = %#v", cleaned)
    }
    got, err := active.Get(context.Background(), interruptedTaskID)
    if err != nil ||
        got.Status != task.StatusFinished ||
        got.Outcome != task.OutcomeInterrupted {
        t.Fatalf("recovered queued prepared task = %#v, %v", got, err)
    }
    if leases := activeLeases(t, layout.Database); len(leases) != 0 {
        t.Fatalf("recovery retained leases %#v", leases)
    }
}
```

Run:

```powershell
& $go test ./apps/test-service/internal/runtime -run '^TestOpenCleansQueuedPreparedLeaseBeforeInterruptedRecovery$' -count=1 -v
```

Expected: PASS。Runtime production code不应修改。

- [ ] **Step 12：实现 per-process `leasePersisted` 状态**

在 `activeTask` 中增加：

```go
leasePersisted bool
```

更新 `setCurrentProcess`：

```go
func (m *Manager) setCurrentProcess(current *activeTask, process ManagedProcess) {
    current.process = process
    current.leasePersisted = false
    current.processCompleted = false
    current.terminating = false
    current.terminationComplete = false
    current.terminationFailed = false
    current.closeStarted = false
    current.closeComplete = false
    current.closeFailed = false
    current.cleanupWithoutDone = false
    current.failPendingStep = false
    current.watcherStop = make(chan struct{})
}
```

当成功 Close 后准备下一 Step并执行 `current.process = nil` 时，同步：

```go
current.process = nil
current.leasePersisted = false
```

`persistSuccessfulStep` 的 `DeleteLease: true` commit成功后、调用 Publisher前同步：

```go
current.task = stored
current.leasePersisted = false
return publishCommitted(m, events, active)
```

`finishExecution` 的 terminal mutation成功且 `current.process != nil` 时同步：

```go
current.task = finished
if current.process != nil {
    current.leasePersisted = false
}
```

这里必须在 Store commit成功后清零，不能在 mutation前清零。Store failure时 durable lease仍可能存在，错误清零会破坏 recovery handoff。

审计手工构造 `activeTask` 的测试 fixture：凡是模拟已持久化 running Process 的 fixture，显式设置 `leasePersisted: true`；模拟尚未写 lease的 Prepared Process则保持 `false`。

不得把 `leasePersisted` 设为 Task-wide flag；它只属于当前 `ManagedProcess`。

- [ ] **Step 13：实现 pre-lease helper**

在 `manager_execution.go` 增加：

```go
func (m *Manager) persistPreparedLease(
    current *activeTask,
    active map[string]*activeTask,
) error {
    lease := current.process.Lease()
    lease.TaskID = current.task.ID
    lease.ServiceInstanceID = m.serviceInstanceID
    stored, _, err := m.store.Apply(context.Background(), Mutation{
        Task: current.task,
        Expected: current.task.Status,
        PutLease: &lease,
    })
    if err != nil {
        if errors.Is(err, ErrStorageUnavailable) {
            m.tripStorage(active)
        }
        return err
    }
    current.task = stored
    current.leasePersisted = true
    return nil
}
```

约束：

- helper不接受 event、Step或artifact；
- helper不调用 Publisher；
- 只有 commit成功后才能设置 `leasePersisted=true`；
- `ErrConflict` 不触发 Store circuit；
- `ErrStorageUnavailable` 才触发 Store circuit；
- first-cause不在 helper内 resolve或替换。

- [ ] **Step 14：重排 `startNextStep`**

把 `Prepare` 后逻辑改为以下顺序：

```go
process, prepareErr := m.processes.Prepare(
    current.execution.ctx,
    step.Process,
    current.task.ID,
    m.serviceInstanceID,
)
if process == nil {
    if prepareErr == nil {
        prepareErr = errors.New("process preparation returned no process")
    }
    return m.finishPendingStep(current, active)
}

m.setCurrentProcess(current, process)
if leaseErr := m.persistPreparedLease(current, active); leaseErr != nil {
    cause := current.execution.resolve(OutcomeInfrastructureFailed)
    current.cleanupWithoutDone = true
    current.failPendingStep = cause == OutcomeInfrastructureFailed
    current.processCompleted = true
    if !current.terminating {
        m.terminate(current)
    }
    m.maybeStartClose(current)
    return leaseErr
}

if cause := current.execution.currentCause(); cause != "" {
    current.cleanupWithoutDone = true
    return nil
}
if prepareErr != nil {
    current.execution.resolve(OutcomeInfrastructureFailed)
    current.cleanupWithoutDone = true
    current.failPendingStep = true
    current.processCompleted = true
    m.terminate(current)
    m.maybeStartClose(current)
    return nil
}
```

随后 existing running mutation保留 Task/Step/event，但删除：

```go
lease := process.Lease()
lease.TaskID = current.task.ID
lease.ServiceInstanceID = m.serviceInstanceID
PutLease: &lease,
```

`Start` 后两个 `UpdateLease` 分支保持不变。

如果实现中 `Prepare error + non-nil Process` 需要等待 cleanup后才 terminalize，复用现有 command-loop cleanup/finish路径；不得在 Process仍可能存活时直接删除 lease并完成 Task。

- [ ] **Step 15：修正 circuit Close error ownership gate**

把 `closeResultCommand` error分支改为：

```go
if value.err != nil {
    if !m.circuitFailed() &&
        !current.recoveryRequired &&
        !current.leasePersisted {
        outcome := current.execution.resolve(OutcomeInfrastructureFailed)
        current.failPendingStep = outcome == OutcomeInfrastructureFailed
    }
    m.healthy.Store(false)
    recoveryHandoffSafe := current.task.Status != StatusFinished &&
        current.leasePersisted
    if (m.circuitFailed() || current.recoveryRequired) && recoveryHandoffSafe {
        current.recoveryRequired = true
        m.stopActive(current)
        delete(active, value.taskID)
    } else if m.circuitFailed() ||
        current.recoveryRequired ||
        !current.leasePersisted {
        current.closeStarted = false
        current.closeComplete = false
        current.closeFailed = true
    } else {
        current.closeStarted = false
        current.closeComplete = false
        current.closeFailed = true
        if current.task.Status != StatusFinished {
            if _, err := m.finishAfterCloseFailure(current, active); err != nil {
                m.abandon(current)
            }
        }
    }
}
```

该分支必须保持：

- stale `closeGeneration` guard；
- durable handoff不写 Store/artifact/Publisher；
- durable handoff同时要求 `leasePersisted=true` 与 Task nonterminal，因为 `ActiveLeases` 不返回 finished Task；
- 任何 `leasePersisted=false && process != nil` 的 Close error都保留 active，不限于 circuit分支；
- `maybeStartClose`等普通 Close路径必须在 `closeFailed=true` 时返回；后续显式 Shutdown使用独立 retry入口触发下一次 bounded Close；
- 即使 timeout cause已经 claim但 `timeoutCommand`排在 Close error之后消费，timeout、cancel、termination result、process done与其他 cleanup command也不得自动 retry；
- normal非 circuit且无 durable lease的 Close failure不得 terminalize；若尚无 cause，Close error处理时必须先 `resolve(OutcomeInfrastructureFailed)`并按结果设置 `failPendingStep`，随后才能 `healthy.Store(false)`；
- cause/health ordering由同 package确定性 barrier测试覆盖；外部行为测试继续断言最终 outcome与 Step状态，不再依赖 watcher scheduling证明内部 ordering；
- 已有 cancel/timeout等 cause不得被 Close failure或后续 Shutdown覆盖；显式 Shutdown retry Close成功后才按固定的 first-cause与最终 outcome terminalize；
- `leasePersisted=true` 的 normal非 circuit Close failure保持既有语义。

- [ ] **Step 16：运行 focused GREEN**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^(TestManagerPersistsPreparedLeaseBeforeStartAndRefreshesItAfterStart|TestManagerPersistsPreparedLeaseBeforeHandlingClaimedCause|TestManagerPrepareErrorWithProcessPersistsLeaseBeforeCleanup|TestManagerPublisherFailureCloseErrorHandsOffQueuedPreparedLease|TestManagerPreparedLease)' -count=1 -v
& $go test ./apps/test-service/internal/taskstore -run '^TestRecoverInterruptedFinishesAllActiveTasksAndDeletesLeases$' -count=1 -v
& $go test ./apps/test-service/internal/runtime -run '^TestOpenCleansQueuedPreparedLeaseBeforeInterruptedRecovery$' -count=1 -v
```

Expected: PASS。

如果 first-cause、normal Close或旧 Publisher tests失败，修复 production ordering/state，不得删除断言或改成 sleep。无 durable lease的 normal Close回归必须断言：首次 Close error后 Task仍 nonterminal且 owner仍 active；无先行 cause时先固定 infrastructure failure，有 cancel/timeout时保留原 cause；显式 Shutdown触发 Close retry，retry成功后才出现固定 outcome的 terminal state。

- [ ] **Step 17：运行 multi-step 与既有 lifecycle 回归**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^(TestManagerStartsAndCancelsOneTask|TestManagerRunsPlanStepsSequentially|TestManagerProcessStartFailureIsPersistedAsInfrastructureFailure|TestManagerApplyFailureTripsCircuitWithoutStartingTarget|TestManagerPrepareFailure|TestManagerShutdown|TestManagerPublisherFailure|TestManagerCancellation|TestManagerCloseFailure)' -count=1
```

Expected: PASS。

检查所有受新 pre-lease transaction影响的 exact Apply call-count测试。只在语义确实多出一次 pre-lease时更新 expected count，并同时断言新增 mutation是：

```text
Task.Status == Expected
PutLease != nil
Steps/Events/Artifacts empty
DeleteLease == false
```

既有 normal Close failure回归按 durable ownership分两类：`leasePersisted=true` 保持既有 terminalization；`leasePersisted=false` 采用 ownership-safety窄例外，断言 terminal visibility延迟到显式 Shutdown Close retry成功，且 first-cause与最终 outcome不变。

- [ ] **Step 18：运行 stress 与 race**

Run:

```powershell
& $go test ./apps/test-service/internal/task -run '^(TestManagerPersistsPreparedLease|TestManagerPrepareErrorWithProcess|TestManagerPublisherFailureCloseErrorHandsOffQueuedPreparedLease|TestManagerPreparedLease)' -count=100
& $go test ./apps/test-service/internal/task -run '^TestManagerPublisherFailure' -count=100
& $go test ./apps/test-service/internal/task -run '^(TestManagerCloseFailureUsesClaimedTimeoutBeforeTimeoutCommand|TestManagerTimeoutCommandDoesNotRetryFailedCloseBeforeShutdown)$' -count=1000
& $go test ./apps/test-service/internal/task -run '^TestManagerRecordCloseFailureResolvesCauseBeforePublishingUnhealthy$' -count=1000
& $go test -race ./apps/test-service/internal/task -run '^(TestManagerPersistsPreparedLease|TestManagerPrepareErrorWithProcess|TestManagerPublisherFailure|TestManagerPreparedLease|TestManagerCancellation|TestManagerCloseFailure)' -count=1
```

Expected: PASS，且无 data race、无 goroutine leak、无偶发 deadline。

- [ ] **Step 19：运行 fresh package 与全 internal 回归**

Run:

```powershell
& $go test ./apps/test-service/internal/task -count=1
& $go test ./apps/test-service/internal/taskstore -count=1
& $go test ./apps/test-service/internal/runtime -count=1
& $go test ./apps/test-service/internal/task ./apps/test-service/internal/runtime ./apps/test-service/internal/session -count=1
& $go test ./apps/test-service/internal/... -count=1
git diff --check
```

Expected: 全部 PASS；`git diff --check` 无输出。

如果 sandbox因为 workspace外现有 `GOMODCACHE` lock拒绝访问，保留原 `GOMODCACHE`，按权限规则在 sandbox外重跑相同命令；不得复制或改写 module cache来掩盖问题。

- [ ] **Step 20：审计范围与不变量**

Run:

```powershell
git diff --stat 676fed0ae50a5b3092a44212b5804edf943e1c65..HEAD
git diff -- apps/test-service/internal/task/ports.go `
  apps/test-service/internal/taskstore/migrations `
  packages/protocol-schema
rg -n 'leasePersisted|persistPreparedLease|PutLease|publishAll\(' `
  apps/test-service/internal/task/manager.go `
  apps/test-service/internal/task/manager_execution.go
```

Expected:

```text
ports.go 无 diff
taskstore migrations 无 diff
protocol schema 无 diff
pre-lease helper 无 publishAll/ArtifactWriter
running mutation不再首次 PutLease
每个 Process set时 leasePersisted=false
每个 durable handoff前 leasePersisted=true
```

- [ ] **Step 21：更新中文证据报告与 ledger**

在新 Task 1 report追加：

```markdown
## Prepared Process lease architecture reset

- BASE 与 commit range
- 原 load-bearing finding
- 已批准 spec 与 plan
- RED 原始命令和关键失败
- 两阶段 lease GREEN
- queued recovery integration
- pre-lease Store failure double-fault证据
- stress/race/fresh回归
- scope审计
- self-review与开放 concern
```

更新新 ledger：

```text
Task 1: architecture reset approved (design 676fed0)
Task 1: ownership-safety ruling approved (lease-free Close error retains active owner; terminal visibility is deferred until explicit Close retry succeeds)
Task 1: prepared-process lease implementation complete (review pending; commit 使用 Step 22 返回的完整 SHA)
```

更新旧 Phase 3A ledger：

```text
Task 3: breaker architecture decision approved (prepared-process durable lease design 676fed0)
Task 3: ownership-safety ruling approved (lease-free Close error retains active owner; terminal visibility is deferred until explicit Close retry succeeds)
Task 3: prepared-process lease ownership implementation complete (review pending; commit 使用 Step 22 返回的完整 SHA)
```

同时关闭旧 Publisher ownership ledger中的 load-bearing `BLOCKED` finding，记录该 durable lease实现与 ownership-safety裁决已经提供明确 owner/handoff边界。

报告必须记录真实命令、exit status和关键输出；不得只写“tests passed”。

- [ ] **Step 22：提交 tracked 实现**

Run:

```powershell
git status --short
git add -- `
  docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md `
  docs/superpowers/plans/2026-07-27-prepared-process-lease-ownership-plan.md `
  apps/test-service/internal/task/manager.go `
  apps/test-service/internal/task/manager_execution.go `
  apps/test-service/internal/task/manager_test.go `
  apps/test-service/internal/task/manager_cause_test.go `
  apps/test-service/internal/task/manager_execution_test.go `
  apps/test-service/internal/taskstore/sqlite_test.go `
  apps/test-service/internal/runtime/runtime_test.go
git diff --cached --check
git commit -m "fix: persist prepared process recovery leases"
git status --short
```

Expected:

```text
commit成功
tracked worktree clean
ignored SDD报告保留在对应 plan workspace
```

- [ ] **Step 23：独立 review gate**

reviewer必须同时给出：

- spec compliance verdict；
- code/task quality verdict；
- 原 load-bearing finding是否 `ADDRESSED`；
- `queued + lease` 是否无客户端可见 running副作用；
- `leasePersisted=false` 是否仍有 owner释放路径；
- `leasePersisted=true` handoff是否由真实 `ActiveLeases`/Runtime recovery证明；
- first-cause、Store/Publisher fault domain、Close retry与 Shutdown是否回归；特别检查 `closeFailed`只能由显式 Shutdown bypass；
- Critical/Important/Minor findings；
- `CLEAN — Ready` 或 `Changes required`。

reviewer不得以测试通过替代代码级 lifecycle推理，也不得要求实现 Task 4 或第二套 recovery journal。

## 完成门禁

- [ ] non-nil Prepared Process 在后续 failure boundary前已经拥有 queued/running durable lease。
- [ ] pre-lease mutation没有 Step/event/artifact/Publisher副作用。
- [ ] Task/Step running与 `task.started` 保持原有可见时机。
- [ ] claimed cancel/timeout/shutdown不被 Prepare error或 infrastructure failure覆盖。
- [ ] Publisher circuit Close error只在 `leasePersisted=true` 时释放 active owner。
- [ ] pre-lease Store failure + Close failure保留 active owner并受 caller deadline约束。
- [ ] Runtime recovery真实清理 queued Prepared lease。
- [ ] normal Start、multi-step、UpdateLease与 Publisher fail-closed无回归；无 durable lease的 normal Close failure仅延迟 terminal visibility，普通 command不得 retry，并由显式 Shutdown Close retry保留 first-cause与最终 outcome。
- [ ] Protocol/interface/schema/Task 4均无越界。
- [ ] focused、stress、race、Task、TaskStore、Runtime、Session、全 internal与 diff check全部 fresh PASS。
