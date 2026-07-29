import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";

import {
  __testing,
  platformKey,
  prepareBundle,
  sha256File,
  validateManifest,
  verifyInstalledFiles,
} from "./prepare.mjs";

const execFileAsync = promisify(execFile);
const VERSION = "4.3.4";
const ARCHIVE_BYTES = Buffer.from("offline archive fixture");
const EXECUTABLE_BYTES = Buffer.from("deterministic cmake executable");
const LICENSE_BYTES = Buffer.from("BSD 3-Clause fixture license");

function digest(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

function manifestFixture() {
  return {
    schemaVersion: 1,
    cmakeVersion: VERSION,
    license: "BSD-3-Clause",
    archives: {
      "win32-x64": {
        url: "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip",
        archiveSha256: digest(ARCHIVE_BYTES),
        rootDirectory: "cmake-4.3.4-windows-x86_64",
        executable: "bin/cmake.exe",
        licensePath: "doc/cmake/LICENSE.rst",
        installedFiles: {
          "bin/cmake.exe": digest(EXECUTABLE_BYTES),
          "doc/cmake/LICENSE.rst": digest(LICENSE_BYTES),
        },
      },
      "linux-x64": {
        url: "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
        archiveSha256: digest(ARCHIVE_BYTES),
        rootDirectory: "cmake-4.3.4-linux-x86_64",
        executable: "bin/cmake",
        licensePath: "doc/cmake/LICENSE.rst",
        installedFiles: {
          "bin/cmake": digest(EXECUTABLE_BYTES),
          "doc/cmake/LICENSE.rst": digest(LICENSE_BYTES),
        },
      },
    },
  };
}

function clone(value) {
  return structuredClone(value);
}

function manifestBytes(manifest) {
  return Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
}

async function exists(path) {
  try {
    await lstat(path);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

function offlineOperations({
  executableBytes = EXECUTABLE_BYTES,
  licenseBytes = LICENSE_BYTES,
  entries,
  beforePublish,
} = {}) {
  return {
    inspectArchive: async (_archive, archive) => entries ?? [
      { path: `${archive.rootDirectory}/`, type: "directory" },
      { path: `${archive.rootDirectory}/${archive.executable}`, type: "file" },
      { path: `${archive.rootDirectory}/${archive.licensePath}`, type: "file" },
    ],
    extractArchive: async (_archivePath, destination, archive) => {
      const root = join(destination, archive.rootDirectory);
      await mkdir(join(root, dirname(archive.executable)), { recursive: true });
      await mkdir(join(root, dirname(archive.licensePath)), { recursive: true });
      await writeFile(join(root, archive.executable), executableBytes);
      await writeFile(join(root, archive.licensePath), licenseBytes);
    },
    readCapabilities: async () => ({ version: { string: VERSION } }),
    beforePublish,
    renameDirectory: rename,
  };
}

async function prepareOffline({
  outputRoot,
  manifest = manifestFixture(),
  key = "linux-x64",
  operations = offlineOperations(),
  download = async (destination) => writeFile(destination, ARCHIVE_BYTES),
}) {
  return __testing.prepareBundleFromManifest({
    key,
    outputRoot,
    download,
    manifest,
    manifestBytes: manifestBytes(manifest),
    operations,
  });
}

async function stagingEntries(outputRoot) {
  return (await readdir(outputRoot)).filter((name) => name.startsWith(".cmake-bundle-"));
}

test("platformKey accepts only the supported operating system and architecture pairs", () => {
  assert.equal(platformKey("win32", "x64"), "win32-x64");
  assert.equal(platformKey("linux", "x64"), "linux-x64");
  for (const pair of [
    ["win32", "arm64"],
    ["linux", "arm64"],
    ["darwin", "x64"],
    ["freebsd", "x64"],
  ]) {
    assert.throws(() => platformKey(...pair), /unsupported CMake bundle platform/);
  }
});

test("validateManifest accepts only the fixed closed supply-chain contract", () => {
  assert.doesNotThrow(() => validateManifest(manifestFixture()));

  const cases = [
    ["missing version", (value) => delete value.cmakeVersion],
    ["unsupported version", (value) => { value.cmakeVersion = "4.3.5"; }],
    ["missing archives", (value) => delete value.archives],
    ["missing license", (value) => delete value.license],
    ["extra top-level field", (value) => { value.latest = true; }],
    ["missing archive", (value) => delete value.archives["win32-x64"]],
    ["extra archive", (value) => { value.archives["darwin-x64"] = clone(value.archives["linux-x64"]); }],
    ["non-HTTPS URL", (value) => { value.archives["linux-x64"].url = "http://cmake.org/files/v4.3/cmake.tar.gz"; }],
    ["foreign URL", (value) => { value.archives["linux-x64"].url = "https://example.com/files/v4.3/cmake.tar.gz"; }],
    ["credentialed URL", (value) => { value.archives["linux-x64"].url = "https://user@cmake.org/files/v4.3/cmake.tar.gz"; }],
    ["query URL", (value) => { value.archives["linux-x64"].url += "?mirror=1"; }],
    ["redirect override", (value) => { value.archives["linux-x64"].redirectUrl = "https://example.com/archive"; }],
    ["unsafe root", (value) => { value.archives["linux-x64"].rootDirectory = "../cmake"; }],
    ["unsafe executable", (value) => { value.archives["linux-x64"].executable = "../cmake"; }],
    ["missing executable digest", (value) => { delete value.archives["linux-x64"].installedFiles["bin/cmake"]; }],
    ["invalid digest", (value) => { value.archives["linux-x64"].archiveSha256 = "not-a-digest"; }],
  ];

  for (const [name, mutate] of cases) {
    const value = manifestFixture();
    mutate(value);
    assert.throws(() => validateManifest(value), /invalid CMake bundle manifest/, name);
  }
});

test("tracked manifest keeps the reviewed CMake 4.3.4 trust anchors", async () => {
  const manifest = JSON.parse(
    await readFile(new URL("./manifest.json", import.meta.url), "utf8"),
  );
  validateManifest(manifest);
  assert.deepEqual(
    {
      windowsArchive: manifest.archives["win32-x64"].archiveSha256,
      windowsExecutable: manifest.archives["win32-x64"].installedFiles["bin/cmake.exe"],
      windowsLicense: manifest.archives["win32-x64"].installedFiles["doc/cmake/LICENSE.rst"],
      linuxArchive: manifest.archives["linux-x64"].archiveSha256,
      linuxExecutable: manifest.archives["linux-x64"].installedFiles["bin/cmake"],
      linuxLicense: manifest.archives["linux-x64"].installedFiles["doc/cmake/LICENSE.rst"],
    },
    {
      windowsArchive: "86e5fcafb38bdf58346a78b187c7b6b4f252ae5242cffe24c463a92bbd2e77d1",
      windowsExecutable: "1aa884bf1f4949327fffcc8ee4a97c2d684bdc1d0a64b71f01dc16321c7fbc64",
      windowsLicense: "cd944d878806fee998ef3f88ca41ec060ae198bd8ba615e284f7d8d90c25593e",
      linuxArchive: "ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68",
      linuxExecutable: "8542b512ac147329e03de375583665a64f02afb65d6c4665099390be103ac2d0",
      linuxLicense: "4382e7c1879ac90e3f101a395d23846fa4dbcaa1eed7265b43681e348754825d",
    },
  );
});

test("redirect validation keeps every response on the locked CMake distribution URL", () => {
  const locked = "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz";
  assert.doesNotThrow(() => __testing.validateDistributionURL(locked, locked));
  for (const value of [
    "http://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
    "https://example.com/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
    "https://user@cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
    "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz?mirror=1",
    "https://cmake.org/files/v4.3/other.tar.gz",
    "https://cmake.org/files/v4.4/cmake-4.3.4-linux-x86_64.tar.gz",
  ]) {
    assert.throws(
      () => __testing.validateDistributionURL(value, locked),
      /outside the fixed distribution origin/,
    );
  }
});

test("CLI output stays inside the repository or an explicit absolute CI root", () => {
  const repository = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
  const inside = join(repository, ".bundled-tools", "cmake");
  const outsideRoot = join(dirname(repository), "cmake-bundle-ci-root");
  const outside = join(outsideRoot, "cmake");

  assert.equal(__testing.parseCLI(["--output", inside], {}).outputRoot, inside);
  assert.throws(
    () => __testing.parseCLI(["--output", outside], {}),
    /inside the repository/,
  );
  assert.equal(
    __testing.parseCLI(
      ["--key", "linux-x64", "--output", outside],
      { UNIT_TEST_IDE_CMAKE_BUNDLE_ALLOWED_ROOT: outsideRoot },
    ).outputRoot,
    outside,
  );
  assert.throws(
    () => __testing.parseCLI(["--key", "darwin-x64"], {}),
    /unsupported CMake bundle key/,
  );
});

test("sha256File and verifyInstalledFiles stream and bind installed paths to digests", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "cmake-bundle-files-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "bin"), { recursive: true });
  await mkdir(join(root, "doc", "cmake"), { recursive: true });
  await writeFile(join(root, "bin", "cmake"), EXECUTABLE_BYTES);
  await writeFile(join(root, "doc", "cmake", "LICENSE.rst"), LICENSE_BYTES);

  assert.equal(await sha256File(join(root, "bin", "cmake")), digest(EXECUTABLE_BYTES));
  await verifyInstalledFiles(root, {
    "bin/cmake": digest(EXECUTABLE_BYTES),
    "doc/cmake/LICENSE.rst": digest(LICENSE_BYTES),
  });
  await assert.rejects(
    verifyInstalledFiles(root, { "bin/cmake": "0".repeat(64) }),
    /installed file SHA-256 mismatch/,
  );

  try {
    await symlink(join(root, "bin", "cmake"), join(root, "linked-cmake"));
  } catch (error) {
    if (error?.code === "EPERM") {
      return;
    }
    throw error;
  }
  await assert.rejects(
    verifyInstalledFiles(root, { "linked-cmake": digest(EXECUTABLE_BYTES) }),
    /unsafe installed file/,
  );
});

test("fixed prepareBundle rejects an archive digest mismatch without publishing staging", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-digest-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));

  await assert.rejects(
    prepareBundle({
      key: "linux-x64",
      outputRoot,
      download: async (destination) => writeFile(destination, "tampered"),
    }),
    /archive SHA-256 mismatch/,
  );
  assert.equal(await exists(join(outputRoot, VERSION, "linux-x64")), false);
  assert.deepEqual(await stagingEntries(outputRoot), []);
});

