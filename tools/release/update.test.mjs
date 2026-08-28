import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { access, chmod, copyFile, mkdir, mkdtemp, readFile, rename, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import {
  installVersion,
  rollbackVersion,
  runSmokeLifecycle,
  uninstall,
} from "./update.mjs";

const sourceCommit = "a".repeat(40);
const baselineSourceCommit = "d".repeat(40);
const platform = process.platform === "win32" ? "windows" : "linux";
const launcherRelativePath = process.platform === "win32"
  ? "app/code-oss-runtime/Code - OSS.exe"
  : "app/code-oss-runtime/code-oss";
const productRelativePath = "app/code-oss-runtime/resources/app/product.json";
const corruptedLauncherBytes = Buffer.from([0x00, 0xff, 0x00, 0x7f, 0x55, 0xaa]);
const generatedAt = "2026-08-25T00:00:00.000Z";
const baselineGeneratedAt = "2026-08-24T00:00:00.000Z";
const sourceDateEpoch = "1787616000";
const packageMsixScript = resolve("tools/release/windows/package-msix.ps1");
const installSmokeScript = resolve("tools/release/install-smoke.ps1");
const linuxInstallSmokeScript = resolve("tools/release/install-smoke.sh");
const linuxOnly = process.platform === "linux" ? test : test.skip;

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function runPowerShellFile(filePath, args, env = {}) {
  return spawnSync("pwsh.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    filePath,
    ...args,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: { ...process.env, ...env },
    windowsHide: true,
  });
}

function runWindowsPowerShellFile(filePath, args, env = {}) {
  return spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    filePath,
    ...args,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: { ...process.env, ...env },
    windowsHide: true,
  });
}

function resolveRealMakeAppx() {
  const result = spawnSync("powershell.exe", [
    "-NoProfile",
    "-Command",
    String.raw`
$command = Get-Command -Name 'makeappx.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
if ($null -ne $command) { [Console]::Write($command.Source); exit 0 }
$kitsRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFilesX86)
if (-not [string]::IsNullOrWhiteSpace($kitsRoot)) {
  $candidateRoot = Join-Path $kitsRoot 'Windows Kits\10\bin'
  if (Test-Path -LiteralPath $candidateRoot) {
    $candidate = Get-ChildItem -LiteralPath $candidateRoot -Filter 'makeappx.exe' -Recurse -File -ErrorAction SilentlyContinue |
      Sort-Object -Property FullName -Descending | Select-Object -First 1
    if ($null -ne $candidate) { [Console]::Write($candidate.FullName); exit 0 }
  }
}
exit 1
`,
  ], { encoding: "utf8", windowsHide: true });
  return result.status === 0 ? result.stdout.trim() : null;
}

