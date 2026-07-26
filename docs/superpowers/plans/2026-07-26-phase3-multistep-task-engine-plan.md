# Phase 3A：多步骤 Task Engine 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 把 Phase 2 单进程 simulation Task Engine 升级为 Service-owned、可持久化、可取消的多步骤 `ExecutionPlan`，同时保持 Protocol v1.1 行为不变。

**架构：** `task.Manager` 接收已经由 Service 内部构造并验证的 `ExecutionPlan`，按顺序为每个 Step 创建受控进程。SQLite 保存规范化请求、plan fingerprint 和脱敏 Step snapshot，不保存 environment；simulation 通过内部 planner 迁移到同一引擎，作为多步骤实现的回归垂直切片。

**技术栈：** Go 1.26.5、SQLite、现有 Process Controller、现有 EventBroker/ArtifactStore、TypeScript/Protocol v1.1 回归套件。

## 全局约束

- 基础提交为已确认 Phase 3 设计提交，且历史必须包含 `f5190ef9230469e913f8f66725c0c46e2936d9bf`。
- Protocol 不能构造或传入 `ExecutionPlan`、`ProcessSpec`、executable、args、environment 或 working directory。
- Task timeout 覆盖完整计划；取消、超时或 Step 失败后不得启动后续 Step。
- v1.1 simulation 请求、Task Snapshot、事件枚举和 artifact wire shape 保持不变。
- 运行中的 Service 重启后仍不重新附着原进程。
- SQLite migration 必须原子化并通过 `PRAGMA foreign_key_check`。
- 所有测试按 TDD 顺序先观察失败，再写最小实现。
- 所有 Markdown 使用中文，English technical terms 保持 English 格式。

---

### Task 1：ExecutionPlan 领域模型与防御性校验

**文件：**
- 创建： `apps/test-service/internal/task/plan.go`
- 创建： `apps/test-service/internal/task/plan_test.go`
- 修改： `apps/test-service/internal/task/model.go`
- 修改： `apps/test-service/internal/task/ports.go`
- 修改： `apps/test-service/internal/task/manager.go`

**接口：**
- 输入： 现有 `task.ProcessSpec`、`task.ProcessFactory` 和 Phase 2 Task 状态。
- 输出：

```go
type Kind string
const (
	KindSimulation Kind = "simulation"
	KindCMakeBuild Kind = "cmake_build"
)

type StepKind string
const (
	StepSimulation StepKind = "simulation"
	StepConfigure StepKind = "configure"
	StepBuild StepKind = "build"
)

type StepStatus string
const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

type CommandSummary struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type ExecutionStep struct {
	ID      string
	Kind    StepKind
	Process ProcessSpec
	Public  CommandSummary
}

type ExecutionPlan struct {
	Version     int
	Fingerprint string
	Steps       []ExecutionStep
}

type ExecutionBoundary interface {
	ValidateExecutable(path string) error
	ValidateWorkingDirectory(path string) error
}

type StartRequest struct {
	IdempotencyKey string
	Kind           Kind
	Request        json.RawMessage
	Timeout        time.Duration
	Plan           ExecutionPlan
	Boundary       ExecutionBoundary
}

func ValidatePlan(ExecutionPlan, ExecutionBoundary) error
func FingerprintPlan(ExecutionPlan) string
```

- [ ] **Step 1：写出失败的 Plan 校验测试**

在 `plan_test.go` 添加表驱动测试，至少覆盖空 executable、boundary 不允许的 executable、重复 Step ID、NUL 参数、空 working directory、workspace/data root 外 cwd、未知 Step kind、环境中的 service token key，以及合法的双 Step 计划：

