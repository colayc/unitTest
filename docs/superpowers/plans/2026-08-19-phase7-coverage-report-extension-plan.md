# Phase 7 Coverage Report Extension Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Code-OSS Testing API vertical slice 上增加受信任工作区的 `Run with Coverage`、CoverageRun 状态查询、报告摘要展示和受校验 HTML artifact 打开闭环。

**Architecture:** Extension 只通过既有 `ProtocolClient` 的 Protocol v1.4 coverage/artifact API 工作，不接触 Go Service 文件系统路径、executable、argv 或 environment。新的 `CoverageController` 负责 trust/session/revision gate、run 状态和脱敏错误；VS Code command/webview adapter 只消费已验证的 report metadata 与受限 artifact bytes。Coverage source decoration、历史比较和 threshold 配置作为后续独立切片，不在本计划中扩展。

**Tech Stack:** TypeScript, Node.js 24.18.0, pnpm 11.4.0, Code-OSS Extension API seam, Protocol v1.4, existing `@unit-test-ide/test-client`, `@unit-test-ide/protocol-models`.

**Spec:** `docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md` Sections 6, 12, 13, 14, 15, 17, 18; `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md` Phase 7.

## Global Constraints

- Extension 只有 `TrustState === "trusted"`、单根 workspace、Service session `running` 且 protocol version `1.4` 时才能启动 coverage。
- Coverage request 只能传 `idempotencyKey`、workspace/project/profile/catalog identity、structured selection、repeat 和 timeout；不得添加 executable、argv、environment、cwd、driver、collector 或 report format。
- Report metadata 只显示 schema version、completeness、summary、tool provenance 和 artifact IDs；不得暴露 native path、token、environment 或 raw command。
- Artifact 读取必须复用 `ProtocolClient.readArtifact()` 的 64 MiB 上限、chunk digest 校验和 protocol authorization；不得按路径读取 artifact。
- stale `catalogRevision`、workspace trust loss、session replacement、protocol downgrade 或 report digest/size 校验失败都必须 fail-closed。
- 每个任务先写 RED 测试，再写最小实现；每个任务独立提交并运行 focused、full package、race/CI 适配门禁。

---

### Task 1: Extension Protocol Coverage Facade

**Files:**
- Modify: `apps/code-oss-extension/src/protocol-client.ts`
- Test: `apps/code-oss-extension/test/protocol-client-coverage.test.ts`

**Interfaces:**
- Consumes: `ProtocolClient.startCoverage`, `getCoverageRun`, `listCoverageRuns`, `getCoverageReport`, `listArtifacts`, `readArtifact`.
- Produces: `ExtensionProtocolClient.startCoverage`, `.getCoverageRun`, `.listCoverageRuns`, `.getCoverageReport`, `.listArtifacts`, `.readArtifact` with the same typed inputs/outputs and no path-like parameters.

- [ ] **Step 1: Write the failing test**

```ts
test("extension protocol facade forwards only typed coverage and artifact calls", async () => {
  const calls: string[] = [];
  const fake = {
    startCoverage: async () => { calls.push("startCoverage"); return coverageRunFixture(); },
    getCoverageRun: async () => { calls.push("getCoverageRun"); return coverageRunFixture(); },
    listCoverageRuns: async () => { calls.push("listCoverageRuns"); return { items: [] }; },
    getCoverageReport: async () => { calls.push("getCoverageReport"); return coverageReportFixture(); },
    listArtifacts: async () => { calls.push("listArtifacts"); return { items: [] }; },
    readArtifact: async () => { calls.push("readArtifact"); return new Uint8Array([60, 104, 116, 109, 108, 62]); }
  };
  const facade = asExtensionProtocolClient(fake);
  await facade.startCoverage(validCoverageInput());
  await facade.getCoverageRun("0123456789abcdef0123456789abcdef");
  await facade.listCoverageRuns({});
  await facade.getCoverageReport("0123456789abcdef0123456789abcdef");
  await facade.listArtifacts("0123456789abcdef0123456789abcdef");
  await facade.readArtifact("0123456789abcdef0123456789abcdef");
  assert.deepEqual(calls, ["startCoverage", "getCoverageRun", "listCoverageRuns", "getCoverageReport", "listArtifacts", "readArtifact"]);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test apps/code-oss-extension/dist/test/protocol-client-coverage.test.js`

Expected: FAIL because `ExtensionProtocolClient` has no coverage or artifact facade methods.

- [ ] **Step 3: Write minimal implementation**

Add type imports and exact methods to `ExtensionProtocolClient`; `createProtocolClient()` continues returning the existing `ProtocolClient`, so the facade adds no new transport or filesystem behavior:

```ts
startCoverage(input: CoverageRunInput): Promise<CoverageRun>;
getCoverageRun(runId: string): Promise<CoverageRun>;
listCoverageRuns(input?: CoverageRunListInput): Promise<CoverageRunPage>;
getCoverageReport(reportId: string): Promise<CoverageReport>;
listArtifacts(taskId: string, input?: PageInput): Promise<ArtifactPage>;
readArtifact(artifactId: string): Promise<Uint8Array>;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter code-oss-extension build` and `node --test apps/code-oss-extension/dist/test/protocol-client-coverage.test.js`

