import { constants } from "node:fs";
import { execFile } from "node:child_process";
import { lstat, open } from "node:fs/promises";
import { dirname, join, parse, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import { validateReleaseInputProvenance } from "./provenance.mjs";

const UNTRUSTED = "RELEASE_PRODUCER_UNTRUSTED";
const INVALID = "RELEASE_PRODUCER_PROVENANCE_INVALID";
const runIdPattern = /^[1-9][0-9]*$/u;
const commitPattern = /^[0-9a-f]{40}$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
const runKeys = ["conclusion", "event", "head_branch", "head_sha", "id", "path", "repository", "status"].sort();
const repositoryKeys = ["full_name"];
const metadataKeys = ["expectedConsumerCommit", "expectedRunId", "run"].sort();
const trustedInputKeys = ["expectedAppimagetoolSha256", "expectedConsumerCommit", "expectedLinuxLauncherSha256", "expectedRunId", "expectedWindowsLauncherSha256", "provenance", "run"].sort();
const runOutputKeys = ["run_id"];
const provenanceOutputKeys = ["run_id", "windows_launcher_sha256", "linux_launcher_sha256", "appimagetool_sha256"];
const maximumGithubOutputBytes = 1024 * 1024;
const execFileAsync = promisify(execFile);
const windowsReparsePointCommand = "$node=Get-Item -Force -LiteralPath $env:TRUSTED_RUN_PATH; while ($null -ne $node) { if (($node.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { [Console]::Out.Write('1'); exit 0 }; $node=$node.Parent }; [Console]::Out.Write('0')";

function failure(code, reason) {
  const error = new Error(`${code}: ${reason}`);
  error.code = code;
  return error;
}

function failUntrusted(reason = "producer run is not trusted") {
  throw failure(UNTRUSTED, reason);
}

function failInvalid(reason = "release input provenance is invalid") {
  throw failure(INVALID, reason);
}

function isPlainObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function snapshotDataObject(value, expectedKeys) {
  if (!isPlainObject(value)) return undefined;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) return undefined;
  const sorted = [...keys].sort();
  if (sorted.length !== expectedKeys.length || !sorted.every((key, index) => key === expectedKeys[index])) return undefined;
  const snapshot = {};
  for (const key of expectedKeys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value")) return undefined;
    snapshot[key] = descriptor.value;
  }
  return snapshot;
}

function snapshotProjectedDataObject(value, requiredKeys) {
  if (!isPlainObject(value)) return undefined;
  const snapshot = {};
  for (const key of requiredKeys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value")) return undefined;
    snapshot[key] = descriptor.value;
  }
  return snapshot;
}

function canonicalRunId(value) {
  if (typeof value === "string" && runIdPattern.test(value)) return value;
  if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) return String(value);
  return undefined;
}

function canonicalExpectedRunId(value) {
  return typeof value === "string" && runIdPattern.test(value) ? value : undefined;
}

function trustedMetadata(request) {
  const input = snapshotDataObject(request, metadataKeys);
  if (!input) failUntrusted();
  const expectedRunId = canonicalExpectedRunId(input.expectedRunId);
  if (!expectedRunId || typeof input.expectedConsumerCommit !== "string" || !commitPattern.test(input.expectedConsumerCommit)) failUntrusted();
  const run = snapshotProjectedDataObject(input.run, runKeys);
  if (!run) failUntrusted();
  const repository = snapshotProjectedDataObject(run.repository, repositoryKeys);
  const runId = canonicalRunId(run.id);
  if (!repository || !runId
    || runId !== expectedRunId
    || repository.full_name !== "colayc/unitTest"
    || run.path !== ".github/workflows/release-inputs.yml"
    || run.event !== "workflow_dispatch"
    || run.head_branch !== "master"
    || run.head_sha !== input.expectedConsumerCommit
    || run.status !== "completed"
    || run.conclusion !== "success") failUntrusted();
  return Object.freeze({ runId, expectedConsumerCommit: input.expectedConsumerCommit });
}

export function validateProducerRunMetadata(request) {
  const metadata = trustedMetadata(request);
  return Object.freeze({ runId: metadata.runId });
}

function canonicalDigest(value) {
  return typeof value === "string" && digestPattern.test(value) ? value : undefined;
}

