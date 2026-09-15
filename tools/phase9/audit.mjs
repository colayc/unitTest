import { open, readdir } from "node:fs/promises";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { inflateRawSync } from "node:zlib";

import { encodeCanonicalJson, phase9Failure, readCanonicalJson } from "./canonical-json.mjs";
import { writeMatrixOutputs } from "./render.mjs";
import { validateMatrix } from "./validate.mjs";
import { validateP7Report, validateP7ReportDocument } from "./p7-report.mjs";
import {
  P8_REPORT_ARTIFACTS,
  validateP8Report,
  validateP8ReportDocument,
} from "./p8-report.mjs";

const EXPECTED_REPOSITORY = "colayc/unitTest";
const RECEIPT_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/u;
const DECIMAL_ID_PATTERN = /^[1-9][0-9]*$/u;
const COUNT_PATTERN = /^(?:0|[1-9][0-9]*)$/u;
const COMMIT_PATTERN = /^[0-9a-f]{40}$/u;
const DIGEST_PATTERN = /^[0-9a-f]{64}$/u;
const UTC_TIMESTAMP_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/u;
const SAFE_TEXT_PATTERN = /^[^\0\r\n]+$/u;
const CONCLUSIONS = new Set(["success", "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped"]);
const GENERATED_AUDIT_ARTIFACT_PATTERN = /^phase9-gate-audit-[1-9][0-9]*$/u;
const SNAPSHOT_FILES = Object.freeze(["artifacts.json", "jobs.json", "run.json"]);
const ALLOWED_DEFERRED_GATE_IDS = new Set([
  "P8-DOCS-CLOSEOUT",
  "P8-LEGAL-THIRD-PARTY",
  "P8-SIGN-WINDOWS",
]);
const P7_SEMANTIC_REPORT_GATES = new Set([
  "P7-COVERAGE-UI-AND-SOURCE-DECORATION",
  "P7-HISTORY-AND-ARTIFACT-BROWSER",
  "P7-MAIN-USER-JOURNEY",
  "P7-MOCK-CONFIGURATION-UX",
]);
const P8_SEMANTIC_REPORT_GATES = new Set(Object.keys(P8_REPORT_ARTIFACTS));
const MAX_MATRIX_BYTES = 1024 * 1024;
const MAX_RECEIPT_BYTES = 256 * 1024;
const MAX_RECEIPTS = 256;
const MAX_SNAPSHOT_DIRECTORIES = 256;
const MAX_RUN_BYTES = 2 * 1024 * 1024;
const MAX_JOBS_BYTES = 8 * 1024 * 1024;
const MAX_ARTIFACTS_BYTES = 8 * 1024 * 1024;
const MAX_REPORT_ARCHIVE_BYTES = 256 * 1024;
const MAX_REPORT_BYTES = 128 * 1024;
const REPORT_ARCHIVE_NAME = /^([1-9][0-9]*)\.zip$/u;

function safeReceiptId(receipt) {
  return RECEIPT_ID_PATTERN.test(receipt?.receiptId ?? "") ? receipt.receiptId : "unknown-receipt";
}

function untrusted(receiptId, reason) {
  throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", `${receiptId}: ${reason}`);
}

function fail(receipt, reason) {
  untrusted(safeReceiptId(receipt), reason);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasExactKeys(value, keys) {
  if (!isObject(value)) return false;
  const actual = Object.keys(value).sort((left, right) => left.localeCompare(right, "en"));
  const expected = [...keys].sort((left, right) => left.localeCompare(right, "en"));
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
}

function isDenseArray(value) {
  if (!Array.isArray(value)) return false;
  for (let index = 0; index < value.length; index += 1) {
    if (!Object.hasOwn(value, index)) return false;
  }
  return true;
}

function isSafeText(value) {
  return typeof value === "string" && value.length > 0 && value.trim() === value && SAFE_TEXT_PATTERN.test(value);
}

function isUtcTimestamp(value) {
  return typeof value === "string" && UTC_TIMESTAMP_PATTERN.test(value) && Number.isFinite(Date.parse(value));
}

function isSafeRepositoryPath(value) {
  if (!isSafeText(value) || value.startsWith("/") || value.includes("\\") || value.includes(":")) return false;
  const segments = value.split("/");
  return segments.every((segment) => segment !== "" && segment !== "." && segment !== "..");
}

function canonicalId(value) {
  if (typeof value === "string") return DECIMAL_ID_PATTERN.test(value) ? value : undefined;
  if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) return String(value);
  return undefined;
}

