# Qualified Release Manifest Collision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Assemble an exact flat qualified release artifact that preserves both platform release manifests under unique asset names and fails closed before upload when any input or digest is invalid.

**Architecture:** Move final artifact assembly out of the untested inline copy sequence and into one focused Node.js module with an exported function and CLI. The module validates canonical inputs, stages eight files in a temporary sibling directory, binds both manifest byte streams to package-job SHA-256 outputs, verifies the exact closed file set, and atomically renames the completed directory before the existing artifact upload.

**Tech Stack:** Node.js 24.18.0, ECMAScript modules, Node built-in test runner, pnpm 11.4.0, GitHub Actions YAML, PowerShell 7 for local orchestration.

## Global Constraints

- Final manifest names are exactly `unit-test-ide-<version>.windows-x64.release-manifest.json` and `unit-test-ide-<version>.linux-x64.release-manifest.json`.
- The final qualified artifact contains exactly eight regular top-level files and no directories or symbolic links.
- Existing Windows/Linux package artifact names, package bytes, embedded `release-manifest.json` names, qualification schema, install-smoke behavior, and license-audit behavior remain unchanged.
- Manifest staging binds Windows to `needs.package-windows.outputs.manifest_sha256` and Linux to `needs.package-linux.outputs.release_manifest_sha256`.
- All failures use `RELEASE_QUALIFIED_STAGING_FAILED` and must not expose absolute paths, environment variables, file contents, tokens, or secrets.
- A failure publishes no final output directory; cleanup is restricted to the command-owned temporary sibling.
- Node.js remains `24.18.0` and pnpm remains `11.4.0`; do not relax engine checks.
- Do not publish a GitHub Release, create a Git tag, materialize a signing certificate, enable signing, or merge the PR.

---

### Task 1: Add the happy-path qualified artifact staging contract

**Files:**
- Create: `tools/release/stage-qualified-release.test.mjs`
- Create: `tools/release/stage-qualified-release.mjs`

**Interfaces:**
- Consumes: `stageQualifiedRelease(input)` where `input` contains `version`, `outRoot`, `windowsPackage`, `windowsManifest`, `windowsManifestSha256`, `windowsLicenseAudit`, `linuxPackage`, `linuxPackageManifest`, `linuxManifest`, `linuxManifestSha256`, `linuxLicenseAudit`, and `qualification`.
- Produces: `Promise<{ outputRoot: string, files: string[] }>` with an absolute final directory and an English-collated sorted filename list.

- [ ] **Step 1: Read the test quality rules before changing tests**

Run:

```powershell
Get-Content -LiteralPath 'C:\Users\DELL\.codex\plugins\cache\openai-curated-remote\superpowers\6.3.0\skills\test-driven-development\writing-good-tests.md' -Raw
```

Expected: the complete test guidance is visible before test code is written.

- [ ] **Step 2: Write the primary regression test**

Create `tools/release/stage-qualified-release.test.mjs` with real temporary files and a static import of the not-yet-created module. Use this fixture shape:

```js
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { access, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

import { stageQualifiedRelease } from "./stage-qualified-release.mjs";

const version = "0.1.0";
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

async function writeFixtureFile(root, relativePath, contents) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(join(path, ".."), { recursive: true });
  await writeFile(path, contents);
  return path;
}

async function createFixture(root) {
  const windowsManifestBytes = Buffer.from('{"platform":"windows"}\n');
  const linuxManifestBytes = Buffer.from('{"platform":"linux"}\n');
  const windowsRoot = join(root, "windows");
  const linuxRoot = join(root, "linux");
  const evidenceRoot = join(root, "evidence");
  return {
    input: {
      version,
      outRoot: join(root, "qualified"),
      windowsPackage: await writeFixtureFile(windowsRoot, `unit-test-ide-${version}.msix`, "windows package\n"),
      windowsManifest: await writeFixtureFile(windowsRoot, `unit-test-ide-${version}.release-manifest.json`, windowsManifestBytes),
      windowsManifestSha256: sha256(windowsManifestBytes),
      windowsLicenseAudit: await writeFixtureFile(windowsRoot, "license-audit-windows.json", "{}\n"),
      linuxPackage: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.AppImage`, "linux package\n"),
      linuxPackageManifest: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.AppImage.sha256.json`, "{}\n"),
      linuxManifest: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.release-manifest.json`, linuxManifestBytes),
      linuxManifestSha256: sha256(linuxManifestBytes),
      linuxLicenseAudit: await writeFixtureFile(linuxRoot, "license-audit-linux.json", "{}\n"),
      qualification: await writeFixtureFile(evidenceRoot, "release-qualification.json", "{}\n"),
    },
    windowsManifestBytes,
    linuxManifestBytes,
  };
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "qualified-release-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await run(root);
}

