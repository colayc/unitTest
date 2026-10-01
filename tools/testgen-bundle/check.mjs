import { createHash } from "node:crypto";
import { createReadStream, readFileSync } from "node:fs";
import { lstat, readFile, readdir } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";

const toolDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDir, "..", "..");
const manifestPath = join(toolDir, "manifest.json");
const schema = JSON.parse(readFileSync(join(toolDir, "manifest.schema.json"), "utf8"));
const validateSchema = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
const archiveHashes = {
  "windows-x64": "d96c2cc1736f4eb7fa43cb9bbdf56d93551a9ae0a9aadb9c99c3c3b2b712a234",
  "linux-x64": "df0e1ecf16caf3489a272a5eea4eec9b0d82878f6477fa309504f918a0006384",
};
const sourceCommit = "ca7933e47d3a3451d81e72ac174dcb5aa28b59d1";
// Also compiled into testgenbundle.Open; Node must never stage a tree Go rejects.
const trustedManifestSHA256 = "e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345";
const expectedArchive = {
  "windows-x64": "clang+llvm-22.1.8-x86_64-pc-windows-msvc.tar.xz",
  "linux-x64": "LLVM-22.1.8-Linux-X64.tar.xz",
};
const digestPattern = /^[0-9a-f]{64}$/u;
const pathPattern = /^[A-Za-z0-9][A-Za-z0-9._+/-]*$/u;

function safeRelativePath(path) {
  return typeof path === "string" && pathPattern.test(path) &&
    !path.split("/").some((part) => part === "" || part === "." || part === "..") &&
    !path.startsWith("/") && !/^[A-Za-z]:/u.test(path);
}

function ensureUniquePaths(paths, label) {
  const seen = new Set();
  for (const path of paths) {
    if (!safeRelativePath(path)) throw new Error(`${label} has unsafe path: ${path}`);
    const folded = path.toLowerCase();
    if (seen.has(folded)) throw new Error(`${label} has duplicate or case-alias path: ${path}`);
    seen.add(folded);
  }
}

function expectedFileMap(platform) {
  const files = [...platform.files, ...platform.licenses];
  ensureUniquePaths(files.map(({ path }) => path), "bundle inventory");
  const result = new Map();
  for (const file of files) {
    if (!digestPattern.test(file.sha256) || !Number.isSafeInteger(file.size) || file.size < 0) {
      throw new Error(`invalid inventory digest or size: ${file.path}`);
    }
    result.set(file.path, file);
  }
  return result;
}

function expectedDirectories(paths) {
  const result = new Set();
  for (const path of paths) {
    const parts = path.split("/");
    for (let count = 1; count < parts.length; count += 1) {
      result.add(parts.slice(0, count).join("/"));
    }
  }
  return result;
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

export function validateSourceManifest(manifest, manifestBytes = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`)) {
  if (!validateSchema(manifest)) {
    throw new Error(`invalid Clang source manifest: ${JSON.stringify(validateSchema.errors)}`);
  }
  if (createHash("sha256").update(manifestBytes).digest("hex") !== trustedManifestSHA256 ||
      JSON.stringify(JSON.parse(manifestBytes.toString("utf8"))) !== JSON.stringify(manifest)) {
    throw new Error("Clang source manifest does not match the product inventory pin");
  }
  if (manifest.clangVersion !== "22.1.8" || manifest.sourceCommit !== sourceCommit) {
    throw new Error("unreviewed Clang version or source commit");
  }
  for (const key of ["windows-x64", "linux-x64"]) {
    const platform = manifest.platforms[key];
    const archive = platform.archive;
    const encodedFilename = expectedArchive[key].replace("+", "%2B");
    if (platform.target !== key || platform.archiveRoot !== expectedArchive[key].replace(/\.tar\.xz$/u, "") ||
        archive.filename !== expectedArchive[key] || archive.sha256 !== archiveHashes[key] ||
        archive.url !== `https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/${encodedFilename}`) {
      throw new Error(`unreviewed ${key} platform or archive identity`);
    }
    if (platform.executable !== (key === "windows-x64" ? "bin/clang.exe" : "bin/clang-22") ||
        platform.resourceDir !== "lib/clang/22") {
      throw new Error(`unreviewed ${key} executable or resource directory`);
    }
    const files = expectedFileMap(platform);
    if (!files.has(platform.executable) ||
        ![...files.keys()].some((path) => path.startsWith(`${platform.resourceDir}/include/`))) {
      throw new Error(`${key} executable or resource directory is not inventoried`);
    }
    if (platform.files.some(({ path }) => path !== platform.executable &&
        !path.startsWith(`${platform.resourceDir}/include/`))) {
      throw new Error(`${key} has unreviewed archive file selection`);
    }
    for (const license of platform.licenses) {
      if (!license.path.startsWith("licenses/") || !license.url.startsWith(`https://raw.githubusercontent.com/llvm/llvm-project/${sourceCommit}/`)) {
        throw new Error(`${key} has unreviewed license source`);
      }
    }
    if (new Set(platform.licenses.map(({ path }) => path)).size !== platform.licenses.length) {
      throw new Error(`${key} has duplicate license paths`);
    }
  }
  return manifest;
}