function canonicalCount(value) {
  if (typeof value === "string") return COUNT_PATTERN.test(value) ? value : undefined;
  if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) return String(value);
  return undefined;
}

function normalizedDigest(value) {
  if (typeof value !== "string") return undefined;
  const digest = value.startsWith("sha256:") ? value.slice("sha256:".length) : value;
  return DIGEST_PATTERN.test(digest) ? digest : undefined;
}

function crc32(bytes) {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ 0xffffffff) >>> 0;
}

export function parseP8ReportArchive(bytes) {
  if (!Buffer.isBuffer(bytes) || bytes.length === 0 || bytes.length > MAX_REPORT_ARCHIVE_BYTES) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "P8 report archive size is invalid");
  }
  const minimumEocd = 22;
  let eocd = -1;
  for (let index = bytes.length - minimumEocd; index >= Math.max(0, bytes.length - 65557); index -= 1) {
    if (bytes.readUInt32LE(index) === 0x06054b50) {
      eocd = index;
      break;
    }
  }
  if (eocd < 0
      || bytes.readUInt16LE(eocd + 4) !== 0
      || bytes.readUInt16LE(eocd + 6) !== 0
      || bytes.readUInt16LE(eocd + 8) !== 1
      || bytes.readUInt16LE(eocd + 10) !== 1
      || bytes.readUInt16LE(eocd + 20) !== 0
      || eocd + minimumEocd !== bytes.length) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "P8 report archive directory is invalid");
  }
  const centralSize = bytes.readUInt32LE(eocd + 12);
  const centralOffset = bytes.readUInt32LE(eocd + 16);
  if (centralOffset + centralSize !== eocd || centralSize < 46
      || bytes.readUInt32LE(centralOffset) !== 0x02014b50) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "P8 report archive entry is invalid");
  }
  const flags = bytes.readUInt16LE(centralOffset + 8);
  const method = bytes.readUInt16LE(centralOffset + 10);
  const expectedCrc = bytes.readUInt32LE(centralOffset + 16);
  const compressedSize = bytes.readUInt32LE(centralOffset + 20);
  const uncompressedSize = bytes.readUInt32LE(centralOffset + 24);
  const nameLength = bytes.readUInt16LE(centralOffset + 28);
  const extraLength = bytes.readUInt16LE(centralOffset + 30);
  const commentLength = bytes.readUInt16LE(centralOffset + 32);
  const localOffset = bytes.readUInt32LE(centralOffset + 42);
  const centralEnd = centralOffset + 46 + nameLength + extraLength + commentLength;
  if ((flags & ~0x808) !== 0 || ![0, 8].includes(method)
      || compressedSize > MAX_REPORT_ARCHIVE_BYTES || uncompressedSize === 0 || uncompressedSize > MAX_REPORT_BYTES
      || commentLength !== 0 || centralEnd !== eocd
      || localOffset + 30 > centralOffset || bytes.readUInt32LE(localOffset) !== 0x04034b50) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "P8 report archive entry is invalid");
  }
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let centralName;
  let localName;
  try {
    centralName = decoder.decode(bytes.subarray(centralOffset + 46, centralOffset + 46 + nameLength));
    const localFlags = bytes.readUInt16LE(localOffset + 6);
    const localMethod = bytes.readUInt16LE(localOffset + 8);
    const localNameLength = bytes.readUInt16LE(localOffset + 26);
    const localExtraLength = bytes.readUInt16LE(localOffset + 28);
    localName = decoder.decode(bytes.subarray(localOffset + 30, localOffset + 30 + localNameLength));
    if (localFlags !== flags || localMethod !== method || localName !== centralName) throw new Error("local header mismatch");
    const dataOffset = localOffset + 30 + localNameLength + localExtraLength;
    if (dataOffset + compressedSize > centralOffset) throw new Error("entry overlaps directory");
    const compressed = bytes.subarray(dataOffset, dataOffset + compressedSize);
    const content = method === 0 ? Buffer.from(compressed) : inflateRawSync(compressed, { maxOutputLength: MAX_REPORT_BYTES });
    if (centralName !== "p8-report.json" || content.length !== uncompressedSize || crc32(content) !== expectedCrc) {
      throw new Error("entry content mismatch");
    }
    const source = decoder.decode(content);
    const report = JSON.parse(source);
    if (report === null || typeof report !== "object" || Array.isArray(report)
        || encodeCanonicalJson(report) !== source) throw new Error("report is not canonical");
    validateP8ReportDocument(report);
    return {
      archiveDigest: createHash("sha256").update(bytes).digest("hex"),
      report,
    };
  } catch (error) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "P8 report archive content is invalid", error);
  }
}

