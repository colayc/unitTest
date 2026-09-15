import assert from "node:assert/strict";
import Ajv2020 from "ajv/dist/2020.js";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
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
import { buildMatrixReport } from "./p4-report.mjs";
import { validateP7Report } from "./p7-report.mjs";
import { artifactNameForP8Gate } from "./p8-report.mjs";
import { createP8ReportArtifacts } from "./p8-report-create.mjs";
import { renderMatrixJson, renderMatrixMarkdown, writeMatrixOutputs } from "./render.mjs";

const execFileAsync = promisify(execFile);
const candidateCommit = "a".repeat(40);
const currentCommit = candidateCommit;
const repositoryRoot = join(import.meta.dirname, "..", "..");
const gateRegistryPath = join(import.meta.dirname, "gates.json");
const P8_REQUIRED_ARTIFACTS = Object.freeze({
  "P8-INSTALL-LIFECYCLE-LINUX": "p8-install-lifecycle-linux-report-{runAttempt}-{reportDigest}",
  "P8-INSTALL-LIFECYCLE-WINDOWS": "p8-install-lifecycle-windows-report-{runAttempt}-{reportDigest}",
  "P8-LICENSE-AUDIT": "p8-license-audit-report-{runAttempt}-{reportDigest}",
  "P8-LINUX-APPIMAGE-PACKAGE": "p8-linux-appimage-package-report-{runAttempt}-{reportDigest}",
  "P8-QUALIFICATION-UNSIGNED": "p8-qualification-unsigned-report-{runAttempt}-{reportDigest}",
  "P8-RUNTIME-PRODUCER-PROVENANCE": "p8-runtime-producer-provenance-report-{runAttempt}-{reportDigest}",
  "P8-WINDOWS-MSIX-PACKAGE": "p8-windows-msix-package-report-{runAttempt}-{reportDigest}",
});
const P4_SCENARIO_IDS = [
  "all",
  "assertion-failure",
  "cancel",
  "crash",
  "discovery",
  "failed-rerun",
  "filter",
  "malformed-output",
  "mock-failure",
  "opaque-fallback",
  "reconnect-replay",
  "repeat",
  "service-restart",
  "single",
  "skip",
  "stale-catalog",
  "timeout",
];
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

function fixtureDigest(label) {
  return createHash("sha256").update(label, "utf8").digest("hex");
}

function p7Report(gateId, overrides = {}) {
  const contracts = {
    "P7-COVERAGE-UI-AND-SOURCE-DECORATION": {
      executionMode: "ui-contract",
      checks: ["coverage-tree", "html-report-offline", "source-decoration"],
    },
    "P7-HISTORY-AND-ARTIFACT-BROWSER": {
      executionMode: "ui-contract",
      checks: ["artifact-browser", "history-browser", "protocol-artifact-integrity"],
    },
    "P7-MAIN-USER-JOURNEY": {
      executionMode: "terminal-free-journey",
      checks: ["artifact-browser", "coverage-ui", "discover-tests", "history-browser", "mock-configuration", "run-tests", "service-lifecycle", "terminal-free"],
    },
    "P7-MOCK-CONFIGURATION-UX": {
      executionMode: "ui-contract",
      checks: ["mock-configuration", "mock-failure-navigation", "stub-configuration"],
    },
  };
  const contract = contracts[gateId];
  return {
    schemaVersion: 1,
    gateId,
    candidateCommit,
    sourceCommit: candidateCommit,
    runAttempt: 1,
    producer: "code-oss-extension-host",
    executionMode: contract.executionMode,
    outcome: "passed",
    startedAt: "2026-09-15T00:00:00.000Z",
    finishedAt: "2026-09-15T00:00:01.000Z",
    checks: contract.checks.map((id) => ({ id, status: "passed" })),
    ...overrides,
  };
}

