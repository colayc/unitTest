# Phase 3C：Protocol v1.2 与 Build Orchestration 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 将 Phase 3A 多步骤 Task Engine 与 Phase 3B discovery 层接入 Protocol v1.2 和 TypeScript Client，使客户端可通过结构化 ID inspect workspace、列出 targets 并启动真实 `cmakeBuild`。

**架构：** JSON Schema 定义 v1.2 wire model；Session 只做 strict decode 和 domain projection；`build.Coordinator` 校验 generation/ID 并生成内部 `ExecutionPlan`；Runtime 组合 workspace、CMake、Toolchain、Task Manager 与 ArtifactStore。v1.1 使用兼容 projection，只能看到 simulation Task，但事件 sequence 仍保持连续。

**技术栈：** JSON Schema Draft 2020-12、quicktype、TypeScript 6.0.3、AJV、Go 1.26.5、SQLite、Phase 3A/3B 内部 package。

## 全局约束

- Protocol v1.2 新增 `workspace/inspect`、`cmake/targets/list` 和 `cmakeBuild`。
- v1.0/v1.1 Schema、fixtures、生成模型和 response shape 不变。
- 客户端只提交 `workspaceGeneration`、project/profile/target ID、jobs 和 timeout。
- Protocol payload 不得出现 executable、args、env、workingDirectory、preset path 或 native tool options。
- `workspace/inspect` 不执行项目 configure/build。
- `cmake/targets/list` 没有有效 File API reply 时返回 `CONFIGURE_REQUIRED`。
- Task timeout 是 configure + build 的总时限。
- `task.step_*` 与 `task.diagnostic` 只在 v1.2 原样投影。
- v1.1 event projection 必须维持全局 sequence 连续。
- 产品运行时不联网；本计划 E2E 使用 deterministic CMake fixture。
- 所有 Markdown 使用中文，English technical terms 保持 English 格式。

---

### Task 1：Protocol v1.2 Schema、fixtures 与生成模型

**文件：**
- 创建： `packages/protocol-schema/schema/v1.2/capabilities.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/diagnostic.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/workspace.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/task.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/event.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/artifact.schema.json`
- 创建： `packages/protocol-schema/schema/v1.2/message.schema.json`
- 创建： `packages/protocol-schema/fixtures/v1.2/workspace-inspect.valid.json`
- 创建： `packages/protocol-schema/fixtures/v1.2/targets-list.valid.json`
- 创建： `packages/protocol-schema/fixtures/v1.2/cmake-build-start.valid.json`
- 创建： `packages/protocol-schema/fixtures/v1.2/cmake-build-shell.invalid.json`
- 创建： `packages/protocol-schema/fixtures/v1.2/event-diagnostic.valid.json`
- 修改： `packages/protocol-schema/package.json`
- 修改： `packages/protocol-schema/test/schema.test.mjs`
- 修改： `tools/protocol-gen/generate.mjs`
- 修改： `packages/protocol-models/src/index.ts`
- 修改： `packages/protocol-models/src/generated-contract.test.ts`
- 创建： generated v1.2 TypeScript files under `packages/protocol-models/src/generated/`
- 创建： generated v1.2 Go files under `apps/test-service/internal/protocolmodel/`

**接口：**
- 输入： 已确认 v1.2 domain shape。
- 输出：生成后的顶层模型：

```text
CapabilitiesV12
WorkspaceSnapshot
TargetList
TaskSnapshotV12
TaskEventV12
ArtifactMetadataV12
Diagnostic
```

- [ ] **Step 1：写出 v1.2 contract 失败测试**

AJV 注册全部 v1.2 child schemas，再断言合法 handshake/inspect/targets/build/event，拒绝 Shell fixture。额外断言旧 v1.1 validator 仍拒绝 v1.2 字段：

