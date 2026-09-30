import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { createWriteStream } from "node:fs";
import { chmod, copyFile, cp, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { dirname, basename, isAbsolute, join, relative, resolve } from "node:path";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import Ajv2020 from "ajv/dist/2020.js";

const execFile = promisify(execFileCallback);
const toolDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDir, "..", "..");
const manifestPath = join(toolDir, "manifest.json");
const schemaPath = join(toolDir, "manifest.schema.json");
const defaultRoot = join(repositoryRoot, ".superpowers", "cache", "llvm-coverage-bundle", "22.1.8", "linux-x64");
const maximumArchiveBytes = 2_200_000_000;
const maximumToolBytes = 512 * 1024 * 1024;
const toolNames = ["clang", "clangxx", "llvmProfdata", "llvmCov"];

function manifestBytes(manifest) {
  return Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
}

function digestBytes(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function digestFile(path) {
  const hash = createHash("sha256");
  const file = await readFile(path);
  hash.update(file);
  return hash.digest("hex");
}

function inside(root, candidate) {
  const part = relative(root, candidate);
  return part === "" || (!part.startsWith("..") && !isAbsolute(part));
}

export async function loadSourceManifest() {
  const [schema, sourceBytes] = await Promise.all([
    readFile(schemaPath, "utf8").then(JSON.parse),
    readFile(manifestPath),
  ]);
  const manifest = JSON.parse(sourceBytes.toString("utf8"));
  const validate = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
  if (!validate(manifest)) throw new Error(`invalid LLVM coverage manifest: ${JSON.stringify(validate.errors)}`);
  return { manifest, manifestBytes: sourceBytes };
}

export function validateManifest(manifest, sourceBytes = manifestBytes(manifest)) {
  if (manifest?.platform !== "linux-x64") throw new Error("LLVM coverage bundle must target linux-x64");
  if (!Buffer.isBuffer(sourceBytes)) throw new Error("LLVM coverage manifest bytes must be a Buffer");
  if (manifestBytes(manifest).compare(sourceBytes) !== 0) throw new Error("LLVM coverage manifest is not canonical JSON");
  return manifest;
}

async function downloadVerifiedSource(url, destination) {
  let current = url;
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    const parsed = new URL(current);
    const allowed = redirects === 0 ? parsed.hostname === "github.com" : ["release-assets.githubusercontent.com", "objects.githubusercontent.com"].includes(parsed.hostname);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || !allowed || (redirects === 0 && (parsed.search || parsed.hash))) {
      throw new Error(`LLVM source URL is outside the reviewed HTTPS boundary: ${current}`);
    }
    const response = await fetch(current, { redirect: "manual", signal: AbortSignal.timeout(15 * 60_000) });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get("location");
      if (!location) throw new Error("LLVM source redirect lacks Location");
      current = new URL(location, current).href;
      continue;
    }
    if (!response.ok || !response.body) throw new Error(`LLVM source download failed: HTTP ${response.status}`);
    let total = 0;
    const counter = new Transform({
      transform(chunk, _encoding, callback) {
        total += chunk.length;
        callback(total > maximumArchiveBytes ? new Error("LLVM archive exceeds byte budget") : null, chunk);
      },
    });
    await pipeline(Readable.fromWeb(response.body), counter, createWriteStream(destination, { flags: "wx" }));
    return;
  }
  throw new Error("LLVM source redirect limit exceeded");
}

async function archiveEntries(archivePath) {
  const { stdout } = await execFile("tar", ["-tf", archivePath], { encoding: "utf8", maxBuffer: 128 * 1024 * 1024, windowsHide: true });
  const entries = stdout.split(/\r?\n/u).filter(Boolean);
  for (const entry of entries) {
    if (entry.startsWith("/") || /^[A-Za-z]:/u.test(entry) || entry.includes("\\") || entry.split("/").some((part) => part === ".." || part === ".")) {
      throw new Error(`unsafe LLVM archive entry: ${entry}`);
    }
  }
  return entries;
}

