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
const runKeys = ["conclusion", "event", "head_branch", "head_sha", "id", "path", "repository", "run_attempt", "status"].sort();
const repositoryKeys = ["full_name"];
const metadataKeys = ["expectedConsumerCommit", "expectedRunAttempt", "expectedRunId", "run"].sort();
const metadataKeysWithoutAttempt = ["expectedConsumerCommit", "expectedRunId", "run"].sort();
const trustedInputKeys = ["artifacts", "expectedAppimagetoolSha256", "expectedConsumerCommit", "expectedLinuxLauncherSha256", "expectedRunAttempt", "expectedRunId", "expectedWindowsLauncherSha256", "provenance", "provenanceArtifactDigest", "provenanceArtifactId", "run"].sort();
const artifactListKeys = ["artifacts", "total_count"];
const artifactKeys = ["digest", "expired", "id", "name", "workflow_run"];
const workflowRunKeys = ["id"];
const runOutputKeys = ["run_id", "run_attempt", "provenance_artifact_id", "provenance_artifact_digest"];
const provenanceOutputKeys = ["run_id", "run_attempt", "windows_launcher_sha256", "linux_launcher_sha256", "appimagetool_sha256", "windows_artifact_id", "windows_artifact_digest", "linux_artifact_id", "linux_artifact_digest", "appimagetool_artifact_id", "appimagetool_artifact_digest"];
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

function canonicalApiId(value) {
  if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) return String(value);
  return undefined;
}

function canonicalExpectedRunId(value) {
  return typeof value === "string" && runIdPattern.test(value) ? value : undefined;
}

function canonicalExpectedAttempt(value) {
  if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) return value;
  if (typeof value !== "string" || !runIdPattern.test(value)) return undefined;
  const attempt = Number(value);
  return Number.isSafeInteger(attempt) ? attempt : undefined;
}

