import type {
  TestGenerationCandidateKindV15,
  TestGenerationCandidatePageV15,
  TestGenerationRunV15
} from "@unit-test-ide/test-client";

export interface CoverageSummary {
  readonly functionPercent: number;
  readonly linePercent: number;
  readonly branchPercent: number;
}

export interface GenerationCandidateResult {
  readonly candidateId: string;
  readonly kind: TestGenerationCandidateKindV15;
  readonly label: "Verified" | "Characterization";
  readonly assertionProvenance: string;
  readonly baseline: CoverageSummary;
  readonly after: CoverageSummary;
  readonly delta: CoverageSummary;
  readonly diagnostics: readonly string[];
  readonly plannedEdits: readonly string[];
}

export interface GenerationResultsModel {
  readonly runId: string;
  readonly state: string;
  readonly baseline?: CoverageSummary;
  readonly after?: CoverageSummary;
  readonly delta?: CoverageSummary;
  readonly candidates: readonly GenerationCandidateResult[];
  readonly unsupportedReasons: readonly string[];
}

function coverage(value: TestGenerationCandidatePageV15["items"][number]["baselineCoverage"]): CoverageSummary {
  return {
    functionPercent: value.functionPercent,
    linePercent: value.linePercent,
    branchPercent: value.branchPercent
  };
}

function sumCoverage(left: CoverageSummary, right: CoverageSummary): CoverageSummary {
  return {
    functionPercent: left.functionPercent + right.functionPercent,
    linePercent: left.linePercent + right.linePercent,
    branchPercent: left.branchPercent + right.branchPercent
  };
}

function pathForDisplay(path: string): string {
  if (!path || path.includes("\0") || path.includes("..") || /^[A-Za-z]:[\\/]/.test(path) || path.startsWith("/") || path.startsWith("\\\\")) return "<workspace-path>";
  return path.replaceAll("\\", "/");
}

function diagnosticLabel(code: string, reason?: string): string {
  return reason ? `${code}: ${reason}` : code;
}

export function candidateResult(candidate: TestGenerationCandidatePageV15["items"][number]): GenerationCandidateResult {
  const baseline = coverage(candidate.baselineCoverage);
  const delta = coverage(candidate.deltaCoverage);
  return {
    candidateId: candidate.candidateId,
    kind: candidate.kind,
    label: candidate.kind === "verified" ? "Verified" : "Characterization",
    assertionProvenance: candidate.assertionProvenance.kind,
    baseline,
    after: sumCoverage(baseline, delta),
    delta,
    diagnostics: candidate.diagnostics.map((diagnostic) => diagnosticLabel(diagnostic.code, diagnostic.reason)),
    plannedEdits: candidate.plannedEdits.map((edit) => `${edit.operation}: ${pathForDisplay(edit.path)}`)
  };
}

export function buildGenerationResults(run: TestGenerationRunV15, page: TestGenerationCandidatePageV15): GenerationResultsModel {
  const candidates = page.items.map(candidateResult);
  // Candidates are alternatives, so the headline coverage is the first
  // service-ranked candidate rather than an invalid sum of alternatives.
  const first = candidates[0];
  const baseline = first?.baseline;
  const delta = first?.delta;
  return {
    runId: run.runId,
    state: run.state,
    baseline,
    after: first?.after,
    delta,
    candidates,
    unsupportedReasons: candidates.flatMap((candidate) => candidate.diagnostics.filter((diagnostic) => /unsupported|unavailable|no candidate/i.test(diagnostic)))
  };
}

function percent(value: number | undefined): string {
  return value === undefined ? "—" : `${value.toFixed(2)}%`;
}

/** Render a bounded, path-redacted review suitable for an output panel. */
export function renderGenerationResults(model: GenerationResultsModel): string {
  const lines = [`Generation ${model.runId}`, `State: ${model.state}`];
  if (model.baseline && model.after && model.delta) {
    lines.push(`Coverage: functions ${percent(model.baseline.functionPercent)} → ${percent(model.after.functionPercent)} (${percent(model.delta.functionPercent)})`,
      `Coverage: lines ${percent(model.baseline.linePercent)} → ${percent(model.after.linePercent)} (${percent(model.delta.linePercent)})`,
      `Coverage: branches ${percent(model.baseline.branchPercent)} → ${percent(model.after.branchPercent)} (${percent(model.delta.branchPercent)})`);
  }
  for (const candidate of model.candidates) {
    lines.push(`${candidate.label} candidate ${candidate.candidateId} (${candidate.assertionProvenance})`);
    for (const edit of candidate.plannedEdits) lines.push(`  edit: ${edit}`);
    for (const diagnostic of candidate.diagnostics) lines.push(`  diagnostic: ${diagnostic}`);
  }
  for (const reason of model.unsupportedReasons) lines.push(`Unsupported: ${reason}`);
  return lines.join("\n");
}
