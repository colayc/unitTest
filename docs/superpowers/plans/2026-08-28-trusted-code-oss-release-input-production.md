# Trusted Code-OSS Release Input Production Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build complete Windows and Linux x64 Code-OSS release inputs from the fixed upstream commit on standard GitHub-hosted runners, attest them with closed provenance, and consume them in one free unsigned `0.1.0` Phase 8 qualification run.

**Architecture:** A repository-owned closed source manifest fixes every upstream coordinate. Small Node.js ESM modules validate that manifest, create deterministic runtime inventories, create/validate provenance, and verify the producer run against GitHub API metadata. A manual four-job producer workflow builds Windows and Linux artifacts and independently attests them after artifact transport. `foundation.yml` adds one fail-closed trust job whose validated outputs, rather than raw dispatch inputs, feed both package jobs.

**Tech Stack:** Node.js 24.18.0 ESM and `node:test` for repository tooling, Code-OSS Node.js 20.14.0 and Yarn 1.22.22, PowerShell 7, Bash, GitHub Actions, GitHub REST API, Windows `windows-2022` with Visual Studio 2022, Ubuntu 24.04, pnpm 11.4.0.

**Spec:** `docs/superpowers/specs/2026-08-28-trusted-code-oss-release-input-production-design.md`

## Global Constraints

- Do not commit or upload `D:\project\VSCode-win32-x64`, `release-inputs/code-oss.exe`, any locally generated package, or any secret.
- The only producer trigger is input-free `workflow_dispatch`; a dispatch from any ref other than `refs/heads/master` must run and fail its authorization job.
- The producer repository, workflow, event, ref, Code-OSS repository/commit/version/toolchains/targets/outputs, and appimagetool repository/asset/name/size/digest are fixed values, not user inputs.
- Producer jobs run only on standard GitHub-hosted runners. They never use the administrator/WFP self-hosted runner.
- Pin every action in `.github/workflows/release-inputs.yml` to these reviewed full commits:
  - `actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803` (`v6`);
  - `actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38` (`v6`);
  - `actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` (`v7`);
  - `actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093` (`v4.3.0`).
- Every producer/runtime/provenance object is a closed schema. Reject extra fields, unsafe integers, malformed case, aliases, links, special files, mutable URLs, and coordinate drift.
- Every file path in inventories is portable, relative, slash-separated, case-insensitively unique, and strictly bytewise sorted using the existing `isPortableReleasePath()` contract.
- Artifact retention is exactly one day. Runtime uploads include hidden files so the artifact is the complete source tree, not a visible-file subset.
- Keep the existing independent post-download runtime, mode, appimagetool, staging, package-manifest, lifecycle, license, and qualification verification boundaries.
- Raw manual run IDs and SHA-256 values may be read only by `verify-release-input-run`; package jobs consume only that job's closed outputs.
- The first real run uses `release_version=0.1.0` and `release_signing_required=0`. It must not create a GitHub Release.
- Formal Windows signing and final license/legal review remain outstanding after this unsigned qualification run; do not mark all of Phase 8 complete.
- Use TDD for every implementation task. Preserve honest RED/GREEN evidence and commit each independently reviewable task before continuing.

---

### Task 1: Fix and validate every producer source coordinate

**Files:**
- Create: `tools/release/producer/source-manifest.json`
- Create: `tools/release/producer/source-manifest.mjs`
- Create: `tools/release/producer/source-manifest.test.mjs`

**Interfaces:**
- `validateSourceManifest(value)` returns a normalized frozen copy of the exact fixed manifest.
- `loadSourceManifest(path)` reads a real, non-linked JSON file and calls `validateSourceManifest()`.
- `validateProducerInvocation({ repository, event, ref, workflowRef })` accepts only `colayc/unitTest`, `workflow_dispatch`, `refs/heads/master`, and `colayc/unitTest/.github/workflows/release-inputs.yml@refs/heads/master`, then derives the fixed provenance path `.github/workflows/release-inputs.yml`.
- `verifyCodeOssCheckout({ root, actualCommit, manifest })` verifies the upstream `.nvmrc`, `package.json` version, `.yarnrc` Electron target `30.1.2`, and the two exact Gulp target/output names.
- CLI commands:
  - `export --manifest tools/release/producer/source-manifest.json --github-output .release/source-manifest.outputs` writes only fixed lowercase/snake-case output names and validated scalar values;
  - `authorize --repository colayc/unitTest --event workflow_dispatch --ref refs/heads/master --workflow-ref colayc/unitTest/.github/workflows/release-inputs.yml@refs/heads/master` demonstrates the only accepted invocation;
  - `verify-checkout --manifest tools/release/producer/source-manifest.json --root .producer/vscode --actual-commit b1c0a14de1414fcdaa400695b4db1c0799bc3124` performs the upstream checkout checks without printing the absolute root.
- Stable error codes are `RELEASE_PRODUCER_CONFIG_INVALID` and `RELEASE_PRODUCER_UNTRUSTED`.

- [ ] **Step 1: Add the exact closed manifest**

Write this byte-stable JSON, with a final newline:

```json
{
  "schemaVersion": 1,
  "codeOss": {
    "repository": "https://github.com/microsoft/vscode.git",
    "commit": "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    "version": "1.92.0",
    "nodeVersion": "20.14.0",
    "yarnVersion": "1.22.22",
    "windowsTarget": "vscode-win32-x64",
    "windowsOutput": "VSCode-win32-x64",
    "linuxTarget": "vscode-linux-x64",
    "linuxOutput": "VSCode-linux-x64"
  },
  "appimagetool": {
    "repository": "AppImage/appimagetool",
    "assetId": 324406882,
    "assetName": "appimagetool-x86_64.AppImage",
    "size": 15092216,
    "sha256": "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0"
  }
}
```

- [ ] **Step 2: Write failing manifest, invocation, checkout, and CLI tests**

Cover the exact accepted object and one-field mutations for every field. Reject extra top-level/nested keys, alternate repository spelling, HTTP or SSH URLs, floating refs, uppercase or short commits/digests, unsafe/non-integer asset values, changed targets/outputs, and local/absolute paths. For checkout fixtures, require:

```text
.nvmrc                         -> 20.14.0
package.json.version           -> 1.92.0
.yarnrc target                 -> 30.1.2
gulpfile.vscode.js             -> vscode-win32-x64 / vscode-linux-x64
actual git commit              -> b1c0a14de1414fcdaa400695b4db1c0799bc3124
```

Test all four invocation fields independently. Spawn each CLI and prove that stderr contains only the stable code/message, never the fixture root. Test `GITHUB_OUTPUT` values for newline/percent injection by passing hostile invocation values and requiring rejection before writing.

- [ ] **Step 3: Run the test and preserve RED**

```powershell
node --test tools/release/producer/source-manifest.test.mjs
```

Expected RED: the module and exported interfaces do not exist.

- [ ] **Step 4: Implement the smallest closed validator and CLI**

Use exact-key comparisons rather than permissive destructuring. Keep the canonical expected object in code so a syntactically valid but changed repository manifest is rejected. Parse `.yarnrc` as text and require one exact `target "30.1.2"` setting. Read only the named upstream files and compare fixed strings; do not execute upstream code during checkout verification.

Write GitHub outputs with a fixed allowlist:

```text
code_oss_repository
code_oss_commit
code_oss_version
code_oss_node_version
code_oss_yarn_version
code_oss_windows_target
code_oss_windows_output
code_oss_linux_target
code_oss_linux_output
appimagetool_repository
appimagetool_asset_id
appimagetool_asset_name
appimagetool_size
appimagetool_sha256
```

- [ ] **Step 5: Verify and commit**

```powershell
node --test tools/release/producer/source-manifest.test.mjs
git diff --check
git add tools/release/producer/source-manifest.json tools/release/producer/source-manifest.mjs tools/release/producer/source-manifest.test.mjs
git commit -m "feat: pin trusted release input sources"
```

---

### Task 2: Create deterministic complete runtime and Linux mode inventories

**Files:**
- Create: `tools/release/producer/runtime-inventory.mjs`
- Create: `tools/release/producer/runtime-inventory.test.mjs`
- Modify: `tools/release/linux/runtime-mode-inventory.mjs`
- Modify: `tools/release/linux/runtime-mode-inventory.test.mjs`

**Interfaces:**
- `createRuntimeModeInventory({ root, expectedLauncherSha256 })` creates the existing closed Linux schema from real source-tree modes.
- Extend `runtime-mode-inventory.mjs` with `create --root .producer/VSCode-linux-x64 --launcher-sha256 "$LINUX_LAUNCHER_SHA256" --out .release/producer/linux/code-oss-runtime-mode.json` while preserving the existing `restore` command unchanged.
- `createRuntimeInventory({ root, platform, expectedLauncherSha256, modeInventory })` returns a full closed inventory with sorted `{path,size,sha256,executable}` records.
- `summarizeRuntimeInventory(inventory)` returns only `{schemaVersion,platform,architecture,launcherRelativePath,launcherSha256,fileCount,totalBytes,treeDigest}`.
- CLI accepts `create`, fixed `--platform windows|linux`, `--root`, `--launcher-sha256`, optional Linux-only `--mode-inventory`, required `--out`, and required `--summary-out`; it writes canonical one-line JSON plus a final newline. Workflow calls use runner-local `.producer/VSCode-*-x64` roots and `.release/producer/*-inventory.json` outputs.
- Stable errors are `RELEASE_PRODUCER_OUTPUT_INVALID` plus existing `RELEASE_INPUT_*` errors from the runtime and mode validators.

- [ ] **Step 1: Write failing Linux mode-inventory creation tests**

On Linux, create a complete fixture with executable `code-oss` and `chrome_crashpad_handler`, non-executable metadata, nested files, and a valid launcher digest. Require the generated inventory to be strictly sorted, complete, and accepted by `validateRuntimeModeInventory()`. Round-trip it through permission loss plus `restoreRuntimeModes()` and require exact `0755`/`0644` restoration.

Add rejection tests for links, special files, unsafe paths, case aliases, a non-executable launcher, malformed output paths, and an unwritable output. Non-Linux creation must fail rather than invent executable state.