```go
func TestValidatePlanRejectsUnsafeSpecs(t *testing.T) {
	valid := ExecutionPlan{Version: 1, Steps: []ExecutionStep{{
		ID: "configure", Kind: StepConfigure,
		Process: ProcessSpec{Executable: "cmake", Args: []string{"-S", "src", "-B", "build"}, Dir: "src"},
		Public: CommandSummary{Executable: "cmake", Args: []string{"-S", "<workspace>", "-B", "<build>"}},
	}}}
	boundary := fakeBoundary{executables: []string{"cmake"}, roots: []string{"src", "build"}}
	if err := ValidatePlan(valid, boundary); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	unsafe := valid
	unsafe.Steps = append([]ExecutionStep(nil), valid.Steps...)
	unsafe.Steps[0].Process.Args = []string{"ok\x00bad"}
	if !errors.Is(ValidatePlan(unsafe, boundary), ErrInvalidArgument) {
		t.Fatal("NUL argument was accepted")
	}
}
```

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/task -run 'TestValidatePlan' -count=1
```

预期： FAIL，原因是 `ExecutionPlan` 或 `ValidatePlan` 尚不存在。

- [ ] **Step 3：实现最小领域模型和校验**

在 `plan.go` 实现固定 `Version == 1`、1–8 个 Step、唯一且受限的 Step ID、已知 kind、非空 executable/Dir、参数与环境 NUL 检查、环境 key 检查和 fingerprint。每个 Step 还必须通过非空 `ExecutionBoundary` 的 executable/cwd 检查。`FingerprintPlan` 对完整执行字段进行规范 JSON + SHA-256，但只持久化 hash；runtime-only boundary 不进入 fingerprint 或数据库；`CommandSummary` 不得包含 environment。

同时在 `model.go` 为 `Task` 增加 `Kind`、`Request`、`PlanFingerprint`、`ActiveStep` 和 `Steps []StepSnapshot`，其中：

```go
type StepSnapshot struct {
	ID         string
	Kind       StepKind
	Status     StepStatus
	StartedAt  *time.Time
	FinishedAt *time.Time
	ExitCode   *int
	ErrorCode  string
}
```

- [ ] **Step 4：运行领域测试和现有 Task 回归**

运行：

```powershell
go test ./apps/test-service/internal/task -count=1
```

预期： PASS；现有状态机测试同时通过。

- [ ] **Step 5：提交领域模型**

```powershell
git add apps/test-service/internal/task/plan.go apps/test-service/internal/task/plan_test.go apps/test-service/internal/task/model.go apps/test-service/internal/task/ports.go apps/test-service/internal/task/manager.go
git commit -m "refactor: define service-owned execution plans"
```

### Task 2：SQLite tasks migration 与 Step persistence

**文件：**
- 创建： `apps/test-service/internal/taskstore/migrations/002_multistep_tasks.sql`
- 创建： `apps/test-service/internal/taskstore/steps.go`
- 修改： `apps/test-service/internal/taskstore/migrations.go`
- 修改： `apps/test-service/internal/taskstore/sqlite.go`
- 修改： `apps/test-service/internal/taskstore/tasks.go`
- 修改： `apps/test-service/internal/taskstore/recovery.go`
- 修改： `apps/test-service/internal/taskstore/sqlite_test.go`
- 修改： `apps/test-service/internal/task/ports.go`

**接口：**
- 输入： Task 1 的 `Kind`、`StepSnapshot` 和规范化 `StartRequest.Request`。
- 输出：

```go
type Mutation struct {
	Task        Task
	Expected    Status
	Steps       []StepMutation
	Events      []EventDraft
	PutLease    *ProcessLease
	DeleteLease bool
	Artifacts   []Artifact
}

type StepMutation struct {
	Step     StepSnapshot
	Expected StepStatus
}

type Store interface {
	Create(context.Context, Task, []StepSnapshot, EventDraft) (Task, []Event, error)
	Apply(context.Context, Mutation) (Task, []Event, error)
}
```

- [ ] **Step 1：写出从 001 升级和原子回滚测试**

在 `sqlite_test.go` 创建只有 migration 001 的数据库，插入完成和活动 simulation task，再以当前 Store 打开。断言：

- `kind == simulation`；
- `request_json == {"scenario":"success"}`；
- `scenario` 保留；
- `task_steps` 可写入/读取；
- `PRAGMA foreign_key_check` 无结果；
- 注入 migration 失败后旧数据库仍可重新打开。

测试核心断言：

```go
if got.Kind != task.KindSimulation || string(got.Request) != `{"scenario":"success"}` {
	t.Fatalf("migration lost task request: %#v", got)
}
if rows := foreignKeyViolations(t, store.db); rows != 0 {
	t.Fatalf("foreign key violations: %d", rows)
}
```

- [ ] **Step 2：运行 migration 测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/taskstore -run 'TestMigration002|TestStepPersistence' -count=1
```

