# Phase 4E：Test Coordinator、运行结果与 Protocol 编排实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 把 Phase 4A–4D 的 Catalog 与 Framework Adapter 接入 Task Engine、Protocol v1.3、Runtime 和 TypeScript Client，形成可取消、可恢复、可重放的 testDiscovery/testRun 垂直切片。

**架构：** Test Coordinator 校验结构化 ID 并生成 Service-owned plan。Task Engine 新增受控 `PlanContinuation` 和 framework result interpreter，以支持 build 后刷新 Catalog、动态添加 list/run step，并把非零 test exit 转换成领域结果。Protocol 仍不能构造 `ProcessSpec`。

**依赖：** Phase 4A、4B、4C、4D。

## 全局约束

- Test Task 不创建嵌套 Build Task；复用 Build Coordinator 的内部 plan preparation。
- 动态 continuation 只能由 Runtime 注册的内部 Test Coordinator 提供。
- queued Test Task 重启后从持久化结构化 request 重新规划，不持久化 executable/args/env。
- assertion/crash/malformed 被完整捕获时 Task 可以 `succeeded`，TestRun 表达领域 outcome。
- build command failure 仍是 Task `command_failed` + TestRun `blocked`。
- cancellation/timeout/interrupted 同时反映到 Task 与 TestRun。
- TestRun 终态和 item results 必须先持久化，再发布终态事件。

---

### Task 1：TestRun、selection snapshot 与 result persistence

**文件：**

- 创建：`apps/test-service/internal/taskstore/migrations/006_test_runs.sql`
- 修改：`apps/test-service/internal/taskstore/migrations.go`
- 创建：`apps/test-service/internal/taskstore/test_runs.go`
- 创建：`apps/test-service/internal/taskstore/test_runs_test.go`
- 创建：`apps/test-service/internal/taskstore/test_results.go`
- 创建：`apps/test-service/internal/taskstore/test_results_test.go`
- 修改：`apps/test-service/internal/taskstore/sqlite_test.go`
- 修改：`apps/test-service/internal/task/ports.go`
- 修改：`apps/test-service/internal/artifactstore/task_sink.go`
- 修改：`apps/test-service/internal/artifactstore/task_sink_test.go`

> 实施说明：Task 1 已完成 Artifact metadata 与 TestRun terminal transaction 的原子关联。`ArtifactSink` 的 test artifact kind 接线保留到 Task 5，在 Task 4 引入 `testDiscovery`/`testRun` Task kind 后统一完成，避免 persistence Task 提前扩大 Task Engine 的可执行面。result persistence 测试集中在 `test_runs_test.go`，领域 canonicalization 测试位于 `testdomain/run_test.go`。

**接口：**

```go
type TestRunRepository interface {
	CreateRun(context.Context, testdomain.TestRun) error
	StartRun(context.Context, string, time.Time) error
	AppendResult(context.Context, string, testdomain.TestItemResult) error
	FinishRun(context.Context, testdomain.TestRun, []task.Artifact) error
	GetRun(context.Context, string) (testdomain.TestRun, error)
	ListRuns(context.Context, testdomain.RunPageRequest) (testdomain.RunPage, error)
}
```

- [x] **Step 1：写出 migration、append 和 terminal transaction 失败测试**

覆盖：

- migration upgrade/rollback；
- runId/taskId/idempotency uniqueness；
- immutable selection snapshot；
- `(runId,itemId,iteration)` uniqueness；
- item result monotonic transition；
- failure detail 和多 source location；
- partial/incomplete；
- finish transaction 失败保持 non-terminal；
- artifact failure 不发布 terminal；
- restart 读取；
- pagination cursor 绑定 query；
- failedFromRun 查询 assertion 和 container-level error。

- [x] **Step 2：运行 Store tests 并确认失败**

```powershell
go test ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -run 'TestRun|TestResult|Migration006' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 normalized tables 与 atomic finish**

SQLite 保存可查询字段；完整 failure/output evidence 通过 ArtifactStore。`FinishRun` 与 Task terminal snapshot 的提交顺序由 Task completion boundary 统一协调。

- [x] **Step 4：运行 Store 全套与 race**

```powershell
go test ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
go test -race ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
```

预期：PASS。

- [x] **Step 5：提交 TestRun persistence**

```powershell
git add apps/test-service/internal/taskstore apps/test-service/internal/task/ports.go apps/test-service/internal/artifactstore
git commit -m "feat: persist test run results"
```

### Task 2：filter、repeat 与 failedFromRun resolver

**文件：**

- 创建：`apps/test-service/internal/testrun/selection.go`
- 创建：`apps/test-service/internal/testrun/selection_test.go`
- 创建：`apps/test-service/internal/testrun/retry.go`
- 创建：`apps/test-service/internal/testrun/retry_test.go`
- 创建：`apps/test-service/internal/testrun/summary.go`
- 创建：`apps/test-service/internal/testrun/summary_test.go`

**接口：**

```go
type TestRunReader interface {
	GetRun(context.Context, string) (testdomain.TestRun, error)
}

