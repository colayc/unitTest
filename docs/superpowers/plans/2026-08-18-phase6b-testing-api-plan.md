# Phase 6B Code-OSS Testing API 集成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有 Code-OSS Extension Vertical Slice 接入 Testing API，完成 trusted workspace 的测试发现、Test Item tree、Run Profile、结果事件和跨平台 CI smoke。

**Architecture:** 新增独立 `TestingApiAdapter`，通过受控 `clientProvider()` 使用现有 Protocol Client；`ExtensionController` 继续拥有 Workspace Trust、Service lifecycle 和 deactivate。Adapter 以 catalog revision 和 event sequence cursor 管理树与运行状态，Go Service 和 Protocol Schema 不变。

**Tech Stack:** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、`@types/vscode` 1.105.0、现有 `@unit-test-ide/test-client`、Go Service Windows Named Pipe/Linux Unix Socket smoke。

## Global Constraints

- 只有 trusted 单根 workspace 才允许 discovery、catalog refresh 和 test run；untrusted/multi-root/no-session 必须零外部协议调用。
- 不修改 Go Service、Protocol Schema 或通用 Protocol Client 的 wire contract；仅扩展现有 TypeScript adapter-facing 类型。
- Test Item ID 必须直接稳定映射 catalog 的 `containerId/itemId`；旧 `catalogRevision` 不得启动运行。
- 所有 token、endpoint、executable path、session directory 和原始 Service error 继续经过现有脱敏边界。
- Windows 和 Linux 共用同一 TypeScript adapter；IPC 平台差异只留在 ServiceManager/CI。
- package manifest 使用合法 Code-OSS `name: code-oss-extension` 与 `publisher: unit-test-ide`。
- 测试和构建使用仓库锁定的 Node 24.18.0、pnpm 11.4.0；Linux runtime 证据只能由 Linux CI 提供。

---

### Task 1: 扩展 Extension Protocol facade 与 Testing API host contract

**Files:**
- Modify: `apps/code-oss-extension/src/protocol-client.ts`
- Create: `apps/code-oss-extension/src/testing-api.ts`
- Create: `apps/code-oss-extension/test/testing-api.test.ts`
- Modify: `apps/code-oss-extension/test/extension.test.ts`

**Interfaces:**
- Consumes: existing `ExtensionProtocolClient.inspectWorkspace()` and `ServiceManager` client provider.
- Produces: `ExtensionProtocolClient` methods `discoverTests(input)`, `getTestCatalog(input)`, `runTests(input)`, `getTestRun(runId)`, `subscribeEvents(afterSequence)` and `close()`; `TestingApiHost` abstraction for fakeable Code-OSS Testing API objects.

- [ ] **Step 1: Write the failing facade and host-contract tests**

Add tests that compile a fake protocol client with the five test methods and assert `TestingApiAdapter` can be constructed without importing the runtime `vscode` module. Assert a host can create a controller, read the trusted workspace snapshot, expose the current client, and receive a redacted error.

- [ ] **Step 2: Run the focused tests to verify RED**

Run:

```powershell
$pnpm = '.superpowers/runtime/node-v24.18.0-win-x64/pnpm.CMD'
& $pnpm --filter code-oss-extension build
node --test apps/code-oss-extension/dist/test/testing-api.test.js
```

Expected: FAIL because the facade methods and `TestingApiAdapter` do not exist.

- [ ] **Step 3: Implement the minimal facade and host contract**

Extend `ExtensionProtocolClient` with typed delegates to the existing `ProtocolClient` methods. Define small structural interfaces in `testing-api.ts` for controller, item, refresh handler, run profile, run and event subscription so unit tests do not instantiate Code-OSS. The adapter constructor must receive `TestingApiHost`, `clientProvider`, and a `readTrust` callback; it must not import `ServiceManager`.

- [ ] **Step 4: Run focused tests to verify GREEN**

Run the same build and direct Node test command. Expected: PASS with no raw `vscode` runtime import.

- [ ] **Step 5: Commit**

```powershell
git add -- apps/code-oss-extension/src/protocol-client.ts apps/code-oss-extension/src/testing-api.ts apps/code-oss-extension/test/testing-api.test.ts apps/code-oss-extension/test/extension.test.ts
git commit -m "feat: add Testing API adapter contracts"
```

### Task 2: Implement catalog refresh and stable Test Item tree

**Files:**
- Modify: `apps/code-oss-extension/src/testing-api.ts`
- Modify: `apps/code-oss-extension/test/testing-api.test.ts`

