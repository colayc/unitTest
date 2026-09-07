import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  prepareLinuxFrameworkInputs,
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
          sha256: digest("cpputest source archive")
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
          sha256: digest("unity source archive")
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

test("Linux framework boundary verifies locked archives, license metadata and the sealed test-only helper seam", async () => {
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

  const boundary = await prepareLinuxFrameworkInputs({
    manifest: valid,
    cacheRoot,
    sourceRoot,
    helperPath,
    generatorPath
  });

  assert.deepEqual(boundary.frameworks, ["cpputest", "unity"]);
  assert.equal(boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT, join(sourceRoot, "cpputest-4.0"));
  assert.equal(boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT, join(sourceRoot, "Unity-2.6.1"));
  assert.equal(boundary.environment.UNIT_TEST_IDE_TEST_CMAKE_HELPER, helperPath);
  assert.equal(boundary.environment.UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR, generatorPath);
  assert.match(boundary.identityDigest, /^[0-9a-f]{64}$/u);
});
