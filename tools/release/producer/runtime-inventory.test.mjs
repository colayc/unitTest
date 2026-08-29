import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  chmod,
  link,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rename,
  rm,
  symlink,
  utimes,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import * as runtimeInventoryModule from "./runtime-inventory.mjs";

const { createRuntimeInventory, summarizeRuntimeInventory } = runtimeInventoryModule;
const linuxOnly = process.platform === "linux" ? test : test.skip;
const windowsOnly = process.platform === "win32" ? test : test.skip;
const script = fileURLToPath(new URL("./runtime-inventory.mjs", import.meta.url));

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function independentTreeDigest(records) {
  const exactRecordBytes = records.map((record) => `${record.path}\0${String(record.size)}\0${record.sha256}\0${record.executable ? "1" : "0"}\0`).join("");
  return sha256(Buffer.from(exactRecordBytes, "utf8"));
}

async function writeFixtureFile(root, relativePath, value, mode = 0o644) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, value, { mode });
  await chmod(path, mode);
  return path;
}

async function fixture(t, platform) {
  const parent = await mkdtemp(join(tmpdir(), "code-oss-runtime-inventory-"));
  t.after(async () => rm(parent, { recursive: true, force: true }));
  const root = join(parent, "runtime");
  const launcher = platform === "windows" ? "Code - OSS.exe" : "code-oss";
  const values = new Map([
    [".runtime/.root-marker", Buffer.from("root hidden\n")],
    [launcher, Buffer.from("launcher\n")],
    ["chrome_crashpad_handler", Buffer.from("handler\n")],
    ["resources/app/.config/.settings.json", Buffer.from('{"hidden":true}\n')],
    ["resources/app/package.json", Buffer.from("{}\n")],
    ["resources/app/product.json", Buffer.from('{"applicationName":"code-oss","licenseName":"MIT","nameShort":"Code - OSS"}\n')],
    ["resources/app/static/data.txt", Buffer.from("data\n")],
  ]);
  for (const [path, value] of values) await writeFixtureFile(root, path, value, path === launcher || path === "chrome_crashpad_handler" ? 0o755 : 0o644);
  const launcherSha256 = sha256(values.get(launcher));
  const modeInventory = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256,
    files: [...values].filter(([path]) => path !== "Code - OSS.exe").map(([path, value]) => ({
      path,
      size: value.length,
      sha256: sha256(value),
      executable: path === "code-oss" || path === "chrome_crashpad_handler",
    })).sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0),
  };
  return { parent, root, launcher, values, launcherSha256, modeInventory };
}

function requestFor(input, platform = "windows") {
  return {
    root: input.root,
    platform,
    expectedLauncherSha256: input.launcherSha256,
    ...(platform === "linux" ? { modeInventory: input.modeInventory } : {}),
  };
}

function cliArguments(input, out, summaryOut) {
  return [
    "create",
    "--platform", "windows",
    "--root", input.root,
    "--launcher-sha256", input.launcherSha256,
    "--out", out,
    "--summary-out", summaryOut,
  ];
}

function runCli(input, out, summaryOut) {
  return spawnSync(process.execPath, [script, ...cliArguments(input, out, summaryOut)], { encoding: "utf8" });
}

function windowsShortPath(path) {
  const result = spawnSync("cmd.exe", ["/d", "/c", "for", "%I", "in", `("${path}")`, "do", "@echo", "%~sI"], { encoding: "utf8", windowsHide: true, windowsVerbatimArguments: true });
  if (result.status !== 0) return undefined;
  const shortPath = result.stdout.trim();
  return shortPath && resolve(shortPath).toLowerCase() !== resolve(path).toLowerCase() ? shortPath : undefined;
}

function assertCanonicalOneLine(bytes, value) {
  assert.equal(bytes, `${JSON.stringify(value)}\n`);
  assert.equal(bytes.endsWith("\n"), true);
  assert.equal(bytes.endsWith("\n\n"), false);
  assert.equal(bytes.includes("\r"), false);
}

