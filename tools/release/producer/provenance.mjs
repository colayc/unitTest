import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { constants, readFileSync } from "node:fs";
import { chmod, link, lstat, mkdir, open, readFile, rm } from "node:fs/promises";
import { dirname, join, parse, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import { validateSourceManifest } from "./source-manifest.mjs";
import { validateRuntimeModeInventory } from "../linux/runtime-mode-inventory.mjs";

const INVALID = "RELEASE_PRODUCER_PROVENANCE_INVALID";
const digestPattern = /^[0-9a-f]{64}$/u;
const commitPattern = /^[0-9a-f]{40}$/u;
const producerKeys = ["event", "ref", "repository", "runAttempt", "runId", "sourceCommit", "workflowPath"].sort();
const codeOssKeys = ["commit", "nodeVersion", "repository", "version", "yarnVersion"].sort();
const artifactIdentityKeys = ["artifactDigest", "artifactId", "transportName"];
const windowsKeys = ["artifactName", ...artifactIdentityKeys, "fileCount", "launcherRelativePath", "launcherSha256", "schemaVersion", "totalBytes", "treeDigest"].sort();
const linuxKeys = [...windowsKeys, "modeInventorySha256"].sort();
const runtimesKeys = ["linux", "windows"].sort();
const appimagetoolKeys = ["artifactName", ...artifactIdentityKeys, "assetId", "assetName", "repository", "sha256", "size"].sort();
const provenanceKeys = ["appimagetool", "codeOss", "producer", "runtimes", "schemaVersion"].sort();
const creationKeys = ["appimagetool", "linux", "linuxModeInventorySha256", "producer", "sourceManifest", "windows"].sort();
const fixedSourceManifest = validateSourceManifest(JSON.parse(readFileSync(new URL("./source-manifest.json", import.meta.url), "utf8")));
const execFileAsync = promisify(execFile);
const windowsReparsePointCommand = "$node=Get-Item -Force -LiteralPath $env:RELEASE_PROVENANCE_INPUT; while ($null -ne $node) { if (($node.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { [Console]::Out.Write('1'); exit 0 }; $node=$node.Parent }; [Console]::Out.Write('0')";
const windowsOwnedDirectoryCommand = "$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User; $acl=[IO.Directory]::GetAccessControl($env:RELEASE_PROVENANCE_INPUT); $owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]); if ($owner.Value -eq $sid.Value) { [Console]::Out.Write('1') } else { [Console]::Out.Write('0') }";

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

function canonicalDecimalString(value) {
  return typeof value === "string" && /^[1-9][0-9]*$/u.test(value) ? value : undefined;
}

function canonicalAttempt(value) {
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
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
    || producer.ref !== "refs/heads/master"
    || canonicalDecimalString(producer.runId) === undefined
    || canonicalAttempt(producer.runAttempt) === undefined) fail();
  return {
    repository: producer.repository,
    workflowPath: producer.workflowPath,
    sourceCommit: producer.sourceCommit,
    event: producer.event,
    ref: producer.ref,
    runId: producer.runId,
    runAttempt: producer.runAttempt,
  };
}

