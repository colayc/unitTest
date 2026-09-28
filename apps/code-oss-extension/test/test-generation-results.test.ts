import assert from "node:assert/strict";
import test from "node:test";
import { buildGenerationResults, candidateResult, renderGenerationResults, renderManagedRecords } from "../src/test-generation-results.js";
import type { TestGenerationCandidatePageV15, TestGenerationRunV15 } from "@unit-test-ide/test-client";

const id = "a".repeat(32);
const candidatePage: TestGenerationCandidatePageV15 = {
  items: [{
    candidateId: "candidate-1", artifactDigest: "b".repeat(64), codeDigest: "c".repeat(64), kind: "verified",
    assertionProvenance: { kind: "independent-oracle", evidenceDigest: "d".repeat(64) },
    baselineCoverage: { functionPercent: 20, linePercent: 30, branchPercent: 40 },
    deltaCoverage: { functionPercent: 10, linePercent: 5, branchPercent: 8 },
    characterizationConfirmed: false,
    diagnostics: [{ code: "COVERAGE_GAP", severity: "info", reason: "uncovered-branch" }],
    plannedEdits: [{ operation: "create", path: "tests/generated.cpp", afterDigest: "e".repeat(64) }]
  }] as any
};
const run: TestGenerationRunV15 = { runId: id, taskId: "f".repeat(32), projectId: "core", workspaceGeneration: "1".repeat(64), state: "awaiting_confirmation", createdAt: new Date(0), lastSequence: 1 } as TestGenerationRunV15;

test("results expose verified labels, assertion provenance, and baseline/after/delta coverage", () => {
  const result = candidateResult(candidatePage.items[0]!);
  assert.equal(result.label, "Verified");
  assert.equal(result.assertionProvenance, "independent-oracle");
  assert.deepEqual(result.after, { functionPercent: 30, linePercent: 35, branchPercent: 48 });
  const model = buildGenerationResults(run, candidatePage);
  assert.match(renderGenerationResults(model), /20\.00% → 30\.00%/);
  assert.match(renderGenerationResults(model), /COVERAGE_GAP/);
});

test("results redact absolute and traversal paths", () => {
  const page = structuredClone(candidatePage);
  page.items[0]!.plannedEdits[0]!.path = "C:\\private\\source\\generated.cpp";
  assert.equal(candidateResult(page.items[0]!).plannedEdits[0], "create: <workspace-path>");
  page.items[0]!.plannedEdits[0]!.path = "../outside.cpp";
  assert.equal(candidateResult(page.items[0]!).plannedEdits[0], "create: <workspace-path>");
});

test("managed maintenance list labels all five states without treating orphaned as a delete", () => {
  const statuses = ["current", "stale", "conflicted", "orphaned", "invalid"];
  const rendered = renderManagedRecords(statuses.map((status, index) => ({ caseId: `utc_${String(index).repeat(32)}`, status, fileId: "a".repeat(32), functionId: "b".repeat(32), acceptedDigest: "c".repeat(64), currentDigest: "d".repeat(64) })) as any);
  for (const status of statuses) assert.match(rendered, new RegExp(`\\[${status}\\]`));
  assert.doesNotMatch(rendered, /delete|remove/i);
});
