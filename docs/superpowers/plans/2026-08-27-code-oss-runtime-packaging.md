# Complete Code-OSS Runtime Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unsafe single-file Code-OSS release input with a validated, digest-pinned complete runtime directory on Windows and Linux, and make every packaging and lifecycle verifier consume the new fixed launcher paths.

**Architecture:** A focused `code-oss-runtime.mjs` module validates platform layout, product identity, path safety, launcher permissions, and launcher digest before staging. `stage.mjs` copies the entire validated tree to `app/code-oss-runtime/`, duplicates all runtime license notices into the closed license tree, and binds every runtime file in the release manifest. MSIX, AppImage, lifecycle smoke, and GitHub Actions use the platform-fixed launcher inside that tree.

**Tech Stack:** Node.js 24.18.0 ESM and `node:test`, pnpm 11.4.0, PowerShell 7, Bash, Windows SDK MSIX tools, AppImage tooling, GitHub Actions YAML.

## Global Constraints

- Windows runtime root launcher is exactly `Code - OSS.exe`; Linux runtime root launcher is exactly `code-oss`.
- Both runtime roots contain `resources/app/product.json` and `resources/app/package.json` as real regular files.
- `product.json` declares `nameShort: "Code - OSS"`, `applicationName: "code-oss"`, and `licenseName: "MIT"`.
- Runtime roots and descendants reject symbolic links, junctions/reparse points, special files, unsafe relative paths, and case-insensitive path aliases.
- Linux launcher has an executable mode bit.
- Launcher SHA-256 is lowercase hexadecimal, is required by staging, and is checked both before and after copy.
- Staged runtime layout is `app/code-oss-runtime/<complete upstream runtime>` with no renamed upstream entries.
- Code-OSS `LICENSE*`, `NOTICE*`, and `COPYING*` files are copied into `licenses/code-oss/` and remain closed by the existing license audit.
- No runtime component downloads dependencies at launch or install time.
- Old `--code-oss <file>` staging input is rejected rather than silently supported.
- Every behavior change follows red-green-refactor and is committed only after its focused tests pass.

---

### Task 1: Add the complete runtime validator

**Files:**
- Create: `tools/release/code-oss-runtime.mjs`
- Create: `tools/release/code-oss-runtime.test.mjs`

**Interfaces:**
- Consumes: `{ root: string, platform: "windows" | "linux", expectedLauncherSha256: string }`.
- Produces: `validateCodeOssRuntime(input) -> Promise<{ root: string, canonicalRoot: string, launcherPath: string, launcherRelativePath: string, launcherSha256: string, productIdentity: { applicationName: "code-oss", licenseName: "MIT", nameShort: "Code - OSS" } }>`.
- Produces CLI flags `--platform`, `--root`, and `--launcher-sha256`; successful output is closed path-free JSON.

- [ ] **Step 1: Write failing validator tests for valid Windows and Linux runtimes**

Add fixture helpers that create exact root launchers, `resources/app/product.json`, `resources/app/package.json`, `locales/en-US.pak`, and one runtime dependency. Add tests equivalent to:

```js
test("validateCodeOssRuntime accepts a complete digest-pinned Windows runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const result = await validateCodeOssRuntime({
      root: fixture.runtimeRoot,
      platform: "windows",
      expectedLauncherSha256: fixture.launcherSha256,
    });
    assert.equal(result.launcherRelativePath, "Code - OSS.exe");
    assert.equal(result.launcherSha256, fixture.launcherSha256);
    assert.deepEqual(result.productIdentity, {
      applicationName: "code-oss",
      licenseName: "MIT",
      nameShort: "Code - OSS",
    });
  });
});

const linuxOnly = process.platform === "linux" ? test : test.skip;

linuxOnly("validateCodeOssRuntime accepts an executable Linux runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "linux");
    const result = await validateCodeOssRuntime({
      root: fixture.runtimeRoot,
      platform: "linux",
      expectedLauncherSha256: fixture.launcherSha256,
    });
    assert.equal(result.launcherRelativePath, "code-oss");
  });
});
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test tools/release/code-oss-runtime.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `code-oss-runtime.mjs`.

- [ ] **Step 3: Implement the minimal validator and closed CLI**

Implement exact platform layout constants:

```js
const layouts = {
  windows: { launcherRelativePath: "Code - OSS.exe", requireExecutable: false },
  linux: { launcherRelativePath: "code-oss", requireExecutable: true },
};