test("stageQualifiedRelease preserves both platform manifests in an exact flat file set", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    const result = await stageQualifiedRelease(fixture.input);
    const expectedFiles = [
      "license-audit-linux.json",
      "license-audit-windows.json",
      "release-qualification.json",
      `unit-test-ide-${version}.AppImage`,
      `unit-test-ide-${version}.AppImage.sha256.json`,
      `unit-test-ide-${version}.linux-x64.release-manifest.json`,
      `unit-test-ide-${version}.msix`,
      `unit-test-ide-${version}.windows-x64.release-manifest.json`,
    ].sort((left, right) => left.localeCompare(right, "en"));

    assert.deepEqual(result, { outputRoot: resolve(fixture.input.outRoot), files: expectedFiles });
    const entries = await readdir(result.outputRoot, { withFileTypes: true });
    assert.deepEqual(entries.map(({ name }) => name).sort((left, right) => left.localeCompare(right, "en")), expectedFiles);
    assert.ok(entries.every((entry) => entry.isFile() && !entry.isSymbolicLink()));
    assert.deepEqual(
      await readFile(join(result.outputRoot, `unit-test-ide-${version}.windows-x64.release-manifest.json`)),
      fixture.windowsManifestBytes,
    );
    assert.deepEqual(
      await readFile(join(result.outputRoot, `unit-test-ide-${version}.linux-x64.release-manifest.json`)),
      fixture.linuxManifestBytes,
    );
    await assert.rejects(access(join(result.outputRoot, `unit-test-ide-${version}.release-manifest.json`)), /ENOENT/u);
  });
});
```

- [ ] **Step 3: Run the regression test in RED state**

Run:

```powershell
& 'C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe' --test tools/release/stage-qualified-release.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `tools/release/stage-qualified-release.mjs`. This proves the test depends on the missing production unit.

- [ ] **Step 4: Implement the minimum happy-path staging function**

Create `tools/release/stage-qualified-release.mjs` with the exported function, a fixed destination map, temporary sibling staging, exact file-set comparison, and atomic rename. The initial implementation uses these concrete records:

```js
const destinationRecords = (input) => [
  [input.windowsPackage, `unit-test-ide-${input.version}.msix`],
  [input.windowsManifest, `unit-test-ide-${input.version}.windows-x64.release-manifest.json`],
  [input.windowsLicenseAudit, "license-audit-windows.json"],
  [input.linuxPackage, `unit-test-ide-${input.version}.AppImage`],
  [input.linuxPackageManifest, `unit-test-ide-${input.version}.AppImage.sha256.json`],
  [input.linuxManifest, `unit-test-ide-${input.version}.linux-x64.release-manifest.json`],
  [input.linuxLicenseAudit, "license-audit-linux.json"],
  [input.qualification, "release-qualification.json"],
];
```

Resolve `input.outRoot`, create its parent, fail if the final directory exists, create a temporary sibling with `mkdtemp`, copy each source once, read the top-level directory with `withFileTypes: true`, compare its English-collated names to the destination map, require every entry to be a regular non-symbolic-link file, then `rename(temporaryRoot, finalRoot)`. On failure, recursively remove only `temporaryRoot` and rethrow.

- [ ] **Step 5: Run the focused test in GREEN state**

Run:

```powershell
& 'C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe' --test tools/release/stage-qualified-release.test.mjs
```

Expected: `1` test passes, `0` fail, and the old unqualified manifest basename is absent.

- [ ] **Step 6: Commit the first independently testable unit**

```powershell
git add tools/release/stage-qualified-release.mjs tools/release/stage-qualified-release.test.mjs
git diff --cached --check
git commit -m "fix: preserve qualified platform manifests"
```

Expected: one commit containing only the staging module and its initial regression test.

---

### Task 2: Harden staging inputs, digest binding, cleanup, and CLI behavior

**Files:**
- Modify: `tools/release/stage-qualified-release.mjs`
- Modify: `tools/release/stage-qualified-release.test.mjs`

**Interfaces:**
- Consumes: the Task 1 `stageQualifiedRelease(input)` object and twelve exact CLI flags mapped one-to-one to its keys.
- Produces: fail-closed `RELEASE_QUALIFIED_STAGING_FAILED` diagnostics and the same successful return object without exposing native paths on failure.

