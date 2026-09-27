import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { prepareBundle, validateArchiveEntries, validateSourceManifest } from "./prepare.mjs";

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
  assert.throws(() => validateSourceManifest(mixed), /platform|target/u);

  const unlicensed = structuredClone(manifest);
  unlicensed.platforms["linux-x64"].licenses = [];
  assert.throws(() => validateSourceManifest(unlicensed), /license/u);
});

test("preparation rejects a wrong archive digest without publishing a cache", async (t) => {
  const cacheRoot = await mkdtemp(join(tmpdir(), "testgen-prepare-test-"));
  t.after(() => rm(cacheRoot, { recursive: true, force: true }));
  const manifest = await trackedManifest();
  await assert.rejects(
    () => prepareBundle({
      manifest,
      platform: "windows-x64",
      cacheRoot,
      downloadArchive: async (_url, destination) => {
        await writeFile(destination, "wrong archive bytes\n");
      },
    }),
    /SHA-256|digest/u,
  );
  assert.deepEqual(await readdir(cacheRoot), []);
  assert.notEqual(
    createHash("sha256").update("wrong archive bytes\n").digest("hex"),
    manifest.platforms["windows-x64"].archive.sha256,
  );
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