function assertReceipt(receipt) {
  const topKeys = ["schemaVersion", "receiptId", "candidateCommit", "observedAt", "gateIds", "evidence"];
  if (!hasExactKeys(receipt, topKeys)
      || receipt.schemaVersion !== 1
      || !RECEIPT_ID_PATTERN.test(receipt.receiptId ?? "")
      || !COMMIT_PATTERN.test(receipt.candidateCommit ?? "")
      || !isUtcTimestamp(receipt.observedAt)
      || !isDenseArray(receipt.gateIds)
      || receipt.gateIds.some((gateId) => !isSafeText(gateId))
      || new Set(receipt.gateIds).size !== receipt.gateIds.length) {
    fail(receipt, "receipt is invalid");
  }
  const evidenceKeys = [
    "kind", "repository", "workflowPath", "runId", "runAttempt", "event", "headSha", "conclusion", "jobs", "artifacts",
  ];
  const evidence = receipt.evidence;
  if (!hasExactKeys(evidence, evidenceKeys)
      || evidence.kind !== "github-actions"
      || evidence.repository !== EXPECTED_REPOSITORY
      || !isSafeRepositoryPath(evidence.workflowPath)
      || canonicalId(evidence.runId) !== evidence.runId
      || !Number.isSafeInteger(evidence.runAttempt)
      || evidence.runAttempt < 1
      || !isSafeText(evidence.event)
      || !COMMIT_PATTERN.test(evidence.headSha ?? "")
      || evidence.headSha !== receipt.candidateCommit
      || evidence.conclusion !== "success"
      || !isDenseArray(evidence.jobs)
      || !isDenseArray(evidence.artifacts)) {
    fail(receipt, "receipt evidence is invalid");
  }
  const jobNames = new Set();
  for (const job of evidence.jobs) {
    if (!hasExactKeys(job, ["name", "conclusion"])
        || !isSafeText(job.name)
        || !CONCLUSIONS.has(job.conclusion)
        || jobNames.has(job.name)) {
      fail(receipt, "receipt jobs are invalid");
    }
    jobNames.add(job.name);
  }
  const artifactIds = new Set();
  const artifactNames = new Set();
  for (const artifact of evidence.artifacts) {
    const artifactKeys = artifact?.report === undefined
      ? ["id", "name", "digest", "expired"]
      : ["id", "name", "digest", "expired", "report"];
    if (!hasExactKeys(artifact, artifactKeys)
        || canonicalId(artifact.id) !== artifact.id
        || !isSafeText(artifact.name)
        || !DIGEST_PATTERN.test(artifact.digest ?? "")
        || artifact.expired !== false
        || artifactIds.has(artifact.id)
        || artifactNames.has(artifact.name)) {
      fail(receipt, "receipt artifacts are invalid");
    }
    artifactIds.add(artifact.id);
    artifactNames.add(artifact.name);
    if (artifact.report !== undefined) {
      try {
        if (artifact.report?.gateId?.startsWith("P7-")) validateP7ReportDocument(artifact.report);
        else validateP8ReportDocument(artifact.report);
      } catch {
        fail(receipt, "receipt artifact report is invalid");
      }
      if (artifact.report.candidateCommit !== receipt.candidateCommit
          || artifact.report.sourceCommit !== receipt.candidateCommit
          || artifact.report.runAttempt !== evidence.runAttempt) {
        fail(receipt, "receipt artifact report identity is invalid");
      }
    }
  }
}

