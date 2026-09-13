import assert from "node:assert/strict";
import Ajv2020 from "ajv/dist/2020.js";
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
import { renderMatrixJson, renderMatrixMarkdown, writeMatrixOutputs } from "./render.mjs";

const execFileAsync = promisify(execFile);
const candidateCommit = "a".repeat(40);
const currentCommit = candidateCommit;
const repositoryRoot = join(import.meta.dirname, "..", "..");
const gateRegistryPath = join(import.meta.dirname, "gates.json");
const ALL_GATE_IDS = [
  "P1-IPC-PER-USER-AUTH",
  "P1-PROTOCOL-NO-SHELL",
  "P1-PROTOCOL-VERSION-COMPAT",
  "P1-TOKEN-FILE-SECURE",
  "P2-ARTIFACT-ATOMIC-CLEANUP",
  "P2-EVENT-REPLAY-PERSISTENCE",
  "P2-FAILURE-OWNERSHIP",
  "P2-PROCESS-TREE-TERMINATION",
  "P2-TASK-CANCEL-TIMEOUT",
  "P3-CMAKE-CONFIGURE-BUILD",
  "P3-DIAGNOSTIC-URI",
  "P3-TOOLCHAIN-LINUX-CLANG",
  "P3-TOOLCHAIN-LINUX-GCC",
  "P3-TOOLCHAIN-WINDOWS-CLANGCL",
  "P3-TOOLCHAIN-WINDOWS-MSVC",
  "P3-WORKSPACE-TRUST-PATHS",
  "P4-CPPUTEST-CPPUMOCK",
  "P4-DISCOVERY-CTEST",
  "P4-RECOVERY-AND-10000-BACKEND",
  "P4-SELECTION-AND-RERUN",
  "P4-UNITY-CMOCK",
  "P5-COVERAGE-FAULT-MAPPING",
  "P5-COVERAGE-REPORTS",
  "P5-LINUX-CLANG-COVERAGE",
  "P5-LINUX-GCC-COVERAGE",
  "P5-PROTOCOL-V14-COMPAT",
  "P5-WINDOWS-LLVM-COVERAGE",
  "P6-BRANDING-AND-BUILTIN-REGISTRATION",
  "P6-CODEOSS-HOST-SMOKE",
  "P6-SERVICE-LIFECYCLE",
  "P6-TESTING-API",
  "P6-TESTING-API-10000-ITEMS",
  "P6-WORKSPACE-TRUST-GATE",
  "P7-COVERAGE-UI-AND-SOURCE-DECORATION",
  "P7-HISTORY-AND-ARTIFACT-BROWSER",
  "P7-LINUX-GCC-OFFLINE",
  "P7-MAIN-USER-JOURNEY",
  "P7-MOCK-CONFIGURATION-UX",
  "P7-WINDOWS-WFP-OFFLINE",
  "P8-DOCS-CLOSEOUT",
  "P8-INSTALL-LIFECYCLE-LINUX",
  "P8-INSTALL-LIFECYCLE-WINDOWS",
  "P8-LEGAL-THIRD-PARTY",
  "P8-LICENSE-AUDIT",
  "P8-LINUX-APPIMAGE-PACKAGE",
  "P8-QUALIFICATION-UNSIGNED",
  "P8-RUNTIME-PRODUCER-PROVENANCE",
  "P8-SIGN-WINDOWS",
  "P8-WINDOWS-MSIX-PACKAGE",
  "P9-MATRIX-CONTRACT",
  "P9-MATRIX-E2E",
  "P9-MATRIX-FAULT-INJECTION",
  "P9-MATRIX-INTEGRATION",
  "P9-MATRIX-UNIT",
  "P9-PERF-CANCEL",
  "P9-PERF-DISCOVERY-10000",
  "P9-PERF-FILTER",
  "P9-PERF-HARDWARE-BASELINE",
  "P9-PERF-MEMORY",
  "P9-PERF-REPORT",
  "P9-PERF-STARTUP",
  "P9-UPSTREAM-CODEOSS",
];
const PHASE_1_THROUGH_4_SOURCE_PATHS = [
  "docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md",
  "docs/superpowers/specs/2026-07-22-task-engine-persistence-design.md",
  "docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md",
  "docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md",
  "docs/superpowers/specs/2026-07-27-publisher-failure-task-ownership-design.md",
  "docs/superpowers/specs/2026-07-28-close-before-terminalization-design.md",
  "docs/superpowers/specs/2026-07-30-test-framework-discovery-execution-design.md",
  "docs/superpowers/specs/2026-09-03-native-diagnostic-uri-design.md",
];
const ALL_SOURCE_PATHS = [
  "docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md",
  "docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md",
  "docs/superpowers/specs/2026-07-22-task-engine-persistence-design.md",
  "docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md",
  "docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md",
  "docs/superpowers/specs/2026-07-27-publisher-failure-task-ownership-design.md",
  "docs/superpowers/specs/2026-07-28-close-before-terminalization-design.md",
  "docs/superpowers/specs/2026-07-30-test-framework-discovery-execution-design.md",
  "docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md",
  "docs/superpowers/specs/2026-08-05-typescript-client-v1-4-coverage-design.md",
  "docs/superpowers/specs/2026-08-16-phase6-code-oss-extension-design.md",
  "docs/superpowers/specs/2026-08-18-phase6b-testing-api-design.md",
  "docs/superpowers/specs/2026-08-20-phase8-windows-llvm-coverage-execution-design.md",
  "docs/superpowers/specs/2026-08-21-windows-wfp-offline-boundary-design.md",
  "docs/superpowers/specs/2026-08-27-code-oss-runtime-packaging-design.md",
  "docs/superpowers/specs/2026-08-28-release-input-attempt-artifact-identity-design.md",
  "docs/superpowers/specs/2026-08-28-trusted-code-oss-release-input-production-design.md",
  "docs/superpowers/specs/2026-08-31-formal-packaging-blockers-design.md",
  "docs/superpowers/specs/2026-09-01-formal-packaging-followup-design.md",
  "docs/superpowers/specs/2026-09-03-code-oss-cli-smoke-handshake-design.md",
  "docs/superpowers/specs/2026-09-03-native-diagnostic-uri-design.md",
  "docs/superpowers/specs/2026-09-06-phase7-linux-gcc-coverage-execution-design.md",
  "docs/superpowers/specs/2026-09-11-qualified-release-manifest-collision-design.md",
  "docs/superpowers/specs/2026-09-11-windows-llvm-coverage-regression-design.md",
  "docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md",
];
const PHASE_9_GATE_IDS = ALL_GATE_IDS.filter((id) => id.startsWith("P9-"));
const EXACT_DEFERMENTS = {
  "P8-DOCS-CLOSEOUT": {
    reason: "Phase 8 状态文档收口",
    resumeCondition: "前两项通过后更新 roadmap、security、README、验收证据和对应文档测试",
  },
  "P8-LEGAL-THIRD-PARTY": {
    reason: "第三方 license/legal 人工审批",
    resumeCondition: "真实公开发布前，由有权负责人或合格法律审查者对精确候选制品和 notice/license 闭集作出书面审批",
  },
  "P8-SIGN-WINDOWS": {
    reason: "正式 Windows 签名",
    resumeCondition: "真实公开发布前，用正式证书和时间戳完成签名、干净机器验签和签名版 foundation 资格验证",
  },
};
const LOCALIZATION_ONLY_SOURCE = "docs/superpowers/specs/2026-07-22-markdown-chinese-localization-design.md";
const UNSAFE_CATALOG_COMMAND_PATTERN = /[\0\r\n`;<>]|\$\(|&&|\|\|/u;

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

async function git(root, arguments_) {
  return execFileAsync("git", ["-C", root, ...arguments_]);
}

async function createGitLineageFixture({ shellSensitiveRoot = false } = {}) {
  const base = await mkdtemp(join(tmpdir(), "phase9-lineage-"));
  fixtureRoots.push(base);
  const root = shellSensitiveRoot ? join(base, "repo & echo untrusted") : base;
  if (shellSensitiveRoot) await mkdir(root);
  await execFileAsync("git", ["init", root]);
  await git(root, ["config", "user.email", "phase9@example.invalid"]);
  await git(root, ["config", "user.name", "Phase 9 Test"]);

  const productPath = join(root, "apps", "test-service", "internal", "task", "manager.go");
  await mkdir(join(root, "apps", "test-service", "internal", "task"), { recursive: true });
  await writeFile(productPath, `package task\n// ${base}\n`);
  await git(root, ["add", "--", "apps/test-service/internal/task/manager.go"]);
  await git(root, ["commit", "-m", "candidate"]);
  const candidate = (await git(root, ["rev-parse", "HEAD"])).stdout.trim();

  const evidencePath = join(root, "docs", "superpowers", "evidence", "phase9", "receipts", "run.json");
  await mkdir(join(root, "docs", "superpowers", "evidence", "phase9", "receipts"), { recursive: true });
  await writeFile(evidencePath, "{}\n");
  await git(root, ["add", "--", "docs/superpowers/evidence/phase9/receipts/run.json"]);
  await git(root, ["commit", "-m", "evidence"]);
  const evidenceCommit = (await git(root, ["rev-parse", "HEAD"])).stdout.trim();

  await writeFile(productPath, `package task\n// ${base}\n\nfunc changed() {}\n`);
  await git(root, ["add", "--", "apps/test-service/internal/task/manager.go"]);
  await git(root, ["commit", "-m", "product change"]);
  const productCommit = (await git(root, ["rev-parse", "HEAD"])).stdout.trim();
  return { root, candidate, evidenceCommit, productCommit };
}