export async function verifyFileInventory(root, platform, { allowedMetadata = [] } = {}) {
  const resolvedRoot = resolve(root);
  const rootInfo = await lstat(resolvedRoot);
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) {
    throw new Error("bundle root must be a direct directory, not a reparse point");
  }
  const expected = expectedFileMap(platform);
  const expectedDirs = expectedDirectories(expected.keys());
  const actualFiles = new Map();
  const actualDirs = new Set();
  const metadata = new Set(allowedMetadata);
  async function walk(directory, prefix) {
    const names = await readdir(directory);
    names.sort();
    for (const name of names) {
      const relativePath = prefix ? `${prefix}/${name}` : name;
      if (!safeRelativePath(relativePath)) throw new Error(`unsafe bundle path: ${relativePath}`);
      const absolutePath = join(directory, name);
      const info = await lstat(absolutePath);
      if (info.isSymbolicLink()) throw new Error(`symbolic link or reparse point in bundle: ${relativePath}`);
      if (info.isDirectory()) {
        actualDirs.add(relativePath);
        if (!expectedDirs.has(relativePath)) throw new Error(`extra bundle directory: ${relativePath}`);
        await walk(absolutePath, relativePath);
      } else if (info.isFile()) {
        if (!expected.has(relativePath) && !metadata.has(relativePath)) {
          throw new Error(`extra unlisted bundle file: ${relativePath}`);
        }
        actualFiles.set(relativePath, info);
      } else {
        throw new Error(`unsupported bundle entry: ${relativePath}`);
      }
    }
  }
  await walk(resolvedRoot, "");
  for (const directory of expectedDirs) {
    if (!actualDirs.has(directory)) throw new Error(`missing bundle directory: ${directory}`);
  }
  for (const [path, record] of expected) {
    const info = actualFiles.get(path);
    if (!info) throw new Error(`missing bundle file or license: ${path}`);
    if (info.size !== record.size || await sha256File(join(resolvedRoot, ...path.split("/"))) !== record.sha256) {
      throw new Error(`bundle file SHA-256 digest mismatch: ${path}`);
    }
  }
  return true;
}

export async function checkBundle({ root, platform, manifest, manifestBytes } = {}) {
  const key = platform ?? (process.platform === "win32" ? "windows-x64" : process.platform === "linux" ? "linux-x64" : "");
  if (!archiveHashes[key]) throw new Error("unsupported Clang bundle platform");
  const sourceBytes = manifestBytes ?? (manifest ? Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`) : await readFile(manifestPath));
  const sourceManifest = validateSourceManifest(manifest ?? JSON.parse(sourceBytes.toString("utf8")), sourceBytes);
  const resolvedRoot = resolve(root ?? join(repositoryRoot, ".superpowers", "cache", "testgen-bundle", sourceManifest.clangVersion, key));
  const preparedManifest = await readFile(join(resolvedRoot, "manifest.json"));
  if (!preparedManifest.equals(sourceBytes)) throw new Error("prepared manifest changed from product source manifest");
  const manifestSha256 = createHash("sha256").update(sourceBytes).digest("hex");
  const ready = JSON.parse(await readFile(join(resolvedRoot, "READY"), "utf8"));
  if (ready.schemaVersion !== 1 || ready.platform !== key || ready.manifestSha256 !== manifestSha256 || Object.keys(ready).length !== 3) {
    throw new Error("prepared Clang bundle READY identity mismatch");
  }
  await verifyFileInventory(resolvedRoot, sourceManifest.platforms[key], { allowedMetadata: ["manifest.json", "READY"] });
  return {
    root: resolvedRoot,
    platform: key,
    clangVersion: sourceManifest.clangVersion,
    manifestSha256,
    clangPath: join(resolvedRoot, ...sourceManifest.platforms[key].executable.split("/")),
    resourceDir: join(resolvedRoot, ...sourceManifest.platforms[key].resourceDir.split("/")),
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const rootFlag = args.indexOf("--root");
  const platformFlag = args.indexOf("--platform");
  if (args.some((arg, index) => arg.startsWith("--") && ![rootFlag, platformFlag].includes(index)) ||
      (rootFlag >= 0 && !args[rootFlag + 1]) || (platformFlag >= 0 && !args[platformFlag + 1])) {
    process.stderr.write("Usage: node check.mjs [--root <prepared-root>] [--platform <windows-x64|linux-x64>]\n");
    process.exitCode = 1;
  } else {
    checkBundle({ root: rootFlag >= 0 ? args[rootFlag + 1] : undefined, platform: platformFlag >= 0 ? args[platformFlag + 1] : undefined })
      .then(({ root }) => process.stdout.write(`${root}\n`))
      .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
  }
}