const identity = {
  applicationName: "code-oss",
  licenseName: "MIT",
  nameShort: "Code - OSS",
};
```

Validate the root with `lstat` and `realpath`; walk the complete tree using `readdir(currentPath, { withFileTypes: true })` followed by `entries.sort((left, right) => left.name.localeCompare(right.name, "en"))`; reject non-files/non-directories and `info.isSymbolicLink()`. Require every `realpath` to remain beneath `canonicalRoot`. Reuse the repository's portable-relative-path rules, maintain both exact and lowercase relative-path sets, reject an alias before descending, and require the launcher plus both metadata files to occur in the exact-case set before opening them. Parse `resources/app/product.json` as a plain JSON object and compare the three identity fields exactly. Hash the launcher incrementally with `createReadStream` and `createHash("sha256")`, require `/^[0-9a-f]{64}$/u`, and return only canonical internal values. Create errors through one helper that assigns `error.code` and prefixes `error.message` with the same stable category; messages name logical inputs but never include absolute host paths.

The CLI prints:

```json
{"schemaVersion":1,"platform":"windows","launcherRelativePath":"Code - OSS.exe","launcherSha256":"1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672","applicationName":"code-oss","nameShort":"Code - OSS","licenseName":"MIT"}
```

with values derived from validation; it never prints `root`, `canonicalRoot`, or `launcherPath`.

- [ ] **Step 4: Add failing security and CLI tests, then complete GREEN**

Add one `node:test` case for each named behavior. Each test creates a valid fixture first, applies exactly one mutation, and calls `validateCodeOssRuntime()` unless the name explicitly targets the CLI:

- `validator rejects a launcher file in place of the runtime root`: pass `fixture.launcherPath` as `root` and assert `RELEASE_INPUT_INVALID`.
- `validator rejects a missing fixed platform launcher`: remove the launcher and assert `RELEASE_INPUT_MISSING`.
- `validator rejects wrong-case required paths`: rename only the Windows launcher to `code - oss.exe`, then rename only `resources/app/product.json` to `Product.json`, and assert each exact-path check fails even on a case-insensitive host.
- `validator rejects missing product and package metadata`: use two subtests, each removing exactly one required file and asserting `RELEASE_INPUT_MISSING`.
- `validator rejects a non-Code-OSS product identity`: change only `applicationName` to `code` and assert `RELEASE_INPUT_INVALID`.
- `validator rejects malformed and mismatched launcher digests`: use one malformed digest and one different valid digest, asserting the syntax and mismatch categories respectively.
- `validator rejects a non-executable Linux launcher`: set mode `0o644` and assert `RELEASE_INPUT_INVALID`.
- `validator rejects a descendant symbolic link`: replace `runtime.dat` with a link to a file outside the root and assert `RELEASE_INPUT_INVALID` before any outside bytes are read.
- `validator rejects a root link and a descendant directory junction`: create each redirect where the host supports it, assert `RELEASE_INPUT_INVALID`, and use an explicit capability skip only when the host cannot create that redirect type.
- `validator rejects special entries and non-portable entry names`: on POSIX create a FIFO plus filenames containing `:` and `\\`, assert `RELEASE_INPUT_INVALID` in separate subtests, and capability-skip only the cases the host filesystem cannot represent.
- `validator rejects case-insensitive runtime path aliases`: create `locales/en-US.pak` and `locales/EN-us.pak` where supported and assert `RELEASE_INPUT_INVALID`.
- `validator CLI emits closed path-free JSON`: spawn the CLI with a valid fixture, assert exit 0, assert the exact seven output keys, and assert serialized output does not contain the temporary root.
- `validator CLI failure is closed and path-free`: pass a missing root, assert nonzero exit, assert `RELEASE_INPUT_MISSING`, and assert stderr does not contain the temporary root.
- `validator catches launcher mutation on a second validation`: validate once, mutate only the launcher, validate again with the original digest, and assert `RELEASE_INPUT_DIGEST_MISMATCH`; staging will use this same second validation after copy.

For each new behavior, first run the focused test and observe the expected assertion failure, then add only the minimal validation needed. Use error objects whose message starts with `RELEASE_INPUT_MISSING:`, `RELEASE_INPUT_INVALID:`, or `RELEASE_INPUT_DIGEST_MISMATCH:` as specified by the design.

Run after every green increment:

```powershell
node --test tools/release/code-oss-runtime.test.mjs
```

Expected final result: all applicable validator tests pass. POSIX executable/special-file cases run on Linux; junction cases run on Windows; only a case that the current host filesystem cannot construct may report an explicit capability skip.

- [ ] **Step 5: Commit the validator slice**

```powershell
git add tools/release/code-oss-runtime.mjs tools/release/code-oss-runtime.test.mjs
git commit -m "feat: validate complete Code-OSS runtimes"
```

---

### Task 2: Stage the complete runtime and its licenses

**Files:**
- Modify: `tools/release/stage.mjs`
- Modify: `tools/release/stage.test.mjs`
- Modify: `tools/release/license-audit.test.mjs`
- Modify: `README.md`

**Interfaces:**
- Consumes Task 1 `validateCodeOssRuntime()`.
- Replaces programmatic inputs `codeOss` with `codeOssRoot` and `codeOssSha256`.
- Replaces CLI `--code-oss` with `--code-oss-root` and `--code-oss-sha256`.
- Produces the fixed staging prefix `app/code-oss-runtime/` and runtime licenses under `licenses/code-oss/`.

- [ ] **Step 1: Rewrite the staging fixture and add a failing full-tree test**

Change `createReleaseFixture(root, platform = "windows")` to select the exact platform launcher and create this Windows default:

```text
inputs/code-oss/Code - OSS.exe
inputs/code-oss/resources/app/product.json
inputs/code-oss/resources/app/package.json
inputs/code-oss/resources/app/LICENSE.txt
inputs/code-oss/LICENSES.chromium.html
inputs/code-oss/locales/en-US.pak
inputs/code-oss/runtime.dll
```

Return `codeOssRoot` and `codeOssSha256`. Update the main staging assertion to require:

```js
for (const relativePath of [
  "app/code-oss-runtime/Code - OSS.exe",
  "app/code-oss-runtime/resources/app/product.json",
  "app/code-oss-runtime/locales/en-US.pak",
  "app/code-oss-runtime/runtime.dll",
  "licenses/code-oss/resources/app/LICENSE.txt",
  "licenses/code-oss/LICENSES.chromium.html",
]) {
  assert.ok((await readFile(join(result.stagingRoot, ...relativePath.split("/")))).length > 0);
}
```

Assert every `app/code-oss-runtime/` file appears once in `manifest.artifacts`, has `kind === "runtime"`, and has the exact staged digest.

Add `linuxOnly("stageRelease preserves the complete executable Linux runtime", ...)` using `createReleaseFixture(root, "linux")`. Assert `app/code-oss-runtime/code-oss` and a sibling resource are present, the old `app/code-oss` path is absent, and the launcher artifact is `kind === "runtime"` with `executable === true`.

- [ ] **Step 2: Run the focused staging test and verify RED**

```powershell
node --test tools/release/stage.test.mjs
```

Expected: FAIL because `stageRelease()` still requires `codeOss` and does not create `app/code-oss-runtime/`.

- [ ] **Step 3: Implement full-tree staging and post-copy digest verification**

Update required keys and CLI map to:

```js
const requiredKeys = [
  "architecture", "cmakeRoot", "codeOssRoot", "codeOssSha256",
  "coverageRoot", "outRoot", "platform", "service", "version",
];