预期： FAIL，原因是 migration 002 和 Step persistence 尚不存在。

- [ ] **Step 3：实现 migration 与 Store mutation**

`002_multistep_tasks.sql` 使用 `tasks_v2` 复制并替换原表，新增：

```sql
kind TEXT NOT NULL CHECK (kind IN ('simulation','cmake_build')),
scenario TEXT,
request_json TEXT NOT NULL CHECK (json_valid(request_json)),
plan_fingerprint TEXT NOT NULL DEFAULT '',
active_step TEXT NOT NULL DEFAULT '',
CHECK ((kind = 'simulation' AND scenario IS NOT NULL) OR
       (kind = 'cmake_build' AND scenario IS NULL))
```

新增 `task_steps`：

```sql
CREATE TABLE task_steps (
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  step_ordinal INTEGER NOT NULL CHECK (step_ordinal >= 0),
  step_id TEXT NOT NULL,
  step_kind TEXT NOT NULL CHECK (step_kind IN ('simulation','configure','build')),
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped')),
  started_at TEXT,
  finished_at TEXT,
  exit_code INTEGER,
  error_code TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (task_id, step_ordinal),
  UNIQUE (task_id, step_id)
);
```

在 migration 文件首行加入 `-- unit-test-ide: foreign-keys-off`。`migrations.go` 在 transaction 前关闭 foreign keys，提交后立即恢复并运行 `PRAGMA foreign_key_check`；任何错误都恢复连接设置并返回 `ErrStorageUnavailable`。

`steps.go` 负责 transaction 内的 Step insert/update 与 Task 读取后的 Step hydration。不得把 environment 或 `ProcessSpec` 写入数据库。

- [ ] **Step 4：运行 Store 全套测试**

运行：

```powershell
go test ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/taskstore -count=1
```

预期： 两条命令 PASS，migration checksum 和 prefix 测试已更新。

- [ ] **Step 5：提交 persistence**

```powershell
git add apps/test-service/internal/taskstore apps/test-service/internal/task/ports.go
git commit -m "feat: persist multistep task plans"
```

### Task 3：Manager 顺序执行多个 Step

**文件：**
- 创建： `apps/test-service/internal/task/manager_execution.go`
- 创建： `apps/test-service/internal/task/manager_execution_test.go`
- 修改： `apps/test-service/internal/task/manager.go`
- 修改： `apps/test-service/internal/task/manager_internal_test.go`
- 修改： `apps/test-service/internal/task/manager_test.go`
- 修改： `apps/test-service/internal/runtime/runtime.go`
- 修改： `apps/test-service/internal/session/session.go`

**接口：**
- 输入： Task 1 `StartRequest.Plan`，Task 2 Store Step mutation。
- 输出：

```go
func NewSimulationStartRequest(
	idempotencyKey string,
	scenario Scenario,
	timeout time.Duration,
	serviceExecutable string,
	simulationDirectory string,
) (StartRequest, error)
```

`Manager.Start` 只接收内部 `StartRequest`；Protocol v1.1 的 simulation payload 由 `Runtime.StartSimulation` 转换为该请求。simulation boundary 只允许当前 Service executable 与 simulation data directory。

- [ ] **Step 1：写出双 Step 顺序、短路和总 timeout 测试**

使用两个可控 fake process，断言第二个 Step 只有在第一个退出码 0 且 Close 完成后才 Prepare/Start。再覆盖第一个非零、取消和 timeout 时第二个永不启动：

```go
func TestManagerRunsPlanStepsSequentially(t *testing.T) {
	first := newFakeProcess()
	second := newFakeProcess()
	manager := newPlanManager(t, first, second)
	started := startTwoStepTask(t, manager)
	first.complete(ProcessResult{ExitCode: 0})
	second.waitStarted(t)
	second.complete(ProcessResult{ExitCode: 0})
	got := waitFinished(t, manager, started.ID)
	if got.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %s", got.Outcome)
	}
}
```

