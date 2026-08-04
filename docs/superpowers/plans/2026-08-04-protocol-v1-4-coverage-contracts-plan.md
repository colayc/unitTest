# Protocol v1.4 Coverage Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保持 Protocol v1.0–v1.3 Schema、fixtures、generated models 和 runtime behavior 不变的前提下，定义 closed Protocol v1.4 coverage wire contract，并生成可由 TypeScript Client 与 Go Service 直接消费的 v1.4 models。

**Architecture:** Protocol v1.4 是 v1.3 的完整新快照：继续引用既有 v1.2 Workspace snapshot，复制并版本化 v1.3 diagnostic/test contract，只在 v1.4 capabilities、task、event、artifact、message 中增加 coverage 能力。`coverage.schema.json` 是 CoverageRun/Report metadata 的唯一 Protocol domain；大型 file/line coverage body 继续由独立 Coverage JSON v1 artifact 承载。`coverage/runs/start` 只接受稳定 ID、结构化 test selection、repeat、timeout 和 idempotency key，不能接受任何 executable、argument、environment、driver、processor 或 report-format 控制。

**Tech Stack:** JSON Schema Draft 2020-12、AJV 8.20.0、quicktype 24.0.0、TypeScript 6.0.3、Go 1.26.5、Node.js 24.18.0、pnpm 11.4.0。

## Global Constraints

- 本计划不启动 CMake、compiler、CTest、test executable、Python、gcovr、`llvm-profdata` 或 `llvm-cov`。
- `packages/protocol-schema/schema/v1` 至 `v1.3`、`packages/protocol-schema/fixtures/v1` 至 `v1.3` 及全部既有 generated files 必须保持 byte-for-byte 不变。
- v1.4 顶层对象、request payload、domain object、event payload 和 nested metadata 全部 closed；除既有 extensible `method`/`payload` response base 外均使用 `additionalProperties: false` 或 `unevaluatedProperties: false`。
- 所有 count、sequence、duration 和 byte size 都是 integer，最大值为 `9007199254740991`；repeat 为 1..100，timeout 为 1..86400000 ms，coverage list page 为 1..200，cursor 为 1..4096 characters。
- 32-byte logical ID 使用 `^[0-9a-f]{32}$`；revision/generation/digest 使用 `^[0-9a-f]{64}$`；Workspace coverage profile ID 与 Project ID 使用 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`。
- `CoverageRunStartRequest` 精确包含 `idempotencyKey`、`workspaceGeneration`、`projectId`、`coverageProfileId`、`catalogRevision`、`selection`、`repeatCount`、`timeoutMs`，不包含 `kind` 或任何 runtime/tool option。
- `CoverageRun.status` 只允许 `queued`、`running`、`finished`；`outcome` 只允许 `available`、`partial`、`unavailable`、`cancelled`。
- `available`/`partial` 终态必须有 `reportId`；`unavailable`/`cancelled` 终态禁止 `reportId`。非终态禁止 `outcome`、`reason`、`finishedAt` 和 `reportId`。
- `cancelled` reason 只允许 `user_cancelled`、`task_timed_out`；`unavailable` reason 只允许 `instrumentation_failed`、`build_failed`、`profile_collection_failed`、`merge_failed`、`normalization_failed`、`report_generation_failed`、`persistence_failed`、`service_restarted`。`available`/`partial` 不使用 run-level reason；partial 细节只在 report completeness reasons 中表达。
- `CoverageReport` 只返回 `reportId`、`coverageRunId`、`testRunId`、`schemaVersion`、`createdAt`、`completeness`、`summary`、`toolProvenance` 和一个 `coverage-json` `artifactId`；禁止内联 `files`、`lines`、raw profile 或 third-party JSON。
- `CoverageSummaryV14` 与 Coverage JSON v1 使用相同的 `lines`、`branches`、`functions` 三组 `{ covered, total }` safe integer。`covered <= total` 的跨字段语义由后续 domain/client validation 负责；本 Task 只保证 closed shape 与 safe integer。
- 每个成功 CoverageRun 固定生成 `coverage-json`、`junit-xml`、`coverage-html`；public artifact kind 不增加 raw profile、indexed profile、`.gcda` 或 third-party JSON。
- v1.4 `coverage/runs/list` 使用最多 200 items 的稳定 cursor page；`tasks/get`、`tasks/list`、`tasks/cancel` 可投影 coverage Task，`tasks/start` 不接受 coverage payload。
- v1.4 coverage event 进入既有 Task journal 并复用单调 `sequence`；只增加 5 个 run-level event，不增加 file/line event。
- generator 的 v1.4 union/import 修复必须在 `tools/protocol-gen/generate.mjs` 中确定性完成；不得手工编辑 generated output。
- 每个 Task 严格 red-green-refactor、独立提交；controller 在每个 Task 完成审查后推送 GitHub `github` 与 Gitee `origin` 的 `codex/workspace-cmake-toolchains`。

## Exact v1.4 Wire Contract

```ts
interface CoverageRunStartRequest {
  idempotencyKey: string;
  workspaceGeneration: string;
  projectId: string;
  coverageProfileId: string;
  catalogRevision: string;
  selection: TestSelectionV14;
  repeatCount: number;
  timeoutMs: number;
}