```js
assert.equal(validateV12(await load("../fixtures/v1.2/cmake-build-start.valid.json")), true);
assert.equal(validateV12(await load("../fixtures/v1.2/cmake-build-shell.invalid.json")), false);
assert.equal(validateV11(await load("../fixtures/v1.2/cmake-build-start.valid.json")), false);
```

- [ ] **Step 2：运行 Schema tests 并确认失败**

运行：

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
```

预期： FAIL，v1.2 schemas 尚不存在。

- [ ] **Step 3：实现严格 Schema 并扩展 deterministic generation**

`tasks/start` v1.2 payload 使用 `oneOf`：

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["idempotencyKey", "kind", "workspaceGeneration", "projectId", "buildProfileId", "targetIds", "jobs", "timeoutMs"],
  "properties": {
    "idempotencyKey": {"type": "string", "pattern": "^[0-9a-f]{32}$"},
    "kind": {"const": "cmakeBuild"},
    "workspaceGeneration": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    "projectId": {"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},
    "buildProfileId": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    "targetIds": {"type": "array", "maxItems": 128, "uniqueItems": true, "items": {"type": "string", "pattern": "^[0-9a-f]{64}$"}},
    "jobs": {"type": "integer", "minimum": 1, "maximum": 256},
    "timeoutMs": {"type": "integer", "minimum": 1, "maximum": 86400000}
  }
}
```

另一个 union member 为 `kind: simulation` + scenario/timeout。Task/Event payload 使用 closed schemas。Workspace wire model不包含 native compiler path、binary directory 或 Service data path，只返回 URI、ID 和 capabilities。

`tools/protocol-gen/generate.mjs` 为每个 v1.2 top level生成 Go/TypeScript，并保持排序固定。

- [ ] **Step 4：生成并验证模型**

运行：

```powershell
pnpm generate:protocol
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
pnpm check:protocol-generated
```

预期： 全部 PASS，第二次 generation 无 diff。

- [ ] **Step 5：提交 v1.2 contract**

```powershell
git add packages/protocol-schema packages/protocol-models apps/test-service/internal/protocolmodel tools/protocol-gen/generate.mjs
git commit -m "feat: define protocol v1.2 workspace builds"
```

### Task 2：Go Protocol、Session routing 与 versioned projection

**文件：**
- 修改： `apps/test-service/internal/protocol/envelope.go`
- 修改： `apps/test-service/internal/protocol/envelope_test.go`
- 修改： `apps/test-service/internal/session/session.go`
- 修改： `apps/test-service/internal/session/session_test.go`
- 修改： `apps/test-service/internal/server/server.go`
- 修改： `apps/test-service/internal/server/server_test.go`
- 修改： `apps/test-service/internal/task/ports.go`
- 修改： `apps/test-service/internal/taskstore/tasks.go`
- 修改： `apps/test-service/internal/taskstore/artifacts.go`

**接口：**
- 输入： generated v1.2 models、`discovery.Snapshot`、`cmake.Target`、`task.Task`。
- 输出：Backend 扩展：

```go
type Backend interface {
	StartSimulation(context.Context, task.SimulationStart) (task.Task, error)
	InspectWorkspace(context.Context) (discovery.Snapshot, error)
	ListTargets(context.Context, build.TargetsRequest) ([]cmake.Target, error)
	StartBuild(context.Context, build.StartRequest) (task.Task, error)
	Get(context.Context, string) (task.Task, error)
	List(context.Context, string, int, []task.Kind) (task.Page[task.Task], error)
	Cancel(context.Context, string) (task.Task, error)
	Subscribe(context.Context, int64) (*eventbroker.Subscription, error)
	ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error)
	ReadArtifact(context.Context, string, int64, int) (ArtifactChunk, error)
}
```

- [ ] **Step 1：写出 negotiation、strict decode 和 compatibility projection 测试**

覆盖：

