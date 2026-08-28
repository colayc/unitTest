import assert from "node:assert/strict";
import { link, lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { createReleaseInputProvenance } from "./provenance.mjs";
import {
  validateProducerRunMetadata,
  validateTrustedReleaseInputs,
  __testOnlyTrustedRun,
} from "./trusted-run.mjs";

const directory = dirname(fileURLToPath(import.meta.url));
const script = join(directory, "trusted-run.mjs");
const UNTRUSTED = "RELEASE_PRODUCER_UNTRUSTED";
const INVALID = "RELEASE_PRODUCER_PROVENANCE_INVALID";
const commit = "a".repeat(40);
const digests = {
  windows: "b".repeat(64),
  linux: "c".repeat(64),
  appimagetool: "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0",
};

function copy(value) {
  return structuredClone(value);
}

function runFixture() {
  return {
    id: 123456789,
    path: ".github/workflows/release-inputs.yml",
    event: "workflow_dispatch",
    head_branch: "master",
    head_sha: commit,
    status: "completed",
    conclusion: "success",
    repository: { full_name: "colayc/unitTest" },
    unrelated: { GitHub: "API fields are allowed" },
  };
}

function provenanceFixture() {
  return createReleaseInputProvenance({
    sourceManifest: {
      schemaVersion: 1,
      codeOss: {
        repository: "https://github.com/microsoft/vscode.git",
        commit: "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
        version: "1.92.0",
        nodeVersion: "20.14.0",
        yarnVersion: "1.22.22",
        windowsTarget: "vscode-win32-x64",
        windowsOutput: "VSCode-win32-x64",
        linuxTarget: "vscode-linux-x64",
        linuxOutput: "VSCode-linux-x64",
      },
      appimagetool: {
        repository: "AppImage/appimagetool",
        assetId: 324406882,
        assetName: "appimagetool-x86_64.AppImage",
        size: 15092216,
        sha256: digests.appimagetool,
      },
    },
    producer: {
      repository: "colayc/unitTest",
      workflowPath: ".github/workflows/release-inputs.yml",
      sourceCommit: commit,
      event: "workflow_dispatch",
      ref: "refs/heads/master",
    },
    windows: {
      schemaVersion: 1,
      platform: "windows",
      architecture: "x64",
      launcherRelativePath: "Code - OSS.exe",
      launcherSha256: digests.windows,
      fileCount: 1,
      totalBytes: 1,
      treeDigest: "d".repeat(64),
    },
    linux: {
      schemaVersion: 1,
      platform: "linux",
      architecture: "x64",
      launcherRelativePath: "code-oss",
      launcherSha256: digests.linux,
      fileCount: 1,
      totalBytes: 1,
      treeDigest: "e".repeat(64),
    },
    linuxModeInventorySha256: "f".repeat(64),
    appimagetool: {
      repository: "AppImage/appimagetool",
      assetId: 324406882,
      assetName: "appimagetool-x86_64.AppImage",
      size: 15092216,
      sha256: digests.appimagetool,
    },
  });
}

function trustedRun(run = runFixture()) {
  return validateProducerRunMetadata({ run, expectedRunId: "123456789", expectedConsumerCommit: commit });
}

function assertUntrusted(action) {
  assert.throws(action, (error) => error?.code === UNTRUSTED);
}

function assertInvalid(action) {
  assert.throws(action, (error) => error?.code === INVALID);
}

function cli(argumentsList) {
  return spawnSync(process.execPath, [script, ...argumentsList], { encoding: "utf8" });
}

async function temporaryDirectory(t) {
  const parent = join(process.cwd(), ".superpowers", "sdd", "2026-08-28-trusted-code-oss-release-input-production", "task-4-test-fixtures");
  await mkdir(parent, { recursive: true });
  const root = await mkdtemp(join(parent, "trusted-run-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

test("validateProducerRunMetadata projects exactly the trusted successful run", () => {
  assert.deepEqual(trustedRun(), { runId: "123456789" });
  const stringId = runFixture();
  stringId.id = "123456789";
  assert.deepEqual(trustedRun(stringId), { runId: "123456789" });
  assert.equal(Object.isFrozen(trustedRun()), true);
});

test("validateProducerRunMetadata independently rejects each run binding", () => {
  const mutations = [
    (run) => { run.id = 123456788; },
    (run) => { run.id = "00123456789"; },
    (run) => { run.id = Number.MAX_SAFE_INTEGER + 1; },
    (run) => { run.repository.full_name = "other/unitTest"; },
    (run) => { run.path = ".github/workflows/foundation.yml"; },
    (run) => { run.event = "push"; },
    (run) => { run.head_branch = "main"; },
    (run) => { run.head_sha = "b".repeat(40); },
    (run) => { run.status = "queued"; },
    (run) => { run.conclusion = "failure"; },
  ];
  for (const mutate of mutations) {
    const run = runFixture();
    mutate(run);
    assertUntrusted(() => trustedRun(run));
  }
  for (const event of ["pull_request", "push"]) {
    const run = runFixture(); run.event = event;
    assertUntrusted(() => trustedRun(run));
  }
  for (const status of ["queued", "in_progress", "stale"]) {
    const run = runFixture(); run.status = status;
    assertUntrusted(() => trustedRun(run));
  }
  for (const conclusion of ["cancelled", "skipped", "timed_out", "neutral", "action_required", "stale", "failure"]) {
    const run = runFixture(); run.conclusion = conclusion;
    assertUntrusted(() => trustedRun(run));
  }
  assertUntrusted(() => validateProducerRunMetadata({ run: runFixture(), expectedRunId: "0", expectedConsumerCommit: commit }));
  assertUntrusted(() => validateProducerRunMetadata({ run: runFixture(), expectedRunId: "123x", expectedConsumerCommit: commit }));
  for (const value of [0, -1, 1.5, 0n, -1n, 1n, Number.MAX_SAFE_INTEGER + 1, "0", "-1", "1.5", "0001"]) {
    const run = runFixture(); run.id = value;
    assertUntrusted(() => trustedRun(run));
  }
  assertUntrusted(() => validateProducerRunMetadata({ run: runFixture(), expectedRunId: "123456789", expectedConsumerCommit: "A".repeat(40) }));
});

test("validateProducerRunMetadata ignores unrelated API fields without reading them", () => {
  const run = runFixture();
  Object.defineProperty(run, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run, "nonEnumerableExtra", { value: true });
  Object.defineProperty(run, Symbol("extra"), { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run.repository, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run.repository, "nonEnumerableExtra", { value: true });
  Object.defineProperty(run.repository, Symbol("extra"), { value: true });
  assert.deepEqual(trustedRun(run), { runId: "123456789" });
});

test("validateProducerRunMetadata rejects ambiguous required API fields", () => {
  for (const mutate of [
    (run) => Object.defineProperty(run, "status", { enumerable: true, get: () => "completed" }),
    (run) => Object.defineProperty(run.repository, "full_name", { enumerable: true, get: () => "colayc/unitTest" }),
    (run) => { const value = run.event; delete run.event; Object.defineProperty(run, "event", { value }); },
    (run) => { const value = run.repository.full_name; delete run.repository.full_name; Object.defineProperty(run.repository, "full_name", { value }); },
    (run) => { delete run.status; run[Symbol("status")] = "completed"; },
    (run) => { delete run.repository.full_name; run.repository[Symbol("full_name")] = "colayc/unitTest"; },
  ]) {
    const run = runFixture();
    mutate(run);
    assertUntrusted(() => trustedRun(run));
  }
});

test("validateTrustedReleaseInputs binds run, provenance, and canonical manual pins", () => {
  const result = validateTrustedReleaseInputs({
    run: runFixture(), provenance: provenanceFixture(), expectedRunId: "123456789", expectedConsumerCommit: commit,
    expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
    expectedAppimagetoolSha256: digests.appimagetool,
  });
  assert.deepEqual(result, {
    runId: "123456789", windowsLauncherSha256: digests.windows,
    linuxLauncherSha256: digests.linux, appimagetoolSha256: digests.appimagetool,
  });
  assert.equal(Object.isFrozen(result), true);
  for (const field of ["expectedWindowsLauncherSha256", "expectedLinuxLauncherSha256", "expectedAppimagetoolSha256"]) {
    const request = {
      run: runFixture(), provenance: provenanceFixture(), expectedRunId: "123456789", expectedConsumerCommit: commit,
      expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
      expectedAppimagetoolSha256: digests.appimagetool,
    };
    request[field] = request[field].toUpperCase();
    assertInvalid(() => validateTrustedReleaseInputs(request));
  }
  const badProvenance = copy(provenanceFixture());
  badProvenance.runtimes.windows.launcherSha256 = "d".repeat(64);
  assertInvalid(() => validateTrustedReleaseInputs({
    run: runFixture(), provenance: badProvenance, expectedRunId: "123456789", expectedConsumerCommit: commit,
    expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
    expectedAppimagetoolSha256: digests.appimagetool,
  }));
  for (const [path, manual] of [
    [["runtimes", "windows", "launcherSha256"], "expectedWindowsLauncherSha256"],
    [["runtimes", "linux", "launcherSha256"], "expectedLinuxLauncherSha256"],
    [["appimagetool", "sha256"], "expectedAppimagetoolSha256"],
  ]) {
    const provenance = copy(provenanceFixture());
    const parent = path.slice(0, -1).reduce((value, key) => value[key], provenance);
    parent[path.at(-1)] = "d".repeat(64);
    const request = {
      run: runFixture(), provenance, expectedRunId: "123456789", expectedConsumerCommit: commit,
      expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
      expectedAppimagetoolSha256: digests.appimagetool,
    };
    assertInvalid(() => validateTrustedReleaseInputs(request));
    request.provenance = provenanceFixture();
    request[manual] = "d".repeat(64);
    assertInvalid(() => validateTrustedReleaseInputs(request));
  }
  const wrongCommit = copy(provenanceFixture());
  wrongCommit.producer.sourceCommit = "b".repeat(40);
  assertInvalid(() => validateTrustedReleaseInputs({
    run: runFixture(), provenance: wrongCommit, expectedRunId: "123456789", expectedConsumerCommit: commit,
    expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
    expectedAppimagetoolSha256: digests.appimagetool,
  }));
});

test("validateTrustedReleaseInputs applies every API run binding before provenance acceptance", () => {
  const mutations = [
    (run) => { run.id = 1; }, (run) => { run.repository.full_name = "other/unitTest"; },
    (run) => { run.path = ".github/workflows/foundation.yml"; }, (run) => { run.event = "push"; },
    (run) => { run.head_branch = "main"; }, (run) => { run.head_sha = "b".repeat(40); },
    (run) => { run.status = "queued"; }, (run) => { run.conclusion = "failure"; },
  ];
  for (const mutate of mutations) {
    const run = runFixture(); mutate(run);
    assertUntrusted(() => validateTrustedReleaseInputs({
      run, provenance: provenanceFixture(), expectedRunId: "123456789", expectedConsumerCommit: commit,
      expectedWindowsLauncherSha256: digests.windows, expectedLinuxLauncherSha256: digests.linux,
      expectedAppimagetoolSha256: digests.appimagetool,
    }));
  }
});

test("CLI validates run identity before exporting only fixed output keys", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(outputPath, "prior=value\n");
  const accepted = cli(["validate-run", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.equal(accepted.status, 0, accepted.stderr);
  assert.equal(accepted.stdout, "");
  assert.equal(await readFile(outputPath, "utf8"), "prior=value\nrun_id=123456789\n");
  const hostile = cli(["validate-run", "--run-json", runPath, "--run-id", "123456789\ninjected=value", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(hostile.status, 0);
  assert.match(hostile.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal((await readFile(outputPath, "utf8")).includes("injected"), false);
  const duplicate = cli(["validate-run", "--run-json", runPath, "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(duplicate.status, 0);
  assert.match(duplicate.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
});

test("CLI provenance failures cannot inject GitHub output or leak local paths", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const provenancePath = join(root, "provenance.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(provenancePath, `${JSON.stringify(provenanceFixture())}\n`);
  await writeFile(outputPath, "unchanged=value\n");
  const rejected = cli(["validate-provenance", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit,
    "--provenance", provenancePath, "--windows-launcher-sha256", `${digests.windows}\ninjected=value`,
    "--linux-launcher-sha256", digests.linux, "--appimagetool-sha256", digests.appimagetool, "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_PROVENANCE_INVALID: [^\r\n]+\r?\n$/u);
  assert.equal(rejected.stderr.includes(root), false);
  assert.equal(await readFile(outputPath, "utf8"), "unchanged=value\n");
});

test("CLI rejects hard-linked run or GitHub output files before either can modify another path", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const runVictim = join(root, "run-victim.json");
  const outputPath = join(root, "github-output");
  const outputVictim = join(root, "output-victim");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(outputPath, "victim=unchanged\n");
  await link(runPath, runVictim);
  await link(outputPath, outputVictim);
  assert.equal((await lstat(runPath, { bigint: true })).nlink, 2n);
  assert.equal((await lstat(outputPath, { bigint: true })).nlink, 2n);
  const rejectedInput = cli(["validate-run", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejectedInput.status, 0);
  assert.match(rejectedInput.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal(await readFile(outputVictim, "utf8"), "victim=unchanged\n");
  await rm(runVictim);
  const rejectedOutput = cli(["validate-run", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejectedOutput.status, 0);
  assert.match(rejectedOutput.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal(await readFile(outputVictim, "utf8"), "victim=unchanged\n");
});

test("GitHub output append rolls back partial writes and failed syncs on its original descriptor", async (t) => {
  const root = await temporaryDirectory(t);
  const outputPath = join(root, "github-output");
  const original = "third_party=value\n";
  let writes = 0;
  let syncs = 0;
  const cases = [
    {
      async write(handle, bytes, offset, _length, position) {
        writes += 1;
        if (writes === 1) return handle.write(bytes.subarray(offset, offset + 1), 0, 1, position);
        throw new Error("short write then failure");
      },
    },
    {
      async sync(handle) {
        syncs += 1;
        if (syncs === 1) throw new Error("sync failure");
        await handle.sync();
      },
    },
  ];
  for (const hooks of cases) {
    writes = 0;
    syncs = 0;
    await writeFile(outputPath, original);
    await assert.rejects(
      () => __testOnlyTrustedRun.appendGithubOutput(outputPath, [["run_id", "123456789"]], hooks),
      (error) => error?.code === UNTRUSTED,
    );
    assert.equal(await readFile(outputPath, "utf8"), original);
  }
});

test("CLI repeats API validation for provenance and rejects linked inputs or outputs", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const provenancePath = join(root, "provenance.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(provenancePath, `${JSON.stringify(provenanceFixture())}\n`);
  await writeFile(outputPath, "");
  const args = ["validate-provenance", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit,
    "--provenance", provenancePath, "--windows-launcher-sha256", digests.windows,
    "--linux-launcher-sha256", digests.linux, "--appimagetool-sha256", digests.appimagetool, "--github-output", outputPath];
  const accepted = cli(args);
  assert.equal(accepted.status, 0, accepted.stderr);
  assert.equal(await readFile(outputPath, "utf8"), `run_id=123456789\nwindows_launcher_sha256=${digests.windows}\nlinux_launcher_sha256=${digests.linux}\nappimagetool_sha256=${digests.appimagetool}\n`);
  const linkedRun = join(root, "linked-run.json");
  try {
    await symlink(runPath, linkedRun, "file");
  } catch (error) {
    if (error?.code === "EPERM") { t.skip("symbolic links unavailable"); return; }
    throw error;
  }
  const rejected = cli(["validate-run", "--run-json", linkedRun, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  const linkedOutput = join(root, "linked-output");
  await symlink(outputPath, linkedOutput, "file");
  const rejectedOutput = cli(["validate-run", "--run-json", runPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", linkedOutput]);
  assert.notEqual(rejectedOutput.status, 0);
  assert.match(rejectedOutput.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal((await readFile(outputPath, "utf8")).includes("run_id=123456789\nrun_id"), false);
});

test("CLI rejects a producer run reached through a directory junction", async (t) => {
  const root = await temporaryDirectory(t);
  const realDirectory = join(root, "real");
  const linkedDirectory = join(root, "linked");
  const outputPath = join(root, "github-output");
  await mkdir(realDirectory, { recursive: true });
  await writeFile(join(realDirectory, "run.json"), `${JSON.stringify(runFixture())}\n`);
  await writeFile(outputPath, "");
  try {
    await symlink(realDirectory, linkedDirectory, "junction");
  } catch (error) {
    if (error?.code === "EPERM") { t.skip("directory links unavailable"); return; }
    throw error;
  }
  const rejected = cli(["validate-run", "--run-json", join(linkedDirectory, "run.json"), "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
});
