import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { buildToolchainReport } from "./test-generation-report.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");
const commit = "b".repeat(40);

function report(mutation) {
  return {
    schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", producerReceiptSha256: null, platform: "linux", toolchainFamily: "gcc", framework: "cpputest",
    compiler: { family: "gcc", version: "14.2.0", sha256: digest("gcc") },
    frameworkIdentity: { version: "4.0", sha256: digest("cpputest"), treeSha256: digest("cpputest-tree") },
    cmake: { version: "3.30.0", sha256: digest("cmake") },
    coverage: { baseline: { functions: { covered: 1, total: 3 }, lines: { covered: 1, total: 3 }, branches: { covered: 1, total: 3 } }, final: { functions: { covered: 2, total: 3 }, lines: { covered: 2, total: 3 }, branches: { covered: 2, total: 3 } } },
    candidates: { generated: 1, retained: 1, rejected: 0 },
    tests: [{ id: "mutation-killer", candidateKind: "verified", assertionCount: 1, sourceArtifactSha256: digest("source"), outputArtifactSha256: digest("output") }],
    faultScenarios: ["cancel-before-build", "compile-failure", "service-restart"].map((id) => ({ id, status: "passed", artifactSha256: digest(id) })),
    inputArtifactSha256: digest("input"), outputArtifactSha256: digest("result"),
    performance: { durationMs: 10, budgetMs: 100, peakMemoryBytes: 10, memoryBudgetBytes: 100 }, mutation,
  };
}

test("mutation gate requires the declared retained suite to kill enough mutations", () => {
  assert.doesNotThrow(() => buildToolchainReport(report({ total: 4, killed: 4, requiredKilled: 3 })));
  assert.throws(() => buildToolchainReport(report({ total: 4, killed: 2, requiredKilled: 3 })), /mutation gate/u);
  assert.throws(() => buildToolchainReport(report({ total: 4, killed: 5, requiredKilled: 3 })), /mutation gate/u);
});
