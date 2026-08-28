import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import {
  createRuntimeModeInventory,
  restoreRuntimeModes,
  validateRuntimeModeInventory,
} from "./runtime-mode-inventory.mjs";

const linuxOnly = process.platform === "linux" ? test : test.skip;

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

async function createRuntimeFixture(t) {
  const parent = await mkdtemp(join(tmpdir(), "code-oss-runtime-mode-"));
  t.after(async () => rm(parent, { recursive: true, force: true }));
  const root = join(parent, "runtime");
  const values = new Map([
    ["chrome_crashpad_handler", Buffer.from("crash handler\n")],
    ["code-oss", Buffer.from("#!/bin/sh\nexit 0\n")],
    ["resources/app/package.json", Buffer.from("{}\n")],
    ["resources/app/product.json", Buffer.from(`${JSON.stringify({
      applicationName: "code-oss",
      licenseName: "MIT",
      nameShort: "Code - OSS",
    })}\n`)],
    ["resources/app/static/data.txt", Buffer.from("data\n")],
  ]);
  const executablePaths = new Set(["chrome_crashpad_handler", "code-oss"]);
  for (const [relativePath, value] of values) {
    await writeFixtureFile(root, relativePath, value, executablePaths.has(relativePath) ? 0o755 : 0o644);
  }
  const launcherSha256 = sha256(values.get("code-oss"));
  const inventory = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256,
    files: [...values].map(([path, value]) => ({
      path,
      size: value.length,
      sha256: sha256(value),
      executable: executablePaths.has(path),
    })).sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0),
  };
  const inventoryPath = join(parent, "code-oss-runtime-mode.json");
  await writeFile(inventoryPath, `${JSON.stringify(inventory, null, 2)}\n`);
  return { root, inventory, inventoryPath, launcherSha256 };
}

function clone(value) {
  return structuredClone(value);
}

test("inventory validation accepts only the closed sorted Linux x64 contract", () => {
  const launcherSha256 = "a".repeat(64);
  const inventory = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256,
    files: [
      { path: "code-oss", size: 4, sha256: launcherSha256, executable: true },
      { path: "resources/app/package.json", size: 3, sha256: "b".repeat(64), executable: false },
    ],
  };

  assert.deepEqual(validateRuntimeModeInventory(inventory, launcherSha256), inventory);
  for (const mutate of [
    (value) => { value.unknown = true; },
    (value) => { delete value.architecture; },
    (value) => { value.platform = "windows"; },
    (value) => { value.launcherRelativePath = "bin/code-oss"; },
    (value) => { value.files[0].unknown = true; },
    (value) => { value.files[0].size = -1; },
    (value) => { value.files[0].sha256 = "A".repeat(64); },
    (value) => { value.files[0].executable = "yes"; },
    (value) => { value.files.reverse(); },
    (value) => { value.files.push({ ...value.files[1] }); },
    (value) => { value.files[1].path = "Code-OSS"; },
    (value) => { value.files[1].path = "../escape"; },
    (value) => { value.files[1].path = "resources//package.json"; },
    (value) => { value.files[1].path = "AUX.txt"; },
    (value) => { value.files[1].path = "resources/app/café.txt"; },
  ]) {
    const candidate = clone(inventory);
    mutate(candidate);
    assert.throws(() => validateRuntimeModeInventory(candidate, launcherSha256), /RELEASE_INPUT_INVALID/u);
  }
  assert.throws(
    () => validateRuntimeModeInventory(inventory, "c".repeat(64)),
    /RELEASE_INPUT_DIGEST_MISMATCH/u,
  );
});

test("inventory validation rejects non-string digest values without coercion", () => {
  const digest = "a".repeat(64);
  const inventory = {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256: digest,
    files: [
      { path: "code-oss", size: 4, sha256: digest, executable: true },
      { path: "resources/app/package.json", size: 3, sha256: "b".repeat(64), executable: false },
    ],
  };
  const assertInvalid = (run) => assert.throws(run, (error) => error?.code === "RELEASE_INPUT_INVALID");

  for (const invalidDigest of [[digest], {}, 123]) {
    assertInvalid(() => validateRuntimeModeInventory(inventory, invalidDigest));

    const invalidLauncher = clone(inventory);
    invalidLauncher.launcherSha256 = invalidDigest;
    assertInvalid(() => validateRuntimeModeInventory(invalidLauncher, digest));

    const invalidFile = clone(inventory);
    invalidFile.files[1].sha256 = invalidDigest;
    assertInvalid(() => validateRuntimeModeInventory(invalidFile, digest));
  }
});

linuxOnly("create records every real runtime file in strict portable order and restore round-trips modes", async (t) => {
  const fixture = await createRuntimeFixture(t);
  const inventory = await createRuntimeModeInventory({
    root: fixture.root,
    expectedLauncherSha256: fixture.launcherSha256,
  });

  assert.deepEqual(validateRuntimeModeInventory(inventory, fixture.launcherSha256), inventory);
  assert.deepEqual(inventory.files.map((record) => record.path), [
    "chrome_crashpad_handler",
    "code-oss",
    "resources/app/package.json",
    "resources/app/product.json",
    "resources/app/static/data.txt",
  ]);
  assert.deepEqual(inventory.files.map((record) => record.executable), [true, true, false, false, false]);

  const inventoryPath = join(dirname(fixture.root), "created-mode-inventory.json");
  await writeFile(inventoryPath, `${JSON.stringify(inventory)}\n`);
  for (const record of inventory.files) await chmod(join(fixture.root, ...record.path.split("/")), 0o600);
  await restoreRuntimeModes({ root: fixture.root, inventoryPath, expectedLauncherSha256: fixture.launcherSha256 });
  for (const record of inventory.files) {
    const info = await lstat(join(fixture.root, ...record.path.split("/")));
    assert.equal(info.mode & 0o777, record.executable ? 0o755 : 0o644, record.path);
  }
});

