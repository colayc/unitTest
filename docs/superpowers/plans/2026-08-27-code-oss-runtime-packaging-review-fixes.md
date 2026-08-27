# Code-OSS Runtime Packaging Final-Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every Critical/Important finding from the final branch review and prove that the real Windows Code-OSS runtime can be staged, packaged, verified, and consumed without weakening path, license, archive, permission, provenance, or lifecycle controls.

**Architecture:** One JS portable-path module becomes the authoritative programmatic contract for runtime validation, staging, manifest construction/validation, and qualification; JSON Schema retains an equivalent closed ASCII pattern for independent consumers. Windows MSIX verification and install-smoke share one canonical ZIP-entry mapper. Linux artifact input gains a closed, trusted-run-bound file/mode inventory that restores executable flags lost by GitHub artifact transport before validation and staging.

**Tech Stack:** Node.js 24.18.0 ESM and `node:test`, pnpm 11.4.0, PowerShell 7, Bash, Go 1.26.6, Windows SDK `makeappx`, GitHub Actions YAML.

**Spec:** `docs/superpowers/plans/2026-08-27-code-oss-runtime-packaging.md` plus final-review findings recorded in `.superpowers/sdd/2026-08-27-code-oss-runtime-packaging/review-66da327..c55ff36.diff` and the reviewer verdict.

## Global Constraints

- Preserve exact launchers: Windows `Code - OSS.exe`; Linux `code-oss`.
- Portable release paths allow only the existing ASCII set plus the real-runtime-required `@`, `(`, and `)`, including internal spaces; every component must be non-empty and must reject leading/trailing space, trailing dot, `.`, `..`, Windows device basenames/extensions, controls, `\`, `:`, `<`, `>`, `"`, `|`, `?`, and `*`.
- Do not commit or upload the locally built Code-OSS runtime.
- Preserve exact payload-set, digest, executable-mode, signature, run-id, event, and signing-secret boundaries.
- Preserve the lifecycle evidence schema, 30-second launch bound, rollback/repeated rollback, uninstall, and user-data checks.
- Use TDD for every fix; preserve truthful RED/GREEN evidence and commit after each independently reviewable task.
- Phase 8 remains incomplete until the later trusted artifact-production plan and hosted workflow evidence finish.

---

### Task 1: Centralize portable paths and accept real Code-OSS names

**Files:**
- Create: `tools/release/portable-path.mjs`
- Create: `tools/release/portable-path.test.mjs`
- Modify: `tools/release/code-oss-runtime.mjs`
- Modify: `tools/release/code-oss-runtime.test.mjs`
- Modify: `tools/release/stage.mjs`
- Modify: `tools/release/stage.test.mjs`
- Modify: `tools/release/manifest.mjs`
- Modify: `tools/release/manifest.schema.json`
- Modify: `tools/release/manifest.test.mjs`
- Modify: `tools/release/release-manifest-validation.mjs`
- Modify: `tools/release/qualification.mjs`
- Modify: `tools/release/qualification.test.mjs`

**Interfaces:**
- Produces `isPortableReleasePath(value): boolean` and `isPortableReleasePathComponent(value): boolean` from `portable-path.mjs`.
- All JS release-path consumers import those functions instead of maintaining private predicates.
- JSON Schema independently enforces the same closed characters and empty-component rules before programmatic validation repeats the full component checks.

- [ ] **Step 1: Add failing shared-contract regressions**

Test acceptance of these literal real-runtime paths:

```text
app/code-oss-runtime/resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe
app/code-oss-runtime/resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage
```

Test rejection of `app//x`, `app/x/`, leading/trailing component spaces, trailing dots, device basename extension forms, traversal, backslashes, colon, controls, invalid characters, and non-ASCII names. Apply direct cases to artifact and license paths in schema validation, `validateReleaseManifestRecord()`, manifest building, qualification current/baseline records, runtime validation, and staging.

