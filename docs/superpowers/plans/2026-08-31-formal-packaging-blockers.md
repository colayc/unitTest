# Formal Packaging Blockers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the free unsigned formal qualification package successfully on Windows and Linux by preserving strict MSIX epoch validation across PowerShell hosts and adding a closed, deterministic AppImage icon contract.

**Architecture:** Keep the two fixes platform-local. Windows normalizes only PowerShell's in-memory representation after the existing Node.js manifest validator has accepted the original JSON; Linux copies one fixed SVG into the AppDir and extends the existing AppImage verifier's fixed-file and closed-path checks. Deliver both commits through one reviewed branch and PR, then use a fresh producer run and a fresh foundation dispatch for formal evidence.

**Tech Stack:** PowerShell 5.1 and PowerShell 7, Node.js 24.18.0, pnpm 11.4.0, `node:test`, SVG, GitHub Actions, GitHub CLI.

## Global Constraints

- Preserve exact `generatedAt` equality with the canonical value derived from `SOURCE_DATE_EPOCH`.
- Support both Windows PowerShell 5.1 string parsing and PowerShell 7 `System.DateTime` parsing.
- Keep Node.js release-manifest schema and canonical UTC ISO validation unchanged.
- Use repository asset `tools/release/linux/unit-test-ide.svg` and AppDir path `unit-test-ide.svg`.
- Keep `Icon=unit-test-ide` in `tools/release/linux/unit-test-ide.desktop`.
- Require the embedded SVG to match repository bytes, be non-executable, and belong to the AppImage expected-path set.
- Do not change producer trust, artifact identity, release-manifest schema, signing, or publication policy.
- Formal qualification remains `release_version=0.1.0` and `release_signing_required=0`.
- Do not reuse failed foundation run `33347132685`; after merge, create a fresh producer run and a fresh foundation run.
- Push reviewed source commits to both GitHub and Gitee.

## File Structure

- Modify `tools/release/windows/package-msix.ps1`: normalize a validated manifest timestamp across PowerShell JSON representations and keep exact epoch binding.
- Modify `tools/release/windows/package-msix.test.mjs`: select the PowerShell host for a package invocation and add PowerShell 7 matching/mismatch regressions without removing PowerShell 5.1 coverage.
- Create `tools/release/linux/unit-test-ide.svg`: deterministic temporary product icon used only for current package completeness.
- Modify `tools/release/linux/package-appimage.mjs`: validate and copy the fixed icon into the AppDir before timestamp normalization.
- Modify `tools/release/linux/verify-appimage.mjs`: require, byte-compare, mode-check, and close the icon path.
- Modify `tools/release/linux/package-appimage.test.mjs`: prove icon materialization and missing, tampered, executable, and alias rejection.

---

### Task 1: Preserve strict MSIX epoch validation across PowerShell hosts

**Files:**
- Modify: `tools/release/windows/package-msix.test.mjs:257-291`
- Modify: `tools/release/windows/package-msix.test.mjs:733-770`
- Modify: `tools/release/windows/package-msix.ps1:157-179`
- Modify: `tools/release/windows/package-msix.ps1:254-275`

**Interfaces:**
- Consumes: the existing validated `release-manifest.json` field `generatedAt` and `Get-SourceDateEpoch().Iso`.
- Produces: PowerShell function `ConvertTo-CanonicalManifestTimestamp([object]$Value) -> string`, which accepts only `System.String` or `System.DateTime` and fails with `RELEASE_INPUT_MISSING` otherwise.
- Preserves: `runPackage(args, env)` callers continue using Windows PowerShell 5.1 by default.

- [ ] **Step 1: Add a host-selectable test helper and the PowerShell 7 regression**

Change the helpers to accept an optional executable while preserving every existing caller:

```js
function runPowerShellFile(filePath, args, env, executable = "powershell.exe") {
  return spawnSync(executable, [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    filePath,
    ...args,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: {
      ...process.env,
      SOURCE_DATE_EPOCH: sourceDateEpoch,
      ...env,
    },
    windowsHide: true,
  });
}

function runPackage(args, env, executable = "powershell.exe") {
  return runPowerShellFile(packageScript, args, env, executable);
}
```

