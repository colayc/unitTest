# Phase 9 F2 Trusted Framework Runtime Producer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a closed, atomic producer for the fixed Phase 9 F2 framework runtime inputs and wire real Windows and Linux hosted jobs to produce and aggregate the 136-scenario P4 matrix.

**Architecture:** Extract the runtime manifest into one shared closed contract, assemble deterministic platform workspaces in owned staging directories, and use the real Service discovery path to bind compiler, catalog, F1 provenance, stable IDs, and compiled executable bytes. Publish a complete platform runtime atomically, consume it in required native execution, and isolate the new fixed `windows-2022` framework job from the existing administrator/WFP path.

**Tech Stack:** TypeScript 6, Node.js 24, pnpm 11.4.0, Go Test Service, CMake, GitHub Actions, Node test runner.

**Spec:** `docs/superpowers/specs/2026-09-17-phase9-f2-trusted-runtime-producer-design.md`

## Global Constraints

- The producer accepts only `--platform linux|win32` and `--candidate` followed by a lowercase 40-character Git commit.
- Caller-selected output paths, workspace paths, Service binaries, compiler paths, commands, shells, hooks, executable overrides, and arbitrary environment maps are forbidden.
- Fixed output paths are `.native-e2e/framework-runtime/{windows|linux}.json` and published `.native-e2e/framework-work/{windows|linux}/{toolchain}/{cpputest|unity}/{service,workspace}`; unpublished owned staging is confined to `.native-e2e/framework-work/.staging/{invocation}/{platform}/{toolchain}/{framework}` on the same volume.
- Windows toolchains are exactly `msvc,clang-cl`; Linux toolchains are exactly `gcc,clang`.
- Every platform runtime contains two toolchains, two frameworks per toolchain, and the exact committed F1/F2 identity fields.
- Required execution remains fail-closed; no missing compiler, fixture, catalog, executable, scenario, report, or provenance field may be skipped.
- Runtime matrix execution does not invoke CMock, Ruby, Docker, or network downloads.
- `verify-framework-windows` uses fixed `windows-2022`; existing `verify-windows`, `verify-windows-wfp`, and `unit-test-wfp` behavior remains unchanged.
- Linux producer and native execution run inside the existing offline wrapper after network preparation.
- Actions are pinned by immutable SHA; framework jobs use no secrets or administrator privileges.
- Formal Windows signing, third-party license/legal approval, and roadmap cleanup remain DEFERRED; `releaseReady=false` remains unchanged.
- Do not push, create a pull request, publish a Release, or enable signing while executing this plan.

---

### Task 1: Extract the closed runtime manifest contract

**Files:**
- Create: `tools/service-probe/src/native-framework-runtime-contract.ts`
- Test: `tools/service-probe/src/native-framework-runtime-contract.test.ts`
- Modify: `tools/service-probe/src/native-framework-runtime.ts`
- Modify: `tools/service-probe/run-tests.mjs`

**Interfaces:**
- Produces: `FrameworkRuntimeManifest`, `FrameworkRuntimeToolchain`, `FrameworkRuntimeFramework`.
- Produces: `parseFrameworkRuntimeManifest(bytes: Uint8Array, expectedPlatform: FrameworkPlatform, expectedContractSha256: string): FrameworkRuntimeManifest`.
- Produces: `buildFrameworkRuntimeManifest(input: unknown): FrameworkRuntimeManifest`.
- Consumed by: Task 4 producer and the existing required runtime loader.

- [ ] **Step 1: Add closed-contract failing tests**

Add tests that construct a complete Windows manifest and assert canonical cloning, exact toolchain order, exact framework order, candidate/contract binding, and rejection of extra keys, path-bearing strings, wrong platform, duplicate toolchains, missing CMock evidence, invalid benchmark fields, and mutable input references.

```ts
test("runtime manifest contract is closed, ordered, and detached", () => {
  const input = runtimeManifestFixture("win32");
  const report = buildFrameworkRuntimeManifest(input);
  input.toolchains[0]!.frameworks[0]!.stableIdDigest = "0".repeat(64);
  assert.equal(report.toolchains[0]!.frameworks[0]!.stableIdDigest, "1".repeat(64));
  assert.deepEqual(report.toolchains.map(({ family }) => family), ["clang-cl", "msvc"]);
  assert.deepEqual(report.toolchains[0]!.frameworks.map(({ frameworkId }) => frameworkId), ["cpputest", "unity"]);
});

test("runtime manifest rejects path and provenance substitution", () => {
  const pathBearing = runtimeManifestFixture("linux");
  pathBearing.toolchains[0]!.compilerVersion = "/usr/bin/clang";
  assert.throws(() => buildFrameworkRuntimeManifest(pathBearing), /compiler identity/u);

  const missingCMock = runtimeManifestFixture("linux");
  delete missingCMock.toolchains[0]!.frameworks[1]!.cMockProvenance;
  assert.throws(() => buildFrameworkRuntimeManifest(missingCMock), /CMock provenance/u);
});
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/native-framework-runtime-contract.test.js
```