function p8Report(gateId, overrides = {}) {
  const outcomes = {
    "P8-INSTALL-LIFECYCLE-LINUX": ["install-linux", "launch-linux", "rollback-linux", "uninstall-linux", "upgrade-linux"],
    "P8-INSTALL-LIFECYCLE-WINDOWS": ["install-windows", "launch-windows", "rollback-windows", "uninstall-windows", "upgrade-windows"],
    "P8-LICENSE-AUDIT": ["license-audit-linux", "license-audit-windows"],
    "P8-LINUX-APPIMAGE-PACKAGE": ["appimage-envelope", "appimage-licenses", "appimage-manifest", "appimage-payload", "appimage-runtime"],
    "P8-QUALIFICATION-UNSIGNED": ["appimage-package", "install-lifecycle-linux", "install-lifecycle-windows", "license-audit-linux", "license-audit-windows", "msix-package"],
    "P8-RUNTIME-PRODUCER-PROVENANCE": ["appimagetool", "fixed-code-oss-source", "provenance", "runtime-linux", "runtime-windows"],
    "P8-WINDOWS-MSIX-PACKAGE": ["msix-licenses", "msix-manifest", "msix-payload", "msix-runtime", "msix-unsigned"],
  };
  const producerRun = {
    workflowPath: ".github/workflows/release-inputs.yml",
    sourceCommit: candidateCommit,
    codeOssCommit: "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    runId: "70",
    runAttempt: 2,
    artifacts: [
      { kind: "appimagetool", id: "701", name: "appimagetool-linux-x64-2", digest: fixtureDigest("producer-appimagetool") },
      { kind: "linux-runtime", id: "702", name: "code-oss-linux-x64-2", digest: fixtureDigest("producer-linux") },
      { kind: "provenance", id: "703", name: "release-input-provenance-2", digest: fixtureDigest("producer-provenance") },
      { kind: "windows-runtime", id: "704", name: "code-oss-windows-x64-2", digest: fixtureDigest("producer-windows") },
    ],
  };
  const producerGate = gateId === "P8-RUNTIME-PRODUCER-PROVENANCE";
  const report = {
    schemaVersion: 1,
    gateId,
    candidateCommit,
    sourceCommit: candidateCommit,
    runId: producerGate ? producerRun.runId : "80",
    runAttempt: producerGate ? producerRun.runAttempt : 1,
    producerRun,
    executionMode: producerGate ? "producer" : "unsigned-foundation",
    releaseVersion: "1.2.3",
    packages: [
      { platform: "linux", id: "801", name: "release-input-linux-1.2.3-1", digest: fixtureDigest("package-linux") },
      { platform: "windows", id: "802", name: "release-input-windows-1.2.3-1", digest: fixtureDigest("package-windows") },
    ],
    signing: { signature_required: "0", signature_outcome: "not-required" },
    outcome: "passed",
    outcomes: outcomes[gateId].map((id) => ({ id, status: "passed" })),
  };
  if (producerGate) {
    delete report.releaseVersion;
    delete report.packages;
    delete report.signing;
  }
  return { ...report, ...overrides };
}

function p8Receipt(gate, { report = p8Report(gate.id), includeReport = true, receiptId } = {}) {
  const artifactName = artifactNameForP8Gate(gate.id, report.runAttempt, report);
  const boundArtifacts = gate.id === "P8-RUNTIME-PRODUCER-PROVENANCE"
    ? report.producerRun.artifacts
    : report.packages;
  return githubReceipt({
    receiptId: receiptId ?? `github-actions-p8-${gate.id.toLowerCase()}`,
    gateIds: [gate.id],
    evidence: {
      workflowPath: gate.verification.workflowPath,
      runId: report.runId,
      runAttempt: report.runAttempt,
      jobs: gate.verification.jobs.map((name) => ({ name, conclusion: "success" })),
      artifacts: [
        ...boundArtifacts.map(({ id, name, digest }) => ({ id, name, digest, expired: false })),
        {
          id: String(900 + Object.keys(P8_REQUIRED_ARTIFACTS).indexOf(gate.id)),
          name: artifactName,
          digest: fixtureDigest(`p8-report:${gate.id}`),
          expired: false,
          ...(includeReport ? { report } : {}),
        },
      ],
    },
  });
}

const P4_SCENARIO_RESULTS = {
  all: ["failed", "aggregate"],
  "assertion-failure": ["failed", "assertion"],
  cancel: ["cancelled", "cancelled"],
  crash: ["errored", "crash"],
  discovery: ["passed", "discovery"],
  "failed-rerun": ["failed", "assertion"],
  filter: ["passed", "selection"],
  "malformed-output": ["errored", "malformed-output"],
  "mock-failure": ["failed", "mock-expectation"],
  "opaque-fallback": ["passed", "opaque-fallback"],
  "reconnect-replay": ["passed", "replay"],
  repeat: ["passed", "repeat"],
  "service-restart": ["interrupted", "service-restarted"],
  single: ["passed", "test"],
  skip: ["skipped", "ignored"],
  "stale-catalog": ["rejected", "stale-catalog"],
  timeout: ["timed-out", "timeout"],
};