- v1.2/v1.1/v1.0 最高共同版本；
- 未协商 v1.2 调用新方法返回 `PROTOCOL_FEATURE_UNAVAILABLE`；
- unknown/unsafe build field 返回 `INVALID_MESSAGE` 或 `INVALID_TASK_SPEC`；
- stale generation/unknown IDs 映射稳定错误；
- v1.1 `tasks/list` 只返回 simulation；
- v1.1 get/cancel/artifact 访问 cmakeBuild 返回 `TASK_NOT_FOUND`；
- v1.1 subscription 将 `task.step_started`、`task.step_finished`、`task.diagnostic` 投影为 `task.output`，但保留原 sequence/taskId；
- v1.2 subscription 保留新 event name。

Compatibility event 核心断言：

```go
projected := eventForVersion(protocol.Version11, storedDiagnosticEvent)
if projected.Event != string(task.EventTaskOutput) || projected.Sequence != storedDiagnosticEvent.Sequence {
	t.Fatalf("projection = %#v", projected)
}
```

- [ ] **Step 2：运行 Go tests 并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session ./apps/test-service/internal/server -run 'V12|Version12|Projection' -count=1
```

预期： FAIL。

- [ ] **Step 3：实现 v1.2 routing 与连续 sequence projection**

新增 `protocol.Version12`，supported order 固定为 `1.2, 1.1, 1.0`。`protocol.NewEvent` 接收目标 version，不再 hard-code v1.1。

Session：

- 每个 v1.2 payload 使用 `decodeStrict`；
- 在调用 Backend 前校验安全 integer/ID；
- domain → generated model 使用独立 projection function；
- backend error → stable Protocol error table集中定义；
- v1.1 list query 向 Store 传 `[]task.Kind{task.KindSimulation}`；
- v1.1 task/artifact access 在返回 wire model 前检查 kind。

Server subscription 根据 handshake version 投影每条 journal event。v1.1 对新事件使用 `task.output` compatibility payload：

```json
{
  "stream": "service",
  "text": "",
  "truncated": false
}
```

compatibility payload 只能包含 `stream`、`text`、`truncated`，不得出现
`stepId`、`data` 或其他字段。投影保留原 `sequence` 与 `taskId`，不得丢弃事件、
重编号或跳过 sequence。

- [ ] **Step 4：运行 Session/Server 全套测试**

运行：

```powershell
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session ./apps/test-service/internal/server ./apps/test-service/internal/taskstore -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Go Protocol routing**

```powershell
git add apps/test-service/internal/protocol apps/test-service/internal/session apps/test-service/internal/server apps/test-service/internal/task/ports.go apps/test-service/internal/taskstore/tasks.go apps/test-service/internal/taskstore/artifacts.go
git commit -m "feat: route protocol v1.2 workspace methods"
```

### Task 3：TypeScript Client v1.2 API 与 runtime decoders

**文件：**
- 修改： `packages/test-client/src/envelopes.ts`
- 修改： `packages/test-client/src/connection.ts`
- 修改： `packages/test-client/src/client.ts`
- 修改： `packages/test-client/src/decoders.ts`
- 修改： `packages/test-client/src/subscription.ts`
- 修改： `packages/test-client/src/index.ts`
- 修改： `packages/test-client/src/client.test.ts`

**接口：**
- 输入： Task 1 generated TypeScript types。
- 输出：

```ts
export interface CMakeBuildInput {
  idempotencyKey: string;
  workspaceGeneration: string;
  projectId: string;
  buildProfileId: string;
  targetIds: string[];
  jobs: number;
  timeoutMs: number;
}

inspectWorkspace(): Promise<WorkspaceSnapshot>;
listCMakeTargets(input: {
  workspaceGeneration: string;
  projectId: string;
  buildProfileId: string;
}): Promise<TargetList>;
startCMakeBuild(input: CMakeBuildInput): Promise<TaskSnapshotV12>;
```

- [ ] **Step 1：写出 v1.2 handshake、API、decoder 和 local rejection 测试**

测试：