async function extractArchive(archivePath, destination, manifest) {
  const entries = await archiveEntries(archivePath);
  const prefix = `${manifest.archiveRoot}/`;
  if (!entries.some((entry) => entry === manifest.archiveRoot || entry.startsWith(prefix))) throw new Error("LLVM archive root is missing");
  await execFile("tar", ["-xJf", archivePath, "-C", destination], { encoding: "utf8", maxBuffer: 16 * 1024 * 1024, windowsHide: true });
  const root = join(destination, manifest.archiveRoot);
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("LLVM archive root is not a regular directory");
  return root;
}

async function findToolSource(sourceRoot, relativePath) {
  const exact = join(sourceRoot, relativePath);
  try {
    await lstat(exact);
    return exact;
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  const directory = dirname(exact);
  const base = basename(relativePath);
  const candidates = (await readdir(directory)).filter((name) => name.startsWith(`${base}-`)).sort();
  if (candidates.length === 0) throw new Error(`required LLVM tool is missing: ${relativePath}`);
  return join(directory, candidates[0]);
}

async function canonicalizeTool(bundleRoot, sourceRoot, relativePath) {
  const source = await findToolSource(sourceRoot, relativePath);
  const canonical = await realpath(source);
  if (!inside(sourceRoot, canonical)) throw new Error(`LLVM tool escapes archive root: ${relativePath}`);
  const sourceInfo = await lstat(canonical);
  if (!sourceInfo.isFile() || sourceInfo.size > maximumToolBytes) throw new Error(`required LLVM tool is not a bounded file: ${relativePath}`);
  const destination = join(bundleRoot, relativePath);
  await mkdir(dirname(destination), { recursive: true });
  let destinationInfo;
  try { destinationInfo = await lstat(destination); } catch (error) { if (error?.code !== "ENOENT") throw error; }
  if (!destinationInfo?.isFile() || destinationInfo.isSymbolicLink() || resolve(canonical) !== resolve(destination)) {
    const temporary = `${destination}.${randomUUID()}.tmp`;
    await copyFile(canonical, temporary);
    await chmod(temporary, 0o755);
    await rename(temporary, destination);
  }
  const result = await lstat(destination);
  if (!result.isFile() || result.isSymbolicLink()) throw new Error(`required LLVM tool was not canonicalized: ${relativePath}`);
}

async function verifyLicense(bundleRoot, manifest) {
  const path = join(bundleRoot, manifest.license.path);
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || info.size !== manifest.license.size || await digestFile(path) !== manifest.license.sha256) {
    throw new Error("LLVM license digest mismatch");
  }
}

export async function checkBundle({ root, manifest, manifestBytes: expectedBytes } = {}) {
  const resolvedRoot = resolve(root ?? defaultRoot);
  const [sourceManifest, sourceBytes] = manifest ? [manifest, expectedBytes ?? manifestBytes(manifest)] : await loadSourceManifest();
  validateManifest(sourceManifest, sourceBytes);
  const rootInfo = await lstat(resolvedRoot);
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error("LLVM bundle root is not a regular directory");
  const preparedBytes = await readFile(join(resolvedRoot, "manifest.json"));
  if (preparedBytes.compare(sourceBytes) !== 0) throw new Error("prepared LLVM manifest changed from source manifest");
  const ready = JSON.parse(await readFile(join(resolvedRoot, "READY"), "utf8"));
  const expectedKeys = ["schemaVersion", "platform", "manifestSha256", "toolDigests"];
  if (JSON.stringify(Object.keys(ready)) !== JSON.stringify(expectedKeys) || ready.schemaVersion !== 1 || ready.platform !== "linux-x64" || ready.manifestSha256 !== digestBytes(sourceBytes)) {
    throw new Error("prepared LLVM READY identity mismatch");
  }
  const toolDigests = {};
  for (const name of toolNames) {
    const path = join(resolvedRoot, sourceManifest.tools[name]);
    const info = await lstat(path);
    if (!info.isFile() || info.isSymbolicLink() || info.size > maximumToolBytes) throw new Error(`required LLVM tool is missing or linked: ${name}`);
    toolDigests[name] = await digestFile(path);
    if (ready.toolDigests[name] !== toolDigests[name]) throw new Error(`LLVM READY tool digest mismatch: ${name}`);
  }
  await verifyLicense(resolvedRoot, sourceManifest);
  return { root: resolvedRoot, manifestSha256: digestBytes(sourceBytes), toolDigests };
}

