import { execFile as execFileCallback } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createWriteStream, createReadStream } from "node:fs";
import { lstat, mkdir, open, readdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { pipeline } from "node:stream/promises";
import { Transform } from "node:stream";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { directoryDigest, frameworkFailure, readFrameworkManifest, sha256File } from "./manifest.mjs";

const execFile = promisify(execFileCallback);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const archiveLimit = 64 * 1024 * 1024;
const maxEntries = 8192, maxDepth = 32, maxExpanded = 256 * 1024 * 1024;
const windowsReserved = /^(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?$/iu;

function failure(code, message, cause) { return frameworkFailure(code, message, cause); }
function randomName(prefix) { return `${prefix}${process.pid}-${randomBytes(10).toString("hex")}`; }
function validPath(path) {
  if (typeof path !== "string" || path.length === 0 || path.includes("\\") || path.includes("\0") || path.includes("\r") || path.includes("\n") || path.startsWith("/") || /^[A-Za-z]:/u.test(path)) return false;
  const parts = path.replace(/\/$/u, "").split("/");
  return parts.length > 0 && parts.every((part) => part && part !== "." && part !== ".." && !windowsReserved.test(part));
}

export function validateArchiveEntries(entries, input) {
  if (!Array.isArray(entries) || entries.length === 0 || entries.length > maxEntries) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive entry count is invalid");
  let total = 0; const seen = new Set(); const root = `${input.sourceDirectory}/`;
  for (const entry of entries) {
    if (!entry || typeof entry !== "object" || !validPath(entry.path) || (entry.type !== "file" && entry.type !== "directory") || !Number.isSafeInteger(entry.size) || entry.size < 0) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive contains an unsafe entry");
    const normalized = entry.path.replace(/\/$/u, "");
    if ((normalized !== input.sourceDirectory && !entry.path.startsWith(root)) || (normalized === input.sourceDirectory && entry.type !== "directory") || seen.has(normalized)) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive path escapes or duplicates its source root");
    seen.add(normalized); const depth = normalized.split("/").length;
    if (depth > maxDepth) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive path exceeds depth limit");
    total += entry.size; if (!Number.isSafeInteger(total) || total > maxExpanded) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive exceeds expanded-byte limit");
  }
  return entries;
}

function tarExecutable() { return process.platform === "win32" ? join(process.env.SystemRoot ?? "C:\\Windows", "System32", "tar.exe") : "/usr/bin/tar"; }
async function inspectArchive(archive) {
  let tar = tarExecutable();
  try { await lstat(tar); } catch { if (process.platform !== "win32") tar = "/bin/tar"; }
  const options = { shell: false, windowsHide: true, timeout: 60_000, maxBuffer: 8 * 1024 * 1024, env: { ...process.env, LANG: "C", LC_ALL: "C" } };
  const [names, verbose] = await Promise.all([execFile(tar, ["-tf", archive], options), execFile(tar, ["-tvf", archive], options)]);
  const listed = names.stdout.split(/\r?\n/u).filter(Boolean), lines = verbose.stdout.split(/\r?\n/u).filter(Boolean);
  if (listed.length !== lines.length) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive listings disagree");
  return listed.map((path, index) => {
    const line = lines[index]; const marker = line[0];
    if (marker !== "-" && marker !== "d") throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive has a non-regular entry");
    const fields = line.trim().split(/\s+/u); const date = fields.findIndex((part) => /^\d{4}-\d\d-\d\d$/u.test(part) || /^[A-Z][a-z]{2}$/u.test(part));
    if (date < 1 || !/^\d+$/u.test(fields[date - 1])) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive has an unparseable entry size");
    const size = Number(fields[date - 1]); if (!Number.isSafeInteger(size)) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "archive has an invalid entry size");
    return { path, type: marker === "d" ? "directory" : "file", size };
  });
}
async function extractArchive(archive, staging) {
  let tar = tarExecutable(); try { await lstat(tar); } catch { if (process.platform !== "win32") tar = "/bin/tar"; }
  await execFile(tar, ["-xf", archive, "-C", staging], { shell: false, windowsHide: true, timeout: 120_000, maxBuffer: 8 * 1024 * 1024, env: { ...process.env, LANG: "C", LC_ALL: "C" } });
}
async function fsyncFile(path) { const handle = await open(path, "r"); try { await handle.sync(); } finally { await handle.close(); } }
function trustedUrl(url, initial = false) {
  let parsed; try { parsed = new URL(url); } catch { throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive URL is invalid"); }
  const host = parsed.hostname.toLowerCase();
  if (parsed.protocol !== "https:" || (initial ? host !== "github.com" : !(host === "github.com" || host === "release-assets.githubusercontent.com" || host === "codeload.github.com"))) throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive URL is outside approved GitHub hosts");
  return parsed;
}
async function defaultDownload(input, target) {
  trustedUrl(input.source.url, true); let url = input.source.url;
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    const response = await fetch(url, { redirect: "manual", signal: AbortSignal.timeout(5 * 60 * 1000) });
    if (response.status >= 300 && response.status < 400) { const location = response.headers.get("location"); if (!location || redirects === 5) throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive redirect is invalid"); url = new URL(location, url).href; trustedUrl(url); continue; }
    if (!response.ok || !response.body) throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive download failed"); trustedUrl(response.url); let bytes = 0;
    const limited = new Transform({ transform(chunk, _encoding, done) { bytes += chunk.length; done(bytes > archiveLimit ? failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive exceeds response limit") : null, chunk); } });
    await pipeline(response.body, limited, createWriteStream(target, { flags: "wx", mode: 0o600 })); await fsyncFile(target); return;
  }
}
export async function verifyLockedArchive(cacheRoot, input) {
  const archive = join(cacheRoot, `${input.source.sha256}-${input.source.filename}`); let data;
  try { data = await lstat(archive); } catch (error) { if (error?.code === "ENOENT") throw error; throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", "archive cache cannot be inspected", error); }
  if (!data.isFile() || data.isSymbolicLink() || await sha256File(archive) !== input.source.sha256) throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", `archive cache identity mismatch: ${input.id}`);
  return archive;
}
async function ensureArchive(cacheRoot, input, download) {
  try { return await verifyLockedArchive(cacheRoot, input); } catch (error) { if (error?.code !== "ENOENT") throw error; }
  const target = join(cacheRoot, `${input.source.sha256}-${input.source.filename}`), partial = join(cacheRoot, randomName(".partial-"));
  try { await download(input, partial); if (await sha256File(partial) !== input.source.sha256) throw failure("FRAMEWORK_ARCHIVE_UNTRUSTED", `downloaded archive identity mismatch: ${input.id}`); try { await rename(partial, target); } catch (error) { if (error?.code !== "EEXIST" && error?.code !== "EPERM") throw error; } return await verifyLockedArchive(cacheRoot, input); } finally { await rm(partial, { force: true }); }
}
async function requiredStat(path, code, message) {
  try { return await lstat(path); } catch (error) { if (error?.code === "ENOENT") throw failure(code, message, error); throw error; }
}
async function assertNoSymlinkComponents(path) {
  const resolved = resolve(path); const components = [];
  for (let current = resolved; dirname(current) !== current; current = dirname(current)) components.push(current);
  for (const component of components.reverse()) {
    try { const stat = await lstat(component); if (stat.isSymbolicLink()) throw failure("FRAMEWORK_PATH_UNSAFE", "mutable framework path contains a symbolic-link component"); } catch (error) { if (error?.code !== "ENOENT") throw error; }
  }
}
async function auditTree(root, input) {
  const source = join(root, input.sourceDirectory); const metadata = await requiredStat(source, "FRAMEWORK_TREE_MISMATCH", `source root is missing: ${input.id}`);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", `source root is unsafe: ${input.id}`);
  async function walk(directory) { for (const entry of await readdir(directory, { withFileTypes: true })) { const path = join(directory, entry.name); const stat = await lstat(path); if (stat.isSymbolicLink() || (!stat.isFile() && !stat.isDirectory())) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "extraction contains unsafe filesystem entry"); if (stat.isDirectory()) await walk(path); } }
  await walk(source); const marker = input.id === "cpputest" ? "CMakeLists.txt" : input.id === "unity" ? "src/unity.c" : "lib/cmock.rb"; const markerStat = await requiredStat(join(source, marker), "FRAMEWORK_MARKER_MISMATCH", `source marker is missing: ${input.id}`);
  if (!markerStat.isFile() || markerStat.isSymbolicLink()) throw failure("FRAMEWORK_MARKER_MISMATCH", `source marker is invalid: ${input.id}`);
  const license = join(source, input.license.path); const licenseStat = await requiredStat(license, "FRAMEWORK_LICENSE_MISMATCH", `license is missing: ${input.id}`); if (!licenseStat.isFile() || licenseStat.isSymbolicLink() || await sha256File(license) !== input.license.sha256) throw failure("FRAMEWORK_LICENSE_MISMATCH", `license identity mismatch: ${input.id}`);
  if (await directoryDigest(source) !== input.treeSha256) throw failure("FRAMEWORK_TREE_MISMATCH", `source tree identity mismatch: ${input.id}`);
}
function canonicalResolved(manifest, manifestSha256) { return { schemaVersion: manifest.schemaVersion, manifestSha256, platforms: manifest.platforms, fixtureTools: manifest.fixtureTools, frameworks: manifest.frameworks.map(({ id, version, tag, revision, source, license, sourceDirectory, treeSha256 }) => ({ id, version, tag, revision, source: { filename: source.filename, sha256: source.sha256 }, license, sourceDirectory, treeSha256 })) }; }
export async function verifyPreparedFrameworkBundle({ root, manifest, manifestSha256 }) {
  const ready = await readFile(join(root, "READY"), "utf8"); if (ready !== "framework-bundle-v2\n") throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "prepared bundle is not ready");
  const resolved = JSON.parse(await readFile(join(root, "manifest.resolved.json"), "utf8")); if (JSON.stringify(resolved) !== JSON.stringify(canonicalResolved(manifest, manifestSha256))) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "prepared bundle manifest differs");
  for (const input of manifest.frameworks) await auditTree(root, input); return true;
}
export async function prepareFrameworkBundle(options = {}) {
  const runtimeRoot = resolve(options.runtimeRoot ?? join(repositoryRoot, ".superpowers", "runtime", "framework-bundle")); const cacheRoot = resolve(options.cacheRoot ?? join(repositoryRoot, ".superpowers", "cache", "framework-bundle")); const ops = options.operations ?? {};
  const locked = await (ops.readManifest ?? readFrameworkManifest)(options.manifestPath); const { manifest, manifestSha256 } = locked; const target = join(runtimeRoot, "v2", manifestSha256);
  await assertNoSymlinkComponents(dirname(target));
  try { await verifyPreparedFrameworkBundle({ root: target, manifest, manifestSha256 }); return { root: target, manifest, manifestSha256, reused: true }; } catch (error) { if (error?.code === "ENOENT") {} else if (error?.code) throw error; }
  await mkdir(cacheRoot, { recursive: true, mode: 0o700 }); await mkdir(dirname(target), { recursive: true, mode: 0o700 }); await assertNoSymlinkComponents(cacheRoot); await assertNoSymlinkComponents(dirname(target)); const staging = join(dirname(target), randomName(".framework-bundle-")); await mkdir(staging, { mode: 0o700 });
  try { for (const input of manifest.frameworks) { const archive = await ensureArchive(cacheRoot, input, ops.download ?? defaultDownload); const entries = ops.inspectArchive ? await ops.inspectArchive(input, archive) : await inspectArchive(archive); validateArchiveEntries(entries, input); await verifyLockedArchive(cacheRoot, input); if (ops.extractArchive) await ops.extractArchive(input, staging, archive); else await extractArchive(archive, staging); }
    const topLevel = await readdir(staging, { withFileTypes: true }); const actualRoots = topLevel.filter((entry) => entry.isDirectory()).map((entry) => entry.name).sort(); const expectedRoots = manifest.frameworks.map((item) => item.sourceDirectory).sort(); if (topLevel.some((entry) => !entry.isDirectory()) || JSON.stringify(actualRoots) !== JSON.stringify(expectedRoots)) throw failure("FRAMEWORK_ARCHIVE_UNSAFE", "extraction roots do not match manifest");
    for (const input of manifest.frameworks) await auditTree(staging, input); await writeFile(join(staging, "manifest.resolved.json"), `${JSON.stringify(canonicalResolved(manifest, manifestSha256), null, 2)}\n`, { flag: "wx", mode: 0o600 }); await writeFile(join(staging, "READY"), "framework-bundle-v2\n", { flag: "wx", mode: 0o600 });
    try { await rename(staging, target); } catch (error) { if (error?.code !== "EEXIST" && error?.code !== "EPERM") throw error; await verifyPreparedFrameworkBundle({ root: target, manifest, manifestSha256 }); return { root: target, manifest, manifestSha256, reused: true }; }
    return { root: target, manifest, manifestSha256, reused: false };
  } catch (error) { await rm(staging, { recursive: true, force: true }); throw error; }
}
export const __testing = Object.freeze({ mkdirp: async (path) => mkdir(path, { recursive: true }), inspectArchive, extractArchive, trustedUrl });
if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) prepareFrameworkBundle().then((result) => process.stdout.write(`${JSON.stringify(result)}\n`)).catch((error) => { process.stderr.write(`framework-bundle: ${error.message}\n`); process.exitCode = 1; });
