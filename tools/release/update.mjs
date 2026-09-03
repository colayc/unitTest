import { createHash, randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import { createReadStream } from "node:fs";
import {
  access,
  chmod,
  copyFile,
  lstat,
  mkdir,
  open,
  readFile,
  readdir,
  realpath,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { basename, dirname, isAbsolute, join, parse, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const manifestSchema = JSON.parse(await readFile(join(toolDirectory, "manifest.schema.json"), "utf8"));
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateManifest = ajv.compile(manifestSchema);

const ownerMarkerName = ".unit-test-ide-owned.json";
const ownerMarker = Object.freeze({ product: "unit-test-ide", schemaVersion: 1 });
const semverLike = /^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/u;
const digestPattern = /^[0-9a-f]{64}$/u;

function releaseError(code, message, cause) {
  const error = new Error(`${code}: ${message}`, cause ? { cause } : undefined);
  error.code = code;
  return error;
}

function verificationFailure(message, cause) {
  return releaseError("RELEASE_VERIFICATION_FAILED", message, cause);
}

function safeRelativePath(value) {
  if (
    typeof value !== "string"
    || value.length === 0
    || value.includes("\\")
    || value.includes(":")
    || isAbsolute(value)
    || posix.isAbsolute(value)
    || posix.normalize(value) !== value
  ) {
    return false;
  }
  return value.split("/").every((segment) => segment.length > 0 && segment !== "." && segment !== "..");
}

function withinRoot(root, candidate) {
  const relativePath = relative(root, candidate);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function pathExists(path) {
  try {
    await access(path);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    throw error;
  }
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function fsyncFile(path) {
  let handle;
  try {
    handle = await open(path, "r+");
    await handle.sync();
  } catch (error) {
    if (!["EACCES", "EINVAL", "ENOTSUP", "EPERM"].includes(error?.code)) throw error;
  } finally {
    await handle?.close();
  }
}

async function fsyncDirectory(path) {
  let handle;
  try {
    handle = await open(path, "r");
    await handle.sync();
  } catch (error) {
    if (!["EACCES", "EINVAL", "EISDIR", "ENOTSUP", "EPERM"].includes(error?.code)) throw error;
  } finally {
    await handle?.close();
  }
}

function normalizeRoot(root) {
  if (typeof root !== "string" || root.trim().length === 0) {
    throw releaseError("RELEASE_ROOT_INVALID", "package-owned root is required");
  }
  const normalized = resolve(root);
  if (normalized === parse(normalized).root) {
    throw releaseError("RELEASE_ROOT_INVALID", "filesystem root cannot be package-owned");
  }
  return normalized;
}

function samePath(left, right) {
  const normalizedLeft = resolve(left);
  const normalizedRight = resolve(right);
  return process.platform === "win32"
    ? normalizedLeft.toLowerCase() === normalizedRight.toLowerCase()
    : normalizedLeft === normalizedRight;
}

async function assertUnredirectedParent(path) {
  let current = resolve(path);
  for (;;) {
    let info;
    try {
      info = await lstat(current);
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
      const parent = dirname(current);
      if (parent === current) throw releaseError("RELEASE_ROOT_NOT_OWNED", "package root has no real parent");
      current = parent;
      continue;
    }
    if (info.isSymbolicLink() || !info.isDirectory()) {
      throw releaseError("RELEASE_ROOT_NOT_OWNED", "package root parent chain contains a reparse point");
    }
    if (!samePath(current, await realpath(current))) {
      throw releaseError("RELEASE_ROOT_NOT_OWNED", "package root parent chain is redirected");
    }
    return;
  }
}

async function validateDirectory(root, label) {
  const normalized = resolve(root);
  let info;
  try {
    info = await lstat(normalized);
  } catch (error) {
    throw verificationFailure(`${label} is missing`, error);
  }
  if (info.isSymbolicLink() || !info.isDirectory()) {
    throw verificationFailure(`${label} must be a real directory`);
  }
  return { canonical: await realpath(normalized), path: normalized };
}

async function validateFileUnderRoot(root, canonicalRoot, relativePath, label) {
  if (!safeRelativePath(relativePath)) throw verificationFailure(`${label} has an unsafe path: ${relativePath}`);
  let current = root;
  for (const segment of relativePath.split("/")) {
    current = join(current, segment);
    let info;
    try {
      info = await lstat(current);
    } catch (error) {
      throw verificationFailure(`${label} is missing: ${relativePath}`, error);
    }
    if (info.isSymbolicLink()) throw verificationFailure(`${label} is a reparse point: ${relativePath}`);
  }
  const info = await stat(current);
  if (!info.isFile()) throw verificationFailure(`${label} must be a file: ${relativePath}`);
  const canonical = await realpath(current);
  if (!withinRoot(canonicalRoot, canonical)) {
    throw verificationFailure(`${label} escapes the artifact root: ${relativePath}`);
  }
  return { info, path: current };
}

async function collectFiles(root) {
  const files = [];
  async function walk(current, prefix) {
    const entries = await readdir(current, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (!safeRelativePath(relativePath)) throw verificationFailure(`artifact contains an unsafe path: ${relativePath}`);
      const path = join(root, ...relativePath.split("/"));
      const info = await lstat(path);
      if (info.isSymbolicLink()) throw verificationFailure(`artifact contains a reparse point: ${relativePath}`);
      if (info.isDirectory()) await walk(path, relativePath);
      else if (info.isFile()) files.push(relativePath);
      else throw verificationFailure(`artifact contains an unsupported entry: ${relativePath}`);
    }
  }
  await walk(root, "");
  return files;
}

function assertSortedUnique(records, property, label) {
  const values = records.map((record) => record[property]);
  const expected = [...new Set(values)].sort((left, right) => left.localeCompare(right, "en"));
  if (expected.length !== values.length || expected.some((value, index) => value !== values[index])) {
    throw verificationFailure(`${label} must be sorted and unique`);
  }
}

async function readVerifiedManifest(artifactRoot) {
  const root = await validateDirectory(artifactRoot, "release artifact root");
  const manifestFile = await validateFileUnderRoot(
    root.path,
    root.canonical,
    "release-manifest.json",
    "release manifest",
  );
  let manifest;
  try {
    manifest = JSON.parse(await readFile(manifestFile.path, "utf8"));
  } catch (error) {
    throw verificationFailure("release manifest is not valid JSON", error);
  }
  if (!validateManifest(manifest)) {
    throw verificationFailure(`release manifest violates the closed contract: ${ajv.errorsText(validateManifest.errors)}`);
  }
  if (manifest.schemaVersion !== 1 || manifest.product !== "unit-test-ide") {
    throw verificationFailure("release manifest has an unsupported identity");
  }
  assertSortedUnique(manifest.artifacts, "id", "release manifest artifacts");
  assertSortedUnique(manifest.licenses, "path", "release manifest licenses");
  const expectedFiles = new Set(["release-manifest.json"]);
  const paths = new Set();
  for (const [label, records, property] of [
    ["artifact", manifest.artifacts, "relativePath"],
    ["license", manifest.licenses, "path"],
  ]) {
    for (const record of records) {
      const relativePath = record[property];
      if (paths.has(relativePath)) throw verificationFailure(`duplicate release payload path: ${relativePath}`);
      paths.add(relativePath);
      expectedFiles.add(relativePath);
      const file = await validateFileUnderRoot(root.path, root.canonical, relativePath, label);
      if (file.info.size !== record.size) throw verificationFailure(`${label} size does not match: ${relativePath}`);
      if (!digestPattern.test(record.sha256) || await sha256File(file.path) !== record.sha256) {
        throw verificationFailure(`${label} hash does not match: ${relativePath}`);
      }
    }
  }
  const actualFiles = (await collectFiles(root.path))
    .sort((left, right) => left.localeCompare(right, "en"));
  const expected = [...expectedFiles].sort((left, right) => left.localeCompare(right, "en"));
  if (actualFiles.length !== expected.length || actualFiles.some((value, index) => value !== expected[index])) {
    throw verificationFailure("release artifact file set is not closed");
  }
  return { manifest, root };
}

async function copyVerifiedArtifact(sourceRoot, destinationRoot, manifest) {
  await mkdir(destinationRoot, { recursive: false });
  const paths = [
    ...manifest.artifacts.map(({ relativePath }) => relativePath),
    ...manifest.licenses.map(({ path }) => path),
    "release-manifest.json",
  ].sort((left, right) => left.localeCompare(right, "en"));
  for (const relativePath of paths) {
    const source = join(sourceRoot, ...relativePath.split("/"));
    const destination = join(destinationRoot, ...relativePath.split("/"));
    await mkdir(dirname(destination), { recursive: true });
    await copyFile(source, destination);
    const sourceInfo = await stat(source);
    await chmod(destination, sourceInfo.mode);
  }
  await fsyncFile(join(destinationRoot, "release-manifest.json"));
}

async function writeOwnedMarker(root) {
  const markerPath = join(root, ownerMarkerName);
  await writeFile(markerPath, `${JSON.stringify(ownerMarker)}\n`, { flag: "wx" });
  await fsyncFile(markerPath);
  await fsyncDirectory(root);
}

async function ensureOwnedRoot(root) {
  const normalized = normalizeRoot(root);
  await assertUnredirectedParent(normalized);
  if (!await pathExists(normalized)) {
    await mkdir(normalized, { recursive: true });
    await writeOwnedMarker(normalized);
    return normalized;
  }
  const info = await lstat(normalized);
  if (info.isSymbolicLink() || !info.isDirectory()) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "package-owned root must be a real directory");
  }
  const markerPath = join(normalized, ownerMarkerName);
  if (!await pathExists(markerPath)) {
    const entries = await readdir(normalized);
    if (entries.length !== 0) {
      throw releaseError("RELEASE_ROOT_NOT_OWNED", "refusing to claim a non-empty unowned root");
    }
    await writeOwnedMarker(normalized);
    return normalized;
  }
  const markerInfo = await lstat(markerPath);
  if (markerInfo.isSymbolicLink() || !markerInfo.isFile()) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker is not a regular file");
  }
  let marker;
  try {
    marker = JSON.parse(await readFile(markerPath, "utf8"));
  } catch (error) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker is invalid", error);
  }
  if (
    marker?.schemaVersion !== ownerMarker.schemaVersion
    || marker?.product !== ownerMarker.product
    || Object.keys(marker).sort().join(",") !== "product,schemaVersion"
  ) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker has an unsupported identity");
  }
  return normalized;
}

