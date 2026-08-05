import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import {
  cp,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { dirname, join, posix, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { gunzipSync, inflateRawSync } from "node:zlib";
import { Transform } from "node:stream";
import { pipeline } from "node:stream/promises";

import { bundleDirectory, cacheDirectory, platformKey } from "./layout.mjs";

const execFile = promisify(execFileCallback);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const manifestPath = join(toolDirectory, "manifest.json");
const resolvedName = "manifest.resolved.json";
const readyName = "READY";
const digestPattern = /^[0-9a-f]{64}$/u;
const maximumDownloadBytes = 512 * 1024 * 1024;
const maximumExpandedArchiveBytes = 1024 * 1024 * 1024;
const allowedHosts = new Set(["www.python.org", "files.pythonhosted.org"]);
const fixedLinuxImage = "quay.io/pypa/manylinux_2_28_x86_64@sha256:c7123a4aebb153c1e45b8152f07a64bd950d65e630cfb633a029cc45ee21897c";

function plainObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function exactKeys(value, expected, label) {
  if (!plainObject(value)) throw new Error(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) {
    throw new Error(`${label} has unexpected fields`);
  }
}

function validateURL(value) {
  const url = new URL(value);
  if (
    url.protocol !== "https:" || !allowedHosts.has(url.hostname) || url.username || url.password ||
    url.search || url.hash
  ) {
    throw new Error(`source URL is outside the reviewed allowlist: ${value}`);
  }
}

function validateArtifact(value, label, includePlatforms = false) {
  exactKeys(value, includePlatforms ? ["platforms", "filename", "url", "sha256"] : ["kind", "filename", "url", "sha256"], label);
  if (!/^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value.filename)) throw new Error(`${label} has an unsafe filename`);
  validateURL(value.url);
  if (!digestPattern.test(value.sha256)) throw new Error(`${label} has an invalid SHA-256`);
  if (includePlatforms) {
    if (!Array.isArray(value.platforms) || value.platforms.length === 0 || value.platforms.some((key) => !["windows-x64", "linux-x64"].includes(key))) {
      throw new Error(`${label} has invalid platforms`);
    }
  } else if (!["embedded-archive", "source-archive"].includes(value.kind)) {
    throw new Error(`${label} has an invalid kind`);
  }
}

export function validateSourceManifest(manifest) {
  exactKeys(manifest, ["schemaVersion", "python", "gcovr", "linux"], "manifest");
  if (manifest.schemaVersion !== 1) throw new Error("unsupported source manifest schemaVersion");
  exactKeys(manifest.python, ["version", "license", "artifacts"], "manifest.python");
  if (manifest.python.version !== "3.14.6" || manifest.python.license !== "PSF-2.0") throw new Error("unsupported Python identity");
  exactKeys(manifest.python.artifacts, ["windows-x64", "linux-x64"], "manifest.python.artifacts");
  for (const key of ["windows-x64", "linux-x64"]) validateArtifact(manifest.python.artifacts[key], `Python artifact ${key}`);
  if (manifest.python.artifacts["windows-x64"].kind !== "embedded-archive" || manifest.python.artifacts["linux-x64"].kind !== "source-archive") {
    throw new Error("Python artifact kind does not match its platform");
  }
  exactKeys(manifest.gcovr, ["version", "license", "wheels"], "manifest.gcovr");
  if (manifest.gcovr.version !== "8.6" || manifest.gcovr.license !== "BSD-3-Clause" || !Array.isArray(manifest.gcovr.wheels) || manifest.gcovr.wheels.length === 0) {
    throw new Error("unsupported gcovr identity");
  }
  const projects = new Set();
  for (const wheel of manifest.gcovr.wheels) {
    exactKeys(wheel, ["project", "version", "kind", "files"], "wheel lock entry");
    if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/u.test(wheel.project) || projects.has(wheel.project) || !/^\d+(?:\.\d+)*$/u.test(wheel.version) || wheel.kind !== "wheel") {
      throw new Error("invalid wheel lock entry");
    }
    projects.add(wheel.project);
    if (!Array.isArray(wheel.files) || wheel.files.length === 0) throw new Error("wheel lock entry has no files");
    for (const file of wheel.files) validateArtifact(file, `wheel ${wheel.project}`, true);
  }
  exactKeys(manifest.linux, ["builder", "glibcBaseline", "muslPolicy"], "manifest.linux");
  exactKeys(manifest.linux.builder, ["image", "sourceUrl"], "manifest.linux.builder");
  if (manifest.linux.builder.image !== fixedLinuxImage || manifest.linux.glibcBaseline !== "2.28" || manifest.linux.muslPolicy !== "unsupported") {
    throw new Error("unsupported Linux builder contract");
  }
  return manifest;
}