async function createCliInputs(candidate, evaluationMode = "candidate") {
  const root = await mkdtemp(join(tmpdir(), "phase9-cli-inputs-"));
  fixtureRoots.push(root);
  const receiptsDirectory = join(root, "receipts");
  await mkdir(receiptsDirectory);
  const receipt = githubReceipt({
    candidateCommit: candidate,
    evidence: { headSha: candidate },
  });
  await writeCanonicalJson(join(root, "registry.json"), validRegistry({ deferred: false }));
  await writeCanonicalJson(join(root, "baseline.json"), {
    ...validBaseline(evaluationMode, [receipt.receiptId]), candidateCommit: candidate,
  });
  await writeCanonicalJson(join(receiptsDirectory, "receipt.json"), receipt);
  return {
    registryPath: join(root, "registry.json"),
    baselinePath: join(root, "baseline.json"),
    receiptsDirectory,
    out: join(root, "matrix.json"),
    requestsOut: join(root, "requests.json"),
  };
}

function validatorArguments(inputs, repositoryRoot) {
  return [
    join(import.meta.dirname, "validate.mjs"),
    "--registry", inputs.registryPath,
    "--baseline", inputs.baselinePath,
    "--receipts", inputs.receiptsDirectory,
    "--repository-root", repositoryRoot,
    "--out", inputs.out,
    "--requests-out", inputs.requestsOut,
  ];
}
const fixtureRoots = [];
test.afterEach(async () => {
  await Promise.all(fixtureRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

function rendererMatrix() {
  return {
    schemaVersion: 1,
    catalogComplete: true,
    releaseReady: false,
    evaluationMode: "historical",
    candidateCommit: "a".repeat(40),
    currentCommit: "b".repeat(40),
    recordedByCommit: "c".repeat(40),
    counts: { pass: 1, missing: 1, failed: 1, deferred: 1 },
    gates: [
      { id: "P9-MATRIX-UNIT", phase: 9, category: "quality|checks", status: "PASS", receiptId: "receipt`1", artifactAvailability: "available" },
      { id: "P8-DOCS-CLOSEOUT", phase: 8, category: "docs\\for\ncloseout", status: "DEFERRED" },
      { id: "P9-FAILED", phase: 9, category: "qa", status: "FAILED", receiptId: "receipt-3", reason: "candidate-descendant-changed-tested-content" },
      { id: "P9-MISSING", phase: 9, category: "qa", status: "MISSING" },
    ],
  };
}

test("renderer emits exact deterministic Markdown summary, sorted gates, reasons, and escaping", () => {
  assert.equal(renderMatrixMarkdown(rendererMatrix()), [
    "# Phase 9 Gate Matrix",
    "",
    "- Candidate commit: `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`",
    "- Recorded by commit: `cccccccccccccccccccccccccccccccccccccccc`",
    "- Evaluation mode: `historical`",
    "- Catalog complete: `true`",
    "- Release ready: `false`",
    "",
    "## Status summary",
    "",
    "| Status | Count |",
    "|---|---:|",
    "| PASS | 1 |",
    "| MISSING | 1 |",
    "| FAILED | 1 |",
    "| DEFERRED | 1 |",
    "",
    "## Gates",
    "",
    "| Gate | Phase | Category | Status | Evidence | Reason |",
    "|---|---:|---|---|---|---|",
    "| P8-DOCS-CLOSEOUT | 8 | docs\\\\for\\ncloseout | DEFERRED |  |  |",
    "| P9-FAILED | 9 | qa | FAILED | receipt-3 | candidate-descendant-changed-tested-content |",
    "| P9-MATRIX-UNIT | 9 | quality\\|checks | PASS | receipt\\`1 |  |",
    "| P9-MISSING | 9 | qa | MISSING |  |  |",
    "",
  ].join("\n"));
});

test("renderer JSON is canonical and deterministic across repeated renders", () => {
  const matrix = rendererMatrix();
  assert.equal(renderMatrixJson(matrix), renderMatrixJson(structuredClone(matrix)));
  assert.equal(renderMatrixJson(matrix), encodeCanonicalJson(JSON.parse(renderMatrixJson(matrix))));
  assert.match(renderMatrixJson(matrix), /\n$/u);
});

test("renderer JSON validates against the closed matrix schema and rejects unsafe reasons", () => {
  const ajv = new Ajv2020({ strict: true });
  ajv.addSchema(schema);
  const validateMatrix = ajv.getSchema(`${schema.$id}#/$defs/matrix`);
  const value = JSON.parse(renderMatrixJson(rendererMatrix()));
  assert.equal(validateMatrix(value), true);
  assert.equal(validateMatrix.errors, null);
  const unsafe = structuredClone(value);
  unsafe.gates[1].reason = "secret\nlocal path";
  assert.equal(validateMatrix(unsafe), false);
});

test("renderer check detects one-byte Markdown drift without overwriting", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-render-"));
  fixtureRoots.push(root);
  const jsonPath = join(root, "out", "matrix.json");
  const markdownPath = join(root, "out", "matrix.md");
  await writeMatrixOutputs({ matrix: rendererMatrix(), jsonPath, markdownPath, check: false });
  const original = await readFile(markdownPath, "utf8");
  await writeFile(markdownPath, `${original.slice(0, -1)}X\n`);
  await assert.rejects(writeMatrixOutputs({ matrix: rendererMatrix(), jsonPath, markdownPath, check: true }), /PHASE9_MATRIX_DRIFT/u);
  assert.equal(await readFile(markdownPath, "utf8"), `${original.slice(0, -1)}X\n`);
});

test("renderer CLI check reports drift without overwriting Markdown", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate);
  const markdownOut = join(lineage.root, "matrix.md");
  const jsonOut = join(lineage.root, "matrix.json");
  const args = [
    join(import.meta.dirname, "render.mjs"),
    "--registry", inputs.registryPath, "--baseline", inputs.baselinePath, "--receipts", inputs.receiptsDirectory,
    "--repository-root", lineage.root, "--json-out", jsonOut, "--markdown-out", markdownOut,
  ];
  await execFileAsync(process.execPath, args);
  const original = await readFile(markdownOut, "utf8");
  await writeFile(markdownOut, `${original.slice(0, -1)}X\n`);
  await assert.rejects(execFileAsync(process.execPath, [...args, "--check"]), (error) => {
    assert.match(`${error.stderr}`, /PHASE9_MATRIX_DRIFT/u);
    return true;
  });
  assert.equal(await readFile(markdownOut, "utf8"), `${original.slice(0, -1)}X\n`);
});

