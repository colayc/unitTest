import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, rename, rm, symlink, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { validateSourceManifest } from "./source-manifest.mjs";
import {
  __testOnlyReleaseInputProvenance,
  createReleaseInputProvenance,
  validateReleaseInputProvenance,
} from "./provenance.mjs";

const directory = dirname(fileURLToPath(import.meta.url));
const script = join(directory, "provenance.mjs");
const manifestPath = join(directory, "source-manifest.json");
const INVALID = "RELEASE_PRODUCER_PROVENANCE_INVALID";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function modeTreeDigest(files) {
  const hash = createHash("sha256");
  for (const record of files) {
    hash.update(record.path, "utf8");
    hash.update("\0", "utf8");
    hash.update(String(record.size), "utf8");
    hash.update("\0", "utf8");
    hash.update(record.sha256, "utf8");
    hash.update("\0", "utf8");
    hash.update(record.executable ? "1" : "0", "utf8");
    hash.update("\0", "utf8");
  }
  return hash.digest("hex");
}

async function fixture(t) {
  const parent = await mkdtemp(join(directory, ".provenance-test-"));
  t.after(async () => rm(parent, { recursive: true, force: true }));
  const manifest = validateSourceManifest(JSON.parse(await readFile(manifestPath, "utf8")));
  const windows = {
    schemaVersion: 1,
    platform: "windows",
    architecture: "x64",
    launcherRelativePath: "Code - OSS.exe",
    launcherSha256: "a".repeat(64),
    fileCount: 2,
    totalBytes: 3,
    treeDigest: "b".repeat(64),
  };
  const linux = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256: "c".repeat(64),
    fileCount: 2,
    totalBytes: 3,
    treeDigest: "d".repeat(64),
  };
  const modeInventory = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256: linux.launcherSha256,
    files: [
      { path: "code-oss", size: 1, sha256: linux.launcherSha256, executable: true },
      { path: "resources/app/package.json", size: 2, sha256: "e".repeat(64), executable: false },
    ],
  };
  linux.treeDigest = modeTreeDigest(modeInventory.files);
  const modeBytes = Buffer.from(`${JSON.stringify(modeInventory)}\n`);
  const producer = {
    repository: "colayc/unitTest",
    workflowPath: ".github/workflows/release-inputs.yml",
    sourceCommit: "f".repeat(40),
    event: "workflow_dispatch",
    ref: "refs/heads/master",
  };
  return {
    parent,
    manifest,
    producer,
    windows,
    linux,
    modeInventory,
    modeBytes,
    request: {
      sourceManifest: manifest,
      producer,
      windows,
      linux,
      linuxModeInventorySha256: sha256(modeBytes),
      appimagetool: manifest.appimagetool,
    },
  };
}

function assertInvalid(action) {
  assert.throws(action, (error) => error?.code === INVALID);
}

function nestedObjects(value, output = []) {
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    output.push(value);
    for (const child of Object.values(value)) nestedObjects(child, output);
  }
  return output;
}

function scalarLocations(value, path = [], output = []) {
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    for (const [key, child] of Object.entries(value)) scalarLocations(child, [...path, key], output);
  } else {
    output.push(path);
  }
  return output;
}

function atPath(value, path) {
  return path.slice(0, -1).reduce((current, key) => current[key], value);
}

function corrupt(value) {
  if (typeof value === "number") return 0;
  if (typeof value === "string") return "invalid";
  return null;
}

function replaceWithTogglingGetter(object, key, values) {
  delete object[key];
  let index = 0;
  Object.defineProperty(object, key, {
    enumerable: true,
    get() {
      const value = values[Math.min(index, values.length - 1)];
      index += 1;
      return value;
    },
  });
  return object;
}

test("creation binds fixed source coordinates, summaries, and deterministic canonical records", async (t) => {
  const input = await fixture(t);
  const first = createReleaseInputProvenance(input.request);
  const second = createReleaseInputProvenance(structuredClone(input.request));

  assert.deepEqual(first, second);
  assert.equal(JSON.stringify(first), JSON.stringify(second));
  assert.equal(Object.isFrozen(first), true);
  assert.equal(Object.isFrozen(first.runtimes.linux), true);
  assert.deepEqual(first.codeOss, {
    repository: input.manifest.codeOss.repository,
    commit: input.manifest.codeOss.commit,
    version: input.manifest.codeOss.version,
    nodeVersion: input.manifest.codeOss.nodeVersion,
    yarnVersion: input.manifest.codeOss.yarnVersion,
  });
  assert.equal(first.runtimes.linux.modeInventorySha256, sha256(input.modeBytes));
});