function collectArtifacts(manifest, key) {
  const result = [manifest.python.artifacts[key]];
  for (const wheel of manifest.gcovr.wheels) {
    const matches = wheel.files.filter(({ platforms }) => platforms.includes(key));
    if (matches.length !== 1) throw new Error(`wheel ${wheel.project} must resolve exactly once for ${key}`);
    result.push(matches[0]);
  }
  const urls = result.map(({ url }) => url);
  if (new Set(urls).size !== urls.length) throw new Error("duplicate source URL in platform allowlist");
  return result;
}

export async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function exists(path) {
  try { await lstat(path); return true; } catch (error) { if (error?.code === "ENOENT") return false; throw error; }
}

async function defaultDownload(url, destination) {
  const response = await fetch(url, { redirect: "error", signal: AbortSignal.timeout(5 * 60 * 1000) });
  if (!response.ok || response.url !== url || !response.body) throw new Error(`download failed for locked URL: ${url}`);
  const declared = Number(response.headers.get("content-length"));
  if (Number.isFinite(declared) && declared > maximumDownloadBytes) throw new Error("download exceeds size limit");
  let received = 0;
  const limit = new Transform({
    transform(chunk, _encoding, callback) {
      received += chunk.length;
      callback(received > maximumDownloadBytes ? new Error("download exceeds size limit") : null, chunk);
    },
  });
  await pipeline(response.body, limit, createWriteStream(destination, { flags: "wx" }));
}

export async function obtainArtifact(artifact, cacheRoot, download = defaultDownload) {
  validateURL(artifact.url);
  await mkdir(cacheRoot, { recursive: true });
  const target = join(cacheRoot, `${artifact.sha256}-${artifact.filename}`);
  if (await exists(target)) {
    if ((await sha256File(target)) !== artifact.sha256) throw new Error(`cached artifact SHA-256 mismatch: ${artifact.filename}`);
    return target;
  }
  const partial = join(cacheRoot, `.partial-${artifact.sha256}-${randomBytes(8).toString("hex")}`);
  try {
    await download(artifact.url, partial);
    const actual = await sha256File(partial);
    if (actual !== artifact.sha256) throw new Error(`artifact SHA-256 mismatch: ${artifact.filename}`);
    try { await rename(partial, target); } catch (error) {
      if (error?.code !== "EEXIST") throw error;
      if ((await sha256File(target)) !== artifact.sha256) throw new Error(`cached artifact SHA-256 mismatch: ${artifact.filename}`);
    }
    return target;
  } finally {
    await rm(partial, { force: true });
  }
}

function portablePath(value) {
  if (typeof value !== "string" || !value || value.includes("\\") || value.includes(":")) return false;
  const path = value.endsWith("/") ? value.slice(0, -1) : value;
  if (!path || posix.isAbsolute(path) || posix.normalize(path) !== path || !/^[A-Za-z0-9._+/-]+$/u.test(path)) return false;
  return path.split("/").every((part) => {
    const base = part.split(".", 1)[0].toUpperCase();
    return part && part !== "." && part !== ".." && !part.endsWith(".") && !part.endsWith(" ") &&
      !["CON", "PRN", "AUX", "NUL"].includes(base) && !/^(?:COM|LPT)[1-9]$/u.test(base);
  });
}

export function validateArchiveEntries(entries) {
  if (!Array.isArray(entries) || entries.length === 0) throw new Error("unsafe archive entry: empty archive");
  const names = new Set();
  for (const entry of entries) {
    if (!plainObject(entry) || !portablePath(entry.path) || !["file", "directory"].includes(entry.type)) throw new Error(`unsafe archive entry: ${entry?.path ?? "unknown"}`);
    const key = entry.path.replace(/\/$/u, "").toLowerCase();
    if (names.has(key)) throw new Error(`unsafe archive entry: duplicate ${entry.path}`);
    names.add(key);
  }
  return entries;
}