const cliFlagMap = new Map([
  ["--platform", "platform"],
  ["--architecture", "architecture"],
  ["--version", "version"],
  ["--code-oss-root", "codeOssRoot"],
  ["--code-oss-sha256", "codeOssSha256"],
  ["--service", "service"],
  ["--cmake-root", "cmakeRoot"],
  ["--coverage-root", "coverageRoot"],
  ["--out", "outRoot"],
]);
```

Call `validateCodeOssRuntime()` before creating the output root. Map its result to `const runtimeSource = { path: validated.root, canonicalPath: validated.canonicalRoot }`, then copy that tree with the existing sorted `copyTree()` into `app/code-oss-runtime`. Call the same validator a second time on the staged runtime root with the original platform and digest before copying licenses or creating the manifest:

```js
const stagedRuntimeRoot = join(temporaryRoot, "app", "code-oss-runtime");
const stagedRuntime = await validateRuntime({
  root: stagedRuntimeRoot,
  platform: normalized.platform,
  expectedLauncherSha256: normalized.codeOssSha256,
});
```

Classify every path beginning with `app/code-oss-runtime/` as `runtime`; mark real executable-mode files, the fixed platform launcher, and the existing service launcher executable. Update `usage()` to show both new flags and remove the old flag.

Make the validation dependency explicit without changing normal callers:

```js
export async function stageRelease(input, {
  validateRuntime = validateCodeOssRuntime,
} = {}) {
  // Pre-copy and post-copy validation both call validateRuntime().
}
```

The default path remains `stageRelease(input)`; the optional dependency exists so the post-copy failure path can be tested deterministically.

Widen `isLicenseRelativePath()` so plural upstream notice names are collected:

```js
return relativePath.split("/").includes("licenses")
  || /^(?:licen[cs]e(?:s)?|notice(?:s)?|copying)(?:[._-].+)?$/iu.test(basename);