function p4PlatformReport(platform) {
  const families = platform === "win32" ? ["clang-cl", "msvc"] : ["clang", "gcc"];
  return {
    schemaVersion: 1,
    candidateCommit,
    sourceCommit: candidateCommit,
    platform,
    architecture: "x64",
    executionMode: "native",
    publication: "atomic-after-cleanup",
    startedAt: "2026-09-15T00:00:00.000Z",
    finishedAt: "2026-09-15T00:20:00.000Z",
    toolchains: families.map((family) => ({
      family,
      compilerVersion: family === "msvc" ? "19.44.35228.0" : "22.1.8",
      compilerSha256: fixtureDigest(`compiler:${platform}:${family}`),
      frameworks: [
        {
          id: "cpputest",
          dependencyVersion: "4.0",
          dependencySha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7",
          dependencyTreeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
          stableIdDigest: "b".repeat(64),
        },
        {
          id: "unity",
          dependencyVersion: "2.6.1",
          dependencySha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
          dependencyTreeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
          stableIdDigest: "c".repeat(64),
          cMockProvenance: {
            revision: "6f6f662d72657669657765642d636d6f636b2d31",
            generatorVersion: "2.5.3",
            inputSha256: fixtureDigest("cmock:input"),
            outputSha256: fixtureDigest("cmock:output"),
            manifestSha256: fixtureDigest("cmock:manifest"),
            generatedAtRuntime: false,
          },
        },
      ].map((framework) => {
        const catalogRevision = fixtureDigest(`catalog-revision:${platform}:${family}:${framework.id}`);
        const sourceArtifactSha256 = fixtureDigest(`source:${framework.id}`);
        const sourceLocationDigest = fixtureDigest(`source-locations:${framework.id}`);
        const executableArtifactSha256 = fixtureDigest(`executable:${platform}:${family}:${framework.id}`);
        return {
          ...framework,
          catalogRevision,
          catalogArtifactSha256: fixtureDigest(`catalog-artifact:${platform}:${family}:${framework.id}`),
          sourceArtifactSha256,
          sourceLocationDigest,
          executableArtifactSha256,
          scenarios: P4_SCENARIO_IDS.map((id, index) => ({
            id,
            status: "passed",
            candidateCommit,
            platform,
            toolchainFamily: family,
            frameworkId: framework.id,
            catalogRevision,
            sourceArtifactSha256,
            sourceLocationDigest,
            executableArtifactSha256,
            resultArtifactSha256: fixtureDigest(`result:${platform}:${family}:${framework.id}:${id}`),
            resultArtifactSizeBytes: 1024 + index,
            startedAt: `2026-09-15T00:00:${String(10 + index).padStart(2, "0")}.000Z`,
            finishedAt: `2026-09-15T00:01:${String(10 + index).padStart(2, "0")}.000Z`,
            observedOutcome: P4_SCENARIO_RESULTS[id][0],
            classification: P4_SCENARIO_RESULTS[id][1],
          })),
        };
      }),
    })),
    benchmark: {
      id: "catalog-10000",
      itemCount: 10000,
      sampleCount: 3,
      allocationBudgetPerOperation: 300000,
      allocationsPerOperation: [210120, 210120, 210120],
      catalogRevision: fixtureDigest(`benchmark-catalog-revision:${platform}`),
      catalogArtifactSha256: fixtureDigest(`benchmark-catalog-artifact:${platform}`),
      stableIdDigest: "d".repeat(64),
      startedAt: "2026-09-15T00:18:00.000Z",
      finishedAt: "2026-09-15T00:19:00.000Z",
      status: "passed",
    },
  };
}

function labelOnlyP4PlatformReport(platform) {
  const report = p4PlatformReport(platform);
  delete report.sourceCommit;
  delete report.executionMode;
  delete report.publication;
  delete report.startedAt;
  delete report.finishedAt;
  for (const toolchain of report.toolchains) {
    delete toolchain.compilerSha256;
    for (const framework of toolchain.frameworks) {
      delete framework.catalogRevision;
      delete framework.catalogArtifactSha256;
      delete framework.sourceArtifactSha256;
      delete framework.sourceLocationDigest;
      delete framework.executableArtifactSha256;
      delete framework.cMockProvenance;
      framework.scenarios = framework.scenarios.map(({ id, status }) => ({ id, status }));
    }
  }
  delete report.benchmark.catalogRevision;
  delete report.benchmark.catalogArtifactSha256;
  delete report.benchmark.startedAt;
  delete report.benchmark.finishedAt;
  return report;
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
    "| Gate | Phase | Category | Status | Evidence | Availability | Reason |",
    "|---|---:|---|---|---|---|---|",
    "| P8-DOCS-CLOSEOUT | 8 | docs\\\\for\\ncloseout | DEFERRED |  |  |  |",
    "| P9-FAILED | 9 | qa | FAILED | receipt-3 |  | candidate-descendant-changed-tested-content |",
    "| P9-MATRIX-UNIT | 9 | quality\\|checks | PASS | receipt\\`1 | available |  |",
    "| P9-MISSING | 9 | qa | MISSING |  |  |  |",
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
  const laterEvidencePath = join(lineage.root, "docs", "superpowers", "evidence", "phase9", "later.json");
  await writeFile(laterEvidencePath, "{}\n");
  await git(lineage.root, ["add", "--", "docs/superpowers/evidence/phase9/later.json"]);
  await git(lineage.root, ["commit", "-m", "later evidence"]);
  await execFileAsync(process.execPath, [...args, "--check"]);
  assert.deepEqual(await readFile(jsonOut), beforeJson);
  assert.deepEqual(await readFile(markdownOut), beforeMarkdown);
});

test("historical renderer check tolerates unavailable ancestry while candidate mode rejects it", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate, "historical");
  const markdownOut = join(lineage.root, "matrix.md");
  const jsonOut = join(lineage.root, "matrix.json");
  const args = [join(import.meta.dirname, "render.mjs"), "--registry", inputs.registryPath, "--baseline", inputs.baselinePath, "--receipts", inputs.receiptsDirectory, "--repository-root", lineage.root, "--json-out", jsonOut, "--markdown-out", markdownOut];
  await execFileAsync(process.execPath, args);

  const shallowRoot = await mkdtemp(join(tmpdir(), "phase9-shallow-"));
  fixtureRoots.push(shallowRoot);
  await execFileAsync("git", ["clone", "--depth=1", pathToFileURL(lineage.root).href, shallowRoot]);
  await assert.rejects(git(shallowRoot, ["cat-file", "-e", lineage.candidate]));

  const shallowArgs = args.map((argument) => argument === lineage.root ? shallowRoot : argument);
  await execFileAsync(process.execPath, [...shallowArgs, "--check"]);

  const baseline = JSON.parse(await readFile(inputs.baselinePath, "utf8"));
  await writeCanonicalJson(inputs.baselinePath, { ...baseline, evaluationMode: "candidate" });
  await assert.rejects(execFileAsync(process.execPath, [...shallowArgs, "--check"]), (error) => {
    assert.match(`${error.stderr}`, /PHASE9_EVIDENCE_UNTRUSTED: rendering failed\r?\n$/u);
    return true;
  });
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