export function validateTrustedReleaseInputs(request) {
  const input = snapshotDataObject(request, trustedInputKeys);
  if (!input) failInvalid();
  const metadata = trustedMetadata({
    run: input.run,
    expectedRunId: input.expectedRunId,
    expectedConsumerCommit: input.expectedConsumerCommit,
  });
  const windows = canonicalDigest(input.expectedWindowsLauncherSha256);
  const linux = canonicalDigest(input.expectedLinuxLauncherSha256);
  const appimagetool = canonicalDigest(input.expectedAppimagetoolSha256);
  if (!windows || !linux || !appimagetool) failInvalid();
  let provenance;
  try {
    provenance = validateReleaseInputProvenance(input.provenance);
  } catch {
    failInvalid();
  }
  if (provenance.producer.sourceCommit !== metadata.expectedConsumerCommit
    || provenance.runtimes.windows.launcherSha256 !== windows
    || provenance.runtimes.linux.launcherSha256 !== linux
    || provenance.appimagetool.sha256 !== appimagetool) failInvalid();
  return Object.freeze({
    runId: metadata.runId,
    windowsLauncherSha256: windows,
    linuxLauncherSha256: linux,
    appimagetoolSha256: appimagetool,
  });
}

function sameNode(left, right) {
  return left.dev === right.dev && left.ino === right.ino && left.mode === right.mode
    && left.mtimeNs === right.mtimeNs && left.ctimeNs === right.ctimeNs
    && left.isFile() === right.isFile() && left.isDirectory() === right.isDirectory();
}

function sameDirectoryIdentity(left, right) {
  return left.dev === right.dev && left.ino === right.ino && left.mode === right.mode && right.isDirectory();
}

function isSingleLinkedRegularFile(info) {
  return info.isFile() && info.nlink === 1n;
}

function sameSingleLinkedFile(left, right) {
  return isSingleLinkedRegularFile(left) && isSingleLinkedRegularFile(right) && sameNode(left, right);
}

async function rejectWindowsReparsePoints(path) {
  if (process.platform !== "win32") return;
  try {
    const { stdout } = await execFileAsync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", windowsReparsePointCommand], {
      encoding: "utf8", env: { ...process.env, TRUSTED_RUN_PATH: path }, windowsHide: true,
    });
    if (stdout.trim() !== "0") failUntrusted("producer run input is linked");
  } catch (error) {
    if (error?.code === UNTRUSTED) throw error;
    failUntrusted("producer run input cannot be inspected");
  }
}

async function realPath(path, requireFile) {
  if (typeof path !== "string" || path.length === 0) failUntrusted("producer run path is invalid");
  const absolute = resolve(path);
  const root = parse(absolute).root;
  let current = root;
  try {
    await rejectWindowsReparsePoints(absolute);
    const rootInfo = await lstat(root, { bigint: true });
    if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory()) failUntrusted("producer run path is invalid");
    const ancestors = [{ path: root, info: rootInfo }];
    const components = absolute.slice(root.length).split(/[\\/]+/u).filter(Boolean);
    for (let index = 0; index < components.length; index += 1) {
      current = join(current, components[index]);
      const info = await lstat(current, { bigint: true });
      const leaf = index === components.length - 1;
      if (info.isSymbolicLink() || (leaf ? (requireFile && !isSingleLinkedRegularFile(info)) : !info.isDirectory())) failUntrusted("producer run path is invalid");
      if (!leaf) ancestors.push({ path: current, info });
      if (leaf) return { absolute, info, ancestors };
    }
  } catch (error) {
    if (error?.code === UNTRUSTED) throw error;
    failUntrusted("producer run path cannot be inspected");
  }
  failUntrusted("producer run path is invalid");
}

async function assertUnchangedAncestors(snapshot) {
  await rejectWindowsReparsePoints(snapshot.absolute);
  for (const ancestor of snapshot.ancestors) {
    const current = await lstat(ancestor.path, { bigint: true });
    if (current.isSymbolicLink() || !sameDirectoryIdentity(ancestor.info, current)) failUntrusted("producer run path changed");
  }
}

function readHooks(value) {
  if (!isPlainObject(value)) failUntrusted("producer run input is invalid");
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !["afterOpenSnapshot", "afterRead"].includes(key)) failUntrusted("producer run input is invalid");
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value") || typeof descriptor.value !== "function") failUntrusted("producer run input is invalid");
  }
  return value;
}