Add this Windows-only test next to the existing `SOURCE_DATE_EPOCH` tests:

```js
windowsOnly("package-msix accepts canonical generatedAt under PowerShell 7", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeMakeAppx = await createFakeMakeAppx(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
    }, "pwsh.exe");

    assert.equal(result.status, 0, result.stderr);
  });
});
```

- [ ] **Step 2: Run the PowerShell 7 regression and confirm RED**

Run:

```powershell
node --test --test-name-pattern="canonical generatedAt under PowerShell 7" tools/release/windows/package-msix.test.mjs
```

Expected: FAIL because `pwsh.exe` converts `generatedAt` into `System.DateTime`, and the current `[string]` conversion produces a culture-specific value that triggers `release manifest generatedAt does not match SOURCE_DATE_EPOCH`.

- [ ] **Step 3: Add canonical timestamp normalization to the package script**

Place this function after `Get-SourceDateEpoch`:

```powershell
function ConvertTo-CanonicalManifestTimestamp {
  param([Parameter(Mandatory = $true)][object]$Value)

  if ($Value -is [string]) {
    return [string]$Value
  }
  if ($Value -is [DateTime]) {
    return $Value.ToUniversalTime().ToString(
      "yyyy-MM-dd'T'HH:mm:ss.fff'Z'",
      [Globalization.CultureInfo]::InvariantCulture
    )
  }
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'release manifest generatedAt has an unsupported PowerShell representation'
}
```

Replace the direct cast-and-compare with:

```powershell
$manifestGeneratedAt = ConvertTo-CanonicalManifestTimestamp -Value $releaseManifest.generatedAt
if ($manifestGeneratedAt -cne $sourceEpoch.Iso) {
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'release manifest generatedAt does not match SOURCE_DATE_EPOCH'
}
```

Do not change the preceding Node.js manifest-validator invocation.

- [ ] **Step 4: Rerun the PowerShell 7 regression and confirm GREEN**

Run:

```powershell
node --test --test-name-pattern="canonical generatedAt under PowerShell 7" tools/release/windows/package-msix.test.mjs
```

Expected: PASS and a fake unsigned MSIX is produced.

- [ ] **Step 5: Add a real mismatch regression under both PowerShell hosts**

Add these parameterized tests immediately after the matching regression:

```js
for (const executable of ["powershell.exe", "pwsh.exe"]) {
  windowsOnly(`package-msix rejects a different canonical generatedAt under ${executable}`, async (t) => {
    await withTemporaryRoot(t, async (root) => {
      const fixture = await createStagingFixture(root);
      const manifest = JSON.parse(await readFile(fixture.manifestPath, "utf8"));
      manifest.generatedAt = "2026-08-25T00:00:01.000Z";
      await writeFile(fixture.manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
      const fakeMakeAppx = await createFakeMakeAppx(root);
      const result = runPackage([
        "-StagingRoot", fixture.stagingRoot,
        "-Output", fixture.outputPath,
        "-Version", fixture.version,
        "-Publisher", "CN=Unit Test IDE",
      ], {
        RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
        RELEASE_SIGNING_REQUIRED: "0",
      }, executable);

      assert.equal(result.status, 1);
      assert.match(result.stderr, /release manifest generatedAt does not match SOURCE_DATE_EPOCH/u);
    });
  });
}
```

- [ ] **Step 6: Run both PowerShell-host contracts**

Run:

```powershell
node --test tools/release/windows/package-msix.test.mjs
```

Expected: every Windows package test passes. Existing package success coverage still runs through `powershell.exe`, the new matching regression runs through `pwsh.exe`, and the mismatch regression runs through both hosts.

- [ ] **Step 7: Commit the Windows fix**

```powershell
git add -- tools/release/windows/package-msix.ps1 tools/release/windows/package-msix.test.mjs
git commit -m "fix: normalize MSIX manifest timestamps"
```

---

### Task 2: Add a deterministic closed AppImage icon contract

