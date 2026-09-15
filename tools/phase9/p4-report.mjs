import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";

import {
  phase9Failure,
  readCanonicalJson,
  writeCanonicalJson,
} from "./canonical-json.mjs";
import schema from "./p4-report.schema.json" with { type: "json" };

const MAX_REPORT_BYTES = 1024 * 1024;
const COMMIT_PATTERN = /^[0-9a-f]{40}$/u;
const PLATFORM_TOOLCHAINS = Object.freeze({
  linux: Object.freeze(["clang", "gcc"]),
  win32: Object.freeze(["clang-cl", "msvc"]),
});
const FRAMEWORK_IDS = Object.freeze(["cpputest", "unity"]);
const SCENARIO_IDS = Object.freeze([
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
]);
const SCENARIO_RESULTS = Object.freeze({
  all: Object.freeze(["failed", "aggregate"]),
  "assertion-failure": Object.freeze(["failed", "assertion"]),
  cancel: Object.freeze(["cancelled", "cancelled"]),
  crash: Object.freeze(["errored", "crash"]),
  discovery: Object.freeze(["passed", "discovery"]),
  "failed-rerun": Object.freeze(["failed", "assertion"]),
  filter: Object.freeze(["passed", "selection"]),
  "malformed-output": Object.freeze(["errored", "malformed-output"]),
  "mock-failure": Object.freeze(["failed", "mock-expectation"]),
  "opaque-fallback": Object.freeze(["passed", "opaque-fallback"]),
  "reconnect-replay": Object.freeze(["passed", "replay"]),
  repeat: Object.freeze(["passed", "repeat"]),
  "service-restart": Object.freeze(["interrupted", "service-restarted"]),
  single: Object.freeze(["passed", "test"]),
  skip: Object.freeze(["skipped", "ignored"]),
  "stale-catalog": Object.freeze(["rejected", "stale-catalog"]),
  timeout: Object.freeze(["timed-out", "timeout"]),
});
const FRAMEWORK_IDENTITIES = Object.freeze({
  cpputest: Object.freeze({
    version: "4.0",
    sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7",
    treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
  }),
  unity: Object.freeze({
    version: "2.6.1",
    sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
    treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
  }),
});

const ajv = new Ajv2020({ allErrors: true, strict: true });
ajv.compile(schema);
const validatePlatformSchema = ajv.getSchema(`${schema.$id}#/$defs/platformReport`);
const validateMatrixSchema = ajv.getSchema(`${schema.$id}#/$defs/matrixReport`);

function invalid(label) {
  throw phase9Failure("PHASE9_P4_REPORT_INVALID", `${label} is invalid`);
}

function exactIds(values, expected) {
  return values.length === expected.length && values.every((value, index) => value === expected[index]);
}

function timestamp(value, label) {
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) invalid(label);
  return milliseconds;
}

function validateInterval(startedAt, finishedAt, label, outer) {
  const started = timestamp(startedAt, `${label} startedAt`);
  const finished = timestamp(finishedAt, `${label} finishedAt`);
  if (started >= finished || (outer && (started < outer.started || finished > outer.finished))) invalid(`${label} interval`);
  return { started, finished };
}

export function validatePlatformReport(report, { candidateCommit, platform }) {
  if (typeof validatePlatformSchema !== "function" || !validatePlatformSchema(report)) invalid("platform report schema");
  if (report.candidateCommit !== candidateCommit || report.sourceCommit !== candidateCommit || report.platform !== platform) {
    invalid("platform report identity");
  }
  const platformInterval = validateInterval(report.startedAt, report.finishedAt, `${platform} report`);
  if (!exactIds(report.toolchains.map(({ family }) => family), PLATFORM_TOOLCHAINS[platform])) invalid("platform toolchains");
  const resultArtifactDigests = new Set();
  for (const toolchain of report.toolchains) {
    if (!exactIds(toolchain.frameworks.map(({ id }) => id), FRAMEWORK_IDS)) invalid("framework set");
    for (const framework of toolchain.frameworks) {
      const identity = FRAMEWORK_IDENTITIES[framework.id];
      if (framework.dependencyVersion !== identity.version
          || framework.dependencySha256 !== identity.sha256
          || framework.dependencyTreeSha256 !== identity.treeSha256) invalid("framework dependency identity");
      if ((framework.id === "unity") !== Object.hasOwn(framework, "cMockProvenance")) invalid("CMock provenance");
      if (!exactIds(framework.scenarios.map(({ id }) => id), SCENARIO_IDS)) invalid("framework scenarios");
      for (const scenario of framework.scenarios) {
        const expected = SCENARIO_RESULTS[scenario.id];
        if (scenario.candidateCommit !== candidateCommit
            || scenario.platform !== platform
            || scenario.toolchainFamily !== toolchain.family
            || scenario.frameworkId !== framework.id
            || scenario.catalogRevision !== framework.catalogRevision
            || scenario.sourceArtifactSha256 !== framework.sourceArtifactSha256
            || scenario.sourceLocationDigest !== framework.sourceLocationDigest
            || scenario.executableArtifactSha256 !== framework.executableArtifactSha256
            || scenario.observedOutcome !== expected[0]
            || scenario.classification !== expected[1]) invalid(`${scenario.id} evidence binding`);
        validateInterval(scenario.startedAt, scenario.finishedAt, `${scenario.id} scenario`, platformInterval);
        if (resultArtifactDigests.has(scenario.resultArtifactSha256)) invalid("duplicate scenario artifact digest");
        resultArtifactDigests.add(scenario.resultArtifactSha256);
      }
    }
  }
  validateInterval(report.benchmark.startedAt, report.benchmark.finishedAt, `${platform} benchmark`, platformInterval);
  return true;
}

