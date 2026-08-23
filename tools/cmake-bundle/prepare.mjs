import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import {
  link,
  lstat,
  mkdir,
  mkdtemp,
  open,
  readFile,
  readdir,
  realpath,
  rename,
  rm,
  unlink,
} from "node:fs/promises";
import { Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import {
  basename,
  dirname,
  isAbsolute,
  join,
  posix,
  relative,
  resolve,
  sep,
} from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const fixedManifestPath = join(toolDirectory, "manifest.json");
const supportedKeys = ["linux-x64", "win32-x64"];
const maximumArchiveBytes = 512 * 1024 * 1024;
const maximumArchiveListingBytes = 64 * 1024 * 1024;
const maximumCapabilitiesBytes = 1024 * 1024;
const downloadTimeoutMs = 5 * 60 * 1000;
const redirectCodes = new Set([301, 302, 303, 307, 308]);
const digestPattern = /^[0-9a-f]{64}$/;

const fixedArchives = {
  "win32-x64": {
    url: "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip",
    rootDirectory: "cmake-4.3.4-windows-x86_64",
    executable: "bin/cmake.exe",
    ctestExecutable: "bin/ctest.exe",
    licensePath: "doc/cmake/LICENSE.rst",
  },
  "linux-x64": {
    url: "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
    rootDirectory: "cmake-4.3.4-linux-x86_64",
    executable: "bin/cmake",
    ctestExecutable: "bin/ctest",
    licensePath: "doc/cmake/LICENSE.rst",
  },
};

export function platformKey(platform = process.platform, arch = process.arch) {
  const key = `${platform}-${arch}`;
  if (!supportedKeys.includes(key)) {
    throw new Error(`unsupported CMake bundle platform: ${key}`);
  }
  return key;
}

export function validateManifest(manifest) {
  try {
    requireObject(manifest);
    requireExactKeys(manifest, ["schemaVersion", "cmakeVersion", "license", "archives"]);
    if (
      manifest.schemaVersion !== 1 ||
      manifest.cmakeVersion !== "4.3.4" ||
      manifest.license !== "BSD-3-Clause"
    ) {
      throw new Error("unsupported fixed manifest identity");
    }

    requireObject(manifest.archives);
    requireExactKeys(manifest.archives, supportedKeys);
    for (const key of supportedKeys) {
      validateArchiveManifest(key, manifest.archives[key]);
    }
  } catch (error) {
    throw new Error("invalid CMake bundle manifest", { cause: error });
  }
  return manifest;
}

function validateArchiveManifest(key, archive) {
  requireObject(archive);
  requireExactKeys(archive, [
    "url",
    "archiveSha256",
    "rootDirectory",
    "executable",
    "ctestExecutable",
    "licensePath",
    "installedFiles",
  ]);
  const fixed = fixedArchives[key];
  if (
    archive.url !== fixed.url ||
    archive.rootDirectory !== fixed.rootDirectory ||
    archive.executable !== fixed.executable ||
    archive.ctestExecutable !== fixed.ctestExecutable ||
    archive.licensePath !== fixed.licensePath ||
    !digestPattern.test(archive.archiveSha256)
  ) {
    throw new Error(`invalid fixed archive ${key}`);
  }
  validateDistributionURL(archive.url, archive.url);
  requireObject(archive.installedFiles);
  requireExactKeys(archive.installedFiles, [
    archive.executable,
    archive.ctestExecutable,
    archive.licensePath,
  ]);
  for (const [path, digest] of Object.entries(archive.installedFiles)) {
    if (!portableRelativePath(path) || !digestPattern.test(digest)) {
      throw new Error(`invalid installed file ${path}`);
    }
  }
}

function requireObject(value) {
  if (
    value === null ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) {
    throw new Error("expected a plain object");
  }
}

function requireExactKeys(value, expected) {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (
    actual.length !== wanted.length ||
    actual.some((name, index) => name !== wanted[index])
  ) {
    throw new Error(`unexpected fields: ${actual.join(",")}`);
  }
}

function portableRelativePath(path) {
  if (
    typeof path !== "string" ||
    path.length === 0 ||
    path.includes("\\") ||
    path.includes(":") ||
    /[\u0000-\u001f\u007f]/u.test(path) ||
    posix.isAbsolute(path) ||
    isAbsolute(path) ||
    /^[A-Za-z]:/u.test(path) ||
    posix.normalize(path) !== path
  ) {
    return false;
  }
  const segments = path.split("/");
  return segments.every((segment) =>
    portablePathSegment(segment) && segment !== "." && segment !== ".."
  );
}

function portablePathSegment(segment) {
  if (
    segment.length === 0 ||
    segment.endsWith(".") ||
    segment.endsWith(" ") ||
    /[<>:"|?*\\]/u.test(segment)
  ) {
    return false;
  }
  const base = segment.split(".", 1)[0].toUpperCase();
  return !(
    ["CON", "PRN", "AUX", "NUL"].includes(base) ||
    /^(COM|LPT)[1-9]$/u.test(base)
  );
}

export async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) {
    hash.update(chunk);
  }
  return hash.digest("hex");
}

