import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { lstat, mkdtemp, readdir, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { directoryDigest } from "./manifest.mjs";
import { __testing, prepareFrameworkBundle, validateArchiveEntries } from "./prepare.mjs";

const sha = (value) => createHash("sha256").update(value).digest("hex");
const cases = JSON.parse(await readFile(new URL("../../testdata/frameworks/failures/archive-entries.json", import.meta.url), "utf8"));

test("archive validator rejects every cross-platform unsafe entry class", () => {
  for (const item of cases) assert.throws(() => validateArchiveEntries(item.entries, { sourceDirectory: "CMock-2.7.0" }), (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE", item.name);
});
test("archive validator accepts ordinary regular files and directories", () => {
  assert.doesNotThrow(() => validateArchiveEntries([{ path: "CMock-2.7.0/", type: "directory", size: 0 }, { path: "CMock-2.7.0/lib/", type: "directory", size: 0 }, { path: "CMock-2.7.0/lib/cmock.rb", type: "file", size: 12345 }], { sourceDirectory: "CMock-2.7.0" }));
});
test("archive validator bounds entry count, depth, and expanded bytes", () => {
  assert.throws(() => validateArchiveEntries(Array.from({ length: 8193 }, (_, index) => ({ path: `CMock-2.7.0/${index}`, type: "file", size: 0 })), { sourceDirectory: "CMock-2.7.0" }), (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE");
  assert.throws(() => validateArchiveEntries([{ path: `CMock-2.7.0/${Array.from({ length: 33 }, () => "d").join("/")}`, type: "file", size: 0 }], { sourceDirectory: "CMock-2.7.0" }), (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE");
  assert.throws(() => validateArchiveEntries([{ path: "CMock-2.7.0/large", type: "file", size: 256 * 1024 * 1024 + 1 }], { sourceDirectory: "CMock-2.7.0" }), (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE");
});
async function prepareFixture({ entries, actualLicense = Buffer.from("license"), expectedLicense = actualLicense } = {}) {
  const root = await mkdtemp(join(tmpdir(), "utide-framework-")); const cacheRoot = join(root, "cache"); const runtimeRoot = join(root, "runtime");
  const descriptions = [["cpputest", "cpputest-4.0", "CMakeLists.txt"], ["unity", "Unity-2.6.1", "src/unity.c"], ["cmock", "CMock-2.7.0", "lib/cmock.rb"]];
  const manifest = { schemaVersion: 2, platforms: ["linux-x64", "windows-x64"], fixtureTools: {}, frameworks: [] };
  for (const [id, sourceDirectory, marker] of descriptions) manifest.frameworks.push({ id, sourceDirectory, marker, source: { filename: `${id}.tgz`, url: `https://github.com/example/${id}`, sha256: sha(Buffer.from(`archive-${id}`)) }, license: { path: "LICENSE.txt", sha256: sha(expectedLicense) }, treeSha256: "" });
  for (const input of manifest.frameworks) { const scratch = join(root, `tree-${input.id}`); await __testing.mkdirp(dirname(join(scratch, input.marker))); await writeFile(join(scratch, input.marker), "x"); await writeFile(join(scratch, "LICENSE.txt"), actualLicense); input.treeSha256 = await directoryDigest(scratch); }
  const operations = {
    readManifest: async () => ({ manifest, manifestSha256: sha(Buffer.from("fixture-manifest")) }),
    download: async (input, target) => writeFile(target, Buffer.from(`archive-${input.id}`)),
    inspectArchive: async (input) => entries ?? [{ path: `${input.sourceDirectory}/`, type: "directory", size: 0 }, { path: `${input.sourceDirectory}/${input.marker}`, type: "file", size: 1 }, { path: `${input.sourceDirectory}/LICENSE.txt`, type: "file", size: actualLicense.length }],
    extractArchive: async (input, staging) => { await __testing.mkdirp(dirname(join(staging, input.sourceDirectory, input.marker))); await writeFile(join(staging, input.sourceDirectory, input.marker), "x"); await writeFile(join(staging, input.sourceDirectory, "LICENSE.txt"), actualLicense); }
  };
  return { root, runtimeRoot, cacheRoot, operations, result: await prepareFrameworkBundle({ cacheRoot, runtimeRoot, operations }) };
}
test("preparation publishes a digest-keyed v2 bundle and reuses a verified target", async () => {
  const fixture = await prepareFixture(); assert.equal(fixture.result.root, join(fixture.runtimeRoot, "v2", fixture.result.manifestSha256)); assert.equal(fixture.result.reused, false); assert.deepEqual(await readdir(fixture.runtimeRoot), ["v2"]);
  const repeated = await prepareFrameworkBundle({ cacheRoot: fixture.cacheRoot, runtimeRoot: fixture.runtimeRoot, operations: fixture.operations }); assert.equal(repeated.reused, true); assert.equal((await lstat(join(repeated.root, "READY"))).isFile(), true);
});
test("preparation rejects unsafe entries before extraction", async () => { await assert.rejects(prepareFixture({ entries: [{ path: "C:/escape", type: "file", size: 1 }] }), (error) => error?.code === "FRAMEWORK_ARCHIVE_UNSAFE"); });
test("preparation rejects substituted license bytes", async () => { await assert.rejects(prepareFixture({ actualLicense: Buffer.from("substituted"), expectedLicense: Buffer.from("license") }), (error) => error?.code === "FRAMEWORK_LICENSE_MISMATCH"); });
