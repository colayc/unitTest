import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { access, chmod, copyFile, mkdir, mkdtemp, readFile, rename, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import {
  installVersion,
  rollbackVersion,
  runSmokeLifecycle,
  uninstall,
} from "./update.mjs";

const sourceCommit = "a".repeat(40);
const baselineSourceCommit = "d".repeat(40);
const platform = process.platform === "win32" ? "windows" : "linux";
const launcherRelativePath = platform === "windows" ? "app/code-oss.exe" : "app/code-oss";
const generatedAt = "2026-08-25T00:00:00.000Z";
const baselineGeneratedAt = "2026-08-24T00:00:00.000Z";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
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
  await writeFixtureFile(artifactRoot, "licenses/NOTICE.txt", notice);
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version,
    platform,
    architecture: "x64",
    sourceCommit: manifestSourceCommit,
    artifacts: [{
      id: "app-code-oss",
      kind: "runtime",
      relativePath: launcherRelativePath,
      size: launcherBytes.length,
      sha256: sha256(launcherBytes),
      executable: true,
    }],
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
    const sourceBytes = await readFile(sourceLauncher);
    const packagePath = await writeFixtureFile(root, "downloads/unit-test-ide-2.0.0.package", "real package bytes\n");
    const baselinePackagePath = await writeFixtureFile(root, "downloads/unit-test-ide-1.0.0.package", "real baseline package bytes\n");
    const evidencePath = join(root, "install-smoke.json");

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

    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.equal(evidence.outcomes.upgradeLaunch, "failed-as-expected");
    assert.equal(evidence.outcomes.rollback, "pass");
    assert.equal(evidence.outcomes.rollbackLaunch, "pass");
    assert.equal(evidence.rollbackVersion, "1.0.0");
    assert.deepEqual(await readFile(sourceLauncher), sourceBytes);
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
