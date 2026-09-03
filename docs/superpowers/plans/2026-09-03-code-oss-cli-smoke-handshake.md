# Code-OSS CLI Install-Smoke Handshake Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Windows and Linux install smoke execute the installed manifest-bound Code-OSS CLI through the installed root Electron binary, while preserving corruption, rollback, isolation, timeout, and fail-closed evidence contracts.

**Architecture:** Keep the root application launcher as the trusted executable and corruption target, but invoke it in Electron-as-Node mode with the installed `resources/app/out/cli.js` script. The same shell-free handshake is used for first install, corrupted-upgrade failure, and rollback; no workflow, package format, schema, signing, or publication behavior changes.

**Tech Stack:** Node.js 24, ECMAScript modules, Node built-in test runner, `spawnSync`, pnpm 11.4.0, Go 1.26.6, CMake 4.3.4, GitHub Actions, GitHub CLI, PowerShell 7.

## Global Constraints

- Work only in `C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake` on branch `codex/fix-cli-launch-handshake`.
- The reviewed base is GitHub `master` commit `13d46b9e4ba9677a557b375c35d186412e8635af`; the design commit is `2c9c5cb`.
- Production changes are limited to `tools/release/update.mjs`; regression changes are limited to `tools/release/update.test.mjs`.
- The application launcher and corruption target remain `app/code-oss-runtime/Code - OSS.exe` on Windows and `app/code-oss-runtime/code-oss` on Linux.
- The installed CLI path is exactly `app/code-oss-runtime/resources/app/out/cli.js` on both platforms.
- Invoke the root executable directly with no shell, wrapper, `AppRun`, GUI automation, retry, or `--no-sandbox` flag.
- Pass exactly the CLI script, `--version`, `--user-data-dir`, and the isolated smoke workspace root.
- Set `ELECTRON_RUN_AS_NODE=1` and `VSCODE_DEV=` while retaining the isolated `HOME`, `USERPROFILE`, `XDG_CACHE_HOME`, and `XDG_CONFIG_HOME` values.
- Keep the timeout exactly `30_000` milliseconds and keep success defined as status `0` plus non-whitespace stdout.
- Keep launcher corruption, manifest verification, closed-set checks, reparse/symlink rejection, containment, size, SHA-256, rollback, uninstall, user-data preservation, diagnostics precedence, and evidence schemas unchanged.
- Do not modify workflows, staging, package entry points, producer code, schemas, signing, secrets, tags, or GitHub Release behavior.
- Keep `.release/`, `.producer/`, `.superpowers/runtime/`, generated packages, downloaded runtimes, and test evidence untracked.
- Stop for another design review if implementation requires any tracked file outside the design, plan, `tools/release/update.mjs`, and `tools/release/update.test.mjs`.
- Require explicit user authorization before pushing either remote, creating a PR, merging, synchronizing `master`, or dispatching producer/foundation workflows.
- A manual foundation run must use `release_version=0.1.0` and `release_signing_required=0`; do not publish a release or enable signing.

---

## File Structure

- Modify `tools/release/update.test.mjs`: add a manifest-bound CLI fixture, assert the exact environment/argument contract, prove first-install and rollback execution with external markers, and cover missing CLI, nonzero exit, timeout, and whitespace output.
- Modify `tools/release/update.mjs`: change only the private `launchHandshake(packageRoot, version, userDataRoot)` spawn request so the installed root Electron binary executes the installed CLI script.
- Create no new production or test module. The handshake belongs beside the existing launcher path, lifecycle, corruption, rollback, and diagnostic logic.
- Preserve evidence only under ignored `.release/evidence/`; do not add generated evidence to Git.

---

### Task 1: Specify and implement the shell-free Code-OSS CLI handshake

**Files:**
- Modify: `tools/release/update.test.mjs:19-180`
- Modify: `tools/release/update.test.mjs:237-352`
- Modify: `tools/release/update.mjs:532-557`
- Verify: `tools/release/update.test.mjs`

**Interfaces:**
- Consumes: existing private `launcherRelativePath(): string`, `runSmokeLifecycle(inputs, { launch }?)`, `requireLaunchHandshake(result, label)`, and `triggerInstalledUpgradeLaunchFailure(root, version, userDataRoot)`.
- Produces: private `launchHandshake(packageRoot: string, version: string, userDataRoot: string)` returning the UTF-8 `spawnSync` result for the installed root executable and installed CLI script.
- Produces in tests: `cliRelativePath: string`, `createCliFixtureSource(options): string`, extended `createArtifact(..., options)`, and `createSmokeInputs(root, options)`.
- `createCliFixtureSource` options are `{ expectedUserDataRoot: string, version: string, markerPath?: string, exitCode?: number }`.
- `createArtifact` adds `{ includeCli?: boolean, cliExitCode?: number, cliMarkerPath?: string, expectedUserDataRoot?: string }`; defaults are `true`, `0`, `undefined`, and `join(root, "disposable-smoke-root", "workspace")` respectively. `expectedUserDataRoot` is a test-only exact string and is compared with complete equality; path sets or fuzzy matching are not permitted.
- `createSmokeInputs` options are `{ baselineArtifactOptions?: object, targetArtifactOptions?: object }` and it returns the complete valid input object accepted by `runSmokeLifecycle`.