function closedInventoryWith(records, overrides = {}) {
  const launcher = records.find((record) => record.path === "Code - OSS.exe");
  return {
    schemaVersion: 1,
    platform: "windows",
    architecture: "x64",
    launcherRelativePath: "Code - OSS.exe",
    launcherSha256: launcher.sha256,
    files: records,
    totalBytes: records.reduce((total, record) => total + record.size, 0),
    treeDigest: independentTreeDigest(records),
    ...overrides,
  };
}

async function createDirectoryLink(t, target, path) {
  try {
    await symlink(target, path, process.platform === "win32" ? "junction" : "dir");
    return true;
  } catch (error) {
    if (error?.code === "EPERM" || error?.code === "EACCES") {
      t.skip("host policy does not permit a directory link fixture");
      return false;
    }
    throw error;
  }
}

test("Windows inventory binds exact path, decimal size, digest, and executable record bytes", async (t) => {
  const input = await fixture(t, "windows");
  const inventory = await createRuntimeInventory(requestFor(input));
  const expectedRecords = [
    { path: ".runtime/.root-marker", size: 12, sha256: "93b0d91eec2f8e253884993480d56820f4646eab0dd0f79c687e9b1e0bd1f6b4", executable: false },
    { path: "Code - OSS.exe", size: 9, sha256: "2a3bb778a7c23f95b8aeaa10989471f42313993322e731a22138bb6f3c81f3c8", executable: false },
    { path: "chrome_crashpad_handler", size: 8, sha256: "7f8043ca52bcf480f1d8705eeeacf2c75b75a48ebafd76d76ae7b47f8c159941", executable: false },
    { path: "resources/app/.config/.settings.json", size: 16, sha256: "52e96442417aa78f3bfae20474d1f68c9f783a93ba29023b612ff2ccac50ba8f", executable: false },
    { path: "resources/app/package.json", size: 3, sha256: "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356", executable: false },
    { path: "resources/app/product.json", size: 76, sha256: "5c6992b939c5cc3a3452c3b354abc85d05e8ecea757de717ef781cb02591f601", executable: false },
    { path: "resources/app/static/data.txt", size: 5, sha256: "6667b2d1aab6a00caa5aee5af8ad9f1465e567abf1c209d15727d57b3e8f6e5f", executable: false },
  ];

  assert.deepEqual(inventory.files, expectedRecords);
  assert.equal(inventory.totalBytes, 129);
  assert.equal(inventory.treeDigest, "31ec6c2a39397a10f77339b88ff22525ef458a4dafc6fe7ad0e8f8c3498644a3");
  assert.equal(inventory.treeDigest, independentTreeDigest(expectedRecords));
  assert.equal(inventory.files.some((record) => record.path.includes(input.root)), false);
  assert.deepEqual(summarizeRuntimeInventory(inventory), {
    schemaVersion: 1,
    platform: "windows",
    architecture: "x64",
    launcherRelativePath: "Code - OSS.exe",
    launcherSha256: input.launcherSha256,
    fileCount: expectedRecords.length,
    totalBytes: 129,
    treeDigest: "31ec6c2a39397a10f77339b88ff22525ef458a4dafc6fe7ad0e8f8c3498644a3",
  });
});

test("inventory validation rejects unsafe file integers and aggregate total overflow", () => {
  const digest = "a".repeat(64);
  const baseRecords = [
    { path: "Code - OSS.exe", size: 1, sha256: digest, executable: false },
    { path: "resources/app/package.json", size: 1, sha256: "b".repeat(64), executable: false },
  ];
  for (const invalidSize of [Number.MAX_SAFE_INTEGER + 1, 1.5, -1]) {
    const records = structuredClone(baseRecords);
    records[1].size = invalidSize;
    assert.throws(() => summarizeRuntimeInventory(closedInventoryWith(records)), (error) => error?.code === "RELEASE_INPUT_INVALID");
  }

  const overflowing = [
    { path: "Code - OSS.exe", size: Number.MAX_SAFE_INTEGER, sha256: digest, executable: false },
    { path: "resources/app/package.json", size: 1, sha256: "b".repeat(64), executable: false },
  ];
  assert.throws(
    () => summarizeRuntimeInventory(closedInventoryWith(overflowing, { totalBytes: Number.MAX_SAFE_INTEGER })),
    (error) => error?.code === "RELEASE_INPUT_INVALID" && /overflows/u.test(error.message),
  );
});