- [ ] **Step 2：运行聚焦测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/task -run 'TestManagerRunsPlanSteps|TestManagerStopsAfterStepFailure|TestManagerPlanTimeout' -count=1
```

预期： FAIL；当前 Manager 只启动 simulation 单进程。

- [ ] **Step 3：实现 Step 驱动状态机**

把 start/prepare/start-next/finish-step 移入 `manager_execution.go`：

- `Start` 先使用请求携带的 Service-owned boundary 执行 `ValidatePlan`，并校验 supplied fingerprint；
- Task、Step snapshots 和 `task.created` 在一个 transaction 中创建；
- active task 保存完整内存 Plan 和 `nextStep` ordinal；
- 第一个 Step 启动时 Task 从 queued 进入 running；
- 每个 Step 启动前再次执行 boundary 校验，防止排队期间 executable/cwd identity 变化；每个 Step 使用独立 Process lease；
- Step 成功后先持久化 finished Step，再 prepare 下一个；
- 最后 Step 成功后结束 Task；
- Step 非零时将当前 Step 标记 failed、剩余 Step 标记 skipped、Task 结束为 `command_failed`；
- Prepare/Start/Store 错误映射为 `infrastructure_failed`；
- Task timeout timer 只启动一次，不在 Step 间重置。

`Runtime.StartSimulation` 使用 `NewSimulationStartRequest` 并构造 simulation boundary；Session 的 v1.1 `tasks/start` 仍只接收 scenario/timeout，不接触 Plan 或 boundary。

- [ ] **Step 4：运行 Task、Runtime 和 Session 回归**

运行：

```powershell
go test ./apps/test-service/internal/task ./apps/test-service/internal/runtime ./apps/test-service/internal/session -count=1
```

预期： PASS；现有 v1.1 请求仍产生 simulation Task。

- [ ] **Step 5：提交多步骤执行**

```powershell
git add apps/test-service/internal/task apps/test-service/internal/runtime/runtime.go apps/test-service/internal/session/session.go
git commit -m "feat: execute service-owned plans step by step"
```

### Task 4：Step events、输出归属、通用 artifacts 与恢复

**文件：**
- 创建： `apps/test-service/internal/task/manager_artifacts.go`
- 修改： `apps/test-service/internal/task/model.go`
- 修改： `apps/test-service/internal/task/manager.go`
- 修改： `apps/test-service/internal/task/manager_execution.go`
- 修改： `apps/test-service/internal/task/manager_test.go`
- 修改： `apps/test-service/internal/taskstore/recovery.go`
- 修改： `apps/test-service/internal/taskstore/sqlite_test.go`
- 修改： `apps/test-service/internal/artifactstore/store.go`
- 修改： `apps/test-service/internal/artifactstore/store_test.go`

**接口：**
- 输入： Task/Step persistence 与 Phase 2 EventBroker/ArtifactStore。
- 输出：

```go
const (
	EventTaskStepStarted  EventType = "task.step_started"
	EventTaskStepFinished EventType = "task.step_finished"
)

type TaskSummary struct {
	TaskID     string         `json:"taskId"`
	Kind       Kind           `json:"kind"`
	Outcome    Outcome        `json:"outcome"`
	FinishedAt time.Time      `json:"finishedAt"`
	Steps      []StepSnapshot `json:"steps"`
}
```

- [ ] **Step 1：写出事件原子性、Step output 和恢复测试**

断言：

- `task.step_started` 与 Step running/lease 在同一 mutation；
- `task.output` payload 带正确 `stepId`；
- Step finished 后才出现下一个 Step started；
- simulation v1.1 不广播新增 Step event，以维持旧 event enum；
- Task summary 对 simulation 仍能通过旧 wire artifact 投影；
- recovery 把 running/cancelling 任务和所有非终止 Step 变成 interrupted/failed 或 skipped。

事件顺序断言：

```go
want := []EventType{
	EventTaskCreated,
	EventTaskStarted,
	EventTaskOutput,
	EventTaskFinished,
	EventArtifactCreated,
}
assertEventTypes(t, events, want)
```

该 simulation 断言故意不包含新 Step event；`cmake_build` 的新事件在 Phase 3C 启用。

- [ ] **Step 2：运行聚焦测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -run 'Step|Summary|Recover' -count=1
```

预期： FAIL，原因是 Step payload、通用 summary 或 recovery 尚未实现。

- [ ] **Step 3：实现事件、artifact registry 和恢复**

`task.output` payload 改为：

```json
{
  "stepId": "simulation",
  "stream": "stdout",
  "data": "Base64URL",
  "truncated": false
}
```