function packageWithRealMakeAppx(artifact, outputPath, version, makeAppx) {
  return runWindowsPowerShellFile(packageMsixScript, [
    "-StagingRoot", artifact,
    "-Output", outputPath,
    "-Version", version,
    "-Publisher", "CN=Unit Test IDE",
  ], {
    RELEASE_MAKEAPPX_PATH: makeAppx,
    RELEASE_SIGNING_REQUIRED: "0",
    SOURCE_DATE_EPOCH: sourceDateEpoch,
  });
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createArtifact(root, version, {
  launcher = `launch ${version}\n`,
  launcherSource,
  manifestGeneratedAt = generatedAt,
  manifestSourceCommit = sourceCommit,
} = {}) {
  const artifactRoot = join(root, `artifact-${version}`);
  const notice = `license ${version}\n`;
  const launcherPath = join(artifactRoot, ...launcherRelativePath.split("/"));
  await mkdir(dirname(launcherPath), { recursive: true });
  if (launcherSource === undefined) await writeFile(launcherPath, launcher);
  else await copyFile(launcherSource, launcherPath);
  if (process.platform !== "win32") await chmod(launcherPath, 0o755);
  const launcherBytes = await readFile(launcherPath);
  const productBytes = Buffer.from(`${JSON.stringify({ nameShort: "Code - OSS", version })}\n`);
  await writeFixtureFile(artifactRoot, productRelativePath, productBytes);
  await writeFixtureFile(artifactRoot, "licenses/NOTICE.txt", notice);
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version,
    platform,
    architecture: "x64",
    sourceCommit: manifestSourceCommit,
    artifacts: [
      {
        id: "app-code-oss",
        kind: "runtime",
        relativePath: launcherRelativePath,
        size: launcherBytes.length,
        sha256: sha256(launcherBytes),
        executable: true,
      },
      {
        id: "app-code-oss-product",
        kind: "runtime",
        relativePath: productRelativePath,
        size: productBytes.length,
        sha256: sha256(productBytes),
        executable: false,
      },
    ],
    licenses: [{
      path: "licenses/NOTICE.txt",
      size: Buffer.byteLength(notice),
      sha256: sha256(notice),
    }],
    generatedAt: manifestGeneratedAt,
  };
  await writeFile(join(artifactRoot, "release-manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  return artifactRoot;
}

async function observeInstalledProductAfterLauncherCorruption(packageRoot, version, smokeFinished) {
  const launcher = join(packageRoot, "versions", version, ...launcherRelativePath.split("/"));
  const product = join(packageRoot, "versions", version, ...productRelativePath.split("/"));
  while (!smokeFinished()) {
    try {
      if ((await readFile(launcher)).equals(corruptedLauncherBytes)) return readFile(product);
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
    await delay(1);
  }
  return null;
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "release-update-"));
  t.after(async () => rm(root, { recursive: true, force: true }));
  await run(root);
}

async function currentVersion(packageRoot) {
  return (await readFile(join(packageRoot, "current"), "utf8")).trim();
}

test("installVersion publishes a verified first install before switching current", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    const artifact = await createArtifact(root, "1.0.0");

    const result = await installVersion(packageRoot, artifact);

    assert.deepEqual(result, { previousVersion: null, version: "1.0.0" });
    assert.equal(await currentVersion(packageRoot), "1.0.0");
    assert.equal(
      await readFile(join(packageRoot, "versions", "1.0.0", ...launcherRelativePath.split("/")), "utf8"),
      "launch 1.0.0\n",
    );
  });
});

test("package-backed production smoke corrupts the target then really launches the restored baseline", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const launcherSource = process.platform === "win32"
      ? join(process.env.SystemRoot, "System32", "mountvol.exe")
      : "/usr/bin/true";
    const baselineArtifact = await createArtifact(root, "1.0.0", {
      launcherSource,
      manifestGeneratedAt: baselineGeneratedAt,
      manifestSourceCommit: baselineSourceCommit,
    });
    const artifact = await createArtifact(root, "2.0.0", { launcherSource });
    const sourceLauncher = join(artifact, ...launcherRelativePath.split("/"));
    const sourceProduct = join(artifact, ...productRelativePath.split("/"));
    const sourceBytes = await readFile(sourceLauncher);
    const sourceProductBytes = await readFile(sourceProduct);
    const packagePath = await writeFixtureFile(root, "downloads/unit-test-ide-2.0.0.package", "real package bytes\n");
    const baselinePackagePath = await writeFixtureFile(root, "downloads/unit-test-ide-1.0.0.package", "real baseline package bytes\n");
    const evidencePath = join(root, "install-smoke.json");
    const packageRoot = join(root, "disposable-smoke-root", "package-owned");
    let smokeFinished = false;
    const installedProductAfterCorruption = observeInstalledProductAfterLauncherCorruption(
      packageRoot,
      "2.0.0",
      () => smokeFinished,
    );

    try {
      await runSmokeLifecycle({
        artifact,
        baselineArtifact,
        baselineManifestSha256: sha256(await readFile(join(baselineArtifact, "release-manifest.json"))),
        baselinePackagePath,
        baselinePackageSha256: sha256(await readFile(baselinePackagePath)),
        evidence: evidencePath,
        manifestSha256: sha256(await readFile(join(artifact, "release-manifest.json"))),
        packagePath,
        packageSha256: sha256(await readFile(packagePath)),
        platform,
        root: join(root, "disposable-smoke-root"),
        version: "2.0.0",
      });
    } finally {
      smokeFinished = true;
    }

    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.equal(evidence.outcomes.upgradeLaunch, "failed-as-expected");
    assert.equal(evidence.outcomes.rollback, "pass");
    assert.equal(evidence.outcomes.rollbackLaunch, "pass");
    assert.equal(evidence.rollbackVersion, "1.0.0");
    assert.deepEqual(await readFile(sourceLauncher), sourceBytes);
    assert.deepEqual(await installedProductAfterCorruption, sourceProductBytes);
    assert.deepEqual(await readFile(sourceProduct), sourceProductBytes);
  });
});