function findEndOfCentralDirectory(buffer) {
  for (let index = buffer.length - 22; index >= Math.max(0, buffer.length - 65_557); index--) {
    if (buffer.readUInt32LE(index) === 0x06054b50) return index;
  }
  throw new Error("invalid ZIP: end record not found");
}

function parseZip(buffer) {
  const end = findEndOfCentralDirectory(buffer);
  const count = buffer.readUInt16LE(end + 10);
  const centralSize = buffer.readUInt32LE(end + 12);
  let offset = buffer.readUInt32LE(end + 16);
  if (count === 0xffff || centralSize === 0xffffffff || offset === 0xffffffff || offset + centralSize > end) throw new Error("unsupported ZIP64 or invalid ZIP");
  const entries = [];
  let expandedTotal = 0;
  for (let index = 0; index < count; index++) {
    if (buffer.readUInt32LE(offset) !== 0x02014b50) throw new Error("invalid ZIP central directory");
    const flags = buffer.readUInt16LE(offset + 8);
    const method = buffer.readUInt16LE(offset + 10);
    const crc = buffer.readUInt32LE(offset + 16);
    const compressedSize = buffer.readUInt32LE(offset + 20);
    const size = buffer.readUInt32LE(offset + 24);
    const nameLength = buffer.readUInt16LE(offset + 28);
    const extraLength = buffer.readUInt16LE(offset + 30);
    const commentLength = buffer.readUInt16LE(offset + 32);
    const external = buffer.readUInt32LE(offset + 38);
    const localOffset = buffer.readUInt32LE(offset + 42);
    const name = buffer.subarray(offset + 46, offset + 46 + nameLength).toString("utf8");
    expandedTotal += size;
    if ((flags & 1) !== 0 || ![0, 8].includes(method) || size > maximumExpandedArchiveBytes || expandedTotal > maximumExpandedArchiveBytes) throw new Error(`unsafe archive entry: ${name}`);
    const mode = external >>> 16;
    const fileType = mode & 0o170000;
    let type = name.endsWith("/") || fileType === 0o040000 ? "directory" : "file";
    if (fileType === 0o120000) type = "symlink";
    entries.push({ path: name, type, method, crc, compressedSize, size, localOffset });
    offset += 46 + nameLength + extraLength + commentLength;
  }
  validateArchiveEntries(entries.map(({ path, type }) => ({ path, type })));
  return entries;
}

function zipEntryBytes(buffer, entry) {
  const offset = entry.localOffset;
  if (buffer.readUInt32LE(offset) !== 0x04034b50) throw new Error(`invalid ZIP local header: ${entry.path}`);
  const nameLength = buffer.readUInt16LE(offset + 26);
  const extraLength = buffer.readUInt16LE(offset + 28);
  const start = offset + 30 + nameLength + extraLength;
  const compressed = buffer.subarray(start, start + entry.compressedSize);
  if (compressed.length !== entry.compressedSize) throw new Error(`truncated ZIP entry: ${entry.path}`);
  const bytes = entry.method === 0 ? Buffer.from(compressed) : inflateRawSync(compressed, { maxOutputLength: maximumExpandedArchiveBytes });
  if (bytes.length !== entry.size || crc32(bytes) !== entry.crc) throw new Error(`corrupt ZIP entry: ${entry.path}`);
  return bytes;
}

function parseTarString(buffer, offset, length) {
  return buffer.subarray(offset, offset + length).toString("utf8").replace(/\0.*$/su, "").trim();
}

function parsePax(buffer) {
  const values = {};
  let offset = 0;
  while (offset < buffer.length) {
    const space = buffer.indexOf(0x20, offset);
    if (space < 0) throw new Error("unsafe archive entry: malformed PAX record");
    const length = Number.parseInt(buffer.subarray(offset, space).toString("ascii"), 10);
    if (!Number.isSafeInteger(length) || length <= 0 || offset + length > buffer.length) throw new Error("unsafe archive entry: malformed PAX length");
    const record = buffer.subarray(space + 1, offset + length - 1).toString("utf8");
    const equals = record.indexOf("=");
    if (equals <= 0) throw new Error("unsafe archive entry: malformed PAX value");
    values[record.slice(0, equals)] = record.slice(equals + 1);
    offset += length;
  }
  return values;
}

