import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import {
  encodeCanonicalJson,
  readCanonicalJson,
  writeCanonicalJson,
} from "./canonical-json.mjs";
import schema from "./gates.schema.json" with { type: "json" };
import {
  ALLOWED_DEFERRED_GATE_IDS,
  EVIDENCE_ONLY_PATHS,
  evaluateRecordedMatrix,
  loadPhase9Inputs,
  validateBaseline,
  validateCandidateChanges,
  validateReceipt,
  validateRegistry,
} from "./validate.mjs";

const execFileAsync = promisify(execFile);
const candidateCommit = "a".repeat(40);
const currentCommit = candidateCommit;

function requiredGate(id = "P9-MATRIX-UNIT", source = "docs/spec.md", section = "Acceptance") {
  return {
    id,
    phase: 9,
    category: "quality",
    title: "Matrix unit tests",
    requirementRefs: [{ source, section }],
    disposition: "required",
    verification: {
      commands: ["node --test tools/phase9/validate.test.mjs"],
      workflowPath: ".github/workflows/phase9-gates.yml",
      jobs: ["phase9"],
      artifacts: ["phase9-report"],
    },
  };
}

function deferredGate(id, source = "docs/spec.md", section = id) {
  return {
    ...requiredGate(id, source, section),
    disposition: "deferred",
    deferment: {
      reason: `reason for ${id}`,
      resumeCondition: `resume ${id}`,
    },
  };
}

function validRegistry({ deferred = true } = {}) {
  const gates = [requiredGate()];
  const sections = ["Acceptance"];
  if (deferred) {
    for (const id of ALLOWED_DEFERRED_GATE_IDS) {
      sections.push(id);
      gates.push(deferredGate(id));
    }
  }
  gates.sort((left, right) => left.id.localeCompare(right.id, "en"));
  return {
    schemaVersion: 1,
    product: "unit-test-ide",
    repository: "colayc/unitTest",
    allowedDeferredGateIds: [...ALLOWED_DEFERRED_GATE_IDS],
    sources: [{ path: "docs/spec.md", sections }],
    gates,
  };
}

function validBaseline(evaluationMode = "historical", receiptIds = []) {
  return { schemaVersion: 1, candidateCommit, evaluationMode, receiptIds };
}

function githubReceipt(overrides = {}) {
  const receipt = {
    schemaVersion: 1,
    receiptId: "github-actions-20-1",
    candidateCommit,
    observedAt: "2026-09-13T02:19:28.000Z",
    gateIds: ["P9-MATRIX-UNIT"],
    evidence: {
      kind: "github-actions",
      repository: "colayc/unitTest",
      workflowPath: ".github/workflows/phase9-gates.yml",
      runId: "20",
      runAttempt: 1,
      event: "workflow_dispatch",
      headSha: candidateCommit,
      conclusion: "success",
      jobs: [{ name: "phase9", conclusion: "success" }],
      artifacts: [{ id: "30", name: "phase9-report", digest: "b".repeat(64), expired: false }],
    },
  };
  return { ...receipt, ...overrides, evidence: { ...receipt.evidence, ...overrides.evidence } };
}

async function fixture(name, bytes) {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  fixtureRoots.push(root);
  const path = join(root, name);
  await writeFile(path, bytes);
  return path;
}
const fixtureRoots = [];
test.afterEach(async () => {
  await Promise.all(fixtureRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

test("canonical JSON recursively sorts object keys and ends with one newline", () => {
  assert.equal(
    encodeCanonicalJson({ z: 1, a: { y: 2, x: 3 }, list: [{ b: 2, a: 1 }] }),
    '{\n  "a": {\n    "x": 3,\n    "y": 2\n  },\n  "list": [\n    {\n      "a": 1,\n      "b": 2\n    }\n  ],\n  "z": 1\n}\n',
  );
});

test("reader rejects duplicate keys through canonical round-trip", async () => {
  const path = await fixture("duplicate.json", '{"gate":"a","gate":"b"}\n');
  await assert.rejects(
    readCanonicalJson(path, { label: "receipt", maxBytes: 1024 }),
    /PHASE9_GATE_SCHEMA_INVALID: receipt is not canonical JSON/u,
  );
});

test("reader rejects invalid UTF-8 and empty input", async () => {
  for (const bytes of [Buffer.from([0xc3, 0x28]), Buffer.alloc(0)]) {
    const path = await fixture("invalid.json", bytes);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID/u,
    );
  }
});

test("reader rejects non-canonical formatting and oversized input", async () => {
  for (const content of ['{"b":1,"a":2}\n', '{ "a": 1 }\n', "{}\n\n"]) {
    const path = await fixture("noncanonical.json", content);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID: input is not canonical JSON/u,
    );
  }
  const path = await fixture("large.json", Buffer.alloc(1025, 0x20));
  await assert.rejects(
    readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
    /PHASE9_GATE_SCHEMA_INVALID: input byte length is invalid/u,
  );
});

test("reader rejects arrays, null, and unsafe top-level primitives", async () => {
  for (const content of ["[]\n", "null\n", "true\n", "1\n", '"text"\n']) {
    const path = await fixture("primitive.json", content);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID: input is not canonical JSON/u,
    );
  }
});