test("renderer CLI check preserves a valid recorded snapshot commit across HEAD changes", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate);
  const markdownOut = join(lineage.root, "matrix.md");
  const jsonOut = join(lineage.root, "matrix.json");
  const args = [join(import.meta.dirname, "render.mjs"), "--registry", inputs.registryPath, "--baseline", inputs.baselinePath, "--receipts", inputs.receiptsDirectory, "--repository-root", lineage.root, "--json-out", jsonOut, "--markdown-out", markdownOut];
  await execFileAsync(process.execPath, args);
  const snapshot = "1".repeat(40);
  const json = JSON.parse(await readFile(jsonOut, "utf8"));
  json.recordedByCommit = snapshot;
  await writeCanonicalJson(jsonOut, json);
  const markdown = (await readFile(markdownOut, "utf8")).replace(/- Recorded by commit: `[0-9a-f]{40}`/u, "- Recorded by commit: `" + snapshot + "`");
  await writeFile(markdownOut, markdown);
  const beforeJson = await readFile(jsonOut);
  const beforeMarkdown = await readFile(markdownOut);
  await execFileAsync(process.execPath, [...args, "--check"]);
  assert.deepEqual(await readFile(jsonOut), beforeJson);
  assert.deepEqual(await readFile(markdownOut), beforeMarkdown);
});