```

Call the existing license copier for the runtime root:

```js
const stagedRuntimeSource = {
  path: stagedRuntime.root,
  canonicalPath: stagedRuntime.canonicalRoot,
};
const codeOssLicenses = await copyLicenseSet(stagedRuntimeSource, temporaryRoot, "code-oss");
const licenses = [...codeOssLicenses, ...cmakeLicenses, ...coverageLicenses]
  .sort((left, right) => left.localeCompare(right, "en"));
```

- [ ] **Step 4: Add failure, CLI, reproducibility, and license tests**

Add and observe RED before each minimal implementation adjustment:

- `stageRelease rejects the removed single-file codeOss input`: destructure the valid fixture to omit `codeOssRoot`/`codeOssSha256`, add `codeOss: fixture.launcherPath`, and assert `RELEASE_INPUT_MISSING` names `codeOssRoot` without accepting the old key.
- `stage CLI rejects --code-oss and requires both runtime-root flags`: spawn once with `--code-oss runtime.exe` and assert `unknown stage flag`, then spawn valid arguments with each new flag omitted in separate subtests and assert the named required input.
- `stageRelease publishes no root after a post-copy launcher digest mismatch`: pass a `validateRuntime` dependency that delegates to the real validator, but immediately before its second call mutates only the staged launcher; assert two validation calls, `RELEASE_INPUT_DIGEST_MISMATCH`, absence of the final root, and absence of `.stage-*` siblings.
- `identical complete runtimes produce byte-identical normalized staging trees`: stage the same complete fixture into two output roots and compare the manifest bytes plus the existing recursive package snapshot.

Extend `license-audit.test.mjs` so both `resources/app/LICENSE.txt` and the real plural-name case `LICENSES.chromium.html` are present in the release manifest and closed audit output, and so an unlisted file beneath `licenses/code-oss/` fails the audit.

Run:

```powershell
node --test tools/release/code-oss-runtime.test.mjs tools/release/stage.test.mjs tools/release/license-audit.test.mjs
```

Expected: all focused tests pass.

Update README staging examples to pass a runtime directory and lowercase launcher digest on both platforms.

- [ ] **Step 5: Commit the staging slice**

```powershell
git add tools/release/stage.mjs tools/release/stage.test.mjs tools/release/license-audit.test.mjs README.md
git commit -m "feat: stage complete Code-OSS runtimes"
```

---

### Task 3: Bind the complete Windows runtime into MSIX

**Files:**
- Modify: `tools/release/windows/AppxManifest.xml.template`
- Modify: `tools/release/windows/package-msix.test.mjs`
- Modify: `tools/release/windows/verify-msix.ps1`

**Interfaces:**
- Consumes staged launcher `app/code-oss-runtime/Code - OSS.exe` and all sibling runtime artifacts.
- Produces an MSIX application entry point at `app\code-oss-runtime\Code - OSS.exe`.

- [ ] **Step 1: Change the Windows fixture and add failing launcher assertions**

Make the MSIX fixture contain:

```text
app/code-oss-runtime/Code - OSS.exe
app/code-oss-runtime/resources/app/product.json
app/code-oss-runtime/locales/en-US.pak
```

List all three in its release manifest. Update the existing application-entry regex assertion to:

```js
assert.match(manifestXml, /Executable="app\\code-oss-runtime\\Code - OSS\.exe"/u);
```

Add a verifier test that packages a valid fixture, changes `app/code-oss-runtime/resources/app/product.json` inside the MSIX, and expects `RELEASE_VERIFICATION_FAILED`.

- [ ] **Step 2: Run the Windows packaging test and verify RED**

```powershell
node --test tools/release/windows/package-msix.test.mjs
```

Expected: launcher assertions fail because the template and verifier still require `app/code-oss.exe`.

- [ ] **Step 3: Update the MSIX entry point and verifier**

Set the template application to:

```xml
<Application Id="UnitTestIDE" Executable="app\code-oss-runtime\Code - OSS.exe" EntryPoint="Windows.FullTrustApplication">
```

Change `verify-msix.ps1` to require exactly one artifact at the case-sensitive relative path `app/code-oss-runtime/Code - OSS.exe`, with `kind === "runtime"` and `executable === true`. Other executable artifacts such as the service remain valid. Change the embedded AppxManifest comparison to the matching backslash path. Keep its existing exact payload-set comparison so every runtime dependency must exist and match the staged digest.

- [ ] **Step 4: Run focused and adjacent Windows tests**

```powershell
node --test tools/release/stage.test.mjs tools/release/windows/package-msix.test.mjs
```

Expected: all tests pass, including real Windows SDK `makeappx` when available, with unsigned development packaging only under `RELEASE_SIGNING_REQUIRED=0`.

- [ ] **Step 5: Commit the Windows package slice**

```powershell
git add tools/release/windows/AppxManifest.xml.template tools/release/windows/package-msix.test.mjs tools/release/windows/verify-msix.ps1
git commit -m "fix: package the complete Windows Code-OSS runtime"
```

---

### Task 4: Bind the complete Linux runtime into AppImage

**Files:**
- Modify: `tools/release/linux/AppRun`
- Modify: `tools/release/linux/package-appimage.mjs`
- Modify: `tools/release/linux/package-appimage.test.mjs`
- Modify: `tools/release/linux/verify-appimage.mjs`

**Interfaces:**
- Consumes staged launcher `app/code-oss-runtime/code-oss` and all sibling runtime artifacts.
- Produces fixed AppImage launcher `usr/lib/unit-test-ide/app/code-oss-runtime/code-oss` in AppRun, desktop metadata, sidecar JSON, and verification.

- [ ] **Step 1: Change the Linux fixture and add failing path/tamper assertions**

Replace the single launcher fixture with:

```text
app/code-oss-runtime/code-oss
app/code-oss-runtime/resources/app/product.json
app/code-oss-runtime/locales/en-US.pak
```

Make the launcher executable and list all files in the embedded release manifest. Assert exact fixed paths:

```js
assert.match(desktop, /^Exec=usr\/lib\/unit-test-ide\/app\/code-oss-runtime\/code-oss$/mu);
assert.match(desktop, /^TryExec=usr\/lib\/unit-test-ide\/app\/code-oss-runtime\/code-oss$/mu);
assert.equal(sidecar.launcher, "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss");
assert.equal(verification.launcher, "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss");
```

Add a tamper test that changes `usr/lib/unit-test-ide/app/code-oss-runtime/resources/app/product.json` in the extracted envelope and expects verification failure.
Use the existing `updateFakeEnvelope()` and `refreshSidecarManifest()` helpers so the rejection proves the embedded release-manifest binding rather than only the outer sidecar digest. Update the fake appimagetool's executable-path suffix from `/app/code-oss` to `/app/code-oss-runtime/code-oss`.

- [ ] **Step 2: Run the AppImage test and verify RED**

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Expected: fixed-path assertions fail because AppRun, desktop generation, sidecar metadata, and verifier still use `app/code-oss`.

- [ ] **Step 3: Update every Linux launcher constant**

Set AppRun's `LAUNCHER`, desktop `Exec`/`TryExec` substitution, sidecar `launcher`, verifier `fixedPaths.launcher`, and embedded manifest launcher match to:

```text
usr/lib/unit-test-ide/app/code-oss-runtime/code-oss
```

Preserve the verifier's full artifact loop so every runtime dependency is checked against the embedded release manifest. Require the fixed launcher artifact to be executable.

- [ ] **Step 4: Run focused and adjacent Linux tests**

```powershell
node --test tools/release/stage.test.mjs tools/release/linux/package-appimage.test.mjs
```

Expected: all tests pass, including rejection of tampered runtime resources, launcher identity drift, desktop mismatch, and unexpected payload files.

- [ ] **Step 5: Commit the Linux package slice**

```powershell
git add tools/release/linux/AppRun tools/release/linux/package-appimage.mjs tools/release/linux/package-appimage.test.mjs tools/release/linux/verify-appimage.mjs
git commit -m "fix: package the complete Linux Code-OSS runtime"
```

---

### Task 5: Update lifecycle smoke and trusted workflow inputs

**Files:**
- Modify: `tools/release/update.mjs`
- Modify: `tools/release/update.test.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/release/qualification.test.mjs`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `docs/security.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Consumes platform launcher paths from the approved design.
- Workflow exports `CODE_OSS_RUNTIME_ROOT`, retains `CODE_OSS_SHA256`, and passes `--code-oss-root` plus `--code-oss-sha256` for target and baseline staging.
- Lifecycle smoke continues to emit the same closed qualification evidence schema.

