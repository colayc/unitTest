import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { access, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { installVersion, rollbackVersion, uninstall } from "./update.mjs";

const sourceCommit = "a".repeat(40);

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createArtifact(root, version, { launcher = `launch ${version}\n` } = {}) {
  const artifactRoot = join(root, `artifact-${version}`);
  const notice = `license ${version}\n`;
  await writeFixtureFile(artifactRoot, "app/code-oss", launcher);
  await writeFixtureFile(artifactRoot, "licenses/NOTICE.txt", notice);
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version,
    platform: process.platform === "win32" ? "windows" : "linux",
    architecture: "x64",
    sourceCommit,
    artifacts: [{
      id: "app-code-oss",
      kind: "runtime",
      relativePath: "app/code-oss",
      size: Buffer.byteLength(launcher),
      sha256: sha256(launcher),
      executable: true,
    }],
    licenses: [{
      path: "licenses/NOTICE.txt",
      size: Buffer.byteLength(notice),
      sha256: sha256(notice),
    }],
    generatedAt: "2026-08-25T00:00:00.000Z",
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
      await readFile(join(packageRoot, "versions", "1.0.0", "app", "code-oss"), "utf8"),
      "launch 1.0.0\n",
    );
  });
});

test("installVersion leaves current unchanged and publishes no version when verification fails", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    const invalidUpgrade = await createArtifact(root, "2.0.0");
    await writeFile(join(invalidUpgrade, "app", "code-oss"), "tampered\n");

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

test("rollbackVersion recovers after a simulated launch failure and is repeatable", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const packageRoot = join(root, "package-owned");
    await installVersion(packageRoot, await createArtifact(root, "1.0.0"));
    await installVersion(packageRoot, await createArtifact(root, "2.0.0", { launcher: "launch failure\n" }));

    const launchFailed = (await readFile(
      join(packageRoot, "versions", "2.0.0", "app", "code-oss"),
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
    await writeFile(join(packageRoot, "versions", "1.0.0", "app", "code-oss"), "tampered\n");

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

test("platform install smoke script emits closed non-secret lifecycle evidence", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    await mkdir(resolve(".superpowers"), { recursive: true });
    const writableContainer = await mkdtemp(resolve(".superpowers", "install-smoke-test-"));
    t.after(async () => rm(writableContainer, { recursive: true, force: true }));
    const disposableRoot = join(writableContainer, "disposable-smoke-root");
    const evidencePath = join(root, "install-smoke.json");
    const command = process.platform === "win32" ? "pwsh" : "bash";
    const args = process.platform === "win32"
      ? [
          "-NoLogo",
          "-NoProfile",
          "-File",
          resolve("tools/release/install-smoke.ps1"),
          "-Root",
          disposableRoot,
          "-EvidencePath",
          evidencePath,
        ]
      : [
          resolve("tools/release/install-smoke.sh"),
          "--root",
          disposableRoot,
          "--evidence",
          evidencePath,
        ];
    const result = spawnSync(command, args, {
      cwd: resolve("."),
      encoding: "utf8",
      windowsHide: true,
    });

    assert.equal(result.status, 0, result.stderr || result.error?.message);
    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.deepEqual(Object.keys(evidence), [
      "schemaVersion",
      "product",
      "platform",
      "sourceCommit",
      "outcomes",
    ]);
    assert.deepEqual(evidence.outcomes, {
      install: "pass",
      launchHandshake: "pass",
      upgrade: "pass",
      rollback: "pass",
      repeatedRollback: "pass",
      uninstall: "pass",
      userDataPreserved: "pass",
      packageResidueAbsent: "pass",
    });
    assert.doesNotMatch(JSON.stringify(evidence), /release-update-|disposable-smoke-root|[A-Z]:\\/iu);
    await assert.rejects(() => access(disposableRoot), (error) => error?.code === "ENOENT");
  });
});
