import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { stageRelease } from "./stage.mjs";

const sourceDateEpoch = "1787616000";
const generatedAt = "2026-08-25T00:00:00.000Z";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "release-stage-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  await run(root);
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createReleaseFixture(root) {
  const codeOss = await writeFixtureFile(root, "inputs/code-oss/code-oss.exe", "code oss runtime\n");
  const service = await writeFixtureFile(root, "inputs/service/unit-test-service.exe", "service binary\n");
  const extensionRoot = join(root, "inputs/extension");
  await writeFixtureFile(extensionRoot, "dist/src/extension.js", "export const value = 1;\n");
  await writeFixtureFile(extensionRoot, "package.json", JSON.stringify({
    name: "code-oss-extension",
    publisher: "unit-test-ide",
    version: "0.0.0-test",
    main: "./dist/src/extension.js",
  }, null, 2));
  const cmakeRoot = join(root, "inputs/cmake");
  await writeFixtureFile(cmakeRoot, "bin/cmake.exe", "cmake binary\n");
  await writeFixtureFile(cmakeRoot, "manifest.json", JSON.stringify({ tool: "cmake" }, null, 2));
  await writeFixtureFile(cmakeRoot, "licenses/LICENSE.txt", "cmake license\n");
  const coverageRoot = join(root, "inputs/coverage");
  await writeFixtureFile(coverageRoot, "app/gcovr-runner.pyz", "runner payload\n");
  await writeFixtureFile(coverageRoot, "manifest.json", JSON.stringify({ tool: "coverage" }, null, 2));
  await writeFixtureFile(coverageRoot, "licenses/NOTICE.txt", "coverage notice\n");
  return {
    codeOss,
    service,
    extensionRoot,
    cmakeRoot,
    coverageRoot,
    outRoot: join(root, "out"),
  };
}

async function packageInputSnapshot(root, current = "") {
  const entries = await readdir(current ? join(root, ...current.split("/")) : root, { withFileTypes: true });
  entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
  const snapshot = [];
  for (const entry of entries) {
    const relativePath = current ? `${current}/${entry.name}` : entry.name;
    const absolutePath = join(root, ...relativePath.split("/"));
    const info = await stat(absolutePath);
    assert.equal(info.mtime.toISOString(), generatedAt, `timestamp for ${relativePath}`);
    if (entry.isDirectory()) snapshot.push(...await packageInputSnapshot(root, relativePath));
    else snapshot.push([relativePath, (await readFile(absolutePath)).toString("base64")]);
  }
  return snapshot;
}

test("stageRelease copies the deterministic staging layout and writes a release manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const result = await stageRelease({
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    });

    assert.equal(result.stagingRoot, join(fixture.outRoot, "staging", "1.2.3", "windows-x64"));
    for (const relativePath of [
      "app/code-oss.exe",
      "app/extensions/unit-test-ide/package.json",
      "app/extensions/unit-test-ide/dist/src/extension.js",
      "service/unit-test-service.exe",
      "bundles/cmake/bin/cmake.exe",
      "bundles/cmake/manifest.json",
      "bundles/coverage/app/gcovr-runner.pyz",
      "bundles/coverage/manifest.json",
      "licenses/cmake/licenses/LICENSE.txt",
      "licenses/coverage/licenses/NOTICE.txt",
      "release-manifest.json",
    ]) {
      const bytes = await readFile(join(result.stagingRoot, ...relativePath.split("/")));
      assert.ok(bytes.length > 0, relativePath);
    }

    assert.equal(
      sha256(await readFile(join(result.stagingRoot, "app/code-oss.exe"))),
      sha256("code oss runtime\n"),
    );
    assert.equal(
      sha256(await readFile(join(result.stagingRoot, "service/unit-test-service.exe"))),
      sha256("service binary\n"),
    );

    const manifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    assert.equal(manifest.version, "1.2.3");
    assert.equal(manifest.platform, "windows");
    assert.equal(manifest.architecture, "x64");
    assert.equal(manifest.sourceCommit, "a".repeat(40));
    assert.equal(manifest.generatedAt, generatedAt);
    assert.deepEqual(
      manifest.licenses.map(({ path }) => path),
      [
        "licenses/cmake/licenses/LICENSE.txt",
        "licenses/coverage/licenses/NOTICE.txt",
      ],
    );
    for (const license of manifest.licenses) {
      const bytes = await readFile(join(result.stagingRoot, ...license.path.split("/")));
      assert.equal(license.size, bytes.length);
      assert.equal(license.sha256, sha256(bytes));
    }
  });
});

