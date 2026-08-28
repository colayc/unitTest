import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import * as modeInventoryModule from "./runtime-mode-inventory.mjs";

const { createRuntimeModeInventory, restoreRuntimeModes, validateRuntimeModeInventory } = modeInventoryModule;

const linuxOnly = process.platform === "linux" ? test : test.skip;
const script = fileURLToPath(new URL("./runtime-mode-inventory.mjs", import.meta.url));

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
    [".runtime/.root-marker", Buffer.from("root hidden\n")],
    ["chrome_crashpad_handler", Buffer.from("crash handler\n")],
    ["code-oss", Buffer.from("#!/bin/sh\nexit 0\n")],
    ["resources/app/.config/.settings.json", Buffer.from('{"hidden":true}\n')],
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
  assert.deepEqual(inventory.files, [
    { path: ".runtime/.root-marker", size: 12, sha256: "93b0d91eec2f8e253884993480d56820f4646eab0dd0f79c687e9b1e0bd1f6b4", executable: false },
    { path: "chrome_crashpad_handler", size: 14, sha256: "1f26cbb000d6104cf770e6af724ab836fff921d975e536e186bf3aea749e6d25", executable: true },
    { path: "code-oss", size: 17, sha256: "306c6ca7407560340797866e077e053627ad409277d1b9da58106fce4cf717cb", executable: true },
    { path: "resources/app/.config/.settings.json", size: 16, sha256: "52e96442417aa78f3bfae20474d1f68c9f783a93ba29023b612ff2ccac50ba8f", executable: false },
    { path: "resources/app/package.json", size: 3, sha256: "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356", executable: false },
    { path: "resources/app/product.json", size: 76, sha256: "5c6992b939c5cc3a3452c3b354abc85d05e8ecea757de717ef781cb02591f601", executable: false },
    { path: "resources/app/static/data.txt", size: 5, sha256: "6667b2d1aab6a00caa5aee5af8ad9f1465e567abf1c209d15727d57b3e8f6e5f", executable: false },
  ]);
  assert.equal(inventory.files.length, 7);

  const inventoryPath = join(dirname(fixture.root), "created-mode-inventory.json");
  await writeFile(inventoryPath, `${JSON.stringify(inventory)}\n`);
  for (const record of inventory.files) await chmod(join(fixture.root, ...record.path.split("/")), 0o600);
  await restoreRuntimeModes({ root: fixture.root, inventoryPath, expectedLauncherSha256: fixture.launcherSha256 });
  for (const record of inventory.files) {
    const info = await lstat(join(fixture.root, ...record.path.split("/")));
    assert.equal(info.mode & 0o777, record.executable ? 0o755 : 0o644, record.path);
  }
});

linuxOnly("create rejects deterministic same-object same-size content and mode mutations", async (t) => {
  const testOnly = modeInventoryModule.__testOnlyRuntimeModeInventory;
  assert.ok(testOnly, "test-only descriptor hooks must be available");
  for (const [label, mutate] of [
    ["content", async (path) => writeFile(path, "DATA\n")],
    ["mode", async (path) => chmod(path, 0o600)],
  ]) {
    const fixture = await createRuntimeFixture(t);
    const target = join(fixture.root, "resources", "app", "static", "data.txt");
    let mutated = false;
    await assert.rejects(
      () => testOnly.createRuntimeModeInventory({
        root: fixture.root,
        expectedLauncherSha256: fixture.launcherSha256,
      }, {
        afterOpenSnapshot: async ({ path }) => {
          if (!mutated && path === "resources/app/static/data.txt") {
            mutated = true;
            await mutate(target);
          }
        },
      }),
      (error) => error?.code === "RELEASE_INPUT_INVALID" && Boolean(label),
    );
    assert.equal(mutated, true);
  }
});

