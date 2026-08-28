import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { lstat, mkdir, mkdtemp, open, readFile, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

import { validateCodeOssRuntime } from "../code-oss-runtime.mjs";
import { validateRuntimeModeInventory } from "../linux/runtime-mode-inventory.mjs";
import { isPortableReleasePath } from "../portable-path.mjs";

const digestPattern = /^[0-9a-f]{64}$/u;
const inventoryKeys = ["architecture", "files", "launcherRelativePath", "launcherSha256", "platform", "schemaVersion", "totalBytes", "treeDigest"].sort();
const recordKeys = ["executable", "path", "sha256", "size"].sort();
const layouts = {
  windows: "Code - OSS.exe",
  linux: "code-oss",
};
const codeOssIdentity = {
  applicationName: "code-oss",
  licenseName: "MIT",
  nameShort: "Code - OSS",
};
const execFileAsync = promisify(execFile);
const windowsReparsePointCommand = "$root=New-Object IO.DirectoryInfo($env:CODE_OSS_RUNTIME_ROOT); function Test-ReparsePoint([IO.FileSystemInfo]$node) { if (($node.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { return $true }; if ($node -is [IO.DirectoryInfo]) { foreach ($child in $node.EnumerateFileSystemInfos()) { if (Test-ReparsePoint $child) { return $true } } }; return $false }; [Console]::Out.Write([int](Test-ReparsePoint $root))";
const noTestHooks = Object.freeze({});

function inputError(code, message) {
  const error = new Error(`${code}: ${message}`);
  error.code = code;
  return error;
}

function outputError(message) {
  return inputError("RELEASE_PRODUCER_OUTPUT_INVALID", message);
}

function isPlainObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function hasExactKeys(value, expected) {
  if (!isPlainObject(value)) return false;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) return false;
  keys.sort();
  return keys.length === expected.length && keys.every((key, index) => key === expected[index]);
}

function closedTestHooks(value, allowedKeys) {
  if (value === undefined) return noTestHooks;
  if (!isPlainObject(value)) throw inputError("RELEASE_INPUT_INVALID", "test hooks must be a closed object");
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !allowedKeys.has(key) || typeof value[key] !== "function") {
      throw inputError("RELEASE_INPUT_INVALID", "test hooks must be a closed object");
    }
  }
  return Object.freeze({ ...value });
}