function parseTar(buffer) {
  const entries = [];
  let offset = 0;
  let longName;
  let globalPax = {};
  let localPax = {};
  let expandedTotal = 0;
  while (offset + 512 <= buffer.length) {
    const header = buffer.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const name = parseTarString(header, 0, 100);
    const prefix = parseTarString(header, 345, 155);
    const sizeText = parseTarString(header, 124, 12);
    const size = Number.parseInt(sizeText || "0", 8);
    expandedTotal += size;
    if (!Number.isSafeInteger(size) || size < 0 || size > maximumExpandedArchiveBytes || expandedTotal > maximumExpandedArchiveBytes) throw new Error("unsafe archive entry: invalid TAR size");
    const typeFlag = String.fromCharCode(header[156] || 48);
    const dataStart = offset + 512;
    if (typeFlag === "L") {
      longName = buffer.subarray(dataStart, dataStart + size).toString("utf8").replace(/\0.*$/su, "");
    } else if (typeFlag === "x") {
      localPax = parsePax(buffer.subarray(dataStart, dataStart + size));
    } else if (typeFlag === "g") {
      globalPax = { ...globalPax, ...parsePax(buffer.subarray(dataStart, dataStart + size)) };
    } else {
      const path = localPax.path ?? globalPax.path ?? longName ?? (prefix ? `${prefix}/${name}` : name);
      longName = undefined;
      localPax = {};
      const type = typeFlag === "0" || typeFlag === "\0" ? "file" : typeFlag === "5" ? "directory" : typeFlag === "2" ? "symlink" : "other";
      entries.push({ path: type === "directory" && !path.endsWith("/") ? `${path}/` : path, type });
    }
    offset = dataStart + Math.ceil(size / 512) * 512;
  }
  return validateArchiveEntries(entries);
}

async function inspectArchive(path) {
  const bytes = await readFile(path);
  if (/\.(?:tgz|tar\.gz)$/iu.test(path)) return parseTar(gunzipSync(bytes, { maxOutputLength: maximumExpandedArchiveBytes }));
  return parseZip(bytes).map(({ path: name, type }) => ({ path: name, type }));
}

async function extractZip(path, destination) {
  const buffer = await readFile(path);
  const entries = parseZip(buffer);
  for (const entry of entries) {
    const target = join(destination, ...entry.path.replace(/\/$/u, "").split("/"));
    if (entry.type === "directory") await mkdir(target, { recursive: true });
    else {
      await mkdir(dirname(target), { recursive: true });
      await writeFile(target, zipEntryBytes(buffer, entry), { flag: "wx" });
    }
  }
}

const crcTable = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let value = n;
    for (let bit = 0; bit < 8; bit++) value = (value & 1) ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    table[n] = value >>> 0;
  }
  return table;
})();

function crc32(buffer) {
  let value = 0xffffffff;
  for (const byte of buffer) value = crcTable[(value ^ byte) & 0xff] ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}