- [ ] **Step 1: Select a compatible local Node and exact pnpm before editing**

Run from the isolated worktree:

```powershell
$node = 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe'
$nodeVersion = (& $node --version).Trim()
if ($nodeVersion -notmatch '^v24\.') { throw "Node 24 is required, found $nodeVersion" }
$nodeBin = Split-Path -Parent $node
$env:Path = "$nodeBin;$env:Path"
$corepack = 'C:\Program Files\nodejs\corepack.cmd'
$env:COREPACK_HOME = Join-Path $PWD '.superpowers\runtime\corepack'
$pnpmVersion = (& $corepack pnpm --version).Trim()
if ($pnpmVersion -cne '11.4.0') { throw "pnpm 11.4.0 is required, found $pnpmVersion" }
& $node --version
& $corepack pnpm --version
```

Expected: Node reports a `v24.x` release and Corepack resolves the repository-pinned pnpm `11.4.0`. Do not use the system Node `v20.15.0` or globally exposed pnpm `11.19.0`.

- [ ] **Step 2: Add a manifest-bound CLI fixture that enforces the exact handshake**

Add the fixed path beside `launcherRelativePath` and `productRelativePath`:

```js
const cliRelativePath = "app/code-oss-runtime/resources/app/out/cli.js";
```

Add this helper before `createArtifact`:

```js
function createCliFixtureSource({
  expectedUserDataRoot,
  version,
  markerPath,
  exitCode = 0,
}) {
  const expectedArguments = ["--version", "--user-data-dir", expectedUserDataRoot];
  const markerStatement = markerPath === undefined
    ? ""
    : `appendFileSync(${JSON.stringify(markerPath)}, ${JSON.stringify(`${version}\n`)});`;
  return `"use strict";
const { appendFileSync } = require("node:fs");
const actualArguments = process.argv.slice(2);
const expectedArguments = ${JSON.stringify(expectedArguments)};
if (process.env.ELECTRON_RUN_AS_NODE !== "1") {
  process.stderr.write("fixture CLI requires ELECTRON_RUN_AS_NODE=1\\n");
  process.exit(71);
}
if (process.env.VSCODE_DEV !== "") {
  process.stderr.write("fixture CLI requires empty VSCODE_DEV\\n");
  process.exit(72);
}
if (JSON.stringify(actualArguments) !== JSON.stringify(expectedArguments)) {
  process.stderr.write("fixture CLI argument mismatch\\n");
  process.exit(73);
}
if (${exitCode} !== 0) {
  process.stderr.write(${JSON.stringify(`fixture CLI exit ${exitCode}\n`)});
  process.exit(${exitCode});
}
${markerStatement}
process.stdout.write(${JSON.stringify(`${version}\n`)});
`;
}
```

Extend the existing `createArtifact` parameter list exactly as follows:

```js
async function createArtifact(root, version, {
  launcher = `launch ${version}\n`,
  launcherSource,
  manifestGeneratedAt = generatedAt,
  manifestSourceCommit = sourceCommit,
  includeCli = true,
  cliExitCode = 0,
  cliMarkerPath,
  expectedUserDataRoot = join(root, "disposable-smoke-root", "workspace"),
} = {}) {
```

For every normal fixture, write the CLI script and bind its exact bytes into the manifest between the launcher and product records:

```js
const cliBytes = includeCli
  ? Buffer.from(createCliFixtureSource({
    expectedUserDataRoot,
    version,
    markerPath: cliMarkerPath,
    exitCode: cliExitCode,
  }))
  : null;
if (cliBytes !== null) await writeFixtureFile(artifactRoot, cliRelativePath, cliBytes);
```

Insert this conditional manifest record after `app-code-oss` and before `app-code-oss-product`:

```js
...(cliBytes === null ? [] : [{
  id: "app-code-oss-cli",
  kind: "runtime",
  relativePath: cliRelativePath,
  size: cliBytes.length,
  sha256: sha256(cliBytes),
  executable: false,
}]),
```

Expected: every normal package-backed fixture contains a real, manifest-verified CLI script. The script succeeds only for the reviewed environment and exact three CLI arguments.

- [ ] **Step 3: Factor complete valid smoke inputs for focused failure cases**

Add this helper after `withTemporaryRoot`:

```js
async function createSmokeInputs(root, {
  baselineArtifactOptions = {},
  targetArtifactOptions = {},
} = {}) {
  const baselineArtifact = await createArtifact(root, "1.0.0", {
    manifestGeneratedAt: baselineGeneratedAt,
    manifestSourceCommit: baselineSourceCommit,
    ...baselineArtifactOptions,
  });
  const artifact = await createArtifact(root, "2.0.0", targetArtifactOptions);
  const packagePath = await writeFixtureFile(
    root,
    "downloads/unit-test-ide-2.0.0.package",
    "real package bytes\n",
  );
  const baselinePackagePath = await writeFixtureFile(
    root,
    "downloads/unit-test-ide-1.0.0.package",
    "real baseline package bytes\n",
  );
  return {
    artifact,
    baselineArtifact,
    baselineManifestSha256: sha256(await readFile(join(baselineArtifact, "release-manifest.json"))),
    baselinePackagePath,
    baselinePackageSha256: sha256(await readFile(baselinePackagePath)),
    evidence: join(root, "install-smoke.json"),
    manifestSha256: sha256(await readFile(join(artifact, "release-manifest.json"))),
    packagePath,
    packageSha256: sha256(await readFile(packagePath)),
    platform,
    root: join(root, "disposable-smoke-root"),
    version: "2.0.0",
  };
}
```

Use this helper to remove only the repeated artifact/package/digest setup from the existing whitespace-output test. Keep its injected result and exact error assertion unchanged.

- [ ] **Step 4: Write the production-backed success, missing-CLI, and nonzero-CLI regressions**

Rename the existing production test to:

```js
test("package-backed production smoke executes the installed CLI before and after rollback", async (t) => {
```

Inside it, create `workspaceRoot` and a marker outside the package-owned root before creating both artifacts:

```js
const workspaceRoot = join(root, "disposable-smoke-root", "workspace");
const cliMarkerPath = join(root, "cli-handshake-markers.txt");
```

Replace the two artifact constructions so both installed versions write to the same external marker:

```js
const baselineArtifact = await createArtifact(root, "1.0.0", {
  cliMarkerPath,
  launcherSource,
  manifestGeneratedAt: baselineGeneratedAt,
  manifestSourceCommit: baselineSourceCommit,
});
const artifact = await createArtifact(root, "2.0.0", {
  cliMarkerPath,
  launcherSource,
  expectedUserDataRoot: join(root, "disposable-smoke-root", "workspace"),
});
```

Replace the direct `--version` source probe with the actual CLI semantic core, then clear the probe marker before the lifecycle:

```js
const sourceCli = join(artifact, ...cliRelativePath.split("/"));
const sourceLauncherProbe = spawnSync(
  sourceLauncher,
  [sourceCli, "--version", "--user-data-dir", workspaceRoot],
  {
    encoding: "utf8",
    env: { ...process.env, ELECTRON_RUN_AS_NODE: "1", VSCODE_DEV: "" },
    windowsHide: true,
  },
);
assert.equal(sourceLauncherProbe.status, 0, sourceLauncherProbe.stderr);
assert.equal(sourceLauncherProbe.stdout.trim(), "2.0.0");
await rm(cliMarkerPath, { force: true });
```

After the existing source/product/corruption assertions, prove only the first baseline launch and rollback launch succeeded through the CLI:

```js
const cliMarkers = (await readFile(cliMarkerPath, "utf8")).trim().split(/\r?\n/u);
assert.deepEqual(cliMarkers, ["1.0.0", "1.0.0"]);
```

Add these two tests after the production-backed success test:

```js
test("package-backed smoke fails closed when the installed CLI script is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const inputs = await createSmokeInputs(root, {
      baselineArtifactOptions: { includeCli: false, launcherSource: process.execPath },
      targetArtifactOptions: { includeCli: false, launcherSource: process.execPath },
    });
    await assert.rejects(
      () => runSmokeLifecycle(inputs),
      (error) => error?.code === "RELEASE_SMOKE_FAILED",
    );
  });
});

test("package-backed smoke fails closed when the installed CLI exits nonzero", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const inputs = await createSmokeInputs(root, {
      baselineArtifactOptions: { cliExitCode: 23, launcherSource: process.execPath },
      targetArtifactOptions: { launcherSource: process.execPath },
    });
    await assert.rejects(
      () => runSmokeLifecycle(inputs),
      (error) => {
        assert.equal(error?.code, "RELEASE_SMOKE_FAILED");
        assert.equal(
          error?.message,
          "RELEASE_SMOKE_FAILED: first launch handshake failed: fixture CLI exit 23",
        );
        return true;
      },
    );
  });
});
```

Add a bounded-result regression without sleeping for 30 seconds:

```js
test("package-backed smoke reports a timed out launch handshake usefully", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const inputs = await createSmokeInputs(root);
    const timeoutError = Object.assign(
      new Error("spawnSync Code - OSS.exe ETIMEDOUT"),
      { code: "ETIMEDOUT" },
    );
    await assert.rejects(
      () => runSmokeLifecycle(inputs, {
        launch: () => ({
          status: null,
          signal: null,
          error: timeoutError,
          stdout: "",
          stderr: "",
        }),
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_SMOKE_FAILED");
        assert.equal(
          error?.message,
          "RELEASE_SMOKE_FAILED: first launch handshake failed: spawnSync Code - OSS.exe ETIMEDOUT",
        );
        return true;
      },
    );
  });
});
```

Expected: the marker is outside `package-owned`, the corrupted target never adds a successful marker, and error diagnostics still prefer spawn error, then stderr, then exit/signal plus stdout classification.

- [ ] **Step 5: Run the new production-backed contract in RED state**

Run before changing `tools/release/update.mjs`:

```powershell
& $node --test --test-name-pattern="package-backed production smoke executes|installed CLI script is missing|installed CLI exits nonzero" tools/release/update.test.mjs
```

Expected RED:

- the production-backed lifecycle finishes without creating `cli-handshake-markers.txt` because the root Node executable handles `--version` itself;
- the missing-CLI test reports a missing expected rejection; and
- the nonzero-CLI test reports a missing expected rejection.

The source CLI sanity probe itself must pass. Preserve the complete RED output under ignored `.superpowers/sdd/2026-09-03-code-oss-cli-smoke-handshake/`.

- [ ] **Step 6: Implement the minimal installed CLI invocation**

Replace only `launchHandshake` with:

```js
function launchHandshake(packageRoot, version, userDataRoot) {
  const versionRoot = join(packageRoot, "versions", version);
  const executable = join(versionRoot, ...launcherRelativePath().split("/"));
  const cli = join(
    versionRoot,
    "app",
    "code-oss-runtime",
    "resources",
    "app",
    "out",
    "cli.js",
  );
  return spawnSync(executable, [cli, "--version", "--user-data-dir", userDataRoot], {
    encoding: "utf8",
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: "1",
      VSCODE_DEV: "",
      HOME: userDataRoot,
      USERPROFILE: userDataRoot,
      XDG_CACHE_HOME: join(userDataRoot, "cache"),
      XDG_CONFIG_HOME: join(userDataRoot, "config"),
    },
    timeout: 30_000,
    windowsHide: true,
  });
}
```

Do not export this function, pre-launch a wrapper, add an existence shortcut, change `requireLaunchHandshake`, or change the target corrupted by `triggerInstalledUpgradeLaunchFailure`.

- [ ] **Step 7: Prove GREEN for the complete handshake boundary**

Run:

```powershell
& $node --test --test-name-pattern="package-backed production smoke executes|installed CLI script is missing|installed CLI exits nonzero|timed out launch handshake|whitespace-only first-launch" tools/release/update.test.mjs
& $node --test tools/release/update.test.mjs
```

Expected: every selected case passes; the complete update test file has zero failures and only pre-existing explicit platform skips. The real makeappx test runs when the Windows SDK tool is available and uses the manifest-bound fixture CLI.

The real makeappx fixture must pass `expectedUserDataRoot: join(root, "disposable-smoke-root", "lifecycle", "workspace")` to each artifact because the wrapper invokes the lifecycle beneath the `lifecycle` root. The fixture must retain complete equality checking of the three arguments; do not accept a path set or fuzzy match.

- [ ] **Step 8: Review and commit the focused implementation**

Run:

```powershell
git diff --check
git diff --name-status
git diff -- tools/release/update.mjs tools/release/update.test.mjs
```

Expected: no whitespace errors and only the two implementation files are uncommitted. Confirm the diff contains no shell, retry, `--no-sandbox`, workflow, schema, or evidence-key change.

Commit:

```powershell
git add -- tools/release/update.mjs tools/release/update.test.mjs
git commit -m "fix: run install smoke through Code-OSS CLI"
```

---

### Task 2: Qualify the implementation locally and against the real Windows runtime

**Files:**
- No intended tracked source changes.
- Verify: all test files under `tools/release/`.
- Verify: complete repository gate and native E2E.
- Read only: `C:\codex_project\unitTest\.worktrees\fix-update-file-set-order\.release\evidence\producer-33494864475\windows-runtime`.
- Preserve untracked evidence under `.release/evidence/cli-handshake-local/`.

**Interfaces:**
- Consumes: Task 1 commit and the retained real Windows Code-OSS `1.92.0` runtime from trusted producer run `33494864475`.
- Produces: focused release, repository-wide, native, and real-runtime evidence for the exact reviewed branch commit.
- The real-runtime probe uses the same executable, CLI, arguments, environment, isolation directories, and 30-second bound as production.

- [ ] **Step 1: Run every release test with Node 24**

Run:

```powershell
& $node --test `
  tools/release/code-oss-runtime.test.mjs `
  tools/release/license-audit.test.mjs `
  tools/release/linux/package-appimage.test.mjs `
  tools/release/linux/runtime-mode-inventory.test.mjs `
  tools/release/manifest.test.mjs `
  tools/release/portable-path.test.mjs `
  tools/release/producer/provenance.test.mjs `
  tools/release/producer/runtime-inventory.test.mjs `
  tools/release/producer/source-manifest.test.mjs `
  tools/release/producer/trusted-run.test.mjs `
  tools/release/producer/workflow-contract.test.mjs `
  tools/release/qualification.test.mjs `
  tools/release/real-runtime.e2e.test.mjs `
  tools/release/stage.test.mjs `
  tools/release/update.test.mjs `
  tools/release/windows/package-msix.test.mjs
```

Expected: zero failures. Tests requiring the other operating system or unavailable external tools may use only their existing explicit skip gates.

- [ ] **Step 2: Reproduce the reviewed CLI semantic core with the retained real Windows runtime**

Run this shell-free Node probe:

```powershell
$runtimeRoot = 'C:\codex_project\unitTest\.worktrees\fix-update-file-set-order\.release\evidence\producer-33494864475\windows-runtime'
$executable = Join-Path $runtimeRoot 'Code - OSS.exe'
$cli = Join-Path $runtimeRoot 'resources\app\out\cli.js'
if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) { throw 'Retained Code-OSS executable is missing' }
if (-not (Test-Path -LiteralPath $cli -PathType Leaf)) { throw 'Retained Code-OSS CLI is missing' }
$launcherSha = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
if ($launcherSha -cne '1C777E2EE43BACF066AE344142C25ADABD21CFA09BA7E7A9DC9DA6D0185A8672') {
  throw "Retained Code-OSS launcher digest mismatch: $launcherSha"
}
$probeRoot = Join-Path ([IO.Path]::GetTempPath()) "unit-test-ide-cli-probe-$([guid]::NewGuid().ToString('N'))"
$userDataRoot = Join-Path $probeRoot 'workspace'
New-Item -ItemType Directory -Force -Path $userDataRoot | Out-Null
$beforeProcessIds = @(Get-Process -Name 'Code - OSS' -ErrorAction SilentlyContinue | ForEach-Object Id)
$probe = @'
const { spawnSync } = require("node:child_process");
const { join } = require("node:path");
const [executable, cli, userDataRoot] = process.argv.slice(1);
const result = spawnSync(
  executable,
  [cli, "--version", "--user-data-dir", userDataRoot],
  {
    encoding: "utf8",
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: "1",
      VSCODE_DEV: "",
      HOME: userDataRoot,
      USERPROFILE: userDataRoot,
      XDG_CACHE_HOME: join(userDataRoot, "cache"),
      XDG_CONFIG_HOME: join(userDataRoot, "config"),
    },
    timeout: 30_000,
    windowsHide: true,
  },
);
const lines = typeof result.stdout === "string"
  ? result.stdout.trim().split(/\r?\n/u)
  : [];
const expected = ["1.92.0", "b1c0a14de1414fcdaa400695b4db1c0799bc3124", "x64"];
if (result.status !== 0 || JSON.stringify(lines) !== JSON.stringify(expected)) {
  process.stderr.write(JSON.stringify({
    status: result.status,
    signal: result.signal,
    error: result.error?.message ?? "",
    stderr: result.stderr ?? "",
    lines,
  }));
  process.exit(1);
}
process.stdout.write(`${JSON.stringify({ status: result.status, lines })}\n`);
'@
try {
  & $node -e $probe $executable $cli $userDataRoot
  if ($LASTEXITCODE -ne 0) { throw 'Real Code-OSS CLI probe failed' }
  Start-Sleep -Milliseconds 500
  $unexpectedProcesses = @(Get-Process -Name 'Code - OSS' -ErrorAction SilentlyContinue | Where-Object { $_.Id -notin $beforeProcessIds })
  if ($unexpectedProcesses.Count -ne 0) { throw 'Real CLI probe left a graphical Code-OSS process running' }
} finally {
  $resolvedProbeRoot = [IO.Path]::GetFullPath($probeRoot)
  $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
  if (-not $resolvedProbeRoot.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Refusing to remove a CLI probe root outside the temporary directory'
  }
  Remove-Item -LiteralPath $resolvedProbeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
```

Expected: status `0`, exactly the three expected version lines, the trusted launcher SHA-256 matches, the user-data path is isolated, and no new graphical Code-OSS process remains.

- [ ] **Step 3: Prepare pinned bundles and run the complete repository gate**

Run through Corepack so pnpm remains exactly `11.4.0`:

```powershell
& $corepack pnpm prepare:cmake-bundle
& $corepack pnpm prepare:coverage-bundle
& $corepack pnpm verify
& $corepack pnpm test:e2e:native
```

Expected: CMake `4.3.4` and the fixed coverage bundle validate, generated files remain unchanged, TypeScript and Go builds pass, unit tests pass, Go race tests pass, regular E2E passes, and the native E2E completes successfully. A new toolchain failure is not an acceptable skip.