export async function verifyInstalledFiles(root, files) {
  requireObject(files);
  const rootPath = resolve(root);
  const rootInfo = await lstat(rootPath);
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) {
    throw new Error("unsafe installed file root");
  }
  const canonicalRoot = await realpath(rootPath);
  for (const path of Object.keys(files).sort()) {
    const expected = files[path];
    if (!portableRelativePath(path) || !digestPattern.test(expected)) {
      throw new Error(`unsafe installed file: ${path}`);
    }
    const absolute = await verifiedRegularFile(rootPath, path);
    if (!withinRoot(canonicalRoot, await realpath(absolute))) {
      throw new Error(`unsafe installed file: ${path}`);
    }
    const before = await lstat(absolute);
    const actual = await sha256File(absolute);
    const after = await lstat(absolute);
    if (!sameFileSnapshot(before, after)) {
      throw new Error(`installed file changed during verification: ${path}`);
    }
    if (actual !== expected) {
      throw new Error(`installed file SHA-256 mismatch: ${path}`);
    }
  }
}

async function verifiedRegularFile(root, path) {
  let current = root;
  const segments = path.split("/");
  for (let index = 0; index < segments.length; index++) {
    current = join(current, segments[index]);
    let info;
    try {
      info = await lstat(current);
    } catch (error) {
      throw new Error(`unsafe installed file: ${path}`, { cause: error });
    }
    if (
      info.isSymbolicLink() ||
      (index < segments.length - 1 ? !info.isDirectory() : !info.isFile())
    ) {
      throw new Error(`unsafe installed file: ${path}`);
    }
  }
  return current;
}

function sameFileSnapshot(left, right) {
  return (
    left.dev === right.dev &&
    left.ino === right.ino &&
    left.size === right.size &&
    left.mtimeMs === right.mtimeMs &&
    left.ctimeMs === right.ctimeMs
  );
}

function withinRoot(root, candidate) {
  const path = relative(root, candidate);
  return path === "" || (!path.startsWith(`..${sep}`) && path !== ".." && !isAbsolute(path));
}

async function inspectArchive(archivePath) {
  const tar = await systemTarExecutable();
  const common = {
    encoding: "utf8",
    env: controlledChildEnvironment(),
    windowsHide: true,
    timeout: 60_000,
    maxBuffer: maximumArchiveListingBytes,
  };
  let names;
  let verbose;
  try {
    ({ stdout: names } = await execFile(tar, ["-tf", archivePath], common));
    ({ stdout: verbose } = await execFile(tar, ["-tvf", archivePath], common));
  } catch (error) {
    throw new Error("unable to inspect CMake archive", { cause: error });
  }
  const paths = splitListing(names);
  const verboseLines = splitListing(verbose);
  if (paths.length === 0 || paths.length !== verboseLines.length) {
    throw new Error("unsafe archive entry listing");
  }
  return paths.map((path, index) => ({
    path,
    type: archiveType(verboseLines[index]?.[0]),
  }));
}

function splitListing(value) {
  return value.replace(/\r\n/gu, "\n").split("\n").filter((line) => line.length > 0);
}

function archiveType(marker) {
  switch (marker) {
    case "-":
      return "file";
    case "d":
      return "directory";
    case "l":
      return "symlink";
    case "h":
      return "hardlink";
    default:
      return "device";
  }
}