- [ ] **Step 2: Write failing cross-platform runtime-inventory tests**

Create equivalent Windows/Linux fixture content. Require:

- Windows records always bind `executable:false` regardless of host mode;
- Linux records take executable values only from an exact validated mode inventory;
- missing, extra, reordered, aliased, size-drifted, or digest-drifted Linux mode records fail;
- file content, path, decimal size, or executable-state changes alter the tree digest;
- timestamps and directory modes do not affect it;
- totals use safe integers and reject overflow;
- no absolute path appears in returned JSON or CLI output.

The tree digest must hash every sorted record exactly as UTF-8 bytes:

```js
hash.update(record.path);
hash.update("\0");
hash.update(String(record.size));
hash.update("\0");
hash.update(record.sha256);
hash.update("\0");
hash.update(record.executable ? "1" : "0");
hash.update("\0");
```

- [ ] **Step 3: Run focused tests and preserve RED**

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Expected RED: creation APIs/CLI and the producer runtime inventory do not exist.

- [ ] **Step 4: Implement one safe scanner and deterministic serializers**

Reuse `isPortableReleasePath()` and `validateCodeOssRuntime()`. Reject symlinks/reparse points, special entries, escapes, and case-insensitive aliases before hashing. Hash file streams rather than loading complete runtime files into memory. For Linux, canonical mode-inventory output is `JSON.stringify(value) + "\n"`; later provenance hashes those exact bytes.

The producer inventory may contain full file records only in runner-local temporary files. Artifact provenance exposes only the closed summary.

- [ ] **Step 5: Verify and commit**

```powershell
node --test tools/release/code-oss-runtime.test.mjs tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
git diff --check
git add tools/release/linux/runtime-mode-inventory.mjs tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.mjs tools/release/producer/runtime-inventory.test.mjs
git commit -m "feat: inventory trusted Code-OSS runtimes"
```

---

### Task 3: Create and independently validate closed release-input provenance

**Files:**
- Create: `tools/release/producer/provenance.mjs`
- Create: `tools/release/producer/provenance.test.mjs`

**Interfaces:**
- `createReleaseInputProvenance({ sourceManifest, producer, windows, linux, linuxModeInventorySha256, appimagetool })` returns a closed record.
- `validateReleaseInputProvenance(value)` returns a normalized frozen record only after exact-key and fixed-coordinate validation.
- CLI commands:
  - `create` requires the manifest, Windows/Linux summary, Linux mode-inventory, appimagetool, and output paths plus the five producer scalars `--producer-repository`, `--producer-workflow-path`, `--producer-source-commit`, `--producer-event`, and `--producer-ref`;
  - `validate --manifest tools/release/producer/source-manifest.json --provenance .release/attestation/release-input-provenance.json`.
- Stable error code: `RELEASE_PRODUCER_PROVENANCE_INVALID`.

**Closed provenance shape:**

```json
{
  "schemaVersion": 1,
  "producer": {
    "repository": "colayc/unitTest",
    "workflowPath": ".github/workflows/release-inputs.yml",
    "sourceCommit": "40 lowercase hex characters",
    "event": "workflow_dispatch",
    "ref": "refs/heads/master"
  },
  "codeOss": {
    "repository": "https://github.com/microsoft/vscode.git",
    "commit": "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    "version": "1.92.0",
    "nodeVersion": "20.14.0",
    "yarnVersion": "1.22.22"
  },
  "runtimes": {
    "windows": {
      "artifactName": "code-oss-windows-x64",
      "launcherRelativePath": "Code - OSS.exe",
      "launcherSha256": "64 lowercase hex characters",
      "fileCount": 1,
      "totalBytes": 1,
      "treeDigest": "64 lowercase hex characters"
    },
    "linux": {
      "artifactName": "code-oss-linux-x64",
      "launcherRelativePath": "code-oss",
      "launcherSha256": "64 lowercase hex characters",
      "fileCount": 1,
      "totalBytes": 1,
      "treeDigest": "64 lowercase hex characters",
      "modeInventorySha256": "64 lowercase hex characters"
    }
  },
  "appimagetool": {
    "repository": "AppImage/appimagetool",
    "artifactName": "appimagetool-linux-x64",
    "assetId": 324406882,
    "assetName": "appimagetool-x86_64.AppImage",
    "size": 15092216,
    "sha256": "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0"
  }
}
```

The `1` values above describe positive safe-integer constraints; real values come from the attested inventories.

- [ ] **Step 1: Write failing creation and closed-schema tests**

Start with a valid fixture, then mutate every scalar and add one extra key at every object level. Reject alternate artifact names, path-bearing launchers, zero/unsafe counts, malformed digests, source/tool coordinate drift, timestamp/environment/token/runner/url keys, and Windows/Linux summary swaps. Require deterministic JSON across repeated creation.

Test the CLI against real temporary summary/inventory/tool files. It must recompute the Linux mode-inventory file digest and appimagetool size/digest; passed metadata alone is insufficient.

- [ ] **Step 2: Run the test and preserve RED**

```powershell
node --test tools/release/producer/provenance.test.mjs
```

Expected RED: provenance module is absent.