function validateArtifactIdentity(value, logicalName, runAttempt) {
  const expectedKeys = logicalName === "code-oss-windows-x64"
    ? windowsKeys
    : logicalName === "code-oss-linux-x64"
      ? linuxKeys
      : logicalName === "appimagetool-linux-x64" ? appimagetoolKeys : undefined;
  const artifact = expectedKeys === undefined ? false : snapshotExactObject(value, expectedKeys);
  if (!artifact
    || canonicalDecimalString(artifact.artifactId) === undefined
    || typeof artifact.artifactDigest !== "string" || !digestPattern.test(artifact.artifactDigest)
    || artifact.transportName !== `${logicalName}-${runAttempt}`) fail();
  return artifact;
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

function validateSummary(value, platform, runAttempt) {
  const expectedLauncher = platform === "linux" ? "code-oss" : "Code - OSS.exe";
  const expectedArtifact = platform === "linux" ? "code-oss-linux-x64" : "code-oss-windows-x64";
  const summary = validateArtifactIdentity(value, expectedArtifact, runAttempt);
  if (!summary || summary.schemaVersion !== 1
    || summary.artifactName !== expectedArtifact
    || summary.launcherRelativePath !== expectedLauncher
    || typeof summary.launcherSha256 !== "string" || !digestPattern.test(summary.launcherSha256)
    || !positiveInteger(summary.fileCount)
    || !positiveInteger(summary.totalBytes)
    || typeof summary.treeDigest !== "string" || !digestPattern.test(summary.treeDigest)
    || (platform === "linux" && (typeof summary.modeInventorySha256 !== "string" || !digestPattern.test(summary.modeInventorySha256)))) fail();
  return platform === "linux"
    ? { schemaVersion: 1, artifactName: expectedArtifact, artifactId: summary.artifactId, artifactDigest: summary.artifactDigest, transportName: summary.transportName, launcherRelativePath: expectedLauncher, launcherSha256: summary.launcherSha256, fileCount: summary.fileCount, totalBytes: summary.totalBytes, treeDigest: summary.treeDigest, modeInventorySha256: summary.modeInventorySha256 }
    : { schemaVersion: 1, artifactName: expectedArtifact, artifactId: summary.artifactId, artifactDigest: summary.artifactDigest, transportName: summary.transportName, launcherRelativePath: expectedLauncher, launcherSha256: summary.launcherSha256, fileCount: summary.fileCount, totalBytes: summary.totalBytes, treeDigest: summary.treeDigest };
}

function validateInputSummary(value, platform) {
  const expectedLauncher = platform === "linux" ? "code-oss" : "Code - OSS.exe";
  const summary = snapshotExactObject(value, ["architecture", ...artifactIdentityKeys, "fileCount", "launcherRelativePath", "launcherSha256", "platform", "schemaVersion", "totalBytes", "treeDigest"].sort());
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

function validateAppimagetool(value, manifest, runAttempt) {
  const appimagetool = validateArtifactIdentity(value, "appimagetool-linux-x64", runAttempt);
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
    artifactId: appimagetool.artifactId,
    artifactDigest: appimagetool.artifactDigest,
    transportName: appimagetool.transportName,
    assetId: expected.assetId,
    assetName: expected.assetName,
    size: expected.size,
    sha256: expected.sha256,
  };
}

function validateInputAppimagetool(value, manifest) {
  const expected = manifest.appimagetool;
  const appimagetool = snapshotExactObject(value, [...Object.keys(expected), ...artifactIdentityKeys].sort());
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
  const producer = validateProducer(provenance.producer);
  return deepFreeze({
    schemaVersion: 1,
    producer,
    codeOss: validateCodeOss(provenance.codeOss, manifest),
    runtimes: {
      windows: validateSummary(runtimes.windows, "windows", producer.runAttempt),
      linux: validateSummary(runtimes.linux, "linux", producer.runAttempt),
    },
    appimagetool: validateAppimagetool(provenance.appimagetool, manifest, producer.runAttempt),
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
  const producer = validateProducer(input.producer);
  const windows = validateInputSummary(input.windows, "windows");
  const linux = validateInputSummary(input.linux, "linux");
  const appimagetool = validateInputAppimagetool(input.appimagetool, manifest);
  if (typeof input.linuxModeInventorySha256 !== "string" || !digestPattern.test(input.linuxModeInventorySha256)) fail();
  const candidate = {
    schemaVersion: 1,
    producer,
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
        artifactId: windows.artifactId,
        artifactDigest: windows.artifactDigest,
        transportName: windows.transportName,
        launcherRelativePath: windows.launcherRelativePath,
        launcherSha256: windows.launcherSha256,
        fileCount: windows.fileCount,
        totalBytes: windows.totalBytes,
        treeDigest: windows.treeDigest,
      },
      linux: {
        schemaVersion: 1,
        artifactName: "code-oss-linux-x64",
        artifactId: linux.artifactId,
        artifactDigest: linux.artifactDigest,
        transportName: linux.transportName,
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
      artifactId: appimagetool.artifactId,
      artifactDigest: appimagetool.artifactDigest,
      transportName: appimagetool.transportName,
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

function sameFileIdentity(left, right) {
  return left.isFile() && right.isFile() && left.dev === right.dev && left.ino === right.ino
    && left.size === right.size && left.mode === right.mode && left.mtimeNs === right.mtimeNs;
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

async function runWindowsDirectoryCheck(path, command, reason) {
  if (process.platform !== "win32") return;
  try {
    const { stdout } = await execFileAsync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command], {
      encoding: "utf8",
      env: { ...process.env, RELEASE_PROVENANCE_INPUT: path },
      windowsHide: true,
    });
    if (stdout.trim() !== "1") fail(reason);
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail(reason);
  }
}

async function snapshotOwnedOutputParent(path) {
  const absolute = resolve(path);
  const root = parse(absolute).root;
  const ancestors = [];
  let current = root;
  try {
    await rejectWindowsReparsePoints(absolute);
    const rootInfo = await lstat(root, { bigint: true });
    if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory()) fail("output parent is invalid");
    ancestors.push({ path: root, info: rootInfo });
    for (const component of absolute.slice(root.length).split(/[\\/]+/u).filter(Boolean)) {
      current = join(current, component);
      const info = await lstat(current, { bigint: true });
      if (info.isSymbolicLink() || !info.isDirectory()) fail("output parent is invalid");
      ancestors.push({ path: current, info });
    }
    const parent = ancestors.at(-1).info;
    if (typeof process.getuid === "function") {
      if (parent.uid !== BigInt(process.getuid()) || (parent.mode & 0o022n) !== 0n) fail("output parent is not private");
    } else {
      await runWindowsDirectoryCheck(absolute, windowsOwnedDirectoryCommand, "output parent owner is invalid");
    }
    return { absolute, ancestors };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("output parent cannot be inspected");
  }
}

async function createPrivateOutputDirectory(path) {
  const absolute = resolve(path);
  const parentPath = dirname(absolute);
  if (parentPath === absolute) fail("output directory is invalid");
  const parent = await snapshotOwnedOutputParent(parentPath);
  try {
    await mkdir(absolute, { mode: 0o700 });
  } catch (error) {
    if (error?.code === "EEXIST") fail("output directory already exists");
    if (error?.code === INVALID) throw error;
    fail("output directory cannot be created");
  }
  try {
    if (typeof process.getuid === "function") await chmod(absolute, 0o700);
    else await runWindowsDirectoryCheck(absolute, windowsOwnedDirectoryCommand, "output directory owner is invalid");
    await assertUnchangedOutputDirectory(parent);
    await rejectWindowsReparsePoints(absolute);
    const info = await lstat(absolute, { bigint: true });
    if (info.isSymbolicLink() || !info.isDirectory()) fail("output directory is invalid");
    if (typeof process.getuid === "function"
      && (info.uid !== BigInt(process.getuid()) || (info.mode & 0o777n) !== 0o700n)) fail("output directory is not private");
    return { absolute, ancestors: [...parent.ancestors, { path: absolute, info }] };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("output directory cannot be secured");
  }
}

async function assertUnchangedOutputDirectory(snapshot) {
  await rejectWindowsReparsePoints(snapshot.absolute);
  for (const ancestor of snapshot.ancestors) {
    const current = await lstat(ancestor.path, { bigint: true });
    if (current.isSymbolicLink() || !current.isDirectory() || current.dev !== ancestor.info.dev || current.ino !== ancestor.info.ino || current.mode !== ancestor.info.mode) fail("output ancestor changed");
  }
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
    ? ["--appimagetool", "--appimagetool-artifact-digest", "--appimagetool-artifact-id", "--appimagetool-artifact-transport-name", "--linux-artifact-digest", "--linux-artifact-id", "--linux-artifact-transport-name", "--linux-mode-inventory", "--linux-summary", "--manifest", "--out", "--producer-event", "--producer-ref", "--producer-repository", "--producer-run-attempt", "--producer-run-id", "--producer-source-commit", "--producer-workflow-path", "--windows-artifact-digest", "--windows-artifact-id", "--windows-artifact-transport-name", "--windows-summary"].sort()
    : command === "validate" ? ["--manifest", "--provenance"] : [];
  if (!snapshotExactObject(options, expected)) fail();
  return { command, options };
}

async function writeCanonical(path, value, hooks = {}) {
  if (typeof path !== "string" || path.length === 0) fail();
  const target = resolve(path);
  const temporary = `${target}.tmp-${process.pid}-${Date.now()}`;
  const bytes = Buffer.from(`${JSON.stringify(value)}\n`);
  let directory;
  let handle;
  let staged;
  try {
    // Security boundary: this producer runs on a fresh standard GitHub-hosted
    // runner with no untrusted same-principal process. The CLI enforces that
    // its private dedicated output directory did not exist before this call.
    directory = await createPrivateOutputDirectory(dirname(target));
    await hooks.beforeStage?.();
    await assertUnchangedOutputDirectory(directory);
    handle = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | (constants.O_NOFOLLOW ?? 0), 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    staged = await handle.stat({ bigint: true });
    if (!staged.isFile() || staged.size !== BigInt(bytes.length)) fail("staged output is invalid");
    await hooks.beforePublish?.();
    await hooks.beforeCommit?.();
    await assertUnchangedOutputDirectory(directory);
    const handleStage = await handle.stat({ bigint: true });
    const currentStage = await lstat(temporary, { bigint: true });
    if (currentStage.isSymbolicLink() || !currentStage.isFile()
      || !sameSnapshot(staged, handleStage) || !sameSnapshot(staged, currentStage)
      || !(await readFile(temporary)).equals(bytes)) fail("staged output changed");
    // link(2)/CreateHardLinkW is the commit point: it fails atomically when the
    // final name exists and never has overwrite semantics.
    await link(temporary, target);
    await hooks.afterCommit?.();
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail();
  } finally {
    await handle?.close().catch(() => {});
    if (directory && staged && await assertUnchangedOutputDirectory(directory).then(() => true, () => false)) {
      const current = await lstat(temporary, { bigint: true }).catch(() => undefined);
      if (current !== undefined && sameFileIdentity(staged, current)
        && await readFile(temporary).then((value) => value.equals(bytes), () => false)) {
        await rm(temporary, { force: true }).catch(() => {});
      }
    }
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
    return writeCanonical(path, value, closedHooks(hooks, ["afterCommit", "beforeCommit", "beforePublish", "beforeStage"]));
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
  const producerRunAttemptText = canonicalDecimalString(options["--producer-run-attempt"]);
  const producerRunAttempt = producerRunAttemptText === undefined ? undefined : Number(producerRunAttemptText);
  if (producerRunAttemptText === undefined || canonicalAttempt(producerRunAttempt) === undefined
    || String(producerRunAttempt) !== producerRunAttemptText) fail();
  const [windowsFile, linuxFile, modeInventoryFile, appimagetool] = await Promise.all([
    loadJsonFile(options["--windows-summary"]),
    loadJsonFile(options["--linux-summary"]),
    loadJsonFile(options["--linux-mode-inventory"]),
    hashRealFile(options["--appimagetool"]),
  ]);
  const windows = validateInputSummary({
    ...windowsFile.value,
    artifactId: options["--windows-artifact-id"],
    artifactDigest: options["--windows-artifact-digest"],
    transportName: options["--windows-artifact-transport-name"],
  }, "windows");
  const linux = validateInputSummary({
    ...linuxFile.value,
    artifactId: options["--linux-artifact-id"],
    artifactDigest: options["--linux-artifact-digest"],
    transportName: options["--linux-artifact-transport-name"],
  }, "linux");
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
      runId: options["--producer-run-id"],
      runAttempt: producerRunAttempt,
    },
    windows: {
      ...windows,
      artifactId: options["--windows-artifact-id"],
      artifactDigest: options["--windows-artifact-digest"],
      transportName: options["--windows-artifact-transport-name"],
    },
    linux: {
      ...linux,
      artifactId: options["--linux-artifact-id"],
      artifactDigest: options["--linux-artifact-digest"],
      transportName: options["--linux-artifact-transport-name"],
    },
    linuxModeInventorySha256: modeInventoryFile.sha256,
    appimagetool: {
      repository: manifest.appimagetool.repository,
      assetId: manifest.appimagetool.assetId,
      assetName: manifest.appimagetool.assetName,
      size: appimagetool.size,
      sha256: appimagetool.sha256,
      artifactId: options["--appimagetool-artifact-id"],
      artifactDigest: options["--appimagetool-artifact-digest"],
      transportName: options["--appimagetool-artifact-transport-name"],
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