function deterministicZip(files) {
  const local = [];
  const central = [];
  let offset = 0;
  const ordered = [...files].sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0);
  for (const [name, bytes] of ordered) {
    const nameBytes = Buffer.from(name);
    const content = Buffer.from(bytes);
    const crc = crc32(content);
    const header = Buffer.alloc(30);
    header.writeUInt32LE(0x04034b50, 0); header.writeUInt16LE(20, 4); header.writeUInt16LE(0x800, 6);
    header.writeUInt16LE(0, 8); header.writeUInt16LE(0, 10); header.writeUInt16LE(33, 12);
    header.writeUInt32LE(crc, 14); header.writeUInt32LE(content.length, 18); header.writeUInt32LE(content.length, 22); header.writeUInt16LE(nameBytes.length, 26);
    local.push(header, nameBytes, content);
    const record = Buffer.alloc(46);
    record.writeUInt32LE(0x02014b50, 0); record.writeUInt16LE(0x0314, 4); record.writeUInt16LE(20, 6); record.writeUInt16LE(0x800, 8);
    record.writeUInt16LE(0, 10); record.writeUInt16LE(0, 12); record.writeUInt16LE(33, 14); record.writeUInt32LE(crc, 16);
    record.writeUInt32LE(content.length, 20); record.writeUInt32LE(content.length, 24); record.writeUInt16LE(nameBytes.length, 28);
    record.writeUInt32LE((0o100644 << 16) >>> 0, 38); record.writeUInt32LE(offset, 42);
    central.push(record, nameBytes);
    offset += header.length + nameBytes.length + content.length;
  }
  const centralBytes = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(ordered.length, 8); end.writeUInt16LE(ordered.length, 10);
  end.writeUInt32LE(centralBytes.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...local, centralBytes, end]);
}

async function createApplicationArchive(wheelPaths, destination) {
  const files = new Map();
  const add = (name, bytes) => {
    const key = name.toLowerCase();
    if ([...files.keys()].some((existing) => existing.toLowerCase() === key)) throw new Error(`duplicate application archive entry: ${name}`);
    files.set(name, bytes);
  };
  add("__main__.py", await readFile(join(toolDirectory, "runner", "__main__.py")));
  add("contract.py", await readFile(join(toolDirectory, "runner", "contract.py")));
  for (const path of wheelPaths) {
    const buffer = await readFile(path);
    for (const entry of parseZip(buffer)) if (entry.type === "file") add(entry.path, zipEntryBytes(buffer, entry));
  }
  await mkdir(dirname(destination), { recursive: true });
  await writeFile(destination, deterministicZip(files));
}

async function copyLicenses(stagingRoot) {
  await cp(join(toolDirectory, "licenses"), join(stagingRoot, "licenses"), { recursive: true, errorOnExist: true });
  await cp(join(toolDirectory, "runner", "NOTICE.txt"), join(stagingRoot, "licenses", "runner-NOTICE.txt"), { errorOnExist: true });
}

async function buildWindows({ stagingRoot, artifacts, downloads }) {
  const pythonRoot = join(stagingRoot, "python");
  await mkdir(pythonRoot, { recursive: true });
  await extractZip(downloads.get(artifacts[0].url), pythonRoot);
  const pthFiles = (await readdir(pythonRoot)).filter((name) => /^python\d+\._pth$/iu.test(name));
  if (pthFiles.length !== 1) throw new Error("Windows embedded Python _pth file is missing or ambiguous");
  await writeFile(join(pythonRoot, pthFiles[0]), "python314.zip\n.\n../app/gcovr-runner.pyz\n");
  await createApplicationArchive(artifacts.slice(1).map(({ url }) => downloads.get(url)), join(stagingRoot, "app", "gcovr-runner.pyz"));
  await copyLicenses(stagingRoot);
}

async function buildLinux({ stagingRoot, artifacts, downloads, manifest }) {
  await execFile("bash", [
    join(toolDirectory, "build-linux.sh"),
    downloads.get(artifacts[0].url),
    stagingRoot,
    artifacts[0].sha256,
    manifest.linux.builder.image,
  ], { maxBuffer: 16 * 1024 * 1024 });
  await createApplicationArchive(artifacts.slice(1).map(({ url }) => downloads.get(url)), join(stagingRoot, "app", "gcovr-runner.pyz"));
  await copyLicenses(stagingRoot);
}

async function walk(root, current = root) {
  const result = [];
  for (const entry of await readdir(current, { withFileTypes: true })) {
    const absolute = join(current, entry.name);
    const path = relative(root, absolute).split(sep).join("/");
    const stats = await lstat(absolute);
    if (stats.isSymbolicLink() || (!stats.isDirectory() && !stats.isFile())) throw new Error(`unsafe bundle output: ${path}`);
    if (stats.isDirectory()) result.push(...await walk(root, absolute));
    else result.push({ path, absolute });
  }
  return result;
}