function validateArchiveEntries(entries, archive) {
  if (!Array.isArray(entries) || entries.length === 0) {
    throw new Error("unsafe archive entry: empty archive");
  }
  const seen = new Set();
  let executableFound = false;
  let ctestExecutableFound = false;
  let licenseFound = false;
  for (const entry of entries) {
    if (
      entry === null ||
      typeof entry !== "object" ||
      Array.isArray(entry) ||
      typeof entry.path !== "string" ||
      (entry.type !== "file" && entry.type !== "directory")
    ) {
      throw new Error("unsafe archive entry");
    }
    const path = entry.type === "directory" && entry.path.endsWith("/")
      ? entry.path.slice(0, -1)
      : entry.path;
    if (
      !portableRelativePath(path) ||
      (path !== archive.rootDirectory && !path.startsWith(`${archive.rootDirectory}/`)) ||
      seen.has(path)
    ) {
      throw new Error(`unsafe archive entry: ${entry.path}`);
    }
    seen.add(path);
    if (path === `${archive.rootDirectory}/${archive.executable}` && entry.type === "file") {
      executableFound = true;
    }
    if (
      path === `${archive.rootDirectory}/${archive.ctestExecutable}` &&
      entry.type === "file"
    ) {
      ctestExecutableFound = true;
    }
    if (path === `${archive.rootDirectory}/${archive.licensePath}` && entry.type === "file") {
      licenseFound = true;
    }
  }
  if (!executableFound || !ctestExecutableFound || !licenseFound) {
    throw new Error("unsafe archive entry: required files are absent");
  }
}

async function extractArchive(archivePath, destination) {
  const tar = await systemTarExecutable();
  try {
    await execFile(tar, ["-xf", archivePath, "-C", destination], {
      encoding: "utf8",
      env: controlledChildEnvironment(),
      windowsHide: true,
      timeout: 120_000,
      maxBuffer: maximumArchiveListingBytes,
    });
  } catch (error) {
    throw new Error("unable to extract CMake archive", { cause: error });
  }
}

async function systemTarExecutable() {
  const candidates = [];
  if (process.platform === "win32") {
    const systemRoot = process.env.SystemRoot;
    if (
      typeof systemRoot !== "string" ||
      !isAbsolute(systemRoot) ||
      systemRoot.includes("\0")
    ) {
      throw new Error("Windows system tar path is unavailable");
    }
    candidates.push(join(resolve(systemRoot), "System32", "tar.exe"));
  } else {
    candidates.push("/usr/bin/tar", "/bin/tar");
  }
  for (const candidate of candidates) {
    const info = await lstat(candidate).catch(() => null);
    if (info?.isFile() && !info.isSymbolicLink()) {
      return candidate;
    }
  }
  throw new Error("controlled system tar executable is unavailable");
}

function controlledChildEnvironment() {
  const result = { LANG: "C", LC_ALL: "C" };
  for (const name of ["SystemRoot", "WINDIR", "TEMP", "TMP"]) {
    const value = process.env[name];
    if (typeof value === "string" && value.length > 0 && !value.includes("\0")) {
      result[name] = value;
    }
  }
  return result;
}

async function auditExtractedTree(extractionRoot, expectedRoot) {
  const topLevel = await readdir(extractionRoot);
  if (topLevel.length !== 1 || topLevel[0] !== expectedRoot) {
    throw new Error("unsafe extracted CMake archive layout");
  }
  const root = join(extractionRoot, expectedRoot);
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("unsafe extracted CMake archive root");
  }
  await auditDirectory(root);
  return root;
}

async function auditDirectory(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
  for (const entry of entries) {
    if (!portablePathSegment(entry.name) || /[\u0000-\u001f\u007f]/u.test(entry.name)) {
      throw new Error(`unsafe extracted archive entry: ${entry.name}`);
    }
    const path = join(directory, entry.name);
    const info = await lstat(path);
    if (info.isSymbolicLink()) {
      throw new Error(`unsafe extracted archive entry: ${entry.name}`);
    }
    if (info.isDirectory()) {
      await auditDirectory(path);
    } else if (!info.isFile()) {
      throw new Error(`unsafe extracted archive entry: ${entry.name}`);
    }
  }
}