test("stageRelease fails closed on a missing required input before it writes a partial manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await rm(fixture.codeOss, { force: true });

    await assert.rejects(
      () => stageRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...fixture,
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_INPUT_MISSING");
        return true;
      },
    );

    await assert.rejects(
      () => readFile(join(fixture.outRoot, "staging", "1.2.3", "windows-x64", "release-manifest.json")),
      /ENOENT/u,
    );
  });
});

test("stage CLI rejects unknown flags with a stable error", () => {
  const result = spawnSync(process.execPath, [
    resolve("tools/release/stage.mjs"),
    "--platform", "windows",
    "--architecture", "x64",
    "--version", "1.2.3",
    "--code-oss", "runtime.exe",
    "--service", "service.exe",
    "--cmake-root", "cmake",
    "--coverage-root", "coverage",
    "--out", "out",
    "--unexpected", "value",
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    windowsHide: true,
  });

  assert.equal(result.status, 1);
  assert.match(result.stderr, /unknown stage flag: --unexpected/u);
});

test("stage CLI rejects duplicate flags instead of overwriting", () => {
  const result = spawnSync(process.execPath, [
    resolve("tools/release/stage.mjs"),
    "--platform", "windows",
    "--platform", "linux",
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    windowsHide: true,
  });

  assert.equal(result.status, 1);
  assert.match(result.stderr, /duplicate stage flag: --platform/u);
});

test("stage CLI accepts a valid invocation and stages the release tree", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const extensionDistRoot = resolve("apps/code-oss-extension/dist");
    await rm(extensionDistRoot, { recursive: true, force: true });
    await writeFixtureFile(extensionDistRoot, "src/extension.js", "export const cli = true;\n");
    t.after(async () => {
      await rm(extensionDistRoot, { recursive: true, force: true });
    });
    const result = spawnSync(process.execPath, [
      resolve("tools/release/stage.mjs"),
      "--platform", "windows",
      "--architecture", "x64",
      "--version", "1.2.3",
      "--code-oss", fixture.codeOss,
      "--service", fixture.service,
      "--cmake-root", fixture.cmakeRoot,
      "--coverage-root", fixture.coverageRoot,
      "--out", fixture.outRoot,
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      env: { ...process.env, SOURCE_DATE_EPOCH: sourceDateEpoch },
      windowsHide: true,
    });

    assert.equal(result.status, 0, result.stderr);
    const stagingRoot = join(fixture.outRoot, "staging", "1.2.3", "windows-x64");
    assert.equal(result.stdout.trim(), stagingRoot);
    const manifest = JSON.parse(await readFile(join(stagingRoot, "release-manifest.json"), "utf8"));
    assert.equal(manifest.version, "1.2.3");
    assert.equal(manifest.platform, "windows");
    assert.equal(manifest.architecture, "x64");
  });
});

test("stageRelease fails closed when SOURCE_DATE_EPOCH is absent", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await assert.rejects(
      () => stageRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        ...fixture,
      }),
      /SOURCE_DATE_EPOCH/u,
    );
  });
});

test("identical staging inputs yield byte-identical normalized package inputs", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const common = {
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    };
    const first = await stageRelease({ ...common, outRoot: join(root, "first") });
    const second = await stageRelease({ ...common, outRoot: join(root, "second") });

    assert.deepEqual(await readFile(first.manifestPath), await readFile(second.manifestPath));
    assert.deepEqual(
      await packageInputSnapshot(first.stagingRoot),
      await packageInputSnapshot(second.stagingRoot),
    );
  });
});