interface CoverageRun {
  coverageRunId: string;
  taskId: string;
  testRunId: string;
  workspaceGeneration: string;
  projectId: string;
  coverageProfileId: string;
  catalogRevision: string;
  selectionSnapshot: TestSelectionSnapshotV14;
  repeatCount: number;
  timeoutMs: number;
  status: CoverageRunStatusV14;
  createdAt: Date;
  lastSequence: number;
  outcome?: CoverageRunOutcomeV14;
  reason?: CoverageRunReasonV14;
  startedAt?: Date;
  finishedAt?: Date;
  reportId?: string;
}

interface CoverageRunPage {
  items: CoverageRun[];
  nextCursor?: string;
}

interface CoverageReport {
  reportId: string;
  coverageRunId: string;
  testRunId: string;
  schemaVersion: "1.0";
  createdAt: Date;
  completeness: CoverageCompletenessV14;
  summary: CoverageSummaryV14;
  toolProvenance: CoverageToolProvenanceV14;
  artifactId: string;
}
```

`CoverageCompletenessV14.outcome` 只允许 `available|partial`。`available` 要求空 `reasons`；`partial` 要求 1..64 个 unique reason，item 只允许 `test_crashed`、`test_timed_out`、`profile_missing_for_failed_invocation`。

`CoverageToolProvenanceV14` 与 Coverage JSON v1 对齐：`platform=windows|linux`、`architecture=x86|x64|arm64`、compiler family `gcc|clang|clang-cl`、driver `gcov|llvm-cov`、collector `gcovr|llvm-cov`、bounded versions、`normalizerVersion` 和 64-hex `instrumentationFingerprint`。

## File Responsibility Map

| Path | Single responsibility |
|---|---|
| `packages/protocol-schema/schema/v1.4/diagnostic.schema.json` | v1.4 versioned diagnostic snapshot；不反向修改 v1.3 |
| `packages/protocol-schema/schema/v1.4/test.schema.json` | v1.4 structured test selection/catalog/run snapshot reused by coverage |
| `packages/protocol-schema/schema/v1.4/coverage.schema.json` | closed CoverageRunStartRequest、CoverageRun/Page、CoverageReport、summary/completeness/provenance |
| `packages/protocol-schema/schema/v1.4/capabilities.schema.json` | v1.3 capabilities 加 coverage method/page/timeout capability |
| `packages/protocol-schema/schema/v1.4/task.schema.json` | v1.3 Task branches 加 CoverageRunTaskSnapshotV14 |
| `packages/protocol-schema/schema/v1.4/event.schema.json` | v1.3 events 加 5 个 bounded CoverageEventV14 branches |
| `packages/protocol-schema/schema/v1.4/artifact.schema.json` | v1.3 artifact kinds 加三种 public coverage report kind/MIME |
| `packages/protocol-schema/schema/v1.4/message.schema.json` | v1.4 negotiation、coverage methods、responses 与 error codes |
| `packages/protocol-schema/fixtures/v1.4/coverage-*.json` | valid wire evidence 与 direct injection regression evidence |
| `packages/protocol-schema/test/schema.test.mjs` | v1.4 AJV contract、bounds、injection、compatibility gate |
| `packages/protocol-schema/package.json` | publish v1.4 Schema subpath exports |
| `tools/protocol-gen/generate.mjs` | deterministic v1.4 TS/Go generation、union 与 cross-package imports |
| `packages/protocol-models/src/generated/*-v1-4.ts` | generated TypeScript v1.4 contract；禁止手工编辑 |
| `apps/test-service/internal/protocolmodel/v1_4/*/generated.go` | generated Go v1.4 contract；禁止手工编辑 |
| `packages/protocol-models/src/index.ts` | public v1.4 type/enum exports without ambiguous v1.3 names |
| `packages/protocol-models/src/generated-contract.test.ts` | TypeScript compile-time/runtime enum contract evidence |
| `apps/test-service/internal/protocolmodel/generated_contract_test.go` | Go union/import/type compile evidence |

---

### Task 1: v1.4 Coverage domain Schema 与 fixtures

**Files:**

- Create: `packages/protocol-schema/schema/v1.4/diagnostic.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/test.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/coverage.schema.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-run-start.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-run.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-report.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-run-command.invalid.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-run-environment.invalid.json`
- Create: `packages/protocol-schema/fixtures/v1.4/coverage-run-driver.invalid.json`
- Modify: `packages/protocol-schema/test/schema.test.mjs`

**Interfaces:**

- Copy v1.3 diagnostic/test content, change only `$id`、title/type suffixes and internal/external refs from v1.3 to v1.4.
- `coverage.schema.json` top level is `CoverageContractV14` with required `runStartRequest`、`run`、`runPage`、`report`; each property points to its named `$defs` member so quicktype emits all public types.
- `coverage.schema.json` references `urn:unit-test-ide:protocol:v1.4:test#/$defs/testSelection` and `selectionSnapshot` rather than redefining selection.

