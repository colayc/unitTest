import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { validateBundlePaths, validateManifest, validateTarEntries, verifyLockedArchive } from "./prepare.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");
const manifest = () => ({
  schemaVersion: 1,
  platform: "linux-x64",
  fixtureTools: {
    cmakeHelper: { path: "sdk/cmake/UnitTestIDE.cmake", sha256: "2297b37584d134b901f0da0dea5d60d67853a496dbf18874c220643ffd2cd2da" },
    unityRunnerGenerator: { name: "unity-runner-generator", schemaVersion: 1, version: "1.0.0", runnerProtocol: "utide.runner.v1" }
  },
  frameworks: [
    { id: "cpputest", version: "4.0", source: { filename: "cpputest-4.0.tar.gz", url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7" }, license: "BSD-3-Clause", sourceDirectory: "cpputest-4.0", treeSha256: "3b83f01045ca74b9a0996913723fb7e24824452dee0e5fd050f847bb15e404a1" },
    { id: "unity", version: "2.6.1", source: { filename: "Unity-2.6.1.tar.gz", url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292" }, license: "MIT", sourceDirectory: "Unity-2.6.1", treeSha256: "ef6b833c394d7af7c2b733f87d38eb5bae4442bc1c237778bbaa8c0086ba00db" }
  ]
});

test("framework bootstrap manifest is closed and locks only reviewed Linux HTTPS sources", () => {
  const valid = manifest();
  assert.deepEqual(validateManifest(valid), valid);
  for (const invalid of [
    { ...valid, platform: "windows-x64" },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, source: { ...item.source, url: "https://evil.invalid/cpputest.tgz" } } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, sourceDirectory: "../cpputest" } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, treeSha256: "0".repeat(64) } : item) },
    { ...valid, fixtureTools: { ...valid.fixtureTools, cmakeHelper: { ...valid.fixtureTools.cmakeHelper, sha256: "0".repeat(64) } } },
    { ...valid, secret: "nope" }
  ]) assert.throws(() => validateManifest(invalid), /framework manifest|framework input|framework fixture/u);
});

test("framework bootstrap rejects a missing or tampered immutable cache archive", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-framework-archive-"));
  const locked = { ...manifest().frameworks[0], source: { ...manifest().frameworks[0].source, sha256: digest("cpp") } };
  const archive = join(root, `${locked.source.sha256}-${locked.source.filename}`);
  await assert.rejects(verifyLockedArchive(root, locked), /missing|ENOENT/iu);
  await writeFile(archive, "tampered");
  await assert.rejects(verifyLockedArchive(root, locked), /digest mismatch/iu);
  await writeFile(archive, "cpp");
  assert.equal(await verifyLockedArchive(root, locked), archive);
});

test("framework bootstrap confines every mutable path to its approved repository roots", () => {
  const root = resolve("C:/unit-test-ide-framework-boundary");
  const valid = {
    manifestPath: join(root, "tools", "framework-bundle", "manifest.json"),
    cacheRoot: join(root, ".superpowers", "cache", "framework-bundle"),
    outputRoot: join(root, ".superpowers", "runtime", "framework-bundle", "linux-x64")
  };
  assert.deepEqual(validateBundlePaths(valid, root), valid);
  for (const invalid of [
    { ...valid, manifestPath: join(root, "manifest.json") },
    { ...valid, cacheRoot: join(root, ".superpowers", "cache", "..", "outside") },
    { ...valid, outputRoot: join(root, ".superpowers", "runtime", "framework-bundle", "other") }
  ]) assert.throws(() => validateBundlePaths(invalid, root), /approved|repository/u);
});

test("framework bootstrap rejects archives whose expanded shape exceeds bounded extraction limits", () => {
  assert.doesNotThrow(() => validateTarEntries(["cpputest-4.0/", "cpputest-4.0/CMakeLists.txt"], ["drwxr-xr-x owner/group 0 2026-01-01 00:00 cpputest-4.0/", "-rw-r--r-- owner/group 8 2026-01-01 00:00 cpputest-4.0/CMakeLists.txt"]));
  assert.throws(() => validateTarEntries(["root/".repeat(33)], ["-rw-r--r-- owner/group 1 2026-01-01 00:00 deep"]), /depth/u);
  assert.throws(() => validateTarEntries(["root/file"], ["-rw-r--r-- owner/group 268435457 2026-01-01 00:00 root/file"]), /expanded size/u);
  assert.throws(() => validateTarEntries(Array.from({ length: 8193 }, (_, index) => `root/${index}`), Array.from({ length: 8193 }, () => "-rw-r--r-- owner/group 0 2026-01-01 00:00 file")), /entry count/u);
});