export async function prepareBundle({ manifest, manifestBytes: sourceBytes, outputRoot = defaultRoot, downloadArchive = downloadVerifiedSource, extractArchive: extract = extractArchive } = {}) {
  const resolvedManifest = manifest ?? (await loadSourceManifest()).manifest;
  const manifestSourceBytes = sourceBytes ?? manifestBytes(resolvedManifest);
  validateManifest(resolvedManifest, manifestSourceBytes);
  const finalRoot = resolve(outputRoot);
  try {
    return await checkBundle({ root: finalRoot, manifest: resolvedManifest, manifestBytes: manifestSourceBytes });
  } catch {
    // Rebuild only after the existing cache fails complete verification.
  }
  await mkdir(dirname(finalRoot), { recursive: true });
  const temporaryRoot = await mkdtemp(join(dirname(finalRoot), `.${basename(finalRoot)}.prepare-`));
  const archivePath = join(temporaryRoot, resolvedManifest.archive.filename);
  const extractionRoot = join(temporaryRoot, "extracted");
  const candidateRoot = join(temporaryRoot, "bundle");
  try {
    await mkdir(extractionRoot);
    await downloadArchive(resolvedManifest.archive.url, archivePath, maximumArchiveBytes);
    const archiveInfo = await lstat(archivePath);
    if (!archiveInfo.isFile() || archiveInfo.isSymbolicLink() || archiveInfo.size > maximumArchiveBytes || await digestFile(archivePath) !== resolvedManifest.archive.sha256) {
      throw new Error("LLVM archive SHA-256 digest mismatch");
    }
    const sourceRoot = await extract(archivePath, extractionRoot, resolvedManifest);
    await cp(sourceRoot, candidateRoot, { recursive: true, dereference: false, force: false });
    const licenseSource = join(sourceRoot, "LICENSE.TXT");
    const licenseDestination = join(candidateRoot, resolvedManifest.license.path);
    await mkdir(dirname(licenseDestination), { recursive: true });
    await copyFile(licenseSource, licenseDestination);
    for (const name of toolNames) await canonicalizeTool(candidateRoot, sourceRoot, resolvedManifest.tools[name]);
    await writeFile(join(candidateRoot, "manifest.json"), manifestSourceBytes, { flag: "wx" });
    const provisional = {};
    for (const name of toolNames) provisional[name] = await digestFile(join(candidateRoot, resolvedManifest.tools[name]));
    await writeFile(join(candidateRoot, "READY"), `${JSON.stringify({ schemaVersion: 1, platform: "linux-x64", manifestSha256: digestBytes(manifestSourceBytes), toolDigests: provisional })}\n`, { flag: "wx" });
    await checkBundle({ root: candidateRoot, manifest: resolvedManifest, manifestBytes: manifestSourceBytes });
    let backup;
    try {
      await lstat(finalRoot);
      backup = `${finalRoot}.replaced-${randomUUID()}`;
      await rename(finalRoot, backup);
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
    try {
      await rename(candidateRoot, finalRoot);
    } catch (error) {
      if (backup) await rename(backup, finalRoot);
      throw error;
    }
    if (backup) await rm(backup, { recursive: true, force: true });
    return await checkBundle({ root: finalRoot, manifest: resolvedManifest, manifestBytes: manifestSourceBytes });
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
}

function argumentValue(args, flag) {
  const index = args.indexOf(flag);
  if (index < 0) return undefined;
  const value = args[index + 1];
  if (!value || value.startsWith("--")) throw new Error(`${flag} requires a value`);
  return value;
}

function requestedPlatform(args) {
  const platform = argumentValue(args, "--platform");
  if (platform && platform !== "linux-x64") throw new Error(`unsupported LLVM coverage platform: ${platform}`);
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  const args = process.argv.slice(2);
  if (args.includes("--check")) {
    requestedPlatform(args);
    checkBundle({ root: argumentValue(args, "--root") })
      .then((result) => process.stdout.write(`${result.root}\n`))
      .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
  } else {
    requestedPlatform(args);
    prepareBundle({ outputRoot: argumentValue(args, "--output-root") })
      .then((result) => process.stdout.write(`${result.root}\n`))
      .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
  }
}
