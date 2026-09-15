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

export function validatePlatformReport(report, { candidateCommit, platform }) {
  if (typeof validatePlatformSchema !== "function" || !validatePlatformSchema(report)) invalid("platform report schema");
  if (report.candidateCommit !== candidateCommit || report.platform !== platform) invalid("platform report identity");
  if (!exactIds(report.toolchains.map(({ family }) => family), PLATFORM_TOOLCHAINS[platform])) invalid("platform toolchains");
  for (const toolchain of report.toolchains) {
    if (!exactIds(toolchain.frameworks.map(({ id }) => id), FRAMEWORK_IDS)) invalid("framework set");
    for (const framework of toolchain.frameworks) {
      const identity = FRAMEWORK_IDENTITIES[framework.id];
      if (framework.dependencyVersion !== identity.version
          || framework.dependencySha256 !== identity.sha256
          || framework.dependencyTreeSha256 !== identity.treeSha256) invalid("framework dependency identity");
      if (!exactIds(framework.scenarios.map(({ id }) => id), SCENARIO_IDS)) invalid("framework scenarios");
    }
  }
  return true;
}

export function buildMatrixReport({ candidateCommit, windows, linux }) {
  if (!COMMIT_PATTERN.test(candidateCommit)) invalid("candidate commit");
  validatePlatformReport(windows, { candidateCommit, platform: "win32" });
  validatePlatformReport(linux, { candidateCommit, platform: "linux" });
  const platforms = [linux, windows];
  const frameworkStableIdDigests = {};
  for (const frameworkId of FRAMEWORK_IDS) {
    const digests = new Set(platforms.flatMap(({ toolchains }) => toolchains.map(
      ({ frameworks }) => frameworks.find(({ id }) => id === frameworkId).stableIdDigest,
    )));
    if (digests.size !== 1) invalid(`${frameworkId} stable ID digest`);
    frameworkStableIdDigests[frameworkId] = [...digests][0];
  }
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