test("renderer CLI check detects changed receipt status without overwriting either output", async () => {
  const lineage = await createGitLineageFixture();
  const inputs = await createCliInputs(lineage.candidate);
  const markdownOut = join(lineage.root, "matrix.md");
  const jsonOut = join(lineage.root, "matrix.json");
  const args = [join(import.meta.dirname, "render.mjs"), "--registry", inputs.registryPath, "--baseline", inputs.baselinePath, "--receipts", inputs.receiptsDirectory, "--repository-root", lineage.root, "--json-out", jsonOut, "--markdown-out", markdownOut];
  await execFileAsync(process.execPath, args);
  const beforeJson = await readFile(jsonOut);
  const beforeMarkdown = await readFile(markdownOut);
  const receiptPath = join(inputs.receiptsDirectory, "receipt.json");
  const receipt = JSON.parse(await readFile(receiptPath, "utf8"));
  receipt.evidence.conclusion = "failure";
  await writeCanonicalJson(receiptPath, receipt);
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

test("generic successful workflow metadata and arbitrary gate IDs cannot satisfy required P8 gates", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 8 registry", maxBytes: 1024 * 1024 });
  const requiredGates = registry.gates.filter(({ id }) => Object.hasOwn(P8_REQUIRED_ARTIFACTS, id));
  const foundationGates = requiredGates.filter(({ id }) => id !== "P8-RUNTIME-PRODUCER-PROVENANCE");
  const producerGate = requiredGates.find(({ id }) => id === "P8-RUNTIME-PRODUCER-PROVENANCE");
  const foundationReceipt = githubReceipt({
    receiptId: "github-actions-p8-generic-foundation",
    gateIds: foundationGates.map(({ id }) => id),
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "80",
      jobs: [...new Set(foundationGates.flatMap(({ verification }) => verification.jobs))]
        .map((name) => ({ name, conclusion: "success" })),
      artifacts: [],
    },
  });
  const producerReceipt = githubReceipt({
    receiptId: "github-actions-p8-generic-producer",
    gateIds: [producerGate.id],
    evidence: {
      workflowPath: ".github/workflows/release-inputs.yml",
      runId: "70",
      jobs: [{ name: "attest", conclusion: "success" }],
      artifacts: [],
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: {
      schemaVersion: 1,
      candidateCommit,
      evaluationMode: "historical",
      receiptIds: [foundationReceipt.receiptId, producerReceipt.receiptId],
    },
    receipts: [foundationReceipt, producerReceipt],
    currentCommit,
    changedPaths: [],
  });

  for (const gate of requiredGates) {
    assert.deepEqual(gate.verification.artifacts, [P8_REQUIRED_ARTIFACTS[gate.id]]);
    assert.equal(matrix.gates.find(({ id }) => id === gate.id).status, "MISSING", `${gate.id} requires gate-specific semantic evidence`);
  }
});

test("attempt-qualified P8 artifacts without closed semantic reports remain MISSING", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 8 registry", maxBytes: 1024 * 1024 });
  for (const gate of registry.gates.filter(({ id }) => Object.hasOwn(P8_REQUIRED_ARTIFACTS, id))) {
    const receipt = p8Receipt(gate, { includeReport: false });
    const matrix = evaluateRecordedMatrix({
      registry,
      baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
      receipts: [receipt],
      currentCommit,
      changedPaths: [],
    });
    assert.equal(matrix.gates.find(({ id }) => id === gate.id).status, "MISSING", `${gate.id} rejects an arbitrary same-name artifact`);
  }
});

test("closed P8 reports bind candidate producer artifacts packages and exact passing outcomes", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 8 registry", maxBytes: 1024 * 1024 });
  for (const gate of registry.gates.filter(({ id }) => Object.hasOwn(P8_REQUIRED_ARTIFACTS, id))) {
    const receipt = p8Receipt(gate);
    const matrix = evaluateRecordedMatrix({
      registry,
      baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
      receipts: [receipt],
      currentCommit,
      changedPaths: [],
    });
    assert.equal(matrix.gates.find(({ id }) => id === gate.id).status, "PASS", `${gate.id} accepts its exact semantic report`);
  }
});

