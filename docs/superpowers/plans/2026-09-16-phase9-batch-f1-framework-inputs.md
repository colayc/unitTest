# Phase 9 Batch F1 Framework Inputs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立跨平台、fail-closed 的 CppUTest 4.0、Unity 2.6.1、CMock 2.7.0 输入锁，提交可复现 CMock 生成文件，并以 Windows MSVC、clang-cl 编译运行真实 CppUTest/CppUMock 与 Unity/CMock fixture。

**Architecture:** 将现有 `tools/framework-bundle` 拆成 manifest identity、archive preparation、CMock provenance、maintainer-only generation、offline check 和 native fixture verification 六个明确边界。普通验证只读取已提交文件且完全离线；只有显式 prepare 可以下载锁定归档，只有显式 update 命令可以在固定、禁网的 Ruby 容器中运行 CMock generator。F1 不生成 Phase 9 P4 platform report；F2 只消费 F1 的已提交 lock、fixture 和 provenance。

**Tech Stack:** Node.js 24.18.x ESM、pnpm 11.4.0、Go 1.26.x、CMake 4.3.4、CTest、CppUTest 4.0、Unity 2.6.1、CMock 2.7.0、Docker Linux containers、MSVC、clang-cl、Node test runner。

**Spec:** `docs/superpowers/specs/2026-09-16-phase9-batch-f1-framework-inputs-design.md` at commit `40c64d4`.

## Global Constraints

- Start from the local commit chain containing `40c64d4`; create the execution branch `codex/phase9-batch-f1-framework-inputs` at execution time.
- Preserve `.merge-stash-20260903/` and `docs/superpowers/plans/2026-09-15-phase9-batch-e-candidate-closeout.md` untouched; they are pre-existing untracked user files.
- Pin CppUTest `4.0`/`v4.0`/`b9b841c56c524a10ccd40e88c3acaf9d5ec751c2`, Unity `2.6.1`/`v2.6.1`/`cbcd08fa7de711053a3deec6339ee89cad5d2697`, and CMock `2.7.0`/`v2.7.0`/`6ea503340b1d3fdc0f2bcaf69273ba0160ec83af` exactly.
- Use only lowercase 64-character SHA-256 values in committed JSON.
- Run CMock only with `docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556` and `--platform linux/amd64`.
- Normal tests, `pnpm verify`, Service, Extension, foundation workflow, and product runtime must not run Docker, Ruby, Ceedling, or the CMock generator.
- Do not modify Phase 9 receipts, candidate baseline, gate matrix, P4 schema, signing, packaging, release publication, or product runtime behavior.
- Keep `releaseReady=false`; keep exactly `P8-DOCS-CLOSEOUT`, `P8-LEGAL-THIRD-PARTY`, and `P8-SIGN-WINDOWS` deferred.
- All child processes use argument arrays with `shell: false`, bounded timeouts, bounded output, controlled environment, and redacted errors.
- Use only these stable failure codes: `FRAMEWORK_MANIFEST_INVALID`, `FRAMEWORK_ARCHIVE_UNTRUSTED`, `FRAMEWORK_ARCHIVE_UNSAFE`, `FRAMEWORK_TREE_MISMATCH`, `FRAMEWORK_LICENSE_MISMATCH`, `FRAMEWORK_CACHE_INVALID`, `CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED`, `CMOCK_GENERATION_OUTPUT_INVALID`, `CMOCK_GENERATION_NONDETERMINISTIC`, `CMOCK_PROVENANCE_INVALID`, and `FRAMEWORK_FIXTURE_VALIDATION_FAILED`.
- Use TDD for every code task and commit after each independently passing deliverable.

---

## File Structure

### Lock and preparation

- `tools/framework-bundle/manifest.json` — reviewed schema v2 lock for three upstream source archives and fixture tools.
- `tools/framework-bundle/manifest.mjs` — closed manifest parser, fixed identities, safe path primitives, deterministic digest helpers, and stable framework errors.
- `tools/framework-bundle/manifest.test.mjs` — schema, canonical bytes, identity, path, license, and digest tests.
- `tools/framework-bundle/prepare.mjs` — explicit network bootstrap, safe archive inspection/extraction, immutable cache verification, and atomic source-tree publication.
- `tools/framework-bundle/prepare.test.mjs` — offline, injected-operation tests for download, extraction, race, cleanup, and reuse behavior.
- `tools/framework-bundle/licenses/dependencies.json` — non-release test-dependency license inventory for CppUTest, Unity, and CMock.
- `testdata/frameworks/failures/archive-entries.json` — small closed negative cases for portable path and archive entry validation.

### CMock provenance and generation

- `tools/framework-bundle/cmock-provenance.mjs` — closed provenance validator and deterministic aggregate-output digest.
- `tools/framework-bundle/cmock-provenance.test.mjs` — omission, substitution, extra-file, CRLF, path, and digest tests.
- `tools/framework-bundle/check.mjs` — ordinary offline repository check; never invokes network or a generator.
- `tools/framework-bundle/check.test.mjs` — repository fixture and no-runtime-generation contract tests.
- `tools/framework-bundle/update-cmock-fixture.mjs` — explicit maintainer-only double generation and rollback-safe publication.
- `tools/framework-bundle/update-cmock-fixture.test.mjs` — exact Docker arguments, deterministic double run, and no-mutation-on-failure tests.
- `testdata/frameworks/unity/cmock.yml` — fixed generator configuration.
- `testdata/frameworks/unity/include/Dependency.h` — the only CMock generator input header.
- `testdata/frameworks/unity/mocks/MockDependency.c` and `.h` — generated, committed output closed set.
- `testdata/frameworks/unity/mocks/cmock-generation.json` — deterministic provenance consumed later by F2.

### Real native fixtures

- `testdata/frameworks/cpputest/CMakeLists.txt` — real CppUTest/CppUMock build and helper registration.
- `testdata/frameworks/cpputest/.unit-test-ide/workspace.json` — future F2 Service discovery configuration.
- `testdata/frameworks/cpputest/fixture.json` — closed native scenario contract.
- `testdata/frameworks/cpputest/tests/framework_tests.cpp` — pass, assertion, ignore, three mock failures, crash, and timeout.
- `testdata/frameworks/unity/CMakeLists.txt` — real Unity/CMock build using committed generated mocks.
- `testdata/frameworks/unity/.unit-test-ide/workspace.json` — future F2 Service discovery configuration.
- `testdata/frameworks/unity/fixture.json` — closed native scenario contract.
- `testdata/frameworks/unity/tests/framework_tests.c` — pass, assertion, ignore, CMock failure, crash, and timeout.
- `tools/framework-bundle/verify-fixtures.mjs` — local compilation/execution verifier for Windows MSVC/clang-cl and Linux GCC.
- `tools/framework-bundle/verify-fixtures.test.mjs` — toolchain argument, timeout, classification, and no-report-publication tests.