- [ ] **Step 1: Create exact valid and invalid fixtures**

Create `coverage-run-start.valid.json` as a complete `coverage/runs/start` request. Its payload is exactly:

```json
{
  "idempotencyKey": "cccccccccccccccccccccccccccccccc",
  "workspaceGeneration": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "projectId": "core",
  "coverageProfileId": "coverage-debug",
  "catalogRevision": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
  "selection": {
    "mode": "items",
    "itemIds": [
      "utid-v1-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
    ]
  },
  "repeatCount": 2,
  "timeoutMs": 60000
}
```

Create `coverage-run.valid.json` as a complete `coverage/runs/get` response with an `available` finished run, `reportId`, `lastSequence: 42`, and the matching `items` selection snapshot. Create `coverage-report.valid.json` as a complete `coverage/reports/get` response with:

```json
{
  "reportId": "88888888888888888888888888888888",
  "coverageRunId": "55555555555555555555555555555555",
  "testRunId": "77777777777777777777777777777777",
  "schemaVersion": "1.0",
  "createdAt": "2026-08-04T00:00:03Z",
  "completeness": { "outcome": "available", "reasons": [] },
  "summary": {
    "lines": { "covered": 10, "total": 12 },
    "branches": { "covered": 4, "total": 6 },
    "functions": { "covered": 3, "total": 3 }
  },
  "toolProvenance": {
    "platform": "windows",
    "architecture": "x64",
    "compiler": { "family": "clang-cl", "version": "22.1.0" },
    "driver": { "name": "llvm-cov", "version": "22.1.0" },
    "collector": { "name": "llvm-cov", "version": "22.1.0" },
    "normalizerVersion": "1.0.0",
    "instrumentationFingerprint": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
  },
  "artifactId": "99999999999999999999999999999999"
}
```

The three invalid fixtures clone the start request and add exactly one forbidden payload member: `command`, `environment`, or `driver`.

