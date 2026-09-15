import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import { phase9Failure } from "./canonical-json.mjs";
import schema from "./gates.schema.json" with { type: "json" };

const COMMIT_PATTERN = /^[0-9a-f]{40}$/u;
const DECIMAL_ID_PATTERN = /^[1-9][0-9]*$/u;
const REVIEWED_CODE_OSS_COMMIT = "b1c0a14de1414fcdaa400695b4db1c0799bc3124";
const PRODUCER_WORKFLOW = ".github/workflows/release-inputs.yml";
const FOUNDATION_WORKFLOW = ".github/workflows/foundation.yml";

export const P8_REPORT_ARTIFACTS = Object.freeze({
  "P8-INSTALL-LIFECYCLE-LINUX": "p8-install-lifecycle-linux-report-{runAttempt}",
  "P8-INSTALL-LIFECYCLE-WINDOWS": "p8-install-lifecycle-windows-report-{runAttempt}",
  "P8-LICENSE-AUDIT": "p8-license-audit-report-{runAttempt}",
  "P8-LINUX-APPIMAGE-PACKAGE": "p8-linux-appimage-package-report-{runAttempt}",
  "P8-QUALIFICATION-UNSIGNED": "p8-qualification-unsigned-report-{runAttempt}",
  "P8-RUNTIME-PRODUCER-PROVENANCE": "p8-runtime-producer-provenance-report-{runAttempt}",
  "P8-WINDOWS-MSIX-PACKAGE": "p8-windows-msix-package-report-{runAttempt}",
});

const CONTRACTS = Object.freeze({
  "P8-INSTALL-LIFECYCLE-LINUX": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["install-linux", "launch-linux", "rollback-linux", "uninstall-linux", "upgrade-linux"]),
  }),
  "P8-INSTALL-LIFECYCLE-WINDOWS": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["install-windows", "launch-windows", "rollback-windows", "uninstall-windows", "upgrade-windows"]),
  }),
  "P8-LICENSE-AUDIT": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["license-audit-linux", "license-audit-windows"]),
  }),
  "P8-LINUX-APPIMAGE-PACKAGE": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["appimage-envelope", "appimage-licenses", "appimage-manifest", "appimage-payload", "appimage-runtime"]),
  }),
  "P8-QUALIFICATION-UNSIGNED": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["appimage-package", "install-lifecycle-linux", "install-lifecycle-windows", "license-audit-linux", "license-audit-windows", "msix-package"]),
  }),
  "P8-RUNTIME-PRODUCER-PROVENANCE": Object.freeze({
    workflowPath: PRODUCER_WORKFLOW,
    executionMode: "producer",
    outcomes: Object.freeze(["appimagetool", "fixed-code-oss-source", "provenance", "runtime-linux", "runtime-windows"]),
  }),
  "P8-WINDOWS-MSIX-PACKAGE": Object.freeze({
    workflowPath: FOUNDATION_WORKFLOW,
    executionMode: "unsigned-foundation",
    outcomes: Object.freeze(["msix-licenses", "msix-manifest", "msix-payload", "msix-runtime", "msix-unsigned"]),
  }),
});

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
ajv.compile(schema);
const validateSchema = ajv.getSchema(`${schema.$id}#/$defs/p8Report`);

function invalid(label) {
  throw phase9Failure("PHASE9_P8_REPORT_INVALID", `${label} is invalid`);
}

function canonicalTimestamp(value, label) {
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) invalid(label);
  return milliseconds;
}

function exactValues(actual, expected) {
  return actual.length === expected.length && actual.every((value, index) => value === expected[index]);
}

function assertUniqueSorted(values, label) {
  if (new Set(values).size !== values.length
      || values.some((value, index) => index > 0 && values[index - 1].localeCompare(value, "en") >= 0)) {
    invalid(label);
  }
}

function assertUnique(values, label) {
  if (new Set(values).size !== values.length) invalid(label);
}

function assertArtifactBindings(bindings, artifacts, label) {
  if (!Array.isArray(artifacts)) invalid(label);
  const artifactsById = new Map(artifacts.map((artifact) => [artifact?.id, artifact]));
  for (const binding of bindings) {
    const artifact = artifactsById.get(binding.id);
    if (artifact?.name !== binding.name
        || artifact?.digest !== binding.digest
        || artifact?.expired !== false) {
      invalid(label);
    }
  }
}

