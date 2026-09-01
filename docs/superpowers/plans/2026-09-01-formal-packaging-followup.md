# Formal Packaging Follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the two deterministic packaging blockers from foundation run `33457835747` without weakening the exact license inventory or AppImage filesystem contract, then prove the merged commit with a fresh unsigned producer/foundation qualification.

**Architecture:** Keep the two fixes local to their existing verifiers. The license audit globally sorts both complete inventories before exact comparison; the AppImage verifier uses a closed regular-file/symlink entry union and permits only root `.DirIcon -> unit-test-ide.svg`. Each subsystem has its own RED/GREEN commit, followed by a shared verification, review, and fresh-evidence flow.

**Tech Stack:** Node.js `>=24.18.0 <25`, pnpm `11.4.0`, Node test runner, ECMAScript modules, PowerShell 7, Git, GitHub CLI, GitHub Actions, Go and the repository-pinned CMake `4.3.4` bundle for the full gate.

**Spec:** `docs/superpowers/specs/2026-09-01-formal-packaging-followup-design.md`

## Global Constraints

- Preserve an exact, duplicate-free, size- and SHA-256-verified license set; only traversal-order dependence may change.
- Accept exactly one AppImage symlink: root `.DirIcon` with raw target `unit-test-ide.svg`.
- Do not follow `.DirIcon` to establish trust; validate its raw target and independently verify the root SVG as a non-executable regular file.
- Reject every other symbolic link, absolute or alternate target, alias, unsupported entry type, and unexpected payload path.
- Do not change `package-appimage.mjs`, either manifest schema, producer trust, artifact identity, workflows, signing, or publication policy.
- Production edits are limited to `tools/release/license-audit.mjs` and `tools/release/linux/verify-appimage.mjs` unless a scope expansion is explicitly reviewed.
- Test edits are limited to `tools/release/license-audit.test.mjs` and `tools/release/linux/package-appimage.test.mjs` unless a scope expansion is explicitly reviewed.
- Do not push either remote, create a PR, merge, synchronize `master`, or dispatch workflows before the corresponding explicit authorization.
- Do not publish a GitHub Release and keep `release_signing_required=0` throughout unsigned qualification.
- Preserve the existing `.release/` evidence in `C:\codex_project\unitTest\.worktrees\trusted-release-input-producer`; never delete or overwrite it.
- Producer run `33453419983` and foundation run `33457835747` remain diagnostic evidence only and cannot qualify the changed source commit.

## File Structure

| File | Responsibility in this change |
|---|---|
| `tools/release/license-audit.mjs` | Collect a real license tree, globally normalize path order, enforce exact membership, and verify size/digest. |
| `tools/release/license-audit.test.mjs` | Reproduce the sibling directory/file prefix collision and retain all negative license cases. |
| `tools/release/linux/verify-appimage.mjs` | Extract typed AppImage entries, require fixed package metadata, verify payload bytes/modes, and enforce the closed path set. |
| `tools/release/linux/package-appimage.test.mjs` | Model real `appimagetool` output in the fake envelope and exercise accepted/rejected `.DirIcon` records. |

No new production module is needed: both changes are small validation corrections inside existing ownership boundaries.

---

### Task 1: Compare the closed license set independently of traversal order

**Files:**
- Modify: `tools/release/license-audit.test.mjs:15-129`
- Modify: `tools/release/license-audit.mjs:211-278`

**Interfaces:**
- Consumes: `auditLicenses(stagingRoot: string): Promise<Array<{path: string, sha256: string, size: number}>>`.
- Produces: the same public return type and failure code, with `actualFiles` globally sorted by the same English collation used for manifest paths.

- [ ] **Step 1: Add a helper that appends real manifest-bound license files**

Add this helper after `createStagingFixture` in `tools/release/license-audit.test.mjs`:

```js
async function addManifestLicenseFiles(stagingRoot, entries) {
  const manifestPath = join(stagingRoot, "release-manifest.json");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  for (const [path, content] of entries) {
    await writeFixtureFile(stagingRoot, path, content);
    manifest.licenses.push({
      path,
      size: Buffer.byteLength(content),
      sha256: sha256(content),
    });
  }
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}
```

This helper writes both sides of the contract: the real staged file and its exact manifest record.