- [ ] **Step 1: Add a table-driven invalid-input test in RED state**

Add cases that independently replace one valid fixture field and assert rejection plus final-output absence. Test a pre-existing output separately because it must remain untouched:

```js
test("stageQualifiedRelease rejects invalid inputs without publishing output", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate, expected] of [
      ["Windows manifest digest mismatch", (input) => { input.windowsManifestSha256 = "0".repeat(64); }, /Windows manifest SHA-256 does not match/u],
      ["Linux manifest digest mismatch", (input) => { input.linuxManifestSha256 = "0".repeat(64); }, /Linux manifest SHA-256 does not match/u],
      ["unexpected package basename", (input) => { input.windowsPackage = input.qualification; }, /Windows package basename is invalid/u],
    ]) {
      await t.test(name, async () => {
        const caseRoot = await mkdtemp(join(root, "case-"));
        const fixture = await createFixture(caseRoot);
        mutate(fixture.input);
        await assert.rejects(stageQualifiedRelease(fixture.input), (error) => {
          assert.equal(error.code, "RELEASE_QUALIFIED_STAGING_FAILED");
          assert.match(error.message, expected);
          assert.doesNotMatch(error.message, new RegExp(caseRoot.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
          return true;
        });
        await assert.rejects(access(fixture.input.outRoot), /ENOENT/u);
      });
    }

    const existingRoot = join(root, "existing-case");
    const existingFixture = await createFixture(existingRoot);
    await mkdir(existingFixture.input.outRoot);
    await writeFile(join(existingFixture.input.outRoot, "owner-marker"), "preserve\n");
    await assert.rejects(stageQualifiedRelease(existingFixture.input), /qualified output already exists/u);
    assert.equal(await readFile(join(existingFixture.input.outRoot, "owner-marker"), "utf8"), "preserve\n");
  });
});
```

Run the selected test and verify it fails because Task 1 does not validate digests, basenames, or an existing output with the required stable error.

- [ ] **Step 2: Implement exact object, version, basename, real-file, and digest validation**

Add:

```js
const failureCode = "RELEASE_QUALIFIED_STAGING_FAILED";
const digestPattern = /^[0-9a-f]{64}$/u;
const versionPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const requiredKeys = [
  "linuxLicenseAudit", "linuxManifest", "linuxManifestSha256", "linuxPackage",
  "linuxPackageManifest", "outRoot", "qualification", "version",
  "windowsLicenseAudit", "windowsManifest", "windowsManifestSha256", "windowsPackage",
].sort((left, right) => left.localeCompare(right, "en"));

function stagingFailure(message) {
  const error = new Error(`${failureCode}: ${message}`);
  error.code = failureCode;
  return error;
}

async function sha256File(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}
```

Require a plain input object with exactly `requiredKeys`; validate the version and both digest strings; require every source path to be a non-empty string whose `lstat` is a regular file and not a symbolic link; and compare every source basename to the canonical names from the global constraints. Wrap unexpected filesystem failures as `RELEASE_QUALIFIED_STAGING_FAILED: qualified release assembly failed` without appending the native error message.

Hash both source manifests before creating the temporary directory and both copied manifest destinations before rename. Require the hashes to equal their corresponding expected digests at both boundaries.

- [ ] **Step 3: Run the invalid-input test in GREEN state**

Run:

```powershell
& 'C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe' --test --test-name-pattern="rejects invalid inputs without publishing output" tools/release/stage-qualified-release.test.mjs
```

Expected: every invalid case passes and no final output directory exists.

- [ ] **Step 4: Add a symbolic-link input test before changing symbolic-link handling**

Create a link replacing `windowsManifest`. If Windows returns `EPERM` while creating the test link, call `t.skip("file symlink creation is unavailable")`; Linux CI must run the assertion. The test requires `RELEASE_QUALIFIED_STAGING_FAILED`, a controlled `Windows manifest must be a real file` message, no absolute temporary path, and no final output directory.

Run the selected test before the production change. Expected: FAIL because the current regular-file check does not yet map the link to the required controlled error.

- [ ] **Step 5: Make symbolic-link rejection GREEN**

Use `lstat` rather than `stat`, reject `isSymbolicLink()` before `isFile()`, and return only controlled labels from validation failures. Rerun the selected link test and the complete staging test file.

Expected: all staging tests pass; Linux exercises the link rejection and Windows either exercises it or reports the one explicit environment skip.

