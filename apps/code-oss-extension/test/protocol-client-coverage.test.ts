import assert from "node:assert/strict";
import test from "node:test";
import type { ExtensionCoverageProtocolClient } from "../src/protocol-client.js";
import { TestSelectionModeV14 } from "@unit-test-ide/test-client";

const id = "0123456789abcdef0123456789abcdef";

test("extension protocol facade exposes typed coverage and artifact operations", async () => {
  const calls: string[] = [];
  const client = {
    startCoverage: async () => { calls.push("startCoverage"); return undefined; },
    getCoverageRun: async () => { calls.push("getCoverageRun"); return undefined; },
    listCoverageRuns: async () => { calls.push("listCoverageRuns"); return { items: [] }; },
    getCoverageReport: async () => { calls.push("getCoverageReport"); return undefined; },
    listArtifacts: async () => { calls.push("listArtifacts"); return { items: [] }; },
    readArtifact: async () => { calls.push("readArtifact"); return new Uint8Array([60, 104, 116, 109, 108, 62]); }
  } as unknown as ExtensionCoverageProtocolClient;

  await client.startCoverage({
    idempotencyKey: "coverage-test-1",
    workspaceGeneration: "workspace-generation-1",
    projectId: "project-1",
    coverageProfileId: "coverage-debug",
    catalogRevision: "catalog-1",
    selection: { mode: TestSelectionModeV14.All },
    repeatCount: 1,
    timeoutMs: 10_000
  });
  await client.getCoverageRun(id);
  await client.listCoverageRuns({});
  await client.getCoverageReport(id);
  await client.listArtifacts(id, {});
  const bytes = await client.readArtifact(id);

  assert.deepEqual(calls, [
    "startCoverage",
    "getCoverageRun",
    "listCoverageRuns",
    "getCoverageReport",
    "listArtifacts",
    "readArtifact"
  ]);
  assert.deepEqual([...bytes], [60, 104, 116, 109, 108, 62]);
});
