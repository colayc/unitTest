# TypeScript Client v1.4 Coverage API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `@unit-test-ide/test-client` 完整协商并验证 Protocol v1.4，保持既有 API 在 v1.4 Session 中可用，并公开四个 strict Coverage API。

**Design:** `docs/superpowers/specs/2026-08-05-typescript-client-v1-4-coverage-design.md`（已确认，commit `a5b3ce36ed66ff62836a38046db6e0465e41dd8d`）。

**Architecture:** 沿用现有显式版本分支，为 v1.4 平行增加 whole-message validator、payload validator、model decoder 和 public method。`Connection` 负责 closed envelope；`ProtocolClient` 负责版本门禁与 request/response payload；`decoders.ts` 负责 Date、safe integer、cross-field semantics 和 defensive clone，且 Coverage JSON artifact body 仍交给独立 coverage-models decoder。

**Tech Stack:** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、AJV 8.20.0、JSON Schema Draft 2020-12、Node `node:test`、Protocol v1.4 generated models。

## Global Constraints

- 实施基线固定为设计提交 `a5b3ce36ed66ff62836a38046db6e0465e41dd8d`，目标分支为 `codex/workspace-cmake-toolchains`。
- 只修改 `packages/test-client` 与本计划/父计划状态；不实现 Go Service handler、Runtime 或 Session dispatch。
- Protocol v1.4 是 additive version；v1.0–v1.3 schema、method、public behavior 与 fallback 语义不得改变。
- Client 不接受 executable、Shell、raw args、environment、working directory、collector option、Python module/script 或 report template。
- Coverage API 只允许 structured ID、selection、repeat 与 timeout；所有 extra field 必须在写 wire 前拒绝。
- 只有 negotiated v1.4 Session 能调用 Coverage API；旧 Session local rejection 必须保持 wire write count 不变。
- `packages/protocol-schema` 是 wire shape 唯一事实来源；不得手写更宽松的替代 schema。
- generated TypeScript model 只提供 compile-time type；所有 inbound value 必须经过 runtime schema 与 semantic decoder。
- Client 不解析 Coverage JSON artifact body，只返回 `CoverageReport.artifactId`。
- 所有 nested array/object 返回 defensive clone；unknown enum、invalid Date、unsafe integer 与 cross-field inconsistency 必须 fail closed。
- 不进行通用 schema registry 重构，不拆分现有大文件，不做与 v1.4 Coverage 无关的 refactor。
- 所有 Markdown 使用中文，English technical terms 保留 English 格式。
- 每个 Task 使用 red-green-refactor TDD、聚焦 commit 和独立 review；全部提交推送 GitHub 与 Gitee 同名分支。

## File Structure

- `packages/test-client/src/envelopes.ts`：ProtocolVersion、Method、event union 与 envelope public types。
- `packages/test-client/src/connection.ts`：各 wire version 的 complete schema registration、whole-message validation、request routing 与 protocol rank。
- `packages/test-client/src/client.ts`：public inputs/API、payload validators、version gates、handshake fallback 和 response routing。
- `packages/test-client/src/decoders.ts`：v1.4 Task/Test/Artifact/Event/Coverage runtime decoder 与 defensive clone。
- `packages/test-client/src/index.ts`：稳定 public exports。
- `packages/test-client/src/client.test.ts`：in-memory duplex protocol、decoder、compatibility 与 adversarial regression tests。

---

### Task 1: Protocol v1.4 Envelope、Whole-Message Validation 与 Negotiation

**Files:**

- Modify: `packages/test-client/src/envelopes.ts:1-21`
- Modify: `packages/test-client/src/connection.ts:1-40,164-228,252-262`
- Modify: `packages/test-client/src/client.ts:133-150,680-724`
- Test: `packages/test-client/src/client.test.ts:246-288,992-1050,1452-1490`

**Interfaces:**

- Consumes: `@unit-test-ide/protocol-schema/v1.4/{capabilities,diagnostic,test,coverage,task,event,artifact,message}`。
- Produces: `ProtocolVersion = "1.0" | "1.1" | "1.2" | "1.3" | "1.4"`、四个 Coverage method、v1.4 whole-message validator，以及优先 v1.4 的 handshake/fallback。

- [ ] **Step 1: 写出 v1.4 negotiation 与 whole-message RED tests**

把现有最高版本测试提升为 v1.4，并保留所有 downgrade：

```ts
test("client prefers protocol 1.4 and accepts negotiated downgrades", async () => {
  for (const negotiated of ["1.4", "1.3", "1.2", "1.1"] as const) {
    const fixture = scriptedClient((request) => response(request, {
      negotiatedProtocolVersion: negotiated,
      serviceVersion: "0.5.0"
    }, negotiated));
    const result = await fixture.client.handshake("0123456789abcdef", "test", "0.5.0");
    assert.equal(result.negotiatedProtocolVersion, negotiated);
    assert.equal(fixture.requests[0]?.protocolVersion, "1.4");
    assert.deepEqual((fixture.requests[0]?.payload as JsonObject).supportedProtocolVersions,
      ["1.4", "1.3", "1.2", "1.1", "1.0"]);
    fixture.client.close();
  }
});

test("client retries legacy services from the v1.4 ceiling", async () => {
  const fixture = scriptedClient((request) => request.protocolVersion === "1.2"
    ? response(request, { negotiatedProtocolVersion: "1.2", serviceVersion: "0.3.0" }, "1.2")
    : error(request, "UNSUPPORTED_PROTOCOL", false, "1.0"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.5.0");
  assert.deepEqual(fixture.requests.map(({ protocolVersion }) => protocolVersion), ["1.4", "1.3", "1.2"]);
  fixture.client.close();
});
```

同时扩展 `Connection validates all handshake request versions before writing`，要求 `1.4` request 被 v1.4 message schema 验证；增加 malformed v1.4 response/event extra-field case，预期 connection close。