- 新客户端首选 v1.2，仍能 fallback v1.1/v1.0；
- v1.1 session 本地拒绝 Phase 3 methods，不写 wire；
- build input generation/ID/jobs/timeout 先在本地校验；
- response 的 unsafe integer、invalid date、unknown union kind 被拒绝；
- EventSubscription 接受 v1.1/v1.2 event union 并保持 sequence。

```ts
await assert.rejects(
  () => client.startCMakeBuild({
    idempotencyKey: TASK_ID,
    workspaceGeneration: "bad",
    projectId: "core",
    buildProfileId: "0".repeat(64),
    targetIds: [],
    jobs: 8,
    timeoutMs: 600_000
  }),
  /workspaceGeneration/
);
```

- [ ] **Step 2：运行 Client tests 并确认失败**

运行：

```powershell
pnpm --filter @unit-test-ide/test-client test
```

预期： FAIL。

- [ ] **Step 3：实现 version-aware validators 与 API**

为 v1.2 child schemas 注册 AJV validator。`#authenticate` 提交 `["1.2","1.1","1.0"]`；legacy unsupported 仍只允许一次。

保留 `startTask(input)` 作为 simulation API：

- v1.1 wire 使用旧 payload；
- v1.2 wire 增加 `kind: "simulation"`；
- 返回类型为 `TaskSnapshot | TaskSnapshotV12`。

新增 API 只能调用 `#requestV12`。Decoder 对 Date、安全 integer、range、related diagnostics 和 discriminated union 做显式转换。

- [ ] **Step 4：运行 TypeScript build/tests**

运行：

```powershell
pnpm --filter @unit-test-ide/protocol-models build
pnpm --filter @unit-test-ide/test-client test
```

预期： PASS，TypeScript strict build 无错误。

- [ ] **Step 5：提交 TypeScript Client**

```powershell
git add packages/test-client
git commit -m "feat: add typed protocol v1.2 client APIs"
```

### Task 4：Build Coordinator、Execution Planner 与 configure state

**文件：**
- 创建： `apps/test-service/internal/build/model.go`
- 创建： `apps/test-service/internal/build/coordinator.go`
- 创建： `apps/test-service/internal/build/coordinator_test.go`
- 创建： `apps/test-service/internal/build/planner.go`
- 创建： `apps/test-service/internal/build/planner_test.go`
- 创建： `apps/test-service/internal/build/lock.go`
- 创建： `apps/test-service/internal/taskstore/migrations/003_build_configurations.sql`
- 创建： `apps/test-service/internal/taskstore/build_configurations.go`
- 修改： `apps/test-service/internal/task/plan.go`
- 修改： `apps/test-service/internal/task/manager_execution.go`
- 修改： `apps/test-service/internal/task/manager_execution_test.go`
- 修改： `apps/test-service/internal/task/ports.go`
- 修改： `apps/test-service/internal/taskstore/sqlite_test.go`

**接口：**
- 输入： `discovery.Inspector`、CMake Profile/File API、Toolchain、Phase 3A Manager。
- 输出：

```go
type StartRequest struct {
	IdempotencyKey     string
	WorkspaceGeneration string
	ProjectID          string
	BuildProfileID     string
	TargetIDs          []string
	Jobs               int
	Timeout            time.Duration
}

type TargetsRequest struct {
	WorkspaceGeneration string
	ProjectID            string
	BuildProfileID       string
}

type Coordinator struct
func (c *Coordinator) Inspect(context.Context) (discovery.Snapshot, error)
func (c *Coordinator) Targets(context.Context, TargetsRequest) ([]cmake.Target, error)
func (c *Coordinator) Start(context.Context, StartRequest) (task.Task, error)
```

新增通用 lifecycle：

```go
type StepObserver interface {
	Succeeded(context.Context, Task, ExecutionStep) error
}

func NewExecutionBoundary(
	cmakeInstallation cmake.Installation,
	workspaceRoot workspace.Root,
	serviceDataRoot string,
) task.ExecutionBoundary
```