- [ ] **Step 2: Write the prefix-collision regression test**

Add this test after the primary success case:

```js
test("auditLicenses compares the closed file set independently of recursive traversal order", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const collisionEntries = [
      ["licenses/code-oss/a/z.txt", "nested notice\n"],
      ["licenses/code-oss/a-b.txt", "sibling notice\n"],
    ];
    await addManifestLicenseFiles(stagingRoot, collisionEntries);

    const result = await auditLicenses(stagingRoot);

    const collisionPaths = result
      .map(({ path }) => path)
      .filter((path) => path === collisionEntries[0][0] || path === collisionEntries[1][0]);
    assert.deepEqual(
      collisionPaths,
      collisionEntries.map(([path]) => path).sort((left, right) => left.localeCompare(right, "en")),
    );
  });
});
```

- [ ] **Step 3: Run the focused test and preserve RED**

Run:

```powershell
node --test --test-name-pattern="auditLicenses compares the closed file set" tools/release/license-audit.test.mjs
```

Expected: FAIL with `RELEASE_LICENSE_AUDIT_FAILED: license file set is not closed; missing=; unlisted=`. Empty differences demonstrate that membership is identical and only positional order is wrong.

- [ ] **Step 4: Apply the minimal global-order normalization**

In `auditLicenses`, replace the current collection assignment with:

```js
const actualFiles = (await collectLicenseFiles(root))
  .sort((left, right) => left.localeCompare(right, "en"));
const expectedFiles = licenses.map(({ path }) => path);
```

Do not alter the exact length/positional comparison, `missing`/`unlisted` diagnostics, duplicate manifest rejection, real-file checks, size checks, or SHA-256 checks.

- [ ] **Step 5: Run the regression and complete license audit file**

Run:

```powershell
node --test --test-name-pattern="auditLicenses compares the closed file set" tools/release/license-audit.test.mjs
node --test tools/release/license-audit.test.mjs
```

Expected: the focused test passes, then the complete license audit file passes with all existing missing, extra, duplicate, link, size, digest, CMake, and coverage cases intact.

- [ ] **Step 6: Review and commit the Windows/license fix**

Run:

```powershell
git diff --check -- tools/release/license-audit.mjs tools/release/license-audit.test.mjs
git diff -- tools/release/license-audit.mjs tools/release/license-audit.test.mjs
git add tools/release/license-audit.mjs tools/release/license-audit.test.mjs
git commit -m "fix: compare closed license set by global path order"
```

Expected: one focused commit that changes no public schema or audit failure code.

---

### Task 2: Accept only the fixed AppImage `.DirIcon` symlink

**Files:**
- Modify: `tools/release/linux/package-appimage.test.mjs:133-220`
- Modify: `tools/release/linux/package-appimage.test.mjs:356-421`
- Modify: `tools/release/linux/package-appimage.test.mjs:599-619`
- Modify: `tools/release/linux/verify-appimage.mjs:1-195`
- Modify: `tools/release/linux/verify-appimage.mjs:235-325`
- Modify: `tools/release/linux/verify-appimage.mjs:353-372`

**Interfaces:**
- Consumes: `verifyAppImage({image, manifest, requireDigest, extractor})`; an injected extractor returns `{files: Map<string, ExtractedEntry>, cleanup: () => Promise<void>}`.
- Produces: `ExtractedEntry = {kind: "file", content: Buffer, executable: boolean, size: number, sha256: string} | {kind: "symlink", target: string}`.
- Produces: a fixed `dirIcon` path `.DirIcon` and a requirement that its symlink target is exactly the fixed `icon` path `unit-test-ide.svg`.

- [ ] **Step 1: Make the fake tool describe regular files explicitly**

In the fake tool's `collect` function, change every regular record to include `kind: "file"`:

```js
files[relativePath] = {
  kind: "file",
  sha256: sha256(bytes),
  size: info.size,
  executable: (info.mode & 0o111) !== 0
    || relativePath === "AppRun"
    || relativePath.endsWith("/app/code-oss-runtime/code-oss")
    || relativePath.endsWith("/service/unit-test-service"),
  contentBase64: bytes.toString("base64"),
};
```

After collection and before constructing `payload`, model the metadata link created by real `appimagetool`:

```js
const files = await collect(appDir);
files[".DirIcon"] = {
  kind: "symlink",
  target: "unit-test-ide.svg",
};
const payload = {
  marker: "UNIT_TEST_IDE_FAKE_APPIMAGE",
  files,
};
```

- [ ] **Step 2: Teach the injected extractor the closed entry union**

Replace the loop body in `createFakeEnvelopeExtractor` with:

```js
for (const [relativePath, entry] of Object.entries(envelope.files)) {
  if (entry.kind === "symlink") {
    files.set(relativePath, {
      kind: "symlink",
      target: entry.target,
    });
    continue;
  }
  assert.equal(entry.kind, "file", `unexpected fake entry kind for ${relativePath}`);
  const content = Buffer.from(entry.contentBase64, "base64");
  files.set(relativePath, {
    kind: "file",
    size: content.length,
    sha256: sha256(content),
    executable: Boolean(entry.executable),
    content,
  });
}
```

Also add `kind: "file"` to the explicitly constructed `usr/lib/unit-test-ide/extra.txt` record so that its existing test continues to reach the closed-path rejection rather than failing entry-shape validation.

- [ ] **Step 3: Add the successful `.DirIcon` assertion**

In the normal package/verify success test, immediately after parsing the envelope, add:

```js
assert.deepEqual(envelope.files[".DirIcon"], {
  kind: "symlink",
  target: "unit-test-ide.svg",
});
```

- [ ] **Step 4: Add the complete `.DirIcon` rejection table**

Add this test after the existing icon rejection test:

```js
test("verifyAppImage accepts only the fixed root .DirIcon symlink", async (t) => {
  const cases = [
    ["missing", (files) => { delete files[".DirIcon"]; }, /\.DirIcon .*missing/u],
    ["regular file", (files) => {
      files[".DirIcon"] = {
        kind: "file",
        executable: false,
        contentBase64: Buffer.from("unit-test-ide.svg\n").toString("base64"),
      };
    }, /\.DirIcon .*symbolic link/u],
    ["wrong relative target", (files) => { files[".DirIcon"].target = "other.svg"; }, /\.DirIcon .*target/u],
    ["dot target", (files) => { files[".DirIcon"].target = "./unit-test-ide.svg"; }, /\.DirIcon .*target/u],
    ["parent target", (files) => { files[".DirIcon"].target = "../unit-test-ide.svg"; }, /\.DirIcon .*target/u],
    ["absolute target", (files) => { files[".DirIcon"].target = "/unit-test-ide.svg"; }, /\.DirIcon .*target/u],
    ["backslash target", (files) => { files[".DirIcon"].target = "icons\\unit-test-ide.svg"; }, /\.DirIcon .*target/u],
    ["drive target", (files) => { files[".DirIcon"].target = "C:/unit-test-ide.svg"; }, /\.DirIcon .*target/u],
    ["extra symlink", (files) => {
      files["unit-test-ide-link"] = {
        kind: "symlink",
        target: "unit-test-ide.svg",
      };
    }, /unsupported AppImage symlink: unit-test-ide-link/u],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate, expected] of cases) {
      const result = await packageWithFakeTool(join(root, name.replaceAll(" ", "-")));
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

- [ ] **Step 5: Run the AppImage tests and preserve RED**

Run:

```powershell
node --test --test-name-pattern="packageAppImage emits|fixed root .DirIcon" tools/release/linux/package-appimage.test.mjs
```

Expected: FAIL before production changes because the existing extraction boundary treats `.DirIcon` as an invalid non-file entry or the verifier does not include it in the closed path set.

- [ ] **Step 6: Add the fixed path and native extraction type model**

Add `readlink` to the `node:fs/promises` import and add the fixed path:

```js
import { lstat, mkdtemp, readFile, readdir, readlink, rm, stat } from "node:fs/promises";

