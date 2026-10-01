import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { buildToolchainReport, validateManagedCoveragePerformance } from "./test-generation-report.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");
const commit = "c".repeat(40);

function report(performance) {
  return {
    schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", producerReceiptSha256: null, platform: "win32", toolchainFamily: "clang-cl", framework: "unity",
    compiler: { family: "clang-cl", version: "19.1.0", sha256: digest("clang-cl") },
    frameworkIdentity: { version: "2.6.1", sha256: digest("unity"), treeSha256: digest("unity-tree") },
    cmake: { version: "3.30.0", sha256: digest("cmake") },
    coverage: { baseline: { functions: { covered: 1, total: 3 }, lines: { covered: 1, total: 3 }, branches: { covered: 1, total: 3 } }, final: { functions: { covered: 2, total: 3 }, lines: { covered: 2, total: 3 }, branches: { covered: 2, total: 3 } } },
    candidates: { generated: 1, retained: 1, rejected: 0 },
    tests: [{ id: "bounded-test", candidateKind: "verified", assertionCount: 1, sourceArtifactSha256: digest("source"), outputArtifactSha256: digest("output") }],
    faultScenarios: ["cancel-before-build", "compile-failure", "service-restart"].map((id) => ({ id, status: "passed", artifactSha256: digest(id) })),
    inputArtifactSha256: digest("input"), outputArtifactSha256: digest("result"),
    performance, mutation: { total: 1, killed: 1, requiredKilled: 1 },
  };
}

test("performance evidence is deterministic and must stay within both budgets", () => {
  assert.doesNotThrow(() => buildToolchainReport(report({ durationMs: 1000, budgetMs: 2000, peakMemoryBytes: 4096, memoryBudgetBytes: 8192 })));
  assert.throws(() => buildToolchainReport(report({ durationMs: 2001, budgetMs: 2000, peakMemoryBytes: 4096, memoryBudgetBytes: 8192 })), /performance budget/u);
  assert.throws(() => buildToolchainReport(report({ durationMs: 1000, budgetMs: 2000, peakMemoryBytes: 8193, memoryBudgetBytes: 8192 })), /performance budget/u);
});

test("managed coverage pagination performance enforces each measured numeric budget", () => {
  const metrics = {
    serviceIndexMs: { measured: 400, budget: 1000 },
    servicePeakMemoryBytes: { measured: 32_000_000, budget: 64_000_000 },
    firstTreePageMs: { measured: 40, budget: 100 },
    functionPageMs: { measured: 30, budget: 100 },
    lineDetailMs: { measured: 50, budget: 150 },
    extensionHeapBytes: { measured: 16_000_000, budget: 32_000_000 },
    extensionDecorations: { measured: 800, budget: 1000 },
  };
  assert.deepEqual(validateManagedCoveragePerformance(metrics), metrics);
  for (const key of Object.keys(metrics)) {
    assert.throws(() => validateManagedCoveragePerformance({ ...metrics, [key]: { ...metrics[key], measured: metrics[key].budget + 1 } }), /performance.*budget/u, key);
  }
  assert.throws(() => validateManagedCoveragePerformance({ ...metrics, functionPageMs: { measured: null, budget: 100 } }), /invalid/u);
  assert.throws(() => validateManagedCoveragePerformance({ ...metrics, missingMetric: { measured: 1, budget: 2 } }), /closed/u);
});