function normalizeRunSnapshot(receipt, snapshot) {
  if (!isObject(snapshot) || !isObject(snapshot.repository)) fail(receipt, "run snapshot is invalid");
  const id = canonicalId(snapshot.id);
  const attempt = canonicalId(snapshot.run_attempt);
  if (id === undefined || attempt === undefined
      || !isSafeText(snapshot.event)
      || !COMMIT_PATTERN.test(snapshot.head_sha ?? "")
      || !isSafeText(snapshot.status)
      || !isSafeText(snapshot.conclusion)
      || !isSafeText(snapshot.path)
      || !isSafeText(snapshot.repository.full_name)) {
    fail(receipt, "run snapshot is invalid");
  }
  return {
    id,
    runAttempt: attempt,
    event: snapshot.event,
    headSha: snapshot.head_sha,
    status: snapshot.status,
    conclusion: snapshot.conclusion,
    workflowPath: snapshot.path,
    repository: snapshot.repository.full_name,
  };
}

function normalizeJobSnapshot(receipt, snapshot) {
  if (!isObject(snapshot) || !isDenseArray(snapshot.jobs)) fail(receipt, "job snapshot is invalid");
  const count = canonicalCount(snapshot.total_count);
  if (count === undefined || count !== String(snapshot.jobs.length)) fail(receipt, "job snapshot is invalid");
  const ids = new Set();
  const names = new Set();
  const jobs = snapshot.jobs.map((job) => {
    if (!isObject(job)) fail(receipt, "job snapshot is invalid");
    const id = canonicalId(job.id);
    const runId = canonicalId(job.run_id);
    if (id === undefined || runId === undefined
        || !isSafeText(job.name)
        || !isSafeText(job.status)
        || !isSafeText(job.conclusion)
        || ids.has(id)
        || names.has(job.name)) {
      fail(receipt, "job snapshot is invalid");
    }
    ids.add(id);
    names.add(job.name);
    return { id, runId, name: job.name, status: job.status, conclusion: job.conclusion };
  });
  return jobs;
}

function normalizeArtifactSnapshot(receipt, snapshot) {
  if (!isObject(snapshot) || !isDenseArray(snapshot.artifacts)) fail(receipt, "artifact snapshot is invalid");
  const count = canonicalCount(snapshot.total_count);
  if (count === undefined || count !== String(snapshot.artifacts.length)) fail(receipt, "artifact snapshot is invalid");
  const ids = new Set();
  const names = new Set();
  return snapshot.artifacts.map((artifact) => {
    if (!isObject(artifact) || !isObject(artifact.workflow_run)) fail(receipt, "artifact snapshot is invalid");
    const id = canonicalId(artifact.id);
    const runId = canonicalId(artifact.workflow_run.id);
    const artifactDigest = normalizedDigest(artifact.digest);
    if (id === undefined || runId === undefined || artifactDigest === undefined
        || !isSafeText(artifact.name)
        || typeof artifact.expired !== "boolean"
        || ids.has(id)
        || names.has(artifact.name)) {
      fail(receipt, "artifact snapshot is invalid");
    }
    ids.add(id);
    names.add(artifact.name);
    return { id, name: artifact.name, digest: artifactDigest, expired: artifact.expired, runId };
  });
}

