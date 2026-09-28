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

/**
 * Native execution is intentionally fail-closed in this local checkout. Task
 * 15 proves the contract and fixtures here; the real provider and hosted
 * four-toolchain matrix remain an explicit external gate.
 */
export async function runNativeTestGenerationScenarioPlan(): Promise<never> {
  throw new Error("native test-generation provider is an external Task 15 gate; no local hosted evidence is claimed");
}
