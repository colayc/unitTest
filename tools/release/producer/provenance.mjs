import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { constants, readFileSync } from "node:fs";
import { lstat, mkdir, open, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, parse, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import { validateSourceManifest } from "./source-manifest.mjs";
import { validateRuntimeModeInventory } from "../linux/runtime-mode-inventory.mjs";

const INVALID = "RELEASE_PRODUCER_PROVENANCE_INVALID";
const digestPattern = /^[0-9a-f]{64}$/u;
const commitPattern = /^[0-9a-f]{40}$/u;
const producerKeys = ["event", "ref", "repository", "sourceCommit", "workflowPath"].sort();
const codeOssKeys = ["commit", "nodeVersion", "repository", "version", "yarnVersion"].sort();
const windowsKeys = ["artifactName", "fileCount", "launcherRelativePath", "launcherSha256", "schemaVersion", "totalBytes", "treeDigest"].sort();
const linuxKeys = [...windowsKeys, "modeInventorySha256"].sort();
const runtimesKeys = ["linux", "windows"].sort();
const appimagetoolKeys = ["artifactName", "assetId", "assetName", "repository", "sha256", "size"].sort();
const provenanceKeys = ["appimagetool", "codeOss", "producer", "runtimes", "schemaVersion"].sort();
const creationKeys = ["appimagetool", "linux", "linuxModeInventorySha256", "producer", "sourceManifest", "windows"].sort();
const fixedSourceManifest = validateSourceManifest(JSON.parse(readFileSync(new URL("./source-manifest.json", import.meta.url), "utf8")));
const execFileAsync = promisify(execFile);
const windowsReparsePointCommand = "$node=Get-Item -Force -LiteralPath $env:RELEASE_PROVENANCE_INPUT; while ($null -ne $node) { if (($node.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { [Console]::Out.Write('1'); exit 0 }; $node=$node.Parent }; [Console]::Out.Write('0')";

function provenanceError(reason = "release input provenance is invalid") {
  const error = new Error(`${INVALID}: ${reason}`);
  error.code = INVALID;
  return error;
}

function fail(reason) {
  throw provenanceError(reason);
}

function isPlainObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function snapshotExactObject(value, expected) {
  if (!isPlainObject(value)) return false;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) return false;
  keys.sort();
  if (keys.length !== expected.length || !keys.every((key, index) => key === expected[index])) return false;
  const snapshot = {};
  for (const key of expected) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value")) return false;
    snapshot[key] = descriptor.value;
  }
  return snapshot;
}

function snapshotDataTree(value) {
  if (value === null || typeof value !== "object") return value;
  if (!isPlainObject(value)) fail();
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) fail();
  const snapshot = {};
  for (const key of keys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value")) fail();
    snapshot[key] = snapshotDataTree(descriptor.value);
  }
  return snapshot;
}

function positiveInteger(value) {
  return Number.isSafeInteger(value) && value > 0;
}

