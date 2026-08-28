import assert from "node:assert/strict";
import { constants } from "node:fs";
import { link, lstat, mkdir, mkdtemp, open, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { createReleaseInputProvenance } from "./provenance.mjs";
import {
  validateProducerRunMetadata,
  selectProvenanceArtifact,
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
    run_attempt: 2,
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

function artifactsFixture() {
  return {
    total_count: 4,
    artifacts: [
      { id: 1001, name: "code-oss-windows-x64-2", expired: false, digest: `sha256:${"1".repeat(64)}`, workflow_run: { id: 123456789 } },
      { id: 1002, name: "code-oss-linux-x64-2", expired: false, digest: `sha256:${"2".repeat(64)}`, workflow_run: { id: 123456789 } },
      { id: 1003, name: "appimagetool-linux-x64-2", expired: false, digest: `sha256:${"3".repeat(64)}`, workflow_run: { id: 123456789 } },
      { id: 1004, name: "release-input-provenance-2", expired: false, digest: `sha256:${"4".repeat(64)}`, workflow_run: { id: 123456789 } },
    ],
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
      runId: "123456789",
      runAttempt: 2,
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
      artifactId: "1001",
      artifactDigest: "1".repeat(64),
      transportName: "code-oss-windows-x64-2",
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
      artifactId: "1002",
      artifactDigest: "2".repeat(64),
      transportName: "code-oss-linux-x64-2",
    },
    linuxModeInventorySha256: "f".repeat(64),
    appimagetool: {
      repository: "AppImage/appimagetool",
      assetId: 324406882,
      assetName: "appimagetool-x86_64.AppImage",
      size: 15092216,
      sha256: digests.appimagetool,
      artifactId: "1003",
      artifactDigest: "3".repeat(64),
      transportName: "appimagetool-linux-x64-2",
    },
  });
}

function trustedRun(run = runFixture(), expectedRunAttempt = 2) {
  return validateProducerRunMetadata({ run, expectedRunId: "123456789", expectedConsumerCommit: commit, expectedRunAttempt });
}

function trustedInputs(overrides = {}) {
  return {
    run: runFixture(), artifacts: artifactsFixture(), provenance: provenanceFixture(),
    expectedRunId: "123456789", expectedRunAttempt: 2, expectedConsumerCommit: commit,
    provenanceArtifactId: "1004", provenanceArtifactDigest: "4".repeat(64),
    ...overrides,
  };
}

