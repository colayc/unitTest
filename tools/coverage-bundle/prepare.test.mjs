import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

import { bundleDirectory, platformKey } from "./layout.mjs";
import { __testing } from "./prepare.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");

const fixtureCrcTable = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index++) {
    let value = index;
    for (let bit = 0; bit < 8; bit++) value = (value & 1) ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    table[index] = value >>> 0;
  }
  return table;
})();

function fixtureCrc32(bytes) {
  let value = 0xffffffff;
  for (const byte of bytes) value = fixtureCrcTable[(value ^ byte) & 0xff] ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}

function rawZip(entries) {
  const localRecords = [];
  const centralRecords = [];
  let offset = 0;
  for (const fixture of entries) {
    const data = Buffer.from(fixture.data ?? "fixture");
    const name = Buffer.from(fixture.name, "ascii");
    const localName = Buffer.from(fixture.localName ?? fixture.name, "ascii");
    const localExtra = Buffer.from(fixture.localExtra ?? []);
    const centralExtra = Buffer.from(fixture.centralExtra ?? []);
    const size = fixture.declaredSize ?? data.length;
    const crc = fixtureCrc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 6);
    local.writeUInt16LE(0, 8);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(size, 18);
    local.writeUInt32LE(size, 22);
    local.writeUInt16LE(localName.length, 26);
    local.writeUInt16LE(localExtra.length, 28);
    localRecords.push(local, localName, localExtra, data);

    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(0x0314, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0, 8);
    central.writeUInt16LE(0, 10);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(size, 20);
    central.writeUInt32LE(size, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt16LE(centralExtra.length, 30);
    central.writeUInt32LE(((fixture.mode ?? 0o100644) << 16) >>> 0, 38);
    central.writeUInt32LE(offset, 42);
    centralRecords.push(central, name, centralExtra);
    offset += local.length + localName.length + localExtra.length + data.length;
  }
  const central = Buffer.concat(centralRecords);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(central.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...localRecords, central, end]);
}

function tarOctal(value, length) {
  return `${value.toString(8).padStart(length - 1, "0")}\0`;
}

function tarHeader({ name, type = "0", size = 0, linkName = "", corruptChecksum = false }) {
  const header = Buffer.alloc(512);
  header.write(name, 0, 100, "utf8");
  header.write(tarOctal(0o644, 8), 100, 8, "ascii");
  header.write(tarOctal(0, 8), 108, 8, "ascii");
  header.write(tarOctal(0, 8), 116, 8, "ascii");
  header.write(tarOctal(size, 12), 124, 12, "ascii");
  header.write(tarOctal(0, 12), 136, 12, "ascii");
  header.fill(0x20, 148, 156);
  header.write(type, 156, 1, "ascii");
  header.write(linkName, 157, 100, "utf8");
  header.write("ustar\0", 257, 6, "ascii");
  header.write("00", 263, 2, "ascii");
  const checksum = [...header].reduce((sum, byte) => sum + byte, 0) + (corruptChecksum ? 1 : 0);
  header.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
  return header;
}

function rawTar(entries) {
  const records = [];
  for (const entry of entries) {
    const data = Buffer.from(entry.data ?? "");
    const declaredSize = entry.declaredSize ?? data.length;
    records.push(tarHeader({ ...entry, size: declaredSize }), data);
    const padding = (512 - (data.length % 512)) % 512;
    if (padding) records.push(Buffer.alloc(padding));
  }
  records.push(Buffer.alloc(1024));
  return Buffer.concat(records);
}

function paxRecord(key, value) {
  let length = key.length + value.length + 4;
  while (`${length} ${key}=${value}\n`.length !== length) length = `${length} ${key}=${value}\n`.length;
  return `${length} ${key}=${value}\n`;
}

function artifact(filename, url, bytes, kind = "wheel") {
  return { filename, url, sha256: digest(bytes), kind };
}

