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

function hasExactKeys(value, expected) {
  if (!isPlainObject(value)) return false;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) return false;
  keys.sort();
  return keys.length === expected.length && keys.every((key, index) => key === expected[index]);
}

function positiveInteger(value) {
  return Number.isSafeInteger(value) && value > 0;
}

function validateProducer(value) {
  if (!hasExactKeys(value, producerKeys)
    || value.repository !== "colayc/unitTest"
    || value.workflowPath !== ".github/workflows/release-inputs.yml"
    || !commitPattern.test(value.sourceCommit)
    || value.event !== "workflow_dispatch"
    || value.ref !== "refs/heads/master") fail();
  return {
    repository: value.repository,
    workflowPath: value.workflowPath,
    sourceCommit: value.sourceCommit,
    event: value.event,
    ref: value.ref,
  };
}

function validateCodeOss(value, manifest) {
  if (!hasExactKeys(value, codeOssKeys)) fail();
  const expected = manifest.codeOss;
  if (value.repository !== expected.repository
    || value.commit !== expected.commit
    || value.version !== expected.version
    || value.nodeVersion !== expected.nodeVersion
    || value.yarnVersion !== expected.yarnVersion) fail();
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
  if (!hasExactKeys(value, expectedKeys)
    || value.schemaVersion !== 1
    || value.artifactName !== expectedArtifact
    || value.launcherRelativePath !== expectedLauncher
    || typeof value.launcherSha256 !== "string" || !digestPattern.test(value.launcherSha256)
    || !positiveInteger(value.fileCount)
    || !positiveInteger(value.totalBytes)
    || typeof value.treeDigest !== "string" || !digestPattern.test(value.treeDigest)
    || (platform === "linux" && (typeof value.modeInventorySha256 !== "string" || !digestPattern.test(value.modeInventorySha256)))) fail();
  return platform === "linux"
    ? { schemaVersion: 1, artifactName: expectedArtifact, launcherRelativePath: expectedLauncher, launcherSha256: value.launcherSha256, fileCount: value.fileCount, totalBytes: value.totalBytes, treeDigest: value.treeDigest, modeInventorySha256: value.modeInventorySha256 }
    : { schemaVersion: 1, artifactName: expectedArtifact, launcherRelativePath: expectedLauncher, launcherSha256: value.launcherSha256, fileCount: value.fileCount, totalBytes: value.totalBytes, treeDigest: value.treeDigest };
}

function validateInputSummary(value, platform) {
  const expectedLauncher = platform === "linux" ? "code-oss" : "Code - OSS.exe";
  if (!hasExactKeys(value, ["architecture", "fileCount", "launcherRelativePath", "launcherSha256", "platform", "schemaVersion", "totalBytes", "treeDigest"].sort())
    || value.schemaVersion !== 1
    || value.platform !== platform
    || value.architecture !== "x64"
    || value.launcherRelativePath !== expectedLauncher
    || typeof value.launcherSha256 !== "string" || !digestPattern.test(value.launcherSha256)
    || !positiveInteger(value.fileCount)
    || !positiveInteger(value.totalBytes)
    || typeof value.treeDigest !== "string" || !digestPattern.test(value.treeDigest)) fail();
  return value;
}

function validateAppimagetool(value, manifest) {
  if (!hasExactKeys(value, appimagetoolKeys)) fail();
  const expected = manifest.appimagetool;
  if (value.repository !== expected.repository
    || value.artifactName !== "appimagetool-linux-x64"
    || value.assetId !== expected.assetId
    || value.assetName !== expected.assetName
    || value.size !== expected.size
    || value.sha256 !== expected.sha256) fail();
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
  if (!hasExactKeys(value, Object.keys(expected).sort())
    || value.repository !== expected.repository
    || value.assetId !== expected.assetId
    || value.assetName !== expected.assetName
    || value.size !== expected.size
    || value.sha256 !== expected.sha256) fail();
  return value;
}