- [ ] **Step 3: Implement exact-key validation and file-backed attestation**

Use the source manifest validator rather than copying its rules. Require the producer source commit separately from the upstream Code-OSS commit. Creation reads and hashes the exact mode-inventory/appimagetool files and compares their values to the supplied validated manifest and runtime summaries before writing provenance.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/release/producer/source-manifest.test.mjs tools/release/producer/runtime-inventory.test.mjs tools/release/producer/provenance.test.mjs
git diff --check
git add tools/release/producer/provenance.mjs tools/release/producer/provenance.test.mjs
git commit -m "feat: attest trusted release input provenance"
```

---

### Task 4: Validate GitHub producer-run identity before any consumer download

**Files:**
- Create: `tools/release/producer/trusted-run.mjs`
- Create: `tools/release/producer/trusted-run.test.mjs`

**Interfaces:**
- `validateProducerRunMetadata({ run, expectedRunId, expectedConsumerCommit })` extracts and verifies API fields without requiring the API object itself to be closed.
- `validateTrustedReleaseInputs({ run, provenance, expectedRunId, expectedConsumerCommit, expectedWindowsLauncherSha256, expectedLinuxLauncherSha256, expectedAppimagetoolSha256 })` returns only `{runId,windowsLauncherSha256,linuxLauncherSha256,appimagetoolSha256}`.
- CLI commands:
  - `validate-run` requires `--run-json`, `--run-id`, `--consumer-commit`, and `--github-output`, performs the pre-download API gate, and emits only `run_id`;
  - `validate-provenance` requires those same flags plus `--provenance`, `--windows-launcher-sha256`, `--linux-launcher-sha256`, and `--appimagetool-sha256`; it repeats the API gate, validates provenance, and writes all four closed outputs.
- Stable error code: `RELEASE_PRODUCER_UNTRUSTED` for run identity/state and `RELEASE_PRODUCER_PROVENANCE_INVALID` for provenance/manual-pin disagreement.

- [ ] **Step 1: Write failing pure-function tests**

Use a realistic GitHub REST run fixture with:

```js
{
  id: 123456789,
  path: ".github/workflows/release-inputs.yml",
  event: "workflow_dispatch",
  head_branch: "master",
  head_sha: "a".repeat(40),
  status: "completed",
  conclusion: "success",
  repository: { full_name: "colayc/unitTest" }
}
```

Independently reject a different/nonnumeric run ID, repository, workflow path, event, branch, head SHA, status, or conclusion. Include `pull_request`, `push`, queued, in-progress, cancelled, skipped, timed-out, neutral, action-required, stale, and failure cases. Preserve acceptance of unrelated extra GitHub API response fields because that external schema is larger than the trusted projection.

For the combined validator, mutate every provenance/API/manual digest binding. Prove uppercase manual digests fail and raw hostile values cannot create new `GITHUB_OUTPUT` keys.

- [ ] **Step 2: Run the test and preserve RED**

```powershell
node --test tools/release/producer/trusted-run.test.mjs
```

Expected RED: trusted-run module is absent.

- [ ] **Step 3: Implement two-stage fail-closed CLI validation**

Treat run IDs as digit strings, not JavaScript numbers. Compare the run JSON `id` through canonical decimal string conversion and reject unsafe numeric representations. The pre-download command must finish before the provenance artifact download step. The second command repeats all API checks so a later step cannot rely on stale shell state.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/release/producer/provenance.test.mjs tools/release/producer/trusted-run.test.mjs
git diff --check
git add tools/release/producer/trusted-run.mjs tools/release/producer/trusted-run.test.mjs
git commit -m "feat: verify trusted release input runs"
```

---

### Task 5: Build and attest both Code-OSS runtimes on hosted runners

**Files:**
- Create: `.github/workflows/release-inputs.yml`
- Create: `tools/release/producer/workflow-contract.test.mjs`
- Modify: `package.json`

**Workflow jobs:**
- `authorize`: always runs on `ubuntu-24.04`, checks out the repository, parses the manifest, and fails non-master/non-trusted dispatches.
- `build-windows`: needs `authorize`, runs on `windows-2022`, fails closed unless the hosted Visual Studio 2022 installation has the required C++ and Spectre components, then builds and validates `code-oss-windows-x64`.
- `build-linux`: needs `authorize`, runs on `ubuntu-24.04`, builds/validates `code-oss-linux-x64` and downloads/validates `appimagetool-linux-x64`.
- `attest`: needs both build jobs, runs on `ubuntu-24.04`, downloads all three current-run artifacts, revalidates transport results, and uploads `release-input-provenance`.

- [ ] **Step 1: Write failing workflow-contract tests first**

Require all of these static contracts:

- exactly one `workflow_dispatch` trigger with no `inputs`, no PR/push/tag/schedule/workflow-call trigger;
- top-level permissions contain only `actions: read` and `contents: read`;
- no `secrets.`, `self-hosted`, `unit-test-wfp`, mutable appimagetool URL, local drive path, or `release-inputs/code-oss.exe`;
- every `uses:` value is a 40-character commit and matches the four reviewed action commits in Global Constraints;
- authorization is a real required job, and every later job needs it directly or transitively;
- fixed standard runner labels and fixed source/Gulp/output coordinates;
- build validation and launcher check precede upload;
- Linux mode inventory creation/validation precedes upload;
- upload steps use exact artifact names, `retention-days: 1`, `if-no-files-found: error`, and `include-hidden-files: true` for runtime trees;
- `attest` needs both build jobs, downloads exactly three input artifacts, validates them after transport, compares recomputed summaries with job outputs, and uploads exactly one provenance file after validation;
- `attest` writes a path-free workflow summary containing the producer run ID, common source commit, and three validated digests only after provenance validation;
- no artifact upload step has `if: always()`.

Add `test:release-producer` to `package.json` with the five explicit test files and add it near the start of the root `test` script:

```json
"test:release-producer": "node --test tools/release/producer/source-manifest.test.mjs tools/release/producer/runtime-inventory.test.mjs tools/release/producer/provenance.test.mjs tools/release/producer/trusted-run.test.mjs tools/release/producer/workflow-contract.test.mjs"
```

- [ ] **Step 2: Run workflow tests and preserve RED**

```powershell
node --test tools/release/producer/workflow-contract.test.mjs
```

Expected RED: producer workflow is missing.

- [ ] **Step 3: Implement the authorization and fixed checkout pattern**

Use the pinned repository checkout action. In each build job, export the validated manifest to `GITHUB_OUTPUT`, then create a fresh runner-local `.producer/vscode` checkout using:

```bash
git init .producer/vscode
git -C .producer/vscode remote add origin https://github.com/microsoft/vscode.git
git -C .producer/vscode -c protocol.version=2 fetch --depth=1 origin b1c0a14de1414fcdaa400695b4db1c0799bc3124
git -C .producer/vscode checkout --detach FETCH_HEAD
```

Compare `git rev-parse HEAD` byte-for-byte and call `source-manifest.mjs verify-checkout`. Reject any sibling `VSCode-*` candidate output other than the one fixed for that platform.

- [ ] **Step 4: Implement the Windows producer in strict order**

Use pinned `setup-node` with `20.14.0`, then install exact Yarn `1.22.22`. Before `yarn install`, use the runner-installed `vswhere.exe` with version range `[17.0,18.0)` and a single query requiring both `Microsoft.VisualStudio.Component.VC.Tools.x86.x64` and `Microsoft.VisualStudio.Component.VC.Runtimes.x86.x64.Spectre`. Require one real 17.x installation and export both `GYP_MSVS_VERSION=2022` and `npm_config_msvs_version=2022`; a missing, ambiguous, linked, wrong-version, or incomplete installation must fail with the fixed producer preflight error. Do not install, downgrade, or fall back to another Visual Studio toolchain. Run from `.producer/vscode`:

```powershell
yarn install --frozen-lockfile
yarn gulp vscode-win32-x64
```

Any nonzero result is `RELEASE_PRODUCER_BUILD_FAILED`; do not retry with changed compiler flags or disabled Spectre mitigations. Require a sole real `.producer/VSCode-win32-x64` directory. Compute lowercase `Code - OSS.exe` SHA-256, call `code-oss-runtime.mjs`, run `Code - OSS.exe --version --user-data-dir .release/producer/windows/user-data` with a 30-second bound, and require version `1.92.0` in the closed output.

Create the Windows runtime inventory/summary, export `launcher_sha256`, `file_count`, `total_bytes`, and `tree_digest` as job outputs, copy the complete tree into `.release/producer/windows/code-oss-windows-x64`, and only then upload that directory as `code-oss-windows-x64` for one day.

- [ ] **Step 5: Implement the Linux producer and fixed appimagetool acquisition**

Install this closed Ubuntu package list without recommends:

```bash
sudo apt-get update
sudo apt-get install --no-install-recommends -y build-essential g++ libx11-dev libx11-xcb-dev libxkbfile-dev libsecret-1-dev pkg-config python-is-python3
```

Use Node `20.14.0`, Yarn `1.22.22`, `yarn install --frozen-lockfile`, and `yarn gulp vscode-linux-x64`. Validate the sole `.producer/VSCode-linux-x64`, run `timeout 30s .producer/VSCode-linux-x64/code-oss --version --user-data-dir .release/producer/linux/user-data`, and require version `1.92.0`.

Create/validate `code-oss-runtime-mode.json`, create the Linux runtime summary, and stage exactly:

```text
.release/producer/linux/code-oss-linux-x64/
  code-oss-runtime-mode.json
  runtime/
```

Download appimagetool from the release-asset API coordinate below using `Accept: application/octet-stream` and the workflow token; do not use a browser URL:

```text
https://api.github.com/repos/AppImage/appimagetool/releases/assets/324406882
```

Require exact name `appimagetool-x86_64.AppImage`, size `15092216`, and SHA-256 `a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0`. Stage/upload it as the sole file in `appimagetool-linux-x64`. Both Linux artifacts use one-day retention.

- [ ] **Step 6: Implement independent post-transport attestation**

Download all three artifacts with the pinned download action into separate fixed directories. Require exact root sets. On Ubuntu:

- validate the Linux mode inventory's closed structure and compare its digest plus launcher digest with the Linux build-job outputs before restoring any mode;
- restore the validated Linux modes, then validate and inventory the Windows and Linux runtimes without executing either launcher;
- recompute appimagetool name/size/digest and compare both runtime summaries, the mode-inventory digest, and the tool coordinates against all build-job outputs;
- only after every build-job output comparison succeeds, execute the Linux launcher version check with the fixed 30-second bound; never execute the Windows launcher on Ubuntu;
- create provenance with the exact producer scalars from `GITHUB_REPOSITORY`, `.github/workflows/release-inputs.yml`, `GITHUB_SHA`, `GITHUB_EVENT_NAME`, and `GITHUB_REF`, then call `provenance.mjs validate` against `tools/release/producer/source-manifest.json`;
- append only producer run ID, source commit, Windows launcher SHA-256, Linux launcher SHA-256, appimagetool SHA-256, and one-day retention to `GITHUB_STEP_SUMMARY` after validation;
- upload only `release-input-provenance.json` as `release-input-provenance` after every comparison succeeds.

- [ ] **Step 7: Run local contract verification and commit**

```powershell
pnpm test:release-producer
node --test tools/workspace-smoke/workspace-smoke.test.mjs
git diff --check
git add .github/workflows/release-inputs.yml tools/release/producer/workflow-contract.test.mjs package.json
git commit -m "feat: produce trusted Code-OSS release inputs"
```

Do not dispatch the workflow from the feature branch; its authorization is intentionally master-only.

---

### Task 6: Gate `foundation.yml` packaging on the trusted producer run

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/release/producer/workflow-contract.test.mjs`
- Modify: `tools/release/qualification.test.mjs`
- Modify: `tools/release/update.test.mjs`
- Modify: `tools/release/windows/package-msix.test.mjs`

**Job contract:**
- Add `verify-release-input-run` for manual or `v*` tag packaging.
- Outputs: `run_id`, `windows_launcher_sha256`, `linux_launcher_sha256`, `appimagetool_sha256` from the final trusted-run validation step.
- Both package jobs add this job to `needs` and bind their runtime/tool environment only to these outputs.

- [ ] **Step 1: Add failing consumer trust-flow assertions**

Slice the YAML by job and prove:

- the trust job runs for the same manual/tag predicate as package jobs;
- it checks out the packaging commit, selects raw dispatch values or repository variables only inside this job, requires digit/lowercase digest syntax, and queries `repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID`;
- it calls `validate-run` before the provenance artifact download;
- it downloads only `release-input-provenance` from the validated run ID with pinned `download-artifact`;
- it calls `validate-provenance` after download and exposes only the four closed outputs;
- package jobs need both platform verification jobs plus `verify-release-input-run`;
- package job source contains no `inputs.release_input_run_id`, no raw SHA input, no `vars.RELEASE_INPUT_RUN_ID`, and no `vars.RELEASE_CODE_OSS_*`/`vars.RELEASE_APPIMAGETOOL_*`;
- existing runtime/appimagetool post-download checks and Linux mode restoration remain ordered before staging.

- [ ] **Step 2: Run focused tests and preserve RED**

```powershell
node --test tools/release/producer/workflow-contract.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs
```

Expected RED: `verify-release-input-run` and the validated-output data flow are absent.

- [ ] **Step 3: Add the two-stage verification job**

The job checks out the exact packaging commit, installs Node `24.18.0` with the pinned setup action, and writes the GitHub REST result to `.release/producer-run.json` using `gh api` with `GH_TOKEN: ${{ github.token }}`. It then runs:

```bash
node tools/release/producer/trusted-run.mjs validate-run \
  --run-json .release/producer-run.json \
  --run-id "$RELEASE_INPUT_RUN_ID" \
  --consumer-commit "$GITHUB_SHA" \
  --github-output "$GITHUB_OUTPUT"
```

Only after this succeeds, download `release-input-provenance` from `steps.precheck.outputs.run_id`. Then run `validate-provenance` with the same API JSON/consumer SHA and all three raw expected digests. Map the final validation step's four outputs to job outputs.

- [ ] **Step 4: Rewire package jobs to validated outputs**

Add `verify-release-input-run` to both `needs` arrays and replace package environment bindings with:

```yaml
RELEASE_INPUT_RUN_ID: ${{ needs.verify-release-input-run.outputs.run_id }}
CODE_OSS_SHA256: ${{ needs.verify-release-input-run.outputs.windows_launcher_sha256 }}
```

and on Linux:

```yaml
RELEASE_INPUT_RUN_ID: ${{ needs.verify-release-input-run.outputs.run_id }}
CODE_OSS_SHA256: ${{ needs.verify-release-input-run.outputs.linux_launcher_sha256 }}
APPIMAGETOOL_SHA256: ${{ needs.verify-release-input-run.outputs.appimagetool_sha256 }}
```

Do not remove the existing `Require release input coordinates`, download, digest recomputation, mode restoration, runtime validation, or target/baseline staging checks.

- [ ] **Step 5: Verify and commit**

```powershell
pnpm test:release-producer
node --test tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs
git diff --check
git add .github/workflows/foundation.yml tools/release/producer/workflow-contract.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs
git commit -m "feat: gate packaging on trusted producer runs"
```

---

### Task 7: Document the one-day unsigned qualification procedure and remaining limits

**Files:**
- Modify: `README.md`
- Modify: `docs/security.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