- [ ] **Step 4: Remove only known generated caches and audit the complete branch**

If verification created Python bytecode, remove only this checked path:

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
git diff --check github/master...HEAD
git diff --name-status github/master...HEAD
git status --short --branch
git log --oneline --decorate github/master..HEAD
```

Expected: the branch contains only the approved design, this implementation plan, `tools/release/update.mjs`, and `tools/release/update.test.mjs`; the worktree is clean. No workflow, schema, package, signing, tag, or publication file is changed.

- [ ] **Step 5: Request independent review and re-run affected gates**

Invoke `superpowers:requesting-code-review` against `github/master...HEAD`. Require the reviewer to check:

- the first and rollback handshakes use the installed CLI through the installed root executable;
- the corrupted target still fails through the corrupted root executable;
- test markers prove exactly two successful CLI executions;
- missing CLI, nonzero exit, timeout, and whitespace stdout remain fail-closed;
- no shell, retry, sandbox bypass, schema, workflow, signing, or publication change exists.

If the reviewer finds an in-scope defect, fix only the two implementation files, rerun Task 1 Step 7 and Task 2 Steps 1-4, and create a focused follow-up commit. Any requested change outside the global scope stops for design review.

Expected: no unresolved review finding and a clean locally qualified branch. Stop before any remote write.

---

### Task 3: Push the reviewed branch to GitHub and Gitee and open an unmerged PR

**Files:**
- No source changes.
- Remote branch: `codex/fix-cli-launch-handshake`.
- GitHub base: `colayc/unitTest:master`.
- Gitee mirror: `yc1211/unit-test`.

**Interfaces:**
- Consumes: the clean, fully verified and independently reviewed Task 2 branch.
- Produces: identical feature-branch heads on both remotes and one unmerged GitHub PR whose checks are successful.

- [ ] **Step 1: Obtain explicit dual-remote push and PR authorization**

Ask the user to authorize pushing `codex/fix-cli-launch-handshake` to GitHub `colayc/unitTest` and Gitee `yc1211/unit-test`, and creating a GitHub PR without merging it.

Expected: direct authorization naming the branch and both remotes, with the PR explicitly left unmerged. Do not perform later Task 3 steps without it.

- [ ] **Step 2: Push the exact reviewed commit to both feature branches**

Run:

```powershell
git push -u github codex/fix-cli-launch-handshake
git push -u origin codex/fix-cli-launch-handshake
$localHead = (git rev-parse HEAD).Trim()
$githubHead = ((git ls-remote github refs/heads/codex/fix-cli-launch-handshake) -split "`t")[0]
$giteeHead = ((git ls-remote origin refs/heads/codex/fix-cli-launch-handshake) -split "`t")[0]
if ($localHead -cne $githubHead -or $localHead -cne $giteeHead) {
  throw 'Remote feature branch heads do not match the reviewed local commit'
}
$localHead
```

Expected: local, GitHub, and Gitee feature-branch hashes are identical.

- [ ] **Step 3: Create the GitHub PR without merging**

Run:

```powershell
$prBody = @'
Fix the common install-smoke launch defect exposed by unsigned foundation run 33499922707. The smoke lifecycle now invokes the installed manifest-bound resources/app/out/cli.js through the installed root Electron binary with ELECTRON_RUN_AS_NODE=1, empty VSCODE_DEV, explicit --version, isolated --user-data-dir, and the unchanged 30-second bound. The corrupted upgrade still fails through the corrupted root binary and rollback still proves the baseline launcher. No workflow, package entry point, schema, signing, or publication behavior changes.
'@
$prUrl = gh pr create `
  --repo colayc/unitTest `
  --base master `
  --head codex/fix-cli-launch-handshake `
  --title "fix: run install smoke through Code-OSS CLI" `
  --body $prBody
$prUrl
```

Expected: one new GitHub PR URL and no merge.

- [ ] **Step 4: Watch required checks and verify PR identity**

Run:

```powershell
gh pr checks --repo colayc/unitTest --watch --interval 10
$pr = gh pr view --repo colayc/unitTest --json number,url,headRefOid,baseRefName,mergeStateStatus,mergeable,statusCheckRollup | ConvertFrom-Json
if ([string]$pr.headRefOid -cne $localHead) { throw 'PR head does not match the reviewed commit' }
if ([string]$pr.baseRefName -cne 'master') { throw 'PR base is not master' }
$pr | ConvertTo-Json -Depth 20
```

Expected: Linux and Windows checks succeed, the head is the reviewed commit, the base is `master`, and the PR is mergeable. Stop and request separate merge/master-synchronization/workflow authorization.

---

### Task 4: Rebase-merge, synchronize masters, and produce fresh unsigned qualification evidence