- [ ] **Step 1：写出 generation、ID、configure skip/reconfigure 和 target remap 测试**

覆盖：

- stale generation 在 Task 创建前返回 `ErrWorkspaceChanged`；
- 未知 project/profile/target；
- 首次计划 configure + build；
- fingerprint 相同只计划 build；
- CMake input 改变重新计划 configure；
- configure 成功后 observer 原子写入 state，写入失败不启动 build；
- reconfigure 后 target 消失时不把旧 name 传给 build；
- target native name 以独立 args 传递；
- binary directory lock 拒绝并发冲突。
- boundary 只接受 resolver 已验证的 CMake executable；cwd 只接受当前 workspace 或 Service data build root，symlink/junction identity 变化时在 Step 启动前再次拒绝。

- [ ] **Step 2：运行 Build/Store tests 并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/build ./apps/test-service/internal/taskstore -run 'Build|Configuration|Target' -count=1
```

预期： FAIL。

- [ ] **Step 3：实现 Coordinator 与 Planner**

Preset configure：

```text
cmake --preset <validated-configure-preset>
```

Generated configure：

```text
cmake -S <validated-source> -B <service-build-dir> -G <validated-generator>
      -DCMAKE_BUILD_TYPE=<validated-configuration>
      -DCMAKE_C_COMPILER=<validated-c-compiler>
      -DCMAKE_CXX_COMPILER=<validated-cxx-compiler>
```

每项为独立 args 元素，不拼 Shell 字符串。Multi-config generator 不传 `CMAKE_BUILD_TYPE`。

Build：

```text
cmake --build <validated-build-dir> --config <configuration>
      --parallel <jobs> --target <resolved-target-name>
```

`ExecutionStep` 增加 non-secret `State json.RawMessage` 和 runtime-only diagnostic parser。Phase 3 的两个进程 Step 都只直接启动 CMake；compiler/linker 是 CMake/build tool 的受控子进程。Coordinator 使用已经验证的 CMake installation、workspace root 与 Service data root 构造 `ExecutionBoundary`，随内部 `task.StartRequest` 交给 Manager；Protocol 无法提供或修改 boundary。Manager 在 configure Step exit 0 后调用 `StepObserver.Succeeded`；observer 写 `build_configurations` 成功后才进入 build。

Migration 003 创建以 workspace/project/profile 为复合 key 的 `build_configurations`，保存 fingerprint、build directory relative identity、CMake/File API identity 和时间。

- [ ] **Step 4：运行 Build/Task/Store tests 与 race**

运行：

```powershell
go test ./apps/test-service/internal/build ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/build ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Build Coordinator**

```powershell
git add apps/test-service/internal/build apps/test-service/internal/task apps/test-service/internal/taskstore
git commit -m "feat: plan validated cmake build tasks"
```

### Task 5：Runtime、CLI 与 Service lifecycle 组合

**文件：**
- 修改： `apps/test-service/internal/runtime/runtime.go`
- 修改： `apps/test-service/internal/runtime/runtime_test.go`
- 修改： `apps/test-service/internal/runtime/data_dir.go`
- 修改： `apps/test-service/internal/runtime/data_dir_test.go`
- 修改： `apps/test-service/cmd/unit-test-service/main.go`
- 修改： `apps/test-service/cmd/unit-test-service/main_test.go`
- 修改： `apps/test-service/internal/session/session.go`
- 修改： `apps/test-service/internal/task/manager.go`
- 修改： `apps/test-service/internal/task/manager_execution.go`
- 修改： `apps/test-service/internal/task/manager_execution_test.go`
- 修改： `apps/test-service/internal/taskstore/recovery.go`
- 修改： `apps/test-service/internal/taskstore/sqlite_test.go`
- 修改： `tools/service-probe/src/probe.ts`
- 修改： `tools/service-probe/src/probe.test.ts`