linuxOnly("mode creation rejects unsafe paths, case aliases, links, special files, and a non-executable launcher", async (t) => {
  for (const kind of ["unsafe path", "case alias", "link", "special", "non-executable launcher"]) {
    const fixture = await createRuntimeFixture(t);
    if (kind === "unsafe path") {
      await writeFile(join(fixture.root, "unsafe:name"), "unsafe\n");
    } else if (kind === "case alias") {
      await writeFile(join(fixture.root, "CODE-OSS"), "alias\n");
    } else if (kind === "link") {
      await symlink(join(fixture.root, "resources", "app", "package.json"), join(fixture.root, "linked-file"));
    } else if (kind === "special") {
      const result = spawnSync("mkfifo", [join(fixture.root, "runtime.fifo")]);
      assert.equal(result.status, 0);
    } else {
      await chmod(join(fixture.root, "code-oss"), 0o644);
    }
    await assert.rejects(
      () => createRuntimeModeInventory({ root: fixture.root, expectedLauncherSha256: fixture.launcherSha256 }),
      (error) => error?.code === "RELEASE_INPUT_INVALID",
    );
  }
});

linuxOnly("mode create CLI writes canonical output and rejects malformed, stale-temp, and symlink targets", async (t) => {
  const fixture = await createRuntimeFixture(t);
  const parent = dirname(fixture.root);
  const out = join(parent, "mode.json");
  const args = [script, "create", "--root", fixture.root, "--launcher-sha256", fixture.launcherSha256, "--out", out];
  let result = spawnSync(process.execPath, args, { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  const outputBytes = await readFile(out, "utf8");
  assert.equal(outputBytes, result.stdout);
  assert.equal(outputBytes, `${JSON.stringify(JSON.parse(outputBytes))}\n`);
  assert.equal(outputBytes.includes(fixture.root), false);

  result = spawnSync(process.execPath, args.slice(0, -2), { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /RELEASE_INPUT_INVALID/u);

  const blockedParent = join(parent, "not-a-directory");
  await writeFile(blockedParent, "blocked\n");
  result = spawnSync(process.execPath, [...args.slice(0, -1), join(blockedParent, "mode.json")], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /RELEASE_PRODUCER_OUTPUT_INVALID/u);

  const staleTemporary = `${out}.tmp-${process.pid}`;
  await writeFile(staleTemporary, "attacker-controlled\n");
  result = spawnSync(process.execPath, args, { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(await readFile(out, "utf8"), outputBytes);
  assert.equal(await readFile(staleTemporary, "utf8"), "attacker-controlled\n");

  const outside = join(parent, "outside-mode.json");
  const outputLink = join(parent, "mode-link.json");
  await writeFile(outside, "outside\n");
  await symlink(outside, outputLink);
  result = spawnSync(process.execPath, [...args.slice(0, -1), outputLink], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.equal(await readFile(outside, "utf8"), "outside\n");
});

linuxOnly("mode create CLI rejects a genuinely unwritable output directory", async (t) => {
  if (typeof process.getuid === "function" && process.getuid() === 0) {
    t.skip("root can bypass directory write permissions");
    return;
  }
  const fixture = await createRuntimeFixture(t);
  const outputDirectory = join(dirname(fixture.root), "unwritable");
  const out = join(outputDirectory, "mode.json");
  await mkdir(outputDirectory);
  await chmod(outputDirectory, 0o555);
  t.after(async () => chmod(outputDirectory, 0o755).catch(() => {}));
  const result = spawnSync(process.execPath, [
    script,
    "create",
    "--root", fixture.root,
    "--launcher-sha256", fixture.launcherSha256,
    "--out", out,
  ], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /RELEASE_PRODUCER_OUTPUT_INVALID/u);
  await assert.rejects(() => readFile(out, "utf8"));
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
  assert.throws(
    () => modeInventoryModule.__testOnlyRuntimeModeInventory.createRuntimeModeInventory({
      root: "runtime",
      expectedLauncherSha256: "a".repeat(64),
    }, { unknownHook: async () => {} }),
    (error) => error?.code === "RELEASE_INPUT_INVALID",
  );
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