test("renderer CLI check rejects malformed recorded snapshot commits without writes", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate);
  const markdownOut = join(lineage.root, "matrix.md");
  const jsonOut = join(lineage.root, "matrix.json");
  const args = [join(import.meta.dirname, "render.mjs"), "--registry", inputs.registryPath, "--baseline", inputs.baselinePath, "--receipts", inputs.receiptsDirectory, "--repository-root", lineage.root, "--json-out", jsonOut, "--markdown-out", markdownOut];
  await execFileAsync(process.execPath, args);
  const json = JSON.parse(await readFile(jsonOut, "utf8"));
  json.recordedByCommit = "not-a-commit";
  await writeCanonicalJson(jsonOut, json);
  const beforeJson = await readFile(jsonOut);
  const beforeMarkdown = await readFile(markdownOut);
  await assert.rejects(execFileAsync(process.execPath, [...args, "--check"]), /PHASE9_MATRIX_DRIFT/u);
  assert.deepEqual(await readFile(jsonOut), beforeJson);
  assert.deepEqual(await readFile(markdownOut), beforeMarkdown);
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
  assert.deepEqual(defs.matrixGate.properties.reason, { const: "candidate-descendant-changed-tested-content" });
  assert.equal(defs.matrixGate.required.includes("reason"), false);
  assert.deepEqual(defs.matrix.properties.recordedByCommit, { $ref: "#/$defs/commit" });
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

