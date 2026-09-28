import assert from "node:assert/strict";
import test from "node:test";
import { CoverageDetailTree } from "../src/coverage-detail-tree.js";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";
import { CoverageDetailStatusV16, CoverageDetailReasonV16, type CoverageProjectV16, type CoverageFileV16, type CoverageFunctionV16 } from "@unit-test-ide/test-client";

const summary = { functions: { covered: 1, total: 2, coveredDelta: -1 }, lines: { covered: 3, total: 4, coveredDelta: 1 }, branches: { covered: 1, total: 2, coveredDelta: 0 } };
const project: CoverageProjectV16 = { coverageReportId: "report", projectId: "project", workspaceGeneration: "workspace", fileCount: 1, status: CoverageDetailStatusV16.Current, reasons: [], summary };
const file: CoverageFileV16 = { fileId: "f".repeat(32), relativePath: "src/math.cpp", sourceSha256: "a".repeat(64), functionCount: 1, status: CoverageDetailStatusV16.Incomplete, reasons: [CoverageDetailReasonV16.AttributionAmbiguous], summary };
const fn: CoverageFunctionV16 = { functionId: "b".repeat(32), fileId: file.fileId, qualifiedName: "math::add", startLine: 3, endLine: 8, status: CoverageDetailStatusV16.Current, reasons: [], summary };

test("tree expands lazily, labels metrics, filters and paginates", async () => {
  const calls: string[] = [];
  const client = {
    getCapabilities: async () => ({ coverageDetails: true, maxCoverageDetailPageSize: 2, maxCoverageLinePageSize: 2 }),
    listCoverageLines: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    getCoverageProject: async () => { calls.push("project"); return project; },
    listCoverageFiles: async ({ cursor, limit }: { cursor?: string; limit?: number }) => { calls.push(`files:${cursor ?? ""}:${limit}`); return { coverageReportId: "report", workspaceGeneration: "workspace", items: cursor ? [] : [file], ...(cursor ? {} : { nextCursor: "next" }) }; },
    listCoverageFunctions: async () => { calls.push("functions"); return { coverageReportId: "report", workspaceGeneration: "workspace", items: [fn] }; }
  };
  const tree = new CoverageDetailTree(() => ({ client: client as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "project", reportId: "report" }));
  assert.equal(await tree.refresh(), true);
  assert.deepEqual(calls, ["project"]);
  const roots = await tree.children();
  assert.equal(roots.length, 1);
  assert.match(roots[0]!.label, /1\/2.*50%.*-1/);
  const files = await tree.children(roots[0]);
  assert.equal(files.length, 2);
  assert.equal(files[0]!.status, "incomplete");
  assert.equal(files[1]!.kind, "load-more");
  const functionNode = (await tree.children(files[0]))[0]!;
  assert.equal(functionNode.label.includes("math::add"), true);
  assert.equal(functionNode.startLine, 3);
  assert.equal(tree.fileIdFor(functionNode), file.fileId);
  assert.deepEqual(calls, ["project", "files::2", "functions"]);
  tree.setFilter("incomplete");
  assert.equal((await tree.children(roots[0]))[0]!.kind, "file");
  tree.setFilter("uncovered");
  assert.equal((await tree.children(roots[0]))[0]!.kind, "file");
  tree.setFilter("regressed");
  assert.equal((await tree.children(roots[0]))[0]!.kind, "file");
  await tree.children(files[1]);
  assert.deepEqual(calls, ["project", "files::2", "functions", "files:next:2"]);
  await tree.children(roots[0]);
  assert.equal(calls.length, 4);
});

