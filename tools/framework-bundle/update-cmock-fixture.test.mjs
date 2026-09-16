import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { buildDockerArguments, updateCMockFixture } from "./update-cmock-fixture.mjs";
import { readFrameworkManifest } from "./manifest.mjs";

function assertArgumentPair(arguments_, pair) {
  const index = arguments_.indexOf(pair[0]);
  assert.notEqual(index, -1, `missing ${pair[0]}`);
  assert.equal(arguments_[index + 1], pair[1]);
}

test("buildDockerArguments pins the container and removes ambient privileges", () => {
  const arguments_ = buildDockerArguments({ cmockRoot: "C:/locked/cmock", unityRoot: "C:/locked/unity", fixtureRoot: "C:/repo/testdata/frameworks/unity", outputRoot: "C:/temporary/out" });
  for (const pair of [
    ["--platform", "linux/amd64"],
    ["--network", "none"],
    ["--cap-drop", "ALL"],
    ["--security-opt", "no-new-privileges"],
    ["--pids-limit", "64"]
  ]) assertArgumentPair(arguments_, pair);
  assert.ok(arguments_.includes("--read-only"));
  assert.ok(arguments_.includes("docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556"));
  const mounts = arguments_.filter((value) => value.startsWith("type=bind,"));
  assert.equal(mounts.length, 4);
  assert.ok(mounts.some((value) => value.includes("dst=/cmock") && value.endsWith(",readonly")));
  assert.ok(mounts.some((value) => value.includes("dst=/cmock/vendor/unity") && value.endsWith(",readonly")));
  assert.ok(mounts.some((value) => value.includes("dst=/fixture") && value.endsWith(",readonly")));
  assert.ok(mounts.some((value) => value.includes("dst=/out") && !value.includes("readonly")));
  const image = arguments_.indexOf("docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556");
  assert.deepEqual(arguments_.slice(image + 1), ["ruby", "/cmock/lib/cmock.rb", "-o/fixture/cmock.yml", "/fixture/include/Dependency.h"]);
});

const canonicalConfig = "---\n:cmock:\n  :mock_path: /out\n  :mock_prefix: Mock\n  :mock_suffix: ''\n  :plugins: []\n  :fail_on_unexpected_calls: true\n  :when_no_prototypes: :error\n  :verbosity: 0\n";
const canonicalHeader = "#ifndef UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\n#define UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\n\nint Dependency_Read(int channel);\n\n#endif\n";
const generatedA = { "MockDependency.c": "#include \\\"MockDependency.h\\\"\nint MockDependency(void) { return 1; }\n", "MockDependency.h": "#ifndef MOCKDEPENDENCY_H\n#define MOCKDEPENDENCY_H\n#endif\n" };
const digest = (value) => createHash("sha256").update(value).digest("hex");

async function write(root, path, value) {
  const target = join(root, path);
  await mkdir(dirname(target), { recursive: true });
  await writeFile(target, value);
}

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "utide-cmock-update-"));
  const { manifest, manifestSha256 } = await readFrameworkManifest();
  await Promise.all([
    write(root, "testdata/frameworks/unity/cmock.yml", canonicalConfig),
    write(root, "testdata/frameworks/unity/include/Dependency.h", canonicalHeader),
    write(root, "testdata/frameworks/unity/mocks/MockDependency.c", "old-c\n"),
    write(root, "testdata/frameworks/unity/mocks/MockDependency.h", "old-h\n"),
    write(root, "testdata/frameworks/unity/mocks/cmock-generation.json", "old-provenance\n"),
    write(root, `.superpowers/runtime/framework-bundle/v2/${manifestSha256}/CMock-2.7.0/lib/cmock.rb`, "# locked\n"),
    write(root, `.superpowers/runtime/framework-bundle/v2/${manifestSha256}/Unity-2.6.1/auto/type_sanitizer.rb`, "# locked\n")
  ]);
  return { root, manifest, manifestSha256, preparedRoot: join(root, ".superpowers/runtime/framework-bundle/v2", manifestSha256) };
}

function fakeRunner(outputs) {
  let run = 0;
  return async ({ outputRoot }) => {
    const current = typeof outputs === "function" ? outputs(run++) : outputs;
    for (const [path, bytes] of Object.entries(current)) await writeFile(join(outputRoot, path), bytes);
  };
}

async function oldMocks(root) {
  return Promise.all(["MockDependency.c", "MockDependency.h", "cmock-generation.json"].map((path) => readFile(join(root, "testdata/frameworks/unity/mocks", path), "utf8")));
}