export function buildMatrixReport({ candidateCommit, windows, linux }) {
  if (!COMMIT_PATTERN.test(candidateCommit)) invalid("candidate commit");
  validatePlatformReport(windows, { candidateCommit, platform: "win32" });
  validatePlatformReport(linux, { candidateCommit, platform: "linux" });
  const platforms = [linux, windows];
  const frameworkStableIdDigests = {};
  for (const frameworkId of FRAMEWORK_IDS) {
    const frameworks = platforms.flatMap(({ toolchains }) => toolchains.map(
      ({ frameworks: values }) => values.find(({ id }) => id === frameworkId),
    ));
    const stableIdDigests = new Set(frameworks.map(({ stableIdDigest }) => stableIdDigest));
    const sourceArtifactDigests = new Set(frameworks.map(({ sourceArtifactSha256 }) => sourceArtifactSha256));
    const sourceLocationDigests = new Set(frameworks.map(({ sourceLocationDigest }) => sourceLocationDigest));
    if (stableIdDigests.size !== 1 || sourceArtifactDigests.size !== 1 || sourceLocationDigests.size !== 1) {
      invalid(`${frameworkId} cross-platform evidence digest`);
    }
    if (frameworkId === "unity") {
      const provenance = new Set(frameworks.map(({ cMockProvenance }) => JSON.stringify(cMockProvenance)));
      if (provenance.size !== 1) invalid("CMock provenance drift");
    }
    frameworkStableIdDigests[frameworkId] = [...stableIdDigests][0];
  }
  const resultArtifactDigests = platforms.flatMap(({ toolchains }) => toolchains.flatMap(
    ({ frameworks }) => frameworks.flatMap(({ scenarios }) => scenarios.map(({ resultArtifactSha256 }) => resultArtifactSha256)),
  ));
  if (new Set(resultArtifactDigests).size !== resultArtifactDigests.length) invalid("cross-matrix scenario artifact substitution");
  if (linux.benchmark.stableIdDigest !== windows.benchmark.stableIdDigest) invalid("backend benchmark stable ID digest");
  const matrix = {
    schemaVersion: 1,
    candidateCommit,
    platforms,
    frameworkStableIdDigests,
    backendBenchmark: {
      id: "catalog-10000",
      itemCount: 10000,
      sampleCountPerPlatform: 3,
      allocationBudgetPerOperation: 300000,
      stableIdDigest: linux.benchmark.stableIdDigest,
      status: "passed",
    },
    overallStatus: "passed",
  };
  if (typeof validateMatrixSchema !== "function" || !validateMatrixSchema(matrix)) invalid("matrix report schema");
  return matrix;
}

function parseArguments(argv) {
  if (argv.length !== 8
      || argv[0] !== "--windows"
      || argv[2] !== "--linux"
      || argv[4] !== "--candidate"
      || argv[6] !== "--out"
      || argv.some((value, index) => index % 2 === 1 && (!value || value.includes("\0") || value.startsWith("-")))) {
    invalid("arguments");
  }
  return {
    windowsPath: argv[1],
    linuxPath: argv[3],
    candidateCommit: argv[5],
    outputPath: argv[7],
  };
}

async function main(argv) {
  const options = parseArguments(argv);
  const [windows, linux] = await Promise.all([
    readCanonicalJson(options.windowsPath, { label: "Windows P4 report", maxBytes: MAX_REPORT_BYTES }),
    readCanonicalJson(options.linuxPath, { label: "Linux P4 report", maxBytes: MAX_REPORT_BYTES }),
  ]);
  const matrix = buildMatrixReport({ candidateCommit: options.candidateCommit, windows, linux });
  await writeCanonicalJson(options.outputPath, matrix);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({
  FRAMEWORK_IDS,
  PLATFORM_TOOLCHAINS,
  SCENARIO_IDS,
});
