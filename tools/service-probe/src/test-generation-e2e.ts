import { randomBytes } from "node:crypto";
import {
  ManagedConflictChoiceV16,
  TestGenerationFrameworkV16,
  TestGenerationScopeV16,
  type CoverageSummaryV16,
  type TestGenerationRunV16,
  type TestGenerationStartRequestV16,
} from "@unit-test-ide/protocol-models";
import type { ProtocolClient } from "@unit-test-ide/test-client";

export const TEST_GENERATION_SCENARIO_IDS = Object.freeze([
  "function-scope",
  "class-scope",
  "file-scope",
  "target-scope",
  "workspace-scope",
  "coverage-gap-scope",
  "verified-oracle",
  "characterization-oracle",
  "no-safe-target",
  "unsatisfied-branch",
  "compile-failure",
  "suite-regression",
  "no-coverage-delta",
  "cancel-before-configure",
  "cancel-during-build",
  "cancel-during-discovery",
  "cancel-during-candidate",
  "cancel-during-suite",
  "cancel-during-coverage",
  "service-restart",
  "stale-snapshot",
  "acceptance-conflict",
  "repeat-acceptance",
  "one-click-refresh",
  "managed-rerun-byte-stable",
  "managed-unmanaged-bytes-preserved",
  "managed-edited-conflict-zero-write",
  "managed-accepted-update-idempotent",
  "coverage-project-file-function-totals",
  "coverage-navigation-lines",
  "coverage-no-metric-regression",
] as const);

export type TestGenerationScenarioId = typeof TEST_GENERATION_SCENARIO_IDS[number];

export interface TestGenerationScenarioResult {
  readonly id: TestGenerationScenarioId;
  readonly status: "passed" | "failed" | "blocked";
  readonly mode: "offline-static" | "native";
  readonly reason?: string;
}

export type TestGenerationScenarioExecutor = (
  scenario: TestGenerationScenarioId,
) => Promise<TestGenerationScenarioResult>;

type NativeManagedGenerationClient = Pick<ProtocolClient,
  "getCapabilities" | "getCoverageProject" | "listCoverageFiles" | "listCoverageFunctions" |
  "startTestGeneration" | "getTestGenerationRun" | "getManagedReview" | "applyManagedReview" |
  "listManagedTests">;

export interface NativeManagedGenerationContext {
  readonly client: NativeManagedGenerationClient;
  readonly projectId: string;
  readonly workspaceGeneration: string;
  readonly coverageReportId: string;
  readonly fileId: string;
  readonly functionId: string;
  readonly framework: "cpputest" | "unity";
  readonly maxPollAttempts?: number;
  readonly pollIntervalMs?: number;
  readonly sleep?: (milliseconds: number) => Promise<void>;
  readonly refreshCoverage: () => Promise<NativeCoverageRefreshBinding>;
}

export interface NativeCoverageRefreshBinding {
  readonly workspaceGeneration: string;
  readonly coverageReportId: string;
  readonly fileId: string;
  readonly functionId: string;
}

export interface NativeManagedGenerationReceipt {
  readonly runId: string;
  readonly reviewId: string;
  readonly reviewDigest: string;
  readonly caseCount: number;
  readonly managedRecordCount: number;
  readonly baseline: CoverageSummaryV16;
  readonly final: CoverageSummaryV16;
}

const nativeBudgets = Object.freeze({
  wallTimeMs: 300_000,
  candidateCount: 32,
  memoryMiB: 1024,
  concurrency: 1,
});
const nativeGoals = Object.freeze({ functionPercent: 100, linePercent: 100, branchPercent: 100 });

function exactManagedContext(context: NativeManagedGenerationContext): void {
  for (const [label, value] of Object.entries({
    projectId: context.projectId,
    workspaceGeneration: context.workspaceGeneration,
    coverageReportId: context.coverageReportId,
    fileId: context.fileId,
    functionId: context.functionId,
  })) {
    if (typeof value !== "string" || value.length === 0 || value.length > 128 || value.includes("\0")) {
      throw new Error(`native managed generation ${label} is invalid`);
    }
  }
  if (context.framework !== "cpputest" && context.framework !== "unity") {
    throw new Error("native managed generation framework is invalid");
  }
}