test("preparation rejects an output root beneath a symlink or junction before download", async (t) => {
  const container = await mkdtemp(join(tmpdir(), "cmake-bundle-output-link-"));
  t.after(() => rm(container, { recursive: true, force: true }));
  const actual = join(container, "actual");
  const linked = join(container, "linked");
  await mkdir(actual);
  try {
    await symlink(actual, linked, process.platform === "win32" ? "junction" : "dir");
  } catch (error) {
    if (error?.code === "EPERM") {
      return;
    }
    throw error;
  }

  let downloads = 0;
  await assert.rejects(
    prepareOffline({
      outputRoot: join(linked, "cmake"),
      download: async () => {
        downloads++;
      },
    }),
    /unsafe CMake bundle directory/,
  );
  assert.equal(downloads, 0);
  assert.equal(await exists(join(actual, "cmake")), false);
});

test("preparation binds published manifest bytes to the validated manifest object", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-manifest-bytes-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));
  const manifest = manifestFixture();
  const different = manifestFixture();
  different.archives["linux-x64"].archiveSha256 = "0".repeat(64);

  await assert.rejects(
    __testing.prepareBundleFromManifest({
      key: "linux-x64",
      outputRoot,
      download: async (destination) => writeFile(destination, ARCHIVE_BYTES),
      manifest,
      manifestBytes: manifestBytes(different),
      operations: offlineOperations(),
    }),
    /manifest bytes do not match/,
  );
  assert.deepEqual(await readdir(outputRoot), []);
});