**Files:**
- Create: `tools/release/linux/unit-test-ide.svg`
- Modify: `tools/release/linux/package-appimage.mjs:11-27`
- Modify: `tools/release/linux/package-appimage.mjs:230-264`
- Modify: `tools/release/linux/verify-appimage.mjs:10-36`
- Modify: `tools/release/linux/verify-appimage.mjs:300-321`
- Modify: `tools/release/linux/verify-appimage.mjs:350-365`
- Modify: `tools/release/linux/package-appimage.test.mjs:266-356`

**Interfaces:**
- Consumes: repository SVG `tools/release/linux/unit-test-ide.svg` and desktop declaration `Icon=unit-test-ide`.
- Produces: AppDir regular file `unit-test-ide.svg` with repository bytes and non-executable mode.
- Adds optional programmatic `packageAppImage` input `iconPath: string` for dependency injection; the CLI continues to use the fixed repository asset and exposes no new flag.
- Extends internal verifier constant `fixedPaths.icon` with exact value `unit-test-ide.svg`.

- [ ] **Step 1: Add RED tests for packaging and verification**

Add the icon source constant near `verifyCli`:

```js
const iconSource = resolve("tools/release/linux/unit-test-ide.svg");
```

Extend the existing successful package test after its desktop assertions:

```js
const iconBytes = await readFile(iconSource);
const appDirIcon = await readFile(join(result.appDir, "unit-test-ide.svg"));
assert.deepEqual(appDirIcon, iconBytes);
const envelope = await parseFakeEnvelope(result.outputPath);
const embeddedIcon = envelope.files["unit-test-ide.svg"];
assert.ok(embeddedIcon);
assert.deepEqual(Buffer.from(embeddedIcon.contentBase64, "base64"), iconBytes);
assert.equal(embeddedIcon.executable, false);
```

Add the missing-template test:

```js
test("packageAppImage fails closed when the fixed icon template is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    await assert.rejects(
      () => packageAppImage({
        stagingRoot: fixture.stagingRoot,
        output: fixture.outputPath,
        appimagetool: fakeTool.path,
        expectedDigest: fakeTool.sha256,
        sourceDateEpoch,
        version: fixture.version,
        architecture: "x64",
        iconPath: join(root, "missing-unit-test-ide.svg"),
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_TEMPLATE_MISSING");
        assert.match(error?.message ?? "", /icon template/u);
        return true;
      },
    );
  });
});
```

Add the verifier mutation matrix:

```js
test("verifyAppImage rejects missing, tampered, executable, and aliased icons", async (t) => {
  const cases = [
    ["missing", (files) => { delete files["unit-test-ide.svg"]; }, /icon is missing/u],
    ["tampered", (files) => {
      files["unit-test-ide.svg"].contentBase64 = Buffer.from("<svg/>\n").toString("base64");
    }, /icon content does not match/u],
    ["executable", (files) => { files["unit-test-ide.svg"].executable = true; }, /icon executable bit/u],
    ["alias", (files) => {
      files["unit-test-ide.png"] = { ...files["unit-test-ide.svg"] };
    }, /unexpected payload path: unit-test-ide\.png/u],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate, expected] of cases) {
      const result = await packageWithFakeTool(join(root, name));
      await updateFakeEnvelope(result.outputPath, async (envelope) => mutate(envelope.files));
      await refreshSidecarManifest(result.manifestPath, result.outputPath);
      await assert.rejects(
        () => verifyAppImage({
          image: result.outputPath,
          manifest: result.manifestPath,
          requireDigest: true,
          extractor: result.extractor,
        }),
        expected,
        name,
      );
    }
  });
});
```

- [ ] **Step 2: Run the icon tests and confirm RED**

Run:

```powershell
node --test --test-name-pattern="icon|icons|closed digest manifest" tools/release/linux/package-appimage.test.mjs
```

Expected: FAIL because `unit-test-ide.svg` does not exist, `iconPath` is not supported, and the current AppDir/verifier contract contains no icon.

- [ ] **Step 3: Create the deterministic SVG asset**