function modeTreeDigest(files) {
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

function bindModeInventoryToLinuxSummary(modeInventory, linux) {
  let totalBytes = 0;
  for (const record of modeInventory.files) {
    if (!Number.isSafeInteger(totalBytes + record.size)) fail();
    totalBytes += record.size;
  }
  if (modeInventory.files.length !== linux.fileCount
    || totalBytes !== linux.totalBytes
    || modeTreeDigest(modeInventory.files) !== linux.treeDigest) fail();
}

function validateProducer(value) {
  const producer = snapshotExactObject(value, producerKeys);
  if (!producer || producer.repository !== "colayc/unitTest"
    || producer.workflowPath !== ".github/workflows/release-inputs.yml"
    || !commitPattern.test(producer.sourceCommit)
    || producer.event !== "workflow_dispatch"
    || producer.ref !== "refs/heads/master") fail();
  return {
    repository: producer.repository,
    workflowPath: producer.workflowPath,
    sourceCommit: producer.sourceCommit,
    event: producer.event,
    ref: producer.ref,
  };
}

function validateCodeOss(value, manifest) {
  const codeOss = snapshotExactObject(value, codeOssKeys);
  if (!codeOss) fail();
  const expected = manifest.codeOss;
  if (codeOss.repository !== expected.repository
    || codeOss.commit !== expected.commit
    || codeOss.version !== expected.version
    || codeOss.nodeVersion !== expected.nodeVersion
    || codeOss.yarnVersion !== expected.yarnVersion) fail();
  return {
    repository: expected.repository,
    commit: expected.commit,
    version: expected.version,
    nodeVersion: expected.nodeVersion,
    yarnVersion: expected.yarnVersion,
  };
}

function validateSummary(value, platform) {
  const expectedKeys = platform === "linux" ? linuxKeys : windowsKeys;
  const expectedLauncher = platform === "linux" ? "code-oss" : "Code - OSS.exe";
  const expectedArtifact = platform === "linux" ? "code-oss-linux-x64" : "code-oss-windows-x64";
  const summary = snapshotExactObject(value, expectedKeys);
  if (!summary || summary.schemaVersion !== 1
    || summary.artifactName !== expectedArtifact
    || summary.launcherRelativePath !== expectedLauncher
    || typeof summary.launcherSha256 !== "string" || !digestPattern.test(summary.launcherSha256)
    || !positiveInteger(summary.fileCount)
    || !positiveInteger(summary.totalBytes)
    || typeof summary.treeDigest !== "string" || !digestPattern.test(summary.treeDigest)
    || (platform === "linux" && (typeof summary.modeInventorySha256 !== "string" || !digestPattern.test(summary.modeInventorySha256)))) fail();
  return platform === "linux"
    ? { schemaVersion: 1, artifactName: expectedArtifact, launcherRelativePath: expectedLauncher, launcherSha256: summary.launcherSha256, fileCount: summary.fileCount, totalBytes: summary.totalBytes, treeDigest: summary.treeDigest, modeInventorySha256: summary.modeInventorySha256 }
    : { schemaVersion: 1, artifactName: expectedArtifact, launcherRelativePath: expectedLauncher, launcherSha256: summary.launcherSha256, fileCount: summary.fileCount, totalBytes: summary.totalBytes, treeDigest: summary.treeDigest };
}

function validateInputSummary(value, platform) {
  const expectedLauncher = platform === "linux" ? "code-oss" : "Code - OSS.exe";
  const summary = snapshotExactObject(value, ["architecture", "fileCount", "launcherRelativePath", "launcherSha256", "platform", "schemaVersion", "totalBytes", "treeDigest"].sort());
  if (!summary || summary.schemaVersion !== 1
    || summary.platform !== platform
    || summary.architecture !== "x64"
    || summary.launcherRelativePath !== expectedLauncher
    || typeof summary.launcherSha256 !== "string" || !digestPattern.test(summary.launcherSha256)
    || !positiveInteger(summary.fileCount)
    || !positiveInteger(summary.totalBytes)
    || typeof summary.treeDigest !== "string" || !digestPattern.test(summary.treeDigest)) fail();
  return summary;
}

function validateAppimagetool(value, manifest) {
  const appimagetool = snapshotExactObject(value, appimagetoolKeys);
  if (!appimagetool) fail();
  const expected = manifest.appimagetool;
  if (appimagetool.repository !== expected.repository
    || appimagetool.artifactName !== "appimagetool-linux-x64"
    || appimagetool.assetId !== expected.assetId
    || appimagetool.assetName !== expected.assetName
    || appimagetool.size !== expected.size
    || appimagetool.sha256 !== expected.sha256) fail();
  return {
    repository: expected.repository,
    artifactName: "appimagetool-linux-x64",
    assetId: expected.assetId,
    assetName: expected.assetName,
    size: expected.size,
    sha256: expected.sha256,
  };
}

function validateInputAppimagetool(value, manifest) {
  const expected = manifest.appimagetool;
  const appimagetool = snapshotExactObject(value, Object.keys(expected).sort());
  if (!appimagetool || appimagetool.repository !== expected.repository
    || appimagetool.assetId !== expected.assetId
    || appimagetool.assetName !== expected.assetName
    || appimagetool.size !== expected.size
    || appimagetool.sha256 !== expected.sha256) fail();
  return appimagetool;
}

function deepFreeze(value) {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const child of Object.values(value)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

function normalizeProvenance(value, manifest) {
  const provenance = snapshotExactObject(value, provenanceKeys);
  if (!provenance || provenance.schemaVersion !== 1) fail();
  const runtimes = snapshotExactObject(provenance.runtimes, runtimesKeys);
  if (!runtimes) fail();
  return deepFreeze({
    schemaVersion: 1,
    producer: validateProducer(provenance.producer),
    codeOss: validateCodeOss(provenance.codeOss, manifest),
    runtimes: {
      windows: validateSummary(runtimes.windows, "windows"),
      linux: validateSummary(runtimes.linux, "linux"),
    },
    appimagetool: validateAppimagetool(provenance.appimagetool, manifest),
  });
}

export function validateReleaseInputProvenance(value) {
  return normalizeProvenance(value, fixedSourceManifest);
}

export function createReleaseInputProvenance(request) {
  const input = snapshotExactObject(request, creationKeys);
  if (!input) fail();
  let manifest;
  try {
    manifest = validateSourceManifest(snapshotDataTree(input.sourceManifest));
  } catch {
    fail();
  }
  const windows = validateInputSummary(input.windows, "windows");
  const linux = validateInputSummary(input.linux, "linux");
  const appimagetool = validateInputAppimagetool(input.appimagetool, manifest);
  if (typeof input.linuxModeInventorySha256 !== "string" || !digestPattern.test(input.linuxModeInventorySha256)) fail();
  const candidate = {
    schemaVersion: 1,
    producer: input.producer,
    codeOss: {
      repository: manifest.codeOss.repository,
      commit: manifest.codeOss.commit,
      version: manifest.codeOss.version,
      nodeVersion: manifest.codeOss.nodeVersion,
      yarnVersion: manifest.codeOss.yarnVersion,
    },
    runtimes: {
      windows: {
        schemaVersion: 1,
        artifactName: "code-oss-windows-x64",
        launcherRelativePath: windows.launcherRelativePath,
        launcherSha256: windows.launcherSha256,
        fileCount: windows.fileCount,
        totalBytes: windows.totalBytes,
        treeDigest: windows.treeDigest,
      },
      linux: {
        schemaVersion: 1,
        artifactName: "code-oss-linux-x64",
        launcherRelativePath: linux.launcherRelativePath,
        launcherSha256: linux.launcherSha256,
        fileCount: linux.fileCount,
        totalBytes: linux.totalBytes,
        treeDigest: linux.treeDigest,
        modeInventorySha256: input.linuxModeInventorySha256,
      },
    },
    appimagetool: {
      repository: appimagetool.repository,
      artifactName: "appimagetool-linux-x64",
      assetId: appimagetool.assetId,
      assetName: appimagetool.assetName,
      size: appimagetool.size,
      sha256: appimagetool.sha256,
    },
  };
  return normalizeProvenance(candidate, manifest);
}

function sameSnapshot(left, right) {
  return left.isFile() && right.isFile() && left.dev === right.dev && left.ino === right.ino
    && left.size === right.size && left.mode === right.mode && left.mtimeNs === right.mtimeNs && left.ctimeNs === right.ctimeNs;
}

function sameNode(left, right) {
  return left.dev === right.dev && left.ino === right.ino && left.mode === right.mode
    && left.mtimeNs === right.mtimeNs && left.ctimeNs === right.ctimeNs
    && left.isFile() === right.isFile() && left.isDirectory() === right.isDirectory();
}

async function rejectWindowsReparsePoints(path) {
  if (process.platform !== "win32") return;
  try {
    const { stdout } = await execFileAsync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", windowsReparsePointCommand], {
      encoding: "utf8",
      env: { ...process.env, RELEASE_PROVENANCE_INPUT: path },
      windowsHide: true,
    });
    if (stdout.trim() !== "0") fail("attested file is linked");
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("attested file cannot be inspected");
  }
}