const windowsOnly = process.platform === "win32" ? test : test.skip;

windowsOnly("install-smoke completes the full lifecycle for real makeappx packages with a spaced launcher", async (t) => {
  const realMakeAppx = resolveRealMakeAppx();
  if (!realMakeAppx) {
    t.skip("makeappx.exe is unavailable in this environment");
    return;
  }

  await withTemporaryRoot(t, async (root) => {
    const baselineArtifact = await createArtifact(root, "1.0.0", {
      launcherSource: process.execPath,
      manifestGeneratedAt: generatedAt,
      manifestSourceCommit: baselineSourceCommit,
    });
    const artifact = await createArtifact(root, "2.0.0", { launcherSource: process.execPath });
    const baselinePackagePath = join(root, "downloads", "unit-test-ide-1.0.0.msix");
    const packagePath = join(root, "downloads", "unit-test-ide-2.0.0.msix");
    const baselinePackageResult = packageWithRealMakeAppx(
      baselineArtifact,
      baselinePackagePath,
      "1.0.0",
      realMakeAppx,
    );
    assert.equal(baselinePackageResult.status, 0, baselinePackageResult.stderr);
    const packageResult = packageWithRealMakeAppx(
      artifact,
      packagePath,
      "2.0.0",
      realMakeAppx,
    );
    assert.equal(packageResult.status, 0, packageResult.stderr);

    const baselineManifestPath = join(baselineArtifact, "release-manifest.json");
    const manifestPath = join(artifact, "release-manifest.json");
    const evidencePath = join(root, "install-smoke.json");
    const smokeRoot = join(root, "disposable-smoke-root");
    const result = runPowerShellFile(installSmokeScript, [
      "-EvidencePath", evidencePath,
      "-PackagePath", packagePath,
      "-PackageSha256", sha256(await readFile(packagePath)),
      "-ManifestPath", manifestPath,
      "-ManifestSha256", sha256(await readFile(manifestPath)),
      "-Version", "2.0.0",
      "-BaselinePackagePath", baselinePackagePath,
      "-BaselinePackageSha256", sha256(await readFile(baselinePackagePath)),
      "-BaselineManifestPath", baselineManifestPath,
      "-BaselineManifestSha256", sha256(await readFile(baselineManifestPath)),
      "-BaselineVersion", "1.0.0",
      "-Root", smokeRoot,
    ]);

    assert.equal(result.status, 0, result.stderr);
    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.deepEqual(evidence.outcomes, {
      install: "pass",
      launchHandshake: "pass",
      upgrade: "pass",
      upgradeLaunch: "failed-as-expected",
      rollback: "pass",
      rollbackLaunch: "pass",
      repeatedRollback: "pass",
      uninstall: "pass",
      userDataPreserved: "pass",
      packageResidueAbsent: "pass",
    });
  });
});