Expected: PASS with no request containing a native path or raw process control field.

- [ ] **Step 5: Commit**

```bash
git add apps/code-oss-extension/src/protocol-client.ts apps/code-oss-extension/test/protocol-client-coverage.test.ts
git commit -m "feat: expose typed coverage protocol facade"
```

### Task 2: Coverage Run Controller and Trust/Revision Gate

**Files:**
- Create: `apps/code-oss-extension/src/coverage-controller.ts`
- Test: `apps/code-oss-extension/test/coverage-controller.test.ts`

**Interfaces:**
- Consumes: `ExtensionProtocolClient`, `CommandStatus`, the current Testing API catalog state, and `WorkspaceSnapshot` identity.
- Produces: `CoverageController.start(input)`, `.refresh(runId)`, `.getState()`, `.dispose()`; state is `idle | starting | running | available | unavailable | stopped` and contains only IDs, summary, completeness and redacted detail.

- [ ] **Step 1: Write the failing test**

```ts
test("coverage start rejects untrusted, stale and non-v1.4 sessions before RPC", async () => {
  const calls: string[] = [];
  const controller = createCoverageController({
    status: trustedStatus(),
    session: runningCoverageClient(() => { calls.push("rpc"); }),
    catalog: catalogFixture("catalog-1"),
    workspace: workspaceFixture("workspace-1")
  });
  await assert.rejects(() => controller.start({ catalogRevision: "catalog-old" }), /current catalog/);
  controller.setTrustState("blocked-untrusted");
  await assert.rejects(() => controller.start({ catalogRevision: "catalog-1" }), /trusted workspace/);
  assert.deepEqual(calls, []);
});

test("coverage report state is published only after matching run and report identities", async () => {
  const controller = createCoverageController(availableFixture());
  await controller.start({ catalogRevision: "catalog-1" });
  assert.equal(controller.getState().state, "available");
  assert.equal(controller.getState().reportId, "report-1");
  assert.equal(controller.getState().summary.lines.total, 10);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test apps/code-oss-extension/dist/test/coverage-controller.test.js`

Expected: FAIL because `coverage-controller.ts` does not exist.

- [ ] **Step 3: Write minimal implementation**

Implement `createCoverageController(options)` with these ordered checks:

1. `status.refreshTrust() === "trusted"`, `session` is unchanged and protocol version is `1.4`.
2. Workspace generation, project ID, coverage profile ID and catalog revision match the captured catalog snapshot.
3. Build a `CoverageRunStartRequest` containing only the schema-approved fields and a fresh idempotency key.
4. Await `startCoverage`, then `getCoverageRun`, then `getCoverageReport`; after each await re-check trust, session identity and catalog revision.
5. Require report `coverageRunId` and `testRunId` to match the run, and copy only summary/completeness/tool provenance/artifact IDs into state.
6. On any failure, redact the error and transition to `unavailable` without retaining request payloads, endpoint strings or raw artifact bytes.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter code-oss-extension build`; `node --test apps/code-oss-extension/dist/test/coverage-controller.test.js`; `node --test --test-name-pattern='coverage' apps/code-oss-extension/dist/test/*.test.js`

Expected: all coverage-controller and existing trust/lifecycle tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/code-oss-extension/src/coverage-controller.ts apps/code-oss-extension/test/coverage-controller.test.ts
git commit -m "feat: gate coverage runs on trusted catalog state"
```

### Task 3: Coverage Commands and Safe Report Artifact Viewer

**Files:**
- Modify: `apps/code-oss-extension/src/commands.ts`
- Modify: `apps/code-oss-extension/src/extension.ts`
- Create: `apps/code-oss-extension/src/coverage-viewer.ts`
- Test: `apps/code-oss-extension/test/coverage-commands.test.ts`
- Test: `apps/code-oss-extension/test/coverage-viewer.test.ts`

**Interfaces:**
- Consumes: `CoverageController`, `ExtensionProtocolClient.readArtifact`, `CommandHost`, and the existing output channel.
- Produces: commands `unitTestIde.runCoverage`, `unitTestIde.refreshCoverage`, and `unitTestIde.openCoverageReport`; viewer accepts only a verified `coverage-html` artifact ID and `Uint8Array` bytes.

- [ ] **Step 1: Write the failing tests**

```ts
test("coverage commands are fail-closed while service is stopping or trust is lost", async () => {
  const registered = registerCoverageCommands(blockedCommandFixture());
  await registered.invoke("unitTestIde.runCoverage");
  assert.deepEqual(registered.controllerCalls, []);
  assert.equal(registered.errors.length, 1);
});

test("viewer rejects non-HTML, oversized, non-UTF8 and unsafe artifact content", async () => {
  await assert.rejects(() => renderCoverageHtml({ kind: "coverage-json", bytes: utf8("{}") }), /HTML artifact/);
  await assert.rejects(() => renderCoverageHtml({ kind: "coverage-html", bytes: new Uint8Array(64 * 1024 * 1024 + 1) }), /size/);
  await assert.rejects(() => renderCoverageHtml({ kind: "coverage-html", bytes: new Uint8Array([0xff]) }), /UTF-8/);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test apps/code-oss-extension/dist/test/coverage-commands.test.js apps/code-oss-extension/dist/test/coverage-viewer.test.js`

Expected: FAIL because the commands and viewer do not exist.

- [ ] **Step 3: Write minimal implementation**

Register the three commands through the existing command host. `runCoverage` delegates to the controller only after the existing live trust/status check. `refreshCoverage` re-fetches the current run and report. `openCoverageReport` lists artifacts, requires exactly one `coverage-html` artifact whose metadata size is within the client limit, reads it through `readArtifact`, validates UTF-8 and the artifact SHA-256 already checked by the client, then opens a Code-OSS `WebviewPanel` with a nonce CSP and no remote resource policy. Never construct a local filename from an artifact ID and never interpolate unescaped report bytes into a command or shell.

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm --filter code-oss-extension build`; `node --test apps/code-oss-extension/dist/test/coverage-commands.test.js apps/code-oss-extension/dist/test/coverage-viewer.test.js`; `node --test apps/code-oss-extension/dist/test/*.test.js`

Expected: all extension tests PASS, including trust-loss/deactivation and existing Testing API suites.

- [ ] **Step 5: Commit**

```bash
git add apps/code-oss-extension/src/commands.ts apps/code-oss-extension/src/extension.ts apps/code-oss-extension/src/coverage-viewer.ts apps/code-oss-extension/test/coverage-commands.test.ts apps/code-oss-extension/test/coverage-viewer.test.ts
git commit -m "feat: add coverage run and report commands"
```

### Task 4: Real Service Smoke, Documentation, and CI Contract

**Files:**
- Modify: `apps/code-oss-extension/test/service-smoke.test.ts`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Consumes: Tasks 1–3 and the existing real Service fixture builder.
- Produces: Windows Named Pipe and Linux Unix Socket smoke coverage for start/report/read/open, plus honest `SKIP` when Code-OSS executable is absent.

- [ ] **Step 1: Write the failing smoke assertions**

Add a real-service case that starts a trusted workspace, executes `unitTestIde.runCoverage`, waits for `coverage.report.available`, reads the `coverage-html` artifact through the protocol, and asserts the report state contains no native path/token/environment. Add an untrusted case asserting zero Service process, token, endpoint, data directory and coverage RPC calls.

- [ ] **Step 2: Run the focused smoke to verify the new case fails**

Run: `pnpm --filter code-oss-extension build`; `pnpm --filter code-oss-extension test:service-smoke`

Expected: the new coverage scenario fails before implementation or reports `PROTOCOL_FEATURE_UNAVAILABLE` until the fixture is built with Protocol v1.4 coverage support.

- [ ] **Step 3: Add the implementation wiring and CI commands**

Wire the controller and commands from `activate()` using the same Service session lifecycle as Testing API. Add the coverage smoke after existing service smoke in both `verify-windows` and `verify-linux`; do not convert missing `CODE_OSS_EXECUTABLE` from `SKIP` to `PASS`. Keep report upload paths under `.native-e2e/artifacts/<platform>/` and do not upload tokens or raw endpoint logs.

- [ ] **Step 4: Run all gates**

Run locally:

```powershell
pnpm --filter code-oss-extension build
pnpm --filter code-oss-extension test
pnpm --filter code-oss-extension test:service-smoke
pnpm build
pnpm test:go
git diff --check
```

Run on CI: `verify-windows` and `verify-linux` must each execute the real service coverage smoke; Linux Unix Socket evidence cannot be replaced by cross-compilation.

- [ ] **Step 5: Update documentation and commit**

Document command names, trusted-session requirements, report metadata limits, artifact viewer behavior and the explicit host `SKIP` rule in `docs/development.md`; update the roadmap only after both platform smoke jobs pass.

```bash
git add apps/code-oss-extension/test/service-smoke.test.ts .github/workflows/foundation.yml docs/development.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "test: verify coverage report extension flow"
```

## Final Verification

- `pnpm --filter code-oss-extension test` passes with all prior Testing API, lifecycle and trust regressions.
- `pnpm --filter code-oss-extension test:service-smoke` passes on Windows Named Pipe and Linux Unix Socket CI.
- `pnpm build`, `pnpm test:go`, `git diff --check`, and generated-protocol checks pass.
- A report artifact can be opened only through protocol-verified bytes; no extension code reads a native artifact path.
- Untrusted, multi-root, stale-catalog, trust-loss, protocol downgrade and malformed-artifact cases fail closed.
- The plan does not mark Phase 7 complete until the real platform smoke evidence exists; source decoration, history comparison and threshold configuration remain separate plans.