test("validation rejects every provenance scalar mutation and every extra key", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  for (const path of scalarLocations(valid)) {
    const candidate = structuredClone(valid);
    const parent = atPath(candidate, path);
    parent[path.at(-1)] = corrupt(parent[path.at(-1)]);
    assertInvalid(() => validateReleaseInputProvenance(candidate));
  }
  for (let index = 0; index < nestedObjects(valid).length; index += 1) {
    const candidate = structuredClone(valid);
    nestedObjects(candidate)[index].extra = true;
    assertInvalid(() => validateReleaseInputProvenance(candidate));
  }
});

test("creation rejects closed request and independent runtime summary shape violations", async (t) => {
  const input = await fixture(t);
  for (const request of [
    null,
    { ...input.request, extra: true },
    Object.defineProperty({ ...input.request }, "hidden", { value: true }),
    Object.assign({ ...input.request }, { [Symbol("extra")]: true }),
    { ...input.request, appimagetool: { ...input.request.appimagetool, extra: true } },
    { ...input.request, producer: { ...input.request.producer, extra: true } },
  ]) assertInvalid(() => createReleaseInputProvenance(request));

  for (const key of ["windows", "linux"]) {
    for (const object of nestedObjects(input.request[key])) {
      const candidate = structuredClone(input.request);
      const clone = nestedObjects(candidate[key])[nestedObjects(input.request[key]).indexOf(object)];
      clone.extra = true;
      assertInvalid(() => createReleaseInputProvenance(candidate));
    }
  }
  for (const mutate of [
    (value) => { value.windows.platform = "linux"; },
    (value) => { value.linux.platform = "windows"; },
    (value) => { value.windows.launcherRelativePath = "C:/Code - OSS.exe"; },
    (value) => { value.linux.fileCount = 0; },
    (value) => { value.windows.totalBytes = Number.MAX_SAFE_INTEGER + 1; },
    (value) => { value.linux.launcherSha256 = "A".repeat(64); },
    (value) => { value.linux.modeInventorySha256 = "a".repeat(64); },
  ]) {
    const candidate = structuredClone(input.request);
    mutate(candidate);
    assertInvalid(() => createReleaseInputProvenance(candidate));
  }
});

test("validation rejects null, non-enumerable, symbol, drift, and path-bearing provenance values", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  assertInvalid(() => validateReleaseInputProvenance(null));
  for (const candidate of [
    Object.defineProperty(structuredClone(valid), "hidden", { value: true }),
    Object.assign(structuredClone(valid), { [Symbol("extra")]: true }),
    { ...structuredClone(valid), producer: { ...valid.producer, sourceCommit: "F".repeat(40) } },
    { ...structuredClone(valid), producer: { ...valid.producer, workflowPath: "C:/workflow.yml" } },
    { ...structuredClone(valid), appimagetool: { ...valid.appimagetool, size: 0 } },
  ]) assertInvalid(() => validateReleaseInputProvenance(candidate));
});

test("creation and validation reject toggling accessor properties at every public boundary", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const producer = structuredClone(input.request);
  replaceWithTogglingGetter(producer.producer, "sourceCommit", [input.producer.sourceCommit, "attacker-data"]);
  const summary = structuredClone(input.request);
  replaceWithTogglingGetter(summary.windows, "fileCount", [2, 2]);
  const request = structuredClone(input.request);
  replaceWithTogglingGetter(request, "producer", [request.producer, request.producer]);
  const provenance = structuredClone(valid);
  replaceWithTogglingGetter(provenance.runtimes.linux, "modeInventorySha256", [valid.runtimes.linux.modeInventorySha256, "attacker-data"]);
  for (const candidate of [producer, summary, request]) {
    assertInvalid(() => createReleaseInputProvenance(candidate));
  }
  assertInvalid(() => validateReleaseInputProvenance(provenance));
});

