import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import { writeCanonicalJson } from "./canonical-json.mjs";
import { auditGithubReceipt, evaluateAuditedMatrix } from "./audit.mjs";

const execFileAsync = promisify(execFile);
const candidateCommit = "a".repeat(40);
const digest = "b".repeat(64);
const runId = "34731651809";
const artifactId = "10310420278";
const receiptId = `github-actions-${runId}-1`;
const fixtureRoots = [];

test.afterEach(async () => {
  await Promise.all(fixtureRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

function validReceipt(overrides = {}) {
  const receipt = {
    schemaVersion: 1,
    receiptId,
    candidateCommit,
    observedAt: "2026-09-13T12:13:32.860Z",
    gateIds: ["P9-MATRIX-UNIT"],
    evidence: {
      kind: "github-actions",
      repository: "colayc/unitTest",
      workflowPath: ".github/workflows/foundation.yml",
      runId,
      runAttempt: 1,
      event: "workflow_dispatch",
      headSha: candidateCommit,
      conclusion: "success",
      jobs: [{ name: "verify-linux", conclusion: "success" }],
      artifacts: [{ id: artifactId, name: "native-toolchain-linux-1", digest, expired: false }],
    },
  };
  return { ...receipt, ...overrides, evidence: { ...receipt.evidence, ...overrides.evidence } };
}

function validRunSnapshot(overrides = {}) {
  return {
    id: 34731651809,
    run_attempt: 1,
    event: "workflow_dispatch",
    head_sha: candidateCommit,
    status: "completed",
    conclusion: "success",
    path: ".github/workflows/foundation.yml",
    repository: { full_name: "colayc/unitTest" },
    ...overrides,
  };
}

function validJobSnapshot(overrides = {}) {
  return {
    total_count: 1,
    jobs: [{
      id: 9001,
      run_id: 34731651809,
      name: "verify-linux",
      status: "completed",
      conclusion: "success",
    }],
    ...overrides,
  };
}

function validArtifactSnapshot({ expired = false, ...overrides } = {}) {
  return {
    total_count: 1,
    artifacts: [{
      id: 10310420278,
      name: "native-toolchain-linux-1",
      digest: `sha256:${digest}`,
      expired,
      workflow_run: { id: 34731651809 },
    }],
    ...overrides,
  };
}

function auditInputs(overrides = {}) {
  return {
    receipt: validReceipt(),
    runSnapshot: validRunSnapshot(),
    jobSnapshot: validJobSnapshot(),
    artifactSnapshot: validArtifactSnapshot(),
    ...overrides,
  };
}

function assertUntrusted(run, expectedReceiptId = receiptId) {
  assert.throws(run, (error) => {
    assert.equal(error?.code, "PHASE9_EVIDENCE_UNTRUSTED");
    assert.match(error.message, new RegExp(`^PHASE9_EVIDENCE_UNTRUSTED: ${expectedReceiptId}: [A-Za-z0-9 -]+$`, "u"));
    return true;
  });
}

test("audits a complete successful GitHub Actions receipt", () => {
  assert.deepEqual(auditGithubReceipt(auditInputs()), {
    receiptId,
    status: "PASS",
    artifactAvailability: "available",
    releaseUsable: true,
  });
});

const mismatchCases = [
  ["repository", ({ runSnapshot }) => { runSnapshot.repository.full_name = "attacker/unitTest"; }],
  ["workflow path", ({ runSnapshot }) => { runSnapshot.path = ".github/workflows/other.yml"; }],
  ["run ID", ({ runSnapshot }) => { runSnapshot.id = 34731651810; }],
  ["run attempt", ({ runSnapshot }) => { runSnapshot.run_attempt = 2; }],
  ["event", ({ runSnapshot }) => { runSnapshot.event = "push"; }],
  ["head SHA", ({ runSnapshot }) => { runSnapshot.head_sha = "c".repeat(40); }],
  ["run status", ({ runSnapshot }) => { runSnapshot.status = "in_progress"; }],
  ["run conclusion", ({ runSnapshot }) => { runSnapshot.conclusion = "failure"; }],
  ["missing job", ({ jobSnapshot }) => { jobSnapshot.total_count = 0; jobSnapshot.jobs = []; }],
  ["duplicate job", ({ jobSnapshot }) => {
    jobSnapshot.total_count = 2;
    jobSnapshot.jobs.push({ ...jobSnapshot.jobs[0], id: 9002 });
  }],
  ["job run ID", ({ jobSnapshot }) => { jobSnapshot.jobs[0].run_id = 34731651810; }],
  ["job status", ({ jobSnapshot }) => { jobSnapshot.jobs[0].status = "in_progress"; }],
  ["job conclusion", ({ jobSnapshot }) => { jobSnapshot.jobs[0].conclusion = "failure"; }],
  ["artifact ID", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].id = 10310420279; }],
  ["artifact name", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].name = "renamed"; }],
  ["artifact digest", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].digest = `sha256:${"c".repeat(64)}`; }],
  ["artifact run ID", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].workflow_run.id = 34731651810; }],
  ["uppercase receipt digest", ({ receipt }) => { receipt.evidence.artifacts[0].digest = digest.toUpperCase(); }],
  ["uppercase raw digest", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].digest = `sha256:${digest.toUpperCase()}`; }],
  ["missing artifact", ({ artifactSnapshot }) => { artifactSnapshot.total_count = 0; artifactSnapshot.artifacts = []; }],
  ["duplicate artifact", ({ artifactSnapshot }) => {
    artifactSnapshot.total_count = 2;
    artifactSnapshot.artifacts.push({ ...artifactSnapshot.artifacts[0] });
  }],
  ["unexpected artifact", ({ artifactSnapshot }) => {
    artifactSnapshot.total_count = 2;
    artifactSnapshot.artifacts.push({
      id: 10310420279,
      name: "unexpected",
      digest: `sha256:${"c".repeat(64)}`,
      expired: false,
      workflow_run: { id: 34731651809 },
    });
  }],
];