- [ ] **Step 6: Add CLI parsing tests in RED state**

Import `spawnSync` from `node:child_process` and add this exact argument builder so the success case exercises every public flag:

```js
function cliArgs(input) {
  return [
    "--version", input.version,
    "--out", input.outRoot,
    "--windows-package", input.windowsPackage,
    "--windows-manifest", input.windowsManifest,
    "--windows-manifest-sha256", input.windowsManifestSha256,
    "--windows-license-audit", input.windowsLicenseAudit,
    "--linux-package", input.linuxPackage,
    "--linux-package-manifest", input.linuxPackageManifest,
    "--linux-manifest", input.linuxManifest,
    "--linux-manifest-sha256", input.linuxManifestSha256,
    "--linux-license-audit", input.linuxLicenseAudit,
    "--qualification", input.qualification,
  ];
}

function runCli(args) {
  return spawnSync(process.execPath, [resolve("tools/release/stage-qualified-release.mjs"), ...args], {
    encoding: "utf8",
  });
}
```

Use `runCli(cliArgs(fixture.input))` to require status `0`, empty stderr, and a parsed stdout object whose files equal the eight-name set from Task 1. Add two failure invocations:

```js
const unknown = runCli(["--secret-file", fixture.input.windowsManifest]);
assert.equal(unknown.status, 1);
assert.match(unknown.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: unknown argument: --secret-file/u);

const missingValue = runCli(["--version"]);
assert.equal(missingValue.status, 1);
assert.match(missingValue.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: missing value for --version/u);
```

These tests prove:

- the twelve exact flags map to the exported function input;
- an unknown `--secret-file` flag exits `1` with `RELEASE_QUALIFIED_STAGING_FAILED: unknown argument: --secret-file`;
- a missing value exits `1` with `RELEASE_QUALIFIED_STAGING_FAILED: missing value for --version`;
- stderr contains neither the temporary root nor fixture contents.

Run the CLI tests. Expected: FAIL because Task 1 has no CLI entry point.

- [ ] **Step 7: Implement the exact CLI interface and rerun GREEN**

Map these flags and reject duplicates, unknown flags, missing values, and missing required keys:

```text
--version
--out
--windows-package
--windows-manifest
--windows-manifest-sha256
--windows-license-audit
--linux-package
--linux-package-manifest
--linux-manifest
--linux-manifest-sha256
--linux-license-audit
--qualification
```

Guard the CLI with:

```js
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    const safe = error?.code === failureCode ? error : stagingFailure("qualified release assembly failed");
    console.error(safe.message);
    process.exitCode = 1;
  });
}
```

On success, write exactly one JSON line containing the return object. Run the CLI selection and then the complete staging test file.

Expected: all tests pass, successful stdout parses as one object, and negative stderr is path-free.

- [ ] **Step 8: Commit the hardened staging contract**

```powershell
git add tools/release/stage-qualified-release.mjs tools/release/stage-qualified-release.test.mjs
git diff --cached --check
git commit -m "fix: validate closed qualified release staging"
```

Expected: a focused hardening commit with no workflow change yet.

---

### Task 3: Bind the foundation workflow to the tested staging command

**Files:**
- Modify: `tools/release/qualification.test.mjs:521-533`
- Modify: `.github/workflows/foundation.yml:1185-1208`
- Modify: `README.md:190-204`

**Interfaces:**
- Consumes: the Task 2's twelve CLI flags and the existing package-job outputs.
- Produces: `.release/qualified` only after qualification succeeds and both manifest digests are independently revalidated.

- [ ] **Step 1: Add the workflow contract test in RED state**

Add a new test after `foundation release publication is downstream of a successful qualification gate`. Slice only the `release-qualification` job and require:

```js
assert.match(qualificationJob, /node tools\/release\/stage-qualified-release\.mjs/u);
assert.match(qualificationJob, /--windows-manifest-sha256 '\$\{\{ needs\.package-windows\.outputs\.manifest_sha256 \}\}'/u);
assert.match(qualificationJob, /--linux-manifest-sha256 '\$\{\{ needs\.package-linux\.outputs\.release_manifest_sha256 \}\}'/u);
assert.match(qualificationJob, /--windows-manifest "\$\(find_input \.release\/qualification\/windows '\$\{\{ needs\.package-windows\.outputs\.manifest_filename \}\}'\)"/u);
assert.match(qualificationJob, /--linux-manifest "\$\(find_input \.release\/qualification\/linux '\$\{\{ needs\.package-linux\.outputs\.release_manifest_filename \}\}'\)"/u);
assert.doesNotMatch(qualificationJob, /cp -- "\$\(find_input \.release\/qualification\/(?:windows|linux)/u);
const stageIndex = qualificationJob.indexOf("node tools/release/stage-qualified-release.mjs");
const uploadIndex = qualificationJob.indexOf("name: Publish qualified release artifacts");
assert.ok(stageIndex >= 0 && uploadIndex > stageIndex);
```