function forbiddenOutputPath(path) {
  const segments = path.toLowerCase().split("/");
  return segments.some((segment) => ["pip", "ensurepip", "test", "tests", "idle", "idlelib", "tk", "tkinter", "include", "includes", "headers", "build", "build-tools"].includes(segment)) || /\.(?:a|lib|h|hpp)$/iu.test(path);
}

function validateWindowsIsolation(files) {
  const pth = files.filter(({ path }) => /^python\/python\d+\._pth$/iu.test(path));
  if (pth.length !== 1) throw new Error("Windows bundle must contain exactly one _pth file");
  return readFile(pth[0].absolute, "utf8").then((text) => {
    if (/^\s*import\s+site\s*$/mu.test(text) || !/^\.\.\/app\/gcovr-runner\.pyz$/mu.test(text)) throw new Error("unsafe Windows _pth isolation");
  });
}

export async function createResolvedManifest(root, key, manifest) {
  const top = (await readdir(root)).sort();
  const expectedTop = ["app", "licenses", "python"];
  if (top.length !== expectedTop.length || top.some((name, index) => name !== expectedTop[index])) throw new Error("unexpected bundle top-level entry");
  const applicationEntries = await readdir(join(root, "app"));
  if (applicationEntries.length !== 1 || applicationEntries[0] !== "gcovr-runner.pyz") throw new Error("unexpected bundle app entry");
  const files = (await walk(root)).filter(({ path }) => ![resolvedName, readyName].includes(path)).sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
  for (const { path } of files) if (forbiddenOutputPath(path)) throw new Error(`forbidden bundle path: ${path}`);
  if (key === "windows-x64") await validateWindowsIsolation(files);
  const resolved = {
    schemaVersion: 1,
    platform: key,
    pythonVersion: manifest.python.version,
    gcovrVersion: manifest.gcovr.version,
    outputs: [],
  };
  for (const { path, absolute } of files) resolved.outputs.push({ path, sha256: await sha256File(absolute), kind: "regular-file" });
  if (resolved.outputs.length === 0) throw new Error("bundle contains no outputs");
  await writeFile(join(root, resolvedName), `${JSON.stringify(resolved, null, 2)}\n`, { flag: "wx" });
  return resolved;
}

function validateResolvedShape(value, key, manifest) {
  exactKeys(value, ["schemaVersion", "platform", "pythonVersion", "gcovrVersion", "outputs"], "resolved manifest");
  if (value.schemaVersion !== 1 || value.platform !== key || value.pythonVersion !== manifest.python.version || value.gcovrVersion !== manifest.gcovr.version || !Array.isArray(value.outputs) || value.outputs.length === 0) throw new Error("resolved manifest identity mismatch");
  let previous = "";
  const names = new Set();
  for (const output of value.outputs) {
    exactKeys(output, ["path", "sha256", "kind"], "resolved output");
    if (!portablePath(output.path) || output.kind !== "regular-file" || !digestPattern.test(output.sha256) || output.path <= previous || names.has(output.path.toLowerCase()) || [resolvedName, readyName].includes(output.path)) throw new Error("invalid resolved output record");
    previous = output.path;
    names.add(output.path.toLowerCase());
  }
}

export async function verifyResolvedBundle(root, key, manifest) {
  if ((await readFile(join(root, readyName), "utf8")) !== "ready\n") throw new Error("coverage bundle is not READY");
  const resolved = JSON.parse(await readFile(join(root, resolvedName), "utf8"));
  validateResolvedShape(resolved, key, manifest);
  const actualFiles = (await walk(root)).map(({ path }) => path).sort();
  const expectedFiles = [...resolved.outputs.map(({ path }) => path), readyName, resolvedName].sort();
  if (actualFiles.length !== expectedFiles.length || actualFiles.some((path, index) => path !== expectedFiles[index])) throw new Error("resolved output list does not match bundle files");
  for (const output of resolved.outputs) {
    const actual = await sha256File(join(root, ...output.path.split("/")));
    if (actual !== output.sha256) throw new Error(`output SHA-256 mismatch: ${output.path}`);
    if (forbiddenOutputPath(output.path)) throw new Error(`forbidden bundle path: ${output.path}`);
  }
  if (key === "windows-x64") await validateWindowsIsolation((await walk(root)).filter(({ path }) => path !== readyName && path !== resolvedName));
  return resolved;
}