- [ ] **Step 2: Write standalone RED tests for coverage payloads**

Add `compileV14Coverage()` that registers v1.4 diagnostic/test then compiles coverage. Validate `fixture.payload` through `$ref` wrappers to `coverageRunStartRequest`、`coverageRun` and `coverageReport`; at this point the complete message is intentionally not compiled.

Add table tests for:

```js
const forbidden = {
  executable: "llvm-cov",
  command: "llvm-cov export",
  args: ["export"],
  argv: ["llvm-cov"],
  flags: ["--format=json"],
  shell: true,
  script: "collect",
  env: { PATH: "C:/attacker" },
  environment: { PATH: "C:/attacker" },
  cwd: "C:/tmp",
  workingDirectory: "C:/tmp",
  hook: "before",
  resultPath: "C:/tmp/result.json",
  driver: "llvm-cov",
  collector: "gcovr",
  gcovrConfig: "attacker.cfg",
  python: "python",
  module: "gcovr",
  plugin: "attacker",
  template: "custom",
  threshold: 80
};
```

Inject every member once into the start payload and once into `payload.selection`; every case must fail. Also test repeat `0/101/1.5/MAX_SAFE+1`、timeout `0/86400001/1.5/MAX_SAFE+1`、unknown selection、empty filter、unsafe summary count、unknown outcome/reason、invalid outcome/reason pair、partial completeness with no reasons、available completeness with reasons、and report `files`/`lines` injection.

- [ ] **Step 3: Run tests and verify RED**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
```

Expected: FAIL because v1.4 coverage files do not exist.

- [ ] **Step 4: Implement closed coverage domain Schema**

Use named `$defs` for IDs、safeCount、cursor、metric、summary、compiler、driver、collector、toolProvenance、completeness、coverageRunStartRequest、coverageRun、coverageRunPage and coverageReport. Encode run state invariants with `allOf` `if/then` branches, including absence checks such as:

```json
{
  "if": { "properties": { "status": { "enum": ["queued", "running"] } }, "required": ["status"] },
  "then": {
    "not": {
      "anyOf": [
        { "required": ["outcome"] },
        { "required": ["reason"] },
        { "required": ["finishedAt"] },
        { "required": ["reportId"] }
      ]
    }
  }
}
```

Add equivalent branches for finished required fields, report-bearing outcomes, unavailable reasons and cancelled reasons. Keep `coverageReport` metadata-only and reuse the Coverage JSON v1 names exactly.

- [ ] **Step 5: Run Task 1 GREEN**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
git diff --check
```

- [ ] **Step 6: Commit Task 1**

```powershell
git add packages/protocol-schema/schema/v1.4/diagnostic.schema.json packages/protocol-schema/schema/v1.4/test.schema.json packages/protocol-schema/schema/v1.4/coverage.schema.json packages/protocol-schema/fixtures/v1.4 packages/protocol-schema/test/schema.test.mjs
git commit -m "feat: define protocol v1.4 coverage domain"
```

---

### Task 2: v1.4 capabilities、Task、events、artifacts 与 messages

**Files:**

- Create: `packages/protocol-schema/schema/v1.4/capabilities.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/task.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/event.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/artifact.schema.json`
- Create: `packages/protocol-schema/schema/v1.4/message.schema.json`
- Modify: `packages/protocol-schema/test/schema.test.mjs`
- Modify: `packages/protocol-schema/package.json`

**Interfaces:**

- `CapabilitiesV14` preserves every v1.3 field and adds required constants `coverageRun: true`、`coverageReport: true`、`maxCoveragePageSize: 200`、`maxCoverageTimeoutMs: 86400000`.
- `CoverageRunTaskSnapshotV14` fields: common Task fields plus `kind: "coverageRun"`、`workspaceGeneration`、`projectId`、`coverageProfileId`、`catalogRevision`、`coverageRunId`、`testRunId`、`repeatCount`、`timeoutMs`.
- `ArtifactKindV14` adds only `coverage-json`、`junit-xml`、`coverage-html`; MIME adds only `application/xml`、`text/html`.
- v1.4 adds methods `coverage/runs/start|get|list` and `coverage/reports/get`; start response payload is `CoverageRun`, not a Task snapshot.

