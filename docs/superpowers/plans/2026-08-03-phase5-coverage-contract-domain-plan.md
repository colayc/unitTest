# Phase 5A：Coverage contract、Protocol v1.4 与 persistence 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 在不执行 compiler、test 或 coverage tool 的前提下，建立 Coverage JSON v1、Workspace coverage profile、Protocol v1.4、CoverageRun/Report domain、SQLite persistence 和 TypeScript Client API。

**架构：** Coverage JSON 与 Protocol 分别维护独立 Schema 和 generated model；Go `coveragedomain` 不依赖 Protocol type；CoverageRun 与 TestRun 共享顶层 Task，但保持独立 outcome；large coverage body 只以 Artifact metadata 关联。

**基础提交：** `26f1aa6f28d42e226f51cce151728922c3313806`

## 全局约束

- 本计划不启动 CMake、compiler、CTest、test executable、Python、gcovr、llvm-profdata 或 llvm-cov。
- v1.0–v1.3 Schema、fixtures、generated model 和 runtime behavior 不变。
- Coverage JSON body 不包含 run/artifact ID、时间、duration、native path 或浮点 percentage。
- Protocol request 不包含 executable、args、env、cwd、flags、driver override、script、plugin 或 report template。
- Workspace glob 仅表达 bounded workspace-relative include/exclude。
- SQLite migration 必须可从 migration 001–008 升级并在失败时完整回滚。

---

### Task 1：Coverage JSON v1 Schema 与 generated models

**writing-plans 详细执行计划：** [2026-08-03-coverage-json-v1-contract-plan.md](./2026-08-03-coverage-json-v1-contract-plan.md)

**文件：**

- 创建：`packages/coverage-schema/package.json`
- 创建：`packages/coverage-schema/schema/v1/coverage.schema.json`
- 创建：`packages/coverage-schema/fixtures/v1/report.valid.json`
- 创建：`packages/coverage-schema/fixtures/v1/report-native-path.invalid.json`
- 创建：`packages/coverage-schema/fixtures/v1/report-float.invalid.json`
- 创建：`packages/coverage-schema/fixtures/v1/report-unsafe-count.invalid.json`
- 创建：`packages/coverage-schema/test/schema.test.mjs`
- 创建：`packages/coverage-models/package.json`
- 创建：`packages/coverage-models/tsconfig.json`
- 创建：`packages/coverage-models/src/index.ts`
- 创建：`packages/coverage-models/src/decoder.ts`
- 创建：`packages/coverage-models/src/decoder.test.ts`
- 创建：`packages/coverage-models/src/generated/coverage-v1.ts`
- 创建：`packages/coverage-models/src/generated-contract.test.ts`
- 创建：`apps/test-service/internal/coveragemodel/v1/generated.go`
- 创建：`apps/test-service/internal/coveragemodel/v1/validate.go`
- 创建：`apps/test-service/internal/coveragemodel/v1/validate_test.go`
- 创建：`apps/test-service/internal/coveragemodel/generated_contract_test.go`
- 创建：`tools/coverage-gen/generate.mjs`
- 修改：`package.json`

**主要模型：**

```text
CoverageDocumentV1
CoverageProvenance
CoverageCompleteness
CoverageSummary
CoverageFile
CoverageLine
CoverageMetric
```

- [ ] **Step 1：写出 valid、closed schema 和 deterministic generation 失败测试**

覆盖：

- `schemaVersion` 固定为 `1.0`；
- platform、architecture、compiler、driver、collector、normalizer 和 instrumentation fingerprint 必填；
- completeness outcome 仅 `available|partial`；
- reason code closed enum 且 bounded；
- summary/file/line/branch/function count 为 0..`Number.MAX_SAFE_INTEGER` integer；
- `covered <= total`；
- line number 从 1 开始、严格递增；
- URI 必须是 canonical relative URI；
- SHA-256 为 lowercase 64 hex；
- 禁止 native path、timestamp、runId、artifactId、duration、percentage 和 unknown field；
- files 按 URI 排序；
- generated TypeScript/Go type 与 Schema enum 一致。