test("writer creates parents and writes canonical UTF-8 bytes", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  try {
    const path = join(root, "nested", "output.json");
    await writeCanonicalJson(path, { b: "值", a: 1 });
    assert.deepEqual(await readCanonicalJson(path, { label: "output", maxBytes: 1024 }), { a: 1, b: "值" });
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("writer rejects unsafe top-level values", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  try {
    for (const value of [[], null, true, 1, "text", undefined, Number.NaN]) {
      await assert.rejects(writeCanonicalJson(join(root, "unsafe.json"), value), /PHASE9_GATE_SCHEMA_INVALID/u);
    }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("writer rejects cyclic objects with a stable schema error", async () => {
  const value = {};
  value.self = value;
  await assert.rejects(
    writeCanonicalJson(join(tmpdir(), "phase9-cycle.json"), value),
    /PHASE9_GATE_SCHEMA_INVALID: value contains a cycle/u,
  );
});

test("schema contract is closed and contains the required definitions", () => {
  assert.equal(schema.$schema, "https://json-schema.org/draft/2020-12/schema");
  assert.equal(schema.$id, "https://unit-test-ide.invalid/schemas/phase9-gates-v1.json");
  for (const name of ["registry", "baseline", "githubActionsReceipt", "manualApprovalReceipt", "matrix", "runSnapshot", "jobSnapshot", "artifactSnapshot"]) {
    assert.ok(schema.$defs[name]);
  }
  const defs = schema.$defs;
  assert.deepEqual(defs.commit.pattern, "^[0-9a-f]{40}$");
  assert.deepEqual(defs.digest.pattern, "^[0-9a-f]{64}$");
  assert.deepEqual(defs.decimalId.pattern, "^[1-9][0-9]*$");
  assert.deepEqual(defs.status.enum, ["PASS", "MISSING", "FAILED", "DEFERRED"]);
  assert.deepEqual(defs.conclusion.enum, ["success", "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped"]);
  assert.equal(defs.utcIso.format, "date-time");
  assert.equal(defs.utcIso.pattern, "Z$");
  assert.deepEqual(defs.baseline.properties.evaluationMode.enum, ["historical", "candidate"]);
  assert.equal(defs.registry.properties.schemaVersion.const, 1);
  assert.equal(defs.baseline.properties.schemaVersion.const, 1);
  assert.equal(defs.matrix.properties.schemaVersion.const, 1);
  assert.equal(defs.receipt.oneOf.length, 2);
  assert.equal(defs.githubActionsReceipt.properties.evidence.properties.kind.const, "github-actions");
  assert.equal(defs.manualApprovalReceipt.properties.evidence.properties.kind.const, "manual-approval");
  const visit = (value) => {
    if (!value || typeof value !== "object") return;
    if (value.type === "object") assert.equal(value.additionalProperties, false);
    for (const child of Object.values(value)) visit(child);
  };
  visit(schema);
});

test("only the exact three approved Phase 8 gates may be deferred", () => {
  assert.deepEqual(ALLOWED_DEFERRED_GATE_IDS, [
    "P8-DOCS-CLOSEOUT",
    "P8-LEGAL-THIRD-PARTY",
    "P8-SIGN-WINDOWS",
  ]);
  assert.deepEqual(EVIDENCE_ONLY_PATHS, ["docs/superpowers/evidence/phase9/"]);
  assert.ok(Object.isFrozen(ALLOWED_DEFERRED_GATE_IDS));
  const widened = validRegistry();
  widened.allowedDeferredGateIds.push("P9-PERF-MEMORY");
  assert.throws(() => validateRegistry(widened), /PHASE9_DEFERRED_NOT_ALLOWED/u);
  const deferred = validRegistry();
  deferred.sources[0].sections.push("P9-PERF-MEMORY");
  deferred.gates.push(deferredGate("P9-PERF-MEMORY"));
  deferred.gates.sort((left, right) => left.id.localeCompare(right.id, "en"));
  assert.throws(() => validateRegistry(deferred), /PHASE9_DEFERRED_NOT_ALLOWED/u);
});

test("registry requires unique, sorted, complete source and gate mappings", () => {
  assert.equal(validateRegistry(validRegistry()), true);

  const missing = validRegistry();
  missing.gates.find(({ id }) => id === "P9-MATRIX-UNIT").requirementRefs = [
    { source: "docs/spec.md", section: "P8-DOCS-CLOSEOUT" },
  ];
  assert.throws(() => validateRegistry(missing), /PHASE9_GATE_MISSING: source section/u);

  const cases = [
    (value) => value.sources.push(structuredClone(value.sources[0])),
    (value) => value.sources[0].sections.push("Acceptance"),
    (value) => value.gates.push(structuredClone(value.gates[0])),
    (value) => { value.gates[0].requirementRefs[0].source = "docs/unknown.md"; },
    (value) => { value.gates[0].requirementRefs[0].section = "Unknown"; },
    (value) => value.gates.reverse(),
    (value) => { value.gates.find(({ id }) => id === "P9-MATRIX-UNIT").requirementRefs = [
      { source: "docs/spec.md", section: "P8-DOCS-CLOSEOUT" },
      { source: "docs/spec.md", section: "Acceptance" },
    ]; },
  ];
  for (const mutate of cases) {
    const value = validRegistry();
    mutate(value);
    assert.throws(() => validateRegistry(value), /PHASE9_(?:GATE_SCHEMA_INVALID|GATE_MISSING)/u);
  }
});

test("registry rejects unsafe display strings, paths, duplicates, and empty verification policy", () => {
  const mutations = [
    (gate) => { gate.verification.commands = ["node test\nwhoami"]; },
    (gate) => { gate.verification.commands = ["node `whoami`"]; },
    (gate) => { gate.verification.commands = ["node $(whoami)"]; },
    (gate) => { gate.verification.commands = ["node test && whoami"]; },
    (gate) => { gate.verification.commands = ["node test || whoami"]; },
    (gate) => { gate.verification.commands = ["node test;whoami"]; },
    (gate) => { gate.verification.commands = ["node test > out"]; },
    (gate) => { gate.verification.commands = ["node C:\\temp\\script.mjs"]; },
    (gate) => { gate.verification.commands = ["node /tmp/script.mjs"]; },
    (gate) => { gate.verification.jobs = ["phase9", "phase9"]; },
    (gate) => { gate.verification.artifacts = ["../report"]; },
    (gate) => { gate.verification.commands = []; gate.verification.jobs = []; gate.verification.artifacts = []; },
    (gate) => { gate.verification.workflowPath = "C:\\workflow.yml"; },
  ];
  for (const mutate of mutations) {
    const registry = validRegistry();
    const gate = registry.gates.find(({ id }) => id === "P9-MATRIX-UNIT");
    mutate(gate);
    assert.throws(() => validateRegistry(registry), /PHASE9_GATE_SCHEMA_INVALID/u);
  }
  const unsafeSource = validRegistry();
  unsafeSource.sources[0].path = "../spec.md";
  for (const gate of unsafeSource.gates) gate.requirementRefs[0].source = "../spec.md";
  assert.throws(() => validateRegistry(unsafeSource), /PHASE9_GATE_SCHEMA_INVALID/u);
});

test("deferred gates require nonempty exact reason and resume condition strings", () => {
  for (const [field, value] of [["reason", ""], ["reason", "  "], ["resumeCondition", ""], ["resumeCondition", "\n"]]) {
    const registry = validRegistry();
    registry.gates.find(({ id }) => id === "P8-DOCS-CLOSEOUT").deferment[field] = value;
    assert.throws(() => validateRegistry(registry), /PHASE9_GATE_SCHEMA_INVALID/u);
  }
  const required = validRegistry();
  required.gates.find(({ id }) => id === "P9-MATRIX-UNIT").deferment = { reason: "x", resumeCondition: "y" };
  assert.throws(() => validateRegistry(required), /PHASE9_DEFERRED_NOT_ALLOWED/u);
});

test("baseline and receipts reject duplicate IDs and invalid candidate evidence", () => {
  assert.equal(validateBaseline(validBaseline()), true);
  assert.throws(() => validateBaseline(validBaseline("historical", ["r", "r"])), /PHASE9_EVIDENCE_CONFLICT/u);
  assert.throws(() => validateBaseline({ ...validBaseline(), evaluationMode: "live" }), /PHASE9_GATE_SCHEMA_INVALID/u);
  assert.equal(validateReceipt(githubReceipt()), true);
  assert.throws(() => validateReceipt(githubReceipt({ gateIds: ["A", "A"] })), /PHASE9_EVIDENCE_CONFLICT/u);
  assert.throws(() => validateReceipt(githubReceipt({ evidence: { headSha: "c".repeat(40) } })), /PHASE9_EVIDENCE_UNTRUSTED/u);
  assert.throws(() => validateReceipt(githubReceipt({ evidence: { jobs: [{ name: "phase9", conclusion: "success" }, { name: "phase9", conclusion: "success" }] } })), /PHASE9_EVIDENCE_CONFLICT/u);
});

test("matrix reports missing evidence without blocking catalog completion", () => {
  const matrix = evaluateRecordedMatrix({
    registry: validRegistry(), baseline: validBaseline(), receipts: [], currentCommit, changedPaths: [],
  });
  assert.equal(matrix.catalogComplete, true);
  assert.equal(matrix.releaseReady, false);
  assert.deepEqual(matrix.counts, { pass: 0, missing: 1, failed: 0, deferred: 3 });
  assert.equal(matrix.gates.find(({ id }) => id === "P9-MATRIX-UNIT").status, "MISSING");
  assert.deepEqual(matrix.gates.map(({ id }) => id), [...matrix.gates.map(({ id }) => id)].sort((a, b) => a.localeCompare(b, "en")));
});

test("recorded status is PASS, FAILED, or MISSING according to exact receipt evidence", () => {
  const scenarios = [
    [githubReceipt(), "PASS"],
    [githubReceipt({ evidence: { conclusion: "failure" } }), "FAILED"],
    [githubReceipt({ evidence: { jobs: [] } }), "MISSING"],
    [githubReceipt({ evidence: { artifacts: [] } }), "MISSING"],
  ];
  for (const [receipt, status] of scenarios) {
    const matrix = evaluateRecordedMatrix({
      registry: validRegistry(),
      baseline: validBaseline("historical", [receipt.receiptId]),
      receipts: [receipt], currentCommit, changedPaths: [],
    });
    assert.equal(matrix.gates.find(({ id }) => id === "P9-MATRIX-UNIT").status, status);
  }
});

test("conflicting selected receipts for one gate fail closed", () => {
  const first = githubReceipt();
  const second = githubReceipt({ receiptId: "github-actions-21-1", evidence: { runId: "21" } });
  assert.throws(() => evaluateRecordedMatrix({
    registry: validRegistry(),
    baseline: validBaseline("historical", [first.receiptId, second.receiptId]),
    receipts: [first, second], currentCommit, changedPaths: [],
  }), /PHASE9_EVIDENCE_CONFLICT/u);
});

test("historical evidence never releases while an exact candidate with all PASS can release", () => {
  const receipt = githubReceipt();
  for (const [mode, releaseReady] of [["historical", false], ["candidate", true]]) {
    const matrix = evaluateRecordedMatrix({
      registry: validRegistry({ deferred: false }),
      baseline: validBaseline(mode, [receipt.receiptId]),
      receipts: [receipt], currentCommit, changedPaths: [],
    });
    assert.equal(matrix.evaluationMode, mode);
    assert.equal(matrix.candidateCommit, candidateCommit);
    assert.equal(matrix.currentCommit, currentCommit);
    assert.equal(matrix.releaseReady, releaseReady);
    assert.deepEqual(matrix.counts, { pass: 1, missing: 0, failed: 0, deferred: 0 });
  }
});

test("candidate path validation accepts exact and evidence-only states and rejects unsafe changes", () => {
  assert.equal(validateCandidateChanges({ candidateCommit, currentCommit, changedPaths: [] }), "exact");
  assert.equal(validateCandidateChanges({
    candidateCommit,
    currentCommit: "d".repeat(40),
    changedPaths: ["docs/superpowers/evidence/phase9/receipts/run.json"],
  }), "evidence-only-descendant");
  for (const changedPath of ["apps/service.js", "../secret", "/absolute", "C:\\secret", "docs/superpowers/evidence/phase9-evil/x", "docs//superpowers/evidence/phase9/x"]) {
    assert.throws(() => validateCandidateChanges({
      candidateCommit, currentCommit: "d".repeat(40), changedPaths: [changedPath],
    }), /PHASE9_EVIDENCE_UNTRUSTED/u);
  }
});

test("loader enforces canonical input bounds and receipt count", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-load-"));
  fixtureRoots.push(root);
  const receiptsDirectory = join(root, "receipts");
  await mkdir(receiptsDirectory);
  await writeCanonicalJson(join(root, "registry.json"), validRegistry());
  await writeCanonicalJson(join(root, "baseline.json"), validBaseline());
  const loaded = await loadPhase9Inputs({
    registryPath: join(root, "registry.json"), baselinePath: join(root, "baseline.json"), receiptsDirectory,
  });
  assert.deepEqual(loaded.receipts, []);
  await writeFile(join(root, "baseline.json"), Buffer.alloc(64 * 1024 + 1, 0x20));
  await assert.rejects(loadPhase9Inputs({
    registryPath: join(root, "registry.json"), baselinePath: join(root, "baseline.json"), receiptsDirectory,
  }), /PHASE9_GATE_SCHEMA_INVALID: baseline byte length is invalid/u);
  await writeCanonicalJson(join(root, "baseline.json"), validBaseline());
  await writeFile(join(root, "registry.json"), Buffer.alloc(1024 * 1024 + 1, 0x20));
  await assert.rejects(loadPhase9Inputs({
    registryPath: join(root, "registry.json"), baselinePath: join(root, "baseline.json"), receiptsDirectory,
  }), /PHASE9_GATE_SCHEMA_INVALID: registry byte length is invalid/u);
  await writeCanonicalJson(join(root, "registry.json"), validRegistry());
  await writeFile(join(receiptsDirectory, "large.json"), Buffer.alloc(256 * 1024 + 1, 0x20));
  await assert.rejects(loadPhase9Inputs({
    registryPath: join(root, "registry.json"), baselinePath: join(root, "baseline.json"), receiptsDirectory,
  }), /PHASE9_GATE_SCHEMA_INVALID: receipt byte length is invalid/u);
  await rm(join(receiptsDirectory, "large.json"));
  for (let index = 0; index < 257; index += 1) await writeFile(join(receiptsDirectory, `${index}.json`), "{}\n");
  await assert.rejects(loadPhase9Inputs({
    registryPath: join(root, "registry.json"), baselinePath: join(root, "baseline.json"), receiptsDirectory,
  }), /PHASE9_GATE_SCHEMA_INVALID/u);
});

test("CLI writes canonical matrix and numerically sorted unique run requests", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-cli-"));
  fixtureRoots.push(root);
  const receiptsDirectory = join(root, "receipts");
  await mkdir(receiptsDirectory);
  const first = githubReceipt({ receiptId: "run-10", evidence: { runId: "10" } });
  const second = githubReceipt({ receiptId: "run-2", gateIds: [], evidence: { runId: "2" } });
  await writeCanonicalJson(join(root, "registry.json"), validRegistry({ deferred: false }));
  await writeCanonicalJson(join(root, "baseline.json"), validBaseline("candidate", [first.receiptId, second.receiptId]));
  await writeCanonicalJson(join(receiptsDirectory, "10.json"), first);
  await writeCanonicalJson(join(receiptsDirectory, "2.json"), second);
  const out = join(root, "matrix.json");
  const requestsOut = join(root, "requests.json");
  await execFileAsync(process.execPath, [
    join(import.meta.dirname, "validate.mjs"),
    "--registry", join(root, "registry.json"), "--baseline", join(root, "baseline.json"),
    "--receipts", receiptsDirectory, "--repository-root", root, "--out", out, "--requests-out", requestsOut,
  ]);
  assert.deepEqual(JSON.parse(await readFile(requestsOut, "utf8")), { schemaVersion: 1, runIds: ["2", "10"] });
  assert.equal((await readFile(out, "utf8")).endsWith("\n"), true);
  await assert.rejects(execFileAsync(process.execPath, [join(import.meta.dirname, "validate.mjs"), "--unknown", "unsafe;value"]), (error) => {
    assert.doesNotMatch(`${error.stderr}`, /unsafe;value/u);
    return true;
  });
});