async function requireOwnedRoot(root) {
  const normalized = normalizeRoot(root);
  await assertUnredirectedParent(normalized);
  if (!await pathExists(normalized)) return null;
  const markerPath = join(normalized, ownerMarkerName);
  const rootInfo = await lstat(normalized);
  if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory() || !await pathExists(markerPath)) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "refusing to remove an unowned root");
  }
  const markerInfo = await lstat(markerPath);
  if (markerInfo.isSymbolicLink() || !markerInfo.isFile()) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker is not a regular file");
  }
  let marker;
  try {
    marker = JSON.parse(await readFile(markerPath, "utf8"));
  } catch (error) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker is invalid", error);
  }
  if (
    marker?.schemaVersion !== 1
    || marker?.product !== "unit-test-ide"
    || Object.keys(marker).sort().join(",") !== "product,schemaVersion"
  ) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", "ownership marker has an unsupported identity");
  }
  return normalized;
}

async function withUpdateLock(root, run) {
  const lockPath = join(root, ".update.lock");
  let handle;
  try {
    handle = await open(lockPath, "wx");
    await handle.writeFile('{"product":"unit-test-ide","schemaVersion":1}\n');
    await handle.sync();
    await handle.close();
    handle = null;
  } catch (error) {
    await handle?.close();
    if (error?.code === "EEXIST") throw releaseError("RELEASE_UPDATE_BUSY", "another update transition is active");
    throw error;
  }
  try {
    return await run();
  } finally {
    await rm(lockPath, { force: true });
  }
}