**Files:**
- No source changes.
- Evidence producer: `.github/workflows/release-inputs.yml`.
- Evidence consumer: `.github/workflows/foundation.yml`.
- Preserve untracked evidence under `.release/evidence/producer-$producerRunId/`.

**Interfaces:**
- Consumes: the exact Task 3 PR with successful required checks and separate explicit authorization.
- Produces: identical GitHub/Gitee `master` refs, a new trusted producer tied to the merged commit, a new successful unsigned foundation run, successful install smoke on both platforms, release qualification evidence, and proof that no release/tag was published.

- [ ] **Step 1: Obtain explicit merge, synchronization, and workflow-dispatch authorization**

Ask the user to authorize all of the following exact actions:

- rebase-merge the Task 3 GitHub PR while preserving the feature branch;
- synchronize the resulting GitHub `master` commit to Gitee `master`;
- dispatch a new `release-inputs.yml` producer from merged `master`; and
- dispatch a new unsigned `foundation.yml` using `release_version=0.1.0` and `release_signing_required=0`.

Expected: direct authorization for all four actions. Do not merge, update `master`, or dispatch either workflow without it.

- [ ] **Step 2: Revalidate the PR immediately before merging**

Run:

```powershell
$pr = gh pr view --repo colayc/unitTest --json number,headRefOid,mergeStateStatus,mergeable,statusCheckRollup | ConvertFrom-Json
if ([string]$pr.headRefOid -cne $localHead) { throw 'PR head changed after review' }
if ([string]$pr.mergeable -cne 'MERGEABLE') { throw 'PR is not mergeable' }
gh pr checks $pr.number --repo colayc/unitTest
```

Expected: the head remains exact and every required check remains successful.

- [ ] **Step 3: Rebase-merge and prove GitHub/Gitee master equality**

Run without `--delete-branch` so both feature branches remain available:

```powershell
gh pr merge $pr.number --repo colayc/unitTest --rebase
git fetch github master
git push origin github/master:master
git fetch origin master
$mergedCommit = (git rev-parse github/master).Trim()
$giteeCommit = (git rev-parse origin/master).Trim()
if ($mergedCommit -cne $giteeCommit) { throw 'GitHub and Gitee master commits differ' }
if ($mergedCommit -ceq '13d46b9e4ba9677a557b375c35d186412e8635af') {
  throw 'Master did not advance beyond the reviewed base commit'
}
$mergedCommit
```

Expected: the PR is merged, both `master` refs equal one new commit, and the two feature branches remain present.

- [ ] **Step 4: Dispatch and watch a completely new trusted producer**

Run:

```powershell
$previousProducerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousProducerRunId = if ($null -eq $previousProducerRun) { '' } else { [string]$previousProducerRun.databaseId }
gh workflow run release-inputs.yml --repo colayc/unitTest --ref master
do {
  Start-Sleep -Seconds 5
  $producerRun = gh run list --repo colayc/unitTest --workflow release-inputs.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,createdAt,headSha,status,url | ConvertFrom-Json
} while ($null -eq $producerRun -or [string]$producerRun.databaseId -eq $previousProducerRunId)
$producerRunId = [string]$producerRun.databaseId
if ([string]$producerRun.headSha -cne $mergedCommit) { throw 'Producer does not use merged master' }
if ($producerRunId -eq '33494864475') { throw 'Old producer run must not be reused' }
$producerRun | ConvertTo-Json -Compress
gh run watch $producerRunId --repo colayc/unitTest --exit-status --interval 10
```

Expected: authorize, Windows build, Linux build, and attestation all succeed on the new merged commit.

- [ ] **Step 5: Download and validate the new closed producer provenance**

Run:

```powershell
$evidenceRoot = Join-Path $PWD ".release/evidence/producer-$producerRunId"
New-Item -ItemType Directory -Force -Path $evidenceRoot | Out-Null
gh run download $producerRunId --repo colayc/unitTest --name release-input-provenance-1 --dir $evidenceRoot
$provenancePath = Join-Path $evidenceRoot 'release-input-provenance.json'
$provenance = Get-Content -LiteralPath $provenancePath -Raw | ConvertFrom-Json
if ([string]$provenance.producer.runId -cne $producerRunId) { throw 'Provenance run ID mismatch' }
if ([string]$provenance.producer.sourceCommit -cne $mergedCommit) { throw 'Provenance source commit mismatch' }
if ([string]$provenance.producer.event -cne 'workflow_dispatch') { throw 'Provenance event mismatch' }
$windowsSha = [string]$provenance.runtimes.windows.launcherSha256
$linuxSha = [string]$provenance.runtimes.linux.launcherSha256
$appImageToolSha = [string]$provenance.appimagetool.sha256
foreach ($digest in @($windowsSha, $linuxSha, $appImageToolSha)) {
  if ($digest -cnotmatch '^[0-9a-f]{64}$') { throw 'Provenance contains an invalid SHA-256 value' }
}
$provenance | ConvertTo-Json -Depth 20
```