function canonicalApiAttempt(value) {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function canonicalRepositoryId(value) {
  return typeof value === "string" && runIdPattern.test(value) ? value : undefined;
}

function snapshotMetadataRequest(value) {
  return snapshotDataObject(value, metadataKeys) ?? snapshotDataObject(value, metadataKeysWithoutAttempt);
}

function trustedMetadata(request) {
  const input = snapshotMetadataRequest(request);
  if (!input) failUntrusted();
  const expectedRunId = canonicalExpectedRunId(input.expectedRunId);
  if (!expectedRunId || typeof input.expectedConsumerCommit !== "string" || !commitPattern.test(input.expectedConsumerCommit)) failUntrusted();
  const run = snapshotProjectedDataObject(input.run, runKeys);
  if (!run) failUntrusted();
  const repository = snapshotProjectedDataObject(run.repository, repositoryKeys);
  const runId = canonicalApiId(run.id);
  const runAttempt = canonicalApiAttempt(run.run_attempt);
  const expectedRunAttempt = Object.hasOwn(input, "expectedRunAttempt") ? canonicalExpectedAttempt(input.expectedRunAttempt) : undefined;
  if (!repository || !runId
    || !runAttempt
    || runId !== expectedRunId
    || (Object.hasOwn(input, "expectedRunAttempt") && (!expectedRunAttempt || runAttempt !== expectedRunAttempt))
    || repository.full_name !== "colayc/unitTest"
    || run.path !== ".github/workflows/release-inputs.yml"
    || run.event !== "workflow_dispatch"
    || run.head_branch !== "master"
    || run.head_sha !== input.expectedConsumerCommit
    || run.status !== "completed"
    || run.conclusion !== "success") failUntrusted();
  return Object.freeze({ runId, runAttempt, expectedConsumerCommit: input.expectedConsumerCommit });
}

export function validateProducerRunMetadata(request) {
  const metadata = trustedMetadata(request);
  return Object.freeze({ runId: metadata.runId, runAttempt: metadata.runAttempt });
}

function canonicalDigest(value) {
  return typeof value === "string" && digestPattern.test(value) ? value : undefined;
}

function snapshotGithubArtifacts(value, runId) {
  const api = snapshotProjectedDataObject(value, artifactListKeys);
  if (!api || !Array.isArray(api.artifacts)
    || !Number.isSafeInteger(api.total_count) || api.total_count < 0
    || api.total_count !== api.artifacts.length || api.total_count > 100) failUntrusted("producer artifacts are not trusted");
  const artifacts = [];
  for (const value of api.artifacts) {
    const artifact = snapshotProjectedDataObject(value, artifactKeys);
    const workflowRun = artifact && snapshotProjectedDataObject(artifact.workflow_run, workflowRunKeys);
    const artifactId = artifact && canonicalApiId(artifact.id);
    const workflowRunId = workflowRun && canonicalApiId(workflowRun.id);
    const digest = artifact && typeof artifact.digest === "string" && artifact.digest.startsWith("sha256:")
      ? canonicalDigest(artifact.digest.slice("sha256:".length)) : undefined;
    if (!artifact || !workflowRun || !artifactId || !workflowRunId || workflowRunId !== runId
      || typeof artifact.name !== "string" || typeof artifact.expired !== "boolean" || !digest) failUntrusted("producer artifacts are not trusted");
    artifacts.push(Object.freeze({ id: artifactId, name: artifact.name, expired: artifact.expired, digest, runId: workflowRunId }));
  }
  return Object.freeze(artifacts);
}

function selectArtifact(artifacts, transportName) {
  const matches = artifacts.filter((artifact) => artifact.name === transportName);
  if (matches.length !== 1 || matches[0].expired) failUntrusted("producer artifact is not trusted");
  return matches[0];
}

export function selectProvenanceArtifact(request) {
  const input = snapshotDataObject(request, ["artifacts", "runAttempt", "runId"].sort());
  const runId = input && canonicalRepositoryId(input.runId);
  const runAttempt = input && canonicalExpectedAttempt(input.runAttempt);
  if (!input || !runId || !runAttempt) failUntrusted("producer artifact selection is invalid");
  const artifact = selectArtifact(snapshotGithubArtifacts(input.artifacts, runId), `release-input-provenance-${runAttempt}`);
  return Object.freeze({
    provenanceArtifactId: artifact.id,
    provenanceArtifactDigest: artifact.digest,
    provenanceTransportName: artifact.name,
  });
}

function validateArtifactBinding(artifacts, provenanceArtifact, logicalName, runAttempt) {
  const artifact = selectArtifact(artifacts, `${logicalName}-${runAttempt}`);
  if (provenanceArtifact.artifactName !== logicalName
    || provenanceArtifact.artifactId !== artifact.id
    || provenanceArtifact.artifactDigest !== artifact.digest
    || provenanceArtifact.transportName !== artifact.name) failInvalid();
  return artifact;
}

export function validateTrustedReleaseInputs(request) {
  const input = snapshotDataObject(request, trustedInputKeys);
  if (!input) failInvalid();
  const metadata = trustedMetadata({
    run: input.run,
    expectedRunId: input.expectedRunId,
    expectedConsumerCommit: input.expectedConsumerCommit,
    expectedRunAttempt: input.expectedRunAttempt,
  });
  const provenanceArtifactId = canonicalRepositoryId(input.provenanceArtifactId);
  const provenanceArtifactDigest = canonicalDigest(input.provenanceArtifactDigest);
  const windowsLauncherSha256 = canonicalDigest(input.expectedWindowsLauncherSha256);
  const linuxLauncherSha256 = canonicalDigest(input.expectedLinuxLauncherSha256);
  const appimagetoolSha256 = canonicalDigest(input.expectedAppimagetoolSha256);
  if (!provenanceArtifactId || !provenanceArtifactDigest || !windowsLauncherSha256 || !linuxLauncherSha256 || !appimagetoolSha256) failInvalid();
  const artifacts = snapshotGithubArtifacts(input.artifacts, metadata.runId);
  const selectedProvenance = selectArtifact(artifacts, `release-input-provenance-${metadata.runAttempt}`);
  if (selectedProvenance.id !== provenanceArtifactId || selectedProvenance.digest !== provenanceArtifactDigest) failUntrusted("provenance artifact changed");
  let provenance;
  try {
    provenance = validateReleaseInputProvenance(input.provenance);
  } catch {
    failInvalid();
  }
  if (provenance.producer.sourceCommit !== metadata.expectedConsumerCommit
    || provenance.producer.runId !== metadata.runId
    || provenance.producer.runAttempt !== metadata.runAttempt
    || provenance.runtimes.windows.launcherSha256 !== windowsLauncherSha256
    || provenance.runtimes.linux.launcherSha256 !== linuxLauncherSha256
    || provenance.appimagetool.sha256 !== appimagetoolSha256) failInvalid();
  const windowsArtifact = validateArtifactBinding(artifacts, provenance.runtimes.windows, "code-oss-windows-x64", metadata.runAttempt);
  const linuxArtifact = validateArtifactBinding(artifacts, provenance.runtimes.linux, "code-oss-linux-x64", metadata.runAttempt);
  const appimagetoolArtifact = validateArtifactBinding(artifacts, provenance.appimagetool, "appimagetool-linux-x64", metadata.runAttempt);
  return Object.freeze({
    runId: metadata.runId,
    runAttempt: metadata.runAttempt,
    windowsLauncherSha256,
    linuxLauncherSha256,
    appimagetoolSha256,
    windowsArtifactId: windowsArtifact.id,
    windowsArtifactDigest: windowsArtifact.digest,
    linuxArtifactId: linuxArtifact.id,
    linuxArtifactDigest: linuxArtifact.digest,
    appimagetoolArtifactId: appimagetoolArtifact.id,
    appimagetoolArtifactDigest: appimagetoolArtifact.digest,
  });
}

function sameNode(left, right) {
  return left.dev === right.dev && left.ino === right.ino && left.size === right.size && left.mode === right.mode
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

function snapshotHooks(value, allowed, reason) {
  if (!isPlainObject(value)) failUntrusted(reason);
  const snapshot = {};
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !allowed.includes(key)) failUntrusted(reason);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value") || typeof descriptor.value !== "function") failUntrusted(reason);
    snapshot[key] = descriptor.value;
  }
  return Object.freeze(snapshot);
}