### Existing consumers and repository wiring

- `tools/service-probe/src/linux-framework-inputs.ts` and `.test.ts` — accept schema v2, verify all three source trees, and expose the CMock root without changing product config.
- `tools/framework-bundle/consume.mjs` — Linux offline consumer updated to the immutable schema v2 prepared root and real fixtures.
- `apps/code-oss-extension/test/coverage-service-smoke-linux.test.ts` — derive the immutable prepared root from manifest bytes.
- `package.json` — add offline check, explicit update, and explicit native fixture verification scripts.
- `tools/workspace-smoke/workspace-smoke.test.mjs` — prove normal verification/workflows cannot invoke CMock generation.
- `tools/framework-bundle/README.md` and `docs/native-e2e.md` — document the trust boundary and exact maintainer commands.

---

### Task 1: Schema v2 Manifest and License Inventory

**Files:**
- Create: `tools/framework-bundle/manifest.mjs`
- Create: `tools/framework-bundle/manifest.test.mjs`
- Create: `tools/framework-bundle/licenses/dependencies.json`
- Modify: `tools/framework-bundle/manifest.json`
- Modify: `tools/framework-bundle/README.md`

**Interfaces:**
- Consumes: existing CMake helper SHA-256 and Unity runner generator identity.
- Produces: `readFrameworkManifest(path?) -> { manifest, bytes, manifestSha256 }`, `parseFrameworkManifestBytes(bytes) -> manifest`, `validateFrameworkManifest(value) -> manifest`, `frameworkManifestSha256(bytes) -> string`, `sha256File(path) -> string`, `directoryDigest(root) -> string`, `portableRelativePath(value) -> boolean`, `frameworkFailure()`, `FRAMEWORK_DEPENDENCY_IDS`, and `ADAPTER_FRAMEWORK_IDS`.

- [ ] **Step 1: Write failing manifest identity tests**

Create `manifest.test.mjs` with a canonical valid fixture and table-driven invalid variants:

```js
import assert from "node:assert/strict";
import test from "node:test";
import {
  FRAMEWORK_DEPENDENCY_IDS,
  parseFrameworkManifestBytes,
  validateFrameworkManifest,
} from "./manifest.mjs";

test("schema v2 is canonical and locks exactly three dependencies", () => {
  const value = manifestFixture();
  assert.deepEqual(validateFrameworkManifest(value), value);
  assert.deepEqual(FRAMEWORK_DEPENDENCY_IDS, ["cpputest", "unity", "cmock"]);
  assert.deepEqual(
    parseFrameworkManifestBytes(Buffer.from(`${JSON.stringify(value, null, 2)}\n`)),
    value,
  );
});

test("schema v2 rejects identity, license, order, case, and extra-field drift", () => {
  for (const value of invalidManifestFixtures()) {
    assert.throws(
      () => validateFrameworkManifest(value),
      (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID",
    );
  }
});

test("manifest bytes reject duplicate keys and non-canonical encoding", () => {
  assert.throws(
    () => parseFrameworkManifestBytes(Buffer.from('{"schemaVersion":2,"schemaVersion":2}\n')),
    (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID",
  );
  assert.throws(
    () => parseFrameworkManifestBytes(Buffer.from("{\"schemaVersion\":2}")),
    (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID",
  );
});
```

The fixture must use the exact identities and digests from the approved design, including license paths and license SHA-256 values.

- [ ] **Step 2: Run the new test and verify the missing-module failure**

Run:

```powershell
node --test tools/framework-bundle/manifest.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `tools/framework-bundle/manifest.mjs`.

- [ ] **Step 3: Implement the closed manifest model and stable errors**

Implement these exact exports in `manifest.mjs`:

```js
export const FRAMEWORK_DEPENDENCY_IDS = Object.freeze(["cpputest", "unity", "cmock"]);
export const ADAPTER_FRAMEWORK_IDS = Object.freeze(["cpputest", "unity"]);

export function frameworkFailure(code, message, cause) {
  const error = new Error(`${code}: ${message}`, cause === undefined ? undefined : { cause });
  error.code = code;
  return error;
}