for (const [name, mutate] of mismatchCases) {
  test(`rejects ${name} drift`, () => {
    const input = structuredClone(auditInputs());
    mutate(input);
    assertUntrusted(() => auditGithubReceipt(input));
  });
}

const malformedCases = [
  ["fractional run ID", ({ runSnapshot }) => { runSnapshot.id = 1.5; }],
  ["unsafe numeric run ID", ({ runSnapshot }) => { runSnapshot.id = Number.MAX_SAFE_INTEGER + 1; }],
  ["noncanonical string run ID", ({ runSnapshot }) => { runSnapshot.id = "034731651809"; }],
  ["fractional job ID", ({ jobSnapshot }) => { jobSnapshot.jobs[0].id = 9.5; }],
  ["incorrect job count", ({ jobSnapshot }) => { jobSnapshot.total_count = 2; }],
  ["non-array artifacts", ({ artifactSnapshot }) => { artifactSnapshot.artifacts = {}; }],
  ["unsupported digest prefix", ({ artifactSnapshot }) => { artifactSnapshot.artifacts[0].digest = `md5:${digest}`; }],
  ["missing expired metadata", ({ artifactSnapshot }) => { delete artifactSnapshot.artifacts[0].expired; }],
  ["sparse jobs array", ({ jobSnapshot }) => { jobSnapshot.jobs = Array(1); }],
];

for (const [name, mutate] of malformedCases) {
  test(`rejects malformed raw ${name}`, () => {
    const input = structuredClone(auditInputs());
    mutate(input);
    assertUntrusted(() => auditGithubReceipt(input));
  });
}

test("normalizes canonical decimal string IDs without accepting numeric ambiguity", () => {
  const input = auditInputs();
  input.runSnapshot.id = runId;
  input.jobSnapshot.jobs[0].id = "9001";
  input.jobSnapshot.jobs[0].run_id = runId;
  input.artifactSnapshot.artifacts[0].id = artifactId;
  input.artifactSnapshot.artifacts[0].workflow_run.id = runId;
  assert.equal(auditGithubReceipt(input).status, "PASS");
});

test("ignores irrelevant provider fields even when they contain conflicting coordinates", () => {
  const input = auditInputs();
  Object.assign(input.runSnapshot, { run_id: 1, workflow_path: "attacker.yml", owner: "attacker" });
  Object.assign(input.runSnapshot.repository, { name: "attacker", fullName: "attacker/unitTest" });
  Object.assign(input.jobSnapshot.jobs[0], { repository: "attacker/unitTest", workflow_path: "attacker.yml" });
  Object.assign(input.artifactSnapshot.artifacts[0], { run_id: 1, workflow_path: "attacker.yml" });
  Object.assign(input.artifactSnapshot.artifacts[0].workflow_run, { repository_id: 1 });
  assert.equal(auditGithubReceipt(input).status, "PASS");
});