function bootstrapOutputs() {
  return [["run_id", "123456789"], ["run_attempt", "2"], ["provenance_artifact_id", "1004"], ["provenance_artifact_digest", "4".repeat(64)]];
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
  const parent = join(process.cwd(), ".superpowers", "test-fixtures");
  await mkdir(parent, { recursive: true });
  const root = await mkdtemp(join(parent, "trusted-run-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

test("validateProducerRunMetadata projects exactly the trusted successful run", () => {
  assert.deepEqual(trustedRun(), { runId: "123456789", runAttempt: 2 });
  assert.equal(Object.isFrozen(trustedRun()), true);
});

test("validateProducerRunMetadata independently rejects each run binding", () => {
  const mutations = [
    (run) => { run.id = 123456788; },
    (run) => { run.id = "00123456789"; },
    (run) => { run.id = Number.MAX_SAFE_INTEGER + 1; },
    (run) => { delete run.run_attempt; },
    (run) => { run.run_attempt = 0; },
    (run) => { run.run_attempt = -1; },
    (run) => { run.run_attempt = 1.5; },
    (run) => { run.run_attempt = Number.MAX_SAFE_INTEGER + 1; },
    (run) => { run.run_attempt = "2"; },
    (run) => { run.run_attempt = 3; },
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
  for (const expectedRunId of [0n, -1n, "-1", "0001"]) {
    assertUntrusted(() => validateProducerRunMetadata({ run: runFixture(), expectedRunId, expectedConsumerCommit: commit }));
  }
  for (const value of [0, -1, 1.5, 0n, -1n, 1n, Number.MAX_SAFE_INTEGER + 1, "0", "-1", "1.5", "0001"]) {
    const run = runFixture(); run.id = value;
    assertUntrusted(() => trustedRun(run));
  }
  assertUntrusted(() => validateProducerRunMetadata({ run: runFixture(), expectedRunId: "123456789", expectedConsumerCommit: "A".repeat(40) }));
});

test("selectProvenanceArtifact closes the selected attempt artifact list", () => {
  assert.deepEqual(selectProvenanceArtifact({ artifacts: artifactsFixture(), runId: "123456789", runAttempt: 2 }), {
    provenanceArtifactId: "1004",
    provenanceArtifactDigest: "4".repeat(64),
    provenanceTransportName: "release-input-provenance-2",
  });
  const mutations = [
    (api) => { api.total_count = 3; },
    (api) => { api.total_count = 101; },
    (api) => { api.artifacts = {}; },
    (api) => { api.artifacts[3].expired = true; },
    (api) => { api.artifacts[3].id = 0; },
    (api) => { api.artifacts[3].id = Number.MAX_SAFE_INTEGER + 1; },
    (api) => { api.artifacts[3].id = "01004"; },
    (api) => { api.artifacts[3].digest = `sha256:${"A".repeat(64)}`; },
    (api) => { api.artifacts[3].digest = "4".repeat(64); },
    (api) => { api.artifacts[3].workflow_run.id = 1; },
    (api) => { api.artifacts[3].name = "release-input-provenance-3"; },
    (api) => { api.artifacts.push(copy(api.artifacts[3])); api.total_count = 5; },
    (api) => Object.defineProperty(api.artifacts[3], "digest", { enumerable: true, get: () => `sha256:${"4".repeat(64)}` }),
  ];
  for (const mutate of mutations) {
    const api = artifactsFixture();
    mutate(api);
    assertUntrusted(() => selectProvenanceArtifact({ artifacts: api, runId: "123456789", runAttempt: 2 }));
  }
});

test("selectProvenanceArtifact ignores unrelated GitHub fields without reading them", () => {
  const api = artifactsFixture();
  Object.defineProperty(api, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(api.artifacts[3], "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(api.artifacts[3].workflow_run, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  assert.deepEqual(selectProvenanceArtifact({ artifacts: api, runId: "123456789", runAttempt: 2 }), {
    provenanceArtifactId: "1004", provenanceArtifactDigest: "4".repeat(64), provenanceTransportName: "release-input-provenance-2",
  });
});

test("validateProducerRunMetadata ignores unrelated API fields without reading them", () => {
  const run = runFixture();
  Object.defineProperty(run, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run, "nonEnumerableExtra", { value: true });
  Object.defineProperty(run, Symbol("extra"), { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run.repository, "unrelated", { enumerable: true, get: () => { throw new Error("must not be read"); } });
  Object.defineProperty(run.repository, "nonEnumerableExtra", { value: true });
  Object.defineProperty(run.repository, Symbol("extra"), { value: true });
  assert.deepEqual(trustedRun(run), { runId: "123456789", runAttempt: 2 });
});

test("validateProducerRunMetadata rejects ambiguous required API fields", () => {
  for (const mutate of [
    (run) => Object.defineProperty(run, "status", { enumerable: true, get: () => "completed" }),
    (run) => Object.defineProperty(run, "run_attempt", { enumerable: true, get: () => 2 }),
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

test("validateTrustedReleaseInputs binds the second API snapshot to immutable provenance identities", () => {
  const result = validateTrustedReleaseInputs(trustedInputs());
  assert.deepEqual(result, {
    runId: "123456789", runAttempt: 2, windowsLauncherSha256: digests.windows,
    linuxLauncherSha256: digests.linux, appimagetoolSha256: digests.appimagetool,
    windowsArtifactId: "1001", windowsArtifactDigest: "1".repeat(64),
    linuxArtifactId: "1002", linuxArtifactDigest: "2".repeat(64),
    appimagetoolArtifactId: "1003", appimagetoolArtifactDigest: "3".repeat(64),
  });
  assert.equal(Object.isFrozen(result), true);
  const changedBootstrap = trustedInputs();
  changedBootstrap.artifacts.artifacts[3].id = 1999;
  assertUntrusted(() => validateTrustedReleaseInputs(changedBootstrap));
  for (const mutate of [
    (provenance) => { provenance.producer.runId = "1"; },
    (provenance) => { provenance.producer.runAttempt = 3; },
    (provenance) => { provenance.runtimes.windows.artifactId = "9999"; },
    (provenance) => { provenance.runtimes.linux.artifactDigest = "d".repeat(64); },
    (provenance) => { provenance.appimagetool.artifactName = "other"; },
    (provenance) => { provenance.appimagetool.transportName = "appimagetool-linux-x64-3"; },
  ]) {
    const request = trustedInputs({ provenance: copy(provenanceFixture()) });
    mutate(request.provenance);
    assertInvalid(() => validateTrustedReleaseInputs(request));
  }
  const changedRuntimeApi = trustedInputs();
  changedRuntimeApi.artifacts.artifacts[0].digest = `sha256:${"d".repeat(64)}`;
  assertInvalid(() => validateTrustedReleaseInputs(changedRuntimeApi));
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
    assertUntrusted(() => validateTrustedReleaseInputs(trustedInputs({ run })));
  }
});

test("CLI validates run identity before exporting only fixed output keys", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const artifactsPath = join(root, "artifacts.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  await writeFile(outputPath, "prior=value\n");
  const accepted = cli(["validate-run", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.equal(accepted.status, 0, accepted.stderr);
  assert.equal(accepted.stdout, "");
  assert.equal(await readFile(outputPath, "utf8"), `prior=value\nrun_id=123456789\nrun_attempt=2\nprovenance_artifact_id=1004\nprovenance_artifact_digest=${"4".repeat(64)}\n`);
  for (const [key, value] of [["--run-id", "123456789\ninjected=value"], ["--artifacts-json", "1%2"], ["--consumer-commit", "1e2"], ["--github-output", "01"]]) {
    const hostile = cli(["validate-run", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath, key, value]);
    assert.notEqual(hostile.status, 0);
    assert.match(hostile.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
    assert.equal((await readFile(outputPath, "utf8")).includes("injected"), false);
  }
  const duplicate = cli(["validate-run", "--run-json", runPath, "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(duplicate.status, 0);
  assert.match(duplicate.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
});

test("CLI provenance failures cannot inject GitHub output or leak local paths", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const artifactsPath = join(root, "artifacts.json");
  const provenancePath = join(root, "provenance.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  await writeFile(provenancePath, `${JSON.stringify(provenanceFixture())}\n`);
  await writeFile(outputPath, "unchanged=value\n");
  const rejected = cli(["validate-provenance", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--run-attempt", "2", "--consumer-commit", commit,
    "--provenance", provenancePath, "--provenance-artifact-id", `1004\ninjected=value`, "--provenance-artifact-digest", "4".repeat(64), "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_PROVENANCE_INVALID: [^\r\n]+\r?\n$/u);
  assert.equal(rejected.stderr.includes(root), false);
  assert.equal(await readFile(outputPath, "utf8"), "unchanged=value\n");
});

test("CLI validate-attempt accepts only the exact current attempt and writes no output", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  const args = ["validate-attempt", "--run-json", runPath, "--run-id", "123456789", "--run-attempt", "2", "--consumer-commit", commit];
  assert.equal(cli(args).status, 0);
  for (const [key, value] of [["run_attempt", 3], ["status", "queued"], ["conclusion", "failure"]]) {
    const run = runFixture(); run[key] = value;
    await writeFile(runPath, `${JSON.stringify(run)}\n`);
    const rejected = cli(args);
    assert.notEqual(rejected.status, 0);
    assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  }
});

test("CLI rejects hard-linked run or GitHub output files before either can modify another path", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const artifactsPath = join(root, "artifacts.json");
  const runVictim = join(root, "run-victim.json");
  const outputPath = join(root, "github-output");
  const outputVictim = join(root, "output-victim");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  await writeFile(outputPath, "victim=unchanged\n");
  await link(runPath, runVictim);
  await link(outputPath, outputVictim);
  assert.equal((await lstat(runPath, { bigint: true })).nlink, 2n);
  assert.equal((await lstat(outputPath, { bigint: true })).nlink, 2n);
  const rejectedInput = cli(["validate-run", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejectedInput.status, 0);
  assert.match(rejectedInput.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal(await readFile(outputVictim, "utf8"), "victim=unchanged\n");
  await rm(runVictim);
  const rejectedOutput = cli(["validate-run", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejectedOutput.status, 0);
  assert.match(rejectedOutput.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal(await readFile(outputVictim, "utf8"), "victim=unchanged\n");
});

test("trusted JSON reads reject same-inode content changes after opening or reading", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const original = `${JSON.stringify(runFixture())}\n`;
  const replacement = original.replace("API fields", "XXX fields");
  assert.equal(replacement.length, original.length);
  for (const hookName of ["afterOpenSnapshot", "afterRead"]) {
    await writeFile(runPath, original);
    await assert.rejects(
      () => __testOnlyTrustedRun.readTrustedJson(runPath, UNTRUSTED, {
        async [hookName]() { await writeFile(runPath, replacement); },
      }),
      (error) => error?.code === UNTRUSTED,
    );
  }
  await writeFile(runPath, original);
  await assert.rejects(
    () => __testOnlyTrustedRun.readTrustedJson(runPath, UNTRUSTED, {
      async afterOpenSnapshot() { await writeFile(runPath, `${replacement} `); },
    }),
    (error) => error?.code === UNTRUSTED,
  );
});

test("test hooks snapshot functions before async boundaries instead of rereading replacement getters", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  let readGetter = false;
  const readHook = {
    async afterOpenSnapshot() {
      Object.defineProperty(readHook, "afterRead", {
        enumerable: true,
        get() { readGetter = true; return async () => {}; },
      });
    },
  };
  await __testOnlyTrustedRun.readTrustedJson(runPath, UNTRUSTED, readHook);
  assert.equal(readGetter, false);

  const outputPath = join(root, "github-output");
  await writeFile(outputPath, "third_party=value\n");
  let outputGetter = false;
  const outputHook = {
    async afterWrite() {
      Object.defineProperty(outputHook, "sync", {
        enumerable: true,
        get() { outputGetter = true; return async (handle) => handle.sync(); },
      });
    },
  };
  await __testOnlyTrustedRun.appendGithubOutput(outputPath, bootstrapOutputs(), outputHook);
  assert.equal(outputGetter, false);
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
      () => __testOnlyTrustedRun.appendGithubOutput(outputPath, bootstrapOutputs(), hooks),
      (error) => error?.code === UNTRUSTED,
    );
    assert.equal(await readFile(outputPath, "utf8"), original);
  }
});

test("GitHub output append rejects same-inode prefix replacement and restores its original bytes", async (t) => {
  const root = await temporaryDirectory(t);
  const outputPath = join(root, "github-output");
  const original = "third_party=value\n";
  await writeFile(outputPath, original);
  await assert.rejects(
    () => __testOnlyTrustedRun.appendGithubOutput(outputPath, bootstrapOutputs(), {
      async afterWrite() {
        const handle = await open(outputPath, constants.O_RDWR);
        try { await handle.write(Buffer.from("X"), 0, 1, 0); } finally { await handle.close(); }
      },
    }),
    (error) => error?.code === UNTRUSTED,
  );
  assert.equal(await readFile(outputPath, "utf8"), original);
});

test("GitHub output append rejects same-length suffix replacement and restores its expected bytes", async (t) => {
  const root = await temporaryDirectory(t);
  const outputPath = join(root, "github-output");
  const original = "third_party=value\n";
  await writeFile(outputPath, original);
  await assert.rejects(
    () => __testOnlyTrustedRun.appendGithubOutput(outputPath, bootstrapOutputs(), {
      async afterWrite() {
        const handle = await open(outputPath, constants.O_RDWR);
        try { await handle.write(Buffer.from("987654321"), 0, 9, original.length + "run_id=".length); } finally { await handle.close(); }
      },
    }),
    (error) => error?.code === UNTRUSTED,
  );
  assert.equal(await readFile(outputPath, "utf8"), original);
});

test("CLI rejects a linked producer-run input", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const artifactsPath = join(root, "artifacts.json");
  const provenancePath = join(root, "provenance.json");
  const outputPath = join(root, "github-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  await writeFile(provenancePath, `${JSON.stringify(provenanceFixture())}\n`);
  await writeFile(outputPath, "");
  const args = ["validate-provenance", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--run-attempt", "2", "--consumer-commit", commit,
    "--provenance", provenancePath, "--provenance-artifact-id", "1004", "--provenance-artifact-digest", "4".repeat(64), "--github-output", outputPath];
  const accepted = cli(args);
  assert.equal(accepted.status, 0, accepted.stderr);
  assert.equal(await readFile(outputPath, "utf8"), `run_id=123456789\nrun_attempt=2\nwindows_launcher_sha256=${digests.windows}\nlinux_launcher_sha256=${digests.linux}\nappimagetool_sha256=${digests.appimagetool}\nwindows_artifact_id=1001\nwindows_artifact_digest=${"1".repeat(64)}\nlinux_artifact_id=1002\nlinux_artifact_digest=${"2".repeat(64)}\nappimagetool_artifact_id=1003\nappimagetool_artifact_digest=${"3".repeat(64)}\n`);
  const linkedRun = join(root, "linked-run.json");
  try {
    await symlink(runPath, linkedRun, "file");
  } catch (error) {
    if (error?.code === "EPERM") { t.skip("symbolic links unavailable"); return; }
    throw error;
  }
  const rejected = cli(["validate-run", "--run-json", linkedRun, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
});

test("CLI rejects a linked GitHub output", async (t) => {
  const root = await temporaryDirectory(t);
  const runPath = join(root, "run.json");
  const outputPath = join(root, "github-output");
  const linkedOutput = join(root, "linked-output");
  await writeFile(runPath, `${JSON.stringify(runFixture())}\n`);
  await writeFile(outputPath, "unchanged=value\n");
  try {
    await symlink(outputPath, linkedOutput, "file");
  } catch (error) {
    if (error?.code === "EPERM") { t.skip("symbolic links unavailable"); return; }
    throw error;
  }
  const artifactsPath = join(root, "artifacts.json");
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  const rejected = cli(["validate-run", "--run-json", runPath, "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", linkedOutput]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
  assert.equal(await readFile(outputPath, "utf8"), "unchanged=value\n");
});

test("CLI rejects a producer run reached through a directory junction", async (t) => {
  const root = await temporaryDirectory(t);
  const realDirectory = join(root, "real");
  const linkedDirectory = join(root, "linked");
  const outputPath = join(root, "github-output");
  const artifactsPath = join(root, "artifacts.json");
  await mkdir(realDirectory, { recursive: true });
  await writeFile(join(realDirectory, "run.json"), `${JSON.stringify(runFixture())}\n`);
  await writeFile(artifactsPath, `${JSON.stringify(artifactsFixture())}\n`);
  await writeFile(outputPath, "");
  try {
    await symlink(realDirectory, linkedDirectory, "junction");
  } catch (error) {
    if (error?.code === "EPERM") { t.skip("directory links unavailable"); return; }
    throw error;
  }
  const rejected = cli(["validate-run", "--run-json", join(linkedDirectory, "run.json"), "--artifacts-json", artifactsPath, "--run-id", "123456789", "--consumer-commit", commit, "--github-output", outputPath]);
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
});
