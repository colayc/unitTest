import { execFile } from "node:child_process";
import { readdir } from "node:fs/promises";
import { posix } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import {
  phase9Failure,
  readCanonicalJson,
  writeCanonicalJson,
} from "./canonical-json.mjs";
import schema from "./gates.schema.json" with { type: "json" };
import { validateP7Report, validateP7ReportDocument } from "./p7-report.mjs";

export const ALLOWED_DEFERRED_GATE_IDS = Object.freeze([
  "P8-DOCS-CLOSEOUT",
  "P8-LEGAL-THIRD-PARTY",
  "P8-SIGN-WINDOWS",
]);

export const EVIDENCE_ONLY_PATHS = Object.freeze([
  "docs/superpowers/evidence/phase9/",
]);

const MAX_REGISTRY_BYTES = 1024 * 1024;
const MAX_BASELINE_BYTES = 64 * 1024;
const MAX_RECEIPT_BYTES = 256 * 1024;
const MAX_RECEIPTS = 256;
const COMMIT_PATTERN = /^[0-9a-f]{40}$/u;
const UNSAFE_DISPLAY_PATTERN = /[\0\r\n`;<>&|]/u;
const RUN_ATTEMPT_ARTIFACT_SUFFIX = "-{runAttempt}";
const CANDIDATE_LINEAGE_REASON = "candidate-descendant-changed-tested-content";
const P7_SEMANTIC_REPORT_GATES = new Set([
  "P7-COVERAGE-UI-AND-SOURCE-DECORATION",
  "P7-HISTORY-AND-ARTIFACT-BROWSER",
  "P7-MAIN-USER-JOURNEY",
  "P7-MOCK-CONFIGURATION-UX",
]);
const execFileAsync = promisify(execFile);

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateRegistrySchema = ajv.compile(schema);
const validateBaselineSchema = ajv.getSchema(`${schema.$id}#/$defs/baseline`);
const validateReceiptSchema = ajv.getSchema(`${schema.$id}#/$defs/receipt`);
const validateMatrixSchema = ajv.getSchema(`${schema.$id}#/$defs/matrix`);

function schemaFailure(label) {
  throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is invalid`);
}

function assertSchema(validator, value, label) {
  if (typeof validator !== "function" || !validator(value)) schemaFailure(label);
}

function hasDuplicates(values) {
  return new Set(values).size !== values.length;
}

function isSorted(values) {
  return values.every((value, index) => index === 0 || values[index - 1].localeCompare(value, "en") <= 0);
}

function assertNonemptyDisplayString(value, label) {
  assertNonemptyExactString(value, label);
  if (UNSAFE_DISPLAY_PATTERN.test(value) || value.includes("$(")) {
    schemaFailure(label);
  }
}

function assertNonemptyExactString(value, label) {
  if (typeof value !== "string" || value.length === 0 || value.trim() !== value || /[\0\r\n]/u.test(value)) schemaFailure(label);
}

function isSafeRepositoryPath(value) {
  if (typeof value !== "string" || value.length === 0 || value.startsWith("/") || value.includes("\\") || value.includes(":")) return false;
  if (/[\p{Cc}]/u.test(value)) return false;
  const segments = value.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) return false;
  return posix.normalize(value) === value;
}

function assertSafeRepositoryPath(value, label) {
  if (!isSafeRepositoryPath(value)) schemaFailure(label);
}

function assertUniqueSortedDisplayStrings(values, label, { command = false, requireOne = false } = {}) {
  if ((requireOne && values.length === 0) || hasDuplicates(values) || !isSorted(values)) schemaFailure(label);
  for (const value of values) {
    assertNonemptyDisplayString(value, label);
    if (value.includes("\\") || /(?:^|[^.\p{L}\p{N}_-])(?:\/|[A-Za-z]:\/)/u.test(value)
        || /(?:^|[^.\p{L}\p{N}_-])\.\.(?![.\p{L}\p{N}_-])/u.test(value) || value.includes("//")) {
      schemaFailure(label);
    }
    if (!command && value.includes("/") && !isSafeRepositoryPath(value)) schemaFailure(label);
  }
}

export function validateRegistry(value) {
  assertSchema(validateRegistrySchema, value, "registry");
  if (value.allowedDeferredGateIds.length !== ALLOWED_DEFERRED_GATE_IDS.length
      || value.allowedDeferredGateIds.some((id, index) => id !== ALLOWED_DEFERRED_GATE_IDS[index])) {
    throw phase9Failure("PHASE9_DEFERRED_NOT_ALLOWED", "deferred gate boundary is invalid");
  }
  assertNonemptyExactString(value.product, "registry product");
  assertNonemptyExactString(value.repository, "registry repository");

  const sourcePaths = value.sources.map(({ path }) => path);
  if (hasDuplicates(sourcePaths) || !isSorted(sourcePaths)) schemaFailure("registry sources");
  const declaredSections = new Set();
  for (const source of value.sources) {
    assertSafeRepositoryPath(source.path, "source path");
    if (hasDuplicates(source.sections) || !isSorted(source.sections)) schemaFailure("source sections");
    for (const section of source.sections) {
      assertNonemptyExactString(section, "source section");
      declaredSections.add(`${source.path}\0${section}`);
    }
  }

  const gateIds = value.gates.map(({ id }) => id);
  if (hasDuplicates(gateIds) || !isSorted(gateIds)) schemaFailure("registry gates");
  const referencedSections = new Set();
  const allowedDeferred = new Set(ALLOWED_DEFERRED_GATE_IDS);
  for (const gate of value.gates) {
    assertNonemptyExactString(gate.category, "gate category");
    assertNonemptyExactString(gate.title, "gate title");
    const shouldBeDeferred = allowedDeferred.has(gate.id);
    if (gate.disposition === "deferred" && !shouldBeDeferred) {
      throw phase9Failure("PHASE9_DEFERRED_NOT_ALLOWED", `gate ${gate.id} may not be deferred`);
    }
    if (shouldBeDeferred && gate.disposition !== "deferred") {
      throw phase9Failure("PHASE9_DEFERRED_NOT_ALLOWED", `gate ${gate.id} must be deferred`);
    }
    if (!shouldBeDeferred && gate.disposition !== "required") {
      throw phase9Failure("PHASE9_DEFERRED_NOT_ALLOWED", `gate ${gate.id} has invalid disposition`);
    }
    if (gate.disposition === "deferred") {
      if (gate.deferment === undefined) schemaFailure(`gate ${gate.id} deferment`);
      assertNonemptyExactString(gate.deferment.reason, `gate ${gate.id} deferment reason`);
      assertNonemptyExactString(gate.deferment.resumeCondition, `gate ${gate.id} resume condition`);
    } else if (gate.deferment !== undefined) {
      throw phase9Failure("PHASE9_DEFERRED_NOT_ALLOWED", `gate ${gate.id} may not contain deferment`);
    }

    const references = gate.requirementRefs.map(({ source, section }) => `${source}\0${section}`);
    if (hasDuplicates(references) || !isSorted(references)) schemaFailure(`gate ${gate.id} requirement references`);
    for (const reference of gate.requirementRefs) {
      assertSafeRepositoryPath(reference.source, "requirement source");
      assertNonemptyExactString(reference.section, "requirement section");
      const key = `${reference.source}\0${reference.section}`;
      if (!declaredSections.has(key)) {
        throw phase9Failure("PHASE9_GATE_MISSING", `gate ${gate.id} references an unknown source section`);
      }
      referencedSections.add(key);
    }

    const { commands, workflowPath, jobs, artifacts } = gate.verification;
    assertUniqueSortedDisplayStrings(commands, `gate ${gate.id} commands`, { command: true });
    assertSafeRepositoryPath(workflowPath, `gate ${gate.id} workflow path`);
    assertUniqueSortedDisplayStrings(jobs, `gate ${gate.id} jobs`);
    assertUniqueSortedDisplayStrings(artifacts, `gate ${gate.id} artifacts`);
    for (const name of artifacts) {
      const firstToken = name.indexOf("{runAttempt}");
      if (firstToken !== -1
          && (firstToken !== name.length - "{runAttempt}".length
            || !name.endsWith(RUN_ATTEMPT_ARTIFACT_SUFFIX))) {
        schemaFailure(`gate ${gate.id} artifacts`);
      }
    }
    if (commands.length + jobs.length + artifacts.length === 0) schemaFailure(`gate ${gate.id} verification policy`);
  }
  for (const sourceSection of declaredSections) {
    if (!referencedSections.has(sourceSection)) {
      throw phase9Failure("PHASE9_GATE_MISSING", "source section is not referenced by a gate");
    }
  }
  return true;
}

export function validateBaseline(value) {
  assertSchema(validateBaselineSchema, value, "baseline");
  if (hasDuplicates(value.receiptIds)) throw phase9Failure("PHASE9_EVIDENCE_CONFLICT", "baseline receipt IDs are duplicated");
  for (const receiptId of value.receiptIds) assertNonemptyDisplayString(receiptId, "baseline receipt ID");
  return true;
}

export function validateReceipt(value) {
  assertSchema(validateReceiptSchema, value, "receipt");
  assertNonemptyDisplayString(value.receiptId, "receipt ID");
  if (hasDuplicates(value.gateIds)) throw phase9Failure("PHASE9_EVIDENCE_CONFLICT", `receipt ${value.receiptId} has duplicate gate IDs`);
  for (const gateId of value.gateIds) assertNonemptyExactString(gateId, "receipt gate ID");
  if (value.evidence.kind === "github-actions") {
    if (value.evidence.headSha !== value.candidateCommit) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", `receipt ${value.receiptId} candidate does not match evidence`);
    }
    assertNonemptyExactString(value.evidence.repository, "receipt repository");
    assertSafeRepositoryPath(value.evidence.workflowPath, "receipt workflow path");
    assertNonemptyExactString(value.evidence.event, "receipt event");
    const jobNames = value.evidence.jobs.map(({ name }) => name);
    const artifactNames = value.evidence.artifacts.map(({ name }) => name);
    if (hasDuplicates(jobNames) || hasDuplicates(artifactNames)) {
      throw phase9Failure("PHASE9_EVIDENCE_CONFLICT", `receipt ${value.receiptId} has duplicate evidence names`);
    }
    for (const name of [...jobNames, ...artifactNames]) assertNonemptyDisplayString(name, "receipt evidence name");
    if (value.evidence.artifacts.some(({ expired }) => expired)) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", `receipt ${value.receiptId} contains expired captured evidence`);
    }
    for (const artifact of value.evidence.artifacts) {
      if (artifact.report !== undefined) validateP7ReportDocument(artifact.report);
    }
  } else {
    assertSafeRepositoryPath(value.evidence.approvalPath, "approval path");
    assertNonemptyExactString(value.evidence.role, "approval role");
  }
  return true;
}

export function validateMatrix(value) {
  assertSchema(validateMatrixSchema, value, "matrix");
  const gateIds = value.gates.map(({ id }) => id);
  if (hasDuplicates(gateIds)) schemaFailure("matrix gates");
  return true;
}

function normalizeChangedPath(value) {
  if (!isSafeRepositoryPath(value)) throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate change path is unsafe");
  return value;
}

function testedContentLineageFailure() {
  const error = phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate changes include tested content");
  error.candidateLineageReason = CANDIDATE_LINEAGE_REASON;
  return error;
}

export function validateCandidateChanges({ candidateCommit, currentCommit, changedPaths }) {
  if (!COMMIT_PATTERN.test(candidateCommit) || !COMMIT_PATTERN.test(currentCommit) || !Array.isArray(changedPaths)) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate lineage input is invalid");
  }
  for (let index = 0; index < changedPaths.length; index += 1) {
    if (!Object.hasOwn(changedPaths, index)) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate changed path set is sparse");
    }
  }
  const normalizedPaths = changedPaths.map(normalizeChangedPath);
  if (candidateCommit === currentCommit) {
    if (normalizedPaths.length === 0) return "exact";
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "exact candidate contains changed paths");
  }
  if (normalizedPaths.length === 0) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate changed path set is empty");
  }
  if (normalizedPaths.every((path) => EVIDENCE_ONLY_PATHS.some((prefix) => path.startsWith(prefix)))) {
    return "evidence-only-descendant";
  }
  throw testedContentLineageFailure();
}

async function execFileText(command, arguments_) {
  const { stdout } = await execFileAsync(command, arguments_, { encoding: "utf8", windowsHide: true });
  return stdout;
}

async function repositoryState(repositoryRoot, candidateCommit) {
  if (!COMMIT_PATTERN.test(candidateCommit)) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate commit is invalid");
  }
  const currentOutput = await execFileText("git", ["-C", repositoryRoot, "rev-parse", "HEAD"]);
  const currentCommit = currentOutput.trim();
  if (!COMMIT_PATTERN.test(currentCommit) || !/^([0-9a-f]{40})\r?\n?$/u.test(currentOutput)) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "current commit output is invalid");
  }
  try {
    await execFileText("git", [
      "-C", repositoryRoot, "merge-base", "--is-ancestor", candidateCommit, currentCommit,
    ]);
  } catch (error) {
    if (error?.code === 1) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate is not an ancestor", error);
    }
    if (Number.isInteger(error?.code)) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate ancestry Git execution failed", error);
    }
    throw error;
  }
  const changedOutput = await execFileText("git", [
    "-C", repositoryRoot,
    "diff", "--name-only", "--diff-filter=ACDMRTUXB",
    `${candidateCommit}..${currentCommit}`,
  ]);
  const changedPaths = changedOutput.split(/\r?\n/u).filter((path) => path.length > 0).map(normalizeChangedPath);
  return { changedPaths, currentCommit };
}

function evidenceSatisfiesVerification(repository, gate, receipt) {
  const { verification } = gate;
  if (receipt.evidence.kind === "manual-approval") {
    return receipt.evidence.decision === "approved" && verification.jobs.length === 0 && verification.artifacts.length === 0;
  }
  if (receipt.evidence.repository !== repository) return false;
  if (receipt.evidence.workflowPath !== verification.workflowPath) return false;
  const jobs = new Map(receipt.evidence.jobs.map((job) => [job.name, job]));
  const artifacts = new Map(receipt.evidence.artifacts.map((artifact) => [artifact.name, artifact]));
  if (!verification.jobs.every((name) => jobs.get(name)?.conclusion === "success")) return false;
  if (!verification.artifacts.every((name) => artifacts.get(artifactNameForRunAttempt(name, receipt.evidence.runAttempt))?.expired === false)) return false;
  if (gate.id === "P7-WINDOWS-WFP-OFFLINE" && receipt.evidence.event !== "push") return false;
  if (P7_SEMANTIC_REPORT_GATES.has(gate.id)) {
    if (verification.artifacts.length !== 1) return false;
    const artifact = artifacts.get(artifactNameForRunAttempt(verification.artifacts[0], receipt.evidence.runAttempt));
    if (artifact?.report === undefined) return false;
    try {
      validateP7Report(artifact.report, {
        gateId: gate.id,
        candidateCommit: receipt.candidateCommit,
        runAttempt: receipt.evidence.runAttempt,
      });
    } catch (error) {
      if (error?.code !== "PHASE9_P7_REPORT_INVALID") throw error;
      return false;
    }
  }
  return true;
}

function artifactNameForRunAttempt(name, runAttempt) {
  return name.endsWith(RUN_ATTEMPT_ARTIFACT_SUFFIX)
    ? `${name.slice(0, -RUN_ATTEMPT_ARTIFACT_SUFFIX.length)}-${runAttempt}`
    : name;
}

function recordedStatus(repository, gate, receipt) {
  if (gate.disposition === "deferred") return "DEFERRED";
  if (receipt === undefined) return "MISSING";
  if (receipt.evidence.kind === "github-actions" && receipt.evidence.conclusion !== "success") return "FAILED";
  if (!evidenceSatisfiesVerification(repository, gate, receipt)) return "MISSING";
  return "PASS";
}

export function evaluateRecordedMatrix({ registry, baseline, receipts, currentCommit, changedPaths }) {
  validateRegistry(registry);
  validateBaseline(baseline);
  if (!COMMIT_PATTERN.test(currentCommit) || !Array.isArray(changedPaths)) schemaFailure("matrix candidate state");
  const receiptsById = new Map();
  for (const receipt of receipts) {
    validateReceipt(receipt);
    if (receiptsById.has(receipt.receiptId)) throw phase9Failure("PHASE9_EVIDENCE_CONFLICT", "receipt IDs are duplicated");
    receiptsById.set(receipt.receiptId, receipt);
  }
  const selectedReceipts = baseline.receiptIds.map((receiptId) => {
    const receipt = receiptsById.get(receiptId);
    if (receipt === undefined) throw phase9Failure("PHASE9_GATE_MISSING", `selected receipt ${receiptId} is missing`);
    if (receipt.candidateCommit !== baseline.candidateCommit) {
      throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", `receipt ${receipt.receiptId} is bound to another candidate`);
    }
    return receipt;
  });
  const gateIds = new Set(registry.gates.map(({ id }) => id));
  const receiptForGate = new Map();
  for (const receipt of selectedReceipts) {
    for (const gateId of receipt.gateIds) {
      if (!gateIds.has(gateId)) throw phase9Failure("PHASE9_GATE_MISSING", `receipt ${receipt.receiptId} names an unknown gate`);
      if (receiptForGate.has(gateId)) throw phase9Failure("PHASE9_EVIDENCE_CONFLICT", `gate ${gateId} has conflicting receipts`);
      receiptForGate.set(gateId, receipt);
    }
  }
  let candidateLineageInvalid = false;
  if (baseline.evaluationMode === "candidate") {
    try {
      validateCandidateChanges({ candidateCommit: baseline.candidateCommit, currentCommit, changedPaths });
    } catch (error) {
      if (error?.code !== "PHASE9_EVIDENCE_UNTRUSTED"
          || error.candidateLineageReason !== CANDIDATE_LINEAGE_REASON) throw error;
      candidateLineageInvalid = true;
    }
  }

  const gates = [...registry.gates]
    .sort((left, right) => left.id.localeCompare(right.id, "en"))
    .map((gate) => {
      const receipt = receiptForGate.get(gate.id);
      let status = recordedStatus(registry.repository, gate, receipt);
      const row = { id: gate.id, status };
      if (candidateLineageInvalid && status === "PASS") {
        status = "FAILED";
        row.status = status;
        row.reason = CANDIDATE_LINEAGE_REASON;
      }
      if (receipt !== undefined && gate.disposition !== "deferred") row.receiptId = receipt.receiptId;
      if (gate.verification.artifacts.length > 0 && gate.disposition !== "deferred") {
        row.artifactAvailability = receipt?.evidence.kind === "github-actions"
          && gate.verification.artifacts.every((name) => receipt.evidence.artifacts.some(
            (item) => item.name === artifactNameForRunAttempt(name, receipt.evidence.runAttempt) && !item.expired,
          ))
          ? "available" : "missing";
      }
      return row;
    });
  const counts = { pass: 0, missing: 0, failed: 0, deferred: 0 };
  for (const gate of gates) counts[gate.status.toLowerCase()] += 1;
  return {
    schemaVersion: 1,
    catalogComplete: true,
    releaseReady: baseline.evaluationMode === "candidate" && gates.every(({ status }) => status === "PASS"),
    evaluationMode: baseline.evaluationMode,
    candidateCommit: baseline.candidateCommit,
    currentCommit,
    counts,
    gates,
  };
}

export async function loadPhase9Inputs({ registryPath, baselinePath, receiptsDirectory }) {
  const [registry, baseline, entries] = await Promise.all([
    readCanonicalJson(registryPath, { label: "registry", maxBytes: MAX_REGISTRY_BYTES }),
    readCanonicalJson(baselinePath, { label: "baseline", maxBytes: MAX_BASELINE_BYTES }),
    readdir(receiptsDirectory, { withFileTypes: true }),
  ]);
  const receiptEntries = entries.filter((entry) => entry.isFile() && entry.name.endsWith(".json"));
  if (receiptEntries.length > MAX_RECEIPTS) schemaFailure("receipt count");
  receiptEntries.sort((left, right) => left.name.localeCompare(right.name, "en"));
  const receipts = await Promise.all(receiptEntries.map((entry) => readCanonicalJson(
    `${receiptsDirectory}/${entry.name}`,
    { label: "receipt", maxBytes: MAX_RECEIPT_BYTES },
  )));
  validateRegistry(registry);
  validateBaseline(baseline);
  for (const receipt of receipts) validateReceipt(receipt);
  return { registry, baseline, receipts };
}

const CLI_FLAGS = Object.freeze([
  "--registry", "--baseline", "--receipts", "--repository-root", "--out", "--requests-out",
]);

function parseArguments(arguments_) {
  const values = new Map();
  for (let index = 0; index < arguments_.length; index += 2) {
    const flag = arguments_[index];
    const value = arguments_[index + 1];
    if (!CLI_FLAGS.includes(flag) || values.has(flag) || value === undefined || value.length === 0 || value.startsWith("--")) {
      schemaFailure("command line arguments");
    }
    values.set(flag, value);
  }
  if (values.size !== CLI_FLAGS.length) schemaFailure("command line arguments");
  return Object.fromEntries(CLI_FLAGS.map((flag) => [flag.slice(2), values.get(flag)]));
}

async function main() {
  const arguments_ = parseArguments(process.argv.slice(2));
  const { registry, baseline, receipts } = await loadPhase9Inputs({
    registryPath: arguments_.registry,
    baselinePath: arguments_.baseline,
    receiptsDirectory: arguments_.receipts,
  });
  const state = await repositoryState(arguments_["repository-root"], baseline.candidateCommit);
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline,
    receipts,
    currentCommit: state.currentCommit,
    changedPaths: state.changedPaths,
  });
  const selected = new Set(baseline.receiptIds);
  const runIds = [...new Set(receipts
    .filter((receipt) => selected.has(receipt.receiptId) && receipt.evidence.kind === "github-actions")
    .map((receipt) => receipt.evidence.runId))]
    .sort((left, right) => (BigInt(left) < BigInt(right) ? -1 : BigInt(left) > BigInt(right) ? 1 : 0));
  await writeCanonicalJson(arguments_.out, matrix);
  await writeCanonicalJson(arguments_["requests-out"], { schemaVersion: 1, runIds });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    const code = typeof error?.code === "string" && error.code.startsWith("PHASE9_")
      ? error.code : "PHASE9_GATE_SCHEMA_INVALID";
    process.stderr.write(`${code}: validation failed\n`);
    process.exitCode = 1;
  });
}