test("expired artifact remains historical only when exact metadata still exists", () => {
  const result = auditGithubReceipt({
    receipt: validReceipt(),
    runSnapshot: validRunSnapshot(),
    jobSnapshot: validJobSnapshot(),
    artifactSnapshot: validArtifactSnapshot({ expired: true }),
  });
  assert.equal(result.status, "PASS");
  assert.equal(result.artifactAvailability, "expired");
  assert.equal(result.releaseUsable, false);
});

test("missing expired artifact identity is untrusted instead of historical PASS", () => {
  assertUntrusted(() => auditGithubReceipt({
    ...auditInputs(),
    artifactSnapshot: { total_count: 0, artifacts: [] },
  }));
});

test("rejects malformed canonical receipt metadata and conclusions", () => {
  const badTimestamp = auditInputs();
  badTimestamp.receipt.observedAt = "not-a-timestamp";
  assertUntrusted(() => auditGithubReceipt(badTimestamp));

  const badConclusion = auditInputs();
  badConclusion.receipt.evidence.jobs[0].conclusion = "invented";
  badConclusion.jobSnapshot.jobs[0].conclusion = "invented";
  assertUntrusted(() => auditGithubReceipt(badConclusion));

  const unsafeWorkflow = auditInputs();
  unsafeWorkflow.receipt.evidence.workflowPath = "../foundation.yml";
  unsafeWorkflow.runSnapshot.path = "../foundation.yml";
  assertUntrusted(() => auditGithubReceipt(unsafeWorkflow));
});

function recordedMatrix({ evaluationMode = "candidate", gates } = {}) {
  const rows = gates ?? [{
    id: "P9-MATRIX-UNIT",
    status: "PASS",
    receiptId,
    artifactAvailability: "available",
  }];
  const counts = { pass: 0, missing: 0, failed: 0, deferred: 0 };
  for (const row of rows) counts[row.status.toLowerCase()] += 1;
  return {
    schemaVersion: 1,
    catalogComplete: true,
    releaseReady: evaluationMode === "candidate" && rows.every(({ status }) => status === "PASS"),
    evaluationMode,
    candidateCommit,
    currentCommit: candidateCommit,
    recordedByCommit: candidateCommit,
    counts,
    gates: rows,
  };
}

function snapshots(expired = false) {
  return {
    [runId]: {
      runSnapshot: validRunSnapshot(),
      jobSnapshot: validJobSnapshot(),
      artifactSnapshot: validArtifactSnapshot({ expired }),
    },
  };
}

test("downgrades a recorded PASS to FAILED when its receipt cannot be audited", () => {
  const broken = snapshots();
  broken[runId].runSnapshot.head_sha = "c".repeat(40);
  const matrix = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix(), receipts: [validReceipt()], snapshotsByRunId: broken,
  });
  assert.equal(matrix.releaseReady, false);
  assert.deepEqual(matrix.counts, { pass: 0, missing: 0, failed: 1, deferred: 0 });
  assert.deepEqual(matrix.gates[0], {
    id: "P9-MATRIX-UNIT",
    status: "FAILED",
    receiptId,
    artifactAvailability: "missing",
  });
});

test("preserves MISSING and allowed DEFERRED rows while recomputing counts", () => {
  const rows = [
    { id: "P9-MATRIX-UNIT", status: "PASS", receiptId, artifactAvailability: "available" },
    { id: "P9-MATRIX-E2E", status: "MISSING", artifactAvailability: "missing" },
    { id: "P8-SIGN-WINDOWS", status: "DEFERRED" },
  ];
  const input = recordedMatrix({ gates: rows });
  input.counts = { pass: 99, missing: 99, failed: 99, deferred: 99 };
  const matrix = evaluateAuditedMatrix({
    recordedMatrix: input, receipts: [validReceipt()], snapshotsByRunId: snapshots(),
  });
  assert.deepEqual(matrix.gates, rows);
  assert.deepEqual(matrix.counts, { pass: 1, missing: 1, failed: 0, deferred: 1 });
  assert.equal(matrix.releaseReady, false);
});