- [ ] **Step 1: Add failing documentation assertions**

Require documentation to state all of these facts together:

- producer workflow name/path and fixed Code-OSS commit;
- standard GitHub-hosted Windows/Linux runners, no self-hosted administrator runner;
- fixed artifact names and one-day retention;
- provenance/run/API/post-transport/package-manifest trust chain;
- exact free unsigned first-run values `0.1.0` and `release_signing_required=0`;
- unsigned output is test evidence, not a GitHub Release or production release;
- local runtime and `release-inputs/code-oss.exe` are forbidden producer inputs;
- Windows signing and final license/legal review remain open Phase 8 work.

- [ ] **Step 2: Run the documentation test and preserve RED**

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs
```

Expected RED: the trusted producer/unsigned qualification contract is not yet documented.

- [ ] **Step 3: Update documentation without embedding an expiring run ID**

Document the exact workflow commands from Task 9, but keep real run IDs/digests in workflow evidence rather than committing them. Explain that one-day retention requires prompt inspection and that a rerun requires a fresh producer artifact set. Keep Phase 8 status truthful.

- [ ] **Step 4: Verify and commit**

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs
pnpm test:release-producer
git diff --check
git add README.md docs/security.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "docs: describe trusted unsigned release qualification"
```

---

### Task 8: Run full local verification and obtain implementation review

**Files:**
- Modify only files for defects proved by the commands below.

- [ ] **Step 1: Run all release tests on Windows**

```powershell
$releaseTests = rg --files tools/release -g '*.test.mjs'
node --test $releaseTests
```

Expected: zero failures. Linux-only filesystem/mode tests may explicitly skip on Windows; their non-platform pure-contract tests must pass.

- [ ] **Step 2: Run the complete repository gate**

```powershell
pnpm test
pnpm verify
git diff --check
git status --short
```

Expected: zero failures, generated files unchanged, and no uncommitted files. Never add `.pnpm-store/`, `.release/`, `.producer/`, local runtimes, downloaded artifacts, or packages.

- [ ] **Step 3: Review the full branch against the approved spec**

Use `superpowers:requesting-code-review` on the complete diff from `ec1b64f` through branch HEAD. Require explicit review of:

- hostile manifest/provenance/path/schema mutations;
- workflow authorization and action pins;
- artifact root completeness including hidden files;
- tree-digest executable binding and Linux restore round trip;
- pre-download API gate and package-job raw-input isolation;
- no secret/local runtime/publication regression.

- [ ] **Step 4: Fix only proved findings with TDD**

For every accepted finding, first add a focused regression test, observe RED, make the smallest fix, run focused plus adjacent tests, and commit one review-fix change. Do not weaken a check merely to make hosted execution pass.

- [ ] **Step 5: Re-run completion verification**

Use `superpowers:verification-before-completion` and repeat Steps 1-2 from a clean tree. Record exact pass/skip counts and current HEAD; do not claim completion from earlier output.

---

### Task 9: Merge, synchronize GitHub/Gitee, and run the real unsigned `0.1.0` qualification

**Files:**
- No repository file changes unless a hosted run proves a defect; any proved defect returns to TDD, review, merge, and a completely fresh producer run.

- [ ] **Step 1: Finish and publish the development branch**

Use `superpowers:finishing-a-development-branch`. Push the reviewed branch to both remotes without force:

```powershell
git push github HEAD
git push origin HEAD:refs/heads/codex/trusted-release-input-producer
gh pr create --repo colayc/unitTest --base master --head codex/trusted-release-input-producer --fill
```

Require PR checks and review to pass, then use the repository's linear rebase merge policy:

```powershell
gh pr merge --repo colayc/unitTest --rebase --delete-branch
```

Fetch the merged GitHub `master`, push that exact commit to Gitee `master`, and verify both remote hashes are identical:

```powershell
git fetch github master
git push origin refs/remotes/github/master:refs/heads/master
git ls-remote github refs/heads/master
git ls-remote origin refs/heads/master
```

- [ ] **Step 2: Dispatch the producer on `master`**

```powershell
gh workflow run release-inputs.yml --repo colayc/unitTest --ref master
$producer = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,headSha,status,conclusion,createdAt | ConvertFrom-Json | Select-Object -First 1
$producerRunId = [string]$producer.databaseId
gh run watch $producerRunId --repo colayc/unitTest --exit-status
```

Confirm the run head SHA equals both remote `master` hashes. Require `authorize`, `build-windows`, `build-linux`, and `attest` to conclude `success`; skips are not acceptance.

- [ ] **Step 3: Download and validate the producer provenance before expiry**