(process.platform === "linux" ? test.skip : test)("create refuses to infer Linux executable state off Linux", async (t) => {
  const fixture = await createRuntimeFixture(t);
  await assert.rejects(
    () => createRuntimeModeInventory({ root: fixture.root, expectedLauncherSha256: fixture.launcherSha256 }),
    (error) => error?.code === "RELEASE_INPUT_INVALID",
  );
});

test("mode creation request is a closed public shape", async () => {
  for (const request of [null, { root: "runtime", expectedLauncherSha256: "a".repeat(64), extra: true }]) {
    await assert.rejects(() => createRuntimeModeInventory(request), (error) => error?.code === "RELEASE_INPUT_INVALID");
  }
});

for (const [label, operations, expectedMessage] of [
  ["chmod", { chmodFile: async () => { throw new Error("EACCES secret runtime path"); } }, "runtime file mode cannot be changed"],
  ["final lstat", {
    chmodFile: async () => {},
    lstatFile: async () => { throw new Error("ENOENT secret runtime path"); },
  }, "runtime file mode state cannot be inspected"],
]) {
  test(`restore maps ${label} failures to stable path-free errors`, async (t) => {
    const fixture = await createRuntimeFixture(t);
    const secretRoot = fixture.root;

    await assert.rejects(() => restoreRuntimeModes({
      root: fixture.root,
      inventoryPath: fixture.inventoryPath,
      expectedLauncherSha256: fixture.launcherSha256,
    }, { platform: "linux", ...operations }), (error) => {
      assert.equal(error?.code, "RELEASE_INPUT_INVALID");
      assert.equal(error?.message, `RELEASE_INPUT_INVALID: ${expectedMessage}`);
      assert.doesNotMatch(error?.message ?? "", new RegExp(secretRoot.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
      return true;
    });
  });
}

linuxOnly("restore validates all bytes before restoring complete executable modes", async (t) => {
  const fixture = await createRuntimeFixture(t);
  for (const record of fixture.inventory.files) {
    await chmod(join(fixture.root, ...record.path.split("/")), 0o644);
  }

  const result = await restoreRuntimeModes({
    root: fixture.root,
    inventoryPath: fixture.inventoryPath,
    expectedLauncherSha256: fixture.launcherSha256,
  });

  assert.deepEqual(result, {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: "code-oss",
    launcherSha256: fixture.launcherSha256,
    fileCount: fixture.inventory.files.length,
  });
  for (const record of fixture.inventory.files) {
    const info = await lstat(join(fixture.root, ...record.path.split("/")));
    assert.equal(info.mode & 0o777, record.executable ? 0o755 : 0o644, record.path);
  }
});

for (const [label, mutate] of [
  ["a missing runtime file", async ({ root }) => rm(join(root, "resources", "app", "static", "data.txt"))],
  ["an extra runtime file", async ({ root }) => writeFixtureFile(root, "extra.txt", "extra\n")],
  ["runtime digest drift", async ({ root }) => writeFile(join(root, "resources", "app", "static", "data.txt"), "changed\n")],
  ["runtime size drift", async ({ inventory, inventoryPath }) => {
    inventory.files.find((record) => record.path.endsWith("data.txt")).size += 1;
    await writeFile(inventoryPath, `${JSON.stringify(inventory)}\n`);
  }],
  ["a runtime symbolic link", async ({ root }) => {
    const path = join(root, "resources", "app", "static", "data.txt");
    const bytes = await readFile(path);
    await rm(path);
    await symlink(join(root, "resources", "app", "package.json"), path);
    assert.notEqual(bytes.length, 0);
  }],
]) {
  linuxOnly(`restore rejects ${label} without changing executable modes`, async (t) => {
    const fixture = await createRuntimeFixture(t);
    for (const record of fixture.inventory.files) {
      await chmod(join(fixture.root, ...record.path.split("/")), 0o644);
    }
    await mutate(fixture);

    await assert.rejects(() => restoreRuntimeModes({
      root: fixture.root,
      inventoryPath: fixture.inventoryPath,
      expectedLauncherSha256: fixture.launcherSha256,
    }), /RELEASE_INPUT_(?:INVALID|MISSING|DIGEST_MISMATCH)/u);
    const launcher = await lstat(join(fixture.root, "code-oss"));
    assert.equal(launcher.mode & 0o777, 0o644);
  });
}

linuxOnly("restore rejects special runtime entries", async (t) => {
  const fixture = await createRuntimeFixture(t);
  const fifoPath = join(fixture.root, "runtime.fifo");
  const result = await import("node:child_process").then(({ spawnSync }) => spawnSync("mkfifo", [fifoPath]));
  assert.equal(result.status, 0);
  await assert.rejects(() => restoreRuntimeModes({
    root: fixture.root,
    inventoryPath: fixture.inventoryPath,
    expectedLauncherSha256: fixture.launcherSha256,
  }), /RELEASE_INPUT_INVALID/u);
});