async function readTrustedJson(path, errorCode, suppliedHooks = {}) {
  const hooks = readHooks(suppliedHooks);
  let checked;
  let handle;
  try {
    checked = await realPath(path, true);
    handle = await open(checked.absolute, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!sameSingleLinkedFile(checked.info, before)) failUntrusted("producer run input changed");
    await hooks.afterOpenSnapshot?.();
    const bytes = await handle.readFile();
    await hooks.afterRead?.();
    const after = await handle.stat({ bigint: true });
    const pathAfter = await lstat(checked.absolute, { bigint: true });
    await assertUnchangedAncestors(checked);
    if (!sameSingleLinkedFile(before, after) || !sameSingleLinkedFile(checked.info, pathAfter)) failUntrusted("producer run input changed");
    try {
      return JSON.parse(bytes.toString("utf8"));
    } catch {
      if (errorCode === INVALID) failInvalid();
      failUntrusted("producer run JSON is invalid");
    }
  } catch (error) {
    if (error?.code === UNTRUSTED || error?.code === INVALID) throw error;
    if (errorCode === INVALID) failInvalid();
    failUntrusted("producer run input cannot be read");
  } finally {
    await handle?.close().catch(() => {});
  }
}

function outputHooks(value) {
  if (!isPlainObject(value)) failUntrusted("GitHub output is invalid");
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !["afterWrite", "sync", "write"].includes(key)) failUntrusted("GitHub output is invalid");
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value") || typeof descriptor.value !== "function") failUntrusted("GitHub output is invalid");
  }
  return value;
}

async function writeFully(handle, bytes, initialPosition, hooks) {
  let offset = 0;
  while (offset < bytes.length) {
    const result = hooks.write === undefined
      ? await handle.write(bytes, offset, bytes.length - offset, initialPosition + offset)
      : await hooks.write(handle, bytes, offset, bytes.length - offset, initialPosition + offset);
    if (!Number.isSafeInteger(result?.bytesWritten) || result.bytesWritten <= 0 || result.bytesWritten > bytes.length - offset) failUntrusted("GitHub output could not be written");
    offset += result.bytesWritten;
  }
}

async function syncOutput(handle, hooks) {
  if (hooks.sync === undefined) await handle.sync();
  else await hooks.sync(handle);
}

async function readFully(handle, bytes, initialPosition) {
  let offset = 0;
  while (offset < bytes.length) {
    const result = await handle.read(bytes, offset, bytes.length - offset, initialPosition + offset);
    if (!Number.isSafeInteger(result?.bytesRead) || result.bytesRead <= 0 || result.bytesRead > bytes.length - offset) failUntrusted("GitHub output changed");
    offset += result.bytesRead;
  }
}

async function rollbackAppend(handle, originalBytes, originalSize) {
  try {
    await writeFully(handle, originalBytes, 0, {});
    await handle.truncate(Number(originalSize));
    await handle.sync();
    const restored = await handle.stat({ bigint: true });
    if (!isSingleLinkedRegularFile(restored) || restored.size !== originalSize) failUntrusted("GitHub output rollback failed");
    const restoredBytes = Buffer.alloc(Number(originalSize));
    await readFully(handle, restoredBytes, 0);
    if (!restoredBytes.equals(originalBytes)) failUntrusted("GitHub output rollback failed");
  } catch (error) {
    if (error?.code === UNTRUSTED) throw error;
    failUntrusted("GitHub output rollback failed");
  }
}