- [ ] **Step 1: Add failing lifecycle tests for the new launcher paths**

Change the test helper constant to:

```js
const launcherRelativePath = process.platform === "win32"
  ? "app/code-oss-runtime/Code - OSS.exe"
  : "app/code-oss-runtime/code-oss";
```

Add a sibling dependency fixture at `app/code-oss-runtime/resources/app/product.json`. In the package-backed production smoke test, assert that forced failure changes only the installed target launcher and leaves the installed target product metadata identical to its manifest-bound source bytes.

- [ ] **Step 2: Run lifecycle tests and verify RED**

```powershell
node --test tools/release/update.test.mjs
```

Expected: launch and corruption tests fail because `update.mjs` still joins `app/code-oss.exe` or `app/code-oss`.

- [ ] **Step 3: Update lifecycle launcher resolution**

Add one internal helper:

```js
function launcherRelativePath() {
  return process.platform === "win32"
    ? "app/code-oss-runtime/Code - OSS.exe"
    : "app/code-oss-runtime/code-oss";
}
```

Use it in `launchHandshake()` and `triggerInstalledUpgradeLaunchFailure()`. Do not change the evidence schema. Keep `--version`, the isolated user-data environment, bounded 30-second launch timeout, rollback verification, repeated rollback, uninstall, and user-data preservation behavior unchanged.