const fixedPaths = {
  appRun: "AppRun",
  desktopEntry: "unit-test-ide.desktop",
  dirIcon: ".DirIcon",
  icon: "unit-test-ide.svg",
  launcher: "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss",
  releaseManifestPath: "usr/lib/unit-test-ide/release-manifest.json",
  payloadRoot: "usr/lib/unit-test-ide",
};
```

In `collectDirectoryFiles`, handle links before the regular-file branch and type every record:

```js
if (entry.isSymbolicLink()) {
  if (relativePath !== fixedPaths.dirIcon) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsupported AppImage symlink: ${relativePath}`);
  }
  files.set(relativePath, {
    kind: "symlink",
    target: await readlink(absolutePath),
  });
  continue;
}
if (!entry.isFile()) {
  throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsupported AppImage entry: ${relativePath}`);
}
const info = await stat(absolutePath);
const bytes = await readFile(absolutePath);
files.set(relativePath, {
  kind: "file",
  size: info.size,
  sha256: createHash("sha256").update(bytes).digest("hex"),
  executable: (info.mode & 0o111) !== 0,
  content: bytes,
});
```

This branch uses `readlink` and does not call `stat` or `readFile` on the symlink.

- [ ] **Step 7: Validate injected extraction records with the same closed union**

Replace the injected-extractor entry validation in `readImageFiles` with:

```js
for (const [relativePath, entry] of extracted.files.entries()) {
  if (!isPortableRelativePath(relativePath)) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsafe extracted AppImage path: ${relativePath}`);
  }
  if (entry?.kind === "symlink") {
    if (relativePath !== fixedPaths.dirIcon) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsupported AppImage symlink: ${relativePath}`);
    }
    if (typeof entry.target !== "string") {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid extracted AppImage symlink: ${relativePath}`);
    }
    continue;
  }
  if (
    entry?.kind !== "file"
    || !Buffer.isBuffer(entry.content)
    || typeof entry.executable !== "boolean"
  ) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid extracted AppImage entry: ${relativePath}`);
  }
  entry.size = entry.content.length;
  entry.sha256 = createHash("sha256").update(entry.content).digest("hex");
}
```

- [ ] **Step 8: Make regular-file and symlink requirements explicit**

Replace `requireFile` and add `requireSymlink`:

```js
function requireFile(files, relativePath, label) {
  const entry = files.get(relativePath);
  if (!entry) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} is missing from the AppImage: ${relativePath}`);
  }
  if (entry.kind !== "file") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} must be a regular file: ${relativePath}`);
  }
  return entry;
}

function requireSymlink(files, relativePath, target, label) {
  const entry = files.get(relativePath);
  if (!entry) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} is missing from the AppImage: ${relativePath}`);
  }
  if (entry.kind !== "symlink") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} must be a symbolic link: ${relativePath}`);
  }
  if (entry.target !== target) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} target must be exactly ${target}`);
  }
  return entry;
}
```

Add `fixedPaths.dirIcon` to the fixed entries in `expectedPayloadPaths`, and require it before content validation:

```js
const paths = new Set([
  fixedPaths.appRun,
  fixedPaths.desktopEntry,
  fixedPaths.dirIcon,
  fixedPaths.icon,
  fixedPaths.releaseManifestPath,
]);
```

```js
const appRun = requireFile(extraction.files, fixedPaths.appRun, "AppRun");
const desktopEntry = requireFile(extraction.files, fixedPaths.desktopEntry, "desktop entry");
requireSymlink(extraction.files, fixedPaths.dirIcon, fixedPaths.icon, ".DirIcon");
const icon = requireFile(extraction.files, fixedPaths.icon, "icon");
```

Do not expose `.DirIcon` in the sidecar or release manifest; it is a fixed verifier-owned metadata path like `AppRun`.

- [ ] **Step 9: Run the focused and complete AppImage tests**

Run:

```powershell
node --test --test-name-pattern="packageAppImage emits|fixed root .DirIcon" tools/release/linux/package-appimage.test.mjs
node --test tools/release/linux/package-appimage.test.mjs
```

Expected: all cases pass. The normal envelope contains the exact link, every alternate link target/type/path fails, and all existing SVG, launcher, manifest, artifact, license, fake-envelope, digest, and unexpected-path cases remain green.

- [ ] **Step 10: Review and commit the AppImage fix**

Run:

```powershell
git diff --check -- tools/release/linux/verify-appimage.mjs tools/release/linux/package-appimage.test.mjs
git diff -- tools/release/linux/verify-appimage.mjs tools/release/linux/package-appimage.test.mjs
git add tools/release/linux/verify-appimage.mjs tools/release/linux/package-appimage.test.mjs
git commit -m "fix: verify the fixed AppImage DirIcon link"
```

