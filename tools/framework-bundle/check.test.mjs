import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { auditArchiveCache, checkFrameworkBundle } from "./check.mjs";

const sourceRoot = join(import.meta.dirname, "..", "..");
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "utide-framework-check-"));
  for (const path of ["tools/framework-bundle/manifest.json", "tools/framework-bundle/licenses/dependencies.json"]) { const bytes = await readFile(join(sourceRoot, path)); const target = join(root, path); await (await import("node:fs/promises")).mkdir(dirname(target), { recursive: true }); await writeFile(target, bytes); }
  const config = "---\n:cmock:\n  :mock_path: /out\n", input = "int Dependency_Read(int channel);\n", c = "#include \"MockDependency.h\"\n", h = "#ifndef MOCKDEPENDENCY_H\n#define MOCKDEPENDENCY_H\n#endif\n";
  const crypto = await import("node:crypto"); const sha = (value) => crypto.createHash("sha256").update(value).digest("hex"); const aggregate = sha(Buffer.concat([Buffer.from("f:MockDependency.c\0"), Buffer.from(c), Buffer.from("f:MockDependency.h\0"), Buffer.from(h)]));
  for (const [path, value] of [["testdata/frameworks/unity/cmock.yml", config], ["testdata/frameworks/unity/include/Dependency.h", input], ["testdata/frameworks/unity/mocks/MockDependency.c", c], ["testdata/frameworks/unity/mocks/MockDependency.h", h]]) { const target = join(root, path); await (await import("node:fs/promises")).mkdir(dirname(target), { recursive: true }); await writeFile(target, value); }
  const manifestBytes = await readFile(join(root, "tools/framework-bundle/manifest.json")); const provenance = { schemaVersion: 1, cmock: { version: "2.7.0", tag: "v2.7.0", revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af" }, generator: { entrypoint: "lib/cmock.rb", version: "2.7.0", containerImage: "docker.io/library/ruby", containerPlatform: "linux/amd64", containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556" }, configuration: { path: "testdata/frameworks/unity/cmock.yml", sha256: sha(config) }, input: { path: "testdata/frameworks/unity/include/Dependency.h", sha256: sha(input) }, outputs: [{ path: "MockDependency.c", sha256: sha(c) }, { path: "MockDependency.h", sha256: sha(h) }], outputSha256: aggregate, frameworkManifestSha256: sha(manifestBytes), generatedAtRuntime: false }; await writeFile(join(root, "testdata/frameworks/unity/mocks/cmock-generation.json"), `${JSON.stringify(provenance, null, 2)}\n`); return root;
}
test("offline check validates repository inputs without cache or external seams", async () => { const root = await fixture(); try { const result = await checkFrameworkBundle({ repositoryRoot: root, fetch: () => { throw new Error("called"); }, execFile: () => { throw new Error("called"); }, docker: () => { throw new Error("called"); }, generator: () => { throw new Error("called"); } }); assert.match(result.manifestSha256, /^[0-9a-f]{64}$/u); assert.match(result.cMockProvenanceSha256, /^[0-9a-f]{64}$/u); } finally { await rm(root, { recursive: true, force: true }); } });
test("offline check redacts local paths for invalid provenance and prepared cache", async () => { const root = await fixture(); try { await writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), "x\r\n"); await assert.rejects(checkFrameworkBundle({ repositoryRoot: root }), (error) => error?.code === "CMOCK_PROVENANCE_INVALID" && !error.message.includes(root)); await writeFile(join(root, "testdata/frameworks/unity/mocks/MockDependency.c"), "#include \"MockDependency.h\"\n"); await (await import("node:fs/promises")).mkdir(join(root, ".superpowers/runtime/framework-bundle/v2", "0".repeat(64)), { recursive: true }); await assert.rejects(checkFrameworkBundle({ repositoryRoot: root, preparedRoot: join(root, ".superpowers/runtime/framework-bundle/v2", "0".repeat(64)) }), (error) => error?.code === "FRAMEWORK_CACHE_INVALID" && !error.message.includes(root)); } finally { await rm(root, { recursive: true, force: true }); } });
test("offline check rejects existing prepared file and symlink paths", async (t) => { const root = await fixture(); try { const file = join(root, "prepared-file"); await writeFile(file, "x"); await assert.rejects(checkFrameworkBundle({ repositoryRoot: root, preparedRoot: file }), (error) => error?.code === "FRAMEWORK_CACHE_INVALID" && !error.message.includes(root)); const link = join(root, "prepared-link"); try { await symlink(file, link, "file"); } catch (error) { t.skip(`symlink unavailable: ${error.code}`); return; } await assert.rejects(checkFrameworkBundle({ repositoryRoot: root, preparedRoot: link }), (error) => error?.code === "FRAMEWORK_CACHE_INVALID" && !error.message.includes(root)); } finally { await rm(root, { recursive: true, force: true }); } });

