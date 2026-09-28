import assert from "node:assert/strict";
import test from "node:test";
import { CoverageDecorations, lineDecorations } from "../src/coverage-decorations.js";
import type { CoverageLinePageV16 } from "@unit-test-ide/test-client";

test("line decorations show hits, branches, stale styling and a hard display bound", () => {
  const items = [
    { line: 3, count: 2, baselineCount: 1, branchesCovered: 2, branchesTotal: 4, baselineBranchesCovered: 1, baselineBranchesTotal: 4 },
    { line: 4, count: 0, baselineCount: 1, branchesCovered: 0, branchesTotal: 0, baselineBranchesCovered: 0, baselineBranchesTotal: 0 }
  ];
  const current = lineDecorations(items, "current", 1);
  assert.equal(current.length, 1);
  assert.equal(current[0]!.line, 3);
  assert.equal(current[0]!.label, "2/4 branches");
  assert.equal(current[0]!.style, "covered");
  assert.equal(lineDecorations(items, "stale", 10)[0]!.style, "stale");
  assert.equal(lineDecorations(items, "incomplete", 10)[0]!.style, "incomplete");
  assert.equal(lineDecorations(items, "current", 10)[1]!.style, "uncovered");
});

test("decorations clear on report change and never apply an obsolete page", async () => {
  let resolve!: (value: CoverageLinePageV16) => void;
  let reportId = "r1";
  const applied: unknown[][] = [];
  const decorations = new CoverageDecorations(() => ({ reportId, workspaceGeneration: "w", limit: 2, client: {
    listCoverageLines: async ({ limit }) => { assert.equal(limit, 2); return new Promise((done) => { resolve = done; }); }
  } }), (items: readonly unknown[]) => { applied.push([...items]); });
  const pending = decorations.load("f".repeat(32), "current");
  await Promise.resolve();
  reportId = "r2";
  decorations.clear();
  resolve({ coverageReportId: "r1", workspaceGeneration: "w", items: [{ line: 1, count: 1, baselineCount: 0, branchesCovered: 0, branchesTotal: 0, baselineBranchesCovered: 0, baselineBranchesTotal: 0 }] });
  await pending;
  assert.deepEqual(applied, [[]]);
});