function coverageImproved(baseline: CoverageSummaryV16, final: CoverageSummaryV16): boolean {
  let improved = false;
  for (const name of ["functions", "lines", "branches"] as const) {
    if (final[name].total !== baseline[name].total || final[name].covered < baseline[name].covered) return false;
    if (final[name].covered > baseline[name].covered) improved = true;
  }
  return improved;
}

export async function executeNativeManagedGeneration(
  context: NativeManagedGenerationContext,
): Promise<NativeManagedGenerationReceipt> {
  exactManagedContext(context);
  const maxPollAttempts = context.maxPollAttempts ?? 600;
  const pollIntervalMs = context.pollIntervalMs ?? 250;
  if (!Number.isSafeInteger(maxPollAttempts) || maxPollAttempts < 1 || maxPollAttempts > 600 ||
      !Number.isSafeInteger(pollIntervalMs) || pollIntervalMs < 0 || pollIntervalMs > 10_000) {
    throw new Error("native managed generation polling budget is invalid");
  }

  const capabilities = await context.client.getCapabilities();
  if (!("coverageDetails" in capabilities) || capabilities.coverageDetails !== true ||
      !("testGeneration" in capabilities) || capabilities.testGeneration !== true ||
      !("managedTests" in capabilities) || capabilities.managedTests !== true) {
    throw new Error("native managed generation capability is unavailable");
  }
  const binding = {
    workspaceGeneration: context.workspaceGeneration,
    coverageReportId: context.coverageReportId,
  };
  const project = await context.client.getCoverageProject({ ...binding, projectId: context.projectId });
  if (project.status !== "current") throw new Error("native managed generation coverage project is not current");
  const files = await context.client.listCoverageFiles({ ...binding, projectId: context.projectId, limit: 200 });
  const file = files.items.find((item) => item.fileId === context.fileId);
  if (files.nextCursor !== undefined || file?.status !== "current") {
    throw new Error("native managed generation file binding is unavailable");
  }
  const functions = await context.client.listCoverageFunctions({ ...binding, fileId: context.fileId, limit: 200 });
  const fn = functions.items.find((item) => item.functionId === context.functionId);
  if (functions.nextCursor !== undefined || fn?.status !== "current" || fn.fileId !== context.fileId) {
    throw new Error("native managed generation function binding is unavailable");
  }

  const request: TestGenerationStartRequestV16 = {
    idempotencyKey: randomBytes(16).toString("hex"),
    projectId: context.projectId,
    workspaceGeneration: context.workspaceGeneration,
    coverageReportId: context.coverageReportId,
    scope: TestGenerationScopeV16.Symbol,
    functionId: context.functionId,
    framework: context.framework === "cpputest" ? TestGenerationFrameworkV16.Cpputest : TestGenerationFrameworkV16.Unity,
    goals: nativeGoals,
    budgets: nativeBudgets,
  };
  const started = await context.client.startTestGeneration(request) as TestGenerationRunV16;
  let run: TestGenerationRunV16 = started;
  for (let attempt = 0; run.state !== "awaiting_confirmation"; attempt++) {
    if (["accepted", "cancelled", "failed", "rejected"].includes(run.state)) {
      throw new Error(`native managed generation terminated before review: ${run.state}`);
    }
    if (attempt >= maxPollAttempts - 1) throw new Error("native managed generation timed out before review");
    if (pollIntervalMs > 0) {
      await (context.sleep?.(pollIntervalMs) ?? new Promise<void>((resolveDelay) => setTimeout(resolveDelay, pollIntervalMs)));
    }
    run = await context.client.getTestGenerationRun(started.runId) as unknown as TestGenerationRunV16;
  }
  if (run.projectId !== context.projectId || run.workspaceGeneration !== context.workspaceGeneration) {
    throw new Error("native managed generation run binding changed");
  }

  const review = await context.client.getManagedReview({ reviewId: run.runId, limit: 200 });
  if (review.reviewId !== run.runId || review.workspaceGeneration !== context.workspaceGeneration ||
      review.coverageReportId !== context.coverageReportId || review.nextCursor !== undefined || review.cases.length === 0) {
    throw new Error("native managed generation review binding is invalid");
  }
  const applied = await context.client.applyManagedReview({
    reviewId: review.reviewId,
    reviewDigest: review.reviewDigest,
    resolutions: review.cases.map(({ caseId }) => ({ caseId, choice: ManagedConflictChoiceV16.UseGenerated })),
  });
  if (!applied.applied || applied.reviewId !== review.reviewId || applied.reviewDigest !== review.reviewDigest) {
    throw new Error("native managed generation review was not applied");
  }

  const refreshed = await context.refreshCoverage();
  exactManagedContext({ ...context, ...refreshed });
  const refreshedBinding = {
    workspaceGeneration: refreshed.workspaceGeneration,
    coverageReportId: refreshed.coverageReportId,
  };
  const finalProject = await context.client.getCoverageProject({ ...refreshedBinding, projectId: context.projectId });
  const finalFiles = await context.client.listCoverageFiles({ ...refreshedBinding, projectId: context.projectId, limit: 200 });
  const finalFile = finalFiles.items.find((item) => item.fileId === refreshed.fileId);
  const finalFunctions = await context.client.listCoverageFunctions({ ...refreshedBinding, fileId: refreshed.fileId, limit: 200 });
  const finalFunction = finalFunctions.items.find((item) => item.functionId === refreshed.functionId);
  if (finalProject.status !== "current" || finalFiles.nextCursor !== undefined || finalFile?.status !== "current" ||
      finalFunctions.nextCursor !== undefined || finalFunction?.status !== "current" ||
      !coverageImproved(project.summary, finalProject.summary) || !coverageImproved(file.summary, finalFile.summary) ||
      !coverageImproved(fn.summary, finalFunction.summary)) {
    throw new Error("native managed generation did not improve project/file/function coverage");
  }
  const records = await context.client.listManagedTests({
    ...refreshedBinding,
    projectId: context.projectId,
    fileId: refreshed.fileId,
    status: "current",
    limit: 200,
  });
  const caseIDs = new Set(review.cases.map(({ caseId }) => caseId));
  if (records.nextCursor !== undefined || records.items.length < review.cases.length || records.items.some((item) =>
    !caseIDs.has(item.caseId) || item.fileId !== refreshed.fileId || item.functionId !== refreshed.functionId || item.status !== "current")) {
    throw new Error("native managed generation records do not close the accepted review");
  }
  return Object.freeze({
    runId: run.runId,
    reviewId: review.reviewId,
    reviewDigest: review.reviewDigest,
    caseCount: review.cases.length,
    managedRecordCount: records.items.length,
    baseline: project.summary,
    final: finalProject.summary,
  });
}

