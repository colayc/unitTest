import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { chmod, lstat, readFile, readdir, realpath } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { validateCodeOssRuntime } from "../code-oss-runtime.mjs";
import { isPortableReleasePath } from "../portable-path.mjs";

const digestPattern = /^[0-9a-f]{64}$/u;
const inventoryKeys = [
  "schemaVersion",
  "platform",
  "architecture",
  "launcherRelativePath",
  "launcherSha256",
  "files",
].sort();
const fileKeys = ["path", "size", "sha256", "executable"].sort();

function releaseInputError(code, message) {
  const error = new Error(`${code}: ${message}`);
  error.code = code;
  return error;
}

function isPlainObject(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function hasExactKeys(value, expectedKeys) {
  if (!isPlainObject(value)) return false;
  const keys = Object.keys(value).sort();
  return keys.length === expectedKeys.length && keys.every((key, index) => key === expectedKeys[index]);
}

function comparePaths(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function withinRoot(rootPath, candidatePath) {
  const relativePath = relative(rootPath, candidatePath);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function hashFile(path) {
  const hash = createHash("sha256");
  await new Promise((resolveHash, rejectHash) => {
    const stream = createReadStream(path);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", rejectHash);
    stream.on("end", resolveHash);
  });
  return hash.digest("hex");
}

export function validateRuntimeModeInventory(inventory, expectedLauncherSha256) {
  if (typeof expectedLauncherSha256 !== "string" || !digestPattern.test(expectedLauncherSha256)) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "launcher digest must be a lowercase SHA-256");
  }
  if (!hasExactKeys(inventory, inventoryKeys)) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory must have the closed schema");
  }
  if (
    inventory.schemaVersion !== 1
    || inventory.platform !== "linux"
    || inventory.architecture !== "x64"
    || inventory.launcherRelativePath !== "code-oss"
    || typeof inventory.launcherSha256 !== "string"
    || !digestPattern.test(inventory.launcherSha256)
    || !Array.isArray(inventory.files)
    || inventory.files.length === 0
  ) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory identity is invalid");
  }
  if (inventory.launcherSha256 !== expectedLauncherSha256) {
    throw releaseInputError("RELEASE_INPUT_DIGEST_MISMATCH", "inventory launcher digest does not match");
  }

  const caseInsensitivePaths = new Map();
  let previousPath;
  let launcherRecord;
  for (const record of inventory.files) {
    if (
      !hasExactKeys(record, fileKeys)
      || !isPortableReleasePath(record.path)
      || !Number.isSafeInteger(record.size)
      || record.size < 0
      || typeof record.sha256 !== "string"
      || !digestPattern.test(record.sha256)
      || typeof record.executable !== "boolean"
    ) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory file record is invalid");
    }
    const aliasKey = record.path.toLowerCase();
    if (caseInsensitivePaths.has(aliasKey)) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory contains duplicate or aliased paths");
    }
    caseInsensitivePaths.set(aliasKey, record.path);
    if (previousPath !== undefined && comparePaths(previousPath, record.path) >= 0) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory file records must be strictly sorted");
    }
    previousPath = record.path;
    if (record.path === inventory.launcherRelativePath) launcherRecord = record;
  }
  if (
    launcherRecord === undefined
    || launcherRecord.sha256 !== inventory.launcherSha256
    || launcherRecord.executable !== true
  ) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory launcher record is invalid");
  }
  return inventory;
}

async function scanRuntimeTree(rootPath) {
  let rootInfo;
  let canonicalRoot;
  try {
    rootInfo = await lstat(rootPath);
    canonicalRoot = await realpath(rootPath);
  } catch {
    throw releaseInputError("RELEASE_INPUT_MISSING", "runtime root is required");
  }
  if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory()) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root must be a real directory");
  }

  const files = new Map();
  const caseInsensitivePaths = new Map();
  async function scanDirectory(directoryPath, relativeDirectory = "") {
    let canonicalDirectory;
    let entries;
    try {
      canonicalDirectory = await realpath(directoryPath);
      entries = await readdir(directoryPath, { withFileTypes: true });
    } catch {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree cannot be read");
    }
    if (!withinRoot(canonicalRoot, canonicalDirectory)) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree escapes the runtime root");
    }
    entries.sort((left, right) => comparePaths(left.name, right.name));
    for (const entry of entries) {
      const relativePath = relativeDirectory ? `${relativeDirectory}/${entry.name}` : entry.name;
      if (!isPortableReleasePath(relativePath)) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a non-portable path");
      }
      const aliasKey = relativePath.toLowerCase();
      if (caseInsensitivePaths.has(aliasKey)) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a case-insensitive path alias");
      }
      caseInsensitivePaths.set(aliasKey, relativePath);

      const entryPath = join(directoryPath, entry.name);
      let info;
      let canonicalEntry;
      try {
        info = await lstat(entryPath);
        canonicalEntry = await realpath(entryPath);
      } catch {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree entry cannot be resolved");
      }
      if (entry.isSymbolicLink() || info.isSymbolicLink()) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a symbolic link");
      }
      if (!withinRoot(canonicalRoot, canonicalEntry)) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree entry escapes the runtime root");
      }
      if (info.isDirectory()) {
        await scanDirectory(entryPath, relativePath);
      } else if (info.isFile()) {
        files.set(relativePath, { path: entryPath, size: info.size });
      } else {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a special entry");
      }
    }
  }
  await scanDirectory(rootPath);
  return files;
}