function readHooks(value) {
  return snapshotHooks(value, ["afterOpenSnapshot", "afterRead"], "producer run input is invalid");
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
  return snapshotHooks(value, ["afterWrite", "sync", "write"], "GitHub output is invalid");
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
    const expectedBytes = Buffer.concat([originalBytes, bytes]);
    const afterWrite = await handle.stat({ bigint: true });
    if (!isSingleLinkedRegularFile(afterWrite) || afterWrite.size !== BigInt(expectedBytes.length)) failUntrusted("GitHub output changed");
    const currentBytes = Buffer.alloc(expectedBytes.length);
    await readFully(handle, currentBytes, 0);
    const after = await handle.stat({ bigint: true });
    if (!sameSingleLinkedFile(afterWrite, after) || !currentBytes.equals(expectedBytes)) failUntrusted("GitHub output changed");
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
    ? ["--artifacts-json", "--consumer-commit", "--github-output", "--run-id", "--run-json"].sort()
    : command === "validate-provenance"
      ? ["--appimagetool-sha256", "--artifacts-json", "--consumer-commit", "--github-output", "--linux-launcher-sha256", "--provenance", "--provenance-artifact-digest", "--provenance-artifact-id", "--run-attempt", "--run-id", "--run-json", "--windows-launcher-sha256"].sort()
      : command === "validate-attempt"
        ? ["--consumer-commit", "--run-attempt", "--run-id", "--run-json"].sort()
        : [];
  if (!snapshotDataObject(options, expected)) failUntrusted("command arguments are invalid");
  return { command, options };
}

async function main(argv) {
  const { command, options } = parseCli(argv);
  const run = await readTrustedJson(options["--run-json"], UNTRUSTED);
  if (command === "validate-run") {
    const result = validateProducerRunMetadata({ run, expectedRunId: options["--run-id"], expectedConsumerCommit: options["--consumer-commit"] });
    const provenance = selectProvenanceArtifact({
      artifacts: await readTrustedJson(options["--artifacts-json"], UNTRUSTED), runId: result.runId, runAttempt: result.runAttempt,
    });
    await appendGithubOutput(options["--github-output"], [
      ["run_id", result.runId], ["run_attempt", String(result.runAttempt)],
      ["provenance_artifact_id", provenance.provenanceArtifactId], ["provenance_artifact_digest", provenance.provenanceArtifactDigest],
    ]);
    return;
  }
  if (command === "validate-provenance") {
    const provenance = await readTrustedJson(options["--provenance"], INVALID);
    const result = validateTrustedReleaseInputs({
      run, provenance, artifacts: await readTrustedJson(options["--artifacts-json"], UNTRUSTED),
      expectedRunId: options["--run-id"], expectedRunAttempt: options["--run-attempt"], expectedConsumerCommit: options["--consumer-commit"],
      provenanceArtifactId: options["--provenance-artifact-id"], provenanceArtifactDigest: options["--provenance-artifact-digest"],
      expectedWindowsLauncherSha256: options["--windows-launcher-sha256"], expectedLinuxLauncherSha256: options["--linux-launcher-sha256"],
      expectedAppimagetoolSha256: options["--appimagetool-sha256"],
    });
    await appendGithubOutput(options["--github-output"], [
      ["run_id", result.runId], ["run_attempt", String(result.runAttempt)], ["windows_launcher_sha256", result.windowsLauncherSha256],
      ["linux_launcher_sha256", result.linuxLauncherSha256], ["appimagetool_sha256", result.appimagetoolSha256],
      ["windows_artifact_id", result.windowsArtifactId], ["windows_artifact_digest", result.windowsArtifactDigest],
      ["linux_artifact_id", result.linuxArtifactId], ["linux_artifact_digest", result.linuxArtifactDigest],
      ["appimagetool_artifact_id", result.appimagetoolArtifactId], ["appimagetool_artifact_digest", result.appimagetoolArtifactDigest],
    ]);
    return;
  }
  if (command === "validate-attempt") {
    validateProducerRunMetadata({
      run, expectedRunId: options["--run-id"], expectedRunAttempt: options["--run-attempt"], expectedConsumerCommit: options["--consumer-commit"],
    });
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