function comparePaths(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function withinRoot(rootPath, candidatePath) {
  const child = relative(rootPath, candidatePath);
  return child === "" || (!child.startsWith("..") && !isAbsolute(child));
}

async function assertNoWindowsReparsePoints(rootPath) {
  if (process.platform !== "win32") return;
  try {
    const { stdout } = await execFileAsync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", windowsReparsePointCommand], { encoding: "utf8", env: { ...process.env, CODE_OSS_RUNTIME_ROOT: rootPath }, windowsHide: true });
    if (stdout.trim() === "1") throw inputError("RELEASE_INPUT_INVALID", "runtime tree contains a reparse point");
  } catch (error) {
    if (error?.code === "RELEASE_INPUT_INVALID") throw error;
    throw inputError("RELEASE_INPUT_INVALID", "runtime tree reparse-point state cannot be inspected");
  }
}

function sameSnapshot(left, right) {
  return left.isFile() && (typeof right.isFile !== "function" || right.isFile()) && left.dev === right.dev && left.ino === right.ino && left.size === BigInt(right.size) && left.mode === right.mode && left.mtimeNs === right.mtimeNs && left.ctimeNs === right.ctimeNs;
}

async function hashScannedFile(file, { captureBytes = false, hooks = noTestHooks } = {}) {
  const hash = createHash("sha256");
  let handle;
  try {
    handle = await open(file.canonicalPath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!sameSnapshot(before, file)) throw new Error("changed");
    await hooks.afterOpenSnapshot?.(Object.freeze({ path: file.path }));
    let counted = 0;
    const chunks = [];
    await new Promise((resolveHash, rejectHash) => {
      const stream = handle.createReadStream({ autoClose: false });
      stream.on("data", (chunk) => { counted += chunk.length; hash.update(chunk); if (captureBytes) chunks.push(chunk); });
      stream.on("error", rejectHash);
      stream.on("end", resolveHash);
    });
    const after = await handle.stat({ bigint: true });
    if (!sameSnapshot(after, file) || !sameSnapshot(before, after) || counted !== Number(before.size)) throw new Error("changed");
    return { sha256: hash.digest("hex"), bytes: captureBytes ? Buffer.concat(chunks) : undefined };
  } finally {
    await handle?.close().catch(() => {});
  }
}

async function scanRuntimeTree(root) {
  const rootPath = resolve(root);
  let rootInfo;
  let canonicalRoot;
  try {
    rootInfo = await lstat(rootPath);
    canonicalRoot = await realpath(rootPath);
  } catch (error) {
    if (error?.code === "ENOENT") throw inputError("RELEASE_INPUT_MISSING", "runtime root is required");
    throw inputError("RELEASE_INPUT_INVALID", "runtime root cannot be inspected");
  }
  if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory()) {
    throw inputError("RELEASE_INPUT_INVALID", "runtime root must be a real directory");
  }
  await assertNoWindowsReparsePoints(rootPath);

  const files = [];
  const aliases = new Map();
  async function scanDirectory(directoryPath, relativeDirectory = "") {
    let canonicalDirectory;
    let entries;
    try {
      canonicalDirectory = await realpath(directoryPath);
      entries = await readdir(directoryPath, { withFileTypes: true });
    } catch {
      throw inputError("RELEASE_INPUT_INVALID", "runtime tree cannot be read");
    }
    if (!withinRoot(canonicalRoot, canonicalDirectory)) {
      throw inputError("RELEASE_INPUT_INVALID", "runtime tree escapes the runtime root");
    }
    entries.sort((left, right) => comparePaths(left.name, right.name));
    for (const entry of entries) {
      const relativePath = relativeDirectory ? `${relativeDirectory}/${entry.name}` : entry.name;
      if (!isPortableReleasePath(relativePath)) {
        throw inputError("RELEASE_INPUT_INVALID", "runtime tree contains a non-portable path");
      }
      const alias = relativePath.toLowerCase();
      if (aliases.has(alias)) throw inputError("RELEASE_INPUT_INVALID", "runtime tree contains a case-insensitive path alias");
      aliases.set(alias, relativePath);

      const entryPath = join(directoryPath, entry.name);
      let info;
      let canonicalEntry;
      try {
        info = await lstat(entryPath, { bigint: true });
        canonicalEntry = await realpath(entryPath);
      } catch {
        throw inputError("RELEASE_INPUT_INVALID", "runtime tree entry cannot be resolved");
      }
      if (entry.isSymbolicLink() || info.isSymbolicLink()) {
        throw inputError("RELEASE_INPUT_INVALID", "runtime tree contains a symbolic link or reparse point");
      }
      if (!withinRoot(canonicalRoot, canonicalEntry)) {
        throw inputError("RELEASE_INPUT_INVALID", "runtime tree entry escapes the runtime root");
      }
      if (info.isDirectory()) {
        await scanDirectory(entryPath, relativePath);
      } else if (info.isFile()) {
        if (info.size < 0n || info.size > BigInt(Number.MAX_SAFE_INTEGER)) {
          throw inputError("RELEASE_INPUT_INVALID", "runtime file size is invalid");
        }
        files.push({ path: relativePath, canonicalPath: canonicalEntry, size: Number(info.size), mode: info.mode, dev: info.dev, ino: info.ino, mtimeNs: info.mtimeNs, ctimeNs: info.ctimeNs });
      } else {
        throw inputError("RELEASE_INPUT_INVALID", "runtime tree contains a special entry");
      }
    }
  }
  await scanDirectory(rootPath);
  files.sort((left, right) => comparePaths(left.path, right.path));
  return { rootPath, files };
}

function treeDigest(files) {
  const hash = createHash("sha256");
  for (const record of files) {
    hash.update(record.path, "utf8");
    hash.update("\0", "utf8");
    hash.update(String(record.size), "utf8");
    hash.update("\0", "utf8");
    hash.update(record.sha256, "utf8");
    hash.update("\0", "utf8");
    hash.update(record.executable ? "1" : "0", "utf8");
    hash.update("\0", "utf8");
  }
  return hash.digest("hex");
}