test("creation and validation reject non-enumerable required fields", async (t) => {
  const input = await fixture(t);
  const request = structuredClone(input.request);
  const provenance = structuredClone(createReleaseInputProvenance(input.request));
  Object.defineProperty(request.windows, "fileCount", { enumerable: false, value: 2 });
  Object.defineProperty(provenance.producer, "sourceCommit", { enumerable: false, value: input.producer.sourceCommit });
  assertInvalid(() => createReleaseInputProvenance(request));
  assertInvalid(() => validateReleaseInputProvenance(provenance));
});

test("CLI rehashes real mode and tool files, emits canonical bytes, validates manifest and never leaks paths", async (t) => {
  const input = await fixture(t);
  const manifest = join(input.parent, "manifest.json");
  const windows = join(input.parent, "windows.json");
  const linux = join(input.parent, "linux.json");
  const mode = join(input.parent, "mode.json");
  const tool = join(input.parent, "appimagetool");
  const out = join(input.parent, "out", "provenance.json");
  await Promise.all([
    writeFile(manifest, `${JSON.stringify(input.manifest)}\n`),
    writeFile(windows, `${JSON.stringify(input.windows)}\n`),
    writeFile(linux, `${JSON.stringify(input.linux)}\n`),
    writeFile(mode, input.modeBytes),
    writeFile(tool, "not-the-fixed-appimagetool\n"),
  ]);
  const createArgs = [
    script, "create", "--manifest", manifest, "--windows-summary", windows, "--linux-summary", linux,
    "--linux-mode-inventory", mode, "--appimagetool", tool, "--out", out,
    "--producer-repository", input.producer.repository, "--producer-workflow-path", input.producer.workflowPath,
    "--producer-source-commit", input.producer.sourceCommit, "--producer-event", input.producer.event,
    "--producer-ref", input.producer.ref,
  ];
  let result = spawnSync(process.execPath, createArgs, { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, new RegExp(INVALID));
  assert.equal(result.stderr.includes(resolve(input.parent)), false);
  await assert.rejects(() => readFile(out, "utf8"));

  const valid = createReleaseInputProvenance(input.request);
  await mkdir(dirname(out), { recursive: true });
  await writeFile(out, `${JSON.stringify(valid)}\n`);
  result = spawnSync(process.execPath, [script, "validate", "--manifest", manifest, "--provenance", out], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "");
  assert.equal(result.stderr, "");
  const bytes = await readFile(out, "utf8");
  assert.equal(bytes, `${JSON.stringify(valid)}\n`);
  assert.equal(bytes.includes("\r"), false);

  await writeFile(manifest, "{}\n");
  result = spawnSync(process.execPath, [script, "validate", "--manifest", manifest, "--provenance", out], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, new RegExp(INVALID));
  assert.equal(result.stderr.includes(resolve(input.parent)), false);
});

test("CLI rejects a manifest reached through a directory link or junction", async (t) => {
  const input = await fixture(t);
  const outside = join(input.parent, "outside");
  const linked = join(input.parent, "linked");
  const provenance = join(input.parent, "provenance.json");
  await mkdir(outside);
  await writeFile(join(outside, "manifest.json"), `${JSON.stringify(input.manifest)}\n`);
  await writeFile(provenance, `${JSON.stringify(createReleaseInputProvenance(input.request))}\n`);
  try {
    await symlink(outside, linked, process.platform === "win32" ? "junction" : "dir");
  } catch (error) {
    if (error?.code === "EPERM" || error?.code === "EACCES") {
      t.skip("host policy does not permit a directory link fixture");
      return;
    }
    throw error;
  }
  const result = spawnSync(process.execPath, [script, "validate", "--manifest", join(linked, "manifest.json"), "--provenance", provenance], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, new RegExp(INVALID));
  assert.equal(result.stderr.includes(resolve(input.parent)), false);
});

test("opt-in CLI creation binds mode file count, bytes, and exact tree digest", async (t) => {
  const appimagetool = process.env.UNIT_TEST_IDE_REAL_APPIMAGETOOL;
  if (!appimagetool) {
    t.skip("UNIT_TEST_IDE_REAL_APPIMAGETOOL is not set");
    return;
  }
  const input = await fixture(t);
  assert.equal(basename(appimagetool), input.manifest.appimagetool.assetName);
  assert.equal(Number((await lstat(appimagetool, { bigint: true })).size), input.manifest.appimagetool.size);
  assert.equal(sha256(await readFile(appimagetool)), input.manifest.appimagetool.sha256);
  const manifest = join(input.parent, "manifest.json");
  const windows = join(input.parent, "windows.json");
  const linux = join(input.parent, "linux.json");
  const mode = join(input.parent, "mode.json");
  const out = join(input.parent, "out", "provenance.json");
  const baseArguments = [
    script, "create", "--manifest", manifest, "--windows-summary", windows, "--linux-summary", linux,
    "--linux-mode-inventory", mode, "--appimagetool", appimagetool, "--out", out,
    "--producer-repository", input.producer.repository, "--producer-workflow-path", input.producer.workflowPath,
    "--producer-source-commit", input.producer.sourceCommit, "--producer-event", input.producer.event,
    "--producer-ref", input.producer.ref,
  ];
  await Promise.all([
    writeFile(manifest, `${JSON.stringify(input.manifest)}\n`),
    writeFile(windows, `${JSON.stringify(input.windows)}\n`),
    writeFile(linux, `${JSON.stringify(input.linux)}\n`),
  ]);
  const outside = join(input.parent, "outside-output");
  const linkedOutput = join(input.parent, "linked-output");
  await mkdir(outside);
  try {
    await symlink(outside, linkedOutput, process.platform === "win32" ? "junction" : "dir");
    const linkedArguments = [...baseArguments];
    linkedArguments[linkedArguments.indexOf("--out") + 1] = join(linkedOutput, "provenance.json");
    await writeFile(mode, `${JSON.stringify(input.modeInventory)}\n`);
    const linkedResult = spawnSync(process.execPath, linkedArguments, { encoding: "utf8" });
    assert.notEqual(linkedResult.status, 0);
    assert.equal(linkedResult.stderr.includes(resolve(input.parent)), false);
    await assert.rejects(() => readFile(join(outside, "provenance.json"), "utf8"));
  } catch (error) {
    if (error?.code !== "EPERM" && error?.code !== "EACCES") throw error;
  }
  for (const mutate of [
    (value) => { value.files.push({ path: "z-extra", size: 1, sha256: "f".repeat(64), executable: false }); },
    (value) => { value.files[1].size = 3; },
    (value) => { value.files[1].sha256 = "f".repeat(64); },
  ]) {
    const candidate = structuredClone(input.modeInventory);
    mutate(candidate);
    await writeFile(mode, `${JSON.stringify(candidate)}\n`);
    const result = spawnSync(process.execPath, baseArguments, { encoding: "utf8" });
    assert.notEqual(result.status, 0, result.stderr);
    assert.match(result.stderr, new RegExp(INVALID));
    await assert.rejects(() => readFile(out, "utf8"));
  }
  await writeFile(mode, `${JSON.stringify(input.modeInventory)}\n`);
  const result = spawnSync(process.execPath, baseArguments, { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  const bytes = await readFile(out, "utf8");
  const provenance = JSON.parse(bytes);
  assert.equal(bytes, `${JSON.stringify(provenance)}\n`);
  assert.equal(bytes.includes("\r"), false);
  assert.equal(bytes.includes(resolve(input.parent)), false);
  const validate = spawnSync(process.execPath, [script, "validate", "--manifest", manifest, "--provenance", out], { encoding: "utf8" });
  assert.equal(validate.status, 0, validate.stderr);
  assert.equal(validate.stdout, "");
  assert.equal(validate.stderr, "");
});

test("descriptor attestation rejects an ancestor swapped to a directory link after open", async (t) => {
  if (process.platform === "win32") {
    t.skip("Windows prevents renaming an ancestor while its child descriptor is open");
    return;
  }
  const input = await fixture(t);
  const root = join(input.parent, "input");
  const nested = join(root, "nested");
  const moved = join(input.parent, "moved");
  const file = join(nested, "payload.json");
  await mkdir(nested, { recursive: true });
  await writeFile(file, "payload\n");
  let swapped = false;
  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.hashRealFile(file, {
      afterOpenSnapshot: async () => {
        await rename(root, moved);
        await symlink(moved, root, process.platform === "win32" ? "junction" : "dir");
        swapped = true;
      },
    }),
    (error) => error?.code === INVALID,
  );
  assert.equal(swapped, true);
});

test("canonical output refuses linked and swapped output ancestors without partial publication", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const relocated = join(input.parent, "relocated");
  const outside = join(input.parent, "outside");
  const out = join(outputDirectory, "provenance.json");
  await mkdir(outside);
  let swapped = false;
  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {
      beforeStage: async () => {
        await rename(outputDirectory, relocated);
        await symlink(outside, outputDirectory, process.platform === "win32" ? "junction" : "dir");
        swapped = true;
      },
    }),
    (error) => error?.code === INVALID,
  );
  assert.equal(swapped, true);
  await assert.rejects(() => readFile(join(outside, "provenance.json"), "utf8"));
  await assert.rejects(() => readFile(join(relocated, "provenance.json"), "utf8"));
});