function fixture() {
  const pythonBytes = Buffer.from("python archive fixture");
  const wheelBytes = Buffer.from("wheel archive fixture");
  const lzmaBytes = Buffer.from("xz source archive fixture");
  return {
    pythonBytes,
    wheelBytes,
    lzmaBytes,
    manifest: {
      schemaVersion: 1,
      python: {
        version: "3.14.6",
        license: "PSF-2.0",
        artifacts: {
          "windows-x64": artifact(
            "python-3.14.6-embed-amd64.zip",
            "https://www.python.org/ftp/python/3.14.6/python-3.14.6-embed-amd64.zip",
            pythonBytes,
            "embedded-archive",
          ),
          "linux-x64": artifact(
            "Python-3.14.6.tgz",
            "https://www.python.org/ftp/python/3.14.6/Python-3.14.6.tgz",
            pythonBytes,
            "source-archive",
          ),
        },
      },
      gcovr: {
        version: "8.6",
        license: "BSD-3-Clause",
        wheels: [{
          project: "gcovr",
          version: "8.6",
          kind: "wheel",
          files: [{
            ...(({ filename, url, sha256 }) => ({ filename, url, sha256 }))(artifact(
              "gcovr-8.6-py3-none-any.whl",
              "https://files.pythonhosted.org/packages/fixture/gcovr.whl",
              wheelBytes,
            )),
            platforms: ["windows-x64", "linux-x64"],
          }],
        }],
      },
      linux: {
        builder: {
          image: "quay.io/pypa/manylinux_2_28_x86_64@sha256:0c87ccb5996dab6c3b7612ee4fda7b80c4ab3c44a86c2541e4a872afdf4f131b",
          sourceUrl: "https://quay.io/repository/pypa/manylinux_2_28_x86_64",
        },
        glibcBaseline: "2.28",
        muslPolicy: "unsupported",
        liblzma: artifact(
          "xz-5.8.1.tar.gz",
          "https://github.com/tukaani-project/xz/releases/download/v5.8.1/xz-5.8.1.tar.gz",
          lzmaBytes,
          "source-archive",
        ),
      },
    },
  };
}

async function exists(path) {
  try {
    await readFile(path);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT" || error?.code === "EISDIR") return false;
    throw error;
  }
}