async function readCapabilities(executable) {
  let stdout;
  try {
    ({ stdout } = await execFile(executable, ["-E", "capabilities"], {
      encoding: "utf8",
      env: controlledChildEnvironment(),
      windowsHide: true,
      timeout: 15_000,
      maxBuffer: maximumCapabilitiesBytes,
    }));
  } catch (error) {
    throw new Error("bundled CMake capabilities probe failed", { cause: error });
  }
  try {
    return JSON.parse(stdout);
  } catch (error) {
    throw new Error("bundled CMake capabilities are invalid", { cause: error });
  }
}

async function readCTestVersion(executable) {
  let stdout;
  try {
    ({ stdout } = await execFile(executable, ["--version"], {
      encoding: "utf8",
      env: controlledChildEnvironment(),
      windowsHide: true,
      timeout: 15_000,
      maxBuffer: maximumCapabilitiesBytes,
    }));
  } catch (error) {
    throw new Error("bundled CTest version probe failed", { cause: error });
  }
  const match = /^ctest version ([0-9]+\.[0-9]+\.[0-9]+)\s*$/m.exec(stdout);
  if (match === null) {
    throw new Error("bundled CTest version output is invalid");
  }
  return match[1];
}

function verifyCapabilities(capabilities, version) {
  if (
    capabilities === null ||
    typeof capabilities !== "object" ||
    capabilities.version === null ||
    typeof capabilities.version !== "object" ||
    capabilities.version.string !== version
  ) {
    throw new Error("bundled CMake version mismatch");
  }
}

function bundleState(key, manifest) {
  const archive = manifest.archives[key];
  return {
    schemaVersion: 1,
    key,
    cmakeVersion: manifest.cmakeVersion,
    archiveSha256: archive.archiveSha256,
    installedFiles: archive.installedFiles,
  };
}

async function writeDurableFile(path, data) {
  let handle;
  let created = false;
  try {
    handle = await open(path, "wx", 0o600);
    created = true;
    await handle.writeFile(data);
    await handle.sync();
    await handle.close();
    handle = undefined;
  } catch (error) {
    await handle?.close().catch(() => {});
    if (created) {
      await rm(path, { force: true }).catch(() => {});
    }
    throw error;
  }
}

async function publishManifest(outputRoot, expectedBytes) {
  const target = join(outputRoot, "manifest.json");
  if (await pathExists(target)) {
    await requireExactFile(target, expectedBytes, "published manifest mismatch");
    return;
  }
  const temporary = join(
    outputRoot,
    `.manifest-${process.pid}-${randomBytes(8).toString("hex")}.tmp`,
  );
  let temporaryPresent = false;
  try {
    await writeDurableFile(temporary, expectedBytes);
    temporaryPresent = true;
    try {
      await link(temporary, target);
    } catch (error) {
      if (error?.code !== "EEXIST") {
        throw error;
      }
      await requireExactFile(target, expectedBytes, "published manifest mismatch");
    }
    await syncDirectory(outputRoot);
  } finally {
    if (temporaryPresent) {
      await unlink(temporary).catch((error) => {
        if (error?.code !== "ENOENT") {
          throw error;
        }
      });
    }
  }
}

async function requireExactFile(path, expected, message) {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new Error(message);
  }
  const actual = await readFile(path);
  if (!actual.equals(expected)) {
    throw new Error(message);
  }
}

async function syncDirectory(path) {
  let handle;
  try {
    handle = await open(path, "r");
    await handle.sync();
  } catch (error) {
    if (!["EISDIR", "EINVAL", "EPERM", "ENOSYS", "EBADF"].includes(error?.code)) {
      throw error;
    }
  } finally {
    await handle?.close().catch(() => {});
  }
}