export function auditGithubReceipt({ receipt, runSnapshot, jobSnapshot, artifactSnapshot, reportArtifacts, gateId }) {
  assertReceipt(receipt);
  const expected = receipt.evidence;
  const run = normalizeRunSnapshot(receipt, runSnapshot);
  const jobs = normalizeJobSnapshot(receipt, jobSnapshot);
  const artifacts = normalizeArtifactSnapshot(receipt, artifactSnapshot)
    .filter((artifact) => !GENERATED_AUDIT_ARTIFACT_PATTERN.test(artifact.name));
  const expectedAttempt = String(expected.runAttempt);
  if (run.id !== expected.runId) fail(receipt, "run ID does not match");
  if (run.runAttempt !== expectedAttempt) fail(receipt, "run attempt does not match");
  if (run.repository !== EXPECTED_REPOSITORY || run.repository !== expected.repository) fail(receipt, "repository does not match");
  if (run.workflowPath !== expected.workflowPath) fail(receipt, "workflow path does not match");
  if (run.event !== expected.event) fail(receipt, "event does not match");
  if (run.headSha !== expected.headSha) fail(receipt, "head SHA does not match");
  if (run.status !== "completed") fail(receipt, "run status is not completed");
  if (run.conclusion !== "success" || run.conclusion !== expected.conclusion) fail(receipt, "run conclusion is not successful");

  const jobsByName = new Map(jobs.map((job) => [job.name, job]));
  for (const expectedJob of expected.jobs) {
    const job = jobsByName.get(expectedJob.name);
    if (job === undefined) fail(receipt, "required job is missing");
    if (job.runId !== expected.runId) fail(receipt, "job run ID does not match");
    if (job.status !== "completed") fail(receipt, "job status is not completed");
    if (job.conclusion !== expectedJob.conclusion) fail(receipt, "job conclusion does not match");
  }

  if (artifacts.length !== expected.artifacts.length) fail(receipt, "artifact set does not match");
  const artifactsById = new Map(artifacts.map((artifact) => [artifact.id, artifact]));
  let artifactAvailability = "available";
  for (const expectedArtifact of expected.artifacts) {
    const artifact = artifactsById.get(expectedArtifact.id);
    if (artifact === undefined
        || artifact.name !== expectedArtifact.name
        || artifact.digest !== expectedArtifact.digest
        || artifact.runId !== expected.runId) {
      fail(receipt, "artifact identity does not match");
    }
    if (artifact.expired) artifactAvailability = "expired";
  }
  if (gateId === "P7-WINDOWS-WFP-OFFLINE" && expected.event !== "push") {
    fail(receipt, "Windows WFP evidence is not from a push run");
  }
  if (P7_SEMANTIC_REPORT_GATES.has(gateId)) {
    const reports = expected.artifacts.filter((artifact) => artifact.report?.gateId === gateId);
    if (reports.length !== 1) fail(receipt, "required P7 semantic report is missing");
    try {
      validateP7Report(reports[0].report, {
        gateId,
        candidateCommit: receipt.candidateCommit,
        runAttempt: expected.runAttempt,
      });
    } catch {
      fail(receipt, "required P7 semantic report is invalid");
    }
  }
  if (P8_SEMANTIC_REPORT_GATES.has(gateId)) {
    const reports = expected.artifacts.filter((artifact) => artifact.report?.gateId === gateId);
    if (reports.length !== 1) fail(receipt, "required P8 semantic report is missing");
    const authenticated = reportArtifacts instanceof Map ? reportArtifacts.get(reports[0].id) : undefined;
    const rawReportArtifact = artifactsById.get(reports[0].id);
    if (authenticated?.archiveDigest !== rawReportArtifact?.digest
        || encodeCanonicalJson(authenticated?.report ?? {}) !== encodeCanonicalJson(reports[0].report)) {
      fail(receipt, "required P8 report bytes are untrusted");
    }
    try {
      validateP8Report(authenticated.report, {
        gateId,
        candidateCommit: receipt.candidateCommit,
        runId: expected.runId,
        runAttempt: expected.runAttempt,
        workflowPath: expected.workflowPath,
        artifacts: expected.artifacts,
        reportArtifactName: rawReportArtifact.name,
      });
    } catch {
      fail(receipt, "required P8 semantic report is invalid");
    }
  }
  return {
    receiptId: receipt.receiptId,
    status: "PASS",
    artifactAvailability,
    releaseUsable: artifactAvailability === "available",
  };
}

function snapshotsForRun(snapshotsByRunId, id) {
  if (snapshotsByRunId instanceof Map) return snapshotsByRunId.get(id);
  if (isObject(snapshotsByRunId) && Object.hasOwn(snapshotsByRunId, id)) return snapshotsByRunId[id];
  return undefined;
}

function downgradePass(row) {
  const output = { ...row, status: "FAILED" };
  if (Object.hasOwn(output, "artifactAvailability")) output.artifactAvailability = "missing";
  delete output.reason;
  return output;
}