- [ ] **Step 2: 运行 Client tests 并确认 RED**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64') + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
```

Expected: FAIL，原因至少包括 `ProtocolVersion` 不接受 `1.4`、首次 handshake 仍发送 `1.3`、`validators["1.4"]` 不存在。

- [ ] **Step 3: 扩展 envelope 与 Connection schema registration**

`envelopes.ts`：

```ts
import type { TaskEvent, TaskEventV12, TaskEventV13, TaskEventV14 } from "@unit-test-ide/protocol-models";

export type ProtocolVersion = "1.0" | "1.1" | "1.2" | "1.3" | "1.4";
export type Method =
  | "handshake"
  | "capabilities/get"
  | "shutdown"
  | "tasks/start"
  | "tasks/get"
  | "tasks/list"
  | "tasks/cancel"
  | "events/subscribe"
  | "artifacts/list"
  | "artifacts/read"
  | "workspace/inspect"
  | "cmake/targets/list"
  | "tests/catalog/get"
  | "tests/runs/get"
  | "tests/runs/list"
  | "coverage/runs/start"
  | "coverage/runs/get"
  | "coverage/runs/list"
  | "coverage/reports/get";
export type ProtocolTaskEvent = TaskEvent | TaskEventV12 | TaskEventV13 | TaskEventV14;
```

`connection.ts` 在 v1.3 schemas 后注册完整 v1.4 dependency set，并编译 message：

```ts
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/capabilities"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/diagnostic"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/test"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/coverage"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/artifact"));

const validators: Record<ProtocolVersion, ValidateFunction> = {
  "1.0": ajv.compile(require("@unit-test-ide/protocol-schema/v1/message")),
  "1.1": ajv.compile(require("@unit-test-ide/protocol-schema/v1.1/message")),
  "1.2": ajv.compile(require("@unit-test-ide/protocol-schema/v1.2/message")),
  "1.3": ajv.compile(require("@unit-test-ide/protocol-schema/v1.3/message")),
  "1.4": ajv.compile(require("@unit-test-ide/protocol-schema/v1.4/message"))
};
```

版本 predicate/rank 必须精确扩展：

```ts
function isProtocolVersion(value: unknown): value is ProtocolVersion {
  return value === "1.0" || value === "1.1" || value === "1.2" || value === "1.3" || value === "1.4";
}

function protocolRank(version: ProtocolVersion): number {
  return { "1.0": 0, "1.1": 1, "1.2": 2, "1.3": 3, "1.4": 4 }[version];
}
```

- [ ] **Step 4: 提升 handshake validator 与 fallback attempts**

`validateHandshakeV12` 改名为能表达 shared modern handshake 的 `validateHandshakeModern`，enum 增加 `1.4`。`#authenticate` 使用：

```ts
const attempts: ReadonlyArray<{
  version: "1.4" | "1.3" | "1.2" | "1.1";
  offered: ProtocolVersion[];
}> = [
  { version: "1.4", offered: ["1.4", "1.3", "1.2", "1.1", "1.0"] },
  { version: "1.3", offered: ["1.3", "1.2", "1.1", "1.0"] },
  { version: "1.2", offered: ["1.2", "1.1", "1.0"] },
  { version: "1.1", offered: ["1.1", "1.0"] }
];
```

只捕获 `ProtocolError` 且 code 为 `UNSUPPORTED_PROTOCOL`；其他 error 原样抛出。

- [ ] **Step 5: 运行 Task 1 tests、build 与 diff check**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client build
git diff --check
```

Expected: PASS；旧 handshake security tests 仍全部执行，v1.4 malformed envelope fail closed。

- [ ] **Step 6: 提交 Task 1**

```powershell
git add -- packages/test-client/src/envelopes.ts packages/test-client/src/connection.ts packages/test-client/src/client.ts packages/test-client/src/client.test.ts
git commit -m "feat: negotiate protocol v1.4"
```

---

### Task 2: CoverageRun、CoverageRunPage 与 CoverageReport Strict Decoders

**Files:**

- Modify: `packages/test-client/src/decoders.ts:1-40,548`
- Test: `packages/test-client/src/client.test.ts:1-245,468-618`

**Interfaces:**

- Consumes: generated `CoverageRun`、`CoverageRunPage`、`CoverageReport`、`CoverageSummaryV14`、`CoverageCompletenessV14`、`CoverageToolProvenanceV14`。
- Produces: `decodeCoverageRun(value)`、`decodeCoverageRunPage(value)`、`decodeCoverageReport(value)`；Task 3 v1.4 events 和 Task 4 public methods 复用这些 functions。

- [ ] **Step 1: 增加 canonical Coverage fixtures 与 decoder RED tests**

在 test constants 增加 `COVERAGE_RUN_ID`、`REPORT_ID`、`COVERAGE_PROFILE_ID`，并创建：

```ts
function coverageRun(overrides: JsonObject = {}): JsonObject {
  return {
    coverageRunId: "a".repeat(32),
    taskId: TASK_ID,
    testRunId: RUN_ID,
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    coverageProfileId: "coverage-debug",
    catalogRevision: CATALOG_REVISION,
    selectionSnapshot: { mode: "items", containerIds: [], itemIds: [ITEM_ID] },
    repeatCount: 1,
    timeoutMs: 60_000,
    status: "finished",
    outcome: "available",
    createdAt: SENT_AT,
    startedAt: SENT_AT,
    finishedAt: SENT_AT,
    reportId: "b".repeat(32),
    lastSequence: 9,
    ...overrides
  };
}

