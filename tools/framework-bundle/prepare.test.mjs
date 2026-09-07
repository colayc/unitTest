import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { validateManifest, verifyLockedArchive } from "./prepare.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");
const manifest = () => ({
  schemaVersion: 1,
  platform: "linux-x64",
  frameworks: [
    { id: "cpputest", version: "4.0", source: { filename: "cpputest-4.0.tar.gz", url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7" }, license: "BSD-3-Clause", sourceDirectory: "cpputest-4.0" },
    { id: "unity", version: "2.6.1", source: { filename: "Unity-2.6.1.tar.gz", url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292" }, license: "MIT", sourceDirectory: "Unity-2.6.1" }
  ]
});

test("framework bootstrap manifest is closed and locks only reviewed Linux HTTPS sources", () => {
  const valid = manifest();
  assert.deepEqual(validateManifest(valid), valid);
  for (const invalid of [
    { ...valid, platform: "windows-x64" },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, source: { ...item.source, url: "https://evil.invalid/cpputest.tgz" } } : item) },
    { ...valid, frameworks: valid.frameworks.map((item, index) => index === 0 ? { ...item, sourceDirectory: "../cpputest" } : item) },
    { ...valid, secret: "nope" }
  ]) assert.throws(() => validateManifest(invalid), /framework manifest|framework input/iu);
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