test("installVersion leaves current unchanged and publishes no version when verification fails", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    const invalidUpgrade = await createArtifact(root, "2.0.0");
    await writeFile(join(invalidUpgrade, ...launcherRelativePath.split("/")), "tampered\n");

    await assert.rejects(
      () => installVersion(packageRoot, invalidUpgrade),
      (error) => error?.code === "RELEASE_VERIFICATION_FAILED",
    );

    assert.equal(await currentVersion(packageRoot), "1.0.0");
    await assert.rejects(
      () => access(join(packageRoot, "versions", "2.0.0")),
      (error) => error?.code === "ENOENT",
    );
  });
});

test("installVersion upgrades atomically and rejects a downgrade outside rollback", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));

    const result = await installVersion(packageRoot, await createArtifact(root, "2.0.0"));
    assert.deepEqual(result, { previousVersion: "1.0.0", version: "2.0.0" });
    assert.equal(await currentVersion(packageRoot), "2.0.0");

    const downgrade = await createArtifact(root, "1.5.0");
    await assert.rejects(
      () => installVersion(packageRoot, downgrade),
      (error) => error?.code === "RELEASE_DOWNGRADE_REJECTED",
    );
    assert.equal(await currentVersion(packageRoot), "2.0.0");
  });
});

test("installVersion compares arbitrarily large semver-like numeric identifiers without rounding", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "100000000000000000001.0.0"));
    const roundedDowngrade = await createArtifact(root, "100000000000000000000.0.0");

    await assert.rejects(
      () => installVersion(packageRoot, roundedDowngrade),
      (error) => error?.code === "RELEASE_DOWNGRADE_REJECTED",
    );
    assert.equal(await currentVersion(packageRoot), "100000000000000000001.0.0");
  });
});

test("installVersion compares prerelease identifiers with case-sensitive SemVer ASCII ordering", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0-a"));

    await assert.rejects(
      () => createArtifact(root, "1.0.0-A").then((artifact) => installVersion(packageRoot, artifact)),
      (error) => error?.code === "RELEASE_DOWNGRADE_REJECTED",
    );
    assert.equal(await currentVersion(packageRoot), "1.0.0-a");
  });
});

test("rollbackVersion recovers after a simulated launch failure and is repeatable", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    await installVersion(packageRoot, await createArtifact(root, "2.0.0", { launcher: "launch failure\n" }));

    const launchFailed = (await readFile(
      join(packageRoot, "versions", "2.0.0", ...launcherRelativePath.split("/")),
      "utf8",
    )).includes("failure");
    assert.equal(launchFailed, true);

    assert.deepEqual(
      await rollbackVersion(packageRoot, "1.0.0"),
      { previousVersion: "2.0.0", version: "1.0.0" },
    );
    assert.equal(await currentVersion(packageRoot), "1.0.0");
    assert.deepEqual(
      await rollbackVersion(packageRoot, "1.0.0"),
      { previousVersion: "1.0.0", version: "1.0.0" },
    );
    assert.equal(await currentVersion(packageRoot), "1.0.0");
    assert.equal(
      await readFile(join(packageRoot, "versions", "2.0.0", "release-manifest.json"), "utf8")
        .then((value) => JSON.parse(value).version),
      "2.0.0",
    );
  });
});

test("rollbackVersion refuses a tampered installed version without changing current", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    await installVersion(packageRoot, await createArtifact(root, "2.0.0"));
    await writeFile(join(packageRoot, "versions", "1.0.0", ...launcherRelativePath.split("/")), "tampered\n");

    await assert.rejects(
      () => rollbackVersion(packageRoot, "1.0.0"),
      (error) => error?.code === "RELEASE_VERIFICATION_FAILED",
    );
    assert.equal(await currentVersion(packageRoot), "2.0.0");
  });
});