function assertInventoryShape(inventory) {
  if (
    !hasExactKeys(inventory, inventoryKeys)
    || inventory.schemaVersion !== 1
    || !Object.hasOwn(layouts, inventory.platform)
    || inventory.architecture !== "x64"
    || inventory.launcherRelativePath !== layouts[inventory.platform]
    || typeof inventory.launcherSha256 !== "string"
    || !digestPattern.test(inventory.launcherSha256)
    || !Array.isArray(inventory.files)
    || !Number.isSafeInteger(inventory.totalBytes)
    || inventory.totalBytes < 0
    || typeof inventory.treeDigest !== "string"
    || !digestPattern.test(inventory.treeDigest)
  ) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory has an invalid closed schema");
  let total = 0;
  let previous;
  const aliases = new Set();
  let launcher;
  for (const record of inventory.files) {
    if (!hasExactKeys(record, recordKeys) || !isPortableReleasePath(record.path) || !Number.isSafeInteger(record.size) || record.size < 0 || typeof record.sha256 !== "string" || !digestPattern.test(record.sha256) || typeof record.executable !== "boolean") {
      throw inputError("RELEASE_INPUT_INVALID", "runtime inventory contains an invalid file record");
    }
    if (previous !== undefined && comparePaths(previous, record.path) >= 0) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory file records must be strictly sorted");
    previous = record.path;
    const alias = record.path.toLowerCase();
    if (aliases.has(alias)) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory contains duplicate or aliased paths");
    aliases.add(alias);
    if (!Number.isSafeInteger(total + record.size)) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory total size overflows");
    total += record.size;
    if (record.path === inventory.launcherRelativePath) launcher = record;
  }
  if (total !== inventory.totalBytes || treeDigest(inventory.files) !== inventory.treeDigest || launcher?.sha256 !== inventory.launcherSha256) {
    throw inputError("RELEASE_INPUT_INVALID", "runtime inventory values are inconsistent");
  }
  return inventory;
}

async function validateLinuxRuntimeWhenPossible({ root, expectedLauncherSha256, platform }) {
  if (platform === "windows" || process.platform === "linux") {
    await validateCodeOssRuntime({ root, platform, expectedLauncherSha256 });
    return;
  }
  // Windows cannot truthfully report Linux execute bits. The validated mode
  // inventory below binds those bits while the scanner still verifies every byte.
  if (typeof expectedLauncherSha256 !== "string" || !digestPattern.test(expectedLauncherSha256)) {
    throw inputError("RELEASE_INPUT_INVALID", "launcher digest must be a lowercase SHA-256");
  }
}

function assertCodeOssIdentity(records, productBytes, platform, expectedLauncherSha256) {
  const byPath = new Map(records.map((record) => [record.path, record]));
  const launcher = byPath.get(layouts[platform]);
  if (launcher === undefined || productBytes === undefined || !byPath.has("resources/app/package.json")) {
    throw inputError("RELEASE_INPUT_MISSING", "runtime tree does not contain required Code - OSS files");
  }
  let productMetadata;
  try {
    productMetadata = JSON.parse(productBytes.toString("utf8"));
  } catch {
    throw inputError("RELEASE_INPUT_INVALID", "product metadata must be valid JSON");
  }
  if (!isPlainObject(productMetadata) || Object.entries(codeOssIdentity).some(([key, value]) => productMetadata[key] !== value)) {
    throw inputError("RELEASE_INPUT_INVALID", "product metadata does not identify Code - OSS");
  }
  if (launcher.sha256 !== expectedLauncherSha256) {
    throw inputError("RELEASE_INPUT_DIGEST_MISMATCH", "platform launcher digest does not match");
  }
}