function coverageReport(overrides: JsonObject = {}): JsonObject {
  return {
    reportId: "b".repeat(32),
    coverageRunId: "a".repeat(32),
    testRunId: RUN_ID,
    schemaVersion: "1.0",
    createdAt: SENT_AT,
    completeness: { outcome: "available", reasons: [] },
    summary: {
      lines: { covered: 8, total: 10 },
      branches: { covered: 3, total: 4 },
      functions: { covered: 2, total: 2 }
    },
    toolProvenance: {
      platform: "linux",
      architecture: "x64",
      compiler: { family: "clang", version: "18.1.8" },
      driver: { name: "llvm-cov", version: "18.1.8" },
      collector: { name: "llvm-cov", version: "18.1.8" },
      normalizerVersion: "1.0.0",
      instrumentationFingerprint: "c".repeat(64)
    },
    artifactId: ARTIFACT_ID,
    ...overrides
  };
}
```

RED assertions 必须覆盖：

```ts
test("coverage decoders clone nested values and convert dates", () => {
  const wireRun = coverageRun();
  const wireReport = coverageReport();
  const run = decodeCoverageRun(wireRun);
  const report = decodeCoverageReport(wireReport);
  assert.ok(run.createdAt instanceof Date);
  assert.ok(report.createdAt instanceof Date);
  (wireRun.selectionSnapshot as JsonObject).itemIds = [];
  (wireReport.summary as JsonObject).lines = { covered: 0, total: 0 };
  assert.deepEqual(run.selectionSnapshot.itemIds, [ITEM_ID]);
  assert.equal(report.summary.lines.covered, 8);
});

test("coverage decoders reject unsafe and inconsistent domain values", () => {
  assert.throws(() => decodeCoverageRun(coverageRun({ lastSequence: Number.MAX_SAFE_INTEGER + 1 })), /safe integer/i);
  assert.throws(() => decodeCoverageRun(coverageRun({ status: "finished", outcome: "available", reportId: undefined })), /report/i);
  assert.throws(() => decodeCoverageReport(coverageReport({
    summary: { lines: { covered: 11, total: 10 }, branches: { covered: 0, total: 0 }, functions: { covered: 0, total: 0 } }
  })), /covered|total/i);
});
```

- [ ] **Step 2: 运行 tests 并确认 decoder symbols 缺失**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
```

Expected: TypeScript build FAIL，指出 `decodeCoverageRun`、`decodeCoverageRunPage`、`decodeCoverageReport` 未导出。

- [ ] **Step 3: 实现 bounded Coverage helpers**

在 `decoders.ts` 复用 `record`、`date`、`optionalDate`、`safeInteger`、`iteration`，新增：

```ts
function decodeCoverageMetric(value: unknown, name: string): CoverageMetricV14 {
  const wire = record(value, name);
  const covered = safeInteger(wire.covered, `${name} covered`);
  const total = safeInteger(wire.total, `${name} total`);
  if (covered < 0 || total < 0 || covered > total) throw new Error(`${name} covered exceeds total`);
  return { covered, total };
}

function decodeCoverageSummary(value: unknown): CoverageSummaryV14 {
  const wire = record(value, "coverage summary");
  return {
    lines: decodeCoverageMetric(wire.lines, "coverage lines"),
    branches: decodeCoverageMetric(wire.branches, "coverage branches"),
    functions: decodeCoverageMetric(wire.functions, "coverage functions")
  };
}
```

`decodeCoverageCompleteness` 必须 clone `reasons`，并要求 available 对应空 reasons、partial 对应非空 unique reasons。`decodeCoverageToolProvenance` 必须 clone compiler/driver/collector objects，不保留 wire alias：

```ts
function decodeCoverageCompleteness(value: unknown): CoverageCompletenessV14 {
  const wire = record(value, "coverage completeness");
  const reasons = [...(wire.reasons as CoverageIncompleteReasonV14[])];
  if (new Set(reasons).size !== reasons.length) throw new Error("coverage completeness reasons are not unique");
  if (wire.outcome === "available" && reasons.length !== 0) {
    throw new Error("available coverage completeness contains reasons");
  }
  if (wire.outcome === "partial" && reasons.length === 0) {
    throw new Error("partial coverage completeness is missing reasons");
  }
  return { outcome: wire.outcome, reasons } as CoverageCompletenessV14;
}

function decodeCoverageToolProvenance(value: unknown): CoverageToolProvenanceV14 {
  const wire = record(value, "coverage tool provenance");
  return {
    platform: wire.platform,
    architecture: wire.architecture,
    compiler: { ...record(wire.compiler, "coverage compiler") },
    driver: { ...record(wire.driver, "coverage driver") },
    collector: { ...record(wire.collector, "coverage collector") },
    normalizerVersion: wire.normalizerVersion,
    instrumentationFingerprint: wire.instrumentationFingerprint
  } as CoverageToolProvenanceV14;
}
```

- [ ] **Step 4: 实现 run/page/report decoder 与 lifecycle checks**