func Resolve(
	ctx context.Context,
	catalog testdomain.Catalog,
	request testdomain.Selection,
	previous TestRunReader,
	limits testdomain.Limits,
) (testdomain.SelectionSnapshot, error)
```

- [x] **Step 1：写出 selection/retry/summary 失败测试**

覆盖：

- all/container/group/case；
- exact label/group 和 Unicode case-fold name substring；
- include/exclude ID；
- disabled 默认排除；
- deterministic order；
- repeat iteration 1..100；
- assertion failure → case；
- crash/timeout/malformed incomplete → container；
- cancelled/skipped 不进入 failed rerun；
- stale/deleted ID 拒绝相似替换；
- 多 iteration aggregate summary；
- empty/oversized selection。

- [x] **Step 2：运行 testrun tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testrun -count=1
```

预期：FAIL。

- [x] **Step 3：实现 immutable selection snapshot**

Resolver 不修改 Catalog 或旧 TestRun。failedFromRun 创建新的 selection snapshot，并保存来源 runId。

- [x] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/testrun -count=1
```

预期：PASS。

- [x] **Step 5：提交 Test Selection**

```powershell
git add apps/test-service/internal/testrun
git commit -m "feat: resolve structured test selections"
```

### Task 3：Task Engine continuation 与领域结果 completion

**文件：**

- 修改：`apps/test-service/internal/task/plan.go`
- 修改：`apps/test-service/internal/task/plan_test.go`
- 修改：`apps/test-service/internal/task/ports.go`
- 修改：`apps/test-service/internal/task/manager.go`
- 修改：`apps/test-service/internal/task/manager_execution.go`
- 修改：`apps/test-service/internal/task/manager_execution_test.go`
- 修改：`apps/test-service/internal/task/manager_completion.go`
- 修改：`apps/test-service/internal/task/manager_completion_test.go`
- 修改：`apps/test-service/internal/task/manager_artifacts.go`
- 创建：`apps/test-service/internal/task/continuation_test.go`
- 创建：`apps/test-service/internal/task/domain_result_test.go`
- 修改：`apps/test-service/internal/taskstore/tasks.go`
- 修改：`apps/test-service/internal/taskstore/steps.go`
- 创建：`apps/test-service/internal/taskstore/continuation_steps_test.go`

**内部接口：**

```go
type PlanContinuation interface {
	AfterStep(context.Context, Task, ExecutionStep, StepResult) (Continuation, error)
}