test("tree digest independently binds path, decimal size, SHA, and executable bytes", () => {
  const records = [
    { path: "Code - OSS.exe", size: 9, sha256: "a".repeat(64), executable: false },
    { path: "resources/app/data.txt", size: 5, sha256: "b".repeat(64), executable: false },
  ];
  const original = summarizeRuntimeInventory(closedInventoryWith(records)).treeDigest;
  for (const mutate of [
    (value) => { value[1].path = "resources/app/next.txt"; },
    (value) => { value[1].size = 6; },
    (value) => { value[1].sha256 = "c".repeat(64); },
    (value) => { value[1].executable = true; },
  ]) {
    const candidate = structuredClone(records);
    mutate(candidate);
    const inventory = closedInventoryWith(candidate);
    assert.equal(summarizeRuntimeInventory(inventory).treeDigest, independentTreeDigest(candidate));
    assert.notEqual(inventory.treeDigest, original);
  }
});

test("Linux inventory accepts only complete exact sorted size and digest mode records", async (t) => {
  const input = await fixture(t, "linux");
  const inventory = await createRuntimeInventory(requestFor(input, "linux"));
  assert.deepEqual(inventory.files, [
    { path: ".runtime/.root-marker", size: 12, sha256: "93b0d91eec2f8e253884993480d56820f4646eab0dd0f79c687e9b1e0bd1f6b4", executable: false },
    { path: "chrome_crashpad_handler", size: 8, sha256: "7f8043ca52bcf480f1d8705eeeacf2c75b75a48ebafd76d76ae7b47f8c159941", executable: true },
    { path: "code-oss", size: 9, sha256: "2a3bb778a7c23f95b8aeaa10989471f42313993322e731a22138bb6f3c81f3c8", executable: true },
    { path: "resources/app/.config/.settings.json", size: 16, sha256: "52e96442417aa78f3bfae20474d1f68c9f783a93ba29023b612ff2ccac50ba8f", executable: false },
    { path: "resources/app/package.json", size: 3, sha256: "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356", executable: false },
    { path: "resources/app/product.json", size: 76, sha256: "5c6992b939c5cc3a3452c3b354abc85d05e8ecea757de717ef781cb02591f601", executable: false },
    { path: "resources/app/static/data.txt", size: 5, sha256: "6667b2d1aab6a00caa5aee5af8ad9f1465e567abf1c209d15727d57b3e8f6e5f", executable: false },
  ]);
  assert.equal(inventory.totalBytes, 129);
  assert.equal(inventory.treeDigest, "4269b25b04c8386ec4a7855b43b56fa572089cec6fadcd5c83d397498a0d8e71");

  const cases = [
    ["missing", (value) => { value.files.pop(); }],
    ["extra", (value) => { value.files.push({ path: "z-extra.txt", size: 1, sha256: "e".repeat(64), executable: false }); }],
    ["reordered", (value) => { value.files.reverse(); }],
    ["case alias", (value) => { value.files[0].path = "Chrome_Crashpad_Handler"; }],
    ["unsafe path", (value) => { value.files[0].path = "../handler"; }],
    ["size drift", (value) => { value.files[0].size += 1; }],
    ["digest drift", (value) => { value.files[0].sha256 = "f".repeat(64); }],
    ["launcher mode drift", (value) => { value.files.find((record) => record.path === "code-oss").executable = false; }],
  ];
  for (const [label, mutate] of cases) {
    const candidate = structuredClone(input.modeInventory);
    mutate(candidate);
    await assert.rejects(
      () => createRuntimeInventory({ ...requestFor(input, "linux"), modeInventory: candidate }),
      (error) => error?.code?.startsWith("RELEASE_INPUT_") && Boolean(label),
    );
  }

  const changedMode = structuredClone(input.modeInventory);
  changedMode.files.find((record) => record.path.endsWith("data.txt")).executable = true;
  const withChangedMode = await createRuntimeInventory({ ...requestFor(input, "linux"), modeInventory: changedMode });
  assert.equal(withChangedMode.files.find((record) => record.path.endsWith("data.txt")).executable, true);
  assert.notEqual(withChangedMode.treeDigest, inventory.treeDigest);
});