```ts
export function decodeCoverageRun(value: unknown): CoverageRun {
  const wire = record(value, "coverage run");
  const selection = record(wire.selectionSnapshot, "coverage selection snapshot");
  if (wire.status !== "finished" &&
      (wire.outcome !== undefined || wire.reason !== undefined || wire.finishedAt !== undefined || wire.reportId !== undefined)) {
    throw new Error("non-terminal coverage run contains terminal metadata");
  }
  if ((wire.outcome === "available" || wire.outcome === "partial") && wire.reportId === undefined) {
    throw new Error("report-bearing coverage run is missing reportId");
  }
  if ((wire.outcome === "unavailable" || wire.outcome === "cancelled") && wire.reportId !== undefined) {
    throw new Error("report-free coverage run contains reportId");
  }
  if (wire.status === "finished" && (wire.outcome === undefined || wire.finishedAt === undefined)) {
    throw new Error("finished coverage run is missing terminal metadata");
  }
  if ((wire.outcome === "available" || wire.outcome === "partial") && wire.reason !== undefined) {
    throw new Error("report-bearing coverage run contains failure reason");
  }
  const cancelledReason = wire.reason === "user_cancelled" || wire.reason === "task_timed_out";
  if (wire.outcome === "cancelled" && !cancelledReason) {
    throw new Error("cancelled coverage run has an invalid reason");
  }
  if (wire.outcome === "unavailable" && (wire.reason === undefined || cancelledReason)) {
    throw new Error("unavailable coverage run has an invalid reason");
  }
  return {
    coverageRunId: wire.coverageRunId,
    taskId: wire.taskId,
    testRunId: wire.testRunId,
    workspaceGeneration: wire.workspaceGeneration,
    projectId: wire.projectId,
    coverageProfileId: wire.coverageProfileId,
    catalogRevision: wire.catalogRevision,
    selectionSnapshot: {
      ...selection,
      containerIds: [...(selection.containerIds as string[])],
      itemIds: [...(selection.itemIds as string[])]
    },
    repeatCount: iteration(wire.repeatCount, "coverage repeatCount"),
    timeoutMs: safeInteger(wire.timeoutMs, "coverage timeoutMs"),
    status: wire.status,
    createdAt: date(wire.createdAt, "coverage createdAt"),
    lastSequence: safeInteger(wire.lastSequence, "coverage lastSequence"),
    ...(wire.outcome === undefined ? {} : { outcome: wire.outcome }),
    ...(wire.reason === undefined ? {} : { reason: wire.reason }),
    ...(wire.startedAt === undefined ? {} : { startedAt: date(wire.startedAt, "coverage startedAt") }),
    ...(wire.finishedAt === undefined ? {} : { finishedAt: date(wire.finishedAt, "coverage finishedAt") }),
    ...(wire.reportId === undefined ? {} : { reportId: wire.reportId })
  } as CoverageRun;
}

export function decodeCoverageRunPage(value: unknown): CoverageRunPage {
  const wire = record(value, "coverage run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeCoverageRun(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as CoverageRunPage;
}

export function decodeCoverageReport(value: unknown): CoverageReport {
  const wire = record(value, "coverage report");
  return {
    reportId: wire.reportId,
    coverageRunId: wire.coverageRunId,
    testRunId: wire.testRunId,
    schemaVersion: wire.schemaVersion,
    createdAt: date(wire.createdAt, "coverage report createdAt"),
    completeness: decodeCoverageCompleteness(wire.completeness),
    summary: decodeCoverageSummary(wire.summary),
    toolProvenance: decodeCoverageToolProvenance(wire.toolProvenance),
    artifactId: wire.artifactId
  } as CoverageReport;
}
```

`decodeCoverageRunPage` map 每个 item 并复制 cursor；`decodeCoverageReport` 使用 bounded summary/completeness/provenance helpers，转换 `createdAt`，复制所有 scalar IDs。

- [ ] **Step 5: 运行 decoder/full Client tests 与 diff check**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
git diff --check
```

Expected: PASS；invalid Date、unsafe integer、covered > total、错误 lifecycle、nested alias tests 全部绿色。

- [ ] **Step 6: 提交 Task 2**

```powershell
git add -- packages/test-client/src/decoders.ts packages/test-client/src/client.test.ts
git commit -m "feat: decode coverage client models"
```

---

### Task 3: v1.4 Existing API Compatibility Projection

**Files:**

- Modify: `packages/test-client/src/client.ts:1-230,282-580,724-770`
- Modify: `packages/test-client/src/decoders.ts:1-375`
- Modify: `packages/test-client/src/index.ts:20-47`
- Test: `packages/test-client/src/client.test.ts:289-665,831-888,1491-1513`

**Interfaces:**

- Consumes: Task 1 v1.4 envelope/negotiation；Task 2 Coverage summary/completeness decoders。
- Produces: v1.4-compatible `getCapabilities`、Task/Test/Artifact/Event APIs、`TaskProtocolVersion` projection 和 public v1.4 model exports；Task 4 可安全在最高版本 Session 上增加 Coverage methods。

- [ ] **Step 1: 写出 v1.4 现有 API 与 event RED tests**

增加一个 negotiated v1.4 fixture，返回完整 `CapabilitiesV14`，并验证：

```ts
test("protocol 1.4 keeps existing task, test, artifact, and event APIs usable", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.4", serviceVersion: "0.5.0" }, "1.4");
    }
    if (request.method === "capabilities/get") {
      return response(request, {
        workspaceInspect: true,
        targetList: true,
        cmakeBuild: true,
        testDiscovery: true,
        testRun: true,
        frameworkAdapters: [],
        opaqueCTestFallback: true,
        ctestJson: true,
        maxRepeatCount: 100,
        maxSelectionSize: 100000,
        maxCatalogPageSize: 1000,
        unityHelperContractVersion: "1",
        unityRunnerContractVersion: "utide.runner.v1",
        coverageRun: true,
        coverageReport: true,
        maxCoveragePageSize: 200,
        maxCoverageTimeoutMs: 86400000
      }, "1.4");
    }
    if (request.method === "tasks/get") return response(request, taskSnapshot(), "1.4");
    if (request.method === "tests/runs/get") return response(request, testRun(), "1.4");
    if (request.method === "artifacts/list") {
      return response(request, { items: [{
        artifactId: ARTIFACT_ID,
        taskId: TASK_ID,
        kind: "coverage-json",
        mimeType: "application/json",
        sizeBytes: 10,
        sha256: "a".repeat(64),
        createdAt: SENT_AT,
        uri: "unit-test-ide://artifacts/" + ARTIFACT_ID
      }] }, "1.4");
    }
    return undefined;
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.5.0");
  const capabilities = await fixture.client.getCapabilities();
  const task = await fixture.client.getTask(TASK_ID);
  const run = await fixture.client.getTestRun(RUN_ID);
  const artifacts = await fixture.client.listArtifacts(TASK_ID);
  assert.equal("coverageRun" in capabilities && capabilities.coverageRun, true);
  assert.ok(task.createdAt instanceof Date);
  assert.ok(run.startedAt instanceof Date);
  assert.ok(artifacts.items[0]?.createdAt instanceof Date);
  assert.deepEqual(fixture.requests.slice(1).map(({ protocolVersion }) => protocolVersion), ["1.4", "1.4", "1.4", "1.4"]);
});
```

通过 raw server 注入一个 v1.4 `coverage.report.available` event，断言 sequence safe、sentAt 为 `Date`、summary/completeness nested object 已 clone。再用 `decodeTaskEvent` table 覆盖五个 Coverage event payload：

```ts
const coverageEventPayloads = [
  ["coverage.run.started", {
    coverageRunId: COVERAGE_RUN_ID,
    testRunId: RUN_ID,
    catalogRevision: CATALOG_REVISION,
    repeatCount: 1
  }],
  ["coverage.build.finished", { coverageRunId: COVERAGE_RUN_ID }],
  ["coverage.collection.started", { coverageRunId: COVERAGE_RUN_ID, testRunId: RUN_ID }],
  ["coverage.report.available", {
    coverageRunId: COVERAGE_RUN_ID,
    reportId: REPORT_ID,
    artifactId: ARTIFACT_ID,
    completeness: { outcome: "available", reasons: [] },
    summary: (coverageReport().summary as JsonObject)
  }],
  ["coverage.run.finished", {
    coverageRunId: COVERAGE_RUN_ID,
    outcome: "available",
    reportId: REPORT_ID
  }]
] as const;