/**
 * Returns the complete scenario plan in stable order. The plan is deliberately
 * independent of a service binary so local report tests cannot silently skip a
 * required case when hosted native providers are unavailable.
 */
export function createTestGenerationScenarioPlan(): readonly TestGenerationScenarioId[] {
  return [...TEST_GENERATION_SCENARIO_IDS];
}

export async function runTestGenerationScenarioPlan(
  execute: TestGenerationScenarioExecutor,
): Promise<readonly TestGenerationScenarioResult[]> {
  const results: TestGenerationScenarioResult[] = [];
  for (const scenario of TEST_GENERATION_SCENARIO_IDS) {
    const result = await execute(scenario);
    if (result.id !== scenario) throw new Error("test-generation scenario result ID is out of order");
    if (result.status === "blocked" && result.mode === "native") {
      throw new Error("native test-generation scenario cannot be blocked without an external gate");
    }
    results.push(Object.freeze({ ...result }));
  }
  return Object.freeze(results);
}

export async function runNativeTestGenerationScenarioPlan(
  execute: TestGenerationScenarioExecutor,
): Promise<readonly TestGenerationScenarioResult[]> {
  const results = await runTestGenerationScenarioPlan(execute);
  for (const result of results) {
    if (result.mode !== "native" || result.status !== "passed") {
      throw new Error(`native test-generation scenario did not pass natively: ${result.id}`);
    }
  }
  return results;
}