test("offline preparation publishes an immutable verified bundle and exact manifest", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-publish-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));
  const manifest = manifestFixture();

  const result = await prepareOffline({ outputRoot, manifest });
  const target = join(outputRoot, VERSION, "linux-x64");
  const installRoot = join(target, manifest.archives["linux-x64"].rootDirectory);
  assert.equal(result.root, target);
  assert.equal(result.installRoot, installRoot);
  assert.equal(result.executable, join(installRoot, manifest.archives["linux-x64"].executable));
  assert.equal(result.reused, false);
  assert.deepEqual(await readFile(join(outputRoot, "manifest.json")), manifestBytes(manifest));
  assert.deepEqual(
    JSON.parse(await readFile(join(target, "bundle-state.json"), "utf8")),
    {
      schemaVersion: 1,
      key: "linux-x64",
      cmakeVersion: VERSION,
      archiveSha256: digest(ARCHIVE_BYTES),
      installedFiles: manifest.archives["linux-x64"].installedFiles,
    },
  );
  assert.deepEqual(await stagingEntries(outputRoot), []);
});

test("installed executable or license mismatch removes staging and preserves no target", async (t) => {
  for (const [name, operations] of [
    ["executable", offlineOperations({ executableBytes: Buffer.from("wrong executable") })],
    ["license", offlineOperations({ licenseBytes: Buffer.from("wrong license") })],
  ]) {
    await t.test(name, async (t) => {
      const outputRoot = await mkdtemp(join(tmpdir(), `cmake-bundle-${name}-`));
      t.after(() => rm(outputRoot, { recursive: true, force: true }));
      await assert.rejects(
        prepareOffline({ outputRoot, operations }),
        /installed file SHA-256 mismatch/,
      );
      assert.equal(await exists(join(outputRoot, VERSION, "linux-x64")), false);
      assert.deepEqual(await stagingEntries(outputRoot), []);
    });
  }
});

