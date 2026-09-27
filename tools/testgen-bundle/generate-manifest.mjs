// Maintainer-only manifest generation from independently downloaded and verified upstream bytes.
// This script does not download artifacts or prepare a runnable bundle.
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, readFile, readdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const version = "22.1.8";
const sourceCommit = "ca7933e47d3a3451d81e72ac174dcb5aa28b59d1";
const provenance = {
  "windows-x64": {
    archiveRoot: "clang+llvm-22.1.8-x86_64-pc-windows-msvc",
    archive: {
      filename: "clang+llvm-22.1.8-x86_64-pc-windows-msvc.tar.xz",
      url: "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/clang%2Bllvm-22.1.8-x86_64-pc-windows-msvc.tar.xz",
      sha256: "d96c2cc1736f4eb7fa43cb9bbdf56d93551a9ae0a9aadb9c99c3c3b2b712a234",
    },
    executable: "bin/clang.exe",
  },
  "linux-x64": {
    archiveRoot: "LLVM-22.1.8-Linux-X64",
    archive: {
      filename: "LLVM-22.1.8-Linux-X64.tar.xz",
      url: "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/LLVM-22.1.8-Linux-X64.tar.xz",
      sha256: "df0e1ecf16caf3489a272a5eea4eec9b0d82878f6477fa309504f918a0006384",
    },
    executable: "bin/clang-22",
  },
};
const licenseNames = [
  ["LICENSE.TXT", "licenses/llvm/LICENSE.TXT", "LICENSE.TXT", "8d85c1057d742e597985c7d4e6320b015a9139385cff4cbae06ffc0ebe89afee"],
  ["clang-LICENSE.TXT", "licenses/clang/LICENSE.TXT", "clang/LICENSE.TXT", "ebcd9bbf783a73d05c53ba4d586b8d5813dcdf3bbec50265860ccc885e606f47"],
  ["compiler-rt-LICENSE.TXT", "licenses/compiler-rt/LICENSE.TXT", "compiler-rt/LICENSE.TXT", "1a8f1058753f1ba890de984e48f0242a3a5c29a6a8f2ed9fd813f36985387e8d"],
];

async function sha256(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function fileRecord(root, relativePath) {
  const absolute = join(root, ...relativePath.split("/"));
  const info = await lstat(absolute);
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`not a direct regular file: ${relativePath}`);
  return { path: relativePath, sha256: await sha256(absolute), size: info.size };
}

async function resourceFiles(root) {
  const prefix = "lib/clang/22/include";
  const results = [];
  async function walk(relativePath) {
    const absolute = join(root, ...relativePath.split("/"));
    const info = await lstat(absolute);
    if (info.isSymbolicLink()) throw new Error(`link in resource directory: ${relativePath}`);
    if (info.isFile()) {
      results.push(await fileRecord(root, relativePath));
      return;
    }
    if (!info.isDirectory()) throw new Error(`unsupported resource entry: ${relativePath}`);
    const names = await readdir(absolute);
    names.sort();
    for (const name of names) await walk(`${relativePath}/${name}`);
  }
  await walk(prefix);
  return results;
}

async function main() {
  const archiveCache = resolve(process.argv[2] ?? "");
  const windowsExtracted = resolve(process.argv[3] ?? "");
  const linuxExtracted = resolve(process.argv[4] ?? "");
  if (process.argv.length !== 5) {
    throw new Error("Usage: node generate-manifest.mjs <archive-cache> <windows-extracted> <linux-extracted>");
  }
  const extracted = { "windows-x64": windowsExtracted, "linux-x64": linuxExtracted };
  const platforms = {};
  for (const key of ["windows-x64", "linux-x64"]) {
    const config = provenance[key];
    const archivePath = join(archiveCache, config.archive.filename);
    if (await sha256(archivePath) !== config.archive.sha256) {
      throw new Error(`${key} archive SHA-256 does not match the official release digest`);
    }
    const root = join(extracted[key], config.archiveRoot);
    const files = [await fileRecord(root, config.executable), ...await resourceFiles(root)];
    files.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
    const licenses = [];
    for (const [name, path, sourcePath, expectedDigest] of licenseNames) {
      const bytes = await readFile(join(archiveCache, name));
      const digest = createHash("sha256").update(bytes).digest("hex");
      if (digest !== expectedDigest) throw new Error(`upstream license digest mismatch: ${sourcePath}`);
      licenses.push({
        path,
        url: `https://raw.githubusercontent.com/llvm/llvm-project/${sourceCommit}/${sourcePath}`,
        sha256: digest,
        size: bytes.length,
      });
    }
    platforms[key] = {
      archive: config.archive,
      archiveRoot: config.archiveRoot,
      target: key,
      executable: config.executable,
      resourceDir: "lib/clang/22",
      files,
      licenses,
    };
  }
  const manifest = { schemaVersion: 1, clangVersion: version, sourceCommit, platforms };
  await writeFile(new URL("./manifest.json", import.meta.url), `${JSON.stringify(manifest, null, 2)}\n`, { flag: "wx" });
  process.stdout.write(`locked ${platforms["windows-x64"].files.length} Windows and ${platforms["linux-x64"].files.length} Linux files\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