Expected: FAIL because `native-framework-runtime-contract.js` and its exports do not exist.

- [ ] **Step 3: Implement the shared contract**

Define closed readonly types and a canonical builder. Reuse `FrameworkPlatform`, `FrameworkToolchainFamily`, `FrameworkId`, `CMockProvenance`, and `FrameworkBenchmarkEvidence` from `native-framework-report.ts`. Validate every digest with lowercase SHA-256, every commit with lowercase SHA-1, version strings with the existing report version grammar, exact platform toolchain order, and exact framework order.

```ts
export function parseFrameworkRuntimeManifest(
  bytes: Uint8Array,
  expectedPlatform: FrameworkPlatform,
  expectedContractSha256: string,
): FrameworkRuntimeManifest {
  const parsed = parseStrictJson(bytes, "framework runtime manifest");
  const manifest = buildFrameworkRuntimeManifest(parsed);
  if (manifest.platform !== expectedPlatform) throw new Error("framework runtime platform is invalid");
  if (manifest.contractSha256 !== expectedContractSha256) throw new Error("framework runtime contract binding is invalid");
  return manifest;
}
```

- [ ] **Step 4: Make the runtime loader consume the shared parser**

Remove the duplicate key lists and loose casts from `native-framework-runtime.ts`. Read the contract bytes, calculate its SHA-256, call `parseFrameworkRuntimeManifest`, and map only validated records into `FrameworkPlatformOptions`.

- [ ] **Step 5: Add the compiled test and a closed focused-test map, then verify GREEN**