async function readCurrent(root) {
  const currentPath = join(root, "current");
  if (!await pathExists(currentPath)) return null;
  const info = await lstat(currentPath);
  if (info.isSymbolicLink() || !info.isFile()) throw verificationFailure("current pointer is not a regular file");
  const value = (await readFile(currentPath, "utf8")).trim();
  if (!semverLike.test(value)) throw verificationFailure("current pointer is invalid");
  return value;
}

function comparePrerelease(left, right) {
  if (left === right) return 0;
  if (left === undefined) return 1;
  if (right === undefined) return -1;
  const leftParts = left.split(".");
  const rightParts = right.split(".");
  const length = Math.max(leftParts.length, rightParts.length);
  for (let index = 0; index < length; index += 1) {
    if (leftParts[index] === undefined) return -1;
    if (rightParts[index] === undefined) return 1;
    const leftNumeric = /^\d+$/u.test(leftParts[index]);
    const rightNumeric = /^\d+$/u.test(rightParts[index]);
    if (leftNumeric && rightNumeric) {
      const leftValue = BigInt(leftParts[index]);
      const rightValue = BigInt(rightParts[index]);
      if (leftValue !== rightValue) return leftValue < rightValue ? -1 : 1;
    } else if (leftNumeric !== rightNumeric) {
      return leftNumeric ? -1 : 1;
    } else {
      if (leftParts[index] !== rightParts[index]) {
        return leftParts[index] < rightParts[index] ? -1 : 1;
      }
    }
  }
  return 0;
}