test("uninstall removes only the package-owned root and preserves sibling user data", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    const workspaceRoot = join(root, "workspace");
    await writeFixtureFile(workspaceRoot, "project/tests.cpp", "user data\n");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));

    await uninstall(packageRoot);

    await assert.rejects(() => access(packageRoot), (error) => error?.code === "ENOENT");
    assert.equal(await readFile(join(workspaceRoot, "project", "tests.cpp"), "utf8"), "user data\n");
  });
});

test("uninstall refuses to remove a root that is not package-owned", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const foreignRoot = join(root, "foreign");
    await writeFixtureFile(foreignRoot, "keep.txt", "keep\n");

    await assert.rejects(
      () => uninstall(foreignRoot),
      (error) => error?.code === "RELEASE_ROOT_NOT_OWNED",
    );
    assert.equal(await readFile(join(foreignRoot, "keep.txt"), "utf8"), "keep\n");
  });
});

test("installVersion rejects an intermediate reparse parent before creating package data", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const actualParent = join(root, "actual-parent");
    const redirectedParent = join(root, "redirected-parent");
    await mkdir(actualParent);
    await symlink(actualParent, redirectedParent, process.platform === "win32" ? "junction" : "dir");
    const packageRoot = join(redirectedParent, "package-owned");
    const artifact = await createArtifact(root, "1.0.0");

    await assert.rejects(
      () => installVersion(packageRoot, artifact),
      (error) => error?.code === "RELEASE_ROOT_NOT_OWNED",
    );
    await assert.rejects(
      () => access(join(actualParent, "package-owned")),
      (error) => error?.code === "ENOENT",
    );
  });
});

test("installVersion rejects a redirected package-owned versions directory", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    const externalVersions = join(root, "external-versions");
    await rename(join(packageRoot, "versions"), externalVersions);
    await symlink(
      externalVersions,
      join(packageRoot, "versions"),
      process.platform === "win32" ? "junction" : "dir",
    );

    await assert.rejects(
      () => createArtifact(root, "2.0.0").then((artifact) => installVersion(packageRoot, artifact)),
      (error) => error?.code === "RELEASE_ROOT_NOT_OWNED",
    );
    await assert.rejects(
      () => access(join(externalVersions, "2.0.0")),
      (error) => error?.code === "ENOENT",
    );
  });
});

test("package-backed smoke lifecycle emits digest-bound non-secret evidence", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const baselineArtifact = await createArtifact(root, "1.0.0", {
      manifestGeneratedAt: baselineGeneratedAt,
      manifestSourceCommit: baselineSourceCommit,
    });
    const artifact = await createArtifact(root, "2.0.0");
    const packagePath = await writeFixtureFile(root, "downloads/unit-test-ide-2.0.0.package", "real package bytes\n");
    const baselinePackagePath = await writeFixtureFile(root, "downloads/unit-test-ide-1.0.0.package", "real baseline package bytes\n");
    const packageSha256 = sha256(await readFile(packagePath));
    const manifestSha256 = sha256(await readFile(join(artifact, "release-manifest.json")));
    const baselinePackageSha256 = sha256(await readFile(baselinePackagePath));
    const baselineManifestSha256 = sha256(await readFile(join(baselineArtifact, "release-manifest.json")));
    const disposableRoot = join(root, "disposable-smoke-root");
    const evidencePath = join(root, "install-smoke.json");
    await runSmokeLifecycle({
      artifact,
      baselineArtifact,
      baselineManifestSha256,
      baselinePackagePath,
      baselinePackageSha256,
      evidence: evidencePath,
      manifestSha256,
      packagePath,
      packageSha256,
      platform,
      root: disposableRoot,
      version: "2.0.0",
    }, {
      launch: () => ({ status: 0, stdout: "2.0.0\n", stderr: "" }),
    });

    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.deepEqual(Object.keys(evidence), [
      "schemaVersion",
      "product",
      "platform",
      "architecture",
      "sourceCommit",
      "generatedAt",
      "packageFilename",
      "version",
      "packageSha256",
      "manifestSha256",
      "rollbackVersion",
      "rollbackPackageFilename",
      "rollbackPackageSha256",
      "rollbackManifestSha256",
      "outcomes",
    ]);
    assert.equal(evidence.packageFilename, "unit-test-ide-2.0.0.package");
    assert.equal(evidence.version, "2.0.0");
    assert.equal(evidence.packageSha256, packageSha256);
    assert.equal(evidence.manifestSha256, manifestSha256);
    assert.equal(evidence.rollbackVersion, "1.0.0");
    assert.equal(evidence.architecture, "x64");
    assert.equal(evidence.generatedAt, generatedAt);
    assert.equal(evidence.rollbackPackageFilename, "unit-test-ide-1.0.0.package");
    assert.equal(evidence.rollbackPackageSha256, baselinePackageSha256);
    assert.equal(evidence.rollbackManifestSha256, baselineManifestSha256);
    assert.deepEqual(evidence.outcomes, {
      install: "pass",
      launchHandshake: "pass",
      upgrade: "pass",
      upgradeLaunch: "failed-as-expected",
      rollback: "pass",
      rollbackLaunch: "pass",
      repeatedRollback: "pass",
      uninstall: "pass",
      userDataPreserved: "pass",
      packageResidueAbsent: "pass",
    });
    assert.doesNotMatch(JSON.stringify(evidence), /release-update-|disposable-smoke-root|[A-Z]:\\/iu);
    await assert.rejects(() => access(join(disposableRoot, "package-owned")), (error) => error?.code === "ENOENT");
  });
});