- [ ] **Step 1: Write RED whole-message, capability, Task, event and artifact tests**

Add `compileV14Message()` registering v1.2 workspace plus all seven v1.4 dependency schemas. Validate the three valid fixtures as whole messages and the three negative fixtures as invalid.

Add coverage method request tests:

```text
coverage/runs/start -> CoverageRunStartRequest
coverage/runs/get   -> { coverageRunId }
coverage/runs/list  -> optional projectId/coverageProfileId/cursor/limit
coverage/reports/get -> { reportId }
```

Test list limits `0/201/1.5/MAX_SAFE+1`, empty/4097-char cursor, malformed IDs and extra properties. Validate `CoverageRunPage` with 200 items and reject 201.

Add capabilities success with all v1.3 fields plus the four new required fields; reject a missing coverage field, false constant, unknown capability, page 201 and timeout 86400001.

Validate all three new public artifact kinds with their intended MIME; reject `raw-profile`、`indexed-profile`、`gcda`、`third-party-json` and unknown MIME.

- [ ] **Step 2: Write exact coverage Task and events RED tests**

The coverage Task branch must validate through `tasks/get/list/cancel` responses and reject unknown fields or malformed coverage IDs. v1.4 `tasks/start` must continue rejecting `{ kind: "coverageRun" }`.

Add exactly these five event branches and payloads:

```ts
type CoverageEventV14 =
  | { event: "coverage.run.started"; payload: { coverageRunId: string; testRunId: string; catalogRevision: string; repeatCount: number } }
  | { event: "coverage.build.finished"; payload: { coverageRunId: string } }
  | { event: "coverage.collection.started"; payload: { coverageRunId: string; testRunId: string } }
  | { event: "coverage.report.available"; payload: { coverageRunId: string; reportId: string; artifactId: string; completeness: CoverageCompletenessV14; summary: CoverageSummaryV14 } }
  | { event: "coverage.run.finished"; payload: { coverageRunId: string; outcome: CoverageRunOutcomeV14; reason?: CoverageRunReasonV14; reportId?: string } };
```

Every event reuses the v1.4 event base with `protocolVersion: "1.4"`、`payloadVersion: 1`、32-hex `taskId` and safe monotonic sequence. Reuse the same finished outcome/reason/report invariant as `CoverageRun`. Reject `coverage.file.*` and `coverage.line.*` event names.

Extend `TaskStepKindV14` only with `coverage-configure`、`coverage-build`、`coverage-test`、`coverage-merge`、`coverage-normalize`、`coverage-report`、`coverage-publish`.

- [ ] **Step 3: Run tests and verify RED**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
```

Expected: new whole-message/capability/Task/event/artifact tests FAIL.

- [ ] **Step 4: Implement the v1.4 envelope schemas**

Copy v1.3 as the source of truth, change every v1.3 `$id`/title/ref to v1.4, then add only the deltas listed above. Keep Workspace response refs on `urn:unit-test-ide:protocol:v1.2:workspace`. Handshake advertises `1.4, 1.3, 1.2, 1.1, 1.0` and negotiates exactly `1.4`.

Add only these error codes to the v1.3 set:

```text
COVERAGE_PROFILE_NOT_FOUND
COVERAGE_RUN_NOT_FOUND
COVERAGE_REPORT_NOT_FOUND
```

Export all eight v1.4 schemas from `packages/protocol-schema/package.json` using `./v1.4/message`、`./v1.4/capabilities`、`./v1.4/diagnostic`、`./v1.4/test`、`./v1.4/coverage`、`./v1.4/task`、`./v1.4/event` and `./v1.4/artifact` subpaths.

- [ ] **Step 5: Add compatibility tests**

Assert:

- v1.3 validator accepts its existing test-run fixture and rejects every v1.4 coverage fixture;
- v1.0–v1.2 continue accepting their existing fixture;
- v1.4 accepts a cloned v1.3 test request only after its envelope is changed to `protocolVersion: "1.4"`;
- v1.3 capabilities reject v1.4 coverage fields and v1.3 event rejects coverage event;
- v1.4 message does not expose coverage through `tasks/start`.

- [ ] **Step 6: Run Task 2 GREEN**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
git diff --check
```