function compareVersions(left, right) {
  const leftMatch = semverLike.exec(left);
  const rightMatch = semverLike.exec(right);
  if (!leftMatch || !rightMatch) throw verificationFailure("version is not semver-like");
  for (let index = 1; index <= 3; index += 1) {
    const leftValue = BigInt(leftMatch[index]);
    const rightValue = BigInt(rightMatch[index]);
    if (leftValue !== rightValue) return leftValue < rightValue ? -1 : 1;
  }
  return comparePrerelease(leftMatch[4], rightMatch[4]);
}

async function writeCurrent(root, version) {
  const temporaryPath = join(root, `.current-${randomUUID()}.tmp`);
  try {
    await writeFile(temporaryPath, `${version}\n`, { flag: "wx" });
    await fsyncFile(temporaryPath);
    await rename(temporaryPath, join(root, "current"));
    await fsyncDirectory(root);
  } catch (error) {
    await rm(temporaryPath, { force: true });
    throw error;
  }
}

async function requireOwnedDirectory(packageRoot, relativePath, { create = false } = {}) {
  if (!safeRelativePath(relativePath)) {
    throw releaseError("RELEASE_ROOT_NOT_OWNED", `package-owned path is unsafe: ${relativePath}`);
  }
  const canonicalRoot = await realpath(packageRoot);
  let current = packageRoot;
  for (const segment of relativePath.split("/")) {
    current = join(current, segment);
    let info;
    try {
      info = await lstat(current);
    } catch (error) {
      if (error?.code !== "ENOENT" || !create) throw error;
      await mkdir(current, { recursive: false });
      info = await lstat(current);
    }
    if (info.isSymbolicLink() || !info.isDirectory()) {
      throw releaseError("RELEASE_ROOT_NOT_OWNED", `package-owned path is redirected: ${relativePath}`);
    }
    const canonical = await realpath(current);
    if (!withinRoot(canonicalRoot, canonical) || !samePath(current, canonical)) {
      throw releaseError("RELEASE_ROOT_NOT_OWNED", `package-owned path escapes its root: ${relativePath}`);
    }
  }
  return current;
}