- [ ] **Step 2：运行 Coverage Schema tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/coverage-schema test
pnpm --filter @unit-test-ide/coverage-models test
```

预期：FAIL，新 package 和 generator 尚不存在。

- [ ] **Step 3：实现独立 coverage generator**

新增 root scripts：

```json
{
  "generate:coverage": "node tools/coverage-gen/generate.mjs",
  "check:coverage-generated": "node tools/coverage-gen/generate.mjs --check"
}
```

同时把 `check:coverage-generated` 接入 root `verify`，使任何 generated drift 都在默认本地/CI 门禁失败。`packages/*` 已由现有 workspace glob 覆盖，不修改 `pnpm-workspace.yaml`。

Generator 必须 deterministic；`--check` 在临时目录生成后 byte-compare，不修改 source tree。JSON Schema 负责 closed structural shape；`covered <= total`、strict ordering、canonical URI 和跨字段 summary invariant 由 TypeScript/Go semantic validator 实现，不能依赖非标准 JSON Schema extension。

- [ ] **Step 4：生成并验证第二次无 diff**

```powershell
pnpm generate:coverage
pnpm check:coverage-generated
pnpm --filter @unit-test-ide/coverage-schema test
pnpm --filter @unit-test-ide/coverage-models test
go test ./apps/test-service/internal/coveragemodel/... -count=1
git diff --check
```

预期：PASS。

- [ ] **Step 5：提交 Coverage JSON contract**

```powershell
git add package.json packages/coverage-schema packages/coverage-models tools/coverage-gen apps/test-service/internal/coveragemodel
git commit -m "feat: define coverage report contracts"
```

### Task 2：Workspace config v3 coverage profiles

**writing-plans 详细执行计划：** [2026-08-03-workspace-config-v3-coverage-profiles-plan.md](./2026-08-03-workspace-config-v3-coverage-profiles-plan.md)

**文件：**

- 修改：`apps/test-service/internal/workspace/workspace.schema.json`
- 修改：`apps/test-service/internal/workspace/config.go`
- 修改：`apps/test-service/internal/workspace/config_test.go`
- 创建：`apps/test-service/internal/workspace/testdata/coverage-v3.valid.json`
- 创建：`apps/test-service/internal/workspace/testdata/coverage-command.invalid.json`
- 创建：`apps/test-service/internal/workspace/testdata/coverage-path.invalid.json`
- 创建：`apps/test-service/internal/workspace/testdata/coverage-duplicate.invalid.json`
- 修改：`apps/test-service/internal/cmake/generation.go`
- 修改：`apps/test-service/internal/cmake/generation_test.go`
- 修改：`apps/test-service/internal/discovery/inspector.go`
- 修改：`apps/test-service/internal/discovery/inspector_test.go`
- 修改：`tools/workspace-smoke/workspace-config-schema.test.mjs`

**接口：**

```go
type CoverageProfile struct {
    ID                 string
    BaseBuildProfileID string
    Include            []string
    Exclude            []string
}
```

- [ ] **Step 1：写出 v1/v2 compatibility、v3 valid 和 injection 失败测试**

覆盖：

- v1/v2 config 不变；
- v3 profile 引用已存在 base profile；
- duplicate ID、missing base、空/超长 glob 拒绝；
- `..`、absolute、drive/UNC、URI scheme、反斜杠和 NUL 拒绝；
- command、flags、compilerArgs、environment、gcovrConfig、script、plugin、driver、threshold 拒绝；
- glob 只支持 bounded `* ? **`；
- canonical config hash 包含 profile 与稳定排序的 glob；
- profile 数和总 matcher state 有界。

- [ ] **Step 2：运行 Workspace tests 并确认失败**

```powershell
go test ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake -run 'Coverage|ConfigV3|Generation' -count=1
pnpm test:workspace
```

预期：FAIL。

- [ ] **Step 3：实现 strict v3 loader 与 immutable clone**

Config loader 继续按顶层 version 选择 closed schema；glob 此阶段只校验和 canonicalize，不访问文件系统或执行匹配。

- [ ] **Step 4：运行完整 Workspace tests**

```powershell
go test ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake -count=1
pnpm test:workspace
git diff --check
```

- [ ] **Step 5：提交 Workspace coverage profiles**

```powershell
git add apps/test-service/internal/workspace apps/test-service/internal/cmake/generation.go apps/test-service/internal/cmake/generation_test.go tools/workspace-smoke
git commit -m "feat: define workspace coverage profiles"
```

### Task 3：Protocol v1.4 Schema、fixtures 与 generated models

**writing-plans 详细执行计划：** [2026-08-04-protocol-v1-4-coverage-contracts-plan.md](./2026-08-04-protocol-v1-4-coverage-contracts-plan.md)

**文件：**

- 创建：`packages/protocol-schema/schema/v1.4/capabilities.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/diagnostic.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/test.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/coverage.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/task.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/event.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/artifact.schema.json`
- 创建：`packages/protocol-schema/schema/v1.4/message.schema.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-run-start.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-run.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-report.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-run-command.invalid.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-run-environment.invalid.json`
- 创建：`packages/protocol-schema/fixtures/v1.4/coverage-run-driver.invalid.json`
- 修改：`packages/protocol-schema/test/schema.test.mjs`
- 修改：`packages/protocol-schema/package.json`
- 修改：`tools/protocol-gen/generate.mjs`
- 修改：`packages/protocol-models/src/index.ts`
- 修改：`packages/protocol-models/src/generated-contract.test.ts`
- 创建：generated v1.4 TypeScript files under `packages/protocol-models/src/generated/`
- 创建：generated v1.4 Go files under `apps/test-service/internal/protocolmodel/v1_4/`
- 修改：`apps/test-service/internal/protocolmodel/generated_contract_test.go`

**主要 wire model：**

```text
CapabilitiesV14
CoverageRunStartRequest
CoverageRun
CoverageRunPage
CoverageReport
CoverageEventV14
TaskSnapshotV14
```

- [ ] **Step 1：写出 v1.4 valid、negative injection 与 compatibility 失败测试**

至少逐字段拒绝：

```text
executable command args argv flags shell script env environment
cwd workingDirectory hook resultPath driver collector gcovrConfig
python module plugin template threshold
```

验证 selection、repeat、timeout、page/cursor、closed outcome/reason、safe integer、artifact kind 和 capabilities。

- [ ] **Step 2：运行 Protocol tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
```

- [ ] **Step 3：实现 v1.4 closed schemas 与 generation**

Methods：

```text
coverage/runs/start
coverage/runs/get
coverage/runs/list
coverage/reports/get
```

`tasks/get/list/cancel` v1.4 增加 coverage Task projection；v1.3 schema 文件不得修改。

- [ ] **Step 4：生成并验证 compatibility/drift**

```powershell
pnpm generate:protocol
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
pnpm check:protocol-generated
git diff --check
```

- [ ] **Step 5：提交 Protocol v1.4 contract**

```powershell
git add packages/protocol-schema packages/protocol-models apps/test-service/internal/protocolmodel tools/protocol-gen/generate.mjs
git commit -m "feat: define protocol v1.4 coverage contracts"
```

### Task 4：Go CoverageRun/Report domain 与 validation

**详细实施计划：** [2026-08-04-coverage-run-report-domain-plan.md](./2026-08-04-coverage-run-report-domain-plan.md)

**文件：**

- 创建：`apps/test-service/internal/coveragedomain/model.go`
- 创建：`apps/test-service/internal/coveragedomain/model_test.go`
- 创建：`apps/test-service/internal/coveragedomain/request.go`
- 创建：`apps/test-service/internal/coveragedomain/request_test.go`
- 创建：`apps/test-service/internal/coveragedomain/summary.go`
- 创建：`apps/test-service/internal/coveragedomain/summary_test.go`
- 修改：`apps/test-service/internal/task/model.go`
- 修改：`apps/test-service/internal/task/plan.go`
- 修改：`apps/test-service/internal/task/plan_test.go`

**接口：**

```go
type Run struct {
    ID, TaskID, TestRunID string
    Status Status
    Outcome Outcome
    Reason Reason
    Request Request
    Toolchain ToolchainSnapshot
    Summary *Summary
    Artifacts ArtifactRefs
}
```

- [ ] **Step 1：写出 outcome、request、clone、bounds 和 Task mapping 失败测试**

覆盖 immutable clone、stable ID、closed enum、selection reuse、available/partial/unavailable/cancelled invariant、summary arithmetic、covered<=total、artifact terminal-only 和 no-native-path serialization。

- [ ] **Step 2：运行领域测试并确认失败**

```powershell
go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/task -run 'Coverage|Plan' -count=1
```

- [ ] **Step 3：实现纯领域模型**

本 Task 不依赖 SQLite、Protocol generated model 或 process package。Task Engine 仅增加内部 `KindCoverageRun`/coverage step kind 与 clone/fingerprint 支持，不生成实际 plan。

- [ ] **Step 4：运行全套领域与 race**

```powershell
go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/task -count=1
go test -race ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/task -count=1
```

- [ ] **Step 5：提交 coverage domain**

```powershell
git add apps/test-service/internal/coveragedomain apps/test-service/internal/task
git commit -m "feat: model coverage runs and reports"
```

### Task 5：SQLite migration 009、repositories 与 atomic creation

**详细实施计划：** [2026-08-04-coverage-run-persistence-plan.md](./2026-08-04-coverage-run-persistence-plan.md)

**文件：**

- 创建：`apps/test-service/internal/taskstore/migrations/009_coverage_runs.sql`
- 创建：`apps/test-service/internal/taskstore/coverage_runs.go`
- 创建：`apps/test-service/internal/taskstore/coverage_runs_test.go`
- 创建：`apps/test-service/internal/taskstore/coverage_reports.go`
- 创建：`apps/test-service/internal/taskstore/coverage_reports_test.go`
- 创建：`apps/test-service/internal/taskstore/coverage_task_creation_test.go`
- 修改：`apps/test-service/internal/taskstore/tasks.go`
- 修改：`apps/test-service/internal/taskstore/events.go`
- 修改：`apps/test-service/internal/taskstore/artifacts.go`
- 修改：`apps/test-service/internal/taskstore/sqlite_test.go`

- [ ] **Step 1：写出 migration/repository/atomicity 失败测试**

覆盖：

- migration 001–008 → 009；
- broken 009 rollback 与 foreign key restore；
- Task/CoverageRun/TestRun/queued event 单 transaction；
- idempotent same request 与 conflicting request；
- get/list stable cursor 和 workspace scope；
- report 只能引用同 run/task 的 published artifact；
- terminal immutable；
- storage/artifact/event fault 不产生 orphan relation；
- 旧数据库 replay 不变。

- [ ] **Step 2：运行 taskstore tests 并确认失败**

```powershell
go test ./apps/test-service/internal/taskstore -run 'Migration009|Coverage|Atomic' -count=1
```

- [ ] **Step 3：实现 migration 与 transaction API**

Migration 保存 request canonical JSON、status/outcome/reason、tool provenance、summary metadata 和 artifact foreign key；逐行 coverage 禁止入表。

- [ ] **Step 4：运行 taskstore 全套/race**

```powershell
go test ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/taskstore -count=1
git diff --check
```

- [ ] **Step 5：提交 persistence**

```powershell
git add apps/test-service/internal/taskstore
git commit -m "feat: persist coverage run metadata"
```

### Task 6：TypeScript Client v1.4 API 与 decoder

**文件：**

- 修改：`packages/test-client/src/envelopes.ts`
- 修改：`packages/test-client/src/connection.ts`
- 修改：`packages/test-client/src/client.ts`
- 修改：`packages/test-client/src/decoders.ts`
- 修改：`packages/test-client/src/index.ts`
- 修改：`packages/test-client/src/client.test.ts`

**接口：**

```ts
startCoverage(input: CoverageRunInput): Promise<CoverageRun>;
getCoverageRun(runId: string): Promise<CoverageRun>;
listCoverageRuns(input: CoverageRunListInput): Promise<CoverageRunPage>;
getCoverageReport(reportId: string): Promise<CoverageReport>;
```

- [x] **Step 1：写出 negotiation/local validation/decoder 失败测试**

覆盖：

- 首选 v1.4，fallback v1.3/v1.2/v1.1/v1.0；
- old session 调用 coverage API 本地拒绝且不写 wire；
- input 只能含结构化 ID/selection/repeat/timeout；
- unknown outcome/reason、unsafe summary、invalid URI/digest/date 拒绝；
- decoder deep clone；
- v1.3 test API 行为不变。

- [x] **Step 2：运行 Client tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/test-client test
```

- [x] **Step 3：实现 version-aware API 与 strict decoder**

Client 不解析大型 Coverage JSON body；它只返回 report metadata/artifact ID，Coverage JSON 由 `@unit-test-ide/coverage-models` 的独立 decoder 验证。

- [x] **Step 4：运行 Phase 5A 完整门禁**

```powershell
pnpm check:coverage-generated
pnpm check:protocol-generated
pnpm build
pnpm test
go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/taskstore -count=1
git diff --check
```

- [x] **Step 5：提交 TypeScript Client v1.4**

```powershell
git add packages/test-client
git commit -m "feat: expose coverage client contracts"
```

2026-08-05 Task 6 证据（pinned Node 24.18.0、pnpm 11.4.0、Go 1.26.5 与 worktree-local cache）：

- RED：`pnpm --filter @unit-test-ide/test-client test` exit 1，TypeScript 精确报告缺少 `CoverageRunInput`、`CoverageRunListInput`、`CoverageRun`、`CoverageRunPage`、`CoverageReport` exports 与四个 Coverage methods。
- GREEN：`pnpm --filter @unit-test-ide/test-client test` 80 passed、0 failed；独立 Client build 与 `git diff --check` 通过。
- `pnpm check:coverage-generated`、`pnpm check:protocol-generated` 与 root `pnpm build` 通过；review fix 后 Client test 为 80 passed、0 failed。
- review fix（base `f81c63e`）证明 start/list 对 caller 只建立一次 deep JSON-wire snapshot、对该 snapshot 做 exact schema validation 并发送同一对象；stateful Proxy 无法在 validation/serialization 间注入 outer operational field 或 nested selection field，snapshot failure 本地拒绝且不关闭 connection。Coverage run/report/artifact linkage malformed IDs 均 fail closed，CoverageRunPage nested item clone 也有直接 regression evidence。
- root `pnpm test` 与 `pnpm verify` 均在唯一的历史 Windows CMake fixture `UnitTestIDE CMake helper has strict deterministic framework registration` 失败，精确原因为 `spawnSync cmake ENOENT`；失败发生前 Coverage generator、CMake bundle、workspace schema/security tests 与 verify 的 drift/build 均通过，未出现 test-client、Coverage/Protocol generated 或本次变更 package 的新增 failure。
- 由于上述历史 baseline，下面的“完整 Phase 5A 门禁与独立 diff/security review”保持未勾选；whole-branch review、后续完整 CI 与双 remote 推送由最终 controller 执行。

## Phase 5A 完成检查

- [ ] Coverage JSON v1 contract 与 generated models
- [ ] Workspace config v3 coverage profiles
- [ ] Protocol v1.4 closed contract 与 compatibility
- [ ] CoverageRun/Report domain
- [ ] Migration 009 与 atomic creation
- [x] TypeScript Client v1.4
- [ ] 完整 Phase 5A 门禁与独立 diff/security review