for (const [label, omittedFlag] of [
  ["package input", "--package"],
  ["package digest", "--package-sha256"],
  ["baseline package", "--baseline-package"],
  ["baseline package digest", "--baseline-package-sha256"],
  ["baseline manifest digest", "--baseline-manifest-sha256"],
  ["evidence output", "--evidence"],
]) {
  test(`smoke CLI fails closed when ${label} is absent`, () => {
    const values = new Map([
      ["--artifact", "target"],
      ["--baseline-artifact", "baseline"],
      ["--baseline-manifest-sha256", "c".repeat(64)],
      ["--baseline-package", "baseline.msix"],
      ["--baseline-package-sha256", "d".repeat(64)],
      ["--evidence", "evidence.json"],
      ["--manifest-sha256", "a".repeat(64)],
      ["--package", "package.msix"],
      ["--package-sha256", "b".repeat(64)],
      ["--platform", platform],
      ["--root", "smoke-root"],
      ["--version", "2.0.0"],
    ]);
    values.delete(omittedFlag);
    const args = [resolve("tools/release/update.mjs"), "smoke"];
    for (const [flag, value] of values) args.push(flag, value);

    const result = spawnSync(process.execPath, args, { encoding: "utf8", windowsHide: true });

    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`${omittedFlag} is required`, "u"));
  });
}

test("install smoke workflow downloads digest-bearing produced packages before exercising wrappers", async () => {
  const workflow = await readFile(resolve(".github/workflows/foundation.yml"), "utf8");
  for (const platform of ["windows", "linux"]) {
    const jobStart = workflow.indexOf(`  install-smoke-${platform}:`);
    const download = workflow.indexOf("uses: actions/download-artifact@", jobStart);
    const wrapper = workflow.indexOf(`tools/release/install-smoke.${platform === "windows" ? "ps1" : "sh"}`, jobStart);
    assert.ok(jobStart >= 0 && download > jobStart && wrapper > download, `${platform} package download must precede smoke`);
    const job = workflow.slice(jobStart, workflow.indexOf("\n  install-smoke-", jobStart + 1) === -1
      ? workflow.length
      : workflow.indexOf("\n  install-smoke-", jobStart + 1));
    assert.match(job, /package_sha256/u);
    assert.match(job, /manifest_sha256/u);
    assert.match(job, /baseline_package/u);
  }
});