Add `dist/native-framework-runtime-contract.test.js` to the normal `tests` array. Replace the current one-off matrix selector with a closed source-or-dist-name map for the existing matrix test and the new contract test; retain the existing exact two-file native-build pair rule. Unknown names, duplicate names, mixed source/dist names, and extra arguments must still throw. Tasks 3–5 extend only this map and the normal array for their own test.

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-framework-runtime-contract.test.ts
pnpm --filter @unit-test-ide/service-probe test
git diff --check
```

Expected: focused contract tests pass; the complete service-probe suite passes with only the existing expected Windows WFP skip.

- [ ] **Step 6: Commit**

```powershell
git add tools/service-probe/src/native-framework-runtime-contract.ts tools/service-probe/src/native-framework-runtime-contract.test.ts tools/service-probe/src/native-framework-runtime.ts tools/service-probe/run-tests.mjs
git commit -m "refactor: close framework runtime manifest contract"
```

---

### Task 2: Expose the verified compiler binary digest in workspace discovery

**Files:**
- Modify: `packages/protocol-schema/schema/v1.2/workspace.schema.json`
- Modify: `packages/protocol-schema/fixtures/v1.2/workspace-inspect.valid.json`
- Modify: `packages/protocol-schema/test/schema.test.mjs`
- Modify: `packages/protocol-models/src/generated-contract.test.ts`
- Modify: `apps/test-service/internal/toolchain/model.go`
- Modify: `apps/test-service/internal/toolchain/gnu.go`
- Modify: `apps/test-service/internal/toolchain/clangcl_windows.go`
- Modify: `apps/test-service/internal/toolchain/msvc_windows.go`
- Modify: `apps/test-service/internal/session/session.go`
- Test: `apps/test-service/internal/session/session_test.go`
- Test: `apps/test-service/internal/toolchain/gnu_test.go`
- Test: `apps/test-service/internal/toolchain/clangcl_windows_test.go`
- Test: `apps/test-service/internal/toolchain/msvc_windows_test.go`
- Regenerate: `packages/protocol-models/src/generated/workspace.ts`
- Regenerate: `apps/test-service/internal/protocolmodel/v1_2/workspace/generated.go`

**Interfaces:**
- Adds backward-compatible optional `compilerSha256?: string` to the workspace protocol's `ToolchainElement`; the Service's production toolchain adapters populate it, while older v1.2 payloads remain valid.
- Adds `CompilerSHA256 string` to `toolchain.Instance`.
- Guarantees the digest is calculated while the verified compiler file handle is open and is lowercase SHA-256.
- Task 4 treats an absent digest as a fail-closed producer error even though the public v1.2 field is optional for wire compatibility.
- Consumed by: Task 4 producer through `WorkspaceSnapshot.toolchains`.

- [ ] **Step 1: Add failing service and protocol tests**

Extend toolchain adapter tests so GCC, Clang, clang-cl, and MSVC instances contain the exact digest of the verified C compiler snapshot. Extend the session test so `workspace/inspect` contains `compilerSha256` for a populated instance, omits it for a legacy empty instance, and rejects uppercase or malformed non-empty values. Extend the v1.2 schema tests so the canonical fixture includes the digest, a missing digest remains valid, and malformed digests fail.

```go
if got := instance.CompilerSHA256; got != strings.Repeat("a", 64) {
	t.Fatalf("CompilerSHA256 = %q", got)
}
```

```go
if !strings.Contains(string(inspectJSON), `"compilerSha256":"`+strings.Repeat("a", 64)+`"`) {
	t.Fatalf("workspace response lacks compiler digest: %s", inspectJSON)
}
```

- [ ] **Step 2: Run the focused Go tests and verify RED**

Run:

```powershell
go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/session -run 'CompilerSHA256|WorkspaceInspect' -count=1
```

Expected: FAIL because `Instance.CompilerSHA256` and the protocol field do not exist.

- [ ] **Step 3: Add the optional schema field, fixtures, and regenerate models**

Add this property to `$defs.toolchain` without adding it to the `required` array:

```json
"compilerSha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" }
```

Run `pnpm generate:protocol`; do not edit generated TypeScript or Go files manually.

Update `workspace-inspect.valid.json` with a lowercase digest and update `generated-contract.test.ts` to prove the generated optional field is consumable. Keep the pre-field payload valid to preserve v1.2 compatibility.

- [ ] **Step 4: Populate the digest from verified compiler snapshots**

Add `CompilerSHA256` to `toolchain.Instance`. Set it from the already-open snapshot digest in GNU/Clang, clang-cl, and MSVC probes. Extend `toProtocolToolchain` to validate a non-empty digest as lowercase SHA-256 and copy it into the generated protocol model; preserve omission only for legacy/synthetic empty instances.

```go
instance := Instance{
	CompilerSHA256: compilerSnapshot.digest,
	Family:         FamilyGCC,
	Version:        compilerVersion,
}
```

For MSVC use the `cl` snapshot digest; for clang-cl use the C compiler snapshot digest. Manual and automatic candidates follow the same verified probe path.

- [ ] **Step 5: Verify generated contracts and all toolchain tests**

Run:

```powershell
pnpm check:protocol-generated
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/session -count=1
go test ./apps/test-service/... -count=1
git diff --check
```

Expected: generated-contract, toolchain, session, and complete Service tests pass.

- [ ] **Step 6: Commit**

```powershell
git add packages/protocol-schema/schema/v1.2/workspace.schema.json packages/protocol-schema/fixtures/v1.2/workspace-inspect.valid.json packages/protocol-schema/test/schema.test.mjs packages/protocol-models/src/generated/workspace.ts packages/protocol-models/src/generated-contract.test.ts apps/test-service/internal/protocolmodel/v1_2/workspace/generated.go apps/test-service/internal/toolchain/model.go apps/test-service/internal/toolchain/gnu.go apps/test-service/internal/toolchain/clangcl_windows.go apps/test-service/internal/toolchain/msvc_windows.go apps/test-service/internal/session/session.go apps/test-service/internal/session/session_test.go apps/test-service/internal/toolchain/gnu_test.go apps/test-service/internal/toolchain/clangcl_windows_test.go apps/test-service/internal/toolchain/msvc_windows_test.go
git commit -m "feat: expose verified compiler digest"
```

---

### Task 3: Stage deterministic owned framework workspaces

**Files:**
- Create: `tools/service-probe/src/native-framework-workspace.ts`
- Test: `tools/service-probe/src/native-framework-workspace.test.ts`
- Modify: `tools/service-probe/run-tests.mjs`

**Interfaces:**
- Produces: `FrameworkWorkspaceStageOptions` with repository root, owned same-volume staging root, platform, toolchain family, framework ID, prepared framework roots, CMake helper, and Unity generator.
- Produces: `stageFrameworkWorkspace(options: FrameworkWorkspaceStageOptions): Promise<StagedFrameworkWorkspace>`.
- Produces: `validateOwnedFrameworkStage(stageRoot: string, ownershipId: string): Promise<void>`.
- `StagedFrameworkWorkspace` exposes only `serviceDirectory`, `workspaceRoot`, `buildRoot`, `family`, and `frameworkId`.
- Produces: `hashCompiledFrameworkExecutable(workRoot: string, platform: FrameworkPlatform, family: FrameworkToolchainFamily, frameworkId: FrameworkId): Promise<string>`.
- Consumed by: Task 4 producer orchestration.

- [ ] **Step 1: Add filesystem safety and layout tests**

Create temporary repository fixtures with the exact committed matrix files and F1 fixture inventories. Assert that staging produces one owned same-volume staging workspace below `.native-e2e/framework-work/.staging/{invocation}/{platform}/{family}/{framework}` containing:

```text
owner.json
service/
workspace/.unit-test-ide/workspace.json
workspace/source/framework-matrix/CMakeLists.txt
workspace/source/framework-matrix/contract.json
workspace/source/framework-matrix/malformed_cpputest.cpp
workspace/source/framework-matrix/malformed_unity.c
workspace/source/framework-matrix/opaque.c
workspace/source/frameworks/cpputest/
workspace/source/frameworks/unity/
```

Add mutation cases for source symlinks, extra files, missing generated CMock output, unknown prior ownership, path escape attempts, final published-root substitution, unsupported family/platform combinations, and an invalid generator path.

```ts
await assert.rejects(
  stageFrameworkWorkspace({ ...options, family: "msvc", platform: "linux" }),
  /toolchain is incompatible/u,
);
await assert.rejects(
  validateOwnedFrameworkStage(stageRoot, "different-owner"),
  /staging ownership/u,
);
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/native-framework-workspace.test.js
```

Expected: FAIL because the workspace staging module is absent.

- [ ] **Step 3: Implement the closed source inventory and ownership record**

Use explicit file inventories, `lstat`, regular-file checks, path containment checks, and canonical JSON. Do not recursively copy unvalidated directory contents. The ownership record contains only schema version, random invocation ID, platform, and candidate; it contains no absolute path. Require the staging root to be the canonical `.native-e2e/framework-work/.staging/{invocation}/{platform}/{family}/{framework}` path and keep it on the same volume as the published root.

```ts
const MATRIX_FILES = Object.freeze([
  "CMakeLists.txt",
  "contract.json",
  "malformed_cpputest.cpp",
  "malformed_unity.c",
  "opaque.c",
]);