async function appendGithubOutput(path, entries, suppliedHooks = {}) {
  const hooks = outputHooks(suppliedHooks);
  if (!Array.isArray(entries) || ![runOutputKeys, provenanceOutputKeys].some((expected) => (
    entries.length === expected.length && entries.every((entry, index) => Array.isArray(entry) && entry.length === 2 && entry[0] === expected[index])
  ))) failUntrusted("GitHub output is invalid");
  const bytes = Buffer.from(entries.map(([key, value]) => {
    if (!/^[a-z][a-z0-9_]*$/u.test(key) || !/^(?:[1-9][0-9]*|[0-9a-f]{64})$/u.test(value)) failUntrusted("GitHub output is invalid");
    return `${key}=${value}`;
  }).join("\n") + "\n", "utf8");
  let checked;
  let handle;
  let originalSize;
  let originalBytes;
  let rollbackRequired = false;
  try {
    checked = await realPath(path, true);
    handle = await open(checked.absolute, constants.O_RDWR | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!sameSingleLinkedFile(checked.info, before)) failUntrusted("GitHub output changed");
    if (before.size > BigInt(maximumGithubOutputBytes)) failUntrusted("GitHub output is invalid");
    originalSize = before.size;
    originalBytes = Buffer.alloc(Number(originalSize));
    await readFully(handle, originalBytes, 0);
    const afterSnapshot = await handle.stat({ bigint: true });
    if (!sameSingleLinkedFile(before, afterSnapshot)) failUntrusted("GitHub output changed");
    rollbackRequired = true;
    await writeFully(handle, bytes, Number(originalSize), hooks);
    await hooks.afterWrite?.();
    await syncOutput(handle, hooks);
    const currentPrefix = Buffer.alloc(originalBytes.length);
    await readFully(handle, currentPrefix, 0);
    if (!currentPrefix.equals(originalBytes)) failUntrusted("GitHub output changed");
    const after = await handle.stat({ bigint: true });
    const pathAfter = await lstat(checked.absolute, { bigint: true });
    await assertUnchangedAncestors(checked);
    if (!sameSingleLinkedFile(after, pathAfter) || !isSingleLinkedRegularFile(after) || after.size !== before.size + BigInt(bytes.length)) failUntrusted("GitHub output changed");
    rollbackRequired = false;
  } catch (error) {
    if (handle !== undefined && rollbackRequired && originalSize !== undefined && originalBytes !== undefined) await rollbackAppend(handle, originalBytes, originalSize);
    if (error?.code === UNTRUSTED) throw error;
    failUntrusted("GitHub output cannot be written");
  } finally {
    await handle?.close().catch(() => {});
  }
}

export const __testOnlyTrustedRun = Object.freeze({
  appendGithubOutput(path, entries, hooks) {
    return appendGithubOutput(path, entries, outputHooks(hooks));
  },
  readTrustedJson(path, errorCode, hooks) {
    return readTrustedJson(path, errorCode, readHooks(hooks));
  },
});

function parseCli(argv) {
  const [command, ...args] = argv;
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index];
    const value = args[index + 1];
    if (typeof key !== "string" || !key.startsWith("--") || value === undefined || Object.hasOwn(options, key)) failUntrusted("command arguments are invalid");
    options[key] = value;
  }
  const expected = command === "validate-run"
    ? ["--consumer-commit", "--github-output", "--run-id", "--run-json"].sort()
    : command === "validate-provenance"
      ? ["--appimagetool-sha256", "--consumer-commit", "--github-output", "--linux-launcher-sha256", "--provenance", "--run-id", "--run-json", "--windows-launcher-sha256"].sort()
      : [];
  if (!snapshotDataObject(options, expected)) failUntrusted("command arguments are invalid");
  return { command, options };
}

async function main(argv) {
  const { command, options } = parseCli(argv);
  const run = await readTrustedJson(options["--run-json"], UNTRUSTED);
  if (command === "validate-run") {
    const result = validateProducerRunMetadata({ run, expectedRunId: options["--run-id"], expectedConsumerCommit: options["--consumer-commit"] });
    await appendGithubOutput(options["--github-output"], [["run_id", result.runId]]);
    return;
  }
  if (command === "validate-provenance") {
    const provenance = await readTrustedJson(options["--provenance"], INVALID);
    const result = validateTrustedReleaseInputs({
      run, provenance, expectedRunId: options["--run-id"], expectedConsumerCommit: options["--consumer-commit"],
      expectedWindowsLauncherSha256: options["--windows-launcher-sha256"],
      expectedLinuxLauncherSha256: options["--linux-launcher-sha256"],
      expectedAppimagetoolSha256: options["--appimagetool-sha256"],
    });
    await appendGithubOutput(options["--github-output"], [
      ["run_id", result.runId], ["windows_launcher_sha256", result.windowsLauncherSha256],
      ["linux_launcher_sha256", result.linuxLauncherSha256], ["appimagetool_sha256", result.appimagetoolSha256],
    ]);
    return;
  }
  failUntrusted("command is invalid");
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    const code = error?.code === INVALID ? INVALID : UNTRUSTED;
    process.stderr.write(`${error?.code === code ? error.message : `${code}: ${code === INVALID ? "release input provenance is invalid" : "producer run is not trusted"}`}\n`);
    process.exitCode = 1;
  });
}