export function evaluateAuditedMatrix({ recordedMatrix, receipts, snapshotsByRunId }) {
  validateMatrix(recordedMatrix);
  if (!isDenseArray(receipts)
      || (!isObject(snapshotsByRunId) && !(snapshotsByRunId instanceof Map))) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "audited matrix inputs are invalid");
  }
  const receiptsById = new Map();
  const duplicatedReceiptIds = new Set();
  for (const receipt of receipts) {
    const id = safeReceiptId(receipt);
    if (receiptsById.has(id)) duplicatedReceiptIds.add(id);
    receiptsById.set(id, receipt);
  }
  const gates = recordedMatrix.gates.map((gate) => {
    if (!isObject(gate) || !isSafeText(gate.id) || !["PASS", "MISSING", "FAILED", "DEFERRED"].includes(gate.status)) {
      throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "recorded matrix gate is invalid");
    }
    if (gate.status === "DEFERRED") {
      if (!ALLOWED_DEFERRED_GATE_IDS.has(gate.id)) {
        throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "recorded deferred gate is not allowed");
      }
      return { ...gate };
    }
    if (gate.status !== "PASS") return { ...gate };
    if (!RECEIPT_ID_PATTERN.test(gate.receiptId ?? "") || duplicatedReceiptIds.has(gate.receiptId)) return downgradePass(gate);
    const receipt = receiptsById.get(gate.receiptId);
    if (receipt === undefined || receipt?.evidence?.kind !== "github-actions") return downgradePass(gate);
    if (receipt.candidateCommit !== recordedMatrix.candidateCommit || !receipt.gateIds?.includes(gate.id)) return downgradePass(gate);
    const snapshots = snapshotsForRun(snapshotsByRunId, receipt.evidence.runId);
    if (!isObject(snapshots)) return downgradePass(gate);
    try {
      const result = auditGithubReceipt({ receipt, ...snapshots, gateId: gate.id });
      const output = { ...gate, artifactAvailability: result.artifactAvailability };
      return output;
    } catch (error) {
      if (error?.code !== "PHASE9_EVIDENCE_UNTRUSTED") throw error;
      return downgradePass(gate);
    }
  });
  const counts = { pass: 0, missing: 0, failed: 0, deferred: 0 };
  for (const gate of gates) counts[gate.status.toLowerCase()] += 1;
  const allArtifactsAvailable = gates.every((gate) => gate.artifactAvailability === undefined || gate.artifactAvailability === "available");
  const matrix = {
    ...recordedMatrix,
    releaseReady: recordedMatrix.catalogComplete === true
      && recordedMatrix.evaluationMode === "candidate"
      && gates.every(({ status }) => status === "PASS")
      && allArtifactsAvailable,
    counts,
    gates,
  };
  validateMatrix(matrix);
  return matrix;
}

async function readRawJson(path, { label, maxBytes }) {
  const chunks = [];
  let total = 0;
  const handle = await open(path, "r");
  try {
    while (total <= maxBytes) {
      const size = Math.min(65536, maxBytes + 1 - total);
      if (size <= 0) break;
      const chunk = Buffer.allocUnsafe(size);
      const { bytesRead } = await handle.read(chunk, 0, size, null);
      if (bytesRead === 0) break;
      chunks.push(chunk.subarray(0, bytesRead));
      total += bytesRead;
    }
  } finally {
    await handle.close();
  }
  if (total === 0 || total > maxBytes) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} byte length is invalid`);
  let source;
  try {
    source = new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks, total));
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not UTF-8`, error);
  }
  try {
    const value = JSON.parse(source);
    if (!isObject(value)) throw new TypeError("top level must be an object");
    return value;
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not JSON`, error);
  }
}

async function readBoundedBytes(path, { label, maxBytes }) {
  const chunks = [];
  let total = 0;
  const handle = await open(path, "r");
  try {
    while (total <= maxBytes) {
      const size = Math.min(65536, maxBytes + 1 - total);
      if (size <= 0) break;
      const chunk = Buffer.allocUnsafe(size);
      const { bytesRead } = await handle.read(chunk, 0, size, null);
      if (bytesRead === 0) break;
      chunks.push(chunk.subarray(0, bytesRead));
      total += bytesRead;
    }
  } finally {
    await handle.close();
  }
  if (total === 0 || total > maxBytes) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} byte length is invalid`);
  return Buffer.concat(chunks, total);
}

async function loadReportArtifacts(directory) {
  let entries;
  try {
    entries = await readdir(directory, { withFileTypes: true });
  } catch (error) {
    if (error?.code === "ENOENT") return new Map();
    throw error;
  }
  if (entries.length > 32 || entries.some((entry) => !entry.isFile() || !REPORT_ARCHIVE_NAME.test(entry.name))) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "P8 report archive files are invalid");
  }
  entries.sort((left, right) => {
    const leftId = BigInt(REPORT_ARCHIVE_NAME.exec(left.name)[1]);
    const rightId = BigInt(REPORT_ARCHIVE_NAME.exec(right.name)[1]);
    return leftId < rightId ? -1 : leftId > rightId ? 1 : 0;
  });
  const reports = new Map();
  for (const entry of entries) {
    const id = REPORT_ARCHIVE_NAME.exec(entry.name)[1];
    if (reports.has(id)) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "P8 report archive IDs are duplicated");
    const bytes = await readBoundedBytes(join(directory, entry.name), {
      label: "P8 report archive", maxBytes: MAX_REPORT_ARCHIVE_BYTES,
    });
    reports.set(id, parseP8ReportArchive(bytes));
  }
  return reports;
}