test("tree digest changes with content but not timestamps or directory modes", async (t) => {
  const input = await fixture(t, "windows");
  const original = await createRuntimeInventory(requestFor(input));
  const dataPath = join(input.root, "resources", "app", "static", "data.txt");
  await utimes(dataPath, new Date("2020-01-01T00:00:00Z"), new Date("2020-01-01T00:00:00Z"));
  await chmod(join(input.root, "resources"), 0o700);
  const unchanged = await createRuntimeInventory(requestFor(input));
  assert.equal(unchanged.treeDigest, original.treeDigest);
  await writeFile(dataPath, "next\n");
  const changed = await createRuntimeInventory(requestFor(input));
  assert.notEqual(changed.treeDigest, original.treeDigest);
});

test("descriptor snapshots reject a deterministic same-object same-size content mutation", async (t) => {
  const testOnly = runtimeInventoryModule.__testOnlyRuntimeInventory;
  assert.ok(testOnly, "test-only descriptor hooks must be available");
  const input = await fixture(t, "windows");
  const target = join(input.root, "resources", "app", "static", "data.txt");
  let mutated = false;
  await assert.rejects(
    () => testOnly.createRuntimeInventory(requestFor(input), {
      afterOpenSnapshot: async ({ path }) => {
        if (!mutated && path === "resources/app/static/data.txt") {
          mutated = true;
          await writeFile(target, "NEXT\n");
        }
      },
    }),
    (error) => error?.code === "RELEASE_INPUT_INVALID",
  );
  assert.equal(mutated, true);
  assert.equal((await lstat(target)).isFile(), true);
});

linuxOnly("descriptor snapshots reject a deterministic same-object mode mutation", async (t) => {
  const input = await fixture(t, "windows");
  const target = join(input.root, "resources", "app", "static", "data.txt");
  let mutated = false;
  await assert.rejects(
    () => runtimeInventoryModule.__testOnlyRuntimeInventory.createRuntimeInventory(requestFor(input), {
      afterOpenSnapshot: async ({ path }) => {
        if (!mutated && path === "resources/app/static/data.txt") {
          mutated = true;
          await chmod(target, 0o600);
        }
      },
    }),
    (error) => error?.code === "RELEASE_INPUT_INVALID",
  );
  assert.equal(mutated, true);
});

test("descriptor-captured product bytes and launcher digest are the inventory identity source", async (t) => {
  const input = await fixture(t, "windows");
  const opens = new Map();
  const inventory = await runtimeInventoryModule.__testOnlyRuntimeInventory.createRuntimeInventory(requestFor(input), {
    afterOpenSnapshot: async ({ path }) => opens.set(path, (opens.get(path) ?? 0) + 1),
  });
  assert.equal(opens.get("resources/app/product.json"), 1);
  assert.equal(opens.get("Code - OSS.exe"), 1);
  assert.equal(inventory.files.find((record) => record.path === "resources/app/product.json").sha256, sha256(input.values.get("resources/app/product.json")));
  assert.equal(inventory.files.find((record) => record.path === "Code - OSS.exe").sha256, input.launcherSha256);
  assert.equal(inventory.launcherSha256, inventory.files.find((record) => record.path === inventory.launcherRelativePath).sha256);
});