async function assertRealFilePath(path) {
  if (typeof path !== "string" || path.length === 0) fail();
  const absolute = resolve(path);
  const root = parse(absolute).root;
  let current = root;
  try {
    await rejectWindowsReparsePoints(absolute);
    const rootInfo = await lstat(root, { bigint: true });
    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) fail("attested file is not real");
    const ancestors = [{ path: root, info: rootInfo }];
    for (const component of absolute.slice(root.length).split(/[\\/]+/u).filter(Boolean)) {
      current = join(current, component);
      const info = await lstat(current, { bigint: true });
      if (info.isSymbolicLink() || (!info.isFile() && current === absolute) || (!info.isDirectory() && current !== absolute)) fail("attested file is not real");
      if (current !== absolute) ancestors.push({ path: current, info });
    }
    return { absolute, info: await lstat(absolute, { bigint: true }), ancestors };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("attested file cannot be read");
  }
}

async function assertUnchangedAncestors(checked) {
  await rejectWindowsReparsePoints(checked.absolute);
  for (const ancestor of checked.ancestors) {
    const current = await lstat(ancestor.path, { bigint: true });
    if (current.isSymbolicLink() || !current.isDirectory() || !sameNode(ancestor.info, current)) fail("attested input ancestor changed");
  }
}

async function hashRealFile(path, hooks = {}) {
  const checked = await assertRealFilePath(path);
  if (!checked.info.isFile() || checked.info.size < 0n || checked.info.size > BigInt(Number.MAX_SAFE_INTEGER)) fail("attested file is invalid");
  let handle;
  try {
    handle = await open(checked.absolute, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!sameSnapshot(checked.info, before)) fail("attested file changed");
    await hooks.afterOpenSnapshot?.();
    const hash = createHash("sha256");
    let byteCount = 0;
    const chunks = [];
    await new Promise((resolveStream, rejectStream) => {
      const stream = handle.createReadStream({ autoClose: false });
      stream.on("data", (chunk) => { byteCount += chunk.length; hash.update(chunk); chunks.push(chunk); });
      stream.on("end", resolveStream);
      stream.on("error", rejectStream);
    });
    const after = await handle.stat({ bigint: true });
    const pathAfter = await lstat(checked.absolute, { bigint: true });
    await assertUnchangedAncestors(checked);
    if (!sameSnapshot(checked.info, after) || !sameSnapshot(before, after) || !sameSnapshot(checked.info, pathAfter) || byteCount !== Number(before.size)) fail("attested file changed");
    return { sha256: hash.digest("hex"), size: byteCount, bytes: Buffer.concat(chunks) };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("attested file cannot be read");
  } finally {
    await handle?.close().catch(() => {});
  }
}

