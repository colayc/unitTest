import assert from "node:assert/strict";
import test from "node:test";
import type { CoverageReport, CoverageRun } from "@unit-test-ide/test-client";
import {
  createCoverageController,
  type CoverageContext
} from "../src/coverage-controller.js";
import type { ExtensionCoverageProtocolClient } from "../src/protocol-client.js";

const id = "0123456789abcdef0123456789abcdef";

function runFixture(overrides: Partial<CoverageRun> = {}): CoverageRun {
  return {
    coverageRunId: id,
    taskId: id,
    testRunId: id,
    workspaceGeneration: "workspace-1",
    projectId: "project-1",
    coverageProfileId: "coverage-debug",
    catalogRevision: "catalog-1",
    selectionSnapshot: { mode: "all" },
    repeatCount: 1,
    timeoutMs: 10_000,
    status: "finished",
    createdAt: new Date(0),
    finishedAt: new Date(1),
    reportId: id,
    lastSequence: 1,
    ...overrides
  } as CoverageRun;
}

function reportFixture(overrides: Partial<CoverageReport> = {}): CoverageReport {
  return {
    reportId: id,
    coverageRunId: id,
    testRunId: id,
    schemaVersion: "1.0",
    createdAt: new Date(1),
    completeness: { outcome: "available", reasons: [] },
    summary: {
      lines: { covered: 8, total: 10 },
      branches: { covered: 4, total: 5 },
      functions: { covered: 3, total: 4 }
    },
    toolProvenance: {
      platform: "windows",
      architecture: "x64",
      compiler: { family: "clang-cl", version: "19.0" },
      driver: { name: "llvm-cov", version: "19.0" },
      collector: { name: "llvm-cov", version: "19.0" },
      normalizerVersion: "1",
      instrumentationFingerprint: "fingerprint"
    },
    artifactId: id,
    ...overrides
  } as CoverageReport;
}

function context(
  client: ExtensionCoverageProtocolClient | undefined,
  overrides: Partial<CoverageContext> = {}
): CoverageContext {
  return {
    trust: "trusted",
    client: client as CoverageContext["client"],
    serviceRunning: true,
    workspaceGeneration: "workspace-1",
    catalog: { projectId: "project-1", profileId: "profile-1", revision: "catalog-1", workspaceGeneration: "workspace-1" },
    coverageProfileId: "coverage-debug",
    ...overrides
  };
}

function clientFixture(
  run: CoverageRun = runFixture(),
  report: CoverageReport = reportFixture()
): ExtensionCoverageProtocolClient {
  return {
    startCoverage: async () => run,
    getCoverageRun: async () => run,
    getCoverageReport: async () => report,
    listCoverageRuns: async () => ({ items: [] }),
    listArtifacts: async () => ({ items: [] }),
    readArtifact: async () => new Uint8Array()
  };
}

test("coverage start rejects untrusted and stale catalogs before RPC", async () => {
  let calls = 0;
  const client = clientFixture();
  const controller = createCoverageController({
    readContext: () => context(client, { trust: "blocked-untrusted" })
  });
  await assert.rejects(() => controller.start({ catalogRevision: "catalog-1" }), /trusted workspace/);

  const stale = createCoverageController({
    readContext: () => context({
      ...client,
      startCoverage: async () => { calls++; return runFixture(); }
    }, { catalog: { projectId: "project-1", profileId: "profile-1", revision: "catalog-2", workspaceGeneration: "workspace-1" } })
  });
  await assert.rejects(() => stale.start({ catalogRevision: "catalog-1" }), /current catalog revision/);
  assert.equal(calls, 0);
});

test("coverage report state is published only after matching run and report identities", async () => {
  const client = clientFixture();
  const controller = createCoverageController({
    readContext: () => context(client),
    sleep: async () => undefined
  });
  const state = await controller.start({ catalogRevision: "catalog-1" });
  assert.equal(state.state, "available");
  assert.equal(state.coverageRunId, id);
  assert.equal(state.reportId, id);
  assert.equal(state.summary?.lines.total, 10);
  assert.equal(state.toolProvenance?.driver.name, "llvm-cov");
});

test("coverage operation fails closed when the session changes after an RPC", async () => {
  const client = clientFixture();
  let current: CoverageContext = context(client);
  const controller = createCoverageController({ readContext: () => current });
  const operation = controller.start({ catalogRevision: "catalog-1" });
  current = context(undefined);
  await assert.rejects(operation, /Service is not running/);
  assert.equal(controller.getState().state, "unavailable");
});

test("coverage report identity mismatch is rejected and not published", async () => {
  const client = clientFixture(runFixture(), reportFixture({ testRunId: "fedcba9876543210fedcba9876543210" }));
  const controller = createCoverageController({ readContext: () => context(client) });
  await assert.rejects(() => controller.start({ catalogRevision: "catalog-1" }), /identity/);
  assert.equal(controller.getState().state, "unavailable");
  assert.equal(controller.getState().reportId, undefined);
});