for (const [event, payload] of coverageEventPayloads) {
  const decoded = decodeTaskEvent({
    protocolVersion: "1.4",
    kind: "event",
    messageId: MESSAGE_ID,
    sentAt: SENT_AT,
    sequence: 1,
    event,
    taskId: TASK_ID,
    payloadVersion: 1,
    payload
  });
  assert.notEqual(decoded.payload, payload);
}

assert.throws(() => decodeTaskEvent({
  protocolVersion: "1.4",
  kind: "event",
  messageId: MESSAGE_ID,
  sentAt: SENT_AT,
  sequence: 1,
  event: "coverage.run.started",
  taskId: TASK_ID,
  payloadVersion: 1,
  payload: { coverageRunId: COVERAGE_RUN_ID, testRunId: RUN_ID, catalogRevision: CATALOG_REVISION, repeatCount: 101 }
}), /repeatCount/i);
```

再加入 invalid v1.4 Artifact `uri`、`sha256`、`sizeBytes` 与 Date response cases，必须拒绝。

- [ ] **Step 2: 运行 tests 并确认 v1.4 分支缺失**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
```

Expected: FAIL，原因包括 v1.4 payload validators/decoders 缺失、`#requireV13` 拒绝 v1.4、Task/Artifact union 不含 V14。

- [ ] **Step 3: 注册 v1.4 payload validators 与 public unions**

`client.ts` 注册 v1.4 capabilities、diagnostic、test、coverage、task、event、artifact 和 message schemas；message registration 让 Task 4 能直接引用其 closed request payload defs。创建：

```ts
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/capabilities"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/diagnostic"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/test"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/coverage"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/event"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/artifact"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/message"));

const validateCapabilitiesV14 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.4:capabilities") as ValidateFunction;
const validateTaskV14 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.4:task") as ValidateFunction;
const validateTaskPageV14 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.4:task" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateArtifactPageV14 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.4:artifact" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
```

Public unions 精确增加 `CapabilitiesV14`、`TaskSnapshotV14`、`ArtifactMetadataV14`、`TaskEventV14`、`TestCatalogV14`、`TestRunV14`、`TestRunPageV14`：

```ts
export type ProtocolTaskSnapshot = TaskSnapshot | TaskSnapshotV12 | TaskSnapshotV13 | TaskSnapshotV14;
export type ProtocolArtifactMetadata = ArtifactMetadata | ArtifactMetadataV12 | ArtifactMetadataV13 | ArtifactMetadataV14;
export type ProtocolTestCatalog = TestCatalog | TestCatalogV14;
export type ProtocolTestRun = TestRun | TestRunV14;
export type ProtocolTestRunPage = TestRunPage | TestRunPageV14;
```

- [ ] **Step 4: 实现 v1.4 Task、Artifact 与 Event decoders**

新增 `decodeTaskSnapshotV14`，显式 switch 五种 kind。Coverage task 分支必须为：

```ts
case "coverageRun":
  return {
    ...common,
    kind: "coverageRun",
    workspaceGeneration: wire.workspaceGeneration,
    projectId: wire.projectId,
    coverageProfileId: wire.coverageProfileId,
    catalogRevision: wire.catalogRevision,
    coverageRunId: wire.coverageRunId,
    testRunId: wire.testRunId,
    repeatCount: iteration(wire.repeatCount, "task repeatCount"),
    timeoutMs: safeInteger(wire.timeoutMs, "task timeoutMs")
  } as unknown as TaskSnapshotV14;
```

`decodeArtifactMetadataV14` 复制 URI/digest，并转换 Date/safe size。`decodeTaskEvent` 增加首个 `1.4` branch；`decodeTaskEventV14` 保留 v1.3 Test event semantic checks，同时对 Coverage event 的 summary、completeness 和 nested arrays 使用 Task 2 helpers，不能返回 raw payload alias。

v1.4 Test schema 只变更 generated type 名称，wire shape 与 v1.3 相同，因此以窄 wrapper 复用已验证的 clone、Date、safe integer 和引用一致性逻辑，不复制整套 decoder：

```ts
export function decodeTestCatalogV14(value: unknown): TestCatalogV14 {
  return decodeTestCatalog(value) as unknown as TestCatalogV14;
}

export function decodeTestRunV14(value: unknown): TestRunV14 {
  return decodeTestRun(value) as unknown as TestRunV14;
}

export function decodeTestRunPageV14(value: unknown): TestRunPageV14 {
  const wire = record(value, "test run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeTestRunV14(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as TestRunPageV14;
}
```

