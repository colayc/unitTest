import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  prepareLinuxFrameworkInputs,
  readResolvedFrameworkTrees,
  verifyResolvedFrameworkTrees,
  validateLinuxFrameworkInputManifest,
  type LinuxFrameworkInputManifest
} from "./linux-framework-inputs.js";

const digest = (value: string) => createHash("sha256").update(value).digest("hex");

function manifest(): LinuxFrameworkInputManifest {
  return {
    schemaVersion: 2,
    platforms: ["linux-x64", "windows-x64"],
    fixtureTools: {
      cmakeHelper: { path: "sdk/cmake/UnitTestIDE.cmake", sha256: "8f05b38b718ad6fca635ab043647099934339135652e0fcfeef62610164b748c" },
      unityRunnerGenerator: { name: "unity-runner-generator", schemaVersion: 1, version: "1.0.0", runnerProtocol: "utide.runner.v1" },
      cmockGenerator: { frameworkId: "cmock", version: "2.7.0", entrypoint: "lib/cmock.rb", containerImage: "docker.io/library/ruby", containerTag: "3.3.6-bookworm", containerPlatform: "linux/amd64", containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556", generatedAtRuntime: false }
    },
    frameworks: [
      {
        id: "cpputest",
        version: "4.0",
        tag: "v4.0",
        revision: "b9b841c56c524a10ccd40e88c3acaf9d5ec751c2",
        source: {
          filename: "cpputest-4.0.tar.gz",
          url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz",
          sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
        },
        license: { spdx: "BSD-3-Clause", path: "COPYING", sha256: "d8fe282e4047197e1fbd6ef2527bde832a1514be6bd82fac7d1296ce184285c8" },
        sourceDirectory: "cpputest-4.0",
        treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
      },
      {
        id: "unity",
        version: "2.6.1",
        tag: "v2.6.1",
        revision: "cbcd08fa7de711053a3deec6339ee89cad5d2697",
        source: {
          filename: "Unity-2.6.1.tar.gz",
          url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz",
          sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292"
        },
        license: { spdx: "MIT", path: "LICENSE.txt", sha256: "907d9e859c6433703c0c183de3ddeaaf4baf3d517382f8f368b2c190fd2581d1" },
        sourceDirectory: "Unity-2.6.1",
        treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae"
      },
      {
        id: "cmock",
        version: "2.7.0",
        tag: "v2.7.0",
        revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
        source: { filename: "CMock-2.7.0.tar.gz", url: "https://github.com/ThrowTheSwitch/CMock/archive/refs/tags/v2.7.0.tar.gz", sha256: "d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c" },
        license: { spdx: "MIT", path: "LICENSE.txt", sha256: "f19bba29498b9405a86ab5fdc6bc58654fffb197603834e6d1423d583649b35c" },
        sourceDirectory: "CMock-2.7.0",
        treeSha256: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3"
      }
    ]
  };
}

test("Linux framework lock is closed and rejects missing, tampered, escaped and network-enabled inputs", () => {
  const valid = manifest();
  assert.deepEqual(validateLinuxFrameworkInputManifest(valid), valid);
  for (const candidate of [
    { ...valid, frameworks: valid.frameworks.slice(0, 2) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, version: "4.1" } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, sourceDirectory: "../cpputest" } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, treeSha256: "0".repeat(64) } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 2 ? { ...item, revision: "0".repeat(40) } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 2 ? { ...item, license: { ...item.license, sha256: "0".repeat(64) } } : item) },
    { ...valid, fixtureTools: { ...valid.fixtureTools, cmakeHelper: { ...valid.fixtureTools.cmakeHelper, sha256: "0".repeat(64) } } },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, source: { ...item.source, url: "http://example.invalid/cpputest.tar.gz" } } : item) },
    { ...valid, token: "must-not-leak" }
  ]) {
    assert.throws(() => validateLinuxFrameworkInputManifest(candidate), /Linux framework (input|fixture)/u);
  }
});