const PLATFORM_FAMILIES = Object.freeze({
  linux: Object.freeze(["gcc", "clang"]),
  win32: Object.freeze(["msvc", "clang-cl"]),
});
```

- [ ] **Step 4: Generate closed workspace and preset configuration**

Write `.unit-test-ide/workspace.json` with one project rooted at `source/framework-matrix`, the exact three containers from `contract.json`, `Debug` only, and the required toolchain family. Write a canonical `CMakePresets.json` whose cache variables fix `UNIT_TEST_IDE_FRAMEWORK`, `UNIT_TEST_IDE_HELPER`, the prepared dependency roots, and `UTIDE_UNITY_RUNNER_GENERATOR` for Unity. The generated configuration must pass the repository workspace schema and CMake preset parser tests.

- [ ] **Step 5: Verify GREEN and normal test discovery**

Add `dist/native-framework-workspace.test.js` to the normal test array and its source/dist names to the closed focused-test map in `run-tests.mjs`, then run:

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-framework-workspace.test.ts
pnpm --filter @unit-test-ide/service-probe test
pnpm test:framework-bundle
git diff --check
```

Expected: all focused and package tests pass; F1 bundle identities remain unchanged.

- [ ] **Step 6: Commit**

```powershell
git add tools/service-probe/src/native-framework-workspace.ts tools/service-probe/src/native-framework-workspace.test.ts tools/service-probe/run-tests.mjs
git commit -m "feat: stage closed framework runtime workspaces"
```

---

### Task 4: Build runtime evidence through real Service discovery