async function loadInventory(inventoryPath, expectedLauncherSha256) {
  let info;
  let bytes;
  try {
    info = await lstat(inventoryPath);
    if (info.isSymbolicLink() || !info.isFile()) throw new Error("not a real file");
    bytes = await readFile(inventoryPath, "utf8");
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory must be a real readable file");
  }
  let inventory;
  try {
    inventory = JSON.parse(bytes);
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode inventory must be valid JSON");
  }
  return validateRuntimeModeInventory(inventory, expectedLauncherSha256);
}

async function verifyRuntimeFiles(rootPath, inventory) {
  const actualFiles = await scanRuntimeTree(rootPath);
  if (actualFiles.size !== inventory.files.length) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file set does not match the mode inventory");
  }
  for (const record of inventory.files) {
    const actual = actualFiles.get(record.path);
    if (actual === undefined) {
      throw releaseInputError("RELEASE_INPUT_MISSING", "runtime file listed by the mode inventory is missing");
    }
    if (actual.size !== record.size) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file size does not match the mode inventory");
    }
    let digest;
    try {
      digest = await hashFile(actual.path);
    } catch {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file cannot be read");
    }
    if (digest !== record.sha256) {
      throw releaseInputError("RELEASE_INPUT_DIGEST_MISMATCH", "runtime file digest does not match the mode inventory");
    }
  }
  return actualFiles;
}

export async function restoreRuntimeModes(
  { root, inventoryPath, expectedLauncherSha256 } = {},
  { platform = process.platform, chmodFile = chmod, lstatFile = lstat } = {},
) {
  if (platform !== "linux") {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime mode restoration is supported only on Linux");
  }
  if (typeof root !== "string" || root.length === 0 || typeof inventoryPath !== "string" || inventoryPath.length === 0) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root and mode inventory are required");
  }
  const rootPath = resolve(root);
  const inventory = await loadInventory(resolve(inventoryPath), expectedLauncherSha256);
  const actualFiles = await verifyRuntimeFiles(rootPath, inventory);

  try {
    for (const record of inventory.files) {
      await chmodFile(actualFiles.get(record.path).path, record.executable ? 0o755 : 0o644);
    }
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file mode cannot be changed");
  }

  const restoredFiles = await verifyRuntimeFiles(rootPath, inventory);
  for (const record of inventory.files) {
    let info;
    try {
      info = await lstatFile(restoredFiles.get(record.path).path);
    } catch {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file mode state cannot be inspected");
    }
    const expectedMode = record.executable ? 0o755 : 0o644;
    if ((info.mode & 0o777) !== expectedMode) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime file mode could not be restored exactly");
    }
  }
  await validateCodeOssRuntime({
    root: rootPath,
    platform: "linux",
    expectedLauncherSha256,
  });

  return {
    schemaVersion: 1,
    platform: "linux",
    architecture: "x64",
    launcherRelativePath: inventory.launcherRelativePath,
    launcherSha256: inventory.launcherSha256,
    fileCount: inventory.files.length,
  };
}

function parseCliArguments(argumentsList) {
  if (argumentsList[0] !== "restore") {
    throw releaseInputError("RELEASE_INPUT_INVALID", "CLI requires the restore command");
  }
  const flags = new Map();
  for (let index = 1; index < argumentsList.length; index += 2) {
    const flag = argumentsList[index];
    const value = argumentsList[index + 1];
    if (!new Set(["--root", "--inventory", "--launcher-sha256"]).has(flag) || value === undefined || flags.has(flag)) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "restore requires --root, --inventory, and --launcher-sha256");
    }
    flags.set(flag, value);
  }
  if (flags.size !== 3) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "restore requires --root, --inventory, and --launcher-sha256");
  }
  return {
    root: flags.get("--root"),
    inventoryPath: flags.get("--inventory"),
    expectedLauncherSha256: flags.get("--launcher-sha256"),
  };
}

async function runCli() {
  try {
    const result = await restoreRuntimeModes(parseCliArguments(process.argv.slice(2)));
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    process.stderr.write(`${error?.message ?? "RELEASE_INPUT_INVALID: runtime mode restoration failed"}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await runCli();
