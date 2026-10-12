import type { CoverageMetricV16, CoverageSummaryV16 } from "./generated/coverage-v1-6.js";

// JSON Schema bounds each field; this cross-field invariant is checked after
// decoding because portable JSON Schema cannot compare sibling properties.
export function validateCoverageMetricV16(metric: CoverageMetricV16): boolean {
  return Number.isSafeInteger(metric.covered) && metric.covered >= 0 &&
    Number.isSafeInteger(metric.total) && metric.total >= 0 &&
    (metric.coveredDelta === null || Number.isSafeInteger(metric.coveredDelta)) &&
    metric.covered <= metric.total;
}

export function validateCoverageSummaryV16(summary: CoverageSummaryV16): boolean {
  const values = [summary.functions, summary.lines, summary.branches];
  return values.every(validateCoverageMetricV16) &&
    values.every((value) => (value.coveredDelta === null) === (values[0]!.coveredDelta === null));
}