`decodeTaskEventV14` 先复制 payload，再按 event name 执行下面的精确转换：

- `task.step_finished`：对 optional `exitCode` 调用 `optionalSafeInteger`。
- `task.diagnostic`：复制 diagnostic，并转换 optional `line`/`column`。
- `test.item.finished`：调用 `decodeTestItemResult`。
- `test.run.finished`：调用 `decodeTestRunSummary` 和 `validateTestRunSummary`。
- `test.container.started`、`test.item.started`、`test.output`、`test.container.finished`：对 `iteration` 调用 `iteration`。
- `coverage.run.started`：对 `repeatCount` 调用 `iteration`。
- `coverage.report.available`：调用 `decodeCoverageCompleteness` 和 `decodeCoverageSummary`。
- `coverage.build.finished`、`coverage.collection.started`、`coverage.run.finished`：只复制 schema 已验证的 scalar fields，并为每个 event 返回新 payload object。

最后统一转换 envelope 的 `sentAt`、`sequence`、`payloadVersion`，并返回 `TaskEventV14`；不得把 v1.4 event cast 成 v1.3 类型。

- [ ] **Step 5: 让全部 existing methods 使用 negotiated v1.4**

版本 gates 改为返回实际 version：

```ts
#requireV12(): "1.2" | "1.3" | "1.4" {
  const version = this.#requireAuthentication();
  if (version !== "1.2" && version !== "1.3" && version !== "1.4") {
    throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.2 or newer was not negotiated", false);
  }
  return version;
}

#requireV13(): "1.3" | "1.4" {
  const version = this.#requireAuthentication();
  if (version !== "1.3" && version !== "1.4") {
    throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.3 or newer was not negotiated", false);
  }
  return version;
}
```

`discoverTests`、`runTests`、`getTestCatalog`、`getTestRun`、`listTestRuns` 不再 hardcode `"1.3"`；按 gate 返回值发送 request，并选择 v1.3/v1.4 validator/decoder。`decodeTaskResponse`、task page、artifact page 与 `startCMakeBuild` 增加 v1.4 branch。`inspectWorkspace`/`listCMakeTargets` 继续返回 v1.2 workspace projection，但用实际 negotiated v1.4 envelope。

关键 public method signatures 与 v1.4 branch 必须为：

```ts
async getCapabilities(): Promise<Capabilities | CapabilitiesV11 | CapabilitiesV12 | CapabilitiesV13 | CapabilitiesV14> {
  const version = this.#requireAuthentication();
  const payload = await this.#connection.request(version, "capabilities/get", {});
  if (version === "1.4") {
    validatePayload("capabilities/get", validateCapabilitiesV14, payload);
    return {
      ...payload,
      frameworkAdapters: (payload.frameworkAdapters as Record<string, unknown>[]).map((adapter) => ({ ...adapter }))
    } as unknown as CapabilitiesV14;
  }
  if (version === "1.3") {
    validatePayload("capabilities/get", validateCapabilitiesV13, payload);
    return payload as unknown as CapabilitiesV13;
  }
  if (version === "1.2") {
    validatePayload("capabilities/get", validateCapabilitiesV12, payload);
    return payload as unknown as CapabilitiesV12;
  }
  if (version === "1.1") {
    validatePayload("capabilities/get", validateCapabilitiesV11, payload);
    return payload as unknown as CapabilitiesV11;
  }
  validatePayload("capabilities/get", validateCapabilitiesV10, payload);
  return payload as unknown as Capabilities;
}

async discoverTests(input: TestDiscoveryInput): Promise<TaskSnapshotV13 | TaskSnapshotV14> {
  const version = this.#requireV13();
  const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "testDiscovery" });
  return decodeTaskResponse("tasks/start", version, payload) as TaskSnapshotV13 | TaskSnapshotV14;
}

async runTests(input: TestRunInput): Promise<TaskSnapshotV13 | TaskSnapshotV14> {
  const version = this.#requireV13();
  const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "testRun" });
  return decodeTaskResponse("tasks/start", version, payload) as TaskSnapshotV13 | TaskSnapshotV14;
}

async getTestCatalog(input: CatalogGetInput): Promise<ProtocolTestCatalog> {
  const version = this.#requireV13();
  const payload = await this.#connection.request(version, "tests/catalog/get", { ...input });
  validatePayload("tests/catalog/get", version === "1.4" ? validateTestCatalogV14 : validateTestCatalogV13, payload);
  return version === "1.4" ? decodeTestCatalogV14(payload) : decodeTestCatalog(payload);
}

async getTestRun(runId: string): Promise<ProtocolTestRun> {
  const version = this.#requireV13();
  const payload = await this.#connection.request(version, "tests/runs/get", { runId });
  validatePayload("tests/runs/get", version === "1.4" ? validateTestRunV14 : validateTestRunV13, payload);
  return version === "1.4" ? decodeTestRunV14(payload) : decodeTestRun(payload);
}

async listTestRuns(input: TestRunListInput = {}): Promise<ProtocolTestRunPage> {
  const version = this.#requireV13();
  const payload = await this.#connection.request(version, "tests/runs/list", { ...input });
  validatePayload("tests/runs/list", version === "1.4" ? validateTestRunPageV14 : validateTestRunPageV13, payload);
  return version === "1.4" ? decodeTestRunPageV14(payload) : decodeTestRunPage(payload);
}
```

`decodeTaskResponse`、`listTasks`、`listArtifacts` 的 branch order 必须为 `1.4` → `1.3` → `1.2` → `1.1`，每个版本选择自己的 validator 和 decoder；不能合并为未经验证的 fallback。`startCMakeBuild` 同样先处理 `1.4`，然后保留现有 `1.3`/`1.2` 分支。

