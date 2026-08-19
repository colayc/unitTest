# Phase 7 Coverage Runtime Queue 接线实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 将 trusted Runtime 的 Protocol v1.4 coverage provider 接到现有 `coveragecoord` 和 `taskstore`，让生产 Service 可以真实创建、查询和分页 coverage run，但在执行 coordinator 尚未接入前绝不伪造完成报告。

**Architecture:** `Runtime` 负责从当前受信任 workspace snapshot 解析 project、base build profile、coverage profile 和 toolchain provenance；`coveragecoord` 负责把已解析的 immutable input 原子排队到 `taskstore`；`session` 只依赖现有 `session.CoverageBackend` 接口。读取路径继续由 `taskstore` 提供，执行路径留给后续 Coverage Execution Coordinator，不把 executable、argv、environment 或 cwd 暴露到 Protocol。

**Tech Stack:** Go 1.26.6、SQLite、现有 `coveragecoord`、`coveragedomain`、`taskstore`、Protocol v1.4。

**Spec:** `docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md` Sections 6, 12, 17, 18；`docs/superpowers/plans/2026-08-03-phase5-coverage-runtime-ci-plan.md` Tasks 1–3。

## Global Constraints

- 只有 `TrustedWorkspace` 且 Runtime 已成功建立 workspace/build/store 依赖时才暴露 coverage provider。
- Provider 只能使用 Runtime 解析出的 project/profile/toolchain identity；不能信任 Protocol 传入的 native path、executable、raw args、environment 或 cwd。
- `coverage/runs/start` 只创建 durable queued aggregate；不得返回 finished、summary、reportId 或 artifact 假数据。
- 请求 identity、selection、catalog revision 和 toolchain snapshot 必须在排队前通过既有 domain validators。
- 幂等 replay 必须返回既有 canonical task/run/testRun，不重复创建关系或事件。
- 每个任务先写 RED 测试，再写最小实现；每个任务独立提交，并推送 GitHub 与 Gitee。

---

### Task 1: Runtime Coverage Provider Resolver

**Files:**
- Create: `apps/test-service/internal/runtime/coverage_backend.go`
- Test: `apps/test-service/internal/runtime/coverage_backend_test.go`
- Modify: `apps/test-service/internal/runtime/runtime.go`

**Interfaces:**
- Consumes: `discovery.Snapshot`, `session.CoverageRunStart`, `coveragecoord.QueuedBackend`, `task.CoverageRepository`。
- Produces: `runtimeCoverageBackend` implementing `session.CoverageBackend`; `Runtime` constructs it only for trusted workspaces when no explicit test provider was injected。

- [x] **Step 1: Write the failing tests**

覆盖以下行为：

```go
func TestRuntimeCoverageBackendResolvesProfileAndQueuesCanonicalAggregate(t *testing.T) {
    // trusted snapshot contains project, base build profile, coverage profile and matching toolchain
    // StartCoverageRun must create one queued task/run/testRun and return no report metadata.
}

func TestRuntimeCoverageBackendRejectsUnknownOrStaleIdentityBeforeStoreMutation(t *testing.T) {
    // unknown project/profile/toolchain and catalog generation mismatch return an error;
    // recording store must observe zero CreateCoverageTask calls.
}

func TestRuntimeCoverageBackendNeverConstructsAProviderForUntrustedRuntime(t *testing.T) {
    // Runtime.Open with TrustedWorkspace=false keeps CoverageBackend nil.
}
```