- [ ] **Step 7: Commit Task 2**

```powershell
git add packages/protocol-schema/schema/v1.4 packages/protocol-schema/test/schema.test.mjs packages/protocol-schema/package.json
git commit -m "feat: expose protocol v1.4 coverage wire"
```

---

### Task 3: Deterministic v1.4 TypeScript 与 Go generation

**Files:**

- Modify: `tools/protocol-gen/generate.mjs`
- Create: `packages/protocol-models/src/generated/capabilities-v1-4.ts`
- Create: `packages/protocol-models/src/generated/diagnostic-v1-4.ts`
- Create: `packages/protocol-models/src/generated/test-v1-4.ts`
- Create: `packages/protocol-models/src/generated/coverage-v1-4.ts`
- Create: `packages/protocol-models/src/generated/task-v1-4.ts`
- Create: `packages/protocol-models/src/generated/event-v1-4.ts`
- Create: `packages/protocol-models/src/generated/artifact-v1-4.ts`
- Create: `apps/test-service/internal/protocolmodel/v1_4/capabilities/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/diagnostic/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/test/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/coverage/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/task/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/event/generated.go`
- Create: `apps/test-service/internal/protocolmodel/v1_4/artifact/generated.go`

**Interfaces:**

- Add seven v1.4 entries to `models`; `coverage` bundles `test.schema.json`, `event` bundles `diagnostic.schema.json`、`test.schema.json`、`coverage.schema.json`.
- Use exact packages `protocolmodelv14capabilities`、`protocolmodelv14diagnostic`、`protocolmodelv14test`、`protocolmodelv14coverage`、`protocolmodelv14task`、`protocolmodelv14event` and `protocolmodelv14artifact`.
- Manual deterministic templates are required only where quicktype cannot preserve discriminated unions or cross-package type identity: v1.4 test selection、coverage、Task and event.

- [ ] **Step 1: Add generated-contract expectations before generator targets**

Add generator self-assertions/tests inside the script where replacements occur:

- v1.4 test generation must replace the quicktype selection struct/interface with `TestSelectionV14` union;
- coverage TS imports `TestSelectionV14` and `TestSelectionSnapshotV14` from `./test-v1-4.js`;
- coverage Go imports `protocolmodelv14test` from `v1_4/test`;
- event TS imports v1.4 diagnostic/test/coverage types; event Go imports the three matching packages;
- Task and event branches implement their union marker interfaces in Go;
- failed replacements throw stable errors `Unable to create TypeScript union for TestSelectionV14`、`Unable to create Go union for TestSelectionV14`、`Unable to create v1.4 coverage template`、`Unable to create v1.4 task template` or `Unable to create v1.4 event template`, matching the failed branch.

- [ ] **Step 2: Run generation and verify RED**

```powershell
pnpm generate:protocol
```

Expected: FAIL until v1.4 model entries/templates are complete, or generated contract inspection shows missing unions/imports.

- [ ] **Step 3: Add exact model entries and templates**

Add entries equivalent to:

```js
{ directory: "v1.4", schema: "capabilities.schema.json", top: "CapabilitiesV14", ts: "capabilities-v1-4.ts", go: "v1_4/capabilities/generated.go", goPackage: "protocolmodelv14capabilities" },
{ directory: "v1.4", schema: "diagnostic.schema.json", top: "DiagnosticV14", ts: "diagnostic-v1-4.ts", go: "v1_4/diagnostic/generated.go", goPackage: "protocolmodelv14diagnostic" },
{ directory: "v1.4", schema: "test.schema.json", top: "TestContractV14", template: "testV14", ts: "test-v1-4.ts", go: "v1_4/test/generated.go", goPackage: "protocolmodelv14test" },
{ directory: "v1.4", schema: "coverage.schema.json", top: "CoverageContractV14", bundle: ["test.schema.json"], template: "coverageV14", ts: "coverage-v1-4.ts", go: "v1_4/coverage/generated.go", goPackage: "protocolmodelv14coverage" },
{ directory: "v1.4", schema: "task.schema.json", top: "TaskSnapshotV14", template: "taskV14", ts: "task-v1-4.ts", go: "v1_4/task/generated.go", goPackage: "protocolmodelv14task" },
{ directory: "v1.4", schema: "event.schema.json", top: "TaskEventV14", bundle: ["diagnostic.schema.json", "test.schema.json", "coverage.schema.json"], template: "eventV14", ts: "event-v1-4.ts", go: "v1_4/event/generated.go", goPackage: "protocolmodelv14event" },
{ directory: "v1.4", schema: "artifact.schema.json", top: "ArtifactMetadataV14", ts: "artifact-v1-4.ts", go: "v1_4/artifact/generated.go", goPackage: "protocolmodelv14artifact" }
```

The TS templates export the exact interfaces/enums in this plan. The Go templates use `int64` for integer fields, `time.Time` for timestamps, pointers for optional scalar/time values, versioned imported types for selection/diagnostic/summary, and marker methods for every union branch. Do not use `map[string]any` or `interface{}` except the closed marker union interfaces.

- [ ] **Step 4: Generate and verify deterministic drift**

```powershell
pnpm generate:protocol
pnpm check:protocol-generated
go test ./apps/test-service/internal/protocolmodel -count=1
git diff --check
```

Run `pnpm generate:protocol` a second time and assert `git diff --exit-code` for all already tracked files plus no content change in new generated files.

- [ ] **Step 5: Commit Task 3**

```powershell
git add tools/protocol-gen/generate.mjs packages/protocol-models/src/generated apps/test-service/internal/protocolmodel/v1_4
git commit -m "feat: generate protocol v1.4 coverage models"
```

---

### Task 4: Public exports、generated contract tests 与 final compatibility gate

**Files:**

- Modify: `packages/protocol-models/src/index.ts`
- Modify: `packages/protocol-models/src/generated-contract.test.ts`
- Modify: `apps/test-service/internal/protocolmodel/generated_contract_test.go`

**Interfaces:**

- Public TypeScript exports use v1.4-suffixed names for every symbol that would collide with v1.3.
- Required public types include `CapabilitiesV14`、`CoverageRunStartRequest`、`CoverageRun`、`CoverageRunPage`、`CoverageReport`、`CoverageEventV14`、`TaskSnapshotV14` and all their closed enum/support types.
- Go compile test imports all seven `v1_4` packages and proves TestSelection、Coverage Task、TaskEvent/CoverageEvent branches satisfy their union interfaces.

- [ ] **Step 1: Write RED TypeScript public contract test**

Construct these typed values without casts:

- Windows clang-cl `CapabilitiesV14`;
- item-based `CoverageRunStartRequest` with timeout/repeat;
- finished `CoverageRun` with `available` outcome;
- `CoverageReport` with safe summary and LLVM provenance;
- `CoverageRunPage`;
- `CoverageRunTaskSnapshotV14` assignable to `TaskSnapshotV14`;
- all five coverage events assignable to both `CoverageEventV14` and `TaskEventV14`;
- `ArtifactMetadataV14` using each new kind/MIME enum.

Assert representative runtime enums have exact wire values and no v1.3 import/export is renamed or removed.

- [ ] **Step 2: Write RED Go compile contract test**

Add `TestGeneratedV14ModelsCompile` following the v1.3 test. It assigns:

```go
var selection testv14.TestSelectionV14 = testv14.ItemsTestSelectionV14{}
var task taskv14.TaskSnapshotV14 = taskv14.CoverageRunTaskSnapshotV14{}
var event eventv14.TaskEventV14 = eventv14.CoverageRunFinishedEventV14{}
var coverageEvent eventv14.CoverageEventV14 = eventv14.CoverageReportAvailableEventV14{}
```