function updateOptions(item, runner, extraOperations = {}) {
  return {
    repositoryRoot: item.root,
    operations: {
      readManifest: async () => ({ manifest: item.manifest, manifestSha256: item.manifestSha256 }),
      verifyPreparedBundle: async () => true,
      runGenerator: runner,
      ...extraOperations
    }
  };
}

test("updateCMockFixture publishes only byte-identical closed generator output", async () => {
  const item = await fixture();
  try {
    const provenance = await updateCMockFixture(updateOptions(item, fakeRunner(generatedA)));
    assert.equal(provenance.generatedAtRuntime, false);
    assert.equal(provenance.configuration.sha256, digest(canonicalConfig));
    assert.equal(provenance.input.sha256, digest(canonicalHeader));
    assert.deepEqual(await oldMocks(item.root), [generatedA["MockDependency.c"], generatedA["MockDependency.h"], `${JSON.stringify(provenance, null, 2)}\n`]);
  } finally { await rm(item.root, { recursive: true, force: true }); }
});

test("updateCMockFixture accepts ordinary generated C comments", async () => {
  const item = await fixture();
  try {
    const comments = { ...generatedA, "MockDependency.c": `/* Generated mock implementation */\n// public mock API\n${generatedA["MockDependency.c"]}` };
    await updateCMockFixture(updateOptions(item, fakeRunner(comments)));
    assert.equal(await readFile(join(item.root, "testdata/frameworks/unity/mocks/MockDependency.c"), "utf8"), comments["MockDependency.c"]);
  } finally { await rm(item.root, { recursive: true, force: true }); }
});

for (const [name, outputs, code] of [
  ["one-byte drift", (run) => run === 0 ? generatedA : { ...generatedA, "MockDependency.c": `${generatedA["MockDependency.c"]} ` }, "CMOCK_GENERATION_NONDETERMINISTIC"],
  ["missing output", { "MockDependency.c": generatedA["MockDependency.c"] }, "CMOCK_GENERATION_OUTPUT_INVALID"],
  ["extra output", { ...generatedA, surprise: "no\n" }, "CMOCK_GENERATION_OUTPUT_INVALID"]
]) test(`updateCMockFixture rolls back when ${name} is generated`, async () => {
  const item = await fixture();
  try {
    const before = await oldMocks(item.root);
    await assert.rejects(updateCMockFixture(updateOptions(item, fakeRunner(outputs))), (error) => error?.code === code);
    assert.deepEqual(await oldMocks(item.root), before);
  } finally { await rm(item.root, { recursive: true, force: true }); }
});

test("updateCMockFixture rolls back when its generator process fails", async () => {
  const item = await fixture();
  try {
    const before = await oldMocks(item.root);
    await assert.rejects(updateCMockFixture(updateOptions(item, async () => { throw new Error("process failed"); })), (error) => error?.code === "CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED");
    assert.deepEqual(await oldMocks(item.root), before);
  } finally { await rm(item.root, { recursive: true, force: true }); }
});

test("updateCMockFixture keeps the published fixture when backup cleanup fails", async () => {
  const item = await fixture();
  let cleanupCalls = 0;
  try {
    const provenance = await updateCMockFixture(updateOptions(item, fakeRunner(generatedA), { cleanupBackup: async () => { cleanupCalls += 1; throw new Error("cleanup denied"); } }));
    assert.equal(provenance.outputSha256.length, 64);
    assert.equal(cleanupCalls, 1);
    assert.equal(await readFile(join(item.root, "testdata/frameworks/unity/mocks/MockDependency.c"), "utf8"), generatedA["MockDependency.c"]);
  } finally { await rm(item.root, { recursive: true, force: true }); }
});

test("updateCMockFixture rejects a concurrent invocation before it can publish", async () => {
  const item = await fixture();
  let started;
  const startedPromise = new Promise((resolve) => { started = resolve; });
  let release;
  const releasePromise = new Promise((resolve) => { release = resolve; });
  try {
    const first = updateCMockFixture(updateOptions(item, async ({ outputRoot }) => { started(); await releasePromise; await fakeRunner(generatedA)({ outputRoot }); }));
    await startedPromise;
    try { await assert.rejects(updateCMockFixture(updateOptions(item, fakeRunner(generatedA))), (error) => error?.code === "CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED"); }
    finally { release(); await first; }
  } finally { release?.(); await rm(item.root, { recursive: true, force: true }); }
});
