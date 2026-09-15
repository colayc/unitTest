import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";

import { phase9Failure, readCanonicalJson } from "./canonical-json.mjs";
import schema from "./p7-report.schema.json" with { type: "json" };

const MAX_REPORT_BYTES = 64 * 1024;
const COMMIT_PATTERN = /^[0-9a-f]{40}$/u;

const CONTRACTS = Object.freeze({
  "P7-COVERAGE-UI-AND-SOURCE-DECORATION": Object.freeze({
    executionMode: "ui-contract",
    checks: Object.freeze(["coverage-tree", "html-report-offline", "source-decoration"]),
  }),
  "P7-HISTORY-AND-ARTIFACT-BROWSER": Object.freeze({
    executionMode: "ui-contract",
    checks: Object.freeze(["artifact-browser", "history-browser", "protocol-artifact-integrity"]),
  }),
  "P7-MAIN-USER-JOURNEY": Object.freeze({
    executionMode: "terminal-free-journey",
    checks: Object.freeze([
      "artifact-browser",
      "coverage-ui",
      "discover-tests",
      "history-browser",
      "mock-configuration",
      "run-tests",
      "service-lifecycle",
      "terminal-free",
    ]),
  }),
  "P7-MOCK-CONFIGURATION-UX": Object.freeze({
    executionMode: "ui-contract",
    checks: Object.freeze(["mock-configuration", "mock-failure-navigation", "stub-configuration"]),
  }),
});

const ajv = new Ajv2020({ allErrors: true, strict: true });
const validateSchema = ajv.compile(schema);

function invalid(label) {
  throw phase9Failure("PHASE9_P7_REPORT_INVALID", `${label} is invalid`);
}

function canonicalTimestamp(value, label) {
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) invalid(label);
  return milliseconds;
}

export function validateP7ReportDocument(report) {
  if (!validateSchema(report)) invalid("P7 report schema");
  const started = canonicalTimestamp(report.startedAt, "P7 report startedAt");
  const finished = canonicalTimestamp(report.finishedAt, "P7 report finishedAt");
  if (started >= finished) invalid("P7 report interval");
  const checkIds = report.checks.map(({ id }) => id);
  if (new Set(checkIds).size !== checkIds.length
      || checkIds.some((id, index) => index > 0 && checkIds[index - 1].localeCompare(id, "en") >= 0)) {
    invalid("P7 report checks");
  }
  return true;
}

export function validateP7Report(report, { gateId, candidateCommit, runAttempt }) {
  validateP7ReportDocument(report);
  const contract = CONTRACTS[gateId];
  if (contract === undefined
      || !COMMIT_PATTERN.test(candidateCommit)
      || !Number.isSafeInteger(runAttempt)
      || runAttempt < 1
      || report.gateId !== gateId
      || report.candidateCommit !== candidateCommit
      || report.sourceCommit !== candidateCommit
      || report.runAttempt !== runAttempt
      || report.executionMode !== contract.executionMode
      || report.outcome !== "passed"
      || report.checks.length !== contract.checks.length
      || report.checks.some(({ id, status }, index) => id !== contract.checks[index] || status !== "passed")) {
    invalid("P7 report semantic contract");
  }
  return true;
}

function parseArguments(argv) {
  if (argv.length !== 8
      || argv[0] !== "--input"
      || argv[2] !== "--gate"
      || argv[4] !== "--candidate"
      || argv[6] !== "--attempt"
      || argv.some((value, index) => index % 2 === 1 && (!value || value.includes("\0") || value.startsWith("-")))) {
    invalid("arguments");
  }
  const runAttempt = Number(argv[7]);
  if (!Number.isSafeInteger(runAttempt) || runAttempt < 1 || String(runAttempt) !== argv[7]) invalid("run attempt");
  return { input: argv[1], gateId: argv[3], candidateCommit: argv[5], runAttempt };
}

async function main(argv) {
  const options = parseArguments(argv);
  const report = await readCanonicalJson(options.input, { label: "P7 report", maxBytes: MAX_REPORT_BYTES });
  validateP7Report(report, options);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({ CONTRACTS });