async function snapshotRealOutputDirectory(path) {
  const absolute = resolve(path);
  const missing = [];
  let current = absolute;
  while (true) {
    try {
      const info = await lstat(current, { bigint: true });
      if (info.isSymbolicLink() || !info.isDirectory()) fail("output directory is invalid");
      break;
    } catch (error) {
      if (error?.code === INVALID) throw error;
      if (error?.code !== "ENOENT") fail("output directory is invalid");
      const parent = dirname(current);
      if (parent === current) fail("output directory is invalid");
      missing.push(current);
      current = parent;
    }
  }
  for (const directory of missing.reverse()) {
    await mkdir(directory);
    const info = await lstat(directory, { bigint: true });
    if (info.isSymbolicLink() || !info.isDirectory()) fail("output directory is invalid");
  }
  const root = parse(absolute).root;
  const ancestors = [];
  current = root;
  await rejectWindowsReparsePoints(absolute);
  for (const component of absolute.slice(root.length).split(/[\\/]+/u).filter(Boolean)) {
    current = join(current, component);
    const info = await lstat(current, { bigint: true });
    if (info.isSymbolicLink() || !info.isDirectory()) fail("output directory is invalid");
    ancestors.push({ path: current, info });
  }
  return { absolute, ancestors };
}

async function assertUnchangedOutputDirectory(snapshot) {
  await rejectWindowsReparsePoints(snapshot.absolute);
  for (const ancestor of snapshot.ancestors) {
    const current = await lstat(ancestor.path, { bigint: true });
    if (current.isSymbolicLink() || !current.isDirectory() || current.dev !== ancestor.info.dev || current.ino !== ancestor.info.ino || current.mode !== ancestor.info.mode) fail("output ancestor changed");
  }
}

function identity(info) {
  return info === undefined ? undefined : { dev: info.dev, ino: info.ino };
}

function sameIdentity(left, right) {
  return left === undefined ? right === undefined : right !== undefined && left.dev === right.dev && left.ino === right.ino;
}

async function loadJsonFile(path) {
  const checked = await hashRealFile(path);
  try {
    return { value: JSON.parse(checked.bytes.toString("utf8")), sha256: checked.sha256 };
  } catch {
    fail("attested JSON is invalid");
  }
}

function parseCli(argv) {
  const [command, ...args] = argv;
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index];
    const value = args[index + 1];
    if (typeof key !== "string" || !key.startsWith("--") || value === undefined || Object.hasOwn(options, key)) fail();
    options[key] = value;
  }
  const expected = command === "create"
    ? ["--appimagetool", "--linux-mode-inventory", "--linux-summary", "--manifest", "--out", "--producer-event", "--producer-ref", "--producer-repository", "--producer-source-commit", "--producer-workflow-path", "--windows-summary"].sort()
    : command === "validate" ? ["--manifest", "--provenance"] : [];
  if (!snapshotExactObject(options, expected)) fail();
  return { command, options };
}