test("Linux framework boundary requires a manifest digest before inspecting inputs", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-required-digest-"));
  try {
    await assert.rejects(prepareLinuxFrameworkInputs({
      manifest: manifest(), cacheRoot: join(root, "cache"), sourceRoot: join(root, "sources"), helperPath: join(root, "helper"), generatorPath: join(root, "generator"), repositoryRoot: root,
      manifestSha256: undefined as unknown as string
    }), /manifest digest is required/u);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("Linux framework boundary rejects an altered prepared manifest digest and CMock tree substitution", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-v2-"));
  const valid = manifest();
  try {
    await assert.rejects(readResolvedFrameworkTrees(join(root, "does-not-exist"), valid, "not-a-digest"), /resolved manifest has an invalid identity/u);
    await writeFile(join(root, "manifest.resolved.json"), `${JSON.stringify({
      schemaVersion: valid.schemaVersion, manifestSha256: "a".repeat(64), platforms: valid.platforms, fixtureTools: valid.fixtureTools,
      frameworks: valid.frameworks.map(({ id, version, tag, revision, source, license, sourceDirectory, treeSha256 }) => ({ id, version, tag, revision, source: { filename: source.filename, sha256: source.sha256 }, license, sourceDirectory, treeSha256 }))
    })}\n`);
    await assert.rejects(readResolvedFrameworkTrees(root, valid, "b".repeat(64)), /resolved manifest has an invalid identity/u);

    const cmock = join(root, "CMock-2.7.0");
    const cpputest = join(root, "cpputest-4.0");
    const unity = join(root, "Unity-2.6.1");
    await mkdir(join(cmock, "lib"), { recursive: true });
    await mkdir(join(unity, "src"), { recursive: true });
    await mkdir(cpputest, { recursive: true });
    await writeFile(join(cmock, "lib", "cmock.rb"), "# expected\n");
    await writeFile(join(cpputest, "CMakeLists.txt"), "project(CppUTest)\n");
    await writeFile(join(unity, "src", "unity.c"), "void UnityBegin(void) {}\n");
    const expected = await verifyResolvedFrameworkTrees(root, valid, undefined);
    await writeFile(join(cmock, "lib", "cmock.rb"), "# substituted\n");
    await assert.rejects(verifyResolvedFrameworkTrees(root, valid, expected), /tree digest mismatch: cmock/u);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("Linux framework boundary accepts only expanded trees bound to the resolved bootstrap identity", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-tree-"));
  try {
    const cpputest = join(root, "cpputest-4.0");
    const unity = join(root, "Unity-2.6.1");
    const cmock = join(root, "CMock-2.7.0");
    await mkdir(join(unity, "src"), { recursive: true });
    await mkdir(join(cmock, "lib"), { recursive: true });
    await mkdir(cpputest, { recursive: true });
    await writeFile(join(cpputest, "CMakeLists.txt"), "project(CppUTest)\n");
    await writeFile(join(unity, "src", "unity.c"), "void UnityBegin(void) {}\n");
    await writeFile(join(cmock, "lib", "cmock.rb"), "# cmock\n");
    await writeFile(join(cmock, "B"), "B");
    await writeFile(join(cmock, "a"), "a");
    const expected = await verifyResolvedFrameworkTrees(root, manifest(), undefined);
    assert.equal(expected.find((item) => item.id === "cmock")?.treeSha256, "20e071e0cf2d6eccede5c0f4206c0a8bf2b04496640d11047b457f81d52c5ef2");
    await verifyResolvedFrameworkTrees(root, manifest(), expected);
    await assert.rejects(verifyResolvedFrameworkTrees(root, manifest(), manifest().frameworks), /tree digest mismatch/u);
    await writeFile(join(cpputest, "CMakeLists.txt"), "tampered\n");
    await assert.rejects(verifyResolvedFrameworkTrees(root, manifest(), expected), /tree digest mismatch/u);
  } finally {
    await import("node:fs/promises").then(({ rm }) => rm(root, { recursive: true, force: true }));
  }
});

test("Linux framework tree boundary accepts a canonical tree reached through a filesystem alias", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-alias-"));
  const actual = join(root, "actual");
  const alias = join(root, "alias");
  try {
    await mkdir(join(actual, "cpputest-4.0"), { recursive: true });
    await mkdir(join(actual, "Unity-2.6.1", "src"), { recursive: true });
    await mkdir(join(actual, "CMock-2.7.0", "lib"), { recursive: true });
    await writeFile(join(actual, "cpputest-4.0", "CMakeLists.txt"), "project(CppUTest)\n");
    await writeFile(join(actual, "Unity-2.6.1", "src", "unity.c"), "void UnityBegin(void) {}\n");
    await writeFile(join(actual, "CMock-2.7.0", "lib", "cmock.rb"), "# cmock\n");
    await symlink(actual, alias, process.platform === "win32" ? "junction" : "dir");

    assert.deepEqual(
      await verifyResolvedFrameworkTrees(alias, manifest(), undefined),
      await verifyResolvedFrameworkTrees(actual, manifest(), undefined)
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("Linux framework boundary rejects a fabricated expanded tree whose locked archive bytes do not match", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-inputs-"));
  const cacheRoot = join(root, "cache");
  const sourceRoot = join(root, "sources");
  const helperPath = join(root, "UnitTestIDE.cmake");
  const generatorPath = join(root, "unity-runner-generator");
  const valid = manifest();
  await mkdir(cacheRoot, { recursive: true });
  await mkdir(join(sourceRoot, "cpputest-4.0"), { recursive: true });
  await mkdir(join(sourceRoot, "Unity-2.6.1", "src"), { recursive: true });
  await writeFile(join(cacheRoot, `${valid.frameworks[0]!.source.sha256}-${valid.frameworks[0]!.source.filename}`), "cpputest source archive");
  await writeFile(join(cacheRoot, `${valid.frameworks[1]!.source.sha256}-${valid.frameworks[1]!.source.filename}`), "unity source archive");
  await writeFile(join(sourceRoot, "cpputest-4.0", "CMakeLists.txt"), "project(CppUTest)");
  await writeFile(join(sourceRoot, "Unity-2.6.1", "src", "unity.c"), "void UnityBegin(void) {}");
  await writeFile(helperPath, "# verified helper\n");
  await writeFile(generatorPath, "verified generator\n");

  await assert.rejects(prepareLinuxFrameworkInputs({
    manifest: valid,
    manifestSha256: "a".repeat(64),
    cacheRoot,
    sourceRoot,
    helperPath,
    generatorPath,
    repositoryRoot: root
  }), /archive digest mismatch/u);
});