Run:

```powershell
& 'C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe' --test --test-name-pattern="foundation stages a closed qualified release" tools/release/qualification.test.mjs
```

Expected: FAIL because the workflow still contains the inline colliding copy sequence and does not invoke the staging command.

- [ ] **Step 2: Replace the inline copy sequence with the staging CLI**

Keep the existing `find_input` helper, version equality gate, upload name, run-attempt suffix, and one-day retention. Replace only the eight `cp` lines with:

```yaml
          node tools/release/stage-qualified-release.mjs \
            --version '${{ steps.canonical-release-version.outputs.version }}' \
            --out .release/qualified \
            --windows-package "$(find_input .release/qualification/windows '${{ needs.package-windows.outputs.package_filename }}')" \
            --windows-manifest "$(find_input .release/qualification/windows '${{ needs.package-windows.outputs.manifest_filename }}')" \
            --windows-manifest-sha256 '${{ needs.package-windows.outputs.manifest_sha256 }}' \
            --windows-license-audit "$(find_input .release/qualification/windows license-audit-windows.json)" \
            --linux-package "$(find_input .release/qualification/linux '${{ needs.package-linux.outputs.package_filename }}')" \
            --linux-package-manifest "$(find_input .release/qualification/linux '${{ needs.package-linux.outputs.manifest_filename }}')" \
            --linux-manifest "$(find_input .release/qualification/linux '${{ needs.package-linux.outputs.release_manifest_filename }}')" \
            --linux-manifest-sha256 '${{ needs.package-linux.outputs.release_manifest_sha256 }}' \
            --linux-license-audit "$(find_input .release/qualification/linux license-audit-linux.json)" \
            --qualification .release/evidence/release-qualification.json
```

Do not change qualification inputs, signing outputs, package jobs, or artifact publication policy.

- [ ] **Step 3: Run workflow and staging tests in GREEN state**

Run:

```powershell
& 'C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe' --test tools/release/stage-qualified-release.test.mjs tools/release/qualification.test.mjs
```

Expected: all staging tests and all `24` pre-existing qualification tests plus the new workflow contract test pass.

- [ ] **Step 4: Document the final qualified artifact contract**

Update `README.md` in the unsigned qualification section to state that `qualified-release-<version>-<attempt>` contains exactly eight files and names both platform-qualified manifests. State that the platform package artifacts and embedded manifest names remain unchanged.

- [ ] **Step 5: Commit workflow integration and documentation**

```powershell
git add .github/workflows/foundation.yml tools/release/qualification.test.mjs README.md
git diff --cached --check
git commit -m "fix: assemble a closed qualified release artifact"
```

Expected: one commit that consumes the already tested staging command without unrelated workflow edits.

---

### Task 4: Run complete verification and inspect the branch diff

**Files:**
- Verify: `tools/release/stage-qualified-release.mjs`
- Verify: `tools/release/stage-qualified-release.test.mjs`
- Verify: `tools/release/qualification.test.mjs`
- Verify: `.github/workflows/foundation.yml`
- Verify: `README.md`

**Interfaces:**
- Consumes: all Task 1-3 commits.
- Produces: fresh evidence that the branch preserves release, package, producer, and repository contracts.

- [ ] **Step 1: Run focused and broader release tests**

```powershell
$node='C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe'
& $node --test `
  tools/release/stage-qualified-release.test.mjs `
  tools/release/qualification.test.mjs `
  tools/release/license-audit.test.mjs `
  tools/release/linux/package-appimage.test.mjs `
  tools/release/windows/package-msix.test.mjs `
  tools/release/stage.test.mjs `
  tools/release/update.test.mjs `
  tools/release/producer/workflow-contract.test.mjs
```

Expected: zero failures; only explicit environment-dependent skips already encoded by the tests are permitted.

- [ ] **Step 2: Run repository verification with pinned pnpm**