test("canonical output never leaves a replaced staged file published after failure", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const out = join(outputDirectory, "provenance.json");
  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {
      beforePublish: async () => {
        const staged = (await readdir(outputDirectory)).find((name) => name.startsWith("provenance.json.tmp-"));
        assert.ok(staged);
        const stagePath = join(outputDirectory, staged);
        await rm(stagePath);
        await writeFile(stagePath, "attacker-bytes\n");
      },
    }),
    (error) => error?.code === INVALID,
  );
  await assert.rejects(() => readFile(out, "utf8"));
});

test("canonical output requires a fresh dedicated output directory and preserves prior files", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const out = join(outputDirectory, "provenance.json");
  await mkdir(outputDirectory);

  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {}),
    (error) => error?.code === INVALID,
  );
  await writeFile(out, "prior-owner-bytes\n");
  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {}),
    (error) => error?.code === INVALID,
  );
  assert.equal(await readFile(out, "utf8"), "prior-owner-bytes\n");
});

test("POSIX canonical output rejects a group-or-world-writable output parent", async (t) => {
  if (process.platform === "win32") {
    t.skip("Windows validates directory ownership and reparse state instead of POSIX mode bits");
    return;
  }
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const out = join(input.parent, "output", "provenance.json");
  await chmod(input.parent, 0o777);

  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {}),
    (error) => error?.code === INVALID,
  );
  await assert.rejects(() => lstat(dirname(out)));
});