- [ ] **Step 2: Run focused tests and preserve RED**

```powershell
node --test tools/release/portable-path.test.mjs tools/release/code-oss-runtime.test.mjs tools/release/manifest.test.mjs tools/release/qualification.test.mjs tools/release/stage.test.mjs
```

Expected RED: `@`/parentheses fail schema validation; doubled/trailing separators pass the shared schema validator.

- [ ] **Step 3: Implement the shared closed contract**

Use a component character set equivalent to:

```js
/^[A-Za-z0-9._+@() -]+$/u
```

Then apply the component rules from Global Constraints. Update the schema pattern to add `@()` and explicit `(?!.*//)` / `(?!.*\/$)` guards while retaining device-name and edge guards. After Ajv succeeds, make `validateReleaseManifestRecord()` call `isPortableReleasePath()` for every artifact and license so schema and programmatic consumers fail closed together.

- [ ] **Step 4: Run focused and adjacent release tests**

```powershell
node --test tools/release/portable-path.test.mjs tools/release/code-oss-runtime.test.mjs tools/release/manifest.test.mjs tools/release/qualification.test.mjs tools/release/stage.test.mjs tools/release/license-audit.test.mjs
git diff --check
```

- [ ] **Step 5: Commit**

```powershell
git add tools/release/portable-path.mjs tools/release/portable-path.test.mjs tools/release/code-oss-runtime.mjs tools/release/code-oss-runtime.test.mjs tools/release/stage.mjs tools/release/stage.test.mjs tools/release/manifest.mjs tools/release/manifest.schema.json tools/release/manifest.test.mjs tools/release/release-manifest-validation.mjs tools/release/qualification.mjs tools/release/qualification.test.mjs
git commit -m "fix: unify portable release path validation"
```

---

### Task 2: Close the real Code-OSS notice inventory

**Files:**
- Modify: `tools/release/stage.mjs`
- Modify: `tools/release/stage.test.mjs`
- Modify: `tools/release/license-audit.test.mjs`

**Interfaces:**
- The Code-OSS notice copier recognizes leading license/notice/copying names plus delimiter-bounded suffix forms and `ThirdPartyNotices`.
- Every recognized source notice is copied under `licenses/code-oss/<original-relative-path>` and appears exactly once in the release manifest/audit.

- [ ] **Step 1: Add failing real-name notice tests**

Add fixtures for all observed forms:

```text
LICENSES.chromium.html
resources/app/LICENSE.txt
resources/app/ThirdPartyNotices.txt
resources/app/extensions/git/dist/main.js.LICENSE.txt
resources/app/extensions/latex/cpp-bailout-license.txt
resources/app/extensions/ms-vscode.js-debug/ThirdPartyNotices.txt
```

Assert each is copied and manifest-bound; deleting any copied notice or adding an unlisted copy must fail license audit.

- [ ] **Step 2: Run staging/license tests and preserve RED**

```powershell
node --test tools/release/stage.test.mjs tools/release/license-audit.test.mjs
```

Expected RED: `ThirdPartyNotices.txt`, `*.js.LICENSE.txt`, and `*-license.txt` are omitted.

- [ ] **Step 3: Broaden only the notice-name classifier**