test("Phase 1 through 4 catalog has exact source, heading, gate, and verification coverage", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 4 registry", maxBytes: 1024 * 1024 });

  assert.equal(registry.schemaVersion, 1);
  assert.equal(registry.product, "unit-test-ide");
  assert.equal(registry.repository, "colayc/unitTest");
  assert.deepEqual(registry.allowedDeferredGateIds, ["P8-DOCS-CLOSEOUT", "P8-LEGAL-THIRD-PARTY", "P8-SIGN-WINDOWS"]);
  assert.deepEqual(
    registry.sources.map(({ path }) => path).filter((path) => PHASE_1_THROUGH_4_SOURCE_PATHS.includes(path)),
    PHASE_1_THROUGH_4_SOURCE_PATHS,
  );
  assert.equal(registry.sources.some(({ path }) => path === LOCALIZATION_ONLY_SOURCE), false);
  assert.deepEqual(registry.gates.filter(({ phase }) => phase <= 4).map(({ id }) => id), ALL_GATE_IDS.filter((id) => /^P[1-4]-/u.test(id)));
  assert.equal(validateRegistry(registry), true);

  const sourcePaths = registry.sources.map(({ path }) => path);
  const sourceSections = registry.sources.flatMap(({ path, sections }) => sections.map((section) => `${path}\0${section}`));
  const gateIds = registry.gates.map(({ id }) => id);
  assert.equal(new Set(sourcePaths).size, sourcePaths.length);
  assert.equal(new Set(sourceSections).size, sourceSections.length);
  assert.equal(new Set(gateIds).size, gateIds.length);

  for (const source of registry.sources) {
    const markdown = await readFile(join(repositoryRoot, source.path), "utf8");
    const headings = new Set(markdown.split(/\r?\n/u).map((line) => /^#{1,6} (.+)$/u.exec(line)?.[1]).filter(Boolean));
    for (const section of source.sections) {
      assert.ok(headings.has(section), `${source.path} is missing exact heading: ${section}`);
    }
  }

  for (const gate of registry.gates.filter(({ phase }) => phase <= 4)) {
    assert.equal(gate.disposition, "required");
    assert.ok(gate.requirementRefs.length > 0, `${gate.id} must reference a source heading`);
    const { commands, jobs, artifacts } = gate.verification;
    assert.ok(commands.length + jobs.length + artifacts.length > 0, `${gate.id} must declare verification evidence`);
    for (const command of commands) assert.doesNotMatch(command, UNSAFE_CATALOG_COMMAND_PATTERN);
  }

  const missingSource = "docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md";
  const missingSection = "14. 完成标准";
  const missingKey = `${missingSource}\0${missingSection}`;
  const references = registry.gates.flatMap(({ requirementRefs }) => requirementRefs);
  assert.equal(references.filter(({ source, section }) => `${source}\0${section}` === missingKey).length, 1);
  const missing = structuredClone(registry);
  const owner = missing.gates.find(({ requirementRefs }) => requirementRefs.some(
    ({ source, section }) => `${source}\0${section}` === missingKey,
  ));
  owner.requirementRefs = owner.requirementRefs.filter(({ source, section }) => `${source}\0${section}` !== missingKey);
  assert.throws(() => validateRegistry(missing), /PHASE9_GATE_MISSING/u);
});

test("Phase 1 through 4 catalog cites direct no-shell, process-tree, and toolchain requirements", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 4 registry", maxBytes: 1024 * 1024 });
  const taskEngineSource = "docs/superpowers/specs/2026-07-22-task-engine-persistence-design.md";
  const toolchainSource = "docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md";
  const expected = [
    ["P1-PROTOCOL-NO-SHELL", taskEngineSource, "4. 非目标", ["外部命令", "Shell"]],
    ["P2-PROCESS-TREE-TERMINATION", taskEngineSource, "9. 跨平台进程树控制", ["Windows", "Linux", "进程树"]],
    ["P3-TOOLCHAIN-LINUX-CLANG", toolchainSource, "12.4 Linux GCC 与 Clang", ["GCC", "Clang"]],
    ["P3-TOOLCHAIN-LINUX-CLANG", toolchainSource, "19.4 Native E2E Matrix", ["Linux + Clang"]],
    ["P3-TOOLCHAIN-LINUX-GCC", toolchainSource, "12.4 Linux GCC 与 Clang", ["GCC", "Clang"]],
    ["P3-TOOLCHAIN-LINUX-GCC", toolchainSource, "19.4 Native E2E Matrix", ["Linux + GCC"]],
    ["P3-TOOLCHAIN-WINDOWS-CLANGCL", toolchainSource, "12.3 Windows clang-cl", ["clang-cl", "lld-link"]],
    ["P3-TOOLCHAIN-WINDOWS-CLANGCL", toolchainSource, "19.4 Native E2E Matrix", ["Windows + clang-cl"]],
  ];
  const markdownBySource = new Map(await Promise.all([taskEngineSource, toolchainSource].map(async (source) => [
    source,
    await readFile(join(repositoryRoot, source), "utf8"),
  ])));

  for (const [gateId, source, section, terms] of expected) {
    const gate = registry.gates.find(({ id }) => id === gateId);
    assert.ok(gate.requirementRefs.some((reference) => reference.source === source && reference.section === section),
      `${gateId} must cite ${section}`);
    const lines = markdownBySource.get(source).split(/\r?\n/u);
    const start = lines.findIndex((line) => /^#{1,6} (.+)$/u.exec(line)?.[1] === section);
    assert.notEqual(start, -1, `${source} must contain heading ${section}`);
    const level = /^#+/u.exec(lines[start])[0].length;
    const endOffset = lines.slice(start + 1).findIndex((line) => {
      const match = /^(#{1,6}) /u.exec(line);
      return match && match[1].length <= level;
    });
    const end = endOffset === -1 ? lines.length : start + 1 + endOffset;
    const sectionText = lines.slice(start, end).join("\n");
    for (const term of terms) assert.ok(sectionText.includes(term), `${section} must contain ${term}`);
  }
});

test("Phase 1 through 9 catalog has the exact complete inventory and source coverage", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 9 registry", maxBytes: 1024 * 1024 });

  assert.deepEqual(registry.gates.map(({ id }) => id), ALL_GATE_IDS);
  assert.deepEqual(registry.sources.map(({ path }) => path), ALL_SOURCE_PATHS);
  assert.equal(validateRegistry(registry), true);

  const references = new Set(registry.gates.flatMap(({ requirementRefs }) => requirementRefs.map(
    ({ source, section }) => `${source}\0${section}`,
  )));
  for (const source of registry.sources) {
    const markdown = await readFile(join(repositoryRoot, source.path), "utf8");
    const headings = new Set(markdown.split(/\r?\n/u).map((line) => /^#{1,6} (.+)$/u.exec(line)?.[1]).filter(Boolean));
    for (const section of source.sections) {
      assert.ok(headings.has(section), `${source.path} is missing exact heading: ${section}`);
      assert.ok(references.has(`${source.path}\0${section}`), `${source.path} section is not referenced: ${section}`);
    }
  }

  const deferred = registry.gates.filter(({ disposition }) => disposition === "deferred");
  assert.deepEqual(deferred.map(({ id }) => id), Object.keys(EXACT_DEFERMENTS));
  for (const gate of registry.gates) {
    assert.ok(gate.requirementRefs.length > 0, `${gate.id} must cite a direct requirement`);
    const { commands, jobs, artifacts } = gate.verification;
    assert.ok(commands.length + jobs.length + artifacts.length > 0, `${gate.id} must declare a verification channel`);
    assert.equal(gate.disposition, gate.id in EXACT_DEFERMENTS ? "deferred" : "required");
    assert.deepEqual(gate.deferment, EXACT_DEFERMENTS[gate.id]);
  }
});

test("Phase 5 through 8 toolchain, package, signing, and legal gates cite substantive requirements", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 5 through 8 registry", maxBytes: 1024 * 1024 });
  const coverageSource = "docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md";
  const linuxCoverageSource = "docs/superpowers/specs/2026-09-06-phase7-linux-gcc-coverage-execution-design.md";
  const runtimeSource = "docs/superpowers/specs/2026-08-27-code-oss-runtime-packaging-design.md";
  const blockersSource = "docs/superpowers/specs/2026-08-31-formal-packaging-blockers-design.md";
  const trustedProducerSource = "docs/superpowers/specs/2026-08-28-trusted-code-oss-release-input-production-design.md";
  const expected = [
    ["P5-LINUX-CLANG-COVERAGE", coverageSource, "10.1 工具约束", ["clang-cl/Clang", "llvm-profdata", "llvm-cov"]],
    ["P5-LINUX-GCC-COVERAGE", linuxCoverageSource, "7. GCC/gcov Toolset Identity", ["GCC", "gcov"]],
    ["P5-WINDOWS-LLVM-COVERAGE", coverageSource, "10.1 工具约束", ["clang-cl/Clang", "llvm-profdata", "llvm-cov"]],
    ["P8-INSTALL-LIFECYCLE-LINUX", runtimeSource, "Install and Rollback Smoke", ["First-install", "rollback"]],
    ["P8-INSTALL-LIFECYCLE-WINDOWS", runtimeSource, "Install and Rollback Smoke", ["First-install", "rollback"]],
    ["P8-LICENSE-AUDIT", runtimeSource, "License Handling", ["license-audit.mjs", "NOTICE"]],
    ["P8-LINUX-APPIMAGE-PACKAGE", blockersSource, "Linux AppImage", ["appimagetool", "SVG"]],
    ["P8-WINDOWS-MSIX-PACKAGE", blockersSource, "Windows MSIX", ["package-msix.ps1", "SOURCE_DATE_EPOCH"]],
    ["P8-LEGAL-THIRD-PARTY", trustedProducerSource, "Goal", ["license/legal review", "separate requirements"]],
    ["P8-SIGN-WINDOWS", trustedProducerSource, "Goal", ["formal Windows signing", "separate requirements"]],
  ];
  const markdownBySource = new Map(await Promise.all([...new Set(expected.map(([, source]) => source))].map(async (source) => [
    source,
    await readFile(join(repositoryRoot, source), "utf8"),
  ])));

  for (const [gateId, source, section, terms] of expected) {
    const gate = registry.gates.find(({ id }) => id === gateId);
    assert.ok(gate.requirementRefs.some((reference) => reference.source === source && reference.section === section),
      `${gateId} must cite ${section}`);
    const lines = markdownBySource.get(source).split(/\r?\n/u);
    const start = lines.findIndex((line) => /^#{1,6} (.+)$/u.exec(line)?.[1] === section);
    assert.notEqual(start, -1, `${source} must contain heading ${section}`);
    const level = /^#+/u.exec(lines[start])[0].length;
    const endOffset = lines.slice(start + 1).findIndex((line) => {
      const match = /^(#{1,6}) /u.exec(line);
      return match && match[1].length <= level;
    });
    const end = endOffset === -1 ? lines.length : start + 1 + endOffset;
    const sectionText = lines.slice(start, end).join("\n");
    for (const term of terms) assert.ok(sectionText.includes(term), `${section} must contain ${term}`);
  }

  const legal = registry.gates.find(({ id }) => id === "P8-LEGAL-THIRD-PARTY");
  assert.deepEqual(legal.verification, {
    artifacts: [],
    commands: ["review exact release candidate notices and licenses"],
    jobs: [],
    workflowPath: ".github/workflows/phase9-gates.yml",
  });
  const signing = registry.gates.find(({ id }) => id === "P8-SIGN-WINDOWS");
  assert.deepEqual(signing.verification.artifacts, ["signed-windows-release"]);
  assert.deepEqual(signing.verification.jobs, ["package-windows", "release-qualification"]);
});

test("future Phase 9 work remains MISSING and only the approved Phase 8 boundary is DEFERRED", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 9 registry", maxBytes: 1024 * 1024 });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [] },
    receipts: [],
    currentCommit,
    changedPaths: [],
  });

  for (const gateId of PHASE_9_GATE_IDS) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "MISSING", `${gateId} must remain future work`);
  }
  for (const gateId of Object.keys(EXACT_DEFERMENTS)) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "DEFERRED", `${gateId} must remain explicitly deferred`);
  }
});