```powershell
$producerEvidence = ".release/evidence/producer-$producerRunId"
New-Item -ItemType Directory -Force -Path $producerEvidence | Out-Null
$producerRun = gh api "repos/colayc/unitTest/actions/runs/$producerRunId" | ConvertFrom-Json
$producerRunAttempt = [int64]$producerRun.run_attempt
$provenanceTransportName = "release-input-provenance-$producerRunAttempt"
$artifactPage = gh api "repos/colayc/unitTest/actions/runs/$producerRunId/artifacts?per_page=100" | ConvertFrom-Json
$provenanceMatches = @($artifactPage.artifacts | Where-Object { $_.name -ceq $provenanceTransportName -and $_.expired -eq $false })
if ($artifactPage.total_count -ne $artifactPage.artifacts.Count -or $artifactPage.total_count -gt 100 -or $provenanceMatches.Count -ne 1) { throw 'producer provenance artifact identity is ambiguous' }
$provenanceArtifactId = [string]$provenanceMatches[0].id
$provenanceZip = Join-Path $producerEvidence 'release-input-provenance.zip'
$downloadHeaders = @{
  Accept = 'application/vnd.github+json'
  Authorization = "Bearer $(gh auth token)"
  'X-GitHub-Api-Version' = '2022-11-28'
  'User-Agent' = 'colayc-unitTest-release-evidence'
}
Invoke-WebRequest -Uri "https://api.github.com/repos/colayc/unitTest/actions/artifacts/$provenanceArtifactId/zip" -Headers $downloadHeaders -OutFile $provenanceZip
Expand-Archive -LiteralPath $provenanceZip -DestinationPath $producerEvidence -Force
Remove-Item -LiteralPath $provenanceZip -Force
node tools/release/producer/provenance.mjs validate --manifest tools/release/producer/source-manifest.json --provenance "$producerEvidence/release-input-provenance.json"
$provenance = Get-Content -LiteralPath "$producerEvidence/release-input-provenance.json" -Raw | ConvertFrom-Json
$windowsSha = [string]$provenance.runtimes.windows.launcherSha256
$linuxSha = [string]$provenance.runtimes.linux.launcherSha256
$appimagetoolSha = [string]$provenance.appimagetool.sha256
```

Require provenance producer source commit to equal the producer run head SHA and all three fixed logical artifact names to match the design; the selected provenance transport name must carry the API-derived run attempt. The artifact page must be complete and bounded (all results returned in the 100-item page), exactly one non-expired attempt-qualified provenance artifact must match, and only its immutable artifact ID may be downloaded. `gh run download` has name/pattern selectors on this host, not an artifact-ID selector, so this authenticated REST ZIP endpoint must not fall back to name selection.

- [ ] **Step 4: Dispatch `foundation.yml` with the attested values**

```powershell
gh workflow run foundation.yml --repo colayc/unitTest --ref master `
  -f release_version=0.1.0 `
  -f release_signing_required=0 `
  -f release_input_run_id=$producerRunId `
  -f windows_code_oss_sha256=$windowsSha `
  -f linux_code_oss_sha256=$linuxSha `
  -f linux_appimagetool_sha256=$appimagetoolSha
$release = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,headSha,status,conclusion,createdAt | ConvertFrom-Json | Select-Object -First 1
$releaseRunId = [string]$release.databaseId
gh run watch $releaseRunId --repo colayc/unitTest --exit-status
```

Require the foundation run head SHA to equal the producer run head SHA. Require these jobs to conclude `success`:

```text
verify-windows
verify-linux
verify-release-input-run
package-windows
package-linux
install-smoke-windows
install-smoke-linux
release-qualification
```

- [ ] **Step 5: Inspect qualification and package evidence**

List artifacts through the GitHub API, require exactly one artifact whose name starts with `release-qualification-`, and download it:

```powershell
$qualificationNames = @(gh api "repos/colayc/unitTest/actions/runs/$releaseRunId/artifacts" --jq '.artifacts[] | select(.name | startswith("release-qualification-")) | .name')
if ($qualificationNames.Count -ne 1) { throw 'expected exactly one release qualification artifact' }
$qualificationName = $qualificationNames[0]
$qualificationEvidence = ".release/evidence/qualification-$releaseRunId"
gh run download $releaseRunId --repo colayc/unitTest --name $qualificationName --dir $qualificationEvidence
$qualification = Get-Content -LiteralPath "$qualificationEvidence/release-qualification.json" -Raw | ConvertFrom-Json
if ($qualification.qualificationOutcome.qualified -ne $true) { throw 'unsigned release qualification did not pass' }
if ($qualification.signatureOutcomes.windows -cne 'not-required') { throw 'unexpected unsigned signature outcome' }
```

Also download and inspect the Windows/Linux package artifacts, release manifests, license audits, and both install-smoke evidence artifacts before the one-day retention expires. Verify the packages are not published under GitHub Releases.

- [ ] **Step 6: Record the truthful milestone outcome**

Report producer run ID, foundation run ID, common source commit, exact job conclusions, `qualificationOutcome.qualified=true`, Windows signature outcome `not-required`, and matching GitHub/Gitee `master` hashes. State explicitly:

- trusted release-input production and free unsigned cross-platform qualification are complete;
- formal Windows signing remains open;
- final third-party license/legal approval remains open;
- therefore Phase 8 is materially advanced but not yet fully complete.