Expected: one focused commit; `tools/release/linux/package-appimage.mjs` and both manifest schemas remain unchanged.

---

### Task 3: Run the complete local release and repository verification gates

**Files:**
- No intended source changes.
- Verify: `tools/release/license-audit.test.mjs`
- Verify: `tools/release/linux/package-appimage.test.mjs`
- Verify: `tools/release/windows/package-msix.test.mjs`
- Verify: `tools/release/stage.test.mjs`
- Verify: `tools/release/qualification.test.mjs`
- Verify: `tools/release/update.test.mjs`
- Verify: `tools/release/producer/workflow-contract.test.mjs`

**Interfaces:**
- Consumes: the Task 1 and Task 2 commits on `codex/fix-formal-packaging-followup`.
- Produces: a clean branch with focused release coverage and the complete repository gate passing before any remote write.

- [ ] **Step 1: Run the focused release regression suite**

Run:

```powershell
node --test tools/release/license-audit.test.mjs tools/release/linux/package-appimage.test.mjs tools/release/windows/package-msix.test.mjs tools/release/stage.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/producer/workflow-contract.test.mjs
```

Expected: exit code 0; Windows packaging consumes the corrected license audit and the fake AppImage suite enforces the typed `.DirIcon` boundary.

- [ ] **Step 2: Ensure the fixed CMake bundle is available without widening runtime trust**

Run:

```powershell
pnpm prepare:cmake-bundle
```

Expected: the repository-pinned CMake `4.3.4` bundle is either fully revalidated and reused or downloaded from its fixed manifest URL and verified before publication. This is the only permitted network preparation for the following `pnpm verify`.

- [ ] **Step 3: Run the complete repository verification gate**

Run:

```powershell
pnpm verify
```

Expected: exit code 0 with generated contracts unchanged, TypeScript/Go builds passing, all unit tests passing, Go race tests passing, and E2E passing or using only pre-existing explicit environment gates.

- [ ] **Step 4: Remove only test-generated Python bytecode if present**

Run from the isolated worktree:

```powershell
$worktreeRoot = (Resolve-Path '.').Path
$cachePath = Join-Path $worktreeRoot 'tools\coverage-bundle\runner\__pycache__'
if (Test-Path -LiteralPath $cachePath) {
  $resolvedCache = (Resolve-Path -LiteralPath $cachePath).Path
  if (-not $resolvedCache.StartsWith($worktreeRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Refusing to remove generated cache outside the isolated worktree'
  }
  Remove-Item -LiteralPath $resolvedCache -Recurse -Force
}
```

Expected: only the known generated `__pycache__` is removed; no `.release/` evidence is touched.

- [ ] **Step 5: Audit the complete branch scope**

Run:

```powershell
git diff --check github/master...HEAD
git diff --name-status github/master...HEAD
git status --short --branch
git log --oneline --decorate github/master..HEAD
```

Expected: no whitespace errors, a clean working tree, and only the confirmed design, implementation plan, license fix, and AppImage verifier/test changes. Stop for review if any workflow, schema, `package-appimage.mjs`, signing, or publication file changed.

---

### Task 4: Publish the review branch to both remotes and open an unmerged PR

**Files:**
- No source changes.
- Remote branch: `codex/fix-formal-packaging-followup`
- GitHub base: `colayc/unitTest:master`
- Gitee mirror: `yc1211/unit-test`

**Interfaces:**
- Consumes: a clean, fully verified Task 3 branch.
- Produces: identical branch heads on GitHub and Gitee plus one GitHub PR with passing required checks.

- [ ] **Step 1: Obtain explicit authorization for both pushes and PR creation**

Ask the user to authorize pushing `codex/fix-formal-packaging-followup` to GitHub `colayc/unitTest` and Gitee `yc1211/unit-test`, and creating a GitHub PR without merging it.

Expected: direct authorization naming the branch/remotes and confirming that the PR must remain unmerged. Do not run later Task 4 steps without it.

- [ ] **Step 2: Push the exact branch commit to both remotes**

Run:

```powershell
git push -u github codex/fix-formal-packaging-followup
git push -u origin codex/fix-formal-packaging-followup
$localHead = (git rev-parse HEAD).Trim()
$githubHead = ((git ls-remote github refs/heads/codex/fix-formal-packaging-followup) -split "`t")[0]
$giteeHead = ((git ls-remote origin refs/heads/codex/fix-formal-packaging-followup) -split "`t")[0]
if ($localHead -cne $githubHead -or $localHead -cne $giteeHead) {
  throw 'Remote feature branch heads do not match the reviewed local commit'
}
```

Expected: all three hashes are identical.

- [ ] **Step 3: Create the GitHub PR without merging**

Run:

```powershell
$prUrl = gh pr create --repo colayc/unitTest --base master --head codex/fix-formal-packaging-followup --title "fix: close formal packaging follow-up gaps" --body "Fix the two deterministic failures from unsigned foundation run 33457835747. License inventory comparison now globally normalizes both exact closed path sets while preserving duplicate, membership, size, digest, and link rejection. AppImage verification models typed entries and accepts only root .DirIcon with the raw target unit-test-ide.svg; every other link, target, alias, and unexpected path remains rejected. No manifest schema, workflow, signing, or publication behavior changes."
$prUrl
```

Expected: one new GitHub PR URL; no merge occurs.

- [ ] **Step 4: Wait for required PR checks and inspect merge readiness**

Run:

```powershell
gh pr checks --repo colayc/unitTest --watch --interval 10
gh pr view --repo colayc/unitTest --json number,url,headRefOid,mergeStateStatus,mergeable,statusCheckRollup
```

Expected: Linux and Windows checks succeed, `headRefOid` equals the reviewed branch commit, and the PR is mergeable. Stop and request separate explicit merge authorization.

---

### Task 5: Merge with authorization and produce fresh unsigned qualification evidence

**Files:**
- No source changes.
- Evidence producer: `.github/workflows/release-inputs.yml`
- Evidence consumer: `.github/workflows/foundation.yml`
- Local evidence: untracked `.release/evidence/producer-$producerRunId/release-input-provenance.json`

**Interfaces:**
- Consumes: the explicitly approved GitHub PR with passing required checks.
- Produces: synchronized GitHub/Gitee `master`, a fresh successful producer, a fresh successful unsigned foundation run, inspected short-lived artifacts, and confirmation that no GitHub Release exists.

- [ ] **Step 1: Obtain explicit merge and master synchronization authorization**

Ask the user to authorize rebase-merging the exact PR, deleting the GitHub feature branch, and synchronizing the resulting GitHub `master` commit to Gitee `master`.

Expected: direct authorization. Do not merge or update either `master` without it.

- [ ] **Step 2: Merge and prove both master refs are identical**

Run:

```powershell
$prNumber = gh pr view --repo colayc/unitTest --json number --jq '.number'
gh pr merge $prNumber --repo colayc/unitTest --rebase --delete-branch
git fetch github master
git push origin github/master:master
git fetch origin master
$mergedCommit = (git rev-parse github/master).Trim()
$giteeCommit = (git rev-parse origin/master).Trim()
if ($mergedCommit -cne $giteeCommit) {
  throw 'GitHub and Gitee master commits differ'
}
$mergedCommit
```

Expected: GitHub and Gitee `master` resolve to one new merged commit that is not `d27c6cae9c864810acee7e2c6924894b8ccb4ece`.

- [ ] **Step 3: Dispatch and watch a fresh trusted producer**

Run:

```powershell
$previousProducerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousProducerRunId = if ($null -eq $previousProducerRun) { '' } else { [string]$previousProducerRun.databaseId }
gh workflow run release-inputs.yml --repo colayc/unitTest --ref master
do {
  Start-Sleep -Seconds 5
  $producerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,createdAt,headSha,status,url | ConvertFrom-Json
} while ($null -eq $producerRun -or [string]$producerRun.databaseId -eq $previousProducerRunId)
if ([string]$producerRun.headSha -cne $mergedCommit) {
  throw 'Producer run is not using the merged master commit'
}
$producerRun | ConvertTo-Json -Compress
gh run watch $producerRun.databaseId --repo colayc/unitTest --exit-status --interval 10
```

Expected: authorize, Windows build, Linux build, and attestation all succeed on the new merged commit.

- [ ] **Step 4: Download and validate the fresh closed provenance**

Run:

```powershell
$producerRunId = [string]$producerRun.databaseId
$evidenceRoot = Join-Path $PWD ".release/evidence/producer-$producerRunId"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
gh run download $producerRunId --repo colayc/unitTest --name release-input-provenance-1 --dir $evidenceRoot
$provenancePath = Join-Path $evidenceRoot 'release-input-provenance.json'
$provenance = Get-Content -LiteralPath $provenancePath -Raw | ConvertFrom-Json
if ([string]$provenance.producer.runId -cne $producerRunId) { throw 'Provenance run ID mismatch' }
if ([string]$provenance.producer.sourceCommit -cne $mergedCommit) { throw 'Provenance source commit mismatch' }
if ([string]$provenance.producer.event -cne 'workflow_dispatch') { throw 'Provenance event mismatch' }
$provenance | ConvertTo-Json -Depth 20
```

Expected: one path-free closed document for the fresh producer, containing Windows launcher, Linux launcher, and appimagetool identities. Keep this evidence untracked and do not delete older evidence.

- [ ] **Step 5: Dispatch a fresh free unsigned foundation run**

Run:

```powershell
$windowsSha = [string]$provenance.runtimes.windows.launcherSha256
$linuxSha = [string]$provenance.runtimes.linux.launcherSha256
$appImageToolSha = [string]$provenance.appimagetool.sha256
foreach ($digest in @($windowsSha, $linuxSha, $appImageToolSha)) {
  if ($digest -cnotmatch '^[0-9a-f]{64}$') {
    throw 'Provenance contains an invalid SHA-256 value'
  }
}
$previousFoundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousFoundationRunId = if ($null -eq $previousFoundationRun) { '' } else { [string]$previousFoundationRun.databaseId }
gh workflow run foundation.yml --repo colayc/unitTest --ref master `
  -f release_version=0.1.0 `
  -f release_signing_required=0 `
  -f release_input_run_id=$producerRunId `
  -f windows_code_oss_sha256=$windowsSha `
  -f linux_code_oss_sha256=$linuxSha `
  -f linux_appimagetool_sha256=$appImageToolSha