test("generic successful foundation jobs cannot satisfy feature-specific gates without their artifacts", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 9 registry", maxBytes: 1024 * 1024 });
  const gateArtifacts = {
    "P5-LINUX-CLANG-COVERAGE": "linux-clang-coverage-report",
    "P6-CODEOSS-HOST-SMOKE": "code-oss-host-smoke-report",
    "P7-COVERAGE-UI-AND-SOURCE-DECORATION": "coverage-ui-source-decoration-report",
    "P7-HISTORY-AND-ARTIFACT-BROWSER": "history-artifact-browser-report",
    "P7-MAIN-USER-JOURNEY": "main-user-journey-report",
    "P7-MOCK-CONFIGURATION-UX": "mock-configuration-ux-report",
  };
  const gateIds = Object.keys(gateArtifacts);
  const receipt = githubReceipt({
    receiptId: "github-actions-foundation-generic",
    gateIds,
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "40",
      jobs: [
        { name: "verify-linux", conclusion: "success" },
        { name: "verify-windows", conclusion: "success" },
      ],
      artifacts: [],
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });

  assert.equal(new Set(Object.values(gateArtifacts)).size, gateIds.length);
  for (const gateId of gateIds) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "MISSING", `${gateId} requires feature-specific evidence`);
    assert.deepEqual(registry.gates.find(({ id }) => id === gateId).verification.artifacts, [gateArtifacts[gateId]]);
  }
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
    (gate) => { gate.verification.commands = ["go test ../other-service/..."]; },
    (gate) => { gate.verification.commands = ["node --require=/tmp/x"]; },
    (gate) => { gate.verification.commands = ["go test --pkg=../other"]; },
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