Match case-insensitive leading `LICENSE(S)`, `LICENCE(S)`, `NOTICE(S)`, and `COPYING`; exact/delimited `ThirdPartyNotices`; and delimiter-bounded `license/licence/notice` suffixes such as `.LICENSE.txt` and `-license.txt`. Preserve the existing closed destination and symlink/reparse checks.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/release/stage.test.mjs tools/release/license-audit.test.mjs tools/release/manifest.test.mjs
git diff --check
git add tools/release/stage.mjs tools/release/stage.test.mjs tools/release/license-audit.test.mjs
git commit -m "fix: close Code-OSS runtime notice collection"
```

---

### Task 3: Share canonical MSIX paths with install-smoke

**Files:**
- Create: `tools/release/windows/msix-entry-path.ps1`
- Modify: `tools/release/windows/verify-msix.ps1`
- Modify: `tools/release/windows/package-msix.test.mjs`
- Modify: `tools/release/install-smoke.ps1`
- Modify: `tools/release/update.test.mjs`

**Interfaces:**
- `ConvertFrom-CanonicalMsixEntryPath` converts a raw ZIP entry name to one logical slash path, permitting canonical per-component `%20` only where decoding remains a safe Windows component.
- `Get-CanonicalMsixEntryMap` rejects raw/decoded aliases before metadata filtering or extraction.
- Both verifier and install-smoke dot-source the same helper.

- [ ] **Step 1: Add failing archive and lifecycle tests**

Add negative metadata-entry cases for leading/trailing space, trailing dot, and `CON.txt`; keep encoded separator/control/double-encoding cases. Add a Windows SDK-backed install-smoke fixture whose launcher is a copy of `process.execPath` named `Code - OSS.exe`, so `makeappx` stores `%20`; package target/baseline fixtures and require the full disposable lifecycle to pass.

- [ ] **Step 2: Run Windows tests and preserve RED**

```powershell
node --test tools/release/windows/package-msix.test.mjs tools/release/update.test.mjs
```

Expected RED: verification accepts unsafe metadata components and `ExtractToDirectory` leaves a literal `%20` launcher that install-smoke cannot find.

- [ ] **Step 3: Extract only through the canonical map**

Move the current strict percent parser/alias map into `msix-entry-path.ps1`. Enforce invalid Windows characters, C0/DEL, empty/dot components, leading/trailing spaces, trailing dots, and device basenames/extensions on every decoded entry, while special package footprint names such as `[Content_Types].xml` remain valid. Replace `ExtractToDirectory` with explicit `ZipArchiveEntry.Open()` copies selected through the canonical map; read/extract `release-manifest.json`, run `verify-msix.ps1`, then extract only manifest-bound artifact/license paths into lifecycle payload roots.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/release/windows/package-msix.test.mjs tools/release/update.test.mjs tools/release/stage.test.mjs
git diff --check
git add tools/release/windows/msix-entry-path.ps1 tools/release/windows/verify-msix.ps1 tools/release/windows/package-msix.test.mjs tools/release/install-smoke.ps1 tools/release/update.test.mjs
git commit -m "fix: canonicalize MSIX lifecycle extraction"
```

---

### Task 4: Restore Linux modes after trusted artifact download

**Files:**
- Create: `tools/release/linux/runtime-mode-inventory.mjs`
- Create: `tools/release/linux/runtime-mode-inventory.test.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/release/install-smoke.sh`
- Modify: `tools/release/qualification.test.mjs`
- Modify: `tools/release/update.test.mjs`
- Modify: `docs/security.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Trusted artifact `code-oss-linux-x64` contains `runtime/` plus `code-oss-runtime-mode.json`.
- Inventory schema is closed: `{schemaVersion:1, platform:"linux", architecture:"x64", launcherRelativePath:"code-oss", launcherSha256, files:[{path,size,sha256,executable}]}` with sorted unique complete file coverage.
- CLI `runtime-mode-inventory.mjs restore --root <runtime> --inventory <json> --launcher-sha256 <digest>` validates provenance-coordinate input bytes, exact path set/digests/sizes, rejects links/special entries, restores deterministic `0755`/`0644`, rechecks modes, and calls the real Code-OSS validator.

- [ ] **Step 1: Add failing mode-round-trip and workflow tests**

On Linux, create a runtime fixture with executable `code-oss` and `chrome_crashpad_handler`, simulate artifact permission loss by chmodding all files `0644`, then require restore to make both executable while leaving data files non-executable. Add negative inventory tests for missing/extra paths, digest/size/mode drift, unsafe paths, aliases, links, and a launcher digest mismatch. Static workflow tests require inventory restore before either target/baseline staging and prohibit a launcher-only chmod workaround.

- [ ] **Step 2: Run focused tests and preserve RED**

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs
```