- [x] **Step 2: Run tests to verify they fail**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/runtime -run 'CoverageBackend|CoverageProvider' -count=1
```

Expected: FAIL because production resolver/provider construction is not defined and Runtime only preserves an injected backend.

- [x] **Step 3: Write the minimal implementation**

Implement a runtime-owned provider with these exact checks:

1. Require trusted runtime, non-empty `WorkspaceGeneration`, valid project/profile/catalog identity and a current `coordinator.Inspect` snapshot.
2. Find the requested project, its base `cmake.BuildProfile`, the matching `workspace.CoverageProfile`, and the referenced `toolchain.Instance`; reject missing or mismatched identities.
3. Convert only verified toolchain metadata to `coveragedomain.ToolchainSnapshot` (`Platform`, `Architecture`, compiler family/version, driver and collector names/versions); never copy executable paths or environment into the domain snapshot.
4. Call `coveragecoord.NewCoordinator`/`NewQueuedBackend` and persist through the existing `CoverageTaskStore`.
5. Delegate `GetCoverageRun`, `ListCoverageRuns`, and `GetCoverageReport` to the canonical repository.
6. In `runtime.Open`, preserve an explicitly injected `Config.CoverageBackend` for tests; otherwise construct the provider only when `TrustedWorkspace` is true and all required runtime dependencies exist.

- [x] **Step 4: Run focused and package tests**

Run:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/internal/runtime ./apps/test-service/internal/coveragecoord ./apps/test-service/internal/session -count=1
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/coveragecoord -count=1
```

Expected: PASS; untrusted runtime still has no coverage capability and existing injected test backends remain unchanged.

- [x] **Step 5: Commit**

```powershell
git add apps/test-service/internal/runtime/coverage_backend.go apps/test-service/internal/runtime/coverage_backend_test.go apps/test-service/internal/runtime/runtime.go
git commit -m "feat: queue production coverage runs from runtime"
git push github master
git push origin master
```

### Task 2: Production Route Smoke and Explicit Pending Semantics

**Files:**
- Modify: `apps/test-service/internal/server/server_test.go`
- Modify: `apps/test-service/internal/session/coverage_routes_test.go`
- Modify: `apps/code-oss-extension/test/service-smoke.test.ts`
- Modify: `docs/development.md`

**Interfaces:**
- Consumes: Task 1 production provider and existing Protocol v1.4 facade。
- Produces: real route evidence for queued start/get/list and a documented non-terminal response until execution coordinator is implemented。

- [x] **Step 1: Write the failing smoke assertions**

Add a trusted service case that starts coverage with the current catalog identity, asserts `status === "queued"`, then reads the same run via `getCoverageRun` and `listCoverageRuns`; assert no `reportId`, summary, artifact ID, native path, token or environment is present. Add an untrusted case asserting coverage start is rejected without process/token/endpoint/data side effects.

- [x] **Step 2: Run the focused smoke to verify it fails**

Run:

```powershell
pnpm --filter code-oss-extension build
pnpm --filter code-oss-extension test:service-smoke
```

Expected: the new route case fails because production Runtime currently does not expose a provider or the fixture has no current coverage profile.

- [x] **Step 3: Implement route fixture and pending assertions**

Use only the existing deterministic workspace fixture and Protocol Client. Do not add fake report responses or mark the test as completed coverage. If the fixture cannot resolve a coverage profile, assert the stable unavailable error and keep the runtime smoke honest.

- [x] **Step 4: Run all applicable gates**

Run:

```powershell
pnpm --filter code-oss-extension test
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; go test ./apps/test-service/... -count=1
git diff --check
```

- [x] **Step 5: Commit and push**

```powershell
git add apps/test-service/internal/server/server_test.go apps/test-service/internal/session/coverage_routes_test.go apps/code-oss-extension/test/service-smoke.test.ts docs/development.md
git commit -m "test: verify production coverage queue routes"
git push github master
git push origin master
```

## Deferred Boundary

The following are deliberately not implemented by this plan: coverage process execution, clang-cl/GCC/Clang collection, gcovr/llvm-cov invocation, report normalization, HTML/JSON/JUnit publication, terminal task transaction, and Windows/Linux native coverage completion smoke. Those belong to the next Coverage Execution Coordinator plan and must not be faked by Task 1 or Task 2.

## Final Verification

- Runtime and session package tests pass, including `-race` for the new provider.
- Trusted production route creates exactly one durable queued aggregate and idempotent replay does not duplicate it.
- Untrusted runtime exposes no coverage provider and performs no external coverage operation.
- GitHub and Gitee `master` point to the same final commit.