```powershell
$node='C:\actions-runner\_work\_tool\node\24.18.0\x64\node.exe'
$pnpmJs='C:\Program Files\nodejs\node_modules\corepack\dist\pnpm.js'
& $node $pnpmJs verify
```

Expected: generated-code checks, builds, Node tests, Go tests/race tests, and configured E2E tests all complete with exit code `0`.

- [ ] **Step 3: Inspect scope and whitespace**

```powershell
git diff --check 33806efdcda20453f0dfdd69b97016913336cb09...HEAD
git diff --stat 33806efdcda20453f0dfdd69b97016913336cb09...HEAD
git diff 33806efdcda20453f0dfdd69b97016913336cb09...HEAD -- `
  tools/release/stage-qualified-release.mjs `
  tools/release/stage-qualified-release.test.mjs `
  tools/release/qualification.test.mjs `
  .github/workflows/foundation.yml `
  README.md `
  docs/superpowers/specs/2026-09-11-qualified-release-manifest-collision-design.md `
  docs/superpowers/plans/2026-09-11-qualified-release-manifest-collision.md
```

Expected: only the seven planned files differ from the base, no whitespace errors exist, and no signing/publication/producer trust behavior changes.

- [ ] **Step 4: Verify branch and workspace state**

```powershell
git status --short --untracked-files=no
git log --oneline --decorate 33806efdcda20453f0dfdd69b97016913336cb09..HEAD
```

Expected: no tracked changes remain. The two local `.release/evidence` directories may remain untracked and must not be staged.

---

### Task 5: Push both remotes and create an unmerged GitHub PR

**Files:**
- No additional source files.

**Interfaces:**
- Consumes: a verified local branch `codex/fix-qualified-release-manifest-collision` based on `33806efdcda20453f0dfdd69b97016913336cb09`.
- Produces: matching GitHub/Gitee feature branch tips and one open GitHub PR targeting `master`.

- [ ] **Step 1: Reconfirm publication and signing boundaries before network writes**

```powershell
$releases=@(gh release list --repo colayc/unitTest --limit 100 --json tagName,isDraft,isPrerelease,publishedAt | ConvertFrom-Json)
$githubTags=@(git ls-remote github refs/tags/0.1.0 refs/tags/v0.1.0)
$giteeTags=@(git ls-remote origin refs/tags/0.1.0 refs/tags/v0.1.0)
if (@($releases | Where-Object { $_.tagName -in @('0.1.0','v0.1.0') }).Count -ne 0 -or $githubTags.Count -ne 0 -or $giteeTags.Count -ne 0) {
  throw 'publication boundary changed'
}
```

Expected: no `0.1.0` or `v0.1.0` release/tag exists.

- [ ] **Step 2: Push the ordinary feature branch to GitHub and Gitee**

```powershell
git push -u github codex/fix-qualified-release-manifest-collision
git push -u origin codex/fix-qualified-release-manifest-collision
```

Expected: both ordinary pushes succeed without force and both remote branch tips equal local `HEAD`.

- [ ] **Step 3: Create the GitHub PR without merging**

```powershell
gh pr create `
  --repo colayc/unitTest `
  --base master `
  --head codex/fix-qualified-release-manifest-collision `
  --title "fix: preserve both qualified release manifests" `
  --body "Fix the final qualified artifact collision exposed by unsigned foundation run 34574201783. A tested Node staging command now preserves the Windows and Linux release manifests under unique platform-qualified asset names, revalidates both package-job digests, enforces the exact eight-file flat set, and publishes no partial output on failure. Package inputs, embedded manifests, producer trust, signing, and publication policy remain unchanged. This PR does not publish a Release or enable signing."
```

Expected: one new open PR URL; the PR remains unmerged.

- [ ] **Step 4: Verify remote identity and inspect PR checks**

```powershell
$head=(git rev-parse HEAD).Trim()
$github=((git ls-remote github refs/heads/codex/fix-qualified-release-manifest-collision) -split '\s+')[0]
$gitee=((git ls-remote origin refs/heads/codex/fix-qualified-release-manifest-collision) -split '\s+')[0]
if ($head -ne $github -or $head -ne $gitee) { throw 'feature branch tips diverged' }
gh pr view --repo colayc/unitTest --json number,url,state,isDraft,mergeStateStatus,headRefOid,baseRefName,statusCheckRollup
```

Expected: local/GitHub/Gitee feature branch hashes match, the PR state is `OPEN`, base is `master`, and no merge has occurred. Wait for the required checks and report their actual conclusions; do not merge without separate user authorization.