test("P8 report generation is deterministic closed and produces provider-name content bindings", async () => {
  const producer = p8Report("P8-RUNTIME-PRODUCER-PROVENANCE");
  const producerInput = {
    schemaVersion: 1,
    mode: "producer",
    candidateCommit,
    runId: producer.runId,
    runAttempt: producer.runAttempt,
    artifacts: producer.producerRun.artifacts,
  };
  const root = await mkdtemp(join(tmpdir(), "phase9-p8-reports-"));
  fixtureRoots.push(root);
  const first = await createP8ReportArtifacts(producerInput, join(root, "producer-first"));
  const second = await createP8ReportArtifacts(producerInput, join(root, "producer-second"));
  assert.deepEqual(first, second);
  assert.equal(first.reports.length, 1);
  const producerEntry = first.reports[0];
  assert.equal(producerEntry.artifactName, artifactNameForP8Gate(
    producerEntry.gateId,
    producer.runAttempt,
    await readCanonicalJson(join(root, "producer-first", producerEntry.file), { label: "generated P8 report", maxBytes: 128 * 1024 }),
  ));

  const qualification = p8Report("P8-QUALIFICATION-UNSIGNED");
  const foundation = await createP8ReportArtifacts({
    schemaVersion: 1,
    mode: "foundation",
    candidateCommit,
    runId: qualification.runId,
    runAttempt: qualification.runAttempt,
    producerRun: qualification.producerRun,
    releaseVersion: qualification.releaseVersion,
    packages: qualification.packages,
    signing: qualification.signing,
  }, join(root, "foundation"));
  assert.equal(foundation.reports.length, 6);
  assert.deepEqual(foundation.reports.map(({ gateId }) => gateId), Object.keys(P8_REQUIRED_ARTIFACTS)
    .filter((gateId) => gateId !== "P8-RUNTIME-PRODUCER-PROVENANCE").sort((left, right) => left.localeCompare(right, "en")));
  await assert.rejects(createP8ReportArtifacts({ ...producerInput, arbitrary: true }, join(root, "open-input")), {
    code: "PHASE9_P8_REPORT_INVALID",
  });
  await assert.rejects(createP8ReportArtifacts({
    schemaVersion: 1,
    mode: "foundation",
    candidateCommit,
    runId: qualification.runId,
    runAttempt: qualification.runAttempt,
    producerRun: qualification.producerRun,
    releaseVersion: qualification.releaseVersion,
    packages: qualification.packages,
    signing: { signature_required: "1", signature_outcome: "verified" },
  }, join(root, "signed-input")), { code: "PHASE9_P8_REPORT_INVALID" });
});

test("unsigned P8 qualification rejects signed ambiguous and malformed reports", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 8 registry", maxBytes: 1024 * 1024 });
  const gate = registry.gates.find(({ id }) => id === "P8-QUALIFICATION-UNSIGNED");
  const status = (report, includeReport = true) => {
    const receipt = p8Receipt(gate, { report, includeReport, receiptId: "github-actions-p8-unsigned-negative" });
    return evaluateRecordedMatrix({
      registry,
      baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
      receipts: [receipt],
      currentCommit,
      changedPaths: [],
    }).gates.find(({ id }) => id === gate.id).status;
  };

  assert.equal(status(p8Report(gate.id), false), "MISSING");
  assert.equal(status(p8Report(gate.id, {
    signing: { signature_required: "1", signature_outcome: "verified" },
  })), "MISSING");
  const wrongSource = p8Report(gate.id, { sourceCommit: "c".repeat(40) });
  assert.equal(status(wrongSource), "MISSING");
  const substitutedPackage = structuredClone(p8Report(gate.id));
  substitutedPackage.packages[0].id = "999";
  const substitutedReceipt = p8Receipt(gate, {
    report: substitutedPackage,
    receiptId: "github-actions-p8-substituted-package",
  });
  substitutedReceipt.evidence.artifacts[0].id = "801";
  const substitutedMatrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [substitutedReceipt.receiptId] },
    receipts: [substitutedReceipt],
    currentCommit,
    changedPaths: [],
  });
  assert.equal(substitutedMatrix.gates.find(({ id }) => id === gate.id).status, "MISSING");
  const missingProducerArtifact = structuredClone(p8Report(gate.id));
  missingProducerArtifact.producerRun.artifacts.pop();
  assert.throws(() => status(missingProducerArtifact), /PHASE9_P8_REPORT_INVALID/u);
  const ambiguous = structuredClone(p8Report(gate.id));
  delete ambiguous.signing;
  assert.equal(status(ambiguous), "MISSING");
  assert.throws(() => status({ ...p8Report(gate.id), unrelated: true }), /PHASE9_P8_REPORT_INVALID/u);
});

test("generic successful foundation jobs cannot satisfy feature-specific gates without their artifacts", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 1 through 9 registry", maxBytes: 1024 * 1024 });
  const gateArtifacts = {
    "P5-LINUX-CLANG-COVERAGE": "linux-clang-coverage-report",
    "P5-WINDOWS-LLVM-COVERAGE": "coverage-execution-windows-{runAttempt}",
    "P6-BRANDING-AND-BUILTIN-REGISTRATION": "code-oss-branding-builtin-report",
    "P6-CODEOSS-HOST-SMOKE": "code-oss-host-smoke-report",
    "P7-COVERAGE-UI-AND-SOURCE-DECORATION": "coverage-ui-source-decoration-report-{runAttempt}",
    "P7-HISTORY-AND-ARTIFACT-BROWSER": "history-artifact-browser-report-{runAttempt}",
    "P7-MAIN-USER-JOURNEY": "main-user-journey-report-{runAttempt}",
    "P7-MOCK-CONFIGURATION-UX": "mock-configuration-ux-report-{runAttempt}",
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
  assert.deepEqual(
    registry.gates.find(({ id }) => id === "P6-BRANDING-AND-BUILTIN-REGISTRATION").verification.jobs,
    ["code-oss-branding-builtin"],
  );
});