export async function installVersion(root, artifact) {
  const source = await readVerifiedManifest(artifact);
  const packageRoot = await ensureOwnedRoot(root);
  return withUpdateLock(packageRoot, async () => {
    const versionsRoot = await requireOwnedDirectory(packageRoot, "versions", { create: true });
    const previousVersion = await readCurrent(packageRoot);
    if (previousVersion !== null) {
      const previousRoot = await requireOwnedDirectory(packageRoot, `versions/${previousVersion}`);
      await readVerifiedManifest(previousRoot);
      if (compareVersions(source.manifest.version, previousVersion) < 0) {
        throw releaseError(
          "RELEASE_DOWNGRADE_REJECTED",
          `install ${source.manifest.version} is older than current ${previousVersion}`,
        );
      }
    }
    const destination = join(versionsRoot, source.manifest.version);
    if (await pathExists(destination)) {
      throw releaseError("RELEASE_VERSION_EXISTS", `version is already installed: ${source.manifest.version}`);
    }
    const temporary = join(versionsRoot, `.install-${source.manifest.version}-${randomUUID()}`);
    try {
      await copyVerifiedArtifact(source.root.path, temporary, source.manifest);
      await requireOwnedDirectory(packageRoot, `versions/${temporary.slice(versionsRoot.length + 1)}`);
      await readVerifiedManifest(temporary);
      await rename(temporary, destination);
      await requireOwnedDirectory(packageRoot, `versions/${source.manifest.version}`);
      await fsyncDirectory(versionsRoot);
      await writeCurrent(packageRoot, source.manifest.version);
    } catch (error) {
      await rm(temporary, { recursive: true, force: true });
      throw error;
    }
    return { previousVersion, version: source.manifest.version };
  });
}

export async function rollbackVersion(root, version) {
  if (typeof version !== "string" || !semverLike.test(version)) {
    throw verificationFailure("rollback version must be semver-like");
  }
  const packageRoot = await requireOwnedRoot(root);
  if (packageRoot === null) throw releaseError("RELEASE_ROOT_NOT_OWNED", "package-owned root is missing");
  return withUpdateLock(packageRoot, async () => {
    await requireOwnedDirectory(packageRoot, "versions");
    const previousVersion = await readCurrent(packageRoot);
    const targetRoot = await requireOwnedDirectory(packageRoot, `versions/${version}`);
    const target = await readVerifiedManifest(targetRoot);
    if (target.manifest.version !== version) throw verificationFailure("rollback target version does not match its directory");
    await writeCurrent(packageRoot, version);
    return { previousVersion, version };
  });
}

export async function uninstall(root) {
  const packageRoot = await requireOwnedRoot(root);
  if (packageRoot === null) return { removed: false };
  return withUpdateLock(packageRoot, async () => {
    if (await pathExists(join(packageRoot, "versions"))) {
      await requireOwnedDirectory(packageRoot, "versions");
    }
    await rm(packageRoot, { recursive: true, force: false });
    return { removed: true };
  });
}

function launcherRelativePath() {
  return process.platform === "win32"
    ? "app/code-oss-runtime/Code - OSS.exe"
    : "app/code-oss-runtime/code-oss";
}