test("Linux packaging keeps trusted coordinates and transported mode validation ahead of staging", async () => {
  const workflow = (await readFile(resolve(".github/workflows/foundation.yml"), "utf8")).replace(/\r\n?/gu, "\n");
  const start = workflow.indexOf("  package-linux:");
  const end = workflow.indexOf("\n  install-smoke-windows:", start);
  const job = workflow.slice(start, end);
  assert.ok(start >= 0 && end > start);
  assert.match(job, /^      RELEASE_INPUT_RUN_ID: \$\{\{ needs\.verify-release-input-run\.outputs\.run_id \}\}$/mu);
  assert.match(job, /^      RELEASE_INPUT_RUN_ATTEMPT: \$\{\{ needs\.verify-release-input-run\.outputs\.run_attempt \}\}$/mu);
  assert.match(job, /^      LINUX_ARTIFACT_ID: \$\{\{ needs\.verify-release-input-run\.outputs\.linux_artifact_id \}\}$/mu);
  assert.match(job, /^      APPIMAGETOOL_ARTIFACT_ID: \$\{\{ needs\.verify-release-input-run\.outputs\.appimagetool_artifact_id \}\}$/mu);
  assert.match(job, /^      CODE_OSS_SHA256: \$\{\{ needs\.verify-release-input-run\.outputs\.linux_launcher_sha256 \}\}$/mu);
  assert.match(job, /^      APPIMAGETOOL_SHA256: \$\{\{ needs\.verify-release-input-run\.outputs\.appimagetool_sha256 \}\}$/mu);
  const ordered = [
    "name: Validate producer attempt before Linux artifact download",
    "name: Download trusted Linux Code-OSS runtime",
    "name: Download trusted Linux appimagetool",
    "name: Validate producer attempt after Linux artifact download",
    '[[ "$(sha256sum "$launcher"',
    '[[ "$(sha256sum "${appimagetool_matches[0]}"',
    "runtime-mode-inventory.mjs restore",
    'echo "CODE_OSS_RUNTIME_ROOT=$runtime_root"',
    "name: Stage and package Linux AppImage",
  ];
  let previous = -1;
  for (const marker of ordered) {
    const index = job.indexOf(marker);
    assert.ok(index > previous, `${marker} must remain after the preceding Linux input gate`);
    previous = index;
  }
});

