import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  prepareLinuxFrameworkInputs,
  verifyResolvedFrameworkTrees,
  validateLinuxFrameworkInputManifest,
  type LinuxFrameworkInputManifest
} from "./linux-framework-inputs.js";

const digest = (value: string) => createHash("sha256").update(value).digest("hex");

function manifest(): LinuxFrameworkInputManifest {
  return {
    schemaVersion: 1,
    platform: "linux-x64",
    frameworks: [
      {
        id: "cpputest",
        version: "4.0",
        source: {
          filename: "cpputest-4.0.tar.gz",
          url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz",
          sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
        },
        license: "BSD-3-Clause",
        sourceDirectory: "cpputest-4.0"
      },
      {
        id: "unity",
        version: "2.6.1",
        source: {
          filename: "Unity-2.6.1.tar.gz",
          url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz",
          sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292"
        },
        license: "MIT",
        sourceDirectory: "Unity-2.6.1"
      }
    ]
  };
}

test("Linux framework lock is closed and rejects missing, tampered, escaped and network-enabled inputs", () => {
  const valid = manifest();
  assert.deepEqual(validateLinuxFrameworkInputManifest(valid), valid);
  for (const candidate of [
    { ...valid, frameworks: valid.frameworks.slice(0, 1) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, version: "4.1" } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, sourceDirectory: "../cpputest" } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, source: { ...item.source, url: "http://example.invalid/cpputest.tar.gz" } } : item) },
    { ...valid, token: "must-not-leak" }
  ]) {
    assert.throws(() => validateLinuxFrameworkInputManifest(candidate), /Linux framework input/u);
  }
});

test("Linux framework boundary accepts only expanded trees bound to the resolved bootstrap identity", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-linux-framework-tree-"));
  try {
    const cpputest = join(root, "cpputest-4.0");
    const unity = join(root, "Unity-2.6.1");
    await mkdir(join(unity, "src"), { recursive: true });
    await mkdir(cpputest, { recursive: true });
    await writeFile(join(cpputest, "CMakeLists.txt"), "project(CppUTest)\n");
    await writeFile(join(unity, "src", "unity.c"), "void UnityBegin(void) {}\n");
    const expected = await verifyResolvedFrameworkTrees(root, manifest(), undefined);
    await verifyResolvedFrameworkTrees(root, manifest(), expected);
    await writeFile(join(cpputest, "CMakeLists.txt"), "tampered\n");
    await assert.rejects(verifyResolvedFrameworkTrees(root, manifest(), expected), /tree digest mismatch/u);
  } finally {
    await import("node:fs/promises").then(({ rm }) => rm(root, { recursive: true, force: true }));
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
    cacheRoot,
    sourceRoot,
    helperPath,
    generatorPath,
    repositoryRoot: root
  }), /archive digest mismatch/u);
});