function launchHandshake(packageRoot, version, userDataRoot) {
  const versionRoot = join(packageRoot, "versions", version);
  const executable = join(
    versionRoot,
    ...launcherRelativePath().split("/"),
  );
  const cli = join(
    versionRoot,
    "app",
    "code-oss-runtime",
    "resources",
    "app",
    "out",
    "cli.js",
  );
  return spawnSync(executable, [cli, "--version", "--user-data-dir", userDataRoot], {
    encoding: "utf8",
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: "1",
      VSCODE_DEV: "",
      HOME: userDataRoot,
      USERPROFILE: userDataRoot,
      XDG_CACHE_HOME: join(userDataRoot, "cache"),
      XDG_CONFIG_HOME: join(userDataRoot, "config"),
    },
    timeout: 30_000,
    windowsHide: true,
  });
}

function requireLaunchHandshake(result, label) {
  if (result.status !== 0 || typeof result.stdout !== "string" || result.stdout.trim().length === 0) {
    const errorMessage = typeof result.error?.message === "string" ? result.error.message.trim() : "";
    const stderr = typeof result.stderr === "string" ? result.stderr.trim() : "";
    const outcome = result.status === null
      ? (result.signal ? `signal ${result.signal}` : "exit unavailable")
      : `exit ${result.status}`;
    const stdout = typeof result.stdout === "string"
      ? (result.stdout.trim().length === 0 ? "stdout empty" : "stdout nonempty")
      : "stdout unavailable";
    const detail = errorMessage || stderr || `${outcome}; ${stdout}`;
    throw releaseError("RELEASE_SMOKE_FAILED", `${label} launch handshake failed: ${detail}`);
  }
}

async function triggerInstalledUpgradeLaunchFailure(root, version, userDataRoot) {
  if (typeof version !== "string" || !semverLike.test(version)) {
    throw verificationFailure("smoke target version must be semver-like");
  }
  const packageRoot = await requireOwnedRoot(root);
  if (packageRoot === null) throw releaseError("RELEASE_ROOT_NOT_OWNED", "package-owned root is missing");
  return withUpdateLock(packageRoot, async () => {
    if (await readCurrent(packageRoot) !== version) {
      throw releaseError("RELEASE_SMOKE_FAILED", "installed smoke target is not current");
    }
    const targetRoot = await requireOwnedDirectory(packageRoot, `versions/${version}`);
    await readVerifiedManifest(targetRoot);
    const launcher = join(targetRoot, ...launcherRelativePath().split("/"));
    await writeFile(launcher, Buffer.from([0x00, 0xff, 0x00, 0x7f, 0x55, 0xaa]));
    await fsyncFile(launcher);
    const result = launchHandshake(packageRoot, version, userDataRoot);
    if (result.status === 0) {
      throw releaseError("RELEASE_SMOKE_FAILED", "expected corrupted upgrade launcher failure was not observed");
    }
    return result;
  });
}