test("candidate release readiness requires every PASS artifact to remain available", () => {
  const available = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix(), receipts: [validReceipt()], snapshotsByRunId: snapshots(),
  });
  assert.equal(available.releaseReady, true);
  const expired = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix(), receipts: [validReceipt()], snapshotsByRunId: snapshots(true),
  });
  assert.equal(expired.gates[0].status, "PASS");
  assert.equal(expired.gates[0].artifactAvailability, "expired");
  assert.equal(expired.releaseReady, false);
  const historical = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix({ evaluationMode: "historical" }),
    receipts: [validReceipt()],
    snapshotsByRunId: snapshots(),
  });
  assert.equal(historical.releaseReady, false);
});

test("a recorded PASS remains bound to its candidate commit and gate ID", () => {
  const otherCandidate = validReceipt({ candidateCommit: "c".repeat(40), evidence: { headSha: "c".repeat(40) } });
  const otherSnapshots = snapshots();
  otherSnapshots[runId].runSnapshot.head_sha = "c".repeat(40);
  const candidateMismatch = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix(), receipts: [otherCandidate], snapshotsByRunId: otherSnapshots,
  });
  assert.equal(candidateMismatch.gates[0].status, "FAILED");

  const unrelatedReceipt = validReceipt({ gateIds: ["P9-MATRIX-E2E"] });
  const gateMismatch = evaluateAuditedMatrix({
    recordedMatrix: recordedMatrix(), receipts: [unrelatedReceipt], snapshotsByRunId: snapshots(),
  });
  assert.equal(gateMismatch.gates[0].status, "FAILED");
});

test("CLI loads only local fixed snapshots and writes deterministic renderer output", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-audit-"));
  fixtureRoots.push(root);
  const receiptsDirectory = join(root, "receipts");
  const snapshotsDirectory = join(root, "snapshots");
  const runDirectory = join(snapshotsDirectory, runId);
  await mkdir(receiptsDirectory, { recursive: true });
  await mkdir(runDirectory, { recursive: true });
  const matrixPath = join(root, "recorded.json");
  const jsonOut = join(root, "audited.json");
  const markdownOut = join(root, "audited.md");
  await writeCanonicalJson(matrixPath, recordedMatrix());
  await writeCanonicalJson(join(receiptsDirectory, `${receiptId}.json`), validReceipt());
  await writeFile(join(runDirectory, "run.json"), `${JSON.stringify(validRunSnapshot())}\n`);
  await writeFile(join(runDirectory, "jobs.json"), `${JSON.stringify(validJobSnapshot())}\n`);
  await writeFile(join(runDirectory, "artifacts.json"), `${JSON.stringify(validArtifactSnapshot())}\n`);
  await execFileAsync(process.execPath, [
    join(import.meta.dirname, "audit.mjs"),
    "--recorded-matrix", matrixPath,
    "--receipts", receiptsDirectory,
    "--snapshots", snapshotsDirectory,
    "--json-out", jsonOut,
    "--markdown-out", markdownOut,
  ]);
  const output = JSON.parse(await readFile(jsonOut, "utf8"));
  assert.equal(output.releaseReady, true);
  assert.equal(output.gates[0].status, "PASS");
  assert.match(await readFile(markdownOut, "utf8"), /Release ready: `true`/u);
});

test("CLI rejects noncanonical snapshot directory IDs and does not write outputs", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-audit-bad-"));
  fixtureRoots.push(root);
  const receiptsDirectory = join(root, "receipts");
  const snapshotsDirectory = join(root, "snapshots");
  await mkdir(receiptsDirectory, { recursive: true });
  await mkdir(join(snapshotsDirectory, `0${runId}`), { recursive: true });
  const matrixPath = join(root, "recorded.json");
  const jsonOut = join(root, "audited.json");
  const markdownOut = join(root, "audited.md");
  await writeCanonicalJson(matrixPath, recordedMatrix());
  await writeCanonicalJson(join(receiptsDirectory, `${receiptId}.json`), validReceipt());
  await assert.rejects(execFileAsync(process.execPath, [
    join(import.meta.dirname, "audit.mjs"),
    "--recorded-matrix", matrixPath,
    "--receipts", receiptsDirectory,
    "--snapshots", snapshotsDirectory,
    "--json-out", jsonOut,
    "--markdown-out", markdownOut,
  ], { env: { ...process.env, NODE_NO_WARNINGS: "1" } }), (error) => {
    assert.match(`${error.stderr}`, /^PHASE9_GATE_SCHEMA_INVALID: audit failed\r?\n$/u);
    return true;
  });
  await assert.rejects(readFile(jsonOut), { code: "ENOENT" });
  await assert.rejects(readFile(markdownOut), { code: "ENOENT" });
});
