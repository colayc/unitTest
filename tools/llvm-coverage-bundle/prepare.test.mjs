import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { lstat, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { mkdtemp } from "node:fs/promises";
import test from "node:test";

import { prepareBundle } from "./prepare.mjs";

const source = new URL("./", import.meta.url);

function digest(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function fixture() {
  const manifest = JSON.parse(await readFile(new URL("manifest.json", source), "utf8"));
  const archiveBytes = Buffer.from("archive-bytes");
  const licenseBytes = Buffer.from("license");
  manifest.archive.sha256 = digest(archiveBytes);
  manifest.license.sha256 = digest(licenseBytes);
  manifest.license.size = licenseBytes.length;
  const manifestBytes = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
  return { manifest, manifestBytes, archiveBytes, licenseBytes };
}

async function fakeExtract({ manifest, destination, licenseBytes, versioned = true, omit = [] }) {
  const root = join(destination, manifest.archiveRoot);
  await mkdir(join(root, "bin"), { recursive: true });
  await mkdir(join(root, "lib"), { recursive: true });
  const names = versioned
    ? ["clang-22", "clang++-22", "llvm-profdata-22", "llvm-cov-22"]
    : ["clang", "clang++", "llvm-profdata", "llvm-cov"];
  for (const name of names) if (!omit.includes(name)) await writeFile(join(root, "bin", name), `${name}\n`);
  await writeFile(join(root, "LICENSE.TXT"), licenseBytes);
  return root;
}

test("prepareBundle publishes canonical regular tool entry points and READY", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "utide-llvm-prepare-"));
  const { manifest, manifestBytes, archiveBytes, licenseBytes } = await fixture();
  const finalRoot = join(root, "bundle");
  const result = await prepareBundle({
    manifest,
    manifestBytes,
    outputRoot: finalRoot,
    downloadArchive: async (_url, destination) => writeFile(destination, archiveBytes),
    extractArchive: (archivePath, destination) => fakeExtract({ archivePath, destination, manifest, licenseBytes }),
  });
  assert.equal(result.root, finalRoot);
  for (const path of Object.values(manifest.tools)) {
    const info = await lstat(join(finalRoot, path));
    assert.equal(info.isFile(), true, path);
    assert.equal(info.isSymbolicLink(), false, path);
  }
  const ready = JSON.parse(await readFile(join(finalRoot, "READY"), "utf8"));
  assert.equal(ready.schemaVersion, 1);
  assert.equal(ready.platform, "linux-x64");
  assert.equal(ready.manifestSha256, digest(manifestBytes));
});

test("prepareBundle rejects a wrong archive digest without publishing READY", async () => {
  const root = await mkdtemp(join(tmpdir(), "utide-llvm-digest-"));
  const { manifest, manifestBytes } = await fixture();
  const finalRoot = join(root, "bundle");
  await assert.rejects(
    prepareBundle({
      manifest,
      manifestBytes,
      outputRoot: finalRoot,
      downloadArchive: async (_url, destination) => writeFile(destination, "tampered"),
      extractArchive: async () => { throw new Error("must not extract a bad archive"); },
    }),
    /archive SHA-256 digest mismatch/u,
  );
  await assert.rejects(lstat(join(finalRoot, "READY")), { code: "ENOENT" });
});

test("prepareBundle rejects a missing required tool without publishing final output", async () => {
  const root = await mkdtemp(join(tmpdir(), "utide-llvm-missing-"));
  const { manifest, manifestBytes, archiveBytes, licenseBytes } = await fixture();
  const finalRoot = join(root, "bundle");
  await assert.rejects(
    prepareBundle({
      manifest,
      manifestBytes,
      outputRoot: finalRoot,
      downloadArchive: async (_url, destination) => writeFile(destination, archiveBytes),
      extractArchive: (_archivePath, destination) => fakeExtract({ manifest, destination, licenseBytes, omit: ["llvm-cov-22"] }),
    }),
    /required LLVM tool/u,
  );
  await assert.rejects(lstat(join(finalRoot, "READY")), { code: "ENOENT" });
});