test("producer rejects missing required files and directory link or junction entries", async (t) => {
  const missing = await fixture(t, "windows");
  await rm(join(missing.root, "resources", "app", "package.json"));
  await assert.rejects(() => createRuntimeInventory(requestFor(missing)), (error) => error?.code === "RELEASE_INPUT_MISSING");

  const linked = await fixture(t, "windows");
  const outside = join(linked.parent, "outside");
  await mkdir(outside);
  await writeFile(join(outside, "outside.txt"), "outside\n");
  if (!await createDirectoryLink(t, outside, join(linked.root, "linked"))) return;
  await assert.rejects(() => createRuntimeInventory(requestFor(linked)), (error) => error?.code === "RELEASE_INPUT_INVALID");
});

linuxOnly("producer rejects direct symbolic links, unsafe paths, case aliases, and special files", async (t) => {
  for (const kind of ["symbolic link", "unsafe path", "case alias", "special file"]) {
    const input = await fixture(t, "windows");
    if (kind === "symbolic link") {
      await symlink(join(input.root, "resources", "app", "package.json"), join(input.root, "linked-file"));
    } else if (kind === "unsafe path") {
      await writeFile(join(input.root, "unsafe:name"), "unsafe\n");
    } else if (kind === "case alias") {
      await writeFile(join(input.root, "code - oss.EXE"), "alias\n");
    } else {
      const result = spawnSync("mkfifo", [join(input.root, "runtime.fifo")]);
      assert.equal(result.status, 0);
    }
    await assert.rejects(() => createRuntimeInventory(requestFor(input)), (error) => error?.code === "RELEASE_INPUT_INVALID");
  }
});

test("public inventory requests are closed and reject mode inventory on Windows", async (t) => {
  const input = await fixture(t, "windows");
  for (const request of [
    null,
    { root: input.root, platform: "windows", expectedLauncherSha256: input.launcherSha256, extra: true },
    { ...requestFor(input), modeInventory: {} },
  ]) {
    await assert.rejects(() => createRuntimeInventory(request), (error) => error?.code === "RELEASE_INPUT_INVALID");
  }
  assert.throws(
    () => runtimeInventoryModule.__testOnlyRuntimeInventory.createRuntimeInventory(requestFor(input), { unknownHook: async () => {} }),
    (error) => error?.code === "RELEASE_INPUT_INVALID",
  );
});

test("CLI writes both success artifacts as canonical path-free one-line JSON", async (t) => {
  const input = await fixture(t, "windows");
  const out = join(input.parent, "out", "inventory.json");
  const summaryOut = join(input.parent, "out", "summary.json");
  const result = runCli(input, out, summaryOut);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "");
  assert.equal(result.stderr, "");

  const fullBytes = await readFile(out, "utf8");
  const summaryBytes = await readFile(summaryOut, "utf8");
  const full = JSON.parse(fullBytes);
  const summary = JSON.parse(summaryBytes);
  assertCanonicalOneLine(fullBytes, full);
  assertCanonicalOneLine(summaryBytes, summary);
  assert.deepEqual(summary, summarizeRuntimeInventory(full));
  for (const bytes of [fullBytes, summaryBytes]) {
    assert.equal(bytes.includes(resolve(input.root)), false);
    assert.equal(bytes.includes(resolve(input.parent)), false);
  }
});