test("tree hides itself without v1.6 negotiation and cancels stale refresh", async () => {
  let resolve!: (value: CoverageProjectV16) => void;
  let reportId = "report";
  const client = {
    getCapabilities: async () => ({ coverageDetails: true, maxCoverageDetailPageSize: 2, maxCoverageLinePageSize: 2 }),
    listCoverageLines: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    getCoverageProject: async () => new Promise<CoverageProjectV16>((done) => { resolve = done; }),
    listCoverageFiles: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    listCoverageFunctions: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] })
  };
  const tree = new CoverageDetailTree(() => ({ client: client as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "project", reportId }));
  const pending = tree.refresh();
  await Promise.resolve();
  reportId = "different";
  tree.invalidate();
  resolve(project);
  assert.equal(await pending, false);
  assert.deepEqual(await tree.children(), []);
  const unavailable = new CoverageDetailTree(() => ({ client: { ...client, getCapabilities: async () => ({ coverageDetails: false }) } as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "project", reportId: "report" }));
  assert.equal(await unavailable.refresh(), false);
  assert.deepEqual(await unavailable.children(), []);
});

test("tree bounds a hostile project label independently of metric suffixes", async () => {
  const client = {
    getCapabilities: async () => ({ coverageDetails: true, maxCoverageDetailPageSize: 1, maxCoverageLinePageSize: 1 }),
    getCoverageProject: async () => ({ ...project, projectId: "x".repeat(1000) }),
    listCoverageFiles: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    listCoverageFunctions: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    listCoverageLines: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] })
  };
  const tree = new CoverageDetailTree(() => ({ client: client as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "x".repeat(1000), reportId: "report" }));
  assert.equal(await tree.refresh(), true);
  assert.ok((await tree.children())[0]!.label.length <= 256);
});

test("concurrent expansion shares one bounded service page", async () => {
  const releases: Array<(value: { coverageReportId: string; workspaceGeneration: string; items: CoverageFileV16[] }) => void> = [];
  let calls = 0;
  const client = {
    getCapabilities: async () => ({ coverageDetails: true, maxCoverageDetailPageSize: 1, maxCoverageLinePageSize: 1 }),
    getCoverageProject: async () => project,
    listCoverageFiles: async () => { calls++; return new Promise<{ coverageReportId: string; workspaceGeneration: string; items: CoverageFileV16[] }>((resolve) => { releases.push(resolve); }); },
    listCoverageFunctions: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    listCoverageLines: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] })
  };
  const tree = new CoverageDetailTree(() => ({ client: client as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "project", reportId: "report" }));
  await tree.refresh();
  const root = (await tree.children())[0]!;
  const first = tree.children(root);
  const second = tree.children(root);
  for (const release of releases) release({ coverageReportId: "report", workspaceGeneration: "workspace", items: [file] });
  const results = await Promise.allSettled([first, second]);
  assert.deepEqual(results.map((item) => item.status), ["fulfilled", "fulfilled"]);
  assert.equal(calls, 1);
});

test("load-more resolves empty after a report invalidates mid-page", async () => {
  let resolvePage!: (value: { coverageReportId: string; workspaceGeneration: string; items: CoverageFileV16[] }) => void;
  const client = {
    getCapabilities: async () => ({ coverageDetails: true, maxCoverageDetailPageSize: 1, maxCoverageLinePageSize: 1 }),
    getCoverageProject: async () => project,
    listCoverageFiles: async ({ cursor }: { cursor?: string }) => cursor
      ? new Promise<{ coverageReportId: string; workspaceGeneration: string; items: CoverageFileV16[] }>((resolve) => { resolvePage = resolve; })
      : { coverageReportId: "report", workspaceGeneration: "workspace", items: [file], nextCursor: "more" },
    listCoverageFunctions: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] }),
    listCoverageLines: async () => ({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] })
  };
  const tree = new CoverageDetailTree(() => ({ client: client as unknown as ExtensionProtocolClient, workspaceGeneration: "workspace", projectId: "project", reportId: "report" }));
  await tree.refresh();
  const root = (await tree.children())[0]!;
  const more = (await tree.children(root))[1]!;
  const pending = tree.children(more);
  tree.invalidate();
  resolvePage({ coverageReportId: "report", workspaceGeneration: "workspace", items: [] });
  assert.deepEqual(await pending, []);
});