async function loadReceipts(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = entries.filter((entry) => entry.isFile() && entry.name.endsWith(".json"));
  if (files.length > MAX_RECEIPTS) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "receipt count is invalid");
  files.sort((left, right) => left.name.localeCompare(right.name, "en"));
  return Promise.all(files.map((entry) => readCanonicalJson(join(directory, entry.name), {
    label: "receipt", maxBytes: MAX_RECEIPT_BYTES,
  })));
}

async function loadSnapshots(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  if (entries.length > MAX_SNAPSHOT_DIRECTORIES
      || entries.some((entry) => !entry.isDirectory() || !DECIMAL_ID_PATTERN.test(entry.name))) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "snapshot directories are invalid");
  }
  entries.sort((left, right) => {
    const leftId = BigInt(left.name);
    const rightId = BigInt(right.name);
    return leftId < rightId ? -1 : leftId > rightId ? 1 : 0;
  });
  const snapshots = {};
  for (const entry of entries) {
    const root = join(directory, entry.name);
    const children = await readdir(root, { withFileTypes: true });
    const files = children.filter((child) => child.isFile());
    const reportDirectories = children.filter((child) => child.isDirectory() && child.name === "reports");
    const names = files.map(({ name }) => name).sort((left, right) => left.localeCompare(right, "en"));
    if (children.length !== files.length + reportDirectories.length
        || reportDirectories.length > 1
        || names.length !== SNAPSHOT_FILES.length
        || names.some((name, index) => name !== SNAPSHOT_FILES[index])) {
      throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "snapshot filenames are invalid");
    }
    const [runSnapshot, jobSnapshot, artifactSnapshot, reportArtifacts] = await Promise.all([
      readRawJson(join(root, "run.json"), { label: "run snapshot", maxBytes: MAX_RUN_BYTES }),
      readRawJson(join(root, "jobs.json"), { label: "job snapshot", maxBytes: MAX_JOBS_BYTES }),
      readRawJson(join(root, "artifacts.json"), { label: "artifact snapshot", maxBytes: MAX_ARTIFACTS_BYTES }),
      loadReportArtifacts(join(root, "reports")),
    ]);
    if (canonicalId(runSnapshot.id) !== entry.name) {
      throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "snapshot directory identity is invalid");
    }
    snapshots[entry.name] = { runSnapshot, jobSnapshot, artifactSnapshot, reportArtifacts };
  }
  return snapshots;
}

function parseArguments(args) {
  const required = ["recorded-matrix", "receipts", "snapshots", "json-out", "markdown-out"];
  const values = {};
  for (let index = 0; index < args.length;) {
    const flag = args[index++];
    const name = typeof flag === "string" && flag.startsWith("--") ? flag.slice(2) : "";
    const value = args[index++];
    if (!required.includes(name) || values[name] !== undefined
        || typeof value !== "string" || value.length === 0 || value.startsWith("--")) {
      throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "command line arguments are invalid");
    }
    values[name] = value;
  }
  if (required.some((name) => values[name] === undefined)) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "command line arguments are invalid");
  }
  return values;
}

async function main() {
  const arguments_ = parseArguments(process.argv.slice(2));
  const [recordedMatrix, receipts, snapshotsByRunId] = await Promise.all([
    readCanonicalJson(arguments_["recorded-matrix"], { label: "recorded matrix", maxBytes: MAX_MATRIX_BYTES }),
    loadReceipts(arguments_.receipts),
    loadSnapshots(arguments_.snapshots),
  ]);
  const matrix = evaluateAuditedMatrix({ recordedMatrix, receipts, snapshotsByRunId });
  await writeMatrixOutputs({
    matrix,
    jsonPath: arguments_["json-out"],
    markdownPath: arguments_["markdown-out"],
  });
  if (matrix.counts.failed > 0) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "audited evidence failed");
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    const code = typeof error?.code === "string" && error.code.startsWith("PHASE9_")
      ? error.code : "PHASE9_GATE_SCHEMA_INVALID";
    process.stderr.write(`${code}: audit failed\n`);
    process.exitCode = 1;
  });
}