async function writeCanonical(path, value, hooks = {}) {
  if (typeof path !== "string" || path.length === 0) fail();
  const target = resolve(path);
  const temporary = `${target}.tmp-${process.pid}-${Date.now()}`;
  let directory;
  try {
    directory = await snapshotRealOutputDirectory(dirname(target));
    const existing = await lstat(target).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
    if (existing?.isSymbolicLink() || (existing && !existing.isFile())) fail();
    const expected = identity(existing);
    await hooks.beforeStage?.();
    await assertUnchangedOutputDirectory(directory);
    await writeFile(temporary, `${JSON.stringify(value)}\n`, { encoding: "utf8", flag: "wx", mode: 0o600 });
    const staged = await lstat(temporary, { bigint: true });
    if (staged.isSymbolicLink() || !staged.isFile()) fail();
    await assertUnchangedOutputDirectory(directory);
    const current = await lstat(target).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
    if (current?.isSymbolicLink() || (current && !current.isFile()) || !sameIdentity(expected, identity(current))) fail();
    await hooks.beforePublish?.();
    await assertUnchangedOutputDirectory(directory);
    const publishTarget = await lstat(target).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
    if (publishTarget?.isSymbolicLink() || (publishTarget && !publishTarget.isFile()) || !sameIdentity(expected, identity(publishTarget))) fail();
    await rename(temporary, target);
    await assertUnchangedOutputDirectory(directory);
    const published = await lstat(target, { bigint: true });
    if (published.isSymbolicLink() || !published.isFile() || !sameIdentity(identity(staged), identity(published))) fail();
  } catch (error) {
    if (directory && await assertUnchangedOutputDirectory(directory).then(() => true, () => false)) {
      await rm(temporary, { force: true }).catch(() => {});
    }
    if (error?.code === INVALID) throw error;
    fail();
  }
}

function closedHooks(value, keys) {
  if (!isPlainObject(value) || Reflect.ownKeys(value).some((key) => typeof key !== "string" || !keys.includes(key) || typeof value[key] !== "function")) fail();
  return value;
}

export const __testOnlyReleaseInputProvenance = Object.freeze({
  hashRealFile(path, hooks) {
    return hashRealFile(path, closedHooks(hooks, ["afterOpenSnapshot"]));
  },
  writeCanonical(path, value, hooks) {
    return writeCanonical(path, value, closedHooks(hooks, ["beforePublish", "beforeStage"]));
  },
});

async function main(argv) {
  const { command, options } = parseCli(argv);
  const manifestDocument = await loadJsonFile(options["--manifest"]);
  let manifest;
  try {
    manifest = validateSourceManifest(manifestDocument.value);
  } catch {
    fail("source manifest is invalid");
  }
  if (command === "validate") {
    const provenance = await loadJsonFile(options["--provenance"]);
    // Loading the fixed manifest above is intentional: provenance validation
    // is only meaningful in the same fixed source-coordinate contract.
    validateReleaseInputProvenance(provenance.value);
    return;
  }
  const [windowsFile, linuxFile, modeInventoryFile, appimagetool] = await Promise.all([
    loadJsonFile(options["--windows-summary"]),
    loadJsonFile(options["--linux-summary"]),
    loadJsonFile(options["--linux-mode-inventory"]),
    hashRealFile(options["--appimagetool"]),
  ]);
  const windows = validateInputSummary(windowsFile.value, "windows");
  const linux = validateInputSummary(linuxFile.value, "linux");
  try {
    validateRuntimeModeInventory(modeInventoryFile.value, linux.launcherSha256);
  } catch {
    fail();
  }
  bindModeInventoryToLinuxSummary(modeInventoryFile.value, linux);
  const provenance = createReleaseInputProvenance({
    sourceManifest: manifest,
    producer: {
      repository: options["--producer-repository"],
      workflowPath: options["--producer-workflow-path"],
      sourceCommit: options["--producer-source-commit"],
      event: options["--producer-event"],
      ref: options["--producer-ref"],
    },
    windows,
    linux,
    linuxModeInventorySha256: modeInventoryFile.sha256,
    appimagetool: {
      repository: manifest.appimagetool.repository,
      assetId: manifest.appimagetool.assetId,
      assetName: manifest.appimagetool.assetName,
      size: appimagetool.size,
      sha256: appimagetool.sha256,
    },
  });
  await writeCanonical(options["--out"], provenance);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error?.code === INVALID ? error.message : `${INVALID}: release input provenance is invalid`}\n`);
    process.exitCode = 1;
  });
}