```

Expected: dispatch accepted with signing disabled; no tag or GitHub Release is created.

- [ ] **Step 6: Identify and watch the new foundation run**

Run:

```powershell
do {
  Start-Sleep -Seconds 5
  $foundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,headSha,status,url | ConvertFrom-Json
} while ($null -eq $foundationRun -or [string]$foundationRun.databaseId -eq $previousFoundationRunId)
if ([string]$foundationRun.headSha -cne $mergedCommit) {
  throw 'Foundation run is not using the merged master commit'
}
$foundationRun | ConvertTo-Json -Compress
gh run watch $foundationRun.databaseId --repo colayc/unitTest --exit-status --interval 10
```

Expected: `verify-release-input-run`, both platform verification jobs, `package-windows`, `package-linux`, both install-smoke jobs, and `release-qualification` all succeed.

- [ ] **Step 7: Inspect artifacts and prove publication stayed disabled**

Run:

```powershell
gh api "repos/colayc/unitTest/actions/runs/$($foundationRun.databaseId)/artifacts?per_page=100" --jq '.artifacts[] | [.id,.name,.expired,.size_in_bytes,.digest] | @tsv'
gh run view $foundationRun.databaseId --repo colayc/unitTest --json conclusion,headSha,event,jobs,url
$releaseList = gh release list --repo colayc/unitTest --limit 100 --json tagName,isDraft,isPrerelease,publishedAt | ConvertFrom-Json
if (@($releaseList | Where-Object { $_.tagName -eq '0.1.0' }).Count -ne 0) {
  throw 'Unexpected GitHub Release exists for 0.1.0'
}
```

Expected: the run concludes `success`, artifacts are unexpired and contain the Windows/Linux packages, manifests, license audits, install-smoke evidence, and qualification evidence; no `0.1.0` GitHub Release exists.

- [ ] **Step 8: Report the exact acceptance boundary**

Report the merged commit, both remote master hashes, producer run ID/URL, foundation run ID/URL, three producer digests, package/evidence artifact names, and job conclusions. State explicitly that the evidence proves free unsigned cross-platform packaging and install qualification only; Windows signing and final third-party legal/license approval remain outside this acceptance.