function deepFreeze(value) {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const child of Object.values(value)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

function normalizeProvenance(value, manifest) {
  if (!hasExactKeys(value, provenanceKeys) || value.schemaVersion !== 1 || !hasExactKeys(value.runtimes, runtimesKeys)) fail();
  return deepFreeze({
    schemaVersion: 1,
    producer: validateProducer(value.producer),
    codeOss: validateCodeOss(value.codeOss, manifest),
    runtimes: {
      windows: validateSummary(value.runtimes.windows, "windows"),
      linux: validateSummary(value.runtimes.linux, "linux"),
    },
    appimagetool: validateAppimagetool(value.appimagetool, manifest),
  });
}

export function validateReleaseInputProvenance(value) {
  return normalizeProvenance(value, fixedSourceManifest);
}

export function createReleaseInputProvenance(request) {
  if (!hasExactKeys(request, creationKeys)) fail();
  let manifest;
  try {
    manifest = validateSourceManifest(request.sourceManifest);
  } catch {
    fail();
  }
  const windows = validateInputSummary(request.windows, "windows");
  const linux = validateInputSummary(request.linux, "linux");
  validateInputAppimagetool(request.appimagetool, manifest);
  if (typeof request.linuxModeInventorySha256 !== "string" || !digestPattern.test(request.linuxModeInventorySha256)) fail();
  const candidate = {
    schemaVersion: 1,
    producer: request.producer,
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
        modeInventorySha256: request.linuxModeInventorySha256,
      },
    },
    appimagetool: {
      repository: request.appimagetool.repository,
      artifactName: "appimagetool-linux-x64",
      assetId: request.appimagetool.assetId,
      assetName: request.appimagetool.assetName,
      size: request.appimagetool.size,
      sha256: request.appimagetool.sha256,
    },
  };
  return normalizeProvenance(candidate, manifest);
}

function sameSnapshot(left, right) {
  return left.isFile() && right.isFile() && left.dev === right.dev && left.ino === right.ino
    && left.size === right.size && left.mode === right.mode && left.mtimeNs === right.mtimeNs && left.ctimeNs === right.ctimeNs;
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
    for (const component of absolute.slice(root.length).split(/[\\/]+/u).filter(Boolean)) {
      current = join(current, component);
      const info = await lstat(current, { bigint: true });
      if (info.isSymbolicLink() || (!info.isFile() && current === absolute) || (!info.isDirectory() && current !== absolute)) fail("attested file is not real");
    }
    return { absolute, info: await lstat(absolute, { bigint: true }) };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("attested file cannot be read");
  }
}

async function hashRealFile(path) {
  const checked = await assertRealFilePath(path);
  if (!checked.info.isFile() || checked.info.size < 0n || checked.info.size > BigInt(Number.MAX_SAFE_INTEGER)) fail("attested file is invalid");
  let handle;
  try {
    handle = await open(checked.absolute, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!sameSnapshot(checked.info, before)) fail("attested file changed");
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
    if (!sameSnapshot(checked.info, after) || !sameSnapshot(before, after) || !sameSnapshot(checked.info, pathAfter) || byteCount !== Number(before.size)) fail("attested file changed");
    return { sha256: hash.digest("hex"), size: byteCount, bytes: Buffer.concat(chunks) };
  } catch (error) {
    if (error?.code === INVALID) throw error;
    fail("attested file cannot be read");
  } finally {
    await handle?.close().catch(() => {});
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
    ? ["--appimagetool", "--linux-mode-inventory", "--linux-summary", "--manifest", "--out", "--producer-event", "--producer-ref", "--producer-repository", "--producer-source-commit", "--producer-workflow-path", "--windows-summary"].sort()
    : command === "validate" ? ["--manifest", "--provenance"] : [];
  if (!hasExactKeys(options, expected)) fail();
  return { command, options };
}

async function writeCanonical(path, value) {
  if (typeof path !== "string" || path.length === 0) fail();
  const target = resolve(path);
  const temporary = `${target}.tmp-${process.pid}-${Date.now()}`;
  try {
    await mkdir(dirname(target), { recursive: true });
    const existing = await lstat(target).catch((error) => error?.code === "ENOENT" ? undefined : Promise.reject(error));
    if (existing?.isSymbolicLink() || (existing && !existing.isFile())) fail();
    await writeFile(temporary, `${JSON.stringify(value)}\n`, { encoding: "utf8", flag: "wx", mode: 0o600 });
    await rename(temporary, target);
  } catch (error) {
    await rm(temporary, { force: true }).catch(() => {});
    if (error?.code === INVALID) throw error;
    fail();
  }
}

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