async function createRuntimeInventoryInternal(request, hooks) {
  if (!isPlainObject(request)) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory request must be a closed object");
  const { root, platform, expectedLauncherSha256, modeInventory } = request;
  const requiredKeys = platform === "linux" ? ["expectedLauncherSha256", "modeInventory", "platform", "root"] : ["expectedLauncherSha256", "platform", "root"];
  if (!hasExactKeys(request, requiredKeys)) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory request must be a closed object");
  if (!Object.hasOwn(layouts, platform) || typeof root !== "string" || root.length === 0) {
    throw inputError("RELEASE_INPUT_INVALID", "runtime root and platform are required");
  }
  if (platform === "windows" && modeInventory !== undefined) {
    throw inputError("RELEASE_INPUT_INVALID", "mode inventory is supported only for Linux");
  }
  if (platform === "linux") validateRuntimeModeInventory(modeInventory, expectedLauncherSha256);
  await validateLinuxRuntimeWhenPossible({ root, expectedLauncherSha256, platform });
  const tree = await scanRuntimeTree(root);
  const modeRecords = platform === "linux" ? new Map(modeInventory.files.map((record) => [record.path, record])) : undefined;
  if (platform === "linux" && tree.files.length !== modeRecords.size) {
    throw inputError("RELEASE_INPUT_INVALID", "runtime file set does not match the mode inventory");
  }

  const records = [];
  let totalBytes = 0;
  let productBytes;
  for (const file of tree.files) {
    const modeRecord = modeRecords?.get(file.path);
    if (platform === "linux" && modeRecord === undefined) {
      throw inputError("RELEASE_INPUT_MISSING", "runtime file listed by the mode inventory is missing");
    }
    if (!Number.isSafeInteger(totalBytes + file.size)) throw inputError("RELEASE_INPUT_INVALID", "runtime inventory total size overflows");
    let hashed;
    try {
      if (file.path === "resources/app/product.json" && file.size > 1024 * 1024) throw new Error("large");
      hashed = await hashScannedFile(file, { captureBytes: file.path === "resources/app/product.json", hooks });
    } catch {
      throw inputError("RELEASE_INPUT_INVALID", "runtime file cannot be read");
    }
    if (modeRecord && (modeRecord.size !== file.size || modeRecord.sha256 !== hashed.sha256)) {
      throw inputError(modeRecord.size !== file.size ? "RELEASE_INPUT_INVALID" : "RELEASE_INPUT_DIGEST_MISMATCH", "runtime file does not match the mode inventory");
    }
    if (file.path === "resources/app/product.json") productBytes = hashed.bytes;
    records.push({ path: file.path, size: file.size, sha256: hashed.sha256, executable: modeRecord?.executable ?? false });
    totalBytes += file.size;
  }
  assertCodeOssIdentity(records, productBytes, platform, expectedLauncherSha256);
  const inventory = {
    schemaVersion: 1,
    platform,
    architecture: "x64",
    launcherRelativePath: layouts[platform],
    launcherSha256: expectedLauncherSha256,
    files: records,
    totalBytes,
    treeDigest: treeDigest(records),
  };
  return assertInventoryShape(inventory);
}

export async function createRuntimeInventory(request = {}) {
  return createRuntimeInventoryInternal(request, noTestHooks);
}

export function summarizeRuntimeInventory(inventory) {
  const valid = assertInventoryShape(inventory);
  return {
    schemaVersion: valid.schemaVersion,
    platform: valid.platform,
    architecture: valid.architecture,
    launcherRelativePath: valid.launcherRelativePath,
    launcherSha256: valid.launcherSha256,
    fileCount: valid.files.length,
    totalBytes: valid.totalBytes,
    treeDigest: valid.treeDigest,
  };
}

function comparablePath(path) {
  const absolute = resolve(path);
  return process.platform === "win32" ? absolute.toLowerCase() : absolute;
}

function snapshotIdentity(info) {
  return info === undefined ? undefined : { dev: info.dev, ino: info.ino };
}

function sameIdentity(left, right) {
  return left === undefined ? right === undefined : right !== undefined && left.dev === right.dev && left.ino === right.ino;
}

async function ensureRealOutputDirectory(path) {
  const directory = resolve(path);
  const missing = [];
  let current = directory;
  while (true) {
    try {
      const info = await lstat(current, { bigint: true });
      if (info.isSymbolicLink() || !info.isDirectory()) throw new Error("unsafe output directory");
      const canonical = await realpath(current);
      if (comparablePath(canonical) !== comparablePath(current)) throw new Error("unsafe output directory");
      break;
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
      const parent = dirname(current);
      if (parent === current) throw error;
      missing.push(basename(current));
      current = parent;
    }
  }
  for (const component of missing.reverse()) {
    current = join(current, component);
    await mkdir(current);
    const info = await lstat(current, { bigint: true });
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error("unsafe output directory");
    const canonical = await realpath(current);
    if (comparablePath(canonical) !== comparablePath(current)) throw new Error("unsafe output directory");
  }
  return directory;
}

async function inspectOutput(path) {
  if (typeof path !== "string" || path.length === 0) throw outputError("output path is required");
  try {
    const target = resolve(path);
    const directory = await ensureRealOutputDirectory(dirname(target));
    const directoryInfo = await lstat(directory, { bigint: true });
    const existing = await lstat(target, { bigint: true }).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
    if (existing?.isSymbolicLink() || (existing && !existing.isFile())) throw new Error("invalid output");
    return { target, directory, directoryIdentity: snapshotIdentity(directoryInfo), existing: snapshotIdentity(existing) };
  } catch (error) {
    if (error?.code === "RELEASE_PRODUCER_OUTPUT_INVALID") throw error;
    throw outputError("output file cannot be written");
  }
}

