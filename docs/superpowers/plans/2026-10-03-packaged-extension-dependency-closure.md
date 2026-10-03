# Packaged Extension Dependency Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the built-in Unit Test IDE extension self-contained so the release-shaped extension activates without repository `node_modules`.

**Architecture:** Keep the CommonJS Code OSS entry wrapper, but replace its imported ESM implementation with an esbuild bundle that includes the extension, workspace clients, AJV, and protocol JSON. Release staging validates the manifest entry before publication, and the Extension Host smoke accepts an explicit staged-extension root so CI tests the files that are actually packaged.

**Tech Stack:** Node.js 24.18.0, pnpm 11.4.0, TypeScript 6.0.3, esbuild 0.28.2, Node test runner, Code OSS Extension Host, GitHub Actions PowerShell.

**Spec:** `docs/superpowers/specs/2026-10-03-packaged-extension-dependency-closure-design.md`

## Global Constraints

- The packaged extension must not resolve npm or workspace packages from an ancestor `node_modules`.
- `vscode` is the only non-Node module intentionally supplied by the host at runtime.
- Node built-ins may remain external platform modules.
- Preserve the existing CommonJS `main` entry and all extension behavior and protocol semantics.
- Release staging must fail before publishing a staging root when the manifest entry is absent or unsafe.
- Do not publish a Release, enable signing, push, create a PR, or merge without separate authorization.
- Formal third-party license/legal approval remains a later Phase 8 gate.

## Review Focus

- A release-shaped extension copied under an unrelated temporary directory must import with no ancestor `node_modules`; Task 1 owns this regression test.
- Protocol schema initialization for every supported version, including loop-loaded v1.5 and v1.6 schemas, must survive bundling; Task 1 owns this through the isolated import plus existing client tests.
- A missing, absolute, traversal, or non-`dist` extension `main` must fail staging before publication; Task 2 owns these cases.
- An explicit staged extension path containing spaces must be used exactly, while an empty override must retain the source-development default; Task 3 owns these cases.
- Producer CI must smoke the current candidate staging tree once, before MSIX packaging, and must not substitute the source extension or redundantly smoke the baseline tree; Task 3 owns this workflow contract.

---

### Task 1: Produce and prove a self-contained extension implementation

**Files:**

- Create: `apps/code-oss-extension/build-extension.mjs`
- Create: `apps/code-oss-extension/test/packaged-extension.test.mjs`
- Create: `packages/test-client/src/schema-registry.ts`
- Modify: `apps/code-oss-extension/package.json`
- Modify: `packages/test-client/src/client.ts`
- Modify: `packages/test-client/src/connection.ts`
- Modify: `packages/test-client/tsconfig.json`
- Modify: `pnpm-lock.yaml`

**Interfaces:**

- Produces: `protocolSchema(path: ProtocolSchemaPath): object` in `schema-registry.ts`, backed by one static JSON import for every key exported by `packages/protocol-schema/package.json`.
- Produces: `apps/code-oss-extension/dist/src/extension.js`, an ESM bundle with only `vscode` and `node:*` imports external.
- Produces: package script `build:bundle`; package script `build` runs TypeScript first and the bundle second.
- Consumes: existing `dist/src/extension-entry.cjs`, which continues to import `./extension.js`.

- [ ] **Step 1: Write the isolated-package regression test**

Add `packaged-extension.test.mjs` with the test name `release-shaped extension implementation imports without repository node_modules`. Build the current extension, copy only `package.json` and `dist/` beneath a fresh OS temporary directory, dynamically import the copied `dist/src/extension.js`, and assert that it exports functions named `activate` and `deactivate`. Assert that the temporary package path has no ancestor `node_modules` before importing.

- [ ] **Step 2: Run the regression test and verify RED**

Run:

```powershell
pnpm --filter code-oss-extension build
node --test apps/code-oss-extension/test/packaged-extension.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND` naming `@unit-test-ide/test-client` from the copied extension implementation.

- [ ] **Step 3: Pin the production bundler**

Run:

```powershell
pnpm --filter code-oss-extension add --save-dev --save-exact esbuild@0.28.2
```

Expected: `apps/code-oss-extension/package.json` and `pnpm-lock.yaml` record exactly `0.28.2`; no root runtime dependency is added.

- [ ] **Step 4: Make protocol schemas statically bundle-visible**

In `schema-registry.ts`, statically import every JSON path exported by `@unit-test-ide/protocol-schema` using JSON import attributes, define `ProtocolSchemaPath` from the closed registry keys, and implement `protocolSchema(path)` as a closed lookup that throws `unknown protocol schema: <path>` for a missing key. Enable `resolveJsonModule` only in `packages/test-client/tsconfig.json`. Replace every `createRequire()` schema load in `client.ts` and `connection.ts` with `protocolSchema()`; preserve the existing AJV registration order and supported protocol versions exactly.