function defaultOperations(key) {
  return {
    inspectArchive,
    buildBundle: key === "windows-x64" ? buildWindows : buildLinux,
    afterReady: ({ stagingRoot, manifest }) => smokeBundle(stagingRoot, key, manifest),
  };
}

export async function prepareBundleFromManifest({ manifest, key, outputRoot, cacheRoot, download = defaultDownload, operations = defaultOperations(key) }) {
  validateSourceManifest(manifest);
  if (!["windows-x64", "linux-x64"].includes(key)) throw new Error(`unsupported coverage bundle platform: ${key}`);
  const target = join(outputRoot, key);
  await mkdir(outputRoot, { recursive: true });
  if (await exists(target)) {
    await verifyResolvedBundle(target, key, manifest);
    return { root: target, reused: true };
  }
  const artifacts = collectArtifacts(manifest, key);
  const downloads = new Map();
  for (const artifact of artifacts) {
    const path = await obtainArtifact(artifact, cacheRoot, download);
    const entries = await operations.inspectArchive(path, artifact);
    validateArchiveEntries(entries);
    downloads.set(artifact.url, path);
  }
  const stagingRoot = await mkdtemp(join(outputRoot, `.coverage-bundle-${key}-`));
  try {
    await operations.buildBundle({ stagingRoot, artifacts, downloads, manifest, key });
    await createResolvedManifest(stagingRoot, key, manifest);
    await operations.beforeReady?.({ stagingRoot, target });
    await writeFile(join(stagingRoot, readyName), "ready\n", { flag: "wx" });
    await verifyResolvedBundle(stagingRoot, key, manifest);
    await operations.afterReady?.({ stagingRoot, target, manifest, key });
    await operations.beforePublish?.({ stagingRoot, target });
    try { await rename(stagingRoot, target); } catch (error) {
      if (!await exists(target)) throw error;
      await verifyResolvedBundle(target, key, manifest);
      return { root: target, reused: true };
    }
    return { root: target, reused: false };
  } finally {
    await rm(stagingRoot, { recursive: true, force: true });
  }
}

async function loadManifest() {
  return JSON.parse(await readFile(manifestPath, "utf8"));
}

async function smokeBundle(root, key, manifest) {
  const executable = key === "windows-x64" ? join(root, "python", "python.exe") : join(root, "python", "bin", "python3");
  const application = join(root, "app", "gcovr-runner.pyz");
  const { stdout } = await execFile(executable, [application, "--self-check"], { cwd: root, timeout: 60_000, maxBuffer: 1024 * 1024, windowsHide: true });
  const result = JSON.parse(stdout.trim());
  if (result.python !== manifest.python.version || result.gcovr !== manifest.gcovr.version) throw new Error("coverage bundle self-check version mismatch");
}

function parseCLI(arguments_) {
  if (arguments_.length === 0) return { check: false };
  if (arguments_.length === 1 && arguments_[0] === "--check") return { check: true };
  throw new Error("usage: node tools/coverage-bundle/prepare.mjs [--check]");
}

async function main() {
  const { check } = parseCLI(process.argv.slice(2));
  const manifest = await loadManifest();
  validateSourceManifest(manifest);
  const key = platformKey();
  const root = bundleDirectory(repositoryRoot, key);
  if (check) {
    await verifyResolvedBundle(root, key, manifest);
    await smokeBundle(root, key, manifest);
    process.stdout.write(`verified coverage bundle ${root}\n`);
    return;
  }
  const result = await prepareBundleFromManifest({
    manifest,
    key,
    outputRoot: dirname(root),
    cacheRoot: cacheDirectory(repositoryRoot),
  });
  await smokeBundle(result.root, key, manifest);
  process.stdout.write(`${result.reused ? "reused" : "prepared"} coverage bundle ${result.root}\n`);
}

export const __testing = Object.freeze({
  createResolvedManifest,
  deterministicZip,
  inspectArchive,
  obtainArtifact,
  parseCLI,
  prepareBundleFromManifest,
  validateArchiveEntries,
  verifyResolvedBundle,
});

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    process.stderr.write(`${error?.stack ?? error}\n`);
    process.exitCode = 1;
  });
}