test("P7 UI and journey gates reject arbitrary same-name artifacts without closed semantic reports", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 7 registry", maxBytes: 1024 * 1024 });
  const artifacts = {
    "P7-COVERAGE-UI-AND-SOURCE-DECORATION": "coverage-ui-source-decoration-report-1",
    "P7-HISTORY-AND-ARTIFACT-BROWSER": "history-artifact-browser-report-1",
    "P7-MAIN-USER-JOURNEY": "main-user-journey-report-1",
    "P7-MOCK-CONFIGURATION-UX": "mock-configuration-ux-report-1",
  };
  const gateIds = Object.keys(artifacts);
  const receipt = githubReceipt({
    receiptId: "github-actions-p7-arbitrary-artifacts",
    gateIds,
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "43",
      jobs: [{ name: "verify-p7-ui-journey", conclusion: "success" }],
      artifacts: gateIds.map((gateId, index) => ({
        id: String(60 + index),
        name: artifacts[gateId],
        digest: String(index + 1).repeat(64),
        expired: false,
      })),
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });

  for (const gateId of gateIds) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "MISSING", `${gateId} requires a closed semantic report`);
  }
});

test("P7 report validator accepts only exact passing UI and terminal-free contracts", () => {
  for (const gateId of [
    "P7-COVERAGE-UI-AND-SOURCE-DECORATION",
    "P7-HISTORY-AND-ARTIFACT-BROWSER",
    "P7-MAIN-USER-JOURNEY",
    "P7-MOCK-CONFIGURATION-UX",
  ]) {
    assert.equal(validateP7Report(p7Report(gateId), { gateId, candidateCommit, runAttempt: 1 }), true);
  }

  assert.throws(
    () => validateP7Report({ ...p7Report("P7-MOCK-CONFIGURATION-UX"), unrelated: true }, {
      gateId: "P7-MOCK-CONFIGURATION-UX", candidateCommit, runAttempt: 1,
    }),
    /PHASE9_P7_REPORT_INVALID/u,
  );
  assert.throws(
    () => validateP7Report(p7Report("P7-MAIN-USER-JOURNEY", {
      executionMode: "activation-only",
      outcome: "skipped",
      checks: [{ id: "activation", status: "passed" }],
    }), { gateId: "P7-MAIN-USER-JOURNEY", candidateCommit, runAttempt: 1 }),
    /PHASE9_P7_REPORT_INVALID/u,
  );
});

test("P7 closed reports remain MISSING until the dedicated producer job succeeds", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 7 registry", maxBytes: 1024 * 1024 });
  const gateId = "P7-COVERAGE-UI-AND-SOURCE-DECORATION";
  const receipt = githubReceipt({
    receiptId: "github-actions-p7-producer-required",
    gateIds: [gateId],
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "46",
      jobs: [],
      artifacts: [{
        id: "72",
        name: "coverage-ui-source-decoration-report-1",
        digest: "9".repeat(64),
        expired: false,
        report: p7Report(gateId),
      }],
    },
  });
  const evaluate = () => evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  }).gates.find(({ id }) => id === gateId).status;

  assert.equal(evaluate(), "MISSING");
  receipt.evidence.jobs.push({ name: "verify-p7-ui-journey", conclusion: "success" });
  assert.equal(evaluate(), "PASS");
});

test("P7 main journey rejects an activation-only skipped report", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 7 registry", maxBytes: 1024 * 1024 });
  const receipt = githubReceipt({
    receiptId: "github-actions-p7-activation-only",
    gateIds: ["P7-MAIN-USER-JOURNEY"],
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "44",
      jobs: [{ name: "verify-p7-ui-journey", conclusion: "success" }],
      artifacts: [{
        id: "70",
        name: "main-user-journey-report-1",
        digest: "7".repeat(64),
        expired: false,
        report: {
          schemaVersion: 1,
          gateId: "P7-MAIN-USER-JOURNEY",
          candidateCommit,
          sourceCommit: candidateCommit,
          runAttempt: 1,
          producer: "code-oss-extension-host",
          executionMode: "activation-only",
          outcome: "skipped",
          startedAt: "2026-09-15T00:00:00.000Z",
          finishedAt: "2026-09-15T00:00:01.000Z",
          checks: [{ id: "activation", status: "passed" }],
        },
      }],
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });

  assert.equal(matrix.gates.find(({ id }) => id === "P7-MAIN-USER-JOURNEY").status, "MISSING");
});

