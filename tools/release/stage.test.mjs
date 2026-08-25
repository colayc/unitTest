import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import { stageRelease } from "./stage.mjs";

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

test("stageRelease copies the deterministic staging layout and writes a release manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const result = await stageRelease({
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      ...fixture,
    });

    assert.equal(result.stagingRoot, join(fixture.outRoot, "staging", "1.2.3", "windows-x64"));
    for (const relativePath of [
      "app/code-oss",
      "app/extensions/unit-test-ide/package.json",
      "app/extensions/unit-test-ide/dist/src/extension.js",
      "service/unit-test-service",
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
      sha256(await readFile(join(result.stagingRoot, "app/code-oss"))),
      sha256("code oss runtime\n"),
    );
    assert.equal(
      sha256(await readFile(join(result.stagingRoot, "service/unit-test-service"))),
      sha256("service binary\n"),
    );

    const manifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    assert.equal(manifest.version, "1.2.3");
    assert.equal(manifest.platform, "windows");
    assert.equal(manifest.architecture, "x64");
    assert.equal(manifest.sourceCommit, "a".repeat(40));
    assert.ok(
      manifest.licenses.includes("licenses/cmake/licenses/LICENSE.txt")
      && manifest.licenses.includes("licenses/coverage/licenses/NOTICE.txt"),
    );
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
