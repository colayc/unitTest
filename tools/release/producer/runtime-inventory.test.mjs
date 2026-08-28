import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import { createRuntimeInventory, summarizeRuntimeInventory } from "./runtime-inventory.mjs";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
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
    [launcher, Buffer.from("launcher\n")],
    ["chrome_crashpad_handler", Buffer.from("handler\n")],
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
  return { root, launcherSha256, modeInventory };
}

test("Windows inventory binds every file to non-executable records and emits a closed summary", async (t) => {
  const input = await fixture(t, "windows");
  const inventory = await createRuntimeInventory({ root: input.root, platform: "windows", expectedLauncherSha256: input.launcherSha256 });
  assert.equal(inventory.platform, "windows");
  assert.ok(inventory.files.every((record) => record.executable === false));
  assert.equal(inventory.files.some((record) => record.path.includes(input.root)), false);
  assert.deepEqual(summarizeRuntimeInventory(inventory), {
    schemaVersion: 1,
    platform: "windows",
    architecture: "x64",
    launcherRelativePath: "Code - OSS.exe",
    launcherSha256: input.launcherSha256,
    fileCount: inventory.files.length,
    totalBytes: inventory.files.reduce((total, record) => total + record.size, 0),
    treeDigest: inventory.treeDigest,
  });
});

test("Linux inventory accepts only an exact validated mode inventory", async (t) => {
  const input = await fixture(t, "linux");
  const inventory = await createRuntimeInventory({ root: input.root, platform: "linux", expectedLauncherSha256: input.launcherSha256, modeInventory: input.modeInventory });
  assert.deepEqual(inventory.files.map((record) => record.executable), [true, true, false, false, false]);
  for (const mutate of [
    (value) => { value.files.pop(); },
    (value) => { value.files.push({ ...value.files[0], path: "extra.txt" }); },
    (value) => { value.files.reverse(); },
    (value) => { value.files[0].path = "Chrome_Crashpad_Handler"; },
    (value) => { value.files[0].size += 1; },
    (value) => { value.files[0].sha256 = "f".repeat(64); },
  ]) {
    const candidate = structuredClone(input.modeInventory);
    mutate(candidate);
    await assert.rejects(
      () => createRuntimeInventory({ root: input.root, platform: "linux", expectedLauncherSha256: input.launcherSha256, modeInventory: candidate }),
      /RELEASE_INPUT_(?:INVALID|MISSING|DIGEST_MISMATCH)/u,
    );
  }
});

test("tree digest changes with record bytes but not timestamps or directory modes", async (t) => {
  const input = await fixture(t, "windows");
  const original = await createRuntimeInventory({ root: input.root, platform: "windows", expectedLauncherSha256: input.launcherSha256 });
  await chmod(join(input.root, "resources"), 0o700);
  const unchanged = await createRuntimeInventory({ root: input.root, platform: "windows", expectedLauncherSha256: input.launcherSha256 });
  assert.equal(unchanged.treeDigest, original.treeDigest);
  await writeFile(join(input.root, "resources", "app", "static", "data.txt"), "changed\n");
  const changed = await createRuntimeInventory({ root: input.root, platform: "windows", expectedLauncherSha256: input.launcherSha256 });
  assert.notEqual(changed.treeDigest, original.treeDigest);
});
