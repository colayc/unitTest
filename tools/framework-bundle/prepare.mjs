import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createWriteStream } from "node:fs";
import { lstat, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { pipeline } from "node:stream/promises";
import { Transform } from "node:stream";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const DIGEST = /^[0-9a-f]{64}$/u;
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const approved = {
  cpputest: { version: "4.0", license: "BSD-3-Clause", url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz", filename: "cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7", sourceDirectory: "cpputest-4.0" },
  unity: { version: "2.6.1", license: "MIT", url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz", filename: "Unity-2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292", sourceDirectory: "Unity-2.6.1" }
};
const downloadLimit = 64 * 1024 * 1024;

export function validateManifest(value) {
  closed(value, ["schemaVersion", "platform", "frameworks"], "framework manifest");
  if (value.schemaVersion !== 1 || value.platform !== "linux-x64" || !Array.isArray(value.frameworks) || value.frameworks.length !== 2) throw new Error("framework manifest has an invalid Linux identity");
  const ids = new Set();
  for (const entry of value.frameworks) {
    closed(entry, ["id", "version", "source", "license", "sourceDirectory"], "framework input");
    if (!(entry.id in approved) || ids.has(entry.id)) throw new Error("framework input has an invalid identity");
    const required = approved[entry.id];
    closed(entry.source, ["filename", "url", "sha256"], "framework input source");
    if (entry.version !== required.version || entry.license !== required.license || entry.source.url !== required.url || entry.source.filename !== required.filename || entry.source.sha256 !== required.sha256 || entry.sourceDirectory !== required.sourceDirectory || !safeFilename(entry.source.filename) || !DIGEST.test(entry.source.sha256) || !safeDirectory(entry.sourceDirectory)) {
      throw new Error("framework input is not locked");
    }
    ids.add(entry.id);
  }
  if (!ids.has("cpputest") || !ids.has("unity")) throw new Error("framework manifest omits a required input");
  return value;
}

export async function verifyLockedArchive(cacheRoot, input) {
  const root = absolute(cacheRoot, "framework cache root");
  const archive = join(root, `${input.source.sha256}-${input.source.filename}`);
  const metadata = await lstat(archive);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`framework archive is not a regular file: ${input.id}`);
  if (await sha256(archive) !== input.source.sha256) throw new Error(`framework archive digest mismatch: ${input.id}`);
  return archive;
}

export async function prepareFrameworkBundle(options = {}) {
  if (process.platform !== "linux") throw new Error("Linux framework bootstrap requires a Linux runner");
  const manifestPath = options.manifestPath ?? join(toolDirectory, "manifest.json");
  const cacheRoot = options.cacheRoot ?? join(repositoryRoot, ".superpowers", "cache", "framework-bundle");
  const outputRoot = options.outputRoot ?? join(repositoryRoot, ".superpowers", "runtime", "framework-bundle", "linux-x64");
  const manifest = validateManifest(JSON.parse(await readFile(manifestPath, "utf8")));
  await mkdir(cacheRoot, { recursive: true, mode: 0o700 });
  const archives = new Map();
  for (const input of manifest.frameworks) {
    try {
      archives.set(input.id, await verifyLockedArchive(cacheRoot, input));
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
      await downloadLockedArchive(cacheRoot, input);
      archives.set(input.id, await verifyLockedArchive(cacheRoot, input));
    }
  }
  const parent = dirname(outputRoot);
  await mkdir(parent, { recursive: true, mode: 0o700 });
  const staging = join(parent, `.framework-bundle-${process.pid}-${randomBytes(8).toString("hex")}`);
  await mkdir(staging, { mode: 0o700 });
  try {
    for (const input of manifest.frameworks) {
      const archive = archives.get(input.id);
      await verifySafeTar(archive);
      await execFile("tar", ["--no-same-owner", "--no-same-permissions", "-xzf", archive, "-C", staging], { shell: false, windowsHide: true, timeout: 120_000 });
      await verifySourceTree(staging, input);
    }
    const resolved = {
      schemaVersion: 1,
      platform: "linux-x64",
      frameworks: manifest.frameworks.map(({ id, version, source, license, sourceDirectory }) => ({ id, version, source: { filename: source.filename, sha256: source.sha256 }, license, sourceDirectory }))
    };
    await writeFile(join(staging, "manifest.resolved.json"), `${JSON.stringify(resolved, null, 2)}\n`, { flag: "wx", mode: 0o600 });
    await writeFile(join(staging, "READY"), "framework-bundle-v1\n", { flag: "wx", mode: 0o600 });
    await rm(outputRoot, { recursive: true, force: true });
    await rename(staging, outputRoot);
  } catch (error) {
    await rm(staging, { recursive: true, force: true });
    throw error;
  }
  return { root: outputRoot, manifest };
}

function closed(value, keys, label) {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) throw new Error(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} has unexpected fields`);
}

function safeFilename(value) { return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value); }
function safeDirectory(value) { return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value) && !value.includes(".."); }
function absolute(value, label) { if (typeof value !== "string" || value.includes("\0") || !isAbsolute(value)) throw new Error(`${label} must be an absolute path`); return resolve(value); }
async function sha256(path) { return createHash("sha256").update(await readFile(path)).digest("hex"); }

async function downloadLockedArchive(cacheRoot, input) {
  const target = join(cacheRoot, `${input.source.sha256}-${input.source.filename}`);
  const partial = join(cacheRoot, `.partial-${randomBytes(8).toString("hex")}`);
  try {
    const response = await fetch(input.source.url, { redirect: "follow", signal: AbortSignal.timeout(5 * 60 * 1000) });
    const final = new URL(response.url);
    if (!response.ok || !response.body || !["release-assets.githubusercontent.com", "codeload.github.com"].includes(final.hostname)) throw new Error(`framework archive download failed: ${input.id}`);
    let received = 0;
    const limit = new Transform({ transform(chunk, _encoding, callback) { received += chunk.length; callback(received > downloadLimit ? new Error("framework archive exceeds size limit") : null, chunk); } });
    await pipeline(response.body, limit, createWriteStream(partial, { flags: "wx", mode: 0o600 }));
    if (await sha256(partial) !== input.source.sha256) throw new Error(`framework archive digest mismatch: ${input.id}`);
    await rename(partial, target);
  } finally {
    await rm(partial, { force: true });
  }
}

async function verifySafeTar(archive) {
  const [verbose, listed] = await Promise.all([
    execFile("tar", ["-tvzf", archive], { shell: false, windowsHide: true, timeout: 60_000, maxBuffer: 8 * 1024 * 1024 }),
    execFile("tar", ["-tzf", archive], { shell: false, windowsHide: true, timeout: 60_000, maxBuffer: 8 * 1024 * 1024 })
  ]);
  const types = verbose.stdout.split(/\r?\n/u).filter(Boolean);
  const names = listed.stdout.split(/\r?\n/u).filter(Boolean);
  if (types.length === 0 || types.length !== names.length || types.some((entry) => !/^[d-]/u.test(entry) || entry.includes(" -> ")) || names.some((name) => !safeTarPath(name))) throw new Error("framework archive contains an unsafe entry");
}

function safeTarPath(name) {
  if (!name || /[\0\r\n\\]/u.test(name) || name.startsWith("/") || /^[A-Za-z]:/u.test(name)) return false;
  const normalized = posix.normalize(name.replace(/\/$/u, ""));
  return normalized !== "." && normalized === name.replace(/\/$/u, "") && !normalized.split("/").some((part) => part === ".." || part === "");
}

async function verifySourceTree(root, input) {
  const path = resolve(root, input.sourceDirectory);
  if (relative(root, path).startsWith("..")) throw new Error(`framework source escapes bootstrap root: ${input.id}`);
  const metadata = await lstat(path);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw new Error(`framework source root is invalid: ${input.id}`);
  const marker = input.id === "cpputest" ? join(path, "CMakeLists.txt") : join(path, "src", "unity.c");
  const markerMetadata = await lstat(marker);
  if (!markerMetadata.isFile() || markerMetadata.isSymbolicLink()) throw new Error(`framework source marker is invalid: ${input.id}`);
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  prepareFrameworkBundle().then((result) => process.stdout.write(`${JSON.stringify({ root: result.root, frameworks: result.manifest.frameworks.map((item) => item.id) })}\n`)).catch((error) => {
    process.stderr.write(`framework-bundle: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