- [ ] **Step 4: Add failing workflow contract assertions, then update workflow and docs**

In `qualification.test.mjs`, require each package job to:

```js
assert.match(job, /CODE_OSS_RUNTIME_ROOT/u);
assert.match(job, /--code-oss-root/u);
assert.match(job, /--code-oss-sha256/u);
assert.doesNotMatch(job, /--code-oss(?:\s|$)/u);
```

Require the Windows job to check the root-level literal `Code - OSS.exe` and the Linux job to check root-level `code-oss` plus executable permission. Verify the launcher digest before staging. Then update `.github/workflows/foundation.yml`:

- Windows artifact root: `.release/inputs/windows-code-oss`.
- Windows launcher: `.release/inputs/windows-code-oss/Code - OSS.exe`.
- Linux artifact root: `.release/inputs/linux-code-oss`.
- Linux launcher: `.release/inputs/linux-code-oss/code-oss`.
- Update workflow-dispatch descriptions and failure text to name those exact launchers.
- Export `CODE_OSS_RUNTIME_ROOT` and pass both new flags in target and baseline staging commands.
- Keep the existing `RELEASE_INPUT_RUN_ID`, digest variables, pinned download-artifact commit, event restrictions, and signing-secret boundaries.

Update workspace smoke documentation assertions, security wording, and the roadmap so they describe a complete runtime artifact rather than a single executable. Do not mark Phase 8 complete.

