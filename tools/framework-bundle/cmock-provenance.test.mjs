import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { aggregateOutputDigest, readCMockGeneration } from "./cmock-provenance.mjs";
import { readFrameworkManifest } from "./manifest.mjs";

const sha = (value) => createHash("sha256").update(value).digest("hex");
const canonical = (value) => `${JSON.stringify(value, null, 2)}\n`;
const manifest = (await readFrameworkManifest()).manifest;
const manifestSha256 = (await readFrameworkManifest()).manifestSha256;
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "utide-cmock-provenance-"));
  const config = "---\n:cmock:\n  :mock_path: /out\n";
  const input = "#ifndef UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\n#define UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\nint Dependency_Read(int channel);\n#endif\n";
  const c = "/* Generated CMock fixture */\n// Ordinary generated comment\n#include \"MockDependency.h\"\nint Dependency_Read_CMockReturnMemThruPtr(void) { return 0; }\n";
  const h = "#ifndef MOCKDEPENDENCY_H\n#define MOCKDEPENDENCY_H\n#endif\n";
  await Promise.all([write(root, "testdata/frameworks/unity/cmock.yml", config), write(root, "testdata/frameworks/unity/include/Dependency.h", input), write(root, "testdata/frameworks/unity/mocks/MockDependency.c", c), write(root, "testdata/frameworks/unity/mocks/MockDependency.h", h)]);
  const files = [{ path: "MockDependency.c", bytes: Buffer.from(c) }, { path: "MockDependency.h", bytes: Buffer.from(h) }];
  const value = { schemaVersion: 1, cmock: { version: "2.7.0", tag: "v2.7.0", revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af" }, generator: { entrypoint: "lib/cmock.rb", version: "2.7.0", containerImage: "docker.io/library/ruby", containerPlatform: "linux/amd64", containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556" }, configuration: { path: "testdata/frameworks/unity/cmock.yml", sha256: sha(config) }, input: { path: "testdata/frameworks/unity/include/Dependency.h", sha256: sha(input) }, outputs: files.map((file) => ({ path: file.path, sha256: sha(file.bytes) })), outputSha256: aggregateOutputDigest(files), frameworkManifestSha256: manifestSha256, generatedAtRuntime: false };
  const provenance = join(root, "testdata/frameworks/unity/mocks/cmock-generation.json"); await writeFile(provenance, canonical(value));
  return { root, provenance, value, files };
}
async function write(root, path, value) { const target = join(root, path); await (await import("node:fs/promises")).mkdir(dirname(target), { recursive: true }); await writeFile(target, value); }
async function rejectMutation(name, mutate) { await test(name, async () => { const item = await fixture(); try { await mutate(item); await assert.rejects(readCMockGeneration(item.provenance, { root: item.root, manifest, manifestSha256 }), (error) => error?.code === "CMOCK_PROVENANCE_INVALID"); } finally { await rm(item.root, { recursive: true, force: true }); } }); }
test("closed provenance accepts only the canonical identity and stable output digest", async () => { const item = await fixture(); try { const result = await readCMockGeneration(item.provenance, { root: item.root, manifest, manifestSha256 }); assert.equal(result.outputSha256, aggregateOutputDigest([...item.files].reverse())); assert.equal(result.cMockProvenanceSha256, sha(await readFile(item.provenance))); } finally { await rm(item.root, { recursive: true, force: true }); } });
for (const [name, mutate] of [
  ["missing provenance field", async ({ provenance, value }) => { delete value.input; await writeFile(provenance, canonical(value)); }],
  ["extra provenance field", async ({ provenance, value }) => { value.extra = true; await writeFile(provenance, canonical(value)); }],
  ["reordered output", async ({ provenance, value }) => { value.outputs.reverse(); await writeFile(provenance, canonical(value)); }],
  ["duplicate output", async ({ provenance, value }) => { value.outputs[1] = value.outputs[0]; await writeFile(provenance, canonical(value)); }],
  ["wrong revision", async ({ provenance, value }) => { value.cmock.revision = "0".repeat(40); await writeFile(provenance, canonical(value)); }],
  ["image substitution", async ({ provenance, value }) => { value.generator.containerImage = "docker.io/library/alpine"; await writeFile(provenance, canonical(value)); }],
  ["input substitution", async ({ provenance, value }) => { value.input.path = "elsewhere.h"; await writeFile(provenance, canonical(value)); }],
  ["output substitution", async ({ provenance, value }) => { value.outputs[0].sha256 = "0".repeat(64); await writeFile(provenance, canonical(value)); }],
  ["CRLF generated output", async ({ root }) => writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), "x\r\n")],
  ["absolute path generated output", async ({ root }) => writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), "C:\\\\Users\\\\unsafe\n")],
  ["timestamp generated banner", async ({ root }) => writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), "Generated on 2026-09-16T12:00\n")],
  ["non-UTF-8 generated output", async ({ root }) => writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), Buffer.from([0xff]))],
  ["unknown output file", async ({ root }) => writeFile(join(root, "testdata/frameworks/unity/mocks/unexpected"), "x")]
]) await rejectMutation(name, mutate);
test("symlinked output is rejected", async (t) => { const item = await fixture(); try { const target = join(item.root, "elsewhere"); await writeFile(target, "x"); try { await rm(join(item.root, "testdata/frameworks/unity/mocks/MockDependency.c")); await symlink(target, join(item.root, "testdata/frameworks/unity/mocks/MockDependency.c"), "file"); } catch (error) { t.skip(`symlink unavailable: ${error.code}`); return; } await assert.rejects(readCMockGeneration(item.provenance, { root: item.root, manifest, manifestSha256 }), (error) => error?.code === "CMOCK_PROVENANCE_INVALID"); } finally { await rm(item.root, { recursive: true, force: true }); } });