windowsOnly("Windows CLI accepts a direct 8.3 output parent and rejects a short-long duplicate target", async (t) => {
  const input = await fixture(t, "windows");
  const repositoryRoot = resolve(dirname(script), "..", "..", "..");
  const candidateRoots = await Promise.all([repositoryRoot, tmpdir()].map((path) => realpath(path)));
  const aliasedRoot = candidateRoots.map((longPath) => ({ longPath, shortPath: windowsShortPath(longPath) })).find(({ shortPath }) => shortPath !== undefined);
  if (aliasedRoot === undefined || !await lstat(aliasedRoot.shortPath).then((info) => info.isDirectory(), () => false)) {
    t.skip("filesystem does not provide a distinct usable 8.3 alias");
    return;
  }

  const longParent = await mkdtemp(join(aliasedRoot.longPath, "runtime-inventory-short-path-"));
  t.after(async () => rm(longParent, { recursive: true, force: true }));
  const shortParent = join(aliasedRoot.shortPath, longParent.slice(aliasedRoot.longPath.length + 1));
  assert.equal((await lstat(shortParent)).isDirectory(), true);
  const shortOutputDirectory = join(shortParent, "eight-dot-three-output");
  const longOutputDirectory = join(longParent, "eight-dot-three-output");
  const out = join(shortOutputDirectory, "inventory.json");
  const summaryOut = join(shortOutputDirectory, "summary.json");
  let result = runCli(input, out, summaryOut);
  assert.equal(result.status, 0, result.stderr);
  const fullBytes = await readFile(join(longOutputDirectory, "inventory.json"), "utf8");
  const summaryBytes = await readFile(join(longOutputDirectory, "summary.json"), "utf8");
  const full = JSON.parse(fullBytes);
  const summary = JSON.parse(summaryBytes);
  assertCanonicalOneLine(fullBytes, full);
  assertCanonicalOneLine(summaryBytes, summary);
  assert.deepEqual(summary, summarizeRuntimeInventory(full));

  const longDuplicate = join(longOutputDirectory, "same-target.json");
  const shortDuplicate = join(shortOutputDirectory, "same-target.json");
  result = runCli(input, longDuplicate, shortDuplicate);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /output paths must be distinct/u);
  await assert.rejects(() => readFile(longDuplicate, "utf8"));
});

test("CLI rejects identical and hard-link-equivalent destinations before writing", async (t) => {
  const input = await fixture(t, "windows");
  const identical = join(input.parent, "same.json");
  let result = runCli(input, identical, identical);
  assert.notEqual(result.status, 0);
  await assert.rejects(() => readFile(identical, "utf8"));

  const out = join(input.parent, "full.json");
  const summaryOut = join(input.parent, "summary.json");
  await writeFile(out, "original\n");
  await link(out, summaryOut);
  result = runCli(input, out, summaryOut);
  assert.notEqual(result.status, 0);
  assert.equal(await readFile(out, "utf8"), "original\n");
  assert.equal(await readFile(summaryOut, "utf8"), "original\n");
  assert.equal(result.stderr.includes(input.root), false);
});

test("parallel second-stage failure removes the successful peer stage and creates no output", async (t) => {
  const input = await fixture(t, "windows");
  const outputDirectory = join(input.parent, "outputs");
  const out = join(outputDirectory, "full.json");
  const summaryOut = join(outputDirectory, "summary.json");

  await assert.rejects(
    () => runtimeInventoryModule.__testOnlyRuntimeInventory.createCliOutputs(cliArguments(input, out, summaryOut), {
      beforeStage: async ({ index }) => { if (index === 1) throw new Error("deterministic second stage failure"); },
    }),
    (error) => error?.code === "RELEASE_PRODUCER_OUTPUT_INVALID",
  );
  const entries = await readdir(outputDirectory);
  assert.deepEqual(entries, []);
});