- [ ] **Step 6: 运行 v1.4 compatibility 与旧版本 full Client tests**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client build
git diff --check
```

Expected: PASS；原 v1.3 Test API、v1.2 CMake、v1.1 Task/Artifact、v1.0 fallback tests 不修改预期语义。

- [ ] **Step 7: 提交 Task 3**

```powershell
git add -- packages/test-client/src/client.ts packages/test-client/src/decoders.ts packages/test-client/src/envelopes.ts packages/test-client/src/index.ts packages/test-client/src/client.test.ts
git commit -m "feat: project protocol v1.4 client models"
```

---

### Task 4: Public Coverage APIs、Local Validation 与 Phase 5A Client Gate

**Files:**

- Modify: `packages/test-client/src/client.ts:40-100,120-260,417-458,724-770`
- Modify: `packages/test-client/src/index.ts:1-47`
- Test: `packages/test-client/src/client.test.ts:1-665`
- Modify after green: `docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md:397-465`

**Interfaces:**

- Consumes: Task 1 v1.4 request methods；Task 2 Coverage decoders；Task 3 v1.4 existing API projection。
- Produces: `CoverageRunInput`、`CoverageRunListInput`、`ProtocolClient.startCoverage`、`getCoverageRun`、`listCoverageRuns`、`getCoverageReport` 与 public exports。

- [ ] **Step 1: 写出四个 API happy-path RED test**

```ts
test("protocol 1.4 client routes strict coverage APIs", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.4", serviceVersion: "0.5.0" }, "1.4");
    }
    if (request.method === "coverage/runs/list") {
      return response(request, { items: [coverageRun()], nextCursor: "coverage-next" }, "1.4");
    }
    if (request.method === "coverage/reports/get") return response(request, coverageReport(), "1.4");
    return response(request, coverageRun(), "1.4");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.5.0");
  const started = await fixture.client.startCoverage({
    idempotencyKey: "d".repeat(32),
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    coverageProfileId: "coverage-debug",
    catalogRevision: CATALOG_REVISION,
    selection: { mode: "items", itemIds: [ITEM_ID] },
    repeatCount: 1,
    timeoutMs: 60_000
  });
  const got = await fixture.client.getCoverageRun(started.coverageRunId);
  const page = await fixture.client.listCoverageRuns({ projectId: "core", coverageProfileId: "coverage-debug", limit: 200 });
  const report = await fixture.client.getCoverageReport("b".repeat(32));
  assert.ok(got.createdAt instanceof Date);
  assert.equal(page.nextCursor, "coverage-next");
  assert.equal(report.artifactId, ARTIFACT_ID);
  assert.deepEqual(fixture.requests.slice(1).map(({ method }) => method), [
    "coverage/runs/start", "coverage/runs/get", "coverage/runs/list", "coverage/reports/get"
  ]);
});
```

- [ ] **Step 2: 写出 old Session、injection 与 invalid response RED tests**

逐个覆盖四个 API：

```ts
test("protocol 1.3 sessions reject every coverage API without writing", async () => {
  const fixture = scriptedClient((request) => response(request,
    { negotiatedProtocolVersion: "1.3", serviceVersion: "0.4.0" }, "1.3"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.5.0");
  const calls = [
    () => fixture.client.startCoverage(validCoverageInput()),
    () => fixture.client.getCoverageRun("a".repeat(32)),
    () => fixture.client.listCoverageRuns(),
    () => fixture.client.getCoverageReport("b".repeat(32))
  ];
  for (const call of calls) {
    await assert.rejects(call, (failure: unknown) =>
      failure instanceof ProtocolError && failure.code === "PROTOCOL_FEATURE_UNAVAILABLE");
  }
  assert.equal(fixture.requests.length, 1);
});
```

Local invalid table 必须包括 extra `command`、`environment`、bad IDs、selection injection、repeat 0/101、timeout 0/86400001、list limit 0/201、bad cursor，且每例保持 wire count。Inbound table 必须包括 unknown status/outcome/reason/completeness、unsafe sequence/summary、invalid Date、report lifecycle mismatch；每例必须拒绝而不返回 partial object。

- [ ] **Step 3: 定义 public input types 与 exact v1.4 validators**

```ts
export type CoverageRunInput = CoverageRunStartRequest;
export interface CoverageRunListInput {
  projectId?: string;
  coverageProfileId?: string;
  cursor?: string;
  limit?: number;
}

const validateCoverageRunStartV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRunStartRequest"
});
const validateCoverageRunV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRun"
});
const validateCoverageRunPageV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRunPage"
});
const validateCoverageReportV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageReport"
});
const validateCoverageRunIdPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageRunIdPayload"
) as ValidateFunction;
const validateCoverageReportIdPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageReportIdPayload"
) as ValidateFunction;
const validateCoverageRunsListPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageRunsListPayload"
) as ValidateFunction;

function validateRequestPayload(method: string, validator: ValidateFunction, payload: unknown): void {
  if (!validator(payload)) {
    throw new Error(`invalid protocol request for ${method}: ${payloadAjv.errorsText(validator.errors)}`);
  }
}
```

这些 validators 直接引用 Protocol v1.4 coverage/message schema defs；不能复制 pattern/range 形成第二套规则。`validateRequestPayload` 产生 `invalid protocol request` error，不能误用 response error 文案。

- [ ] **Step 4: 实现 v1.4 gate 与四个 methods**

```ts
#requireV14(): "1.4" {
  const version = this.#requireAuthentication();
  if (version !== "1.4") {
    throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.4 was not negotiated", false);
  }
  return version;
}

async startCoverage(input: CoverageRunInput): Promise<CoverageRun> {
  const version = this.#requireV14();
  validateRequestPayload("coverage/runs/start", validateCoverageRunStartV14, input);
  const payload = await this.#connection.request(version, "coverage/runs/start", { ...input });
  validatePayload("coverage/runs/start", validateCoverageRunV14, payload);
  return decodeCoverageRun(payload);
}