**Interfaces:**
- Consumes: Task 1 `TestingApiHost`, `ExtensionProtocolClient`, `readTrust` and current workspace snapshot.
- Produces: `TestingApiAdapter.refresh(): Promise<void>`, catalog state `{ projectId, profileId, revision }`, and stable Test Item mapping from `containerId/itemId`.

- [ ] **Step 1: Write failing catalog tests**

Cover: deterministic first project/profile selection, `workspace/inspect` before discovery, `discoverTests` idempotency key, catalog published event filtering, finite catalog polling fallback, nested parent items, disabled items, source locations, diagnostics, same-revision no-op, and untrusted/multi-root/no-session zero-call behavior.

- [ ] **Step 2: Run RED**

```powershell
node --test apps/code-oss-extension/dist/test/testing-api.test.js --test-name-pattern "catalog|refresh|tree|untrusted|multi-root"
```

Expected: FAIL because refresh and catalog mapping are not implemented.

- [ ] **Step 3: Implement refresh and tree reconciliation**

Implement this exact flow:

```ts
await assertTrustedClient();
const workspace = await client.inspectWorkspace();
const project = [...workspace.projects].sort((a, b) => a.projectId.localeCompare(b.projectId))[0];
const profile = [...(project?.buildProfiles ?? [])]
  .sort((a, b) => a.buildProfileId.localeCompare(b.buildProfileId))[0];
await client.discoverTests({ idempotencyKey, projectId: project.projectId, profileId: profile.buildProfileId });
const catalog = await awaitCatalogOrPoll(project.projectId, profile.buildProfileId);
reconcileTree(catalog);
```

Use a bounded backoff sequence `[50, 100, 200, 400, 800, 1600, 3200]` milliseconds, then fail with a redacted diagnostic. Keep the last event cursor and reject catalog responses with an unexpected project/profile. Reuse existing Test Items when the ID and revision are unchanged; remove stale children atomically when revision changes.

- [ ] **Step 4: Run GREEN and regression tests**

Run the focused catalog pattern, then the full extension package test. Expected: all catalog tests and the existing 63-test baseline pass.

- [ ] **Step 5: Commit**

```powershell
git add -- apps/code-oss-extension/src/testing-api.ts apps/code-oss-extension/test/testing-api.test.ts
git commit -m "feat: map test catalogs into Testing API tree"
```

### Task 3: Add Run Profile, selections, event mapping, and final convergence

**Files:**
- Modify: `apps/code-oss-extension/src/testing-api.ts`
- Modify: `apps/code-oss-extension/test/testing-api.test.ts`

**Interfaces:**
- Consumes: Task 2 catalog state and stable Test Items.
- Produces: one default `RunProfile`, root/container/item selection conversion, `runTests` dispatch, event subscription lifecycle, and final `getTestRun` convergence.

- [ ] **Step 1: Write failing run/event tests**

Assert exact `runTests` inputs for root (`all`), container (`containers`) and item (`items`) runs, always with `repeatCount: 1`; reject stale revision and unknown IDs; map started, passed, failed, skipped and errored events including failure details; reconcile after event gap with `getTestRun`.

- [ ] **Step 2: Run RED**

```powershell
node --test apps/code-oss-extension/dist/test/testing-api.test.js --test-name-pattern "run|selection|event|revision|gap"
```

Expected: FAIL because no profile, event subscription or result mapping exists.

- [ ] **Step 3: Implement run and event lifecycle**

Create the profile with `runHandler` that validates live trust and revision before calling:

```ts
await client.runTests({
  idempotencyKey,
  projectId: state.projectId,
  profileId: state.profileId,
  catalogRevision: state.revision,
  selection,
  repeatCount: 1
});
```

Subscribe from the saved sequence cursor. For every matching run event, update the corresponding `TestRun`; on sequence gap, connection close, or run completion call `getTestRun(runId)` and apply the authoritative result. A stale or untrusted run must be ended as errored/cancelled without a new protocol call.

- [ ] **Step 4: Run GREEN and race tests**

Run the focused run/event pattern, the full extension package test, and `node --test --test-name-pattern "run|event|deactivate"` against the compiled suite. Expected: all pass, including no updates after adapter close.

- [ ] **Step 5: Commit**

```powershell
git add -- apps/code-oss-extension/src/testing-api.ts apps/code-oss-extension/test/testing-api.test.ts
git commit -m "feat: run tests through Code-OSS Testing API"
```

### Task 4: Wire adapter into activation, Trust, and deactivate