export async function runSmokeLifecycle({
  artifact,
  baselineArtifact,
  baselineManifestSha256,
  baselinePackagePath,
  baselinePackageSha256,
  evidence,
  manifestSha256,
  packagePath,
  packageSha256,
  platform,
  root,
  version,
}, { launch = launchHandshake } = {}) {
  if (!["windows", "linux"].includes(platform)) throw releaseError("RELEASE_SMOKE_INVALID", "unsupported platform");
  if ((platform === "windows") !== (process.platform === "win32")) {
    throw releaseError("RELEASE_SMOKE_INVALID", `platform ${platform} does not match this host`);
  }
  if (
    typeof packagePath !== "string" || packagePath.trim().length === 0
    || typeof baselinePackagePath !== "string" || baselinePackagePath.trim().length === 0
  ) {
    throw releaseError("RELEASE_SMOKE_INVALID", "package and baseline package inputs are required");
  }
  if (
    !digestPattern.test(packageSha256 ?? "")
    || !digestPattern.test(manifestSha256 ?? "")
    || !digestPattern.test(baselinePackageSha256 ?? "")
    || !digestPattern.test(baselineManifestSha256 ?? "")
  ) {
    throw releaseError("RELEASE_SMOKE_INVALID", "package, manifest, and baseline SHA-256 digests are required");
  }
  if (typeof evidence !== "string" || evidence.trim().length === 0) {
    throw releaseError("RELEASE_SMOKE_INVALID", "evidence output is required");
  }
  if (typeof version !== "string" || !semverLike.test(version)) {
    throw releaseError("RELEASE_SMOKE_INVALID", "package version must be semver-like");
  }
  const packageParent = await validateDirectory(dirname(resolve(packagePath)), "package parent");
  const packageFile = await validateFileUnderRoot(
    packageParent.path,
    packageParent.canonical,
    basename(resolve(packagePath)),
    "package",
  );
  if (await sha256File(packageFile.path) !== packageSha256) {
    throw releaseError("RELEASE_SMOKE_INVALID", "package SHA-256 does not match the downloaded artifact");
  }
  const baselinePackageParent = await validateDirectory(dirname(resolve(baselinePackagePath)), "baseline package parent");
  const baselinePackageFile = await validateFileUnderRoot(
    baselinePackageParent.path,
    baselinePackageParent.canonical,
    basename(resolve(baselinePackagePath)),
    "baseline package",
  );
  if (await sha256File(baselinePackageFile.path) !== baselinePackageSha256) {
    throw releaseError("RELEASE_SMOKE_INVALID", "baseline package SHA-256 does not match the downloaded artifact");
  }
  const baseline = await readVerifiedManifest(baselineArtifact);
  const target = await readVerifiedManifest(artifact);
  if (target.manifest.version !== version || target.manifest.platform !== platform) {
    throw releaseError("RELEASE_SMOKE_INVALID", "extracted payload identity does not match package inputs");
  }
  if (baseline.manifest.platform !== platform || compareVersions(baseline.manifest.version, version) >= 0) {
    throw releaseError("RELEASE_SMOKE_INVALID", "baseline payload must be an older package for the same platform");
  }
  if (baseline.manifest.architecture !== target.manifest.architecture) {
    throw releaseError("RELEASE_SMOKE_INVALID", "baseline payload architecture does not match the target release");
  }
  if (await sha256File(join(target.root.path, "release-manifest.json")) !== manifestSha256) {
    throw releaseError("RELEASE_SMOKE_INVALID", "manifest SHA-256 does not match the extracted package payload");
  }
  if (await sha256File(join(baseline.root.path, "release-manifest.json")) !== baselineManifestSha256) {
    throw releaseError("RELEASE_SMOKE_INVALID", "baseline manifest SHA-256 does not match the extracted package payload");
  }
  const sourceCommit = target.manifest.sourceCommit;
  const containerRoot = normalizeRoot(root);
  await mkdir(containerRoot, { recursive: true });
  const packageRoot = join(containerRoot, "package-owned");
  const workspaceRoot = join(containerRoot, "workspace");
  const userData = join(workspaceRoot, "project", "tests.cpp");
  await mkdir(dirname(userData), { recursive: true });
  await writeFile(userData, "preserve user workspace data\n");

  await installVersion(packageRoot, baseline.root.path);
  const firstLaunch = launch(packageRoot, baseline.manifest.version, workspaceRoot);
  requireLaunchHandshake(firstLaunch, "first");

  await installVersion(packageRoot, target.root.path);
  if ((await readCurrent(packageRoot)) !== version) {
    throw releaseError("RELEASE_SMOKE_FAILED", "upgrade did not switch current");
  }
  const failedUpgradeLaunch = await triggerInstalledUpgradeLaunchFailure(packageRoot, version, workspaceRoot);
  if (failedUpgradeLaunch.status === 0) {
    throw releaseError("RELEASE_SMOKE_FAILED", "expected corrupted upgrade launcher failure was not observed");
  }
  await rollbackVersion(packageRoot, baseline.manifest.version);
  requireLaunchHandshake(launch(packageRoot, baseline.manifest.version, workspaceRoot), "rollback");
  await rollbackVersion(packageRoot, baseline.manifest.version);
  if ((await readCurrent(packageRoot)) !== baseline.manifest.version) {
    throw releaseError("RELEASE_SMOKE_FAILED", "repeated rollback changed the target");
  }
  await uninstall(packageRoot);
  const packageResidueAbsent = !await pathExists(packageRoot);
  const userDataPreserved = await readFile(userData, "utf8") === "preserve user workspace data\n";
  if (!packageResidueAbsent || !userDataPreserved) {
    throw releaseError("RELEASE_SMOKE_FAILED", "uninstall boundary verification failed");
  }
  const result = {
    schemaVersion: 1,
    product: "unit-test-ide",
    platform,
    architecture: target.manifest.architecture,
    sourceCommit,
    generatedAt: target.manifest.generatedAt,
    packageFilename: basename(packageFile.path),
    version,
    packageSha256,
    manifestSha256,
    rollbackVersion: baseline.manifest.version,
    rollbackPackageFilename: basename(baselinePackageFile.path),
    rollbackPackageSha256: baselinePackageSha256,
    rollbackManifestSha256: baselineManifestSha256,
    outcomes: {
      install: "pass",
      launchHandshake: "pass",
      upgrade: "pass",
      upgradeLaunch: "failed-as-expected",
      rollback: "pass",
      rollbackLaunch: "pass",
      repeatedRollback: "pass",
      uninstall: "pass",
      userDataPreserved: "pass",
      packageResidueAbsent: "pass",
    },
  };
  await mkdir(dirname(resolve(evidence)), { recursive: true });
  await writeFile(resolve(evidence), `${JSON.stringify(result, null, 2)}\n`);
  await rm(workspaceRoot, { recursive: true, force: true });
  return result;
}