Expected: a fresh, path-free closed provenance document binds the merged source commit, producer run ID, Windows launcher, Linux launcher, and appimagetool identities. Keep it untracked and preserve older evidence.

- [ ] **Step 6: Dispatch a new free unsigned foundation run**

Run:

```powershell
$previousFoundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId | ConvertFrom-Json
$previousFoundationRunId = if ($null -eq $previousFoundationRun) { '' } else { [string]$previousFoundationRun.databaseId }
gh workflow run foundation.yml --repo colayc/unitTest --ref master `
  -f release_version=0.1.0 `
  -f release_signing_required=0 `
  -f release_input_run_id=$producerRunId `
  -f windows_code_oss_sha256=$windowsSha `
  -f linux_code_oss_sha256=$linuxSha `
  -f linux_appimagetool_sha256=$appImageToolSha
do {
  Start-Sleep -Seconds 5
  $foundationRun = gh run list --repo colayc/unitTest --workflow foundation.yml --branch master --event workflow_dispatch --limit 1 --json databaseId,headSha,status,url | ConvertFrom-Json
} while ($null -eq $foundationRun -or [string]$foundationRun.databaseId -eq $previousFoundationRunId)
if ([string]$foundationRun.headSha -cne $mergedCommit) { throw 'Foundation does not use merged master' }
$foundationRun | ConvertTo-Json -Compress
gh run watch $foundationRun.databaseId --repo colayc/unitTest --exit-status --interval 10
```

Expected: `verify-release-input-run`, `verify-windows`, `verify-linux`, `package-windows`, `package-linux`, `install-smoke-windows`, `install-smoke-linux`, and `release-qualification` all succeed. The prior failed foundation run `33499922707` is not reused.

- [ ] **Step 7: Inspect jobs, artifacts, signing state, and publication state**

Run:

```powershell
$foundationView = gh run view $foundationRun.databaseId --repo colayc/unitTest --json conclusion,headSha,event,jobs,url | ConvertFrom-Json
if ([string]$foundationView.conclusion -cne 'success') { throw 'Foundation did not conclude successfully' }
if ([string]$foundationView.headSha -cne $mergedCommit) { throw 'Foundation head SHA changed' }
$requiredJobs = @(
  'verify-release-input-run',
  'verify-windows',
  'verify-linux',
  'package-windows',
  'package-linux',
  'install-smoke-windows',
  'install-smoke-linux',
  'release-qualification'
)
foreach ($jobName in $requiredJobs) {
  $job = @($foundationView.jobs | Where-Object { $_.name -eq $jobName })
  if ($job.Count -ne 1 -or [string]$job[0].conclusion -cne 'success') {
    throw "Required foundation job is not successful: $jobName"
  }
}
$signingStep = @(
  $foundationView.jobs.steps |
    Where-Object { $_.name -eq 'Materialize MSIX signing certificate' }
)
if ($signingStep.Count -ne 1 -or [string]$signingStep[0].conclusion -cne 'skipped') {
  throw 'MSIX signing step was not skipped for the unsigned run'
}
gh api "repos/colayc/unitTest/actions/runs/$($foundationRun.databaseId)/artifacts?per_page=100" --jq '.artifacts[] | [.id,.name,.expired,.size_in_bytes,.digest] | @tsv'
$releaseList = gh release list --repo colayc/unitTest --limit 100 --json tagName,isDraft,isPrerelease,publishedAt | ConvertFrom-Json
$unexpectedReleases = @($releaseList | Where-Object { $_.tagName -in @('0.1.0', 'v0.1.0') })
if ($unexpectedReleases.Count -ne 0) { throw 'Unexpected 0.1.0 GitHub Release exists' }
$unexpectedTags = @(git ls-remote --tags github refs/tags/0.1.0 refs/tags/v0.1.0)
if ($unexpectedTags.Count -ne 0) { throw 'Unexpected 0.1.0 tag exists' }
$foundationView | ConvertTo-Json -Depth 20
```

Expected: the run and every required job are successful; package, manifest, license-audit, install-smoke, and qualification artifacts are present and unexpired; signing materialization is skipped; no `0.1.0`/`v0.1.0` release or tag exists.

- [ ] **Step 8: Report the exact final acceptance boundary**

Report all of the following values:

- PR number and URL;
- reviewed feature-branch commit and rebase-merged `master` commit;
- GitHub and Gitee `master` hashes;
- producer run ID, URL, head SHA, attempt, and job conclusions;
- Windows launcher, Linux launcher, and appimagetool SHA-256 values;
- foundation run ID, URL, head SHA, required job conclusions, and artifact names;
- the skipped signing step and absence of `0.1.0`/`v0.1.0` tags and releases.

State explicitly that this accepts free unsigned cross-platform packaging, deterministic CLI install smoke, rollback, and qualification only. Windows signing and final third-party legal/license approval remain outside this acceptance.