test("post-extraction audit rejects a symlink or junction omitted from the listing", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-extracted-link-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));
  const operations = offlineOperations();
  const extract = operations.extractArchive;
  operations.extractArchive = async (...arguments_) => {
    await extract(...arguments_);
    const [, destination, archive] = arguments_;
    const outside = join(outputRoot, "outside");
    await mkdir(outside);
    await symlink(
      outside,
      join(destination, archive.rootDirectory, "linked"),
      process.platform === "win32" ? "junction" : "dir",
    );
  };

  await assert.rejects(
    prepareOffline({ outputRoot, operations }),
    /unsafe extracted archive entry/,
  );
  assert.equal(await exists(join(outputRoot, VERSION, "linux-x64")), false);
  assert.deepEqual(await stagingEntries(outputRoot), []);
});

test("unsafe archive entries are rejected before extraction", async (t) => {
  for (const entry of [
    { path: "/absolute/cmake", type: "file" },
    { path: "../escape", type: "file" },
    { path: "cmake-4.3.4-linux-x86_64/../../escape", type: "file" },
    { path: "C:/escape", type: "file" },
    { path: "cmake-4.3.4-linux-x86_64/file:stream", type: "file" },
    { path: "other-root/bin/cmake", type: "file" },
    { path: "cmake-4.3.4-linux-x86_64/link", type: "symlink" },
    { path: "cmake-4.3.4-linux-x86_64/device", type: "device" },
  ]) {
    await t.test(`${entry.type} ${entry.path}`, async (t) => {
      const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-entry-"));
      t.after(() => rm(outputRoot, { recursive: true, force: true }));
      let extracted = false;
      const operations = offlineOperations({ entries: [entry] });
      operations.extractArchive = async () => {
        extracted = true;
      };
      await assert.rejects(
        prepareOffline({ outputRoot, operations }),
        /unsafe archive entry/,
      );
      assert.equal(extracted, false);
      assert.deepEqual(await stagingEntries(outputRoot), []);
    });
  }
});

test("an existing complete bundle is verified and reused without downloading", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-reuse-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));
  await prepareOffline({ outputRoot });

  let downloads = 0;
  const result = await prepareOffline({
    outputRoot,
    download: async () => {
      downloads++;
      throw new Error("download must not run");
    },
  });
  assert.equal(result.reused, true);
  assert.equal(downloads, 0);
  assert.deepEqual(await stagingEntries(outputRoot), []);
});

test("a publish race keeps the existing valid target and discards staging", async (t) => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-race-"));
  t.after(() => rm(outputRoot, { recursive: true, force: true }));
  const manifest = manifestFixture();
  const archive = manifest.archives["linux-x64"];
  let racedTarget;

  const operations = offlineOperations({
    beforePublish: async ({ target }) => {
      racedTarget = target;
      const installRoot = join(target, archive.rootDirectory);
      await mkdir(join(installRoot, "bin"), { recursive: true });
      await mkdir(join(installRoot, "doc", "cmake"), { recursive: true });
      await writeFile(join(installRoot, archive.executable), EXECUTABLE_BYTES);
      await writeFile(join(installRoot, archive.licensePath), LICENSE_BYTES);
      await writeFile(join(target, "race-marker"), "existing");
      await writeFile(join(target, "bundle-state.json"), `${JSON.stringify({
        schemaVersion: 1,
        key: "linux-x64",
        cmakeVersion: VERSION,
        archiveSha256: archive.archiveSha256,
        installedFiles: archive.installedFiles,
      }, null, 2)}\n`);
    },
  });

  const result = await prepareOffline({ outputRoot, manifest, operations });
  assert.equal(result.reused, true);
  assert.equal(result.root, racedTarget);
  assert.equal(await readFile(join(racedTarget, "race-marker"), "utf8"), "existing");
  assert.deepEqual(await stagingEntries(outputRoot), []);
});

test("system archive inspection reports only regular files and directories", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "cmake-bundle-tar-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = join(root, "source");
  const archive = join(root, "fixture.tar");
  await mkdir(join(source, "fixture", "bin"), { recursive: true });
  await writeFile(join(source, "fixture", "bin", "cmake"), "fixture");
  await execFileAsync("tar", ["-cf", archive, "-C", source, "fixture"]);

  const entries = await __testing.inspectArchive(archive);
  assert.ok(entries.some((entry) => entry.path === "fixture/bin/cmake" && entry.type === "file"));
  assert.ok(entries.every((entry) => entry.type === "file" || entry.type === "directory"));
});