test("P7 Windows WFP gate rejects workflow_dispatch runs that skip native coverage", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 7 registry", maxBytes: 1024 * 1024 });
  const receipt = githubReceipt({
    receiptId: "github-actions-p7-wfp-dispatch-skip",
    gateIds: ["P7-WINDOWS-WFP-OFFLINE"],
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "45",
      event: "workflow_dispatch",
      jobs: [
        { name: "verify-windows", conclusion: "success" },
        { name: "verify-windows-wfp", conclusion: "success" },
      ],
      artifacts: [{
        id: "71",
        name: "coverage-execution-windows-1",
        digest: "8".repeat(64),
        expired: false,
      }],
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });

  assert.equal(matrix.gates.find(({ id }) => id === "P7-WINDOWS-WFP-OFFLINE").status, "MISSING");
});

test("P5 coverage gates require the closed Linux GCC and Windows LLVM artifacts", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 5 registry", maxBytes: 1024 * 1024 });
  const gateArtifacts = {
    "P5-COVERAGE-FAULT-MAPPING": ["coverage-execution-windows-{runAttempt}", "linux-gcc-coverage-report-{runAttempt}"],
    "P5-COVERAGE-REPORTS": ["coverage-execution-windows-{runAttempt}", "linux-gcc-coverage-report-{runAttempt}"],
    "P5-LINUX-GCC-COVERAGE": ["linux-gcc-coverage-report-{runAttempt}"],
    "P5-WINDOWS-LLVM-COVERAGE": ["coverage-execution-windows-{runAttempt}"],
  };
  const gateIds = Object.keys(gateArtifacts);
  const receipt = githubReceipt({
    receiptId: "github-actions-p5-without-closed-artifacts",
    gateIds,
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "42",
      jobs: [
        { name: "coverage-linux-gcc", conclusion: "success" },
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

  for (const gateId of gateIds) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "MISSING", `${gateId} requires closed coverage artifacts`);
    assert.deepEqual(registry.gates.find(({ id }) => id === gateId).verification.artifacts, gateArtifacts[gateId]);
  }
});

test("P5 attempt-qualified artifact contracts derive exact names from receipt runAttempt", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 5 registry", maxBytes: 1024 * 1024 });
  const receipt = githubReceipt({
    receiptId: "github-actions-p5-attempt-2",
    gateIds: ["P5-LINUX-GCC-COVERAGE"],
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "42",
      runAttempt: 2,
      jobs: [{ name: "coverage-linux-gcc", conclusion: "success" }],
      artifacts: [
        { id: "51", name: "linux-gcc-coverage-report-1", digest: "c".repeat(64), expired: false },
        { id: "52", name: "linux-gcc-coverage-report-2", digest: "d".repeat(64), expired: false },
      ],
    },
  });
  const matrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });

  assert.equal(matrix.gates.find(({ id }) => id === "P5-LINUX-GCC-COVERAGE").status, "PASS");

  receipt.evidence.artifacts[1].name = "linux-gcc-coverage-report-3";
  const wrongAttemptMatrix = evaluateRecordedMatrix({
    registry,
    baseline: { schemaVersion: 1, candidateCommit, evaluationMode: "historical", receiptIds: [receipt.receiptId] },
    receipts: [receipt],
    currentCommit,
    changedPaths: [],
  });
  assert.equal(wrongAttemptMatrix.gates.find(({ id }) => id === "P5-LINUX-GCC-COVERAGE").status, "MISSING");
});