- [ ] **Step 5: Add the deterministic extension bundle**

In `build-extension.mjs`, call esbuild with entry `src/extension.ts`, output `dist/src/extension.js`, `bundle: true`, `platform: "node"`, `format: "esm"`, `target: "node20"`, `external: ["vscode"]`, `metafile: true`, `sourcemap: false`, and deterministic non-minified output. After building, inspect output imports and throw unless every external import is exactly `vscode` or begins with `node:`. Update package scripts so `build:bundle` runs this file and `build` runs `tsc -b tsconfig.json` followed by `build:bundle`.

- [ ] **Step 6: Run the isolated test and focused client tests and verify GREEN**

Run:

```powershell
pnpm --filter code-oss-extension build
node --test apps/code-oss-extension/test/packaged-extension.test.mjs
pnpm --filter @unit-test-ide/test-client test
pnpm --filter code-oss-extension test
```

Expected: isolated import PASS; test-client PASS; extension suite PASS with no unresolved package warning.

- [ ] **Step 7: Commit Task 1**

```powershell
git add apps/code-oss-extension packages/test-client pnpm-lock.yaml
git commit -m "fix: bundle Code OSS extension runtime dependencies"
```

### Task 2: Fail release staging on an invalid extension entry

**Files:**

- Modify: `tools/release/stage.mjs`
- Modify: `tools/release/stage.test.mjs`

**Interfaces:**

- Produces: internal `validateExtensionPackage(extensionRoot)` returning validated manifest, dist, and manifest-entry files.
- Consumes: `package.json.main`; accepted values start with `./dist/`, use portable relative components, and resolve to a regular non-reparse-point file inside the extension root.

- [ ] **Step 1: Write staging failure tests**

Add tests named:

- `stageRelease rejects a missing extension manifest entry before publication`
- `stageRelease rejects an extension manifest entry outside dist before publication`
- `stageRelease rejects absolute and traversal extension manifest entries before publication`

Each test uses `createReleaseFixture`, changes only the extension fixture, asserts a closed `extension main`/`unsafe staged path` error, and asserts the final staging root does not exist. Update the happy-path fixture to use `./dist/src/extension-entry.cjs` plus an implementation file and assert both are staged.

- [ ] **Step 2: Run the staging tests and verify RED**

Run:

```powershell
node --test tools/release/stage.test.mjs
```

Expected: the new missing/unsafe-entry tests FAIL because staging currently validates only `package.json` and `dist/`.

- [ ] **Step 3: Implement extension package validation**

Implement `validateExtensionPackage(extensionRoot)` in `stage.mjs`: parse the manifest as a plain JSON object, require a non-empty `main`, require the normalized value to begin with `./dist/`, reject absolute/traversal/non-portable components using the existing path helpers, and validate the resolved entry with `validateRealFile`. Call it before creating the staging parent and reuse its validated manifest and dist paths for copying.

- [ ] **Step 4: Run staging and extension package tests and verify GREEN**

Run:

```powershell
node --test tools/release/stage.test.mjs
node --test apps/code-oss-extension/test/packaged-extension.test.mjs
```

Expected: all staging tests PASS (environment-dependent bundle tests may explicitly SKIP); isolated package test PASS.

- [ ] **Step 5: Commit Task 2**

```powershell
git add tools/release/stage.mjs tools/release/stage.test.mjs
git commit -m "fix: validate staged extension entry"
```

### Task 3: Smoke the staged extension in the Windows producer

**Files:**

- Modify: `apps/code-oss-extension/test/extension-host-smoke-support.mjs`
- Modify: `apps/code-oss-extension/test/extension-host-smoke-support.test.mjs`
- Modify: `apps/code-oss-extension/test/extension-host-smoke.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/release/producer/workflow-contract.test.mjs`

**Interfaces:**

- Produces: `resolveExtensionUnderTest(repositoryRoot, configuredPath)` returning the default source extension for blank input or the exact absolute resolution of `UNIT_TEST_IDE_EXTENSION_PATH`.
- Consumes: `CODE_OSS_EXECUTABLE` and optional `UNIT_TEST_IDE_EXTENSION_PATH`.
- Produces: Windows producer contract that runs the host smoke once against `<stagingRoot>/app/extensions/unit-test-ide` with `<stagingRoot>/app/code-oss-runtime/Code - OSS.exe` before MSIX packaging.

- [ ] **Step 1: Write resolver tests and verify RED**

Add tests proving that `resolveExtensionUnderTest`:

- returns `apps/code-oss-extension` for `undefined`, empty, or whitespace input;
- resolves an explicit staged path containing spaces without falling back to the source tree.

Run:

```powershell
node --test apps/code-oss-extension/test/extension-host-smoke-support.test.mjs
```

Expected: FAIL because `resolveExtensionUnderTest` is not exported.