为 artifact validation 增加按 Task kind 的 writer，而不是把 `scenario` 固定为唯一 JSON shape。simulation 继续生成 wire kind `task-summary`；内部 summary 增加 kind/steps 时，由 simulation writer 输出旧兼容字段。

`RecoverInterrupted` transaction 同时：

- 删除 process lease；
- 将 Task 改为 `finished/interrupted`；
- running Step 改为 failed 并写 `SERVICE_RESTARTED`；
- pending Step 改为 skipped；
- 追加一个 `task.finished` 事件。

- [ ] **Step 4：运行完整 Go 测试和 race**

运行：

```powershell
go test ./apps/test-service/... -count=1
go test -race ./apps/test-service/... -count=1
```

预期： 两条命令 PASS。

- [ ] **Step 5：提交事件、artifact 与恢复**

```powershell
git add apps/test-service/internal/task apps/test-service/internal/taskstore apps/test-service/internal/artifactstore
git commit -m "feat: journal multistep task execution"
```

### Task 5：v1.1 compatibility gate 与架构记录

**文件：**
- 创建： `docs/decisions/0003-service-owned-execution-plans.md`
- 修改： `packages/protocol-schema/test/schema.test.mjs`
- 修改： `packages/test-client/src/client.test.ts`
- 修改： `tools/service-probe/src/probe.test.ts`
- 修改： `tools/workspace-smoke/workspace-smoke.test.mjs`

**接口：**
- 输入： 完成后的多步骤 simulation 引擎。
- 输出： 明确记录“Protocol payload → Runtime typed request → Service-owned ExecutionPlan → Process Controller”的不可绕过边界。

- [ ] **Step 1：增加端到端兼容失败测试**

扩充 contract/E2E：

- v1.1 `tasks/start` 仍拒绝 `kind`、`steps`、`executable`、`args` 和 `env`；
- simulation 的 Task Snapshot 仍满足现有 v1.1 Schema；
- simulation 事件仍全部满足现有 v1.1 event enum；
- E2E 的成功、取消、重连、崩溃恢复继续通过。

Schema 测试输入：

```js
assert.equal(validate({
  protocolVersion: "1.1",
  kind: "request",
  messageId: "0123456789abcdef0123456789abcdef",
  method: "tasks/start",
  sentAt: "2026-07-26T00:00:00Z",
  payload: {
    idempotencyKey: "fedcba9876543210fedcba9876543210",
    scenario: "success",
    timeoutMs: 1000,
    executable: "cmake"
  }
}), false);
```

- [ ] **Step 2：运行完整门禁并观察任何回归**

运行：

```powershell
pnpm verify
```

预期： 如果前面尚有兼容缺口则 FAIL；记录首个真实失败，不同时修改多个组件。

- [ ] **Step 3：只修复门禁揭示的 compatibility projection**

修复范围只能是 v1.1 simulation 投影、Schema fixture 或 E2E fixture；不得向 v1.1 增加字段或新 event enum。新增 ADR 用中文记录：

- Plan 只由 Service 构造；
- SQLite 不保存 environment；
- simulation 是兼容垂直切片；
- Phase 3C 才向 v1.2 暴露 build/Step。

- [ ] **Step 4：重新运行完整验证**

运行：

```powershell
pnpm verify
git diff --check
```

预期： `pnpm verify` PASS，`git diff --check` 无输出。

- [ ] **Step 5：提交 Phase 3A 门禁**

```powershell
git add docs/decisions/0003-service-owned-execution-plans.md packages/protocol-schema/test/schema.test.mjs packages/test-client/src/client.test.ts tools/service-probe/src/probe.test.ts tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "test: preserve protocol v1.1 on multistep engine"
```

## Phase 3A 完成检查

- [ ] `go test ./apps/test-service/... -count=1`
- [ ] `go test -race ./apps/test-service/... -count=1`
- [ ] `pnpm verify`
- [ ] `git diff --check`
- [ ] `git status --short` 为空
- [ ] Manager 在 Task 创建前与每个 Step 启动前执行 Service-owned executable/cwd boundary 校验
- [ ] 独立评审确认没有 Protocol 到 `ExecutionPlan`/`ProcessSpec` 的直接入口