export function artifactNameForP8Gate(gateId, runAttempt) {
  const template = P8_REPORT_ARTIFACTS[gateId];
  if (template === undefined || !Number.isSafeInteger(runAttempt) || runAttempt < 1) invalid("P8 report artifact name");
  return template.replace("{runAttempt}", String(runAttempt));
}

export function validateP8ReportDocument(report) {
  if (typeof validateSchema !== "function" || !validateSchema(report)) invalid("P8 report schema");
  const started = canonicalTimestamp(report.startedAt, "P8 report startedAt");
  const finished = canonicalTimestamp(report.finishedAt, "P8 report finishedAt");
  if (started >= finished) invalid("P8 report interval");
  assertUniqueSorted(report.outcomes.map(({ id }) => id), "P8 report outcomes");
  assertUniqueSorted(report.producerRun.artifacts.map(({ kind }) => kind), "P8 producer artifacts");
  assertUnique(report.producerRun.artifacts.map(({ id }) => id), "P8 producer artifact IDs");
  if (report.packages !== undefined) {
    assertUniqueSorted(report.packages.map(({ platform }) => platform), "P8 package artifacts");
    assertUnique(report.packages.map(({ id }) => id), "P8 package artifact IDs");
  }
  return true;
}

export function validateP8Report(report, {
  gateId, candidateCommit, runId, runAttempt, workflowPath, artifacts,
}) {
  validateP8ReportDocument(report);
  const contract = CONTRACTS[gateId];
  if (contract === undefined
      || !COMMIT_PATTERN.test(candidateCommit)
      || !DECIMAL_ID_PATTERN.test(runId)
      || !Number.isSafeInteger(runAttempt)
      || runAttempt < 1
      || report.gateId !== gateId
      || report.candidateCommit !== candidateCommit
      || report.sourceCommit !== candidateCommit
      || report.runId !== runId
      || report.runAttempt !== runAttempt
      || workflowPath !== contract.workflowPath
      || report.executionMode !== contract.executionMode
      || report.outcome !== "passed"
      || !exactValues(report.outcomes.map(({ id }) => id), contract.outcomes)
      || report.outcomes.some(({ status }) => status !== "passed")
      || report.producerRun.workflowPath !== PRODUCER_WORKFLOW
      || report.producerRun.sourceCommit !== candidateCommit
      || report.producerRun.codeOssCommit !== REVIEWED_CODE_OSS_COMMIT) {
    invalid("P8 report semantic contract");
  }

  const producerNames = [
    `appimagetool-linux-x64-${report.producerRun.runAttempt}`,
    `code-oss-linux-x64-${report.producerRun.runAttempt}`,
    `release-input-provenance-${report.producerRun.runAttempt}`,
    `code-oss-windows-x64-${report.producerRun.runAttempt}`,
  ];
  if (!exactValues(report.producerRun.artifacts.map(({ name }) => name), producerNames)) invalid("P8 producer artifact identities");

  const producerGate = gateId === "P8-RUNTIME-PRODUCER-PROVENANCE";
  if (producerGate) {
    if (report.producerRun.runId !== runId
        || report.producerRun.runAttempt !== runAttempt
        || report.releaseVersion !== undefined
        || report.packages !== undefined
        || report.signing !== undefined) {
      invalid("P8 producer report contract");
    }
    assertArtifactBindings(report.producerRun.artifacts, artifacts, "P8 producer artifact bindings");
  } else {
    const packageNames = [
      `release-input-linux-${report.releaseVersion}-${runAttempt}`,
      `release-input-windows-${report.releaseVersion}-${runAttempt}`,
    ];
    if (report.signing?.signature_required !== "0"
        || report.signing?.signature_outcome !== "not-required"
        || report.packages === undefined
        || !exactValues(report.packages.map(({ name }) => name), packageNames)) {
      invalid("P8 unsigned foundation contract");
    }
    assertArtifactBindings(report.packages, artifacts, "P8 package artifact bindings");
  }
  return true;
}

export const __testing = Object.freeze({ CONTRACTS });