**Files:**
- Create: `tools/service-probe/src/native-framework-prepare.ts`
- Test: `tools/service-probe/src/native-framework-prepare.test.ts`
- Modify: `tools/service-probe/src/native-framework-matrix.ts`
- Test: `tools/service-probe/src/native-framework-matrix.test.ts`
- Modify: `tools/service-probe/run-tests.mjs`

**Interfaces:**
- Produces: `FrameworkRuntimePrepareOptions` with `repositoryRoot`, `platform`, and `candidateCommit`.
- Produces: `PreparedFrameworkRuntime` with validated manifest and owned staging roots.
- Produces: `prepareFrameworkRuntime(options: FrameworkRuntimePrepareOptions, dependencies?: FrameworkRuntimePrepareDependencies): Promise<PreparedFrameworkRuntime>`.
- Produces: `discoverFrameworkCatalog(options: FrameworkDiscoveryOptions): Promise<DiscoveredFrameworkCatalog>` extracted from the existing scenario runner.
- `DiscoveredFrameworkCatalog` contains the selected project/profile/toolchain, complete catalog, and verified `test-catalog` artifact digest and size.
- `FrameworkRuntimePrepareDependencies` carries a complete, already-verified `catalog-10000` benchmark evidence record; preparation does not execute or synthesize benchmark measurements.
- Consumed by: Task 5 atomic CLI publication.

- [ ] **Step 1: Add RED tests for discovery reuse and identity binding**

Extend `native-framework-matrix.test.ts` so both `runFrameworkMatrix` and the new producer discovery path use the same catalog validator. The producer test must prove that it rejects:

- partial catalogs;
- the wrong framework or container names;
- compiler-family substitution;
- F1 fixture executable-input substitution;
- catalog artifact digest substitution;
- duplicate compiled executable profiles;
- a Service discovery task that does not succeed;
- Service cleanup failure.

```ts
test("producer binds the discovered catalog and compiled executable to F1", async () => {
  const prepared = await prepareFrameworkRuntime(prepareOptions, fakeDependencies());
  const unity = prepared.manifest.toolchains[0]!.frameworks[1]!;
  assert.equal(unity.stableIdDigest, stableFrameworkIdDigest("unity", catalog, identity));
  assert.equal(unity.evidence.executableArtifactSha256, sha256(executableBytes));
  assert.equal(unity.cMockProvenance!.manifestSha256, identity.cMockProvenanceSha256);
});
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/native-framework-prepare.test.js tools/service-probe/dist/native-framework-matrix.test.js
```

Expected: FAIL because `prepareFrameworkRuntime` and `discoverFrameworkCatalog` do not exist.

- [ ] **Step 3: Extract the bounded discovery helper**

Move the current inspect, profile selection, discovery task wait, catalog read, catalog validation, and `test-catalog` artifact read into `discoverFrameworkCatalog`. Keep the same named deadlines and sanitized errors. Refactor `runFrameworkMatrix` to call the helper before executing scenarios so producer and consumer cannot drift.

```ts
export interface DiscoveredFrameworkCatalog {
  readonly catalog: ProtocolTestCatalog;
  readonly catalogArtifactSha256: string;
  readonly catalogArtifactSizeBytes: number;
  readonly profile: BuildProfileElement;
  readonly projectId: string;
  readonly toolchain: ToolchainElement;
}
```

- [ ] **Step 4: Implement producer orchestration**

For the platform's exact families and both frameworks, load the closed F1 identity, prepared dependency roots, matrix contract, CMake bundle, Service binary, generator, and the already-verified benchmark evidence dependency. Stage each workspace below the owned same-volume staging root, start the Service, discover/build the catalog, calculate stable ID and actual executable digest, and assemble a closed runtime record.

Use `try/finally` so every Service is disposed before returning. Do not execute the 17 scenario matrix during preparation.

```ts
const discovery = await discoverFrameworkCatalog({
  fixture,
  frameworkId,
  toolchainFamily: family,
  timeoutMs: 120_000,
});
const stableIdDigest = stableFrameworkIdDigest(frameworkId, discovery.catalog, identity);
const executableArtifactSha256 = await hashCompiledFrameworkExecutable(
  stagingWorkRoot,
  platform,
  family,
  frameworkId,
);
```

- [ ] **Step 5: Build and validate the closed manifest**