test("registry accepts safe relative package arguments in display-only commands", () => {
  const registry = validRegistry();
  registry.gates.find(({ id }) => id === "P9-MATRIX-UNIT").verification.commands = [
    "./tools/check.mjs",
    "go test ./apps/test-service/...",
  ];
  assert.equal(validateRegistry(registry), true);
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

test("successful evidence from another repository cannot PASS a gate", () => {
  const receipt = githubReceipt({ evidence: { repository: "attacker/unitTest" } });
  const matrix = evaluateRecordedMatrix({
    registry: validRegistry({ deferred: false }),
    baseline: validBaseline("candidate", [receipt.receiptId]),
    receipts: [receipt], currentCommit, changedPaths: [],
  });
  assert.equal(matrix.gates.find(({ id }) => id === "P9-MATRIX-UNIT").status, "MISSING");
  assert.equal(matrix.releaseReady, false);
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
  for (const changedPath of [
    "", "apps/service.js", "../secret", "./docs/superpowers/evidence/phase9/x", "/absolute", "C:\\secret",
    "docs/superpowers/evidence/phase9-evil/x", "docs//superpowers/evidence/phase9/x",
    "docs/superpowers/evidence/phase9/x/../y", "docs/superpowers/evidence/phase9/x\ny",
    ".github/workflows/foundation.yml",
  ]) {
    assert.throws(() => validateCandidateChanges({
      candidateCommit, currentCommit: "d".repeat(40), changedPaths: [changedPath],
    }), /PHASE9_EVIDENCE_UNTRUSTED/u);
  }
  assert.throws(() => validateCandidateChanges({
    candidateCommit, currentCommit: "d".repeat(40), changedPaths: [],
  }), /PHASE9_EVIDENCE_UNTRUSTED/u);
  for (const malformedCommit of ["", "A".repeat(40), "a".repeat(39), "$(whoami)"]) {
    assert.throws(() => validateCandidateChanges({
      candidateCommit: malformedCommit, currentCommit, changedPaths: [],
    }), /PHASE9_EVIDENCE_UNTRUSTED/u);
  }
});

test("candidate path validation rejects sparse changed-path arrays", () => {
  for (const changedPaths of [
    new Array(1),
    [, "docs/superpowers/evidence/phase9/x"],
  ]) {
    assert.throws(() => validateCandidateChanges({
      candidateCommit,
      currentCommit: "d".repeat(40),
      changedPaths,
    }), /PHASE9_EVIDENCE_UNTRUSTED/u);
  }
});

test("candidate evidence survives only an evidence-only descendant", async () => {
  const lineage = await createGitLineageFixture();
  assert.doesNotThrow(() => validateCandidateChanges({
    candidateCommit: lineage.candidate,
    currentCommit: lineage.evidenceCommit,
    changedPaths: ["docs/superpowers/evidence/phase9/receipts/run.json"],
  }));
  assert.throws(() => validateCandidateChanges({
    candidateCommit: lineage.candidate,
    currentCommit: lineage.productCommit,
    changedPaths: ["apps/test-service/internal/task/manager.go"],
  }), /PHASE9_EVIDENCE_UNTRUSTED/u);
});

test("candidate mode fails only would-be PASS rows after tested-content changes", () => {
  const receipt = githubReceipt();
  const matrix = evaluateRecordedMatrix({
    registry: validRegistry({ deferred: false }),
    baseline: validBaseline("candidate", [receipt.receiptId]),
    receipts: [receipt],
    currentCommit: "d".repeat(40),
    changedPaths: ["apps/test-service/internal/task/manager.go"],
  });
  assert.equal(matrix.releaseReady, false);
  assert.deepEqual(matrix.counts, { pass: 0, missing: 0, failed: 1, deferred: 0 });
  assert.deepEqual(matrix.gates[0], {
    id: "P9-MATRIX-UNIT",
    status: "FAILED",
    receiptId: receipt.receiptId,
    artifactAvailability: "available",
    reason: "candidate-descendant-changed-tested-content",
  });
});

test("candidate mode does not downgrade malformed or unsafe lineage inputs", () => {
  const receipt = githubReceipt();
  const base = {
    registry: validRegistry({ deferred: false }),
    baseline: validBaseline("candidate", [receipt.receiptId]),
    receipts: [receipt],
  };
  for (const state of [
    { currentCommit: "d".repeat(40), changedPaths: [] },
    { currentCommit: "d".repeat(40), changedPaths: ["../unsafe"] },
    { currentCommit: "not-a-commit", changedPaths: [] },
  ]) {
    assert.throws(
      () => evaluateRecordedMatrix({ ...base, ...state }),
      /PHASE9_(?:EVIDENCE_UNTRUSTED|GATE_SCHEMA_INVALID)/u,
    );
  }
});

test("historical mode preserves receipt-backed rows after later product changes", () => {
  const receipt = githubReceipt();
  const matrix = evaluateRecordedMatrix({
    registry: validRegistry({ deferred: false }),
    baseline: validBaseline("historical", [receipt.receiptId]),
    receipts: [receipt],
    currentCommit: "d".repeat(40),
    changedPaths: ["apps/test-service/internal/task/manager.go"],
  });
  assert.equal(matrix.evaluationMode, "historical");
  assert.equal(matrix.releaseReady, false);
  assert.equal(matrix.gates[0].status, "PASS");
  assert.equal("reason" in matrix.gates[0], false);
});

test("checked-in historical evidence keeps deferred and unproven gates closed", async () => {
  const evidenceRoot = join(repositoryRoot, "docs", "superpowers", "evidence", "phase9");
  const inputs = await loadPhase9Inputs({
    registryPath: gateRegistryPath,
    baselinePath: join(evidenceRoot, "baseline.json"),
    receiptsDirectory: join(evidenceRoot, "receipts"),
  });
  const matrix = await readCanonicalJson(join(evidenceRoot, "gate-matrix.json"), {
    label: "checked-in gate matrix",
    maxBytes: 1024 * 1024,
  });
  const gatesById = new Map(matrix.gates.map((gate) => [gate.id, gate]));

  assert.equal(inputs.baseline.evaluationMode, "historical");
  assert.equal(matrix.evaluationMode, "historical");
  assert.equal(matrix.releaseReady, false);
  assert.equal(gatesById.get("P8-SIGN-WINDOWS")?.status, "DEFERRED");
  assert.equal(gatesById.get("P9-PERF-MEMORY")?.status, "MISSING");
});

test("candidate CLI derives tested-content changes from Git", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate);
  await execFileAsync(process.execPath, validatorArguments(inputs, lineage.root));
  const matrix = JSON.parse(await readFile(inputs.out, "utf8"));
  assert.equal(matrix.currentCommit, lineage.productCommit);
  assert.deepEqual(matrix.gates[0], {
    artifactAvailability: "available",
    id: "P9-MATRIX-UNIT",
    reason: "candidate-descendant-changed-tested-content",
    receiptId: "github-actions-20-1",
    status: "FAILED",
  });
});