Create `tools/release/linux/unit-test-ide.svg` with exactly:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" role="img" aria-label="Unit Test IDE">
  <rect width="256" height="256" rx="48" fill="#2563eb"/>
  <path d="M64 64v72c0 39.8 32.2 72 72 72s72-32.2 72-72V64h-32v72c0 22.1-17.9 40-40 40s-40-17.9-40-40V64H64z" fill="#fff"/>
  <path d="m112 119 18 18 38-42" fill="none" stroke="#2563eb" stroke-width="14" stroke-linecap="round" stroke-linejoin="round"/>
</svg>
```

This is a deterministic temporary icon, not final branding.

- [ ] **Step 4: Validate and copy the icon in the AppImage packager**

Add the default path:

```js
const defaultIconPath = join(toolDirectory, "unit-test-ide.svg");
```

Add `"iconPath"` to `supportedKeys`, validate it with the existing template failure code, and copy it before timestamp normalization:

```js
const icon = await validateRealFile(
  input.iconPath ?? defaultIconPath,
  "icon template",
  "RELEASE_TEMPLATE_MISSING",
);
```

```js
await materializeDesktopEntry(desktopTemplate.path, join(appDir, "unit-test-ide.desktop"), input.version);
await copyRegularFile(icon.path, join(appDir, "unit-test-ide.svg"));
await normalizeTreeTimestamps(appDir, sourceEpoch);
```

- [ ] **Step 5: Close the icon contract in the AppImage verifier**

Add the repository asset and fixed path:

```js
const defaultIconPath = join(toolDirectory, "unit-test-ide.svg");