test("offline check rejects corrupt and unrecognized existing archive cache entries", async () => {
  const root = await fixture();
  try {
    const cache = join(root, ".superpowers/cache/framework-bundle");
    await mkdir(cache, { recursive: true });
    await assert.doesNotReject(checkFrameworkBundle({ repositoryRoot: root }));
    const manifest = JSON.parse(await readFile(join(root, "tools/framework-bundle/manifest.json"), "utf8"));
    const input = manifest.frameworks[0];
    const path = join(cache, `${input.source.sha256}-${input.source.filename}`);
    await writeFile(path, "substituted cached archive");
    await assert.rejects(checkFrameworkBundle({ repositoryRoot: root }), (error) => error.code === "FRAMEWORK_CACHE_INVALID" && !error.message.includes(root));
    await rm(path);
    await writeFile(join(cache, "unlocked.tgz"), "unknown");
    await assert.rejects(checkFrameworkBundle({ repositoryRoot: root }), (error) => error.code === "FRAMEWORK_CACHE_INVALID");
  } finally { await rm(root, { recursive: true, force: true }); }
});
test("offline check rejects archive cache links and non-directory roots without downloading", async () => {
  const root = await fixture();
  try {
    const cache = join(root, ".superpowers/cache/framework-bundle");
    await mkdir(dirname(cache), { recursive: true });
    await writeFile(cache, "not a directory");
    await assert.rejects(checkFrameworkBundle({ repositoryRoot: root }), (error) => error.code === "FRAMEWORK_CACHE_INVALID");
    await rm(cache);
    const target = join(root, "external-cache");
    await mkdir(target);
    await symlink(target, cache, process.platform === "win32" ? "junction" : "dir");
    await assert.rejects(checkFrameworkBundle({ repositoryRoot: root }), (error) => error.code === "FRAMEWORK_CACHE_INVALID");
  } finally { await rm(root, { recursive: true, force: true }); }
});
test("archive cache audit accepts a verified partial cache but rejects file links", async () => {
  const root = await mkdtemp(join(tmpdir(), "utide-archive-audit-"));
  const bytes = Buffer.from("trusted archive bytes");
  const digest = (await import("node:crypto")).createHash("sha256").update(bytes).digest("hex");
  const input = { id: "cpputest", source: { sha256: digest, filename: "fixture.tgz" } };
  const path = join(root, `${digest}-fixture.tgz`);
  try {
    await writeFile(path, bytes);
    await assert.doesNotReject(auditArchiveCache(root, { frameworks: [input, { id: "absent", source: { sha256: "a".repeat(64), filename: "absent.tgz" } }] }));
    await rm(path);
    const external = join(root, "..", `${root.split(/[\\/]/u).at(-1)}-target`);
    try {
      await writeFile(external, bytes);
      await symlink(external, path, "file");
      await assert.rejects(auditArchiveCache(root, { frameworks: [input] }), (error) => error.code === "FRAMEWORK_CACHE_INVALID");
    } finally { await rm(external, { force: true }); }
  } finally { await rm(root, { recursive: true, force: true }); }
});