test("atomic no-clobber publication preserves a target created at the commit boundary", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const out = join(outputDirectory, "provenance.json");

  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {
      beforeCommit: async () => writeFile(out, "later-owner-bytes\n", { flag: "wx" }),
    }),
    (error) => error?.code === INVALID,
  );
  assert.equal(await readFile(out, "utf8"), "later-owner-bytes\n");
});

test("atomic publication refuses a stage exchanged at the commit boundary", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const out = join(outputDirectory, "provenance.json");

  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {
      beforeCommit: async () => {
        const staged = (await readdir(outputDirectory)).find((name) => name.startsWith("provenance.json.tmp-"));
        assert.ok(staged);
        const stagePath = join(outputDirectory, staged);
        await rm(stagePath);
        await writeFile(stagePath, "attacker-bytes\n", { flag: "wx" });
      },
    }),
    (error) => error?.code === INVALID,
  );
  await assert.rejects(() => readFile(out, "utf8"));
});

test("a post-commit failure never deletes a later replacement target", async (t) => {
  const input = await fixture(t);
  const valid = createReleaseInputProvenance(input.request);
  const outputDirectory = join(input.parent, "output");
  const out = join(outputDirectory, "provenance.json");

  await assert.rejects(
    () => __testOnlyReleaseInputProvenance.writeCanonical(out, valid, {
      afterCommit: async () => {
        await rm(out);
        await writeFile(out, "later-owner-bytes\n", { flag: "wx" });
        throw new Error("deterministic post-commit failure");
      },
    }),
    (error) => error?.code === INVALID,
  );
  assert.equal(await readFile(out, "utf8"), "later-owner-bytes\n");
});