**接口：**
- 输入： Task 4 Coordinator、Phase 3B Inspector。
- 输出：Runtime 配置：

```go
type Config struct {
	DataDir           string
	ServiceExecutable string
	WorkspaceRoot     string
	TrustedWorkspace  bool
	CMakeBundleRoot   string
	DevCMakeExecutable string
	Platform          string
	Clock             task.Clock
	NewID             task.IDGenerator
	TerminationGrace  time.Duration
}
```

- [ ] **Step 1：写出 CLI mode isolation 和 Runtime cleanup 失败测试**

测试：

- service mode 必须有 `--workspace-root`；
- `--trusted-workspace` 只接受显式 bool flag；
- `--cmake-bundle-root` 与 `--dev-cmake-executable` 不允许出现在 internal process-host/task-fixture modes；
- root invalid 时不创建 listener；
- data layout 包含 `build/`；
- Runtime partial initialization failure 逆序关闭 Inspector/Store/Artifacts/lock；
- untrusted workspace 可运行 simulation，但 Phase 3 methods 返回 `WORKSPACE_TRUST_REQUIRED`。
- 重启时 simulation queued/running/cancelling 与 running/cancelling `cmake_build` 保持 interrupted 语义；
- queued `cmake_build` 在 Coordinator 可用后从持久化的规范化 request 重新验证 generation/profile/toolchain/target 并生成新 Plan；
- queued build 的 generation 或引用对象失效时以 `interrupted` 和 `WORKSPACE_CHANGED`/对应稳定错误码结束，不启动进程。

- [ ] **Step 2：运行 Runtime/CLI tests 并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/cmd/unit-test-service -run 'Workspace|CMake|Trusted|Cleanup' -count=1
```

预期： FAIL。

- [ ] **Step 3：组合 Runtime dependencies 与 CLI flags**

Runtime Open 顺序：

1. 准备 owner-only data layout；
2. 锁定 instance；
3. 打开 Store 并执行 migration；
4. 清理 process leases；
5. 打开 workspace root；
6. 解析 CMake；
7. 创建 probe runner、toolchain registry、Inspector；
8. 创建 Broker/Manager，恢复 simulation 与活动 `cmake_build`；
9. 创建 Coordinator；
10. 读取保留的 queued `cmake_build`，逐个重新验证并通过 `Manager.ResumeQueued` 装载新的 runtime-only Plan/boundary；
11. 恢复 artifacts 并完成 Runtime publication。

`Manager.ResumeQueued` 只能接收 Store 已存在且仍为 queued 的 `cmake_build`，不得创建新 task/event 或改变原 idempotency identity；它在启动前执行与新任务相同的 plan fingerprint 和 boundary 校验。`Runtime` 实现 Session Backend 的 inspect/targets/startBuild。所有 Phase 3 method 先检查 `TrustedWorkspace`。

Service probe `StartServiceOptions` 增加 workspace root、trusted flag、bundle root/dev CMake，并对这些路径执行现有 safe-error redaction。

- [ ] **Step 4：运行 Runtime/CLI/Probe tests**

运行：

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/cmd/unit-test-service -count=1
pnpm --filter @unit-test-ide/service-probe test
```

预期： PASS。

- [ ] **Step 5：提交 Runtime integration**

```powershell
git add apps/test-service/internal/runtime apps/test-service/cmd/unit-test-service apps/test-service/internal/session/session.go tools/service-probe
git commit -m "feat: bind runtime to a trusted workspace"
```

### Task 6：Diagnostic events、build artifacts 与 deterministic CMake fixture E2E

