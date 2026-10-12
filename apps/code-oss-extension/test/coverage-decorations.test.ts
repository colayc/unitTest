import assert from "node:assert/strict";
import test from "node:test";
import { CoverageDecorations, lineDecorations } from "../src/coverage-decorations.js";
import type { CoverageLineListInputV16, CoverageLinePageV16 } from "@unit-test-ide/test-client";

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

test("unavailable line history preserves current branches, hits and uncovered styling", () => {
  const items = [
    { line: 1, count: 1, baselineCount: null, branchesCovered: 3, branchesTotal: 4, baselineBranchesCovered: null, baselineBranchesTotal: null },
    { line: 9, count: 0, baselineCount: null, branchesCovered: 0, branchesTotal: 0, baselineBranchesCovered: null, baselineBranchesTotal: null }
  ];
  assert.deepEqual(lineDecorations(items, "current"), [
    { line: 1, style: "covered", label: "3/4 branches" }, { line: 9, style: "uncovered", label: "0 hits" }
  ]);
  assert.equal(lineDecorations([{ ...items[0]!, branchesCovered: 0, branchesTotal: 0 }], "current")[0]!.label, "1 hits");
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

test("function decorations request only that function's lines before styling them", async () => {
  const requests: unknown[] = [];
  const applied: unknown[][] = [];
  const client = {
    listCoverageLines: async (input: CoverageLineListInputV16): Promise<CoverageLinePageV16> => {
      requests.push(input);
      return { coverageReportId: "r1", workspaceGeneration: "w", items: [{ line: 7, count: 1, baselineCount: 0, branchesCovered: 0, branchesTotal: 0, baselineBranchesCovered: 0, baselineBranchesTotal: 0 }] };
    }
  };
  const decorations = new CoverageDecorations(() => ({ reportId: "r1", workspaceGeneration: "w", client }), (items) => { applied.push([...items]); });
  await decorations.load("f".repeat(32), "incomplete", "a".repeat(32));
  assert.deepEqual(requests, [{ coverageReportId: "r1", workspaceGeneration: "w", functionId: "a".repeat(32), limit: 200 }]);
  assert.deepEqual(applied.map((items) => items.map((item) => (item as { style: string }).style)), [["incomplete"]]);
});

test("null-history decorations clear after report, source or generation changes", async () => {
  for (const change of ["report", "source", "generation"]) {
    let reportId = "r1", workspaceGeneration = "w1";
    let resolve!: (page: CoverageLinePageV16) => void;
    const applied: unknown[][] = [];
    const page: CoverageLinePageV16 = {
      coverageReportId: "r1", workspaceGeneration: "w1", items: [
        { line: 1, count: 1, baselineCount: null, branchesCovered: 3, branchesTotal: 4, baselineBranchesCovered: null, baselineBranchesTotal: null }
      ]
    };
    let pending = false;
    const client = { listCoverageLines: async (): Promise<CoverageLinePageV16> => pending
      ? new Promise((done) => { resolve = done; }) : page };
    const decorations = new CoverageDecorations(() => ({ reportId, workspaceGeneration, client }), (items) => applied.push([...items]));
    await decorations.load("f".repeat(32), "current");
    assert.deepEqual(applied[0], [{ line: 1, style: "covered", label: "3/4 branches" }]);
    pending = true;
    const loading = decorations.load("f".repeat(32), "current");
    if (change === "report") reportId = "r2";
    if (change === "generation") workspaceGeneration = "w2";
    // Source changes invalidate the binding through the existing controller's clear path.
    decorations.clear();
    resolve(page);
    await loading;
    assert.equal(applied.length, 2, `${change} reapplied obsolete evidence`);
    assert.deepEqual(applied.at(-1), []);
  }
});