async function inspectDistinctOutputs(firstPath, secondPath) {
  if (comparablePath(firstPath) === comparablePath(secondPath)) throw outputError("output paths must be distinct");
  const first = await inspectOutput(firstPath);
  const second = await inspectOutput(secondPath);
  if (first.existing !== undefined && sameIdentity(first.existing, second.existing)) {
    throw outputError("output paths must be distinct");
  }
  return [first, second];
}

async function stageCanonicalJson(output, value, index, hooks) {
  let temporaryDirectory;
  try {
    await hooks.beforeStage?.(Object.freeze({ index, target: output.target }));
    await assertUnchangedOutputDirectory(output);
    temporaryDirectory = await mkdtemp(join(output.directory, ".release-runtime-"));
    await assertUnchangedOutputDirectory(output);
    const temporary = join(temporaryDirectory, "payload.json");
    await writeFile(temporary, `${JSON.stringify(value)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
    const temporaryInfo = await lstat(temporary, { bigint: true });
    if (temporaryInfo.isSymbolicLink() || !temporaryInfo.isFile()) throw new Error("invalid temporary output");
    return {
      ...output,
      temporary,
      temporaryDirectory,
      backup: join(temporaryDirectory, "previous.json"),
      backedUp: false,
      committed: false,
      published: undefined,
      temporaryIdentity: snapshotIdentity(temporaryInfo),
    };
  } catch {
    if (temporaryDirectory && await assertUnchangedOutputDirectory(output).then(() => true, () => false)) {
      await rm(temporaryDirectory, { recursive: true, force: true }).catch(() => {});
    }
    throw outputError("output file cannot be written");
  }
}

async function assertUnchangedOutputDirectory(item) {
  const current = await lstat(item.directory, { bigint: true });
  if (current.isSymbolicLink() || !current.isDirectory() || !sameIdentity(item.directoryIdentity, snapshotIdentity(current))) {
    throw new Error("output directory changed");
  }
  if (comparablePath(await realpath(item.directory)) !== comparablePath(item.directory)) throw new Error("output directory changed");
}

async function assertTargetIdentity(item, expected) {
  await assertUnchangedOutputDirectory(item);
  const current = await lstat(item.target, { bigint: true }).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
  if (current?.isSymbolicLink() || (current && !current.isFile()) || !sameIdentity(expected, snapshotIdentity(current))) {
    throw new Error("output target changed");
  }
}

async function assertUnchangedOutput(item) {
  await assertTargetIdentity(item, item.existing);
}

async function commitCanonicalPair(first, second, hooks) {
  const items = [first, second];
  try {
    for (const [index, item] of items.entries()) {
      await assertUnchangedOutput(item);
      if (item.existing !== undefined) {
        await hooks.beforeCommit?.(Object.freeze({ index, phase: "backup", target: item.target }));
        await assertUnchangedOutput(item);
        await rename(item.target, item.backup);
        item.backedUp = true;
      }
      await hooks.beforeCommit?.(Object.freeze({ index, phase: "publish", target: item.target }));
      await assertTargetIdentity(item, undefined);
      const temporary = await lstat(item.temporary, { bigint: true });
      if (temporary.isSymbolicLink() || !temporary.isFile() || !sameIdentity(item.temporaryIdentity, snapshotIdentity(temporary))) throw new Error("staged output changed");
      await rename(item.temporary, item.target);
      item.committed = true;
      item.published = item.temporaryIdentity;
      await hooks.afterPublish?.(Object.freeze({ index, target: item.target }));
      await assertTargetIdentity(item, item.published);
    }
  } catch {
    for (const item of [...items].reverse()) {
      const directorySafe = await assertUnchangedOutputDirectory(item).then(() => true, () => false);
      if (!directorySafe) continue;
      if (item.committed) {
        const current = await lstat(item.target, { bigint: true }).catch(() => undefined);
        if (sameIdentity(item.published, snapshotIdentity(current))) await rm(item.target, { force: true }).catch(() => {});
      }
      if (item.backedUp) {
        try {
          await rename(item.backup, item.target);
          item.backedUp = false;
        } catch {
          // Retain the private stage directory rather than delete the only
          // preserved copy of a pre-existing target after a rollback failure.
        }
      }
    }
    throw outputError("output file cannot be written");
  } finally {
    await Promise.all(items.filter((item) => !item.backedUp).map(async (item) => {
      if (await assertUnchangedOutputDirectory(item).then(() => true, () => false)) {
        await rm(item.temporaryDirectory, { recursive: true, force: true }).catch(() => {});
      }
    }));
  }
}

function parseCliArguments(argumentsList) {
  if (argumentsList[0] !== "create") throw inputError("RELEASE_INPUT_INVALID", "CLI requires the create command");
  const flags = new Map();
  const allowed = new Set(["--platform", "--root", "--launcher-sha256", "--mode-inventory", "--out", "--summary-out"]);
  for (let index = 1; index < argumentsList.length; index += 2) {
    const flag = argumentsList[index];
    const value = argumentsList[index + 1];
    if (!allowed.has(flag) || value === undefined || flags.has(flag)) throw inputError("RELEASE_INPUT_INVALID", "create requires fixed valid flags");
    flags.set(flag, value);
  }
  if (!flags.has("--platform") || !flags.has("--root") || !flags.has("--launcher-sha256") || !flags.has("--out") || !flags.has("--summary-out")) {
    throw inputError("RELEASE_INPUT_INVALID", "create requires --platform, --root, --launcher-sha256, --out, and --summary-out");
  }
  const platform = flags.get("--platform");
  if (!Object.hasOwn(layouts, platform) || (platform === "linux") !== flags.has("--mode-inventory")) {
    throw inputError("RELEASE_INPUT_INVALID", "Linux create requires --mode-inventory and Windows create forbids it");
  }
  return { platform, root: flags.get("--root"), expectedLauncherSha256: flags.get("--launcher-sha256"), modeInventoryPath: flags.get("--mode-inventory"), out: flags.get("--out"), summaryOut: flags.get("--summary-out") };
}

async function loadModeInventory(path, expectedLauncherSha256) {
  try {
    const info = await lstat(resolve(path));
    if (info.isSymbolicLink() || !info.isFile()) throw new Error("invalid");
    return validateRuntimeModeInventory(JSON.parse(await readFile(path, "utf8")), expectedLauncherSha256);
  } catch (error) {
    if (error?.code?.startsWith("RELEASE_INPUT_")) throw error;
    throw inputError("RELEASE_INPUT_INVALID", "runtime mode inventory must be a real valid JSON file");
  }
}

async function createCliOutputs(argumentsList, hooks = noTestHooks) {
  const options = parseCliArguments(argumentsList);
  const modeInventory = options.modeInventoryPath ? await loadModeInventory(options.modeInventoryPath, options.expectedLauncherSha256) : undefined;
  const outputs = await inspectDistinctOutputs(options.out, options.summaryOut);
  const inventory = await createRuntimeInventoryInternal({
    root: options.root,
    platform: options.platform,
    expectedLauncherSha256: options.expectedLauncherSha256,
    ...(modeInventory ? { modeInventory } : {}),
  }, noTestHooks);
  const values = [inventory, summarizeRuntimeInventory(inventory)];
  const results = await Promise.allSettled(outputs.map((output, index) => stageCanonicalJson(output, values[index], index, hooks)));
  const staged = results.filter((result) => result.status === "fulfilled").map((result) => result.value);
  const rejected = results.find((result) => result.status === "rejected");
  if (rejected !== undefined) {
    await Promise.all(staged.map(async (item) => {
      if (await assertUnchangedOutputDirectory(item).then(() => true, () => false)) {
        await rm(item.temporaryDirectory, { recursive: true, force: true }).catch(() => {});
      }
    }));
    throw rejected.reason?.code === "RELEASE_PRODUCER_OUTPUT_INVALID" ? rejected.reason : outputError("output file cannot be written");
  }
  await commitCanonicalPair(staged[0], staged[1], hooks);
}

async function runCli() {
  try {
    await createCliOutputs(process.argv.slice(2));
  } catch (error) {
    process.stderr.write(`${error?.message ?? "RELEASE_PRODUCER_OUTPUT_INVALID: runtime inventory creation failed"}\n`);
    process.exitCode = 1;
  }
}

export const __testOnlyRuntimeInventory = Object.freeze({
  createRuntimeInventory(request, hooks) {
    return createRuntimeInventoryInternal(request, closedTestHooks(hooks, new Set(["afterOpenSnapshot"])));
  },
  createCliOutputs(argumentsList, hooks) {
    if (!Array.isArray(argumentsList) || argumentsList.some((value) => typeof value !== "string")) {
      return Promise.reject(inputError("RELEASE_INPUT_INVALID", "test CLI arguments must be strings"));
    }
    return createCliOutputs([...argumentsList], closedTestHooks(hooks, new Set(["afterPublish", "beforeCommit", "beforeStage"])));
  },
});

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await runCli();