async function verifyExistingBundle(target, key, manifest, operations) {
  const archive = manifest.archives[key];
  await auditDirectoryRoot(target);
  const statePath = join(target, "bundle-state.json");
  const stateInfo = await lstat(statePath).catch(() => null);
  if (!stateInfo?.isFile() || stateInfo.isSymbolicLink()) {
    throw new Error("existing CMake bundle state is invalid");
  }
  let state;
  try {
    state = JSON.parse(await readFile(statePath, "utf8"));
    requireObject(state);
    requireExactKeys(state, [
      "schemaVersion",
      "key",
      "cmakeVersion",
      "archiveSha256",
      "installedFiles",
    ]);
    if (JSON.stringify(state) !== JSON.stringify(bundleState(key, manifest))) {
      throw new Error("state mismatch");
    }
  } catch (error) {
    throw new Error("existing CMake bundle state is invalid", { cause: error });
  }
  const installRoot = join(target, archive.rootDirectory);
  await auditDirectoryRoot(installRoot);
  await verifyInstalledFiles(installRoot, archive.installedFiles);
  const capabilities = await operations.readCapabilities(
    join(installRoot, ...archive.executable.split("/")),
  );
  verifyCapabilities(capabilities, manifest.cmakeVersion);
  const ctestVersion = await operations.readCTestVersion(
    join(installRoot, ...archive.ctestExecutable.split("/")),
  );
  if (ctestVersion !== manifest.cmakeVersion) {
    throw new Error("bundled CTest version mismatch");
  }
}

async function auditDirectoryRoot(root) {
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("existing CMake bundle root is unsafe");
  }
  await auditDirectory(root);
}