test("candidate CLI rejects unrelated history without disclosing repository paths", async () => {
  const candidateLineage = await createGitLineageFixture();
  const unrelatedLineage = await createGitLineageFixture();
  const inputs = await createCliInputs(candidateLineage.candidate);
  await assert.rejects(
    execFileAsync(process.execPath, validatorArguments(inputs, unrelatedLineage.root)),
    (error) => {
      assert.match(`${error.stderr}`, /PHASE9_EVIDENCE_UNTRUSTED/u);
      assert.doesNotMatch(`${error.stderr}`, new RegExp(unrelatedLineage.root.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
      return true;
    },
  );
});

test("candidate CLI wraps Git exit 128 as an untrusted execution failure without path disclosure", async () => {
  const lineage = await createGitLineageFixture();
  const missingObject = "f".repeat(40);
  const inputs = await createCliInputs(missingObject);
  await assert.rejects(
    execFileAsync(process.execPath, validatorArguments(inputs, lineage.root)),
    (error) => {
      assert.match(`${error.stderr}`, /PHASE9_EVIDENCE_UNTRUSTED: validation failed\r?\n$/u);
      assert.doesNotMatch(`${error.stderr}`, new RegExp(lineage.root.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
      return true;
    },
  );
  await assert.rejects(readFile(inputs.out, "utf8"), { code: "ENOENT" });
});

test("candidate CLI passes shell-sensitive repository roots as literal Git arguments", async () => {
  const lineage = await createGitLineageFixture({ shellSensitiveRoot: true });
  await git(lineage.root, ["checkout", "--detach", lineage.candidate]);
  const inputs = await createCliInputs(lineage.candidate);
  await execFileAsync(process.execPath, validatorArguments(inputs, lineage.root));
  const matrix = JSON.parse(await readFile(inputs.out, "utf8"));
  assert.equal(matrix.currentCommit, lineage.candidate);
  assert.equal(matrix.releaseReady, true);
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
  await execFileAsync("git", ["init", root]);
  await git(root, ["config", "user.email", "phase9@example.invalid"]);
  await git(root, ["config", "user.name", "Phase 9 Test"]);
  await writeFile(join(root, "candidate.txt"), "candidate\n");
  await git(root, ["add", "--", "candidate.txt"]);
  await git(root, ["commit", "-m", "candidate"]);
  const commit = (await git(root, ["rev-parse", "HEAD"])).stdout.trim();
  const first = githubReceipt({
    receiptId: "run-10", candidateCommit: commit, evidence: { runId: "10", headSha: commit },
  });
  const second = githubReceipt({
    receiptId: "run-2", candidateCommit: commit, gateIds: [], evidence: { runId: "2", headSha: commit },
  });
  await writeCanonicalJson(join(root, "registry.json"), validRegistry({ deferred: false }));
  await writeCanonicalJson(join(root, "baseline.json"), {
    ...validBaseline("candidate", [first.receiptId, second.receiptId]), candidateCommit: commit,
  });
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