- [ ] **Step 3: Implement fail-closed restore and workflow contract**

Download into `.release/inputs/linux-code-oss`, set runtime root to its `runtime` child, require exactly one root inventory, and run the restore CLI with `CODE_OSS_SHA256` before exporting `CODE_OSS_RUNTIME_ROOT` or staging. Keep the pinned download action, `RELEASE_INPUT_RUN_ID`, root launcher digest, source epoch, event restrictions, and signing boundaries. In `install-smoke.sh`, validate package digests first, then `chmod u+x` both downloaded AppImages before the verifier executes either image.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs
git diff --check
git add tools/release/linux/runtime-mode-inventory.mjs tools/release/linux/runtime-mode-inventory.test.mjs .github/workflows/foundation.yml tools/release/install-smoke.sh tools/release/qualification.test.mjs tools/release/update.test.mjs docs/security.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "fix: preserve Linux runtime executable modes"
```

---

### Task 5: Prove the real runtime end to end and requalify the branch

**Files:**
- Create: `tools/release/real-runtime.e2e.test.mjs`
- Modify only if this test proves a defect in a Task 1-4 file.

**Interfaces:**
- Opt-in environment: `UNIT_TEST_IDE_REAL_CODE_OSS_ROOT` and `UNIT_TEST_IDE_REAL_CODE_OSS_SHA256`.
- The test creates temporary service/CMake/coverage inputs, stages the real Windows runtime, verifies the exact source/staged runtime file set and all 16 observed notices, packages an unsigned development MSIX through real `makeappx`, and verifies it. It never copies output into the repository and never logs the real root.

- [ ] **Step 1: Write the opt-in E2E test and observe pre-fix RED from the saved base if needed**

Require the two real paths with `@`, the parenthesized grammar file, exact launcher digest/identity, every runtime artifact digest, and every observed notice destination. Skip only when the two explicit environment inputs are absent; when present, any SDK/runtime failure is fatal.

- [ ] **Step 2: Run real E2E**

```powershell
$env:UNIT_TEST_IDE_REAL_CODE_OSS_ROOT='D:\project\VSCode-win32-x64'
$env:UNIT_TEST_IDE_REAL_CODE_OSS_SHA256='1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672'
node --test tools/release/real-runtime.e2e.test.mjs
```

Expected: pass with real staging plus real Windows SDK package verification and no local-path output.

- [ ] **Step 3: Run complete verification**

```powershell
$releaseTests = rg --files tools/release -g '*.test.mjs'
node --test $releaseTests
pnpm test
git diff --check
git status --short
```

Expected: zero failures; only explicit platform/capability skips when their environment input is absent. If the pre-existing Windows Go rename flake recurs, preserve evidence and use the already established branch-independent classification; do not add a plan-external fix.

- [ ] **Step 4: Commit the E2E gate**

```powershell
git add tools/release/real-runtime.e2e.test.mjs
git commit -m "test: verify the real Code-OSS release runtime"
```

Then request a fresh final branch review before invoking verification-before-completion and finishing-a-development-branch.

## Plan Self-Review

- Spec coverage: Tasks 1-4 map one-to-one to all five Important findings and the MSIX metadata Minor finding; Task 5 closes the missing real staging/package evidence.
- Placeholder scan: no deferred implementation placeholders remain; every task has concrete files, RED, implementation contract, GREEN command, and commit.
- Type consistency: `isPortableReleasePath`, canonical MSIX map functions, Linux inventory fields, and real E2E environment names are defined once and used consistently.
- Scope: trusted artifact production remains a later plan, but this consumer now states and validates its required `runtime/` plus mode-inventory artifact contract.