**文件：**
- 创建： `apps/test-service/cmd/cmake-fixture/main.go`
- 创建： `apps/test-service/cmd/cmake-fixture/main_test.go`
- 创建： `apps/test-service/internal/artifactstore/task_sink.go`
- 创建： `apps/test-service/internal/artifactstore/task_sink_test.go`
- 修改： `apps/test-service/internal/task/ports.go`
- 修改： `apps/test-service/internal/task/manager.go`
- 修改： `apps/test-service/internal/task/manager_execution.go`
- 修改： `apps/test-service/internal/task/manager_artifacts.go`
- 修改： `apps/test-service/internal/task/manager_test.go`
- 修改： `apps/test-service/internal/artifactstore/store.go`
- 修改： `apps/test-service/internal/artifactstore/store_test.go`
- 修改： `tools/service-probe/build-service.mjs`
- 修改： `tools/service-probe/src/probe.ts`
- 修改： `tools/service-probe/src/probe.test.ts`

**接口：**
- 输入： Step runtime diagnostic parser、ArtifactStore 原子文件机制。
- 输出：

```go
type ArtifactSink interface {
	AppendOutput(context.Context, string, string, []byte) error
	AppendDiagnostic(context.Context, diagnostic.Diagnostic) error
	CommitJSON(context.Context, string, string, any) error
	Finalize(context.Context, time.Time) ([]Artifact, error)
	Abort(context.Context) error
}

type ArtifactWriter interface {
	OpenTask(context.Context, string, Kind) (ArtifactSink, error)
}
```

- [ ] **Step 1：写出流式 artifacts、diagnostic event 和 fixture E2E 失败测试**

Go tests 断言：

- output 先写 sink 再 journal event；
- parser 分块产生 `task.diagnostic`；
- parser failure 不改变 process outcome；
- sink failure 终止活动进程并使 Service unhealthy；
- finalize 生成 stdout/stderr/execution-plan/diagnostics/build-summary；
- 所有文件通过 temp + fsync + rename 提交；
- environment 和 token 不出现在 artifacts。

Service-probe E2E 启动 deterministic `cmake-fixture`，完成 inspect → build → targets → second build skip configure，并断言 Step/Diagnostic event replay。

- [ ] **Step 2：运行聚焦测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/task ./apps/test-service/internal/artifactstore ./apps/test-service/cmd/cmake-fixture -run 'Diagnostic|ArtifactSink|Fixture' -count=1
pnpm --filter @unit-test-ide/service-probe test:e2e
```

预期： FAIL。

- [ ] **Step 3：实现 sink、parser feed、v1.2 events 与 fixture**

Manager 对每个 ProcessOutput：

1. 向 sink 追加 raw bytes；
2. 向 journal 写入 `task.output`；
3. feed 当前 Step parser；
4. 对每个 Diagnostic append JSONL；
5. 向 journal 写入 `task.diagnostic`。

`task.step_started`/`task.step_finished` 只对 `KindCMakeBuild` 写入 journal。Execution plan artifact 使用 `ExecutionStep.Public`，不序列化 ProcessSpec Env。

`cmake-fixture` 支持固定命令：

- `--version=json-v1`
- `--list-presets=configure`
- `--build --list-presets`
- configure args，写入 deterministic File API reply
- `--build`，输出可解析 warning 并退出 0

未知参数退出 2，fixture 不执行外部命令。

- [ ] **Step 4：运行完整门禁**

运行：

```powershell
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
git diff --check
```

预期： 全部 PASS。

- [ ] **Step 5：提交 Phase 3C integration**

```powershell
git add apps/test-service packages tools/service-probe
git commit -m "feat: stream cmake build diagnostics and artifacts"
```

## Phase 3C 完成检查

- [ ] v1.2 Schema/生成模型 deterministic
- [ ] v1.0/v1.1 contract tests 全部通过
- [ ] v1.1 event sequence compatibility projection 测试通过
- [ ] deterministic CMake fixture E2E 通过
- [ ] `pnpm verify`
- [ ] `git diff --check`
- [ ] `git status --short` 为空
- [ ] 独立评审确认 Session/Client 均不能传入 executable/args/env/path