linuxOnly("Linux install smoke restores both AppImage execute bits before either verifier runs", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const scriptRoot = join(root, "tools", "release");
    const linuxRoot = join(scriptRoot, "linux");
    await mkdir(linuxRoot, { recursive: true });
    await copyFile(linuxInstallSmokeScript, join(scriptRoot, "install-smoke.sh"));

    const targetPackage = join(root, "target.AppImage");
    const baselinePackage = join(root, "baseline.AppImage");
    const fakeImage = "#!/usr/bin/env bash\nmkdir -p squashfs-root/usr/lib/unit-test-ide\n";
    await writeFile(targetPackage, fakeImage, { mode: 0o644 });
    await writeFile(baselinePackage, fakeImage, { mode: 0o644 });
    await chmod(targetPackage, 0o644);
    await chmod(baselinePackage, 0o644);

    const orderLog = join(root, "order.log");
    const commandRoot = join(root, "bin");
    await mkdir(commandRoot);
    await writeFile(join(commandRoot, "sha256sum"), `#!/usr/bin/env bash
printf 'sha256:%s\\n' "$*" >> "$ORDER_LOG"
exec /usr/bin/sha256sum "$@"
`, { mode: 0o755 });
    await writeFile(join(commandRoot, "chmod"), `#!/usr/bin/env bash
printf 'chmod:%s\\n' "$*" >> "$ORDER_LOG"
exec /usr/bin/chmod "$@"
`, { mode: 0o755 });
    await chmod(join(commandRoot, "sha256sum"), 0o755);
    await chmod(join(commandRoot, "chmod"), 0o755);

    await writeFile(join(linuxRoot, "verify-appimage.mjs"), `
import { access, appendFile } from "node:fs/promises";
import { constants } from "node:fs";
const image = process.argv[process.argv.indexOf("--image") + 1];
await access(image, constants.X_OK);
await appendFile(process.env.ORDER_LOG, \`verify:\${image}\\n\`);
`);
    await writeFile(join(scriptRoot, "update.mjs"), `
import { writeFile } from "node:fs/promises";
const value = (flag) => process.argv[process.argv.indexOf(flag) + 1];
await writeFile(value("--evidence"), JSON.stringify({
  platform: "linux",
  packageSha256: value("--package-sha256"),
  manifestSha256: value("--manifest-sha256"),
  version: value("--version"),
  rollbackVersion: "1.0.0",
  rollbackPackageSha256: value("--baseline-package-sha256"),
  rollbackManifestSha256: value("--baseline-manifest-sha256"),
  outcomes: { upgradeLaunch: "failed-as-expected", rollbackLaunch: "pass", packageResidueAbsent: "pass" },
}));
`);

    const packageSha256 = sha256(await readFile(targetPackage));
    const baselinePackageSha256 = sha256(await readFile(baselinePackage));
    const targetManifest = join(root, "target.json");
    const baselineManifest = join(root, "baseline.json");
    const targetManifestValue = {
      version: "2.0.0",
      packageSha256,
      releaseManifestSha256: "a".repeat(64),
    };
    const baselineManifestValue = {
      version: "1.0.0",
      packageSha256: baselinePackageSha256,
      releaseManifestSha256: "b".repeat(64),
    };
    await writeFile(targetManifest, JSON.stringify(targetManifestValue));
    await writeFile(baselineManifest, JSON.stringify(baselineManifestValue));
    const smokeRoot = join(root, "smoke");
    const evidence = join(root, "evidence.json");
    const result = spawnSync("bash", [join(scriptRoot, "install-smoke.sh"),
      "--root", smokeRoot,
      "--evidence", evidence,
      "--package", targetPackage,
      "--package-sha256", packageSha256,
      "--manifest", targetManifest,
      "--manifest-sha256", targetManifestValue.releaseManifestSha256,
      "--version", targetManifestValue.version,
      "--baseline-package", baselinePackage,
      "--baseline-package-sha256", baselinePackageSha256,
      "--baseline-manifest", baselineManifest,
      "--baseline-manifest-sha256", baselineManifestValue.releaseManifestSha256,
      "--baseline-version", baselineManifestValue.version,
    ], {
      encoding: "utf8",
      env: { ...process.env, ORDER_LOG: orderLog, PATH: `${commandRoot}:${process.env.PATH}` },
      windowsHide: true,
    });

    assert.equal(result.status, 0, result.stderr);
    const events = (await readFile(orderLog, "utf8")).trim().split("\n");
    const targetDigest = events.findIndex((event) => event === `sha256:-- ${targetPackage}`);
    const baselineDigest = events.findIndex((event) => event === `sha256:-- ${baselinePackage}`);
    const imageChmod = events.findIndex((event) => event === `chmod:u+x -- ${targetPackage} ${baselinePackage}`);
    const targetVerify = events.findIndex((event) => event === `verify:${targetPackage}`);
    const baselineVerify = events.findIndex((event) => event === `verify:${baselinePackage}`);
    assert.ok(targetDigest >= 0 && baselineDigest >= 0 && imageChmod >= 0 && targetVerify >= 0 && baselineVerify >= 0, events);
    assert.ok(targetDigest < imageChmod && baselineDigest < imageChmod, events);
    assert.ok(imageChmod < targetVerify && imageChmod < baselineVerify, events);
  });
});