Also instantiate `CapabilitiesV14`、`DiagnosticV14`、`CoverageRunStartRequest`、`CoverageRun`、`CoverageRunPage`、`CoverageReport` and `ArtifactMetadataV14` to prove imports/type names compile.

- [ ] **Step 3: Run contract tests and verify RED**

```powershell
pnpm --filter @unit-test-ide/protocol-models test
go test ./apps/test-service/internal/protocolmodel -count=1
```

Expected: FAIL until public exports and compile checks are implemented.

- [ ] **Step 4: Export exact v1.4 public API and make tests GREEN**

Add explicit `export type` and enum exports from all seven `*-v1-4.js` files. Do not use wildcard exports because v1.3/v1.4 quicktype names can collide.

```powershell
pnpm --filter @unit-test-ide/protocol-models test
go test ./apps/test-service/internal/protocolmodel -count=1
pnpm check:protocol-generated
git diff --check
```

- [ ] **Step 5: Verify legacy files and forbidden surface**

Because this plan creates exactly four Task commits, derive the slice base from the parent of Task 1, then run:

```powershell
$sliceBase = git rev-parse HEAD~4
git diff --exit-code $sliceBase -- packages/protocol-schema/schema/v1 packages/protocol-schema/schema/v1.1 packages/protocol-schema/schema/v1.2 packages/protocol-schema/schema/v1.3 packages/protocol-schema/fixtures/v1 packages/protocol-schema/fixtures/v1.1 packages/protocol-schema/fixtures/v1.2 packages/protocol-schema/fixtures/v1.3
git diff "$sliceBase..HEAD" -- packages/protocol-schema/schema/v1.4 packages/protocol-schema/fixtures/v1.4 tools/protocol-gen packages/protocol-models apps/test-service/internal/protocolmodel | rg -n '"(executable|command|args|argv|flags|shell|script|env|environment|cwd|workingDirectory|hook|resultPath|driver|collector|gcovrConfig|python|module|plugin|template|threshold)"\s*:'
```

Expected: legacy Schema/fixtures diff is empty. The forbidden scan may match only negative fixtures/tests and output-only `toolProvenance.driver`/`collector` definitions; it must not match any start-request accepted property or generated `CoverageRunStartRequest` field.

- [ ] **Step 6: Run the complete Protocol v1.4 slice gate**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
pnpm generate:protocol
pnpm --filter @unit-test-ide/protocol-models test
go test ./apps/test-service/internal/protocolmodel -count=1
pnpm check:protocol-generated
git diff --check
```

Expected: all commands PASS and the post-generation working tree has no generated drift.

- [ ] **Step 7: Commit Task 4**

```powershell
git add packages/protocol-models/src/index.ts packages/protocol-models/src/generated-contract.test.ts apps/test-service/internal/protocolmodel/generated_contract_test.go
git commit -m "feat: publish protocol v1.4 coverage contracts"
```

## Completion Gate

After all four Tasks have passed spec-compliance and code-quality review:

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
pnpm generate:protocol
pnpm --filter @unit-test-ide/protocol-models test
go test ./apps/test-service/internal/protocolmodel -count=1
pnpm check:protocol-generated
pnpm check:coverage-generated
git diff --check
```

The final whole-slice review must confirm:

- v1.0–v1.3 Schema、fixtures and generated models are unchanged;
- v1.4 retains every v1.3 non-coverage method and adds only the approved coverage methods;
- CoverageRun request surface is structural and cannot control process/tool execution;
- Workspace coverage profile ID is the human-readable stable identifier, while generation/catalog IDs remain 64-hex revisions;
- outcome/reason/status combinations are closed and consistent across run and finished event;
- summary/provenance match Coverage JSON v1 metadata while file/line bodies remain artifact-only;
- public artifacts expose exactly JSON/JUnit/HTML report kinds and never expose raw profiles;
- TypeScript and Go union models preserve branch discrimination without untyped maps;
- generation is reproducible and `--check` catches any drift.