const fixedPaths = {
  appRun: "AppRun",
  desktopEntry: "unit-test-ide.desktop",
  icon: "unit-test-ide.svg",
  launcher: "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss",
  releaseManifestPath: "usr/lib/unit-test-ide/release-manifest.json",
  payloadRoot: "usr/lib/unit-test-ide",
};
```

Include `fixedPaths.icon` in `expectedPayloadPaths`:

```js
const paths = new Set([
  fixedPaths.appRun,
  fixedPaths.desktopEntry,
  fixedPaths.icon,
  fixedPaths.releaseManifestPath,
]);
```

Require and verify the file alongside `AppRun` and the desktop entry:

```js
const icon = requireFile(extraction.files, fixedPaths.icon, "icon");
const expectedIcon = await readFile(defaultIconPath);
assertBufferEquals(icon.content, expectedIcon, "icon");
assertExecutable(icon.executable, false, "icon");
```

Do not add the icon to `releaseManifest.artifacts`; it is fixed AppDir metadata guarded by the AppImage verifier.

- [ ] **Step 6: Rerun all icon tests and confirm GREEN**

Run:

```powershell
node --test --test-name-pattern="icon|icons|closed digest manifest" tools/release/linux/package-appimage.test.mjs
```

Expected: all selected tests pass. The fake envelope contains exactly one fixed SVG, and every mutation is rejected with the expected stable error.

- [ ] **Step 7: Run the complete AppImage test file**

Run:

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Expected: all tests pass, including the existing unexpected-payload closed-set regression.

- [ ] **Step 8: Commit the Linux fix**

```powershell
git add -- tools/release/linux/unit-test-ide.svg tools/release/linux/package-appimage.mjs tools/release/linux/verify-appimage.mjs tools/release/linux/package-appimage.test.mjs
git commit -m "fix: package deterministic AppImage icon"
```

---

### Task 3: Verify the branch and publish one reviewed PR

**Files:**
- Verify: `tools/release/windows/package-msix.ps1`
- Verify: `tools/release/windows/package-msix.test.mjs`
- Verify: `tools/release/linux/unit-test-ide.svg`
- Verify: `tools/release/linux/package-appimage.mjs`
- Verify: `tools/release/linux/verify-appimage.mjs`
- Verify: `tools/release/linux/package-appimage.test.mjs`
- Verify: `docs/superpowers/specs/2026-08-31-formal-packaging-blockers-design.md`
- Verify: `docs/superpowers/plans/2026-08-31-formal-packaging-blockers.md`

**Interfaces:**
- Consumes: Task 1 and Task 2 commits on `codex/fix-formal-packaging-blockers`.
- Produces: one GitHub PR with passing Linux and Windows gates and the same branch pushed to Gitee.

- [ ] **Step 1: Run the focused release suite**

```powershell
node --test tools/release/windows/package-msix.test.mjs tools/release/linux/package-appimage.test.mjs tools/release/stage.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/producer/workflow-contract.test.mjs
```

Expected: all tests pass; Windows-only tests execute on this Windows host and Linux packaging tests use the deterministic fake tool.

- [ ] **Step 2: Run full verification**

```powershell
pnpm verify
```

Expected: exit code 0 with generated contracts unchanged, TypeScript and Go builds passing, unit tests passing, Go race tests passing, and E2E passing or explicitly skipped only by existing environment gates.

- [ ] **Step 3: Check the patch and workspace boundary**

```powershell
git diff --check github/master...HEAD
git status --short
git log --oneline github/master..HEAD
```

Expected: no whitespace errors; the only untracked path is the retained `.release/` diagnostic evidence; commits contain the design, plan, Windows fix, and Linux fix.

- [ ] **Step 4: Push the branch to GitHub and Gitee**

```powershell
git push -u github codex/fix-formal-packaging-blockers
git push -u origin codex/fix-formal-packaging-blockers
```

Expected: both remotes point the branch at the same commit.

- [ ] **Step 5: Open the GitHub PR**

```powershell
gh pr create --repo colayc/unitTest --base master --head codex/fix-formal-packaging-blockers --title "fix: unblock formal Windows and Linux packaging" --body "Fix the two deterministic packaging failures exposed by foundation run 33347132685. PowerShell 7 manifest timestamps are normalized back to canonical UTC ISO before strict SOURCE_DATE_EPOCH comparison, while Windows PowerShell 5.1 coverage remains. AppImage packaging now includes one deterministic fixed SVG that is byte-, mode-, and closed-path verified. No signing or publication policy changes."
```

Expected: one new PR URL.

- [ ] **Step 6: Wait for required PR gates and review the result**

```powershell
gh pr checks --repo colayc/unitTest --watch --interval 10
gh pr view --repo colayc/unitTest --json number,url,mergeStateStatus,mergeable,statusCheckRollup
```

Expected: Linux and Windows gates succeed; the PR is mergeable and clean. Stop and request explicit user confirmation before merging.

---

### Task 4: Produce fresh formal unsigned qualification evidence

**Files:**
- No source changes.
- Evidence source: `.github/workflows/release-inputs.yml`
- Evidence consumer: `.github/workflows/foundation.yml`

**Interfaces:**
- Consumes: the explicitly approved and merged PR from Task 3.
- Produces: a successful fresh producer run, a successful fresh foundation run, and inspected short-lived package/install/qualification artifacts.

- [ ] **Step 1: Merge only after explicit approval and synchronize master**

```powershell
$prNumber = gh pr view --repo colayc/unitTest --json number --jq '.number'
gh pr merge $prNumber --repo colayc/unitTest --rebase --delete-branch
git fetch github master
git push origin github/master:master
git fetch origin master
$mergedCommit = (git rev-parse github/master).Trim()
$giteeCommit = (git rev-parse origin/master).Trim()
if ($mergedCommit -cne $giteeCommit) { throw "GitHub and Gitee master commits differ" }
$mergedCommit
```

Expected: GitHub and Gitee `master` resolve to the same merged commit.

- [ ] **Step 2: Dispatch a fresh trusted producer run**

```powershell
$previousProducerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousProducerRunId = if ($null -eq $previousProducerRun) { "" } else { [string]$previousProducerRun.databaseId }
gh workflow run release-inputs.yml --repo colayc/unitTest --ref master
do {
  Start-Sleep -Seconds 5
  $producerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,createdAt,headSha,status,url | ConvertFrom-Json
} while ($null -eq $producerRun -or [string]$producerRun.databaseId -eq $previousProducerRunId)
if ([string]$producerRun.headSha -cne $mergedCommit) { throw "Producer run is not using the merged master commit" }
$producerRun | ConvertTo-Json -Compress
gh run watch $producerRun.databaseId --repo colayc/unitTest --exit-status --interval 10
```

Expected: the producer run succeeds on the merged `master` commit.

- [ ] **Step 3: Download and read the fresh provenance**

```powershell
$producerRunId = [string]$producerRun.databaseId
$evidenceRoot = Join-Path $PWD ".release/evidence/producer-$producerRunId"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
gh run download $producerRunId --repo colayc/unitTest --name release-input-provenance-1 --dir $evidenceRoot
$provenance = Get-Content -LiteralPath (Join-Path $evidenceRoot 'release-input-provenance.json') -Raw | ConvertFrom-Json
if ([string]$provenance.producer.runId -cne $producerRunId) { throw "Provenance run ID mismatch" }
if ([string]$provenance.producer.sourceCommit -cne $mergedCommit) { throw "Provenance source commit mismatch" }
if ([string]$provenance.producer.event -cne "workflow_dispatch") { throw "Provenance event mismatch" }
$provenance | ConvertTo-Json -Depth 20
```

Expected: one closed provenance document naming the successful run and merged source commit, with Windows launcher, Linux launcher, and appimagetool SHA-256 values.

- [ ] **Step 4: Dispatch a fresh free unsigned foundation run**

```powershell
$windowsSha = [string]$provenance.runtimes.windows.launcherSha256
$linuxSha = [string]$provenance.runtimes.linux.launcherSha256
$appImageToolSha = [string]$provenance.appimagetool.sha256
foreach ($digest in @($windowsSha, $linuxSha, $appImageToolSha)) {
  if ($digest -cnotmatch '^[0-9a-f]{64}$') { throw "Provenance contains an invalid SHA-256 value" }
}
$previousFoundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousFoundationRunId = if ($null -eq $previousFoundationRun) { "" } else { [string]$previousFoundationRun.databaseId }
gh workflow run foundation.yml --repo colayc/unitTest --ref master `
  -f release_version=0.1.0 `
  -f release_signing_required=0 `
  -f release_input_run_id=$producerRunId `
  -f windows_code_oss_sha256=$windowsSha `
  -f linux_code_oss_sha256=$linuxSha `
  -f linux_appimagetool_sha256=$appImageToolSha
