import { createRequire } from "node:module";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import type {
  CoverageCompletenessV1,
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageMetricV1,
  CoverageSummaryV1
} from "./generated/coverage-v1.js";

const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true });
const validateSchema = ajv.compile(
  require("@unit-test-ide/coverage-schema/v1/coverage")
) as ValidateFunction;

export function decodeCoverageDocumentV1(value: unknown): CoverageDocumentV1 {
  if (!validateSchema(value)) {
    throw new Error("invalid Coverage JSON v1: " + ajv.errorsText(validateSchema.errors));
  }
  const result = structuredClone(value) as CoverageDocumentV1;
  validateCoverageDocumentV1(result);
  return result;
}

export function validateCoverageDocumentV1(value: CoverageDocumentV1): void {
  assertCompleteness(value.completeness);
  assertSummary(value.summary, "summary");
  let aggregate = emptySummary();
  let previousURI: string | undefined;
  for (const file of value.files) {
    assertCanonicalURI(file.uri);
    if (previousURI !== undefined &&
        Buffer.compare(Buffer.from(previousURI, "utf8"), Buffer.from(file.uri, "utf8")) >= 0) {
      fail("files are not strictly sorted by URI");
    }
    previousURI = file.uri;
    assertFile(file);
    aggregate = addSummary(aggregate, file.summary);
  }
  if (!sameSummary(aggregate, value.summary)) {
    fail("summary does not equal the sum of file summaries");
  }
}

function fail(message: string): never {
  throw new Error("invalid Coverage JSON v1: " + message);
}

function assertMetric(value: CoverageMetricV1, field: string): void {
  if (!Number.isSafeInteger(value.covered) || !Number.isSafeInteger(value.total) ||
      value.covered < 0 || value.total < 0 || value.covered > value.total) {
    fail(field + " is not a valid metric");
  }
}

function addMetric(first: CoverageMetricV1, second: CoverageMetricV1): CoverageMetricV1 {
  const result = {
    covered: first.covered + second.covered,
    total: first.total + second.total
  };
  assertMetric(result, "aggregated metric");
  return result;
}

function emptySummary(): CoverageSummaryV1 {
  return {
    lines: { covered: 0, total: 0 },
    branches: { covered: 0, total: 0 },
    functions: { covered: 0, total: 0 }
  };
}

function addSummary(first: CoverageSummaryV1, second: CoverageSummaryV1): CoverageSummaryV1 {
  return {
    lines: addMetric(first.lines, second.lines),
    branches: addMetric(first.branches, second.branches),
    functions: addMetric(first.functions, second.functions)
  };
}

function sameSummary(first: CoverageSummaryV1, second: CoverageSummaryV1): boolean {
  return first.lines.covered === second.lines.covered &&
    first.lines.total === second.lines.total &&
    first.branches.covered === second.branches.covered &&
    first.branches.total === second.branches.total &&
    first.functions.covered === second.functions.covered &&
    first.functions.total === second.functions.total;
}

function assertSummary(value: CoverageSummaryV1, field: string): void {
  assertMetric(value.lines, field + ".lines");
  assertMetric(value.branches, field + ".branches");
  assertMetric(value.functions, field + ".functions");
}

function assertCompleteness(value: CoverageCompletenessV1): void {
  if (value.outcome === "available" && value.reasons.length !== 0 ||
      value.outcome === "partial" && value.reasons.length === 0) {
    fail("completeness outcome and reasons are inconsistent");
  }
}

function assertCanonicalURI(uri: string): void {
  const segments = uri.split("/");
  if (uri.length === 0 || uri !== uri.normalize("NFC") ||
      uri.startsWith("/") || uri.includes("\\") || uri.includes("?") ||
      uri.includes("#") || uri.includes("\0") || uri.includes("//") ||
      /^[A-Za-z]:/.test(uri) || /^[A-Za-z][A-Za-z0-9+.-]*:/.test(uri) ||
      segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    fail("file URI is not canonical");
  }
}

function assertFile(file: CoverageFileV1): void {
  assertSummary(file.summary, "file.summary");
  let previousLine = 0;
  let coveredLines = 0;
  let branches: CoverageMetricV1 = { covered: 0, total: 0 };
  for (const line of file.lines) {
    if (!Number.isSafeInteger(line.line) || line.line <= previousLine ||
        !Number.isSafeInteger(line.count) || line.count < 0) {
      fail("file lines are not canonical");
    }
    previousLine = line.line;
    if (line.count > 0) coveredLines++;
    assertMetric(line.branches, "line.branches");
    branches = addMetric(branches, line.branches);
  }
  const lines = { covered: coveredLines, total: file.lines.length };
  if (lines.covered !== file.summary.lines.covered ||
      lines.total !== file.summary.lines.total ||
      branches.covered !== file.summary.branches.covered ||
      branches.total !== file.summary.branches.total) {
    fail("file summary does not match line records");
  }
}
