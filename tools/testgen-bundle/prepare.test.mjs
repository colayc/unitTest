import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import { parseArchiveListing, prepareBundle, publishCheckedBundle, validateArchiveEntries, validateSelectedArchiveEntries, validateSourceManifest, verifyExtractedSelection } from "./prepare.mjs";

async function trackedManifest() {
  return JSON.parse(await readFile(new URL("./manifest.json", import.meta.url), "utf8"));
}

test("preparation rejects mutable URLs, cross-platform identity, and missing license inventory", async () => {
  const manifest = await trackedManifest();
  const mutable = structuredClone(manifest);
  mutable.platforms["windows-x64"].archive.url = "https://github.com/llvm/llvm-project/releases/latest/download/clang.tar.xz";
  assert.throws(() => validateSourceManifest(mutable), /URL|archive/u);

  const mixed = structuredClone(manifest);
  mixed.platforms["windows-x64"].target = "linux-x64";
  assert.throws(() => validateSourceManifest(mixed), /pin|platform|target/u);

  const unlicensed = structuredClone(manifest);
  unlicensed.platforms["linux-x64"].licenses = [];
  assert.throws(() => validateSourceManifest(unlicensed), /pin|license/u);
});

test("Node refuses any selected-file or required-license change that the Go product pin would reject", async () => {
  const manifest = await trackedManifest();
  const removedLicense = structuredClone(manifest);
  removedLicense.platforms["windows-x64"].licenses = removedLicense.platforms["windows-x64"].licenses.filter(
    ({ path }) => path !== "licenses/compiler-rt/LICENSE.TXT",
  );
  assert.throws(() => validateSourceManifest(removedLicense), /pin|digest|inventory|license/u);

  const changedResource = structuredClone(manifest);
  changedResource.platforms["linux-x64"].files[1].sha256 = "0".repeat(64);
  assert.throws(() => validateSourceManifest(changedResource), /pin|digest|inventory/u);

  const sourceBytes = await readFile(new URL("./manifest.json", import.meta.url));
  assert.throws(() => validateSourceManifest(manifest, Buffer.concat([sourceBytes, Buffer.from("\n")])), /pin/u);
});

test("preparation rejects a wrong archive digest without publishing a cache", async (t) => {
  const cacheRoot = await mkdtemp(join(tmpdir(), "testgen-prepare-test-"));
  t.after(() => rm(cacheRoot, { recursive: true, force: true }));
  const manifest = await trackedManifest();
  const progress = [];
  await assert.rejects(
    () => prepareBundle({
      manifest,
      platform: "windows-x64",
      cacheRoot,
      onProgress: (stage) => progress.push(stage),
      downloadArchive: async (_url, destination) => {
        await writeFile(destination, "wrong archive bytes\n");
      },
    }),
    /SHA-256|digest/u,
  );
  assert.deepEqual(await readdir(cacheRoot), []);
  assert.deepEqual(progress, ["download-archive", "verify-archive"]);
  assert.notEqual(
    createHash("sha256").update("wrong archive bytes\n").digest("hex"),
    manifest.platforms["windows-x64"].archive.sha256,
  );
});

test("preparation rebuilds instead of returning a corrupt existing cache error", async (t) => {
  const cacheRoot = await mkdtemp(join(tmpdir(), "testgen-corrupt-cache-"));
  t.after(() => rm(cacheRoot, { recursive: true, force: true }));
  await mkdir(join(cacheRoot, "windows-x64"));
  await writeFile(join(cacheRoot, "windows-x64", "manifest.json"), "corrupt");
  const progress = [];
  await assert.rejects(
    () => prepareBundle({
      platform: "windows-x64",
      cacheRoot,
      onProgress: (stage) => progress.push(stage),
      downloadArchive: async () => { throw new Error("rebuild attempted"); },
    }),
    /rebuild attempted/u,
  );
  assert.deepEqual(progress, ["verify-cache", "download-archive"]);
});

test("checked publication replaces a corrupt cache with a verified candidate", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "testgen-publish-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const finalRoot = join(root, "windows-x64");
  const candidateRoot = join(root, ".prepare", "bundle");
  await mkdir(candidateRoot, { recursive: true });
  await mkdir(finalRoot);
  await writeFile(join(finalRoot, "value"), "corrupt");
  await writeFile(join(candidateRoot, "value"), "verified");
  const verify = async (path) => {
    const value = await readFile(join(path, "value"), "utf8");
    if (value !== "verified") throw new Error("corrupt cache");
    return value;
  };
  assert.equal(await publishCheckedBundle({ candidateRoot, finalRoot, temporaryRoot: dirname(candidateRoot), verify }), "verified");
  assert.equal(await readFile(join(finalRoot, "value"), "utf8"), "verified");
  assert.deepEqual(await readdir(dirname(candidateRoot)), []);
});