function parseSmokeCli(argv) {
  if (argv[0] !== "smoke") throw releaseError("RELEASE_SMOKE_INVALID", "expected smoke command");
  const values = {};
  const allowed = new Set([
    "--artifact",
    "--baseline-artifact",
    "--baseline-manifest-sha256",
    "--baseline-package",
    "--baseline-package-sha256",
    "--evidence",
    "--manifest-sha256",
    "--package",
    "--package-sha256",
    "--platform",
    "--root",
    "--version",
  ]);
  for (let index = 1; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!allowed.has(flag)) throw releaseError("RELEASE_SMOKE_INVALID", `unknown flag: ${flag}`);
    if (!value || value.startsWith("--")) throw releaseError("RELEASE_SMOKE_INVALID", `missing value for ${flag}`);
    if (Object.hasOwn(values, flag)) throw releaseError("RELEASE_SMOKE_INVALID", `duplicate flag: ${flag}`);
    values[flag] = value;
  }
  for (const required of [
    "--artifact",
    "--baseline-artifact",
    "--baseline-manifest-sha256",
    "--baseline-package",
    "--baseline-package-sha256",
    "--evidence",
    "--manifest-sha256",
    "--package",
    "--package-sha256",
    "--platform",
    "--root",
    "--version",
  ]) {
    if (!values[required]) throw releaseError("RELEASE_SMOKE_INVALID", `${required} is required`);
  }
  return {
    artifact: values["--artifact"],
    baselineArtifact: values["--baseline-artifact"],
    baselineManifestSha256: values["--baseline-manifest-sha256"],
    baselinePackagePath: values["--baseline-package"],
    baselinePackageSha256: values["--baseline-package-sha256"],
    evidence: values["--evidence"],
    manifestSha256: values["--manifest-sha256"],
    packagePath: values["--package"],
    packageSha256: values["--package-sha256"],
    platform: values["--platform"],
    root: values["--root"],
    version: values["--version"],
  };
}

async function main(argv) {
  const input = parseSmokeCli(argv);
  await runSmokeLifecycle(input);
  process.stdout.write(`${resolve(input.evidence)}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