type ResultInterpreter interface {
	Interpret(context.Context, Task, ExecutionStep, ProcessResult) (StepVerdict, error)
}
```

- [x] **Step 1：写出 continuation 安全与 completion 失败测试**

覆盖：

- default step 非零仍 command_failed；
- Test Adapter 验证 assertion 后非零 → step/task succeeded + TestRun failed；
- crash/malformed 已持久化 → task succeeded + TestRun errored；
- parser/result persistence 失败 → infrastructure_failed；
- continuation 只能追加 Service-owned validated step；
- persisted Task 不含 ProcessSpec/continuation/parser；
- cancel/timeout 阻止新 continuation；
- terminal 后 continuation/output 不修改 outcome；
- appended step sequence 单调；
- crash/timeout process tree cleanup 不变。

- [x] **Step 2：运行 Task tests 并确认失败**

```powershell
go test ./apps/test-service/internal/task -run 'Continuation|DomainResult|TestStep' -count=1
```

预期：FAIL。

- [x] **Step 3：实现最小 runtime-only extension**

非 Test Task 不配置 continuation/interpreter，行为 byte-for-byte 保持。Continuation output 重新经过 Manager 的 `ExecutionBoundary.Validate`，不得直接信任 Adapter。

> 实施说明：Task 3 已实现 runtime-only `PlanContinuation`、`ResultInterpreter`、流式 `ResultOutputObserver`，以及当前 step completion 与 appended step snapshot 的 SQLite 原子事务。初始 plan 仍限制为 8 steps；受控 continuation 每次最多追加 256 steps，runtime plan 总量最多 10,000 steps。旧 `StepObserver` 仍只在既有中间 step 上调用，simulation/CMake 非 Test 路径不扩张。具体 CppUTest/Unity/Opaque 的 crash、malformed、assertion 映射与 `testDiscovery`/`testRun` Task kind schema 扩展由 Task 4 接线。

- [x] **Step 4：运行 Task 全套与 race**

```powershell
go test ./apps/test-service/internal/task -count=1
go test -race ./apps/test-service/internal/task -count=1
```

预期：PASS。

- [x] **Step 5：提交 Task Engine test extension**

```powershell
git add apps/test-service/internal/task
git commit -m "feat: interpret test task domain results"
```

### Task 4：Test Coordinator、Discovery/Run plan 与 bounded scheduler

> 实施进度：已完成 TestRun Task/queued TestRun 原子创建、Build
> `PreparePlan` 复用、Catalog revision preflight、build 后 stable-ID
> rebind、test executable pin、framework/Opaque run planning、结果
> interpreter、确定性 bounded wave scheduler，以及跨 Windows/Linux 的
> `ENVIRONMENT_MODIFICATION` runtime-only overlay/unset replay。CTest、
> CppUTest 与 Unity discovery 已改为可取消的 `test-discovery` Task steps，
> continuation 不再直接执行 framework 外部进程。Task 4 已完成：scheduler
> 的每个 wave 现在被编译为一个 runtime-only `ProcessSpec.Batch` step，
> Process Host 在 wave 内并发启动不同 container，同时保持 wave 之间和同
> container invocation 的确定性顺序。每个 batch item 独立计时和终止，
> timeout 经 child result 映射为 `ProcessTimedOut`/`timed_out`，不会把同
> wave 的其他目标误杀。Windows 使用 inner/outer Job；Linux 使用独立 process
> group，并通过 SQLite migration 008 持久化多 group lease，restart recovery
> 在信号前逐一校验 session ownership。Windows 真实 E2E、Go 全套、`go vet`、
> race 与 Linux cross-compile 均通过。

**文件：**

- 创建：`apps/test-service/internal/testrun/coordinator.go`
- 创建：`apps/test-service/internal/testrun/coordinator_test.go`
- 创建：`apps/test-service/internal/testrun/planner.go`
- 创建：`apps/test-service/internal/testrun/planner_test.go`
- 创建：`apps/test-service/internal/testrun/scheduler.go`
- 创建：`apps/test-service/internal/testrun/scheduler_test.go`
- 创建：`apps/test-service/internal/testrun/interpreter.go`
- 创建：`apps/test-service/internal/testrun/interpreter_test.go`
- 创建：`apps/test-service/internal/testrun/discovery_refresh.go`
- 创建：`apps/test-service/internal/testdiscovery/task_execution.go`
- 创建：`apps/test-service/internal/testframework/cpputest/task_discovery.go`
- 创建：`apps/test-service/internal/testframework/unity/task_discovery.go`
- 修改：`apps/test-service/internal/testdiscovery/service.go`
- 修改：`apps/test-service/internal/testdiscovery/service_test.go`
- 修改：`apps/test-service/internal/build/coordinator.go`
- 修改：`apps/test-service/internal/build/coordinator_test.go`
- 修改：`apps/test-service/internal/build/planner.go`
- 修改：`apps/test-service/internal/processhost/host.go`
- 修改：`apps/test-service/internal/processcontrol/process.go`
- 修改：`apps/test-service/internal/processcontrol/runner_windows.go`
- 修改：`apps/test-service/internal/processcontrol/runner_unix.go`
- 修改：`apps/test-service/internal/task/ports.go`
- 修改：`apps/test-service/internal/runtime/runtime.go`
- 修改：`apps/test-service/internal/taskstore/recovery.go`
- 创建：`apps/test-service/internal/taskstore/migrations/008_batch_process_leases.sql`

**接口：**

```go
func (c *Coordinator) StartDiscovery(context.Context, DiscoveryRequest) (task.Task, error)
func (c *Coordinator) StartRun(context.Context, RunRequest) (task.Task, testdomain.TestRun, error)
```

- [x] **Step 1：写出 build→refresh→run 失败测试**

覆盖：

- preflight 在 Task 创建前拒绝 stale Workspace/project/profile；
- discovery 计划 configure-if-needed、default build、CTest JSON、framework list、publish；
- run 计划 build、revision validate/refresh、stable-ID rebind、selection、framework run；
- deleted selected ID 不启动 process；
- CppUTest/Unity/Opaque 分流；
- container 间有界并发；
- 同 container 默认串行；
- RUN_SERIAL exclusive；
- repeat iteration；
- timeout 总预算和 CTest property timeout 上限；
- blocked external command；
- idempotency 同 request 返回同 Task/Run；
- idempotency key 同而 payload 不同拒绝。

- [x] **Step 2：运行 Coordinator tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testrun ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/build -run 'Coordinator|Planner|Scheduler|Rebind' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 Service-owned orchestration**

Build Coordinator 提供内部 `PreparePlan`，不创建嵌套 Task。Test Coordinator 是唯一 continuation provider；每次动态 step 都绑定 Catalog revision、container/item ID 和 Adapter version。

- [x] **Step 4：运行领域全套与 race**

```powershell
go test ./apps/test-service/internal/testrun ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/testframework/... ./apps/test-service/internal/build ./apps/test-service/internal/task -count=1
go test -race ./apps/test-service/internal/testrun ./apps/test-service/internal/testdiscovery -count=1
```

预期：PASS。

- [x] **Step 5：提交 Test Coordinator**

```powershell
git add apps/test-service/internal/testrun apps/test-service/internal/testdiscovery apps/test-service/internal/build apps/test-service/internal/task
git commit -m "feat: orchestrate test discovery and runs"
```

### Task 5：test events、artifacts 与 terminal ordering

> 实施说明：Task 5 已将 TestRun completion 合并到 Task Engine 的 `Store.Apply` 事务。`TestItemResult` 先由 result repository 持久化，随后 Task Manager 才把 runtime-only interpreter 产生的领域事件写入单一 Task journal 并发布；终态时 `test-selection`、`test-results`、`test-run-summary`、diagnostics 与 stdout/stderr metadata、TestRun summary、Task snapshot 和 `test.run.finished` 在同一 SQLite transaction 中提交。TestRun 在首个执行 wave 前先持久化为 `running`；Catalog 只按有界的 container 集合生成 `test.container.discovered`，不按 10,000 个静态 item 展开 journal。Artifact kind 继续作为稳定逻辑名称，物理文件名由 ArtifactStore 使用不可预测 ID 生成。

**文件：**

- 创建：`apps/test-service/internal/testrun/events.go`
- 创建：`apps/test-service/internal/testrun/events_test.go`
- 创建：`apps/test-service/internal/testrun/artifacts.go`
- 创建：`apps/test-service/internal/testrun/artifacts_test.go`
- 修改：`apps/test-service/internal/task/manager_artifacts.go`
- 修改：`apps/test-service/internal/task/manager_completion.go`
- 修改：`apps/test-service/internal/taskstore/events.go`
- 修改：`apps/test-service/internal/taskstore/test_runs.go`
- 修改：`apps/test-service/internal/artifactstore/task_sink.go`

**Artifacts：**

```text
test-catalog.json
test-selection.json
test-results.jsonl
test-run-summary.json
diagnostics.jsonl
stdout/stderr step logs
```

- [x] **Step 1：写出事件顺序和 artifact failure 失败测试**

覆盖：

- discovery/run/container/item started/finished；
- 单 Task sequence 单调；
- output 先 sink 后 event；
- item terminal record 先 Store 后 event；
- TestRun summary/artifacts/Task snapshot 全部完成后才 run.finished；
- 终态后 late output 仅 bounded checkpoint；
- sink/store/event publisher failure ownership；
- 10,000 item 使用 batch/page，不产生 10,000 catalog journal events；
- artifact 中不含 token/env/native secret。

- [x] **Step 2：运行 event/artifact tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testrun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -run 'Event|Artifact|Terminal|Late' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 durable-before-publish boundary**

复用 Phase 2 publisher ownership 和 close-before-terminalization 规则。TestRun completion 不另建并行 journal。

- [x] **Step 4：运行全套与 race**

```powershell
go test ./apps/test-service/internal/testrun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
go test -race ./apps/test-service/internal/testrun ./apps/test-service/internal/task -count=1
```

预期：PASS。

- [x] **Step 5：提交 test events/artifacts**

```powershell
git add apps/test-service/internal/testrun apps/test-service/internal/task apps/test-service/internal/taskstore apps/test-service/internal/artifactstore
git commit -m "feat: persist test events and artifacts"
```

### Task 6：Protocol Session、Runtime、recovery 与 deterministic E2E

> 实施说明：Task 6 已完成 Protocol v1.3 的 strict routing、TypeScript/Go model、Session handler、旧版本 compatibility projection，以及 Runtime 中 Build/TestDiscovery/TestRun Coordinator、repositories 和 restart recovery 的组合。确定性 CMake/CppUTest fixture 覆盖 inspect→discover→run→event replay→failedFromRun，并验证 cancel、reconnect 与 crash recovery。重复发现同一 Catalog revision 现在按内容校验后幂等刷新，冲突内容会被拒绝；`test-selection` 已纳入 v1.3 artifact kind；普通 assertion 的 category/message 会持久化为 FailureDetail，保证 failed rerun 不会扩大选择范围。continuation 的单进程和 batch item 都必须通过同一 `ExecutionBoundary`，计划持久化成功后才允许启动下一进程。

**文件：**

- 修改：`apps/test-service/internal/protocol/envelope.go`
- 修改：`apps/test-service/internal/protocol/envelope_test.go`
- 修改：`apps/test-service/internal/session/session.go`
- 修改：`apps/test-service/internal/session/session_test.go`
- 创建：`apps/test-service/internal/session/v13_test.go`
- 修改：`apps/test-service/internal/server/server.go`
- 修改：`apps/test-service/internal/server/server_test.go`
- 创建：`apps/test-service/internal/server/v13_projection_test.go`
- 修改：`apps/test-service/internal/runtime/runtime.go`
- 修改：`apps/test-service/internal/runtime/runtime_test.go`
- 修改：`apps/test-service/internal/taskstore/recovery.go`
- 修改：`apps/test-service/internal/taskstore/sqlite_test.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main_test.go`
- 修改：`tools/service-probe/build-service.mjs`
- 修改：`tools/service-probe/src/probe.ts`
- 修改：`tools/service-probe/src/probe.test.ts`
- 创建：`tools/service-probe/src/test-framework-fixture.ts`
- 创建：`tools/service-probe/src/test-framework-fixture.test.ts`

- [x] **Step 1：写出 v1.3 routing、compat projection 和 recovery 失败测试**

覆盖：

- negotiation 1.3→1.0；
- strict request decode；
- tests/catalog/runs read；
- old version get/list/cancel/artifact 隐藏 Test Task；
- test events 向旧版本投影为保持 sequence 的 compatibility output；
- Workspace authorization；
- Runtime dependency open/close rollback；
- queued Test Task 重启重新验证 request/revision；
- running Test Task → interrupted；
- item result 保留，未完成 → not_run/service_restarted；
- deterministic inspect→discover→run→failed rerun；
-断线 cursor replay。

- [x] **Step 2：运行 Session/Runtime/Probe tests 并确认失败**

```powershell
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session ./apps/test-service/internal/server ./apps/test-service/internal/runtime ./apps/test-service/internal/taskstore ./apps/test-service/cmd/ctest-fixture -run 'V13|TestRun|Recovery|CTestFixture' -count=1
pnpm --filter @unit-test-ide/service-probe test
```

预期：FAIL。

- [x] **Step 3：实现 Runtime composition 与 version projection**

Runtime 注册 CTest/Framework/Test Coordinator 和 repositories。queued request 只保存结构化 ID/limits；恢复时重建所有 runtime-only plan/parser/boundary。

- [x] **Step 4：运行完整 Phase 4E 门禁**

```powershell
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
git diff --check
```

预期：全部 PASS。

验证结果：protocol generated check、TypeScript build/test、CMake bundle/workspace、Go 全包 test、Windows race、Windows vet、`linux/amd64` cross-build/vet，以及 20 项 deterministic E2E 全部通过。完整 Go/race 门禁因单次运行时长拆为覆盖相同 package 集合的分组执行。

- [x] **Step 5：提交 Protocol/Test Runtime vertical slice**

```powershell
git add apps/test-service packages/test-client tools/service-probe
git commit -m "feat: expose protocol v1.3 test execution"
```

## Phase 4E 完成检查

- [x] TestRun/result migration 与 terminal transaction
- [x] selection/filter/repeat/failedFromRun
- [x] result-aware Task completion
- [x] discovery/run Service-owned continuation
- [x] test events/artifacts durable ordering
- [x] v1.3 Session/Runtime/Client E2E
- [x] v1.0–v1.2 compatibility projection
- [x] cancel/timeout/reconnect/restart recovery
- [x] `pnpm verify`
- [x] 独立评审确认 continuation 不能绕过 ExecutionBoundary