- [ ] **Step 2: Implement the smoke path override and verify GREEN**

Implement `resolveExtensionUnderTest(repositoryRoot, configuredPath)` in the support module and use it in `extension-host-smoke.mjs`. Add the chosen extension path to the existing redaction set. Do not change the current honest SKIP when `CODE_OSS_EXECUTABLE` is absent.

Run the support test again. Expected: PASS.

- [ ] **Step 3: Write the producer workflow contract test and verify RED**

Add `Windows producer smokes the current staged extension before MSIX packaging` to `workflow-contract.test.mjs`. Assert that the `Stage and package Windows MSIX` body, after obtaining `$stagingRoot` and before calling `package-msix.ps1`, sets both environment variables to paths below that staging root, invokes `node apps/code-oss-extension/test/extension-host-smoke.mjs`, fails on nonzero exit, and removes both variables. Assert that this sequence occurs only once and is absent from the baseline-staging block.

Run:

```powershell
node --test tools/release/producer/workflow-contract.test.mjs
```

Expected: FAIL because the producer does not yet run a staged-extension host smoke.

- [ ] **Step 4: Add the Windows staged-extension smoke and verify GREEN**

Modify the Windows packaging script block in `foundation.yml` immediately after the staged testgen consumer check. Set `CODE_OSS_EXECUTABLE` to the staged `Code - OSS.exe`, set `UNIT_TEST_IDE_EXTENSION_PATH` to the staged extension root, run the smoke, fail closed on a nonzero status, and remove both environment variables in a `finally` block. Do not add a second smoke for `$baselineStagingRoot`.

Run:

```powershell
node --test tools/release/producer/workflow-contract.test.mjs
node --test apps/code-oss-extension/test/extension-host-smoke-support.test.mjs
```

Expected: both suites PASS.

- [ ] **Step 5: Run a real local staged-extension host smoke when the acceptance runtime exists**

Build the extension, create a release-shaped temporary extension containing only its manifest and `dist/`, and run:

```powershell
$env:CODE_OSS_EXECUTABLE = 'D:\unitTest\windows-app\app\code-oss-runtime\Code - OSS.exe'
$env:UNIT_TEST_IDE_EXTENSION_PATH = '<absolute temporary release-shaped extension path>'
node apps/code-oss-extension/test/extension-host-smoke.mjs
```

Expected when the executable exists: `PASS: Code-OSS Extension Host activation marker observed and host process exited`. If the exact executable is absent, record the explicit environment skip rather than claiming host evidence.

- [ ] **Step 6: Commit Task 3**

```powershell
git add apps/code-oss-extension/test .github/workflows/foundation.yml tools/release/producer/workflow-contract.test.mjs
git commit -m "test: smoke staged Code OSS extension"
```

### Task 4: Document and verify the complete fix

**Files:**

- Modify: `docs/development.md`

**Interfaces:**

- Documents: `build:bundle`, isolated package verification, and `UNIT_TEST_IDE_EXTENSION_PATH` for staged host smoke.
- Verifies: all workspace suites plus the focused packaging and producer contracts.

- [ ] **Step 1: Update development documentation**

Document that `pnpm --filter code-oss-extension build` now emits a self-contained `dist/src/extension.js`, that `vscode` and Node built-ins are the only allowed externals, and that `UNIT_TEST_IDE_EXTENSION_PATH` selects a release-shaped extension root for `test:host` while blank input retains source-development behavior.

- [ ] **Step 2: Run focused verification**

Run:

```powershell
pnpm --filter @unit-test-ide/test-client test
pnpm --filter code-oss-extension test
node --test tools/release/stage.test.mjs tools/release/producer/workflow-contract.test.mjs
pnpm build
```

Expected: all commands exit 0; only tests with explicitly unavailable native bundles may report SKIP.

- [ ] **Step 3: Run the repository test suite**

Run:

```powershell
pnpm test
```

Expected: exit 0. Any environment-only native skip must be named in the final report; no failure may be omitted.

- [ ] **Step 4: Inspect release dependency closure**

Copy the built extension manifest and `dist/` into a fresh directory outside the repository, confirm there is no `node_modules`, run the isolated import test there, and search `dist/src/extension.js` for unresolved `@unit-test-ide/`, `ajv`, or `ajv-formats` import specifiers. Expected: import PASS and no unresolved runtime package specifier.

- [ ] **Step 5: Commit Task 4**

```powershell
git add docs/development.md
git commit -m "docs: describe packaged extension verification"
```

- [ ] **Step 6: Run final diff and status checks**

Run:

```powershell
git diff --check master...HEAD
git status --short --branch
git log --oneline --decorate master..HEAD
```

Expected: no whitespace errors, no uncommitted tracked changes, and only the design, implementation, tests, workflow, and documentation commits for this fix.