Run:

```powershell
node --test tools/release/update.test.mjs tools/release/qualification.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs
```

Expected: all tests pass and workflow ordering remains input-check -> artifact download -> digest verification -> package.

- [ ] **Step 5: Commit lifecycle and workflow integration**

```powershell
git add tools/release/update.mjs tools/release/update.test.mjs .github/workflows/foundation.yml tools/release/qualification.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs docs/security.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "fix: launch complete Code-OSS release runtimes"
```

---

### Task 6: Verify the real Windows runtime and full project

**Files:**
- Modify only if verification exposes a test-proven defect in a file already listed by Tasks 1-5.

**Interfaces:**
- Consumes the real Windows root `D:\project\VSCode-win32-x64` and launcher digest `1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672`.
- Produces fresh automated and real-input verification evidence; no generated runtime is committed.

- [ ] **Step 1: Run the complete release test set**

```powershell
$node = 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe'
$releaseTests = rg --files tools/release -g '*.test.mjs'
& $node --test $releaseTests
```

Expected: all release tests pass with zero failures. Any failure is handled by adding a minimal reproducing test before changing production code.

- [ ] **Step 2: Validate the real Windows Code-OSS runtime**

```powershell
$node = 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe'
& $node tools/release/code-oss-runtime.mjs `
  --platform windows `
  --root 'D:\project\VSCode-win32-x64' `
  --launcher-sha256 '1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672'
```

Expected: exit 0 and closed JSON containing `platform: "windows"`, `launcherRelativePath: "Code - OSS.exe"`, the exact lowercase digest, and the three Code-OSS identity values, with no `D:\` path.

- [ ] **Step 3: Run the complete project test command**

Use the repository-pinned Node.js 24.18.0 and pnpm 11.4.0 environment, including the existing local CMake bundle:

```powershell
pnpm test
```

Expected: workspace smoke, all package tests, extension tests, and all Go tests pass.

- [ ] **Step 4: Run final repository checks**

```powershell
git diff --check
git status --short
git log --oneline --decorate -8
```

Expected: no whitespace errors; only the pre-existing untracked `.pnpm-store/` may remain outside the implementation changes; implementation commits are present on the isolated feature branch.

- [ ] **Step 5: Record any verification-only fix and prepare branch completion**

If Step 1-4 exposed no defect, create no additional commit. If a defect was found, first commit its failing regression test and minimal fix together using the affected slice's commit prefix and rerun Steps 1-4. Then invoke `superpowers:verification-before-completion` followed by `superpowers:finishing-a-development-branch`. After the verified implementation is integrated into `master`, push without force to both configured remotes and verify the two remote refs equal local `master`:

```powershell
git push github master
git push origin master
git rev-parse master
git ls-remote github refs/heads/master
git ls-remote origin refs/heads/master
```

Do not upload the locally built Code-OSS runtime in this task; trusted runtime artifact production remains the next plan.

## Plan Self-Review

- Spec coverage: Tasks 1-5 cover runtime validation, complete staging, license closure, MSIX, AppImage, lifecycle smoke, workflow, and documentation; Task 6 covers the real Windows input and full verification.
- Type consistency: `validateCodeOssRuntime`, `codeOssRoot`, `codeOssSha256`, `CODE_OSS_RUNTIME_ROOT`, and the two fixed launcher paths are used consistently across all tasks.
- Scope: trusted artifact production/upload remains deliberately outside this plan and starts only after this runtime-consumption contract is merged.
- Delivery: the verified implementation is integrated into `master` and synchronized to both the GitHub `github` remote and the Gitee `origin` remote without force-pushing.