test("second-commit failure removes new outputs and restores both pre-existing targets", async (t) => {
  const input = await fixture(t, "windows");
  const testOnly = runtimeInventoryModule.__testOnlyRuntimeInventory;

  for (const preExisting of [false, true]) {
    const outputDirectory = join(input.parent, preExisting ? "existing" : "new");
    const out = join(outputDirectory, "full.json");
    const summaryOut = join(outputDirectory, "summary.json");
    await mkdir(outputDirectory);
    if (preExisting) {
      await writeFile(out, "previous-full\n");
      await writeFile(summaryOut, "previous-summary\n");
    }
    await assert.rejects(
      () => testOnly.createCliOutputs(cliArguments(input, out, summaryOut), {
        beforeCommit: async ({ index, phase }) => {
          if (index === 1 && phase === "publish") throw new Error("deterministic second commit failure");
        },
      }),
      (error) => error?.code === "RELEASE_PRODUCER_OUTPUT_INVALID",
    );
    if (preExisting) {
      assert.equal(await readFile(out, "utf8"), "previous-full\n");
      assert.equal(await readFile(summaryOut, "utf8"), "previous-summary\n");
    } else {
      await assert.rejects(() => readFile(out, "utf8"));
      await assert.rejects(() => readFile(summaryOut, "utf8"));
    }
    assert.deepEqual((await readdir(outputDirectory)).sort(), preExisting ? ["full.json", "summary.json"] : []);
  }
});

test("post-publish failure restores both pre-existing targets", async (t) => {
  const input = await fixture(t, "windows");
  const outputDirectory = join(input.parent, "post-publish");
  const out = join(outputDirectory, "full.json");
  const summaryOut = join(outputDirectory, "summary.json");
  await mkdir(outputDirectory);
  await writeFile(out, "previous-full\n");
  await writeFile(summaryOut, "previous-summary\n");

  await assert.rejects(
    () => runtimeInventoryModule.__testOnlyRuntimeInventory.createCliOutputs(cliArguments(input, out, summaryOut), {
      afterPublish: async ({ index }) => {
        if (index === 1) throw new Error("deterministic post-publish failure");
      },
    }),
    (error) => error?.code === "RELEASE_PRODUCER_OUTPUT_INVALID",
  );
  assert.equal(await readFile(out, "utf8"), "previous-full\n");
  assert.equal(await readFile(summaryOut, "utf8"), "previous-summary\n");
  assert.deepEqual((await readdir(outputDirectory)).sort(), ["full.json", "summary.json"]);
});

test("staging rejects an output ancestor replaced by a link after inspection", async (t) => {
  const input = await fixture(t, "windows");
  const fullDirectory = join(input.parent, "full-output");
  const summaryDirectory = join(input.parent, "summary-output");
  const relocatedDirectory = join(input.parent, "relocated-output");
  const outside = join(input.parent, "outside-stage");
  const out = join(fullDirectory, "full.json");
  const summaryOut = join(summaryDirectory, "summary.json");
  await mkdir(outside);
  let swapped = false;

  await assert.rejects(
    () => runtimeInventoryModule.__testOnlyRuntimeInventory.createCliOutputs(cliArguments(input, out, summaryOut), {
      beforeStage: async ({ index }) => {
        if (index !== 0 || swapped) return;
        await rename(fullDirectory, relocatedDirectory);
        await symlink(outside, fullDirectory, process.platform === "win32" ? "junction" : "dir");
        swapped = true;
      },
    }),
    (error) => error?.code === "RELEASE_PRODUCER_OUTPUT_INVALID",
  );
  assert.equal(swapped, true);
  await assert.rejects(() => readFile(join(outside, "full.json"), "utf8"));
  await assert.rejects(() => readFile(summaryOut, "utf8"));
  assert.deepEqual(await readdir(relocatedDirectory), []);
});

test("CLI refuses a symlinked output ancestor without touching its referent", async (t) => {
  const input = await fixture(t, "windows");
  const outside = join(input.parent, "outside");
  const outsideTarget = join(outside, "target.json");
  const outLink = join(input.parent, "output-link");
  const summaryOut = join(input.parent, "summary.json");
  await mkdir(outside);
  await writeFile(outsideTarget, "outside-original\n");
  if (!await createDirectoryLink(t, outside, outLink)) return;

  const result = runCli(input, join(outLink, "inventory.json"), summaryOut);
  assert.notEqual(result.status, 0);
  assert.equal(await readFile(outsideTarget, "utf8"), "outside-original\n");
  await assert.rejects(() => readFile(join(outside, "inventory.json"), "utf8"));
  await assert.rejects(() => readFile(summaryOut, "utf8"));
});