export function parseFrameworkManifestBytes(bytes) {
  if (!Buffer.isBuffer(bytes) || bytes.length === 0 || bytes.length > 128 * 1024) {
    throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest byte length is invalid");
  }
  let text;
  let value;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    value = JSON.parse(text);
  } catch (error) {
    throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest is not strict UTF-8 JSON", error);
  }
  validateFrameworkManifest(value);
  if (text !== `${JSON.stringify(value, null, 2)}\n`) {
    throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest is not canonical JSON");
  }
  return value;
}
```

Add `readFrameworkManifest(path)`, raw-byte `frameworkManifestSha256(bytes)`, streaming `sha256File(path)`, and the existing sorted POSIX-path `directoryDigest(root)`. Validate exact keys, exact ordering, lower-case digests, safe relative paths, fixed HTTPS URLs, exact license objects, and exact `cmockGenerator` container identity. Never silently normalize submitted JSON.

- [ ] **Step 4: Replace `manifest.json` with the exact schema v2 lock**

Use this root shape and exact ordering:

```json
{
  "schemaVersion": 2,
  "platforms": ["linux-x64", "windows-x64"],
  "fixtureTools": {
    "cmakeHelper": {
      "path": "sdk/cmake/UnitTestIDE.cmake",
      "sha256": "101ba1a2cb15b54dfbdce49c5d92d9e6a32ffef35e038d4aaf96ae9f4746f4d3"
    },
    "unityRunnerGenerator": {
      "name": "unity-runner-generator",
      "schemaVersion": 1,
      "version": "1.0.0",
      "runnerProtocol": "utide.runner.v1"
    },
    "cmockGenerator": {
      "frameworkId": "cmock",
      "version": "2.7.0",
      "entrypoint": "lib/cmock.rb",
      "containerImage": "docker.io/library/ruby",
      "containerTag": "3.3.6-bookworm",
      "containerPlatform": "linux/amd64",
      "containerDigest": "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556",
      "generatedAtRuntime": false
    }
  },
  "frameworks": []
}
```

Populate `frameworks` with this exact array:

```json
[
  {
    "id": "cpputest",
    "version": "4.0",
    "tag": "v4.0",
    "revision": "b9b841c56c524a10ccd40e88c3acaf9d5ec751c2",
    "source": {
      "filename": "cpputest-4.0.tar.gz",
      "url": "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz",
      "sha256": "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
    },
    "license": {
      "spdx": "BSD-3-Clause",
      "path": "COPYING",
      "sha256": "d8fe282e4047197e1fbd6ef2527bde832a1514be6bd82fac7d1296ce184285c8"
    },
    "sourceDirectory": "cpputest-4.0",
    "treeSha256": "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
  },
  {
    "id": "unity",
    "version": "2.6.1",
    "tag": "v2.6.1",
    "revision": "cbcd08fa7de711053a3deec6339ee89cad5d2697",
    "source": {
      "filename": "Unity-2.6.1.tar.gz",
      "url": "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz",
      "sha256": "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292"
    },
    "license": {
      "spdx": "MIT",
      "path": "LICENSE.txt",
      "sha256": "907d9e859c6433703c0c183de3ddeaaf4baf3d517382f8f368b2c190fd2581d1"
    },
    "sourceDirectory": "Unity-2.6.1",
    "treeSha256": "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae"
  },
  {
    "id": "cmock",
    "version": "2.7.0",
    "tag": "v2.7.0",
    "revision": "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
    "source": {
      "filename": "CMock-2.7.0.tar.gz",
      "url": "https://github.com/ThrowTheSwitch/CMock/archive/refs/tags/v2.7.0.tar.gz",
      "sha256": "d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c"
    },
    "license": {
      "spdx": "MIT",
      "path": "LICENSE.txt",
      "sha256": "f19bba29498b9405a86ab5fdc6bc58654fffb197603834e6d1423d583649b35c"
    },
    "sourceDirectory": "CMock-2.7.0",
    "treeSha256": "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3"
  }
]
```

- [ ] **Step 5: Add the closed test-dependency license inventory**

Create `licenses/dependencies.json` with `schemaVersion: 1` and exactly three ordered entries. Each entry records `id`, `version`, `revision`, `spdx`, `archiveLicensePath`, `archiveLicenseSha256`, and the immutable GitHub blob URL at its pinned revision. Include CMock MIT explicitly and state in the README that this inventory is evidence input, not the deferred human legal approval.

- [ ] **Step 6: Run focused tests and validate canonical committed JSON**

Run:

```powershell
node --test tools/framework-bundle/manifest.test.mjs
node --input-type=module -e "import {readFrameworkManifest} from './tools/framework-bundle/manifest.mjs'; console.log((await readFrameworkManifest()).manifest.frameworks.map(({id}) => id).join(','));"
git diff --check
```

Expected: tests PASS; CLI prints `cpputest,unity,cmock`; no whitespace errors.

- [ ] **Step 7: Commit the schema v2 lock**

```powershell
git add -- tools/framework-bundle/manifest.json tools/framework-bundle/manifest.mjs tools/framework-bundle/manifest.test.mjs tools/framework-bundle/licenses/dependencies.json tools/framework-bundle/README.md
git commit -m "build: lock cross-platform framework inputs"
```

### Task 2: Cross-Platform Safe Preparation and Immutable Publication

**Files:**
- Modify: `tools/framework-bundle/prepare.mjs`
- Modify: `tools/framework-bundle/prepare.test.mjs`
- Create: `testdata/frameworks/failures/archive-entries.json`
- Modify: `tools/framework-bundle/README.md`

**Interfaces:**
- Consumes: `readFrameworkManifest()`, `sha256File()`, `directoryDigest()`, and stable errors from Task 1.
- Produces: `prepareFrameworkBundle(options) -> { root, manifest, manifestSha256, reused }`, `verifyPreparedFrameworkBundle(options)`, `verifyLockedArchive(cacheRoot, input)`, `validateArchiveEntries(entries, input)`, and a test-only `__testing` operations surface.

- [ ] **Step 1: Replace Linux-only tests with injected cross-platform failure tests**

Add tests that do not access the network and inject `download`, `inspectArchive`, and `extractArchive` operations. The core assertions are:

```js
assert.equal(result.root, join(runtimeRoot, "v2", result.manifestSha256));
assert.equal(result.reused, false);
assert.deepEqual(await readdir(runtimeRoot), ["v2"]);