**Files:**
- Modify: `apps/code-oss-extension/src/extension.ts`
- Modify: `apps/code-oss-extension/src/commands.ts`
- Modify: `apps/code-oss-extension/test/extension.test.ts`
- Modify: `apps/code-oss-extension/test/testing-api.test.ts`

**Interfaces:**
- Consumes: Task 3 `TestingApiAdapter` constructor, refresh, run profile and close methods.
- Produces: activated Extension registering the controller/profile only in the active host, trust-gated refresh/run commands, and bounded deactivate cleanup.

- [ ] **Step 1: Write failing activation tests**

Assert activation registers exactly one controller/profile, trusted activation can refresh, untrusted activation registers no usable run path, trust loss clears the tree and blocks run, and deactivate closes subscription/controller exactly once.

- [ ] **Step 2: Run RED**

```powershell
node --test apps/code-oss-extension/dist/test/extension.test.js apps/code-oss-extension/dist/test/testing-api.test.js --test-name-pattern "Testing API|test tree|run profile|deactivate"
```

Expected: FAIL because activation does not create the adapter.

- [ ] **Step 3: Implement activation wiring**

Create the adapter after the ServiceManager/controller is available, pass a client provider that returns only the current authenticated session client, register `refresh` and the default run profile, and add the adapter to `context.subscriptions`. On Workspace Trust or folder changes, enqueue refresh/clear through the existing lifecycle queue. On deactivate, close the adapter before or together with Service stop, bounded by the existing stop timeout.

- [ ] **Step 4: Run GREEN and existing lifecycle regression tests**

Run the focused activation pattern and the full extension package test. Expected: no regression in the existing 63-test lifecycle suite and all new adapter tests pass.

- [ ] **Step 5: Commit**

```powershell
git add -- apps/code-oss-extension/src/extension.ts apps/code-oss-extension/src/commands.ts apps/code-oss-extension/test/extension.test.ts apps/code-oss-extension/test/testing-api.test.ts
git commit -m "feat: wire Testing API into extension lifecycle"
```

### Task 5: Add real smoke coverage, 10k tree benchmark, CI and docs

**Files:**
- Modify: `apps/code-oss-extension/test/service-smoke.test.ts`
- Create: `apps/code-oss-extension/test/testing-api-benchmark.test.ts`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Consumes: Task 4 activated adapter and existing real Service smoke fixture.
- Produces: Windows/Linux catalog refresh/run acceptance, explicit host SKIP behavior, reproducible 10k tree benchmark, and roadmap/development documentation.

- [ ] **Step 1: Write failing integration/benchmark tests**

Extend the fake/real smoke to assert `workspace/inspect → discoverTests → catalog → runTests → item result` and add a deterministic catalog generator with 10,000 items. Assert same revision does not replace existing Test Item objects.

- [ ] **Step 2: Run RED**

```powershell
$pnpm = '.superpowers/runtime/node-v24.18.0-win-x64/pnpm.CMD'
& $pnpm --filter code-oss-extension test:service-smoke
node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
```

Expected: service acceptance and benchmark fail until the adapter is wired and tree reconciliation exists.

- [ ] **Step 3: Implement smoke, benchmark and CI wiring**

Keep service smoke no-shell and redacted. Add Linux workflow steps that build the existing service/fixture and run `pnpm --filter code-oss-extension test:service-smoke`; retain the host harness as explicit SKIP when `CODE_OSS_EXECUTABLE` is absent. Record benchmark environment, item count, revision, elapsed time and replacement count without printing secrets or paths.

- [ ] **Step 4: Update Chinese docs and roadmap**

Document trusted refresh/run commands, catalog revision behavior, Windows/Linux CI evidence, host SKIP semantics and the remaining Coverage UI/source-decoration/desktop packaging scope. Mark Phase 6B complete only after both platform CI jobs run the real smoke.

- [ ] **Step 5: Run final gates**

```powershell
$pnpm = '.superpowers/runtime/node-v24.18.0-win-x64/pnpm.CMD'
& $pnpm --filter code-oss-extension test
& $pnpm --filter code-oss-extension test:service-smoke
& $pnpm --filter code-oss-extension test:host
& $pnpm build
& $pnpm test:go
git diff --check
```

Expected: all TypeScript/Go tests pass; host is PASS only with a real `CODE_OSS_EXECUTABLE`, otherwise explicit SKIP; Linux runtime evidence comes from Linux CI.

- [ ] **Step 6: Commit**

```powershell
git add -- apps/code-oss-extension .github/workflows/foundation.yml docs/development.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "feat: complete Phase 6B Testing API smoke"
```