test("failed post-publication verification restores the previous cache", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "testgen-rollback-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const finalRoot = join(root, "windows-x64");
  const temporaryRoot = join(root, ".prepare");
  const candidateRoot = join(temporaryRoot, "bundle");
  await mkdir(candidateRoot, { recursive: true });
  await mkdir(finalRoot);
  await writeFile(join(finalRoot, "value"), "corrupt");
  await writeFile(join(candidateRoot, "value"), "verified");
  const verify = async (path) => {
    if (path === finalRoot) throw new Error("post-publication verification failed");
    return await readFile(join(path, "value"), "utf8");
  };
  await assert.rejects(
    () => publishCheckedBundle({ candidateRoot, finalRoot, temporaryRoot, verify }),
    /post-publication verification failed/u,
  );
  assert.equal(await readFile(join(finalRoot, "value"), "utf8"), "corrupt");
  assert.equal(await readFile(join(candidateRoot, "value"), "utf8"), "verified");
});

test("preparation rejects traversal and case-alias archive entries before extraction", () => {
  assert.throws(
    () => validateArchiveEntries(["LLVM-22.1.8-Linux-X64/bin/clang-22", "../outside"]),
    /traversal|unsafe/u,
  );
  assert.throws(
    () => validateArchiveEntries(["LLVM-22.1.8-Linux-X64/bin/clang-22", "/absolute/path"]),
    /absolute|unsafe/u,
  );
  assert.throws(
    () => validateArchiveEntries(["root/lib/clang/22/include/Name.h", "root/lib/clang/22/include/name.h"]),
    /case|alias/u,
  );
});

test("archive inspection rejects nested selected links and unlisted selected files before extraction", () => {
  const spec = {
    archiveRoot: "root",
    executable: "bin/clang.exe",
    resourceDir: "lib/clang/22",
    files: [
      { path: "bin/clang.exe" },
      { path: "lib/clang/22/include/stddef.h" },
    ],
  };
  const entries = [
    { path: "root/", type: "d" },
    { path: "root/bin/", type: "d" },
    { path: "root/bin/clang.exe", type: "-" },
    { path: "root/lib/", type: "d" },
    { path: "root/lib/clang/", type: "d" },
    { path: "root/lib/clang/22/", type: "d" },
    { path: "root/lib/clang/22/include/", type: "d" },
    { path: "root/lib/clang/22/include/stddef.h", type: "-" },
  ];
  assert.doesNotThrow(() => validateSelectedArchiveEntries(entries, spec));
  assert.throws(() => validateSelectedArchiveEntries([
    ...entries,
    { path: "root/lib/clang/22/include/nested.h", type: "l", target: "../../escape" },
  ], spec), /link|symlink|type/u);
  assert.throws(() => validateSelectedArchiveEntries([
    ...entries,
    { path: "root/lib/clang/22/include/unlisted.h", type: "-" },
  ], spec), /unlisted|extra|inventory/u);
  assert.throws(() => validateSelectedArchiveEntries([
    { path: "root/lib/clang/", type: "l", target: "../../escape" },
    ...entries.filter(({ path }) => path !== "root/lib/clang/"),
  ], spec), /link|symlink|type/u);
});

test("archive listing parser accepts GNU tar and bsdtar metadata layouts", () => {
  const spec = {
    archiveRoot: "clang+llvm-22.1.8-x86_64-pc-windows-msvc",
    executable: "bin/clang.exe",
    resourceDir: "lib/clang/22",
    files: [{ path: "bin/clang.exe" }],
  };
  const gnu = "-rwxr-xr-x runner/runner 123 2026-01-01 00:00 clang+llvm-22.1.8-x86_64-pc-windows-msvc/bin/clang.exe";
  const bsd = "-rwxr-xr-x  1000  1000  123 Jan 01 00:00:00 2026 clang+llvm-22.1.8-x86_64-pc-windows-msvc/bin/clang.exe";
  assert.equal(parseArchiveListing(`${gnu}\n`, spec)[0].path, `${spec.archiveRoot}/bin/clang.exe`);
  assert.equal(parseArchiveListing(`${bsd}\n`, spec)[0].path, `${spec.archiveRoot}/bin/clang.exe`);
});

test("post-extraction inspection rejects an unlisted nested selected file", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "testgen-extract-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const spec = {
    archiveRoot: "root",
    executable: "bin/clang.exe",
    resourceDir: "lib/clang/22",
    files: [
      { path: "bin/clang.exe" },
      { path: "lib/clang/22/include/stddef.h" },
    ],
  };
  const clang = join(root, "root", "bin", "clang.exe");
  const header = join(root, "root", "lib", "clang", "22", "include", "stddef.h");
  await mkdir(dirname(clang), { recursive: true });
  await mkdir(dirname(header), { recursive: true });
  await writeFile(clang, "clang");
  await writeFile(header, "header");
  await verifyExtractedSelection(root, spec);
  await writeFile(join(dirname(header), "unlisted.h"), "extra");
  await assert.rejects(() => verifyExtractedSelection(root, spec), /unlisted|extra|inventory/u);
});