Require the selected Service toolchain's optional wire field `compilerSha256` to be present and lowercase SHA-256, then populate compiler version/SHA from that selected toolchain. Populate dependency version/archive/tree from the locked F1 manifest and identity, source evidence from the fixture identity, catalog digest from the Service artifact, executable digest from the staged build root, and the canonical CMock record from `cmock-generation.json`. Carry the complete verified benchmark dependency unchanged; do not fabricate timings or allocation counts. Pass the result through `buildFrameworkRuntimeManifest` before returning it.

- [ ] **Step 6: Verify GREEN**

Add the producer test to the normal test array and closed focused-test map in `run-tests.mjs`, then run:

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-framework-prepare.test.ts
pnpm --filter @unit-test-ide/service-probe test -- native-framework-matrix.test.ts
pnpm --filter @unit-test-ide/service-probe test
pnpm check:framework-bundle
git diff --check
```

Expected: producer and matrix focused tests pass; the complete service-probe suite and F1 integrity check pass.

- [ ] **Step 7: Commit**

```powershell
git add tools/service-probe/src/native-framework-prepare.ts tools/service-probe/src/native-framework-prepare.test.ts tools/service-probe/src/native-framework-matrix.ts tools/service-probe/src/native-framework-matrix.test.ts tools/service-probe/run-tests.mjs
git commit -m "feat: prepare framework runtime from service evidence"
```

---

### Task 5: Publish the runtime atomically and expose the closed CLI

**Files:**
- Create: `tools/service-probe/src/native-framework-publish.ts`
- Test: `tools/service-probe/src/native-framework-publish.test.ts`
- Create: `tools/service-probe/src/native-framework-benchmark.ts`
- Test: `tools/service-probe/src/native-framework-benchmark.test.ts`
- Modify: `apps/test-service/internal/testdomain/catalog_benchmark_test.go`
- Modify: `tools/service-probe/src/native-framework-prepare.ts`
- Test: `tools/service-probe/src/native-framework-prepare.test.ts`
- Modify: `tools/service-probe/src/native-framework-workspace.ts`
- Test: `tools/service-probe/src/native-framework-workspace.test.ts`
- Modify: `tools/service-probe/src/native-framework-runtime.ts`
- Modify: `tools/service-probe/package.json`
- Modify: `tools/service-probe/run-tests.mjs`
- Modify: `package.json`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- Produces: `publishFrameworkRuntime(prepared: PreparedFrameworkRuntime): Promise<PublishedFrameworkRuntime>`.
- Produces: `parseFrameworkPrepareArguments(arguments_: readonly string[]): { platform: FrameworkPlatform; candidateCommit: string }`.
- Produces CLI command: `pnpm prepare:native-framework-runtime -- --platform win32 --candidate 0123456789abcdef0123456789abcdef01234567`.
- The CLI loads benchmark evidence only from the fixed, repository-owned audited source selected by the implementation; it accepts no benchmark values or paths from argv/environment.
- Produces `collectAuditedFrameworkBenchmark(repositoryRoot: string): Promise<VerifiedFrameworkBenchmark>` by invoking only the fixed repository-owned Go `BenchmarkCatalog10000` command three times; it parses exactly three allocation samples, combines them with fixed catalog identity anchors, and validates the closed benchmark contract before returning it.
- Consumed by: Task 6 hosted workflow.

- [ ] **Step 1: Add RED tests for atomic publication and rollback**

Test a new publish, replacement of a complete old runtime, rename failure after backup, validation failure after final rename, owned staging cleanup, and unknown staging preservation. Assert that the consumer sees either the old complete runtime or the new complete runtime, never a mixed pair.

```ts
test("publication restores the previous complete runtime after final validation fails", async () => {
  const previous = await writePublishedRuntime(root, "a".repeat(40));
  const prepared = await writePreparedRuntime(root, "b".repeat(40));
  await assert.rejects(
    publishFrameworkRuntime(prepared, { validatePublished: async () => { throw new Error("injected"); } }),
    /published runtime validation/u,
  );
  assert.deepEqual(await readPublishedRuntime(root), previous);
});
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/native-framework-publish.test.js
```

Expected: FAIL because the publisher is absent.

- [ ] **Step 3: Implement owned same-volume publication with reader coordination**

Validate staging ownership before every mutation. Acquire a fixed repository-local publication lock shared with the runtime loader. Rename the current platform manifest/work root to bounded backup paths, rename verified staging into the final fixed paths, re-open the final manifest with `parseFrameworkRuntimeManifest`, validate all final workspace roots, then remove only the owned backup. Restore the backup if any final step fails. Reject unknown backup or staging ownership instead of deleting it. The consumer must hold the same lock while reading the manifest and all work roots; lock failure is a sanitized fail-closed error.

- [ ] **Step 4: Implement the closed CLI and package entry**

`parseFrameworkPrepareArguments` accepts exactly four arguments in the order `--platform`, platform value, `--candidate`, commit. `main` derives the repository root from `import.meta.dirname`, calls prepare then publish, and writes one path-free JSON summary containing schema version, platform, candidate, toolchain families, and manifest SHA-256.

Implement `collectAuditedFrameworkBenchmark` in the new benchmark module. Run the fixed Go benchmark with a fixed package, benchmark name, `-benchtime=1x`, and `-count=3`; parse exactly three `allocs/op` values and reject malformed or over-budget output. Bind catalog revision/artifact/stable-ID digests to fixed constants derived from the committed benchmark input, then validate the complete record with Task 1's benchmark rules. Reject caller-supplied benchmark paths, values, environment overrides, shell text, or mutable fixture substitution. Tests must prove malformed/over-budget output and path-bearing values fail closed.

Add:

```json
{
  "prepare:native-framework-runtime": "node build-service.mjs && pnpm run build && node dist/native-framework-prepare.js"
}
```

to the service-probe package and a root forwarding script with the same name.

- [ ] **Step 5: Update native E2E documentation**

Replace the Task 6 placeholder prerequisite text with the exact Windows and Linux producer commands, fixed outputs, required environment variables, offline requirement, failure semantics, and the statement that local success is not hosted evidence.

- [ ] **Step 6: Verify GREEN and package coverage**

Add publish tests to the normal test array and closed focused-test map in `run-tests.mjs`, then run:

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-framework-publish.test.ts
pnpm --filter @unit-test-ide/service-probe test -- native-framework-prepare.test.ts
pnpm --filter @unit-test-ide/service-probe test
pnpm test:workspace
pnpm check:phase9-gates
git diff --check
```