test("generic foundation verification cannot PASS P4 without the native framework matrix report", async () => {
  const registry = await readCanonicalJson(gateRegistryPath, { label: "Phase 4 registry", maxBytes: 1024 * 1024 });
  const gateIds = [
    "P4-CPPUTEST-CPPUMOCK",
    "P4-DISCOVERY-CTEST",
    "P4-RECOVERY-AND-10000-BACKEND",
    "P4-SELECTION-AND-RERUN",
    "P4-UNITY-CMOCK",
  ];
  const receipt = githubReceipt({
    receiptId: "github-actions-foundation-generic-p4",
    gateIds,
    evidence: {
      workflowPath: ".github/workflows/foundation.yml",
      runId: "41",
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

  for (const gateId of gateIds) {
    assert.equal(matrix.gates.find(({ id }) => id === gateId).status, "MISSING", `${gateId} requires P4-specific evidence`);
    assert.deepEqual(registry.gates.find(({ id }) => id === gateId).verification, {
      artifacts: ["native-framework-matrix-report"],
      commands: registry.gates.find(({ id }) => id === gateId).verification.commands,
      jobs: ["verify-framework-matrix", "verify-linux", "verify-windows"],
      workflowPath: ".github/workflows/foundation.yml",
    });
  }
});

test("P4 report CLI aggregates the exact four-toolchain framework and backend benchmark contract", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-p4-report-"));
  fixtureRoots.push(root);
  const windows = join(root, "windows.json");
  const linux = join(root, "linux.json");
  const output = join(root, "matrix.json");
  await writeFile(windows, encodeCanonicalJson(p4PlatformReport("win32")));
  await writeFile(linux, encodeCanonicalJson(p4PlatformReport("linux")));

  let failure;
  try {
    await execFileAsync(process.execPath, [
      join(import.meta.dirname, "p4-report.mjs"),
      "--windows", windows,
      "--linux", linux,
      "--candidate", candidateCommit,
      "--out", output,
    ]);
  } catch (error) {
    failure = error;
  }
  assert.equal(failure, undefined, failure?.stderr);

  const matrix = await readCanonicalJson(output, { label: "P4 matrix report", maxBytes: 1024 * 1024 });
  assert.equal(matrix.schemaVersion, 1);
  assert.equal(matrix.candidateCommit, candidateCommit);
  assert.equal(matrix.overallStatus, "passed");
  assert.deepEqual(matrix.platforms.map(({ platform }) => platform), ["linux", "win32"]);
  assert.deepEqual(matrix.platforms.flatMap(({ toolchains }) => toolchains.map(({ family }) => family)), [
    "clang", "gcc", "clang-cl", "msvc",
  ]);
  assert.deepEqual(matrix.frameworkStableIdDigests, {
    cpputest: "b".repeat(64),
    unity: "c".repeat(64),
  });
  assert.deepEqual(matrix.backendBenchmark, {
    id: "catalog-10000",
    itemCount: 10000,
    sampleCountPerPlatform: 3,
    allocationBudgetPerOperation: 300000,
    stableIdDigest: "d".repeat(64),
    status: "passed",
  });
});

test("P4 report validator rejects incomplete, unlocked, failed, or cross-platform-inconsistent evidence", () => {
  const cases = [
    (windows) => { windows.toolchains[0].family = "gcc"; },
    (windows) => { delete windows.startedAt; },
    (windows) => { delete windows.toolchains[0].compilerSha256; },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios.pop(); },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios[0].status = "failed"; },
    (windows) => { delete windows.toolchains[0].frameworks[0].catalogRevision; },
    (windows) => { delete windows.toolchains[0].frameworks[0].sourceArtifactSha256; },
    (windows) => { delete windows.toolchains[0].frameworks[0].sourceLocationDigest; },
    (windows) => { delete windows.toolchains[0].frameworks[0].scenarios[0].resultArtifactSha256; },
    (windows) => { delete windows.toolchains[0].frameworks[1].cMockProvenance; },
    (windows) => { windows.toolchains[0].frameworks[0].dependencySha256 = "0".repeat(64); },
    (windows) => { windows.toolchains[0].frameworks[0].stableIdDigest = "0".repeat(64); },
    (windows) => { windows.toolchains[0].frameworks[1].cMockProvenance.outputSha256 = "0".repeat(64); },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios[0].catalogRevision = "0".repeat(64); },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios[0].finishedAt = windows.startedAt; },
    (windows) => { windows.benchmark.itemCount = 9999; },
    (windows) => { windows.benchmark.allocationsPerOperation[0] = 300001; },
    (windows) => { delete windows.benchmark.catalogArtifactSha256; },
    (windows) => { windows.unreviewed = true; },
  ];
  for (const mutate of cases) {
    const windows = p4PlatformReport("win32");
    const linux = p4PlatformReport("linux");
    mutate(windows);
    assert.throws(
      () => buildMatrixReport({ candidateCommit, windows, linux }),
      /PHASE9_P4_REPORT_INVALID/u,
    );
  }
});

test("P4 report validator rejects label-only reports without native evidence bindings", () => {
  assert.throws(
    () => buildMatrixReport({
      candidateCommit,
      windows: labelOnlyP4PlatformReport("win32"),
      linux: labelOnlyP4PlatformReport("linux"),
    }),
    /PHASE9_P4_REPORT_INVALID/u,
  );
});

test("P4 report validator rejects a scenario result substituted from another toolchain", () => {
  const windows = p4PlatformReport("win32");
  const linux = p4PlatformReport("linux");
  windows.toolchains[1].frameworks[0].scenarios[0] = structuredClone(
    windows.toolchains[0].frameworks[0].scenarios[0],
  );
  assert.throws(
    () => buildMatrixReport({ candidateCommit, windows, linux }),
    /PHASE9_P4_REPORT_INVALID/u,
  );
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
    (gate) => { gate.verification.artifacts = ["report{runAttempt}"]; },
    (gate) => { gate.verification.artifacts = ["report-{runAttempt}-extra"]; },
    (gate) => { gate.verification.artifacts = ["report-{runAttempt}{runAttempt}"]; },
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

test("checked-in candidate evidence keeps deferred and unproven gates closed", async () => {
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

  assert.equal(inputs.baseline.evaluationMode, "candidate");
  assert.equal(matrix.evaluationMode, "candidate");
  assert.equal(matrix.releaseReady, false);
  assert.equal(gatesById.get("P8-SIGN-WINDOWS")?.status, "DEFERRED");
  const selectedReceipts = inputs.receipts.filter((receipt) => inputs.baseline.receiptIds.includes(receipt.receiptId));
  const performanceGate = gatesById.get("P9-PERF-MEMORY");
  assert.equal(performanceGate?.status, "FAILED");
  assert.equal(performanceGate?.reason, "candidate-descendant-changed-tested-content");
  assert.equal(
    selectedReceipts.some(
      (receipt) => receipt.receiptId === performanceGate?.receiptId && receipt.gateIds.includes("P9-PERF-MEMORY"),
    ),
    true,
  );
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