async function temporary(t, prefix) {
  const root = await mkdtemp(join(tmpdir(), prefix));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

async function writeOutput(root, pth = "python314.zip\n.\n../app/gcovr-runner.pyz\n") {
  await mkdir(join(root, "python"), { recursive: true });
  await mkdir(join(root, "app"), { recursive: true });
  await mkdir(join(root, "licenses"), { recursive: true });
  await writeFile(join(root, "python", "python.exe"), "python");
  await writeFile(join(root, "python", "python314._pth"), pth);
  await writeFile(join(root, "app", "gcovr-runner.pyz"), "application");
  await writeFile(join(root, "licenses", "NOTICE.txt"), "licenses");
}

function operations({ inspect, build, beforeReady, beforePublish } = {}) {
  return {
    inspectArchive: inspect ?? (async () => [{ path: "safe/archive", type: "file" }]),
    buildBundle: build ?? (async ({ stagingRoot }) => writeOutput(stagingRoot)),
    beforeReady,
    beforePublish,
  };
}

async function prepareFixture(t, overrides = {}) {
  const root = await temporary(t, "coverage-bundle-");
  const cacheRoot = join(root, "cache");
  const outputRoot = join(root, "runtime");
  const { manifest, pythonBytes, wheelBytes, lzmaBytes } = fixture();
  const downloads = [];
  const byUrl = new Map([
    [manifest.python.artifacts["windows-x64"].url, pythonBytes],
    [manifest.gcovr.wheels[0].files[0].url, wheelBytes],
    [manifest.linux.liblzma.url, lzmaBytes],
  ]);
  const download = async (url, destination) => {
    downloads.push(url);
    await writeFile(destination, byUrl.get(url) ?? "unexpected");
  };
  const result = await __testing.prepareBundleFromManifest({
    manifest,
    key: "windows-x64",
    outputRoot,
    cacheRoot,
    download,
    operations: operations(),
    ...overrides,
  });
  return { root, cacheRoot, outputRoot, manifest, downloads, result, download };
}

test("layout accepts only the separate Windows/Linux x64 bundle keys", () => {
  assert.equal(platformKey("win32", "x64"), "windows-x64");
  assert.equal(platformKey("linux", "x64"), "linux-x64");
  assert.throws(() => platformKey("darwin", "x64"), /unsupported coverage bundle platform/u);
  assert.throws(() => platformKey("win32", "arm64"), /unsupported coverage bundle platform/u);
  assert.match(bundleDirectory("C:/repo", "windows-x64"), /coverage-bundle[\\/]windows-x64$/u);
});

test("platform-specific wheels are omitted from another platform's resolved inputs", async () => {
  const { manifest, wheelBytes } = fixture();
  manifest.gcovr.wheels.push({
    project: "colorama",
    version: "0.4.6",
    kind: "wheel",
    files: [{
      ...artifact(
        "colorama-0.4.6-py2.py3-none-any.whl",
        "https://files.pythonhosted.org/packages/fixture/colorama.whl",
        wheelBytes,
      ),
      platforms: ["windows-x64"],
    }],
  });
  const resolved = await __testing.resolvedInputs(manifest, "linux-x64");
  assert.deepEqual(resolved.wheels.map(({ project }) => project), ["gcovr"]);
});

test("cold prepare downloads only the platform manifest allowlist and verifies before inspection", async (t) => {
  const order = [];
  const { manifest } = fixture();
  const expected = [
    manifest.python.artifacts["windows-x64"].url,
    manifest.gcovr.wheels[0].files[0].url,
  ];
  const prepared = await prepareFixture(t, {
    download: async (url, destination) => {
      order.push(`download:${url}`);
      const bytes = url === expected[0] ? Buffer.from("python archive fixture") : Buffer.from("wheel archive fixture");
      await writeFile(destination, bytes);
    },
    operations: operations({
      inspect: async (path) => {
        order.push(`inspect:${path}`);
        return [{ path: "safe/file", type: "file" }];
      },
    }),
  });
  assert.deepEqual(prepared.downloads, []);
  assert.deepEqual(order.filter((item) => item.startsWith("download:")), expected.map((url) => `download:${url}`));
  assert.equal(order[0].startsWith("download:"), true);
  assert.equal(order[1].startsWith("inspect:"), true);
});

test("tampered and interrupted downloads never reach inspection or leave partial cache files", async (t) => {
  const root = await temporary(t, "coverage-bundle-download-");
  const { manifest } = fixture();
  let inspections = 0;
  await assert.rejects(
    __testing.prepareBundleFromManifest({
      manifest,
      key: "windows-x64",
      outputRoot: join(root, "runtime"),
      cacheRoot: join(root, "cache"),
      download: async (_url, destination) => writeFile(destination, "tampered"),
      operations: operations({ inspect: async () => { inspections++; return []; } }),
    }),
    /SHA-256 mismatch/u,
  );
  assert.equal(inspections, 0);
  assert.deepEqual(await readdir(join(root, "cache")), []);

  await assert.rejects(
    __testing.obtainArtifact(manifest.python.artifacts["windows-x64"], join(root, "cache"), async (_url, destination) => {
      await writeFile(destination, "partial");
      throw new Error("network interrupted");
    }),
    /network interrupted/u,
  );
  assert.deepEqual(await readdir(join(root, "cache")), []);
});

test("archive audit rejects traversal, symlink and duplicate entries before extraction", () => {
  for (const entries of [
    [{ path: "../escape", type: "file" }],
    [{ path: "/absolute", type: "file" }],
    [{ path: "C:/escape", type: "file" }],
    [{ path: "safe/link", type: "symlink" }],
    [{ path: "same", type: "file" }, { path: "same", type: "file" }],
  ]) {
    assert.throws(() => __testing.validateArchiveEntries(entries), /unsafe archive entry/u);
  }
});

test("archive audit accepts safe internal spaces in regular filenames", () => {
  assert.deepEqual(__testing.validateArchiveEntries([
    { path: "Python-3.14.6/Mac/Icons/Disk Image.icns", type: "file" },
  ]), [
    { path: "Python-3.14.6/Mac/Icons/Disk Image.icns", type: "file" },
  ]);
  assert.throws(() => __testing.validateArchiveEntries([
    { path: "Python-3.14.6/Mac/Icons/Disk Image.icns ", type: "file" },
  ]), /unsafe archive entry/u);
  assert.throws(() => __testing.validateArchiveEntries([
    { path: "Python-3.14.6/Mac/Icons/ Disk Image.icns", type: "file" },
  ]), /unsafe archive entry/u);
  assert.throws(() => __testing.validateArchiveEntries([
    { path: "Python-3.14.6/Mac/Icons/DiskImage.icns.", type: "file" },
  ]), /unsafe archive entry/u);
});

test("prepare builds in a temp directory, writes READY last, and atomically publishes", async (t) => {
  let sawTargetDuringBuild = false;
  let sawReadyBeforeHook = true;
  let sawReadyAtPublish = false;
  const root = await temporary(t, "coverage-bundle-publish-");
  const outputRoot = join(root, "runtime");
  const target = join(outputRoot, "windows-x64");
  const { manifest, pythonBytes, wheelBytes } = fixture();
  await __testing.prepareBundleFromManifest({
    manifest,
    key: "windows-x64",
    outputRoot,
    cacheRoot: join(root, "cache"),
    download: async (url, destination) => writeFile(destination, url.includes("python.org") ? pythonBytes : wheelBytes),
    operations: operations({
      build: async ({ stagingRoot }) => {
        sawTargetDuringBuild = await exists(join(target, "READY"));
        await writeOutput(stagingRoot);
      },
      beforeReady: async ({ stagingRoot }) => { sawReadyBeforeHook = await exists(join(stagingRoot, "READY")); },
      beforePublish: async ({ stagingRoot }) => { sawReadyAtPublish = await exists(join(stagingRoot, "READY")); },
    }),
  });
  assert.equal(sawTargetDuringBuild, false);
  assert.equal(sawReadyBeforeHook, false);
  assert.equal(sawReadyAtPublish, true);
  assert.equal(await readFile(join(target, "READY"), "utf8"), "ready\n");
  assert.deepEqual((await readdir(outputRoot)).filter((name) => name.startsWith(".coverage-bundle-")), []);
});

test("interrupted prepare leaves no consumable bundle", async (t) => {
  const root = await temporary(t, "coverage-bundle-interrupt-");
  const { manifest, pythonBytes, wheelBytes } = fixture();
  const outputRoot = join(root, "runtime");
  await assert.rejects(
    __testing.prepareBundleFromManifest({
      manifest,
      key: "windows-x64",
      outputRoot,
      cacheRoot: join(root, "cache"),
      download: async (url, destination) => writeFile(destination, url.includes("python.org") ? pythonBytes : wheelBytes),
      operations: operations({ beforePublish: async () => { throw new Error("interrupted"); } }),
    }),
    /interrupted/u,
  );
  assert.equal(await exists(join(outputRoot, "windows-x64", "READY")), false);
  assert.deepEqual((await readdir(outputRoot)).filter((name) => name.startsWith(".coverage-bundle-")), []);
});

test("post-install build failure leaves host-side staging removable and unpublished", async (t) => {
  const root = await temporary(t, "coverage-bundle-post-install-");
  const { manifest, pythonBytes, wheelBytes } = fixture();
  const outputRoot = join(root, "runtime");
  await assert.rejects(
    __testing.prepareBundleFromManifest({
      manifest,
      key: "windows-x64",
      outputRoot,
      cacheRoot: join(root, "cache"),
      download: async (url, destination) => writeFile(destination, url.includes("python.org") ? pythonBytes : wheelBytes),
      operations: operations({
        build: async ({ stagingRoot }) => {
          await writeOutput(stagingRoot);
          throw new Error("post-install failure");
        },
      }),
    }),
    /post-install failure/u,
  );
  assert.equal(await exists(join(outputRoot, "windows-x64", "READY")), false);
  assert.deepEqual((await readdir(outputRoot)).filter((name) => name.startsWith(".coverage-bundle-")), []);
});

test("cache hit performs full resolved-manifest verification and detects tampering", async (t) => {
  const prepared = await prepareFixture(t);
  let downloads = 0;
  const reused = await __testing.prepareBundleFromManifest({
    manifest: prepared.manifest,
    key: "windows-x64",
    outputRoot: prepared.outputRoot,
    cacheRoot: prepared.cacheRoot,
    download: async () => { downloads++; },
    operations: operations(),
  });
  assert.equal(reused.reused, true);
  assert.equal(downloads, 0);

  await writeFile(join(prepared.result.root, "app", "gcovr-runner.pyz"), "tampered");
  await assert.rejects(
    __testing.prepareBundleFromManifest({
      manifest: prepared.manifest,
      key: "windows-x64",
      outputRoot: prepared.outputRoot,
      cacheRoot: prepared.cacheRoot,
      download: async () => { downloads++; },
      operations: operations(),
    }),
    /output SHA-256 mismatch/u,
  );
  assert.equal(downloads, 0);
});

test("cache identity binds exact selected inputs and deterministic recipe provenance", async (t) => {
  const prepared = await prepareFixture(t);
  const resolved = JSON.parse(await readFile(join(prepared.result.root, "manifest.resolved.json"), "utf8"));
  assert.deepEqual(Object.keys(resolved.inputs).sort(), ["buildSources", "provenance", "pythonArtifact", "wheels"]);
  assert.deepEqual(resolved.inputs.buildSources, []);
  assert.deepEqual(Object.keys(resolved.inputs.provenance).sort(), ["builderImage", "glibcBaseline", "recipe"]);
  assert.match(resolved.inputs.provenance.recipe.name, /coverage-bundle/u);
  assert.match(resolved.inputs.provenance.recipe.sha256, /^[0-9a-f]{64}$/u);
  assert.equal(resolved.inputs.pythonArtifact.sha256, prepared.manifest.python.artifacts["windows-x64"].sha256);
  assert.deepEqual(resolved.inputs.wheels.map(({ project }) => project), ["gcovr"]);
  assert.equal(resolved.inputs.provenance.builderImage, null);
  assert.equal(resolved.inputs.provenance.glibcBaseline, null);

  const linuxRoot = await temporary(t, "coverage-bundle-linux-provenance-");
  await writeOutput(linuxRoot);
  const linuxResolved = await __testing.createResolvedManifest(linuxRoot, "linux-x64", prepared.manifest);
  assert.deepEqual(linuxResolved.inputs.buildSources.map(({ filename }) => filename), ["xz-5.8.1.tar.gz"]);
  assert.equal(linuxResolved.inputs.provenance.builderImage, prepared.manifest.linux.builder.image);
  assert.equal(linuxResolved.inputs.provenance.glibcBaseline, "2.28");

  const changed = structuredClone(prepared.manifest);
  changed.python.artifacts["windows-x64"].sha256 = digest("different locked Python input");
  await assert.rejects(
    __testing.prepareBundleFromManifest({
      manifest: changed,
      key: "windows-x64",
      outputRoot: prepared.outputRoot,
      cacheRoot: prepared.cacheRoot,
      download: async () => assert.fail("identity mismatch must fail before download"),
      operations: operations(),
    }),
    /resolved input.*mismatch/iu,
  );

  resolved.inputs.provenance.recipe.sha256 = digest("stale preparation recipe");
  await writeFile(join(prepared.result.root, "manifest.resolved.json"), `${JSON.stringify(resolved, null, 2)}\n`);
  await assert.rejects(
    __testing.verifyResolvedBundle(prepared.result.root, "windows-x64", prepared.manifest),
    /resolved input.*mismatch/iu,
  );
});

test("resolved output list and bytes are deterministic and include only regular generated files", async (t) => {
  const first = await prepareFixture(t);
  const second = await prepareFixture(t);
  const one = await readFile(join(first.result.root, "manifest.resolved.json"), "utf8");
  const two = await readFile(join(second.result.root, "manifest.resolved.json"), "utf8");
  assert.equal(one, two);
  const resolved = JSON.parse(one);
  assert.deepEqual(Object.keys(resolved).sort(), ["gcovrVersion", "inputs", "outputs", "platform", "pythonVersion", "schemaVersion"]);
  assert.deepEqual(resolved.outputs.map(({ path }) => path), [...resolved.outputs.map(({ path }) => path)].sort());
  assert.ok(resolved.outputs.every((entry) => entry.kind === "regular-file" && /^[0-9a-f]{64}$/u.test(entry.sha256)));
  assert.equal(resolved.outputs.some(({ path }) => path === "READY" || path === "manifest.resolved.json"), false);
});

test("deterministic application ZIP round-trips every regular entry", async (t) => {
  const root = await temporary(t, "coverage-bundle-zip-");
  const archive = join(root, "application.pyz");
  await writeFile(archive, __testing.deterministicZip(new Map([
    ["__main__.py", Buffer.from("print('ok')\n")],
    ["package/module.py", Buffer.from("VALUE = 1\n")],
  ])));
  assert.deepEqual(await __testing.inspectArchive(archive), [
    { path: "__main__.py", type: "file" },
    { path: "package/module.py", type: "file" },
  ]);
});

test("Windows isolation path excludes import site and forbidden final layout entries", async (t) => {
  const prepared = await prepareFixture(t);
  const pth = await readFile(join(prepared.result.root, "python", "python314._pth"), "utf8");
  assert.doesNotMatch(pth, /^\s*import\s+site\s*$/mu);
  assert.match(pth, /^\.\.\/app\/gcovr-runner\.pyz$/mu);
  await assert.rejects(async () => {
    const root = await temporary(t, "coverage-bundle-forbidden-");
    await writeOutput(root);
    await mkdir(join(root, "python", "Lib", "ensurepip"), { recursive: true });
    await writeFile(join(root, "python", "Lib", "ensurepip", "__init__.py"), "bad");
    await __testing.createResolvedManifest(root, "windows-x64", fixture().manifest);
  }, /forbidden bundle path/u);
});

test("resolved layout rejects unexpected top-level and app files", async (t) => {
  for (const extra of ["unexpected.txt", "app/forwarded-options.json"]) {
    const root = await temporary(t, "coverage-bundle-extra-");
    await writeOutput(root);
    await mkdir(dirname(join(root, extra)), { recursive: true });
    await writeFile(join(root, extra), "unexpected");
    await assert.rejects(
      __testing.createResolvedManifest(root, "windows-x64", fixture().manifest),
      /unexpected bundle (?:top-level|app) entry/u,
    );
  }
});

test("cache verification reapplies exact app layout even when output digests were rewritten", async (t) => {
  const prepared = await prepareFixture(t);
  const extraPath = join(prepared.result.root, "app", "forwarded-options.json");
  await writeFile(extraPath, "unexpected");
  const manifestPath = join(prepared.result.root, "manifest.resolved.json");
  const resolved = JSON.parse(await readFile(manifestPath, "utf8"));
  resolved.outputs.push({ path: "app/forwarded-options.json", sha256: digest("unexpected"), kind: "regular-file" });
  resolved.outputs.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
  await writeFile(manifestPath, `${JSON.stringify(resolved, null, 2)}\n`);
  await assert.rejects(
    __testing.verifyResolvedBundle(prepared.result.root, "windows-x64", prepared.manifest),
    /unexpected bundle app entry/u,
  );
});

test("byte-level ZIP audit rejects unsafe paths, aliases, types, metadata and expansion", async (t) => {
  const root = await temporary(t, "coverage-bundle-unsafe-zip-");
  const cases = [
    ["traversal", [{ name: "../escape" }]],
    ["absolute", [{ name: "/absolute" }]],
    ["drive", [{ name: "C:/escape" }]],
    ["ADS", [{ name: "safe/file:stream" }]],
    ["symlink", [{ name: "safe/link", mode: 0o120777 }]],
    ["unsupported FIFO", [{ name: "safe/fifo", mode: 0o010644 }]],
    ["duplicate", [{ name: "same" }, { name: "same" }]],
    ["case alias", [{ name: "Same" }, { name: "same" }]],
    ["local-central mismatch", [{ name: "safe/name", localName: "evil/name" }]],
    ["unsupported hardlink metadata", [{ name: "safe/link", centralExtra: Buffer.from([0x0d, 0x00, 0x00, 0x00]) }]],
    ["malformed local metadata", [{ name: "safe/name", localExtra: Buffer.from([0x55]) }]],
    ["expansion overflow", [{ name: "safe/huge", declaredSize: 1024 * 1024 * 1024 + 1 }]],
  ];
  for (const [name, entries] of cases) {
    const path = join(root, `${name.replaceAll(" ", "-")}.zip`);
    await writeFile(path, rawZip(entries));
    await assert.rejects(__testing.inspectArchive(path), /archive|ZIP/iu, name);
  }
});

test("byte-level TAR audit rejects unsafe paths, links, types, metadata and expansion", async (t) => {
  const root = await temporary(t, "coverage-bundle-unsafe-tar-");
  const cases = [
    ["traversal", [{ name: "../escape", data: "x" }]],
    ["absolute", [{ name: "/absolute", data: "x" }]],
    ["drive", [{ name: "C:/escape", data: "x" }]],
    ["ADS", [{ name: "safe/file:stream", data: "x" }]],
    ["symlink", [{ name: "safe/link", type: "2", linkName: "../escape" }]],
    ["hardlink", [{ name: "safe/link", type: "1", linkName: "safe/target" }]],
    ["unsupported device", [{ name: "safe/device", type: "3" }]],
    ["duplicate", [{ name: "same", data: "a" }, { name: "same", data: "b" }]],
    ["case alias", [{ name: "Same", data: "a" }, { name: "same", data: "b" }]],
    ["bad checksum", [{ name: "safe/file", data: "x", corruptChecksum: true }]],
    ["unsupported PAX size", [
      { name: "PaxHeader", type: "x", data: paxRecord("size", "100") },
      { name: "safe/file", data: "x" },
    ]],
    ["malformed PAX record", [
      { name: "PaxHeader", type: "x", data: "12 path=x\n" },
      { name: "safe/file", data: "x" },
    ]],
    ["expansion overflow", [{ name: "safe/huge", declaredSize: 1024 * 1024 * 1024 + 1 }]],
  ];
  for (const [name, entries] of cases) {
    const path = join(root, `${name.replaceAll(" ", "-")}.tgz`);
    await writeFile(path, gzipSync(rawTar(entries)));
    await assert.rejects(__testing.inspectArchive(path), /archive|TAR|PAX/iu, name);
  }
});

test("TAR audit accepts a safe GNU longname with identical extraction semantics", async (t) => {
  const root = await temporary(t, "coverage-bundle-safe-tar-");
  const longPath = `Python-3.14.6/${"long-directory/".repeat(8)}module.py`;
  const archive = rawTar([
    { name: "././@LongLink", type: "L", data: `${longPath}\0` },
    { name: "placeholder", data: "safe" },
  ]);
  const path = join(root, "safe.tgz");
  await writeFile(path, gzipSync(archive));
  assert.deepEqual(await __testing.inspectArchive(path), [{ path: longPath, type: "file" }]);
});

test("TAR audit accepts the locked release archive's V7 headers", async (t) => {
  const root = await temporary(t, "coverage-bundle-v7-tar-");
  const archive = rawTar([{ name: "Python-3.14.6/Modules/_lzma.c", data: "safe" }]);
  archive.fill(0, 257, 263);
  archive.fill(0x20, 148, 156);
  const checksum = [...archive.subarray(0, 512)].reduce((sum, byte) => sum + byte, 0);
  archive.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
  const path = join(root, "v7.tgz");
  await writeFile(path, gzipSync(archive));
  assert.deepEqual(await __testing.inspectArchive(path), [{ path: "Python-3.14.6/Modules/_lzma.c", type: "file" }]);
});

test("TAR audit accepts a Linux source filename with a trailing dot", async (t) => {
  const root = await temporary(t, "coverage-bundle-safe-trailing-dot-");
  const sourcePath = "Python-3.14.6/Modules/_xxtestfuzz/fuzz_elementtree_parsewhole_corpus/out_inNsSuperfluous_c14nPrefix.";
  const archive = rawTar([{ name: sourcePath, data: "safe" }]);
  const path = join(root, "safe-trailing-dot.tgz");
  await writeFile(path, gzipSync(archive));
  assert.deepEqual(await __testing.inspectArchive(path), [{ path: sourcePath, type: "file" }]);
});

test("TAR audit accepts only path-safe local PAX and metadata-only global PAX semantics", async (t) => {
  const root = await temporary(t, "coverage-bundle-safe-pax-");
  const paxPath = `Python-3.14.6/${"pax-directory/".repeat(8)}module.py`;
  const archive = rawTar([
    { name: "GlobalPaxHeader", type: "g", data: paxRecord("mtime", "0") },
    { name: "PaxHeader", type: "x", data: paxRecord("path", paxPath) },
    { name: "placeholder", data: "safe" },
  ]);
  const path = join(root, "safe-pax.tgz");
  await writeFile(path, gzipSync(archive));
  assert.deepEqual(await __testing.inspectArchive(path), [{ path: paxPath, type: "file" }]);
});

test("Linux builder contract pins image ABI, configure flags, source epoch and disables network", async () => {
  const script = await readFile(new URL("./build-linux.sh", import.meta.url), "utf8");
  assert.match(script, /manylinux_2_28/u);
  assert.match(script, /glibc[^\n]*2\.28|2\.28[^\n]*glibc/iu);
  assert.match(script, /SOURCE_DATE_EPOCH=\d+/u);
  assert.match(script, /--with-ensurepip=no/u);
  assert.match(script, /--disable-test-modules/u);
  assert.match(script, /--without-static-libpython/u);
  assert.match(script, /--enable-shared/u);
  assert.ok(script.includes("LDFLAGS='-Wl,-rpath,\\$ORIGIN/../lib'"), "Python binary must retain a bundle-relative shared-library rpath");
  assert.match(script, /LZMA_SOURCE/u);
  assert.match(script, /xz-5\.8\.1\.tar\.gz/u);
  assert.match(script, /LIBLZMA_CFLAGS/u);
  assert.match(script, /LIBLZMA_LIBS/u);
  assert.match(script, /--disable-static/u);
  assert.match(script, /liblzma\.so\.5/u);
  assert.match(script, /python3\.bin/u);
  assert.match(script, /export LD_LIBRARY_PATH="\$self_dir\/\.\.\/lib"/u);
  assert.match(script, /--network[= ]none/u);
  assert.match(script, /\*disabled\*[\s\S]*_tkinter/u);
  assert.match(script, /HOST_UID/u);
  assert.match(script, /HOST_GID/u);
  assert.match(script, /trap[^\n]+EXIT/u);
  assert.match(script, /chown\s+-R/u);
  assert.ok(script.indexOf("trap restore_output_ownership EXIT") < script.indexOf("make -j1"));
});

test("Linux prune removes Tcl/Tk symlink fixtures before generic symlink materialization", async () => {
  const script = await readFile(new URL("./build-linux.sh", import.meta.url), "utf8");
  const symlinkPrune = script.match(/find \/out\/python -type l[^\n]+-delete/u)?.[0];
  assert.ok(symlinkPrune, "missing symlink-specific prune rooted at /out/python");
  for (const fixture of ["libtcl8.6.so", "libtk8.6.so"]) {
    const prefix = fixture.startsWith("libtcl") ? "libtcl" : "libtk";
    assert.match(symlinkPrune, new RegExp(`-iname '${prefix}\\*'`, "u"), fixture);
  }
  assert.ok(
    script.indexOf(symlinkPrune) < script.indexOf("while IFS= read -r -d '' link"),
    "Tcl/Tk symlinks must be deleted before symlink materialization",
  );
});

test("final layout rejects native Tk and Tcl/Tk runtime paths", async (t) => {
  for (const extra of [
    "python/DLLs/_tkinter.pyd",
    "python/lib/tcl8.6/init.tcl",
    "python/lib/tk8.6/tk.tcl",
    "python/lib/libtcl8.6.so",
    "python/lib/libtk8.6.so",
    "python/lib/python3.14/lib-tk/tkinter.py",
    "python/lib/python3.14/__pycache__/json.cpython-314.pyc",
  ]) {
    const root = await temporary(t, "coverage-bundle-tk-");
    await writeOutput(root);
    await mkdir(dirname(join(root, extra)), { recursive: true });
    await writeFile(join(root, extra), "forbidden");
    await assert.rejects(
      __testing.createResolvedManifest(root, "windows-x64", fixture().manifest),
      /forbidden bundle path/u,
      extra,
    );
  }
});

test("Linux Python invocation is isolated and Python-related environment is sanitized", () => {
  assert.deepEqual(
    __testing.pythonInvocationArguments("linux-x64", "/bundle/app/gcovr-runner.pyz", ["descriptor.json"]),
    ["-I", "-S", "/bundle/app/gcovr-runner.pyz", "descriptor.json"],
  );
  const clean = __testing.sanitizePythonEnvironment({
    PATH: "/trusted/bin",
    PYTHONPATH: "/hostile",
    PythonUserBase: "/hostile-user",
    PYTHONSTARTUP: "/hostile/startup.py",
    VIRTUAL_ENV: "/hostile-venv",
    CONDA_PREFIX: "/hostile-conda",
  });
  assert.deepEqual(clean, { PATH: "/trusted/bin" });
});

test("runner descriptor is closed and maps only fixed root/object/gcov/output fields", async () => {
  const contract = await readFile(new URL("./runner/contract.py", import.meta.url), "utf8");
  const main = await readFile(new URL("./runner/__main__.py", import.meta.url), "utf8");
  assert.match(contract, /schemaVersion/u);
  for (const field of ["root", "objectDirectory", "gcovExecutable", "outputPath"]) assert.match(contract, new RegExp(field, "u"));
  assert.match(contract, /set\([^\n]+\)\s*!=\s*|keys\(\)[^\n]+!=/u);
  assert.doesNotMatch(contract, /include|exclude/iu);
  assert.match(main, /sys\.executable/u);
  assert.match(main, /shell=False/u);
  assert.doesNotMatch(main, /shell=True|pip\s+install/iu);
  assert.match(main, /gcovr/u);
  assert.ok(contract.indexOf('"--json"') < contract.indexOf('descriptor["outputPath"]'));
  assert.ok(contract.indexOf('descriptor["outputPath"]') < contract.indexOf('"--json-pretty"'));
  assert.match(main, /"-I"[\s\S]*"-S"/u);
  assert.match(main, /PYTHON/iu);
});

test("real Python runner contract rejects malformed descriptors", async (t) => {
  const python = process.env.PYTHON ?? (process.platform === "win32" ? "python" : "python3");
  const probe = spawnSync(python, ["-c", "import sys; print(sys.version_info[:2])"], { encoding: "utf8" });
  if (probe.error) {
    if (probe.error.code === "ENOENT" || probe.error.code === "EPERM") {
      t.skip(`Python subprocess unavailable (${probe.error.code}): ${probe.error.message}`);
      return;
    }
    throw probe.error;
  }
  if (typeof probe.status !== "number") throw new Error(`Python probe did not return an exit status: ${probe.error ?? probe.stderr}`);
  if (probe.status !== 0) throw new Error(`Python probe failed: ${probe.stderr}`);
  const root = await temporary(t, "coverage-runner-contract-");
  const descriptorPath = join(root, "descriptor.json");
  const runnerDirectory = dirname(fileURLToPath(new URL("./runner/contract.py", import.meta.url)));
  const script = "import sys; from contract import load_descriptor; load_descriptor(sys.argv[1])";
  const valid = {
    schemaVersion: 1,
    root: join(root, "source"),
    objectDirectory: join(root, "objects"),
    gcovExecutable: join(root, "gcov.exe"),
    outputPath: join(root, "coverage.json"),
  };
  await writeFile(descriptorPath, JSON.stringify(valid));
  const accepted = spawnSync(python, ["-c", script, descriptorPath], { cwd: runnerDirectory, encoding: "utf8" });
  assert.equal(accepted.status, 0, accepted.stderr);
  for (const malformed of [
    JSON.stringify({ ...valid, extra: true }),
    `{"schemaVersion":1,"root":"${valid.root}","root":"${valid.root}","objectDirectory":"${valid.objectDirectory}","gcovExecutable":"${valid.gcovExecutable}","outputPath":"${valid.outputPath}"}`,
    JSON.stringify({ ...valid, schemaVersion: 2 }),
  ]) {
    await writeFile(descriptorPath, malformed);
    const rejected = spawnSync(python, ["-c", script, descriptorPath], { cwd: runnerDirectory, encoding: "utf8" });
    assert.equal(rejected.error, undefined, rejected.error?.message);
    assert.equal(typeof rejected.status, "number", "runner did not return an exit status");
    assert.notEqual(rejected.status, 0, `runner accepted malformed descriptor: ${malformed}`);
  }
});