Expected: all focused/package/static checks pass and the gate matrix remains unchanged with `releaseReady=false`.

- [ ] **Step 7: Commit**

```powershell
git add tools/service-probe/src/native-framework-publish.ts tools/service-probe/src/native-framework-publish.test.ts tools/service-probe/src/native-framework-prepare.ts tools/service-probe/src/native-framework-prepare.test.ts tools/service-probe/package.json tools/service-probe/run-tests.mjs package.json docs/native-e2e.md
git commit -m "feat: publish trusted framework runtime atomically"
```

---

### Task 6: Wire fixed hosted producers and exact artifacts

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `tools/linux-offline/run.test.mjs`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- Produces job: `verify-framework-windows` on `windows-2022`.
- Modifies job: `verify-linux` to prepare and run the Linux required framework matrix offline.
- Modifies job: `verify-framework-matrix` to depend on `verify-framework-windows` and `verify-linux`.
- Produces exact artifacts: `native-framework-windows`, `native-framework-linux`, `native-framework-matrix-report`.

- [ ] **Step 1: Add failing workflow contract tests**

Assert:

- `verify-framework-windows` exists and uses exactly `windows-2022`;
- existing `verify-windows` and `verify-windows-wfp` runner expressions and privileged steps remain byte-for-byte present;
- all setup/upload/download actions remain pinned by full SHA;
- the Windows framework job has no `secrets`, WFP command, self-hosted label, or administrator step;
- producer precedes required native execution;
- Linux producer and native execution both use `tools/linux-offline/run.mjs --allow-sudo-root` after dependency preparation;
- framework uploads have fixed paths, 14-day retention, `if-no-files-found: error`, and no `always()` condition;
- the aggregator needs exactly `verify-framework-windows` and `verify-linux` and uses the fixed P4 command.

```js
assert.match(workflow, /^  verify-framework-windows:\r?\n    runs-on: windows-2022$/mu);
assert.doesNotMatch(jobBlock("verify-framework-windows"), /unit-test-wfp|windows-2025-vs2026|secrets:|WFP/iu);
assert.ok(
  jobBlock("verify-framework-windows").indexOf("pnpm prepare:native-framework-runtime") <
  jobBlock("verify-framework-windows").indexOf("pnpm test:e2e:native"),
);
```

- [ ] **Step 2: Run workflow tests and verify RED**