async function prepareBundleFromManifest({
  key,
  outputRoot,
  download,
  manifest,
  manifestBytes,
  operations,
}) {
  validateManifest(manifest);
  if (!supportedKeys.includes(key) || typeof download !== "function") {
    throw new Error("invalid CMake bundle preparation options");
  }
  if (!Buffer.isBuffer(manifestBytes) || manifestBytes.length === 0) {
    throw new Error("invalid fixed manifest bytes");
  }
  let manifestFromBytes;
  try {
    manifestFromBytes = JSON.parse(manifestBytes.toString("utf8"));
    validateManifest(manifestFromBytes);
  } catch (error) {
    throw new Error("invalid fixed manifest bytes", { cause: error });
  }
  if (JSON.stringify(manifestFromBytes) !== JSON.stringify(manifest)) {
    throw new Error("manifest bytes do not match the validated manifest object");
  }
  const root = requireAbsoluteOutputRoot(outputRoot);
  const archive = manifest.archives[key];
  const ops = {
    inspectArchive,
    extractArchive,
    readCapabilities,
    readCTestVersion,
    renameDirectory: rename,
    ...operations,
  };

  await ensureDirectoryChain(root, true);
  await mkdir(root, { recursive: true, mode: 0o700 });
  await ensureDirectoryChain(root, false);
  await publishManifest(root, manifestBytes);
  const versionRoot = join(root, manifest.cmakeVersion);
  await ensureDirectoryChain(versionRoot, true);
  await mkdir(versionRoot, { recursive: true, mode: 0o700 });
  await ensureDirectoryChain(versionRoot, false);
  const target = join(versionRoot, key);
  if (await pathExists(target)) {
    await verifyExistingBundle(target, key, manifest, ops);
    return bundleResult(target, key, manifest, true);
  }

  const staging = await mkdtemp(join(root, ".cmake-bundle-"));
  try {
    const archivePath = join(staging, "archive.download");
    await download(archivePath);
    const archiveInfo = await lstat(archivePath).catch(() => null);
    if (!archiveInfo?.isFile() || archiveInfo.isSymbolicLink()) {
      throw new Error("download did not create a regular archive");
    }
    if (archiveInfo.size > maximumArchiveBytes) {
      throw new Error("CMake archive exceeds the size limit");
    }
    const actualDigest = await sha256File(archivePath);
    if (actualDigest !== archive.archiveSha256) {
      throw new Error("archive SHA-256 mismatch");
    }

    const entries = await ops.inspectArchive(archivePath, archive);
    validateArchiveEntries(entries, archive);
    const extractionRoot = join(staging, "extract");
    await mkdir(extractionRoot, { mode: 0o700 });
    await ops.extractArchive(archivePath, extractionRoot, archive);
    const stagedRoot = await auditExtractedTree(extractionRoot, archive.rootDirectory);
    await verifyInstalledFiles(stagedRoot, archive.installedFiles);
    const capabilities = await ops.readCapabilities(
      join(stagedRoot, ...archive.executable.split("/")),
    );
    verifyCapabilities(capabilities, manifest.cmakeVersion);
    const ctestVersion = await ops.readCTestVersion(
      join(stagedRoot, ...archive.ctestExecutable.split("/")),
    );
    if (ctestVersion !== manifest.cmakeVersion) {
      throw new Error("bundled CTest version mismatch");
    }

    const publishRoot = join(staging, "publish");
    await mkdir(publishRoot, { mode: 0o700 });
    await ops.renameDirectory(stagedRoot, join(publishRoot, archive.rootDirectory));
    const stateBytes = Buffer.from(`${JSON.stringify(bundleState(key, manifest), null, 2)}\n`);
    await writeDurableFile(join(publishRoot, "bundle-state.json"), stateBytes);
    await syncDirectory(join(publishRoot, archive.rootDirectory));
    await syncDirectory(publishRoot);
    await ops.beforePublish?.({ stagedRoot: publishRoot, target });
    try {
      await ops.renameDirectory(publishRoot, target);
      await syncDirectory(versionRoot);
      return bundleResult(target, key, manifest, false);
    } catch (error) {
      if (!(await pathExists(target))) {
        throw new Error("unable to publish CMake bundle", { cause: error });
      }
      await verifyExistingBundle(target, key, manifest, ops);
      return bundleResult(target, key, manifest, true);
    }
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

function bundleResult(root, key, manifest, reused) {
  const archive = manifest.archives[key];
  const installRoot = join(root, archive.rootDirectory);
  return {
    root,
    installRoot,
    key,
    cmakeVersion: manifest.cmakeVersion,
    executable: join(installRoot, ...archive.executable.split("/")),
    ctestExecutable: join(installRoot, ...archive.ctestExecutable.split("/")),
    licensePath: join(installRoot, ...archive.licensePath.split("/")),
    reused,
  };
}

function requireAbsoluteOutputRoot(outputRoot) {
  if (
    typeof outputRoot !== "string" ||
    outputRoot.length === 0 ||
    outputRoot.includes("\0") ||
    !isAbsolute(outputRoot)
  ) {
    throw new Error("CMake bundle output root must be absolute");
  }
  return resolve(outputRoot);
}

async function ensureDirectoryChain(path, allowMissing) {
  let current = resolve(path);
  for (;;) {
    try {
      const info = await lstat(current);
      if (!info.isDirectory() || info.isSymbolicLink()) {
        throw new Error(`unsafe CMake bundle directory: ${current}`);
      }
    } catch (error) {
      if (error?.code !== "ENOENT" || !allowMissing) {
        if (error?.message?.startsWith("unsafe CMake bundle directory:")) {
          throw error;
        }
        throw new Error(`unsafe CMake bundle directory: ${current}`, { cause: error });
      }
    }
    const parent = dirname(current);
    if (parent === current) {
      return;
    }
    current = parent;
  }
}

async function pathExists(path) {
  try {
    await lstat(path);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

export async function prepareBundle({
  key = platformKey(),
  outputRoot = join(repositoryRoot, ".bundled-tools", "cmake"),
  download,
} = {}) {
  const bytes = await readFile(fixedManifestPath);
  let manifest;
  try {
    manifest = JSON.parse(bytes);
  } catch (error) {
    throw new Error("invalid CMake bundle manifest JSON", { cause: error });
  }
  validateManifest(manifest);
  const archive = manifest.archives[key];
  if (archive === undefined) {
    throw new Error(`unsupported CMake bundle key: ${key}`);
  }
  const downloader = download ?? ((destination) => downloadArchive(archive.url, destination));
  return prepareBundleFromManifest({
    key,
    outputRoot: resolve(outputRoot),
    download: downloader,
    manifest,
    manifestBytes: bytes,
  });
}

async function downloadArchive(lockedURL, destination) {
  let current = lockedURL;
  for (let redirects = 0; redirects <= 5; redirects++) {
    validateDistributionURL(current, lockedURL);
    const response = await fetch(current, {
      redirect: "manual",
      signal: AbortSignal.timeout(downloadTimeoutMs),
    });
    if (redirectCodes.has(response.status)) {
      const location = response.headers.get("location");
      if (location === null || redirects === 5) {
        throw new Error("invalid CMake archive redirect");
      }
      current = new URL(location, current).href;
      continue;
    }
    if (!response.ok || response.body === null) {
      throw new Error(`CMake archive download failed with status ${response.status}`);
    }
    validateDistributionURL(response.url || current, lockedURL);
    await writeResponseBody(response.body, destination);
    return;
  }
  throw new Error("too many CMake archive redirects");
}

function validateDistributionURL(value, lockedURL) {
  const url = new URL(value);
  const locked = new URL(lockedURL);
  if (
    url.protocol !== "https:" ||
    url.hostname !== "cmake.org" ||
    url.port !== "" ||
    url.username !== "" ||
    url.password !== "" ||
    url.search !== "" ||
    url.hash !== "" ||
    url.pathname !== locked.pathname ||
    !url.pathname.startsWith("/files/v4.3/") ||
    basename(url.pathname) !== basename(locked.pathname)
  ) {
    throw new Error("CMake archive URL is outside the fixed distribution origin");
  }
}

async function writeResponseBody(body, destination) {
  let bytes = 0;
  const limiter = new Transform({
    transform(chunk, _encoding, callback) {
      bytes += chunk.length;
      if (bytes > maximumArchiveBytes) {
        callback(new Error("CMake archive exceeds the size limit"));
        return;
      }
      callback(null, chunk);
    },
  });
  try {
    await pipeline(
      body,
      limiter,
      createWriteStream(destination, { flags: "wx", mode: 0o600 }),
    );
  } catch (error) {
    await rm(destination, { force: true });
    throw error;
  }
}

function parseCLI(arguments_, environment = process.env) {
  let key;
  let output;
  for (let index = 0; index < arguments_.length; index++) {
    const name = arguments_[index];
    if ((name !== "--key" && name !== "--output") || index + 1 >= arguments_.length) {
      throw new Error("usage: prepare.mjs [--key <win32-x64|linux-x64>] [--output <directory>]");
    }
    const value = arguments_[++index];
    if (value.length === 0 || value.includes("\0")) {
      throw new Error(`invalid ${name} value`);
    }
    if (name === "--key") {
      if (key !== undefined) {
        throw new Error("duplicate --key");
      }
      key = value;
    } else {
      if (output !== undefined) {
        throw new Error("duplicate --output");
      }
      output = resolve(value);
    }
  }
  if (key !== undefined && !supportedKeys.includes(key)) {
    throw new Error(`unsupported CMake bundle key: ${key}`);
  }
  const allowedRoot = environment.UNIT_TEST_IDE_CMAKE_BUNDLE_ALLOWED_ROOT;
  if (allowedRoot !== undefined && (!isAbsolute(allowedRoot) || allowedRoot.includes("\0"))) {
    throw new Error("UNIT_TEST_IDE_CMAKE_BUNDLE_ALLOWED_ROOT must be an absolute path");
  }
  const outputRoot = output ?? join(repositoryRoot, ".bundled-tools", "cmake");
  const allowed = [repositoryRoot];
  if (allowedRoot !== undefined) {
    allowed.push(resolve(allowedRoot));
  }
  if (!allowed.some((root) => withinRoot(root, outputRoot))) {
    throw new Error("--output must be inside the repository or the explicitly allowed CI root");
  }
  return { key: key ?? platformKey(), outputRoot };
}

async function main() {
  const options = parseCLI(process.argv.slice(2));
  const result = await prepareBundle(options);
  process.stdout.write(`${JSON.stringify({
    key: result.key,
    cmakeVersion: result.cmakeVersion,
    root: result.root,
    executable: result.executable,
    ctestExecutable: result.ctestExecutable,
    reused: result.reused,
  })}\n`);
}

export const __testing = Object.freeze({
  inspectArchive,
  parseCLI,
  prepareBundleFromManifest,
  systemTarExecutable,
  validateArchiveEntries,
  validateDistributionURL,
});

if (
  process.argv[1] !== undefined &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  main().catch((error) => {
    process.stderr.write(`cmake-bundle: ${error.message}\n`);
    process.exitCode = 1;
  });
}