async getCoverageRun(coverageRunId: string): Promise<CoverageRun> {
  const version = this.#requireV14();
  const request = { coverageRunId };
  validateRequestPayload("coverage/runs/get", validateCoverageRunIdPayloadV14, request);
  const payload = await this.#connection.request(version, "coverage/runs/get", request);
  validatePayload("coverage/runs/get", validateCoverageRunV14, payload);
  return decodeCoverageRun(payload);
}

async listCoverageRuns(input: CoverageRunListInput = {}): Promise<CoverageRunPage> {
  const version = this.#requireV14();
  validateRequestPayload("coverage/runs/list", validateCoverageRunsListPayloadV14, input);
  const payload = await this.#connection.request(version, "coverage/runs/list", { ...input });
  validatePayload("coverage/runs/list", validateCoverageRunPageV14, payload);
  return decodeCoverageRunPage(payload);
}

async getCoverageReport(reportId: string): Promise<CoverageReport> {
  const version = this.#requireV14();
  const request = { reportId };
  validateRequestPayload("coverage/reports/get", validateCoverageReportIdPayloadV14, request);
  const payload = await this.#connection.request(version, "coverage/reports/get", request);
  validatePayload("coverage/reports/get", validateCoverageReportV14, payload);
  return decodeCoverageReport(payload);
}
```

后三个 methods 必须重复完整 validation/request/decode 代码，不使用接受任意 method/ref 的宽松 Coverage helper。

- [ ] **Step 5: 导出完整 public surface 并验证 TypeScript declarations**

`index.ts` 导出 `CoverageRunInput`、`CoverageRunListInput`，以及 generated `CoverageRun`、`CoverageRunPage`、`CoverageReport` 和 v1.4 union types。happy-path test 使用显式返回类型形成 compile-time assertions：

```ts
const start: Parameters<ProtocolClient["startCoverage"]>[0] = validCoverageInput();
const started: CoverageRun = await fixture.client.startCoverage(start);
const page: CoverageRunPage = await fixture.client.listCoverageRuns();
const report: CoverageReport = await fixture.client.getCoverageReport("b".repeat(32));
assert.equal(started.coverageRunId, COVERAGE_RUN_ID);
assert.equal(page.items[0]?.coverageRunId, COVERAGE_RUN_ID);
assert.equal(report.reportId, REPORT_ID);
```

不得从 test-client 重导出 `@unit-test-ide/coverage-models` decoder，保持 wire metadata 与大型 artifact decoder 分离。

- [ ] **Step 6: 运行 Task 4 full Client tests**

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client build
git diff --check
```

Expected: PASS；四个 API、全部 adversarial cases、v1.4 existing surface 和 v1.0–v1.3 regressions 绿色。

- [x] **Step 7: 运行 Phase 5A contract/drift gates**

```powershell
$go = (Resolve-Path '.superpowers\runtime\go1.26.5-windows-amd64\go\bin').Path
$node = (Resolve-Path '.superpowers\runtime\node-v24.18.0-win-x64').Path
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:GOCACHE = Join-Path (Get-Location) '.superpowers\cache\go-build'
$env:Path = $go + ';' + $node + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:coverage-generated
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:protocol-generated
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm build
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm --filter @unit-test-ide/test-client test
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm test
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm verify
git diff --check
```

保存 root `pnpm test`/`pnpm verify` 的完整结果。已知 repository baseline 是 Linux build/CppUTest fixture 与 Windows CMake fixture/CppUTest parser/Unity golden failures，Windows VSWhere probe 还存在历史间歇失败；本 Task 必须证明 test-client、coverage/protocol generated 和本次变更 package 没有新增 failure。不得把既有 baseline 伪报为本 Task 绿色，也不得为通过本 Task 顺带修改这些无关模块。

2026-08-05 controller evidence：Client 86/86、Client build、Coverage/Protocol drift、root build、Go full、Go full race、Phase 5A `coveragedomain + taskstore` race、service-probe E2E 20/20 与 `git diff --check` 均通过。首次并行跑 Go full/race 时，`internal/probe` 的 200 ms timeout control 因资源竞争返回 `context deadline exceeded`；随后顺序运行 `internal/probe` race 和全量 race 均通过。root `pnpm test` 与 `pnpm verify` 只复现已记录的 `spawnSync cmake ENOENT`，`verify` 因此短路的 race/E2E 已独立运行并通过。

- [x] **Step 8: 更新父计划的 Task 6 状态并提交**

只有 Steps 1–7 全部有证据后，勾选 `2026-08-03-phase5-coverage-contract-domain-plan.md` 的 Task 6 Steps 1–5 和 `TypeScript Client v1.4` completion item。若 root full gate 仍有已知 baseline failure，则保留“完整 Phase 5A 门禁”未勾选，并在 Task 6 下记录 run/测试证据与 baseline disposition。

```powershell
git add -- packages/test-client docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md
git commit -m "feat: expose coverage client contracts"
```

---

## Final Review and Handoff

- [x] 每个 Task 创建只含该 Task base..head 的 review package，并完成 spec compliance 与 code quality review。
- [x] Whole-plan reviewer 检查 v1.4 version equality、legacy fallback、old-session zero-write、execution-plan injection、Date/safe integer、nested clone 与 old API projection。
- [x] Security boundary scan 确认 test-client 没有新增 executable、raw args、environment、working directory、Shell、HTTP/TLS/OAuth/GitHub dependency。
- [x] 使用 pinned Node/Go runtime 重跑 Client test/build、Coverage/Protocol drift、root applicable gates 与 `git diff --check`。
- [x] 确认 tracked worktree clean，删除仅属于本计划的 worktree-local cache/review workspace。
- [ ] 推送 `codex/workspace-cmake-toolchains` 到 GitHub `github` 与 Gitee `origin`，并验证两个 remote ref 等于 local HEAD。
- [ ] 等待 GitHub Actions，比较 exact failed tests 与已记录 baseline；任何本 Task 新 failure 必须修复后重新双推。