Run:

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/linux-offline/run.test.mjs
```

Expected: FAIL because `verify-framework-windows` and the producer invocations are absent.

- [ ] **Step 3: Add the fixed Windows framework job**

Use pinned checkout, pnpm, Node, Go, CMake cache, and F1 cache steps following existing workflow patterns. Build the Unity generator and Service, invoke:

```powershell
pnpm prepare:native-framework-runtime -- --platform win32 --candidate '${{ github.sha }}'
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
$env:UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED='1'
pnpm test:e2e:native -- --platform win32
```

Upload `.native-e2e/artifacts/windows/framework-report.json` as `native-framework-windows` only after the required matrix succeeds.

- [ ] **Step 4: Add Linux offline producer execution**

After CMake/F1/Go preparation and before the existing native matrix, run:

```sh
node tools/linux-offline/run.mjs --allow-sudo-root -- pnpm prepare:native-framework-runtime -- --platform linux --candidate "${{ github.sha }}"
```

Set both required environment variables on the Linux native step. Remove `always()` from the framework upload while keeping `if-no-files-found: error` and 14-day retention.

- [ ] **Step 5: Update the aggregator dependency and documentation**

Change `verify-framework-matrix.needs` to `verify-framework-windows` and `verify-linux`. Keep the exact two download paths, sole P4 aggregator command, output path, artifact name, 14-day retention, and error-on-missing behavior. Document the independent hosted Windows framework job and unchanged administrator/WFP path.

- [ ] **Step 6: Verify GREEN**

Run:

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/linux-offline/run.test.mjs
pnpm test:native-framework-matrix
pnpm check:phase9-gates
git diff --check
```

Expected: workflow/static tests pass; the P4 aggregator contract passes; Phase 9 recorded state remains unchanged.

- [ ] **Step 7: Commit**

```powershell
git add .github/workflows/foundation.yml tools/workspace-smoke/workspace-smoke.test.mjs tools/linux-offline/run.test.mjs docs/native-e2e.md
git commit -m "ci: produce phase9 framework matrix on fixed runners"
```

---

### Task 7: Run final local verification and prepare the hosted handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-09-16-phase9-batch-f2-framework-matrix.md`
- Create: `.superpowers/sdd/2026-09-17-phase9-f2-trusted-runtime-producer/task-6-report.md` (git-ignored execution evidence)

**Interfaces:**
- Produces a clean local branch ready for whole-branch review.
- Does not push, dispatch a workflow, create a PR, or change gate evidence.

- [ ] **Step 1: Run focused producer and workflow suites**

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-framework-runtime-contract.test.ts
pnpm --filter @unit-test-ide/service-probe test -- native-framework-workspace.test.ts
pnpm --filter @unit-test-ide/service-probe test -- native-framework-prepare.test.ts
pnpm --filter @unit-test-ide/service-probe test -- native-framework-publish.test.ts
node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/linux-offline/run.test.mjs
```

Expected: all focused tests pass with no unexpected skip.

- [ ] **Step 2: Run complete local verification**

Use repository-pinned Node 24.18 or newer within major 24 and pnpm 11.4.0:

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm test:framework-bundle
pnpm test:native-framework-matrix
pnpm test:workspace
pnpm check:phase9-gates
pnpm verify
git diff --check
```

Expected: all commands pass; only explicitly existing platform-dependent skips are accepted; no generated tracked file differs.

- [ ] **Step 3: Update the parent F2 plan status and superseded workflow wording**

Mark the trusted producer and static workflow wiring steps complete. Update the parent plan's stale Task 5/Task 6 references from `.native-e2e/framework-inputs/{windows|linux}` and the existing `verify-windows` producer path to the approved fixed `.native-e2e/framework-runtime/{windows|linux}` outputs and independent `verify-framework-windows` plus offline `verify-linux` producer path. Keep hosted evidence, receipt, gate-matrix update, push, PR, merge, signing, Release, and legal approval incomplete. Record that real four-toolchain execution requires separate user authorization for remote operations.

- [ ] **Step 4: Commit the local handoff update**

```powershell
git add docs/superpowers/plans/2026-09-16-phase9-batch-f2-framework-matrix.md
git commit -m "docs: record phase9 framework producer verification"
```

- [ ] **Step 5: Run whole-branch review**

Generate a review package from `b9541b3` through `HEAD`. Review the producer, runtime consumer, platform report, P4 aggregator, workflow contracts, deferred gates, and the ledger's residual Minor about Linux CMock field assertions. Resolve all Critical/Important findings before requesting remote authorization.