await assert.rejects(
  prepareFixture({ entries: [{ path: "C:/escape", type: "file", size: 1 }] }),
  (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE",
);

await assert.rejects(
  prepareFixture({ licenseBytes: Buffer.from("substituted") }),
  (error) => error?.code === "FRAMEWORK_LICENSE_MISMATCH",
);
```

Cover absolute paths, backslashes, `..`, empty segments, Windows reserved names, symlink, hardlink, junction-like/reparse entries, device entries, duplicate paths, unexpected top-level roots, more than 8192 entries, depth over 32, and expanded bytes over 256 MiB. Load the small case table from `testdata/frameworks/failures/archive-entries.json`.

- [ ] **Step 2: Run preparation tests and verify current Linux-only assumptions fail**

Run:

```powershell
node --test tools/framework-bundle/prepare.test.mjs
```

Expected: FAIL because schema v1/Linux-only validation and destructive output replacement do not meet the new tests.

- [ ] **Step 3: Implement controlled system-tar discovery and archive inspection**

Use `%SystemRoot%\System32\tar.exe` on Windows and `/usr/bin/tar` then `/bin/tar` on Linux. Run `-tf` and `-tvf` with `LANG=C`, `LC_ALL=C`, a 60-second timeout, and an 8 MiB output cap. Convert the type marker to only `file` or `directory`; reject `l`, `h`, and every other marker before extraction. Pair the exact path listing with a conservatively parsed non-negative file size and reject any unparseable listing.

The exported validator accepts only:

```js
{ path: "CMock-2.7.0/lib/cmock.rb", type: "file", size: 12345 }
{ path: "CMock-2.7.0/lib/", type: "directory", size: 0 }
```

No archive path may normalize to another name or escape its one expected `sourceDirectory`.

- [ ] **Step 4: Implement bounded download and immutable archive cache**

Use a maximum response body of 64 MiB, five-minute timeout, and a maximum of five explicit redirects. The initial URL must exactly match the lock. Redirects may remain on `github.com` or end on `release-assets.githubusercontent.com`/`codeload.github.com`; all other schemes and hosts fail with `FRAMEWORK_ARCHIVE_UNTRUSTED`.

Write to a same-directory random partial file using `wx`, fsync it, verify SHA-256, then publish without overwriting an existing cache entry. If another process wins the race, re-verify the winner and remove only this invocation's partial file.

- [ ] **Step 5: Implement safe extraction, post-extraction audit, and license/tree checks**

Extract all three archives into one fresh staging directory. After extraction, recursively reject reparse/symbolic links and non-file/non-directory entries, require the exact three top-level roots, verify marker files (`CMakeLists.txt`, `src/unity.c`, `lib/cmock.rb`), verify license regular files and exact license digests, then verify all three directory tree digests.

Write `manifest.resolved.json` as canonical JSON containing `schemaVersion`, `manifestSha256`, `platforms`, `fixtureTools`, and the ordered resolved framework identities. Write `READY` exactly as `framework-bundle-v2\n` only after every verification succeeds.

- [ ] **Step 6: Publish by manifest digest without deleting a trusted target**

Use:

```text
.superpowers/runtime/framework-bundle/v2/$manifestSha256/
```

Attempt a same-parent rename from staging. If the target already exists or a race creates it, call `verifyPreparedFrameworkBundle()` and reuse it only if every byte identity still matches. Never call recursive remove on the immutable target. On failure, remove only the random staging and partial paths created by this invocation.

- [ ] **Step 7: Run cross-platform preparation tests**

```powershell
node --test tools/framework-bundle/manifest.test.mjs tools/framework-bundle/prepare.test.mjs
git diff --check
```

Expected: all tests PASS, including reuse, race, tamper, extraction limits, cleanup, and Windows path cases.

- [ ] **Step 8: Commit safe preparation**

```powershell
git add -- tools/framework-bundle/prepare.mjs tools/framework-bundle/prepare.test.mjs tools/framework-bundle/README.md testdata/frameworks/failures/archive-entries.json
git commit -m "build: harden framework bundle preparation"
```

### Task 3: Offline CMock Provenance and Repository Check

**Files:**
- Create: `tools/framework-bundle/cmock-provenance.mjs`
- Create: `tools/framework-bundle/cmock-provenance.test.mjs`
- Create: `tools/framework-bundle/check.mjs`
- Create: `tools/framework-bundle/check.test.mjs`

**Interfaces:**
- Consumes: schema v2 manifest and digest/path helpers from Task 1.
- Produces: `aggregateOutputDigest(files)`, `validateCMockGeneration(value, expected)`, `readCMockGeneration(path, expected)`, and `checkFrameworkBundle(options) -> { manifestSha256, cMockProvenanceSha256 }`.

- [ ] **Step 1: Write failing closed-provenance tests**

Use a temporary repository fixture with `cmock.yml`, `include/Dependency.h`, two generated files, and canonical provenance. Assert this exact semantic shape:

```js
{
  schemaVersion: 1,
  cmock: { version: "2.7.0", tag: "v2.7.0", revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af" },
  generator: {
    entrypoint: "lib/cmock.rb",
    version: "2.7.0",
    containerImage: "docker.io/library/ruby",
    containerPlatform: "linux/amd64",
    containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556"
  },
  configuration: { path: "testdata/frameworks/unity/cmock.yml", sha256: digest },
  input: { path: "testdata/frameworks/unity/include/Dependency.h", sha256: digest },
  outputs: [
    { path: "MockDependency.c", sha256: digest },
    { path: "MockDependency.h", sha256: digest }
  ],
  outputSha256: digest,
  frameworkManifestSha256: digest,
  generatedAtRuntime: false
}
```

Add one mutation per test for missing field, extra field, reordered/duplicate output, wrong revision, image substitution, input substitution, output substitution, CRLF, absolute path text, timestamp-like generated banner, non-UTF-8, symlink, and unknown file in `mocks/`.

- [ ] **Step 2: Run the provenance test and verify the missing-module failure**

```powershell
node --test tools/framework-bundle/cmock-provenance.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND`.

- [ ] **Step 3: Implement deterministic provenance validation**

`aggregateOutputDigest()` must sort by output path and hash this unambiguous byte sequence:

```text
f:${relativePath}\0${rawFileBytes}
```

Require LF-only UTF-8 generated output, exactly `MockDependency.c` then `MockDependency.h`, no absolute Windows/POSIX path token, no `Generated on`/ISO timestamp banner, and no filesystem alias. Compute `manifestSha256` for F2 as SHA-256 of the complete canonical `cmock-generation.json`; never store a self-hash inside the file.

- [ ] **Step 4: Write failing ordinary-check tests**

Assert that `checkFrameworkBundle()`:

- validates manifest and license inventory;
- validates `cmock.yml`, input, output set, per-file digests, aggregate digest, and provenance;
- optionally verifies an existing prepared root but succeeds when the prepared/cache roots are absent;
- never calls injected `fetch`, `execFile`, `docker`, or generator seams;
- reports `CMOCK_PROVENANCE_INVALID` or `FRAMEWORK_CACHE_INVALID` without an absolute local path.

- [ ] **Step 5: Implement `check.mjs` as a strictly offline CLI**

The CLI takes no arguments and resolves only repository-owned fixed paths. It prints one small JSON line with exactly the keys `frameworkBundle`, `manifestSha256`, and `cMockProvenanceSha256`; the first value is `verified` and both digest values match `^[0-9a-f]{64}$`.

The library accepts path overrides only for unit tests. It must not import `update-cmock-fixture.mjs`, call `prepareFrameworkBundle()`, inspect Docker, or download a missing cache.

- [ ] **Step 6: Run focused offline tests**

```powershell
node --test tools/framework-bundle/cmock-provenance.test.mjs tools/framework-bundle/check.test.mjs
git diff --check
```

Expected: all synthetic provenance and offline-boundary tests PASS. Do not run the real CLI until Task 4 creates committed outputs.

- [ ] **Step 7: Commit offline provenance checking**

```powershell
git add -- tools/framework-bundle/cmock-provenance.mjs tools/framework-bundle/cmock-provenance.test.mjs tools/framework-bundle/check.mjs tools/framework-bundle/check.test.mjs
git commit -m "test: verify committed cmock provenance"
```

### Task 4: Maintainer-Only Deterministic CMock Generation

**Files:**
- Create: `tools/framework-bundle/update-cmock-fixture.mjs`
- Create: `tools/framework-bundle/update-cmock-fixture.test.mjs`
- Create: `testdata/frameworks/unity/cmock.yml`
- Create: `testdata/frameworks/unity/include/Dependency.h`
- Generate: `testdata/frameworks/unity/mocks/MockDependency.c`
- Generate: `testdata/frameworks/unity/mocks/MockDependency.h`
- Generate: `testdata/frameworks/unity/mocks/cmock-generation.json`

**Interfaces:**
- Consumes: prepared CMock schema v2 source root and Task 3 provenance functions.
- Produces: `buildDockerArguments(input)`, `updateCMockFixture(options) -> provenance`, and the committed closed output set.

- [ ] **Step 1: Add the fixed CMock input header and configuration**

Create `include/Dependency.h` with one stable callable dependency:

```c
#ifndef UNIT_TEST_IDE_PHASE9_DEPENDENCY_H
#define UNIT_TEST_IDE_PHASE9_DEPENDENCY_H

int Dependency_Read(int channel);

#endif
```

Create `cmock.yml` with these exact canonical LF bytes:

```yaml
---
:cmock:
  :mock_path: /out
  :mock_prefix: Mock
  :mock_suffix: ''
  :plugins: []
  :fail_on_unexpected_calls: true
  :when_no_prototypes: :error
  :verbosity: 0
```

The only generator input is `/fixture/include/Dependency.h`; the expected output set is exactly `MockDependency.c` and `MockDependency.h`.

- [ ] **Step 2: Write exact Docker-contract and rollback tests**

Test `buildDockerArguments()` for this ordered security subset:

```js
for (const pair of [
  ["--platform", "linux/amd64"],
  ["--network", "none"],
  ["--cap-drop", "ALL"],
  ["--security-opt", "no-new-privileges"],
  ["--pids-limit", "64"],
]) assertArgumentPair(arguments_, pair);
assert.ok(arguments_.includes("--read-only"));
assert.ok(arguments_.includes("docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556"));
```

Assert that `/cmock` and `/fixture` mounts are `readonly`, `/out` is the only writable mount, and the post-image command is exactly:

```text
ruby /cmock/lib/cmock.rb -o/fixture/cmock.yml /fixture/include/Dependency.h
```

Inject a fake generation runner for these cases: identical A/B output publishes; one-byte B drift fails `CMOCK_GENERATION_NONDETERMINISTIC`; missing/extra output fails; thrown process error fails; every failure leaves the pre-existing `mocks/` bytes unchanged.

- [ ] **Step 3: Run tests and verify the missing implementation**

```powershell
node --test tools/framework-bundle/update-cmock-fixture.test.mjs
```

Expected: FAIL with the update module absent.

- [ ] **Step 4: Implement isolated double generation**

`updateCMockFixture()` must:

1. read and validate the fixed manifest;
2. locate and verify the immutable prepared CMock tree;
3. create separate `run-a` and `run-b` temporary output directories;
4. execute `docker.exe`/`docker` with the exact argument array and a five-minute timeout;
5. validate each run's closed set and safe bytes;
6. compare corresponding raw bytes;
7. compute all provenance fields from actual bytes;
8. validate the staged provenance with Task 3;
9. publish a complete staged `mocks/` directory with rollback if final rename fails;
10. delete only this invocation's staging/backup after a verified success.

Do not accept environment overrides for image, platform, source root, input header, or output names.

- [ ] **Step 5: Run unit tests without Docker**

```powershell
node --test tools/framework-bundle/update-cmock-fixture.test.mjs tools/framework-bundle/cmock-provenance.test.mjs
```

Expected: PASS using only injected fake generation operations.

- [ ] **Step 6: Prepare the real locked source bundle**

```powershell
pnpm prepare:framework-bundle
```

Expected: downloads or reuses exactly three digest-keyed archives and publishes one verified schema v2 prepared root. Record no cache/runtime file in Git.

- [ ] **Step 7: Run the real maintainer generation twice through the script**

```powershell
node tools/framework-bundle/update-cmock-fixture.mjs
node tools/framework-bundle/check.mjs
```

Expected: the update command performs its internal A/B double run, commits no runtime metadata, and the ordinary offline check reports `frameworkBundle=verified`. Inspect `git diff -- testdata/frameworks/unity/mocks` and require only the two generated files plus canonical provenance.

- [ ] **Step 8: Commit the generator boundary and generated outputs**

```powershell
git add -- tools/framework-bundle/update-cmock-fixture.mjs tools/framework-bundle/update-cmock-fixture.test.mjs testdata/frameworks/unity/cmock.yml testdata/frameworks/unity/include/Dependency.h testdata/frameworks/unity/mocks
git commit -m "test: add reproducible cmock fixture outputs"
```

### Task 5: Real CppUTest and CppUMock Fixture

**Files:**
- Create: `tools/framework-bundle/verify-fixtures.mjs`
- Create: `tools/framework-bundle/verify-fixtures.test.mjs`
- Create: `testdata/frameworks/cpputest/CMakeLists.txt`
- Create: `testdata/frameworks/cpputest/.unit-test-ide/workspace.json`
- Create: `testdata/frameworks/cpputest/fixture.json`
- Create: `testdata/frameworks/cpputest/tests/framework_tests.cpp`

**Interfaces:**
- Consumes: prepared CppUTest root, CMake helper, CMake executable, and explicit toolchain family.
- Produces: `verifyFrameworkFixtures(options)` where `options` contains `repositoryRoot`, `cmake`, `generator`, `toolchains`, `frameworks`, `platform`, optional `frameworkInputs: { cpputestRoot, unityRoot, cmockRoot, helper }`, and injectable `execFile`; also produces a CppUTest fixture with eight stable native scenarios. Output is an stdout summary only, never a P4 report.

- [ ] **Step 1: Write verifier argument and classification tests**

Define supported F1 CLI arguments exactly:

```text
--cmake $cmake
--generator $generator
--toolchains msvc,clang-cl
--frameworks cpputest,unity
```

The public library options are:

```js
{
  repositoryRoot,
  cmake,
  generator,
  toolchains: ["msvc", "clang-cl"],
  frameworks: ["cpputest"],
  execFile,
  platform: "win32"
}
```

Tests must prove that MSVC configures `Visual Studio 17 2022` with `-A x64`, clang-cl configures `Ninja` with both `CMAKE_C_COMPILER=clang-cl` and `CMAKE_CXX_COMPILER=clang-cl`, and Linux GCC configures `Ninja` with `CMAKE_C_COMPILER=gcc` and `CMAKE_CXX_COMPILER=g++`. Every configure/build has a two-minute timeout, normal scenarios have a ten-second timeout, timeout scenarios are killed after one second, and no file named `framework-report.json` is written. Reject GCC on Windows and MSVC/clang-cl on Linux.

- [ ] **Step 2: Run verifier tests and observe the missing implementation**

```powershell
node --test tools/framework-bundle/verify-fixtures.test.mjs
```

Expected: FAIL with the verifier module absent.

- [ ] **Step 3: Add the closed CppUTest fixture contract**

Create `fixture.json` with canonical fields `schemaVersion`, `framework`, `ctestName`, and ordered `scenarios`. Use this exact scenario order and expectations:

```text
pass                         passed       exit 0
assertion-failure            failed       nonzero + assertion output
skip                         skipped      exit 0 + IGNORE output
mock-missing-call            mock-failure nonzero + mock output
mock-unexpected-call         mock-failure nonzero + mock output
mock-parameter-mismatch      mock-failure nonzero + mock output
crash                        crash        abnormal/nonzero exit
timeout                      timeout      verifier deadline
```

Use CppUTest group `Phase9` and test names `Pass`, `AssertionFailure`, `Skipped`, `MockMissingCall`, `MockUnexpectedCall`, `MockParameterMismatch`, `Crash`, and `Timeout`.

- [ ] **Step 4: Implement the real CppUTest/CppUMock source**

Use real framework macros and install `MockSupportPlugin` before `CommandLineTestRunner::RunAllTests`. The important bodies are:

```cpp
TEST(Phase9, Pass) { CHECK_EQUAL(4, 2 + 2); }
TEST(Phase9, AssertionFailure) { CHECK_EQUAL(1, 2); }
IGNORE_TEST(Phase9, Skipped) { FAIL("ignored test executed"); }
TEST(Phase9, MockMissingCall) { mock().expectOneCall("read"); }
TEST(Phase9, MockUnexpectedCall) { mock().actualCall("unexpected"); }
TEST(Phase9, MockParameterMismatch) {
  mock().expectOneCall("read").withIntParameter("channel", 1);
  mock().actualCall("read").withIntParameter("channel", 2);
}
TEST(Phase9, Crash) { std::abort(); }
TEST(Phase9, Timeout) { std::this_thread::sleep_for(std::chrono::seconds(30)); }
```

The fixture main installs `MockSupportPlugin`, executes the runner, and removes the plugin before returning.

- [ ] **Step 5: Add CMake and workspace configuration**

`CMakeLists.txt` requires absolute `UNIT_TEST_IDE_CPPUTEST_ROOT` and `UNIT_TEST_IDE_HELPER`, adds the pinned upstream with `add_subdirectory("${UNIT_TEST_IDE_CPPUTEST_ROOT}" "${CMAKE_BINARY_DIR}/upstream-cpputest" EXCLUDE_FROM_ALL)`, links `CppUTest` and `CppUTestExt`, forces runtime output to `${CMAKE_BINARY_DIR}/bin` for every configuration, includes the helper, and registers CTest name `cpputest.framework` through `unit_test_ide_add_cpputest`.

The workspace JSON uses version `2`, project ID `cpputest-fixture`, source directory `.`, fallback `Debug`, and one container mapping `cpputest.framework` to framework `cpputest`.

- [ ] **Step 6: Implement CppUTest compilation and execution in the verifier**

For each toolchain, configure into `.superpowers/runtime/framework-fixtures/$toolchain/cpputest`, build `--config Debug`, and invoke the executable with `-g Phase9 -n` plus the exact `Pass`, `AssertionFailure`, `Skipped`, `MockMissingCall`, `MockUnexpectedCall`, `MockParameterMismatch`, `Crash`, or `Timeout` name from `fixture.json`. Classify each result, require the expected exit/output/deadline behavior, and return only an in-memory summary:

```js
{
  framework: "cpputest",
  toolchain: "msvc",
  scenarios: [{ id: "pass", outcome: "passed" }]
}
```

Do not include candidate commit, timestamps, artifact digests, or P4 schema fields in F1.

- [ ] **Step 7: Run tests and one available local toolchain smoke**

```powershell
node --test tools/framework-bundle/verify-fixtures.test.mjs
$cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
$generator = (Resolve-Path build\unity-runner-generator.exe).Path
node tools/framework-bundle/verify-fixtures.mjs --cmake $cmake --generator $generator --toolchains msvc --frameworks cpputest
```

Before the native command, build the generator if absent:

```powershell
go -C apps/test-service build -trimpath -o ../../build/unity-runner-generator.exe ./cmd/unity-runner-generator
```

Expected: eight CppUTest scenarios are observed with the contracted outcomes.

- [ ] **Step 8: Commit the CppUTest fixture and verifier foundation**

```powershell
git add -- tools/framework-bundle/verify-fixtures.mjs tools/framework-bundle/verify-fixtures.test.mjs testdata/frameworks/cpputest
git commit -m "test: add real cpputest fixture"
```

### Task 6: Real Unity and CMock Fixture

**Files:**
- Create: `testdata/frameworks/unity/CMakeLists.txt`
- Create: `testdata/frameworks/unity/.unit-test-ide/workspace.json`
- Create: `testdata/frameworks/unity/fixture.json`
- Create: `testdata/frameworks/unity/tests/framework_tests.c`
- Modify: `tools/framework-bundle/verify-fixtures.mjs`
- Modify: `tools/framework-bundle/verify-fixtures.test.mjs`

**Interfaces:**
- Consumes: pinned Unity/CMock roots, committed `MockDependency.c/.h`, provenance, helper, and product Unity runner generator.
- Produces: six stable Unity runner scenarios and generator-free native compilation on MSVC/clang-cl.

- [ ] **Step 1: Add failing Unity runner protocol tests to the verifier**

Use an injected fake executable to prove the verifier first runs:

```text
--utide-protocol utide.runner.v1 --utide-mode list --utide-result $listResultPath
```

It then resolves exact identities from the list records and runs each case with:

```text
--utide-protocol utide.runner.v1 --utide-mode run --utide-case $caseIdentity --utide-result $caseResultPath
```

Tests must cover passed, failed, skipped, mock expectation failure, crash before a complete result, and timeout. Reject duplicate/missing records, result-file escape, unknown status, malformed JSONL, and a result identity different from the requested identity.

- [ ] **Step 2: Run the focused verifier test and confirm Unity support is absent**

```powershell
node --test --test-name-pattern "Unity|runner protocol" tools/framework-bundle/verify-fixtures.test.mjs
```

Expected: FAIL because the verifier only supports CppUTest.

- [ ] **Step 3: Add the Unity fixture contract and real test source**

Create the ordered contract:

```text
pass                 test_pass                       passed
assertion-failure    test_assertion_failure          failed
skip                 test_skipped                    skipped
mock-failure         test_cmock_expectation_failure  mock-failure
crash                test_crash                      crash
timeout              test_timeout                    timeout
```

Implement `setUp()` with `MockDependency_Init()` and `tearDown()` with `MockDependency_Verify()` then `MockDependency_Destroy()`. Use:

```c
void test_pass(void) { TEST_ASSERT_EQUAL_INT(4, 2 + 2); }
void test_assertion_failure(void) { TEST_ASSERT_EQUAL_INT(1, 2); }
void test_skipped(void) { TEST_IGNORE_MESSAGE("phase9 skip fixture"); }
void test_cmock_expectation_failure(void) {
  Dependency_Read_ExpectAndReturn(7, 42);
  (void)Dependency_Read(8);
}
void test_crash(void) { abort(); }
```

Implement the timeout with `Sleep(30000)` on Windows and `sleep(30)` on POSIX so it does not busy-loop.

- [ ] **Step 4: Add generator-free Unity/CMock CMake wiring**

Require absolute `UNIT_TEST_IDE_UNITY_ROOT`, `UNIT_TEST_IDE_CMOCK_ROOT`, `UNIT_TEST_IDE_HELPER`, and `UTIDE_UNITY_RUNNER_GENERATOR`. Build `unity.c`, `cmock.c`, the committed `MockDependency.c`, and `tests/framework_tests.c`; include only pinned source/include roots and the fixture include/mocks directories. Never add a custom command for Ruby/CMock generation.

Register `unity.framework` through `unit_test_ide_add_unity_test(TEST_SOURCES tests/framework_tests.c)`. Force one stable runtime output directory for single- and multi-config generators.

- [ ] **Step 5: Implement Unity list/run verification**

After configure/build, read the helper-generated manifest from `.unit-test-ide/3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988/manifest.json`, compare its cases with `fixture.json`, then run the exact identities. A completed record controls pass/fail/skip; a process termination without a valid complete record is crash; the verifier deadline is timeout. For `mock-failure`, require failed status and CMock/mismatch evidence in stdout/stderr while retaining the structured record as status authority.

- [ ] **Step 6: Prove generation is absent from build/runtime**

In the verifier test, scan the generated build invocation list and fixture CMake source. Assert no argument or text matches:

```text
ruby
ceedling
lib/cmock.rb
update-cmock-fixture
docker run
```

Then temporarily hide the Docker executable from the verifier's controlled environment in the test and require the Unity compilation/execution plan to remain unchanged.

- [ ] **Step 7: Run the Unity fixture with one available toolchain**

```powershell
$cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
$generator = (Resolve-Path build\unity-runner-generator.exe).Path
node tools/framework-bundle/verify-fixtures.mjs --cmake $cmake --generator $generator --toolchains msvc --frameworks unity
node tools/framework-bundle/check.mjs
```

Expected: all six native outcomes match the contract and the offline provenance check remains successful.

- [ ] **Step 8: Commit the Unity/CMock fixture**

```powershell
git add -- testdata/frameworks/unity/CMakeLists.txt testdata/frameworks/unity/.unit-test-ide/workspace.json testdata/frameworks/unity/fixture.json testdata/frameworks/unity/tests/framework_tests.c tools/framework-bundle/verify-fixtures.mjs tools/framework-bundle/verify-fixtures.test.mjs
git commit -m "test: add real unity cmock fixture"
```

### Task 7: Migrate Existing Linux Consumers to Schema v2

**Files:**
- Modify: `tools/service-probe/src/linux-framework-inputs.ts`
- Modify: `tools/service-probe/src/linux-framework-inputs.test.ts`
- Modify: `tools/framework-bundle/consume.mjs`
- Modify: `apps/code-oss-extension/test/coverage-service-smoke-linux.test.ts`

**Interfaces:**
- Consumes: schema v2 manifest, immutable prepared root, and F1 real fixtures.
- Produces: Linux boundary environment with `UNIT_TEST_IDE_TEST_CPPUTEST_ROOT`, `UNIT_TEST_IDE_TEST_UNITY_ROOT`, `UNIT_TEST_IDE_TEST_CMOCK_ROOT`, helper path, and generator path; existing product configuration remains unchanged.

- [ ] **Step 1: Update Linux boundary tests first**

Change the TypeScript manifest fixture to schema v2 and add CMock. Define:

```ts
export type FrameworkDependencyID = "cpputest" | "unity" | "cmock";
export type AdapterFrameworkID = "cpputest" | "unity";
```

Require the boundary result to keep `frameworks: ["cpputest", "unity"]` while its environment includes `UNIT_TEST_IDE_TEST_CMOCK_ROOT`. Add failures for missing CMock, wrong license digest, wrong revision, wrong prepared manifest digest, and CMock tree substitution.

- [ ] **Step 2: Run service-probe tests and confirm schema v1 code fails**

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/linux-framework-inputs.test.js
```

Expected: FAIL on schema/identity assertions.

- [ ] **Step 3: Upgrade the Linux boundary without widening product inputs**

Validate all schema v2 fields and all three archives/source trees. Include CMock in the boundary identity digest and add only the test-only environment key. Continue verifying the helper digest and generator version. Do not serialize the CMock path into `.unit-test-ide/workspace.json`, Protocol, Service configuration, or user-visible product settings.

- [ ] **Step 4: Derive the prepared root from raw manifest bytes in every consumer**

Replace `.superpowers/runtime/framework-bundle/linux-x64` with:

```ts
const manifestBytes = await readFile(join(root, "tools/framework-bundle/manifest.json"));
const manifestSha256 = createHash("sha256").update(manifestBytes).digest("hex");
const sourceRoot = join(root, ".superpowers/runtime/framework-bundle/v2", manifestSha256);
```

Apply this to `consume.mjs` and the Linux extension smoke. Keep the archive cache root unchanged.

- [ ] **Step 5: Make Linux `consume.mjs` exercise the real F1 fixtures**

After the boundary validates, call `verifyFrameworkFixtures()` with platform `linux`, toolchain `gcc`, and frameworks `cpputest,unity`. Pass the exact prepared roots, helper, and generator from the boundary. Keep the command inside the existing offline namespace; it must not call prepare or update.

- [ ] **Step 6: Run TypeScript and Linux-consumer contract tests**

```powershell
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/linux-framework-inputs.test.js tools/service-probe/dist/test-framework-fixture.test.js
pnpm --filter code-oss-extension build
node --test tools/framework-bundle/prepare.test.mjs tools/framework-bundle/verify-fixtures.test.mjs
```

Expected: all tests PASS on Windows; Linux-native execution remains guarded to the Linux foundation job.

- [ ] **Step 7: Commit schema v2 consumer migration**

```powershell
git add -- tools/service-probe/src/linux-framework-inputs.ts tools/service-probe/src/linux-framework-inputs.test.ts tools/framework-bundle/consume.mjs apps/code-oss-extension/test/coverage-service-smoke-linux.test.ts
git commit -m "test: consume schema v2 framework inputs"
```

### Task 8: Repository Wiring, Dual-Toolchain Acceptance, and Scope Audit

**Files:**
- Modify: `package.json`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `tools/framework-bundle/README.md`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- Consumes: all F1 tools and fixtures.
- Produces: ordinary offline verification wiring, explicit maintainer commands, Windows MSVC/clang-cl acceptance result, and an unchanged Phase 9 evidence state.

- [ ] **Step 1: Add failing repository-boundary smoke tests**

Assert these root scripts exactly:

```json
{
  "check:framework-bundle": "node tools/framework-bundle/check.mjs",
  "prepare:framework-bundle": "node tools/framework-bundle/prepare.mjs",
  "update:cmock-fixture": "node tools/framework-bundle/update-cmock-fixture.mjs",
  "verify:framework-fixtures": "node tools/framework-bundle/verify-fixtures.mjs"
}
```

Assert `test:framework-bundle` includes every new `*.test.mjs`, `verify` includes `check:framework-bundle`, and neither `test` nor `verify` contains `update:cmock-fixture`, `docker`, `ruby`, or `ceedling`.

Read `.github/workflows/*.yml`, `apps/`, `sdk/`, and `tools/service-probe/src/`; reject executable references to `lib/cmock.rb`, `update-cmock-fixture`, `ruby`, or `docker run`. Allow the generator identity only as inert data in `tools/framework-bundle/manifest.json` and generation code/tests in `tools/framework-bundle/update-cmock-fixture*`.

- [ ] **Step 2: Run smoke tests and observe missing wiring**

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs
```

Expected: FAIL because the new scripts and generation-boundary assertions are not wired.

- [ ] **Step 3: Wire ordinary offline checks and explicit maintainer commands**

Set `test:framework-bundle` to run:

```text
manifest.test.mjs
prepare.test.mjs
cmock-provenance.test.mjs
check.test.mjs
update-cmock-fixture.test.mjs
verify-fixtures.test.mjs
tools/linux-offline/run.test.mjs
```

Place `pnpm check:framework-bundle` after generated-file checks and before `pnpm build` in `verify`. Keep prepare, update, and native fixture compilation outside `verify` because they require network, Docker, or native toolchains.

- [ ] **Step 4: Document the exact trust boundary and operator commands**

In the framework README and `docs/native-e2e.md`, document:

```powershell
pnpm prepare:cmake-bundle
pnpm prepare:framework-bundle
pnpm update:cmock-fixture
pnpm check:framework-bundle
$cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
$generator = (Resolve-Path build\unity-runner-generator.exe).Path
pnpm verify:framework-fixtures -- --cmake $cmake --generator $generator --toolchains msvc,clang-cl --frameworks cpputest,unity
```

State explicitly that update is maintainer-only, generated files are committed, ordinary CI is generator-free, the license inventory is not human legal approval, and F2 owns hosted four-toolchain evidence.

- [ ] **Step 5: Run all focused F1 tests**

```powershell
pnpm test:framework-bundle
pnpm --filter @unit-test-ide/service-probe test
node --test tools/workspace-smoke/workspace-smoke.test.mjs
pnpm check:framework-bundle
```

Expected: all commands PASS without invoking Docker, Ruby, Ceedling, or network.

- [ ] **Step 6: Run real Windows MSVC and clang-cl acceptance**

```powershell
go -C apps/test-service build -trimpath -o ../../build/unity-runner-generator.exe ./cmd/unity-runner-generator
pnpm prepare:framework-bundle
$cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
$generator = (Resolve-Path build\unity-runner-generator.exe).Path
pnpm verify:framework-fixtures -- --cmake $cmake --generator $generator --toolchains msvc,clang-cl --frameworks cpputest,unity
```

Expected: both toolchains build both fixtures; CppUTest reports all eight contracted outcomes and Unity reports all six. A missing required compiler is a failure, not a skip.

- [ ] **Step 7: Run the complete repository verification**

```powershell
pnpm check:phase9-gates
pnpm verify
git diff --check
```

Expected: all checks PASS. If an unrelated environment-only E2E prerequisite is absent, capture the exact existing documented skip separately; do not describe a skip as PASS.

- [ ] **Step 8: Prove Phase 9 evidence and release state did not change**

```powershell
git diff --exit-code 40c64d4 -- docs/superpowers/evidence/phase9 tools/phase9/p4-report.mjs tools/phase9/p4-report.schema.json .github/workflows
node --input-type=module -e "import {readFile} from 'node:fs/promises'; const m=JSON.parse(await readFile('docs/superpowers/evidence/phase9/gate-matrix.json','utf8')); if(m.releaseReady!==false) process.exit(1); const deferred=m.gates.filter(g=>g.status==='DEFERRED').map(g=>g.id).sort(); if(JSON.stringify(deferred)!==JSON.stringify(['P8-DOCS-CLOSEOUT','P8-LEGAL-THIRD-PARTY','P8-SIGN-WINDOWS'])) process.exit(1); if(m.gates.filter(g=>g.id==='P4-CPPUTEST-CPPUMOCK'||g.id==='P4-UNITY-CMOCK').some(g=>g.status==='PASS')) process.exit(1);"
```

Expected: no evidence/P4/workflow diff; `releaseReady=false`; exactly three approved deferrals; P4 framework gates are not falsely promoted.

- [ ] **Step 9: Commit repository wiring and documentation**

```powershell
git add -- package.json tools/workspace-smoke/workspace-smoke.test.mjs tools/framework-bundle/README.md docs/native-e2e.md
git commit -m "test: wire phase9 framework fixture checks"
```

- [ ] **Step 10: Perform final status and secret audit**

```powershell
git status --short --branch
git log --oneline 40c64d4..HEAD
rg -n -i "BEGIN .*PRIVATE KEY|github_pat_|ghp_|password\s*[:=]|token\s*[:=]" tools/framework-bundle testdata/frameworks docs/native-e2e.md
```

Expected: only the two pre-existing untracked user paths remain; the F1 commit series is visible; secret scan has no credential values. Do not push, create a PR, merge, sign, or publish without a new explicit authorization.