```

Expected: dispatch accepted with signing disabled and no GitHub Release created.

- [ ] **Step 5: Identify and watch the new foundation run**

```powershell
do {
  Start-Sleep -Seconds 5
  $foundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,headSha,status,url | ConvertFrom-Json
} while ($null -eq $foundationRun -or [string]$foundationRun.databaseId -eq $previousFoundationRunId)
if ([string]$foundationRun.headSha -cne $mergedCommit) { throw "Foundation run is not using the merged master commit" }
$foundationRun | ConvertTo-Json -Compress
gh run watch $foundationRun.databaseId --repo colayc/unitTest --exit-status --interval 10
```

Expected: `verify-release-input-run`, both verify jobs, both package jobs, both install-smoke jobs, and `release-qualification` succeed.

- [ ] **Step 6: Inspect the closed artifact set before expiry**

```powershell
gh api "repos/colayc/unitTest/actions/runs/$($foundationRun.databaseId)/artifacts?per_page=100" --jq '.artifacts[] | [.id,.name,.expired,.size_in_bytes,.digest] | @tsv'
gh run view $foundationRun.databaseId --repo colayc/unitTest --json conclusion,headSha,event,jobs,url
$releaseList = gh release list --repo colayc/unitTest --limit 100 --json tagName,isDraft,isPrerelease,publishedAt | ConvertFrom-Json
if (@($releaseList | Where-Object { $_.tagName -eq "0.1.0" }).Count -ne 0) { throw "Unexpected GitHub Release exists for 0.1.0" }
```

Expected: conclusion `success`, merged master head, no expired artifacts, Windows and Linux packages/manifests/license audits, both install-smoke evidence artifacts, and release-qualification evidence. Confirm the repository has no GitHub Release created for `0.1.0`.

- [ ] **Step 7: Report formal acceptance boundaries**

Report the producer run ID/URL, foundation run ID/URL, merged commit, package and evidence artifact names, and conclusions. State explicitly that this proves free unsigned cross-platform packaging and install qualification only; formal Windows signing and final third-party legal/license approval remain open Phase 8 items.
