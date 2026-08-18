import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import test from "node:test";
import type {
  CatalogGetInput,
  ProtocolTestCatalog,
  ProtocolTestRun,
  TestDiscoveryInput,
  TestRunInput,
  WorkspaceSnapshot
} from "@unit-test-ide/test-client";
import { EventSubscription } from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";
import {
  TestingApiAdapter,
  type TestingApiHost,
  type TestingController,
  type TestingTestItem,
  type TestingTestItemCollection
} from "../src/testing-api.js";

const ITEM_COUNT = 10_000;
const REVISION = "benchmark-r1";

interface BenchmarkMutationCounts {
  create: number;
  add: number;
  delete: number;
  replace: number;
}

class BenchmarkCollection implements TestingTestItemCollection {
  readonly entries = new Map<string, TestingTestItem>();

  constructor(private readonly mutationCounts: BenchmarkMutationCounts) {}

  add(item: TestingTestItem): void {
    this.mutationCounts.add++;
    this.entries.set(item.id, item);
  }
  delete(id: string): void {
    this.mutationCounts.delete++;
    this.entries.delete(id);
  }
  get(id: string): TestingTestItem | undefined { return this.entries.get(id); }
  replace(items: readonly TestingTestItem[]): void {
    this.mutationCounts.replace++;
    this.entries.clear();
    for (const item of items) this.entries.set(item.id, item);
  }
}

function benchmarkCatalog(): ProtocolTestCatalog {
  return {
    projectId: "benchmark-project",
    profileId: "benchmark-profile",
    revision: REVISION,
    generatedAt: new Date("2026-08-18T00:00:00.000Z"),
    partial: false,
    containers: [{
      id: "benchmark-container",
      projectId: "benchmark-project",
      displayName: "Benchmark container",
      ctestLogicalName: "benchmark",
      framework: "ctest",
      disabled: false,
      labels: [],
      capabilities: {
        canDiscoverCases: true,
        canReportMockDetails: false,
        canReportSkipped: true,
        canReportSourceLocation: false,
        canRunCase: true
      }
    }],
    items: Array.from({ length: ITEM_COUNT }, (_, index) => ({
      id: `benchmark-item-${index.toString().padStart(5, "0")}`,
      containerId: "benchmark-container",
      displayName: `Benchmark item ${index}`,
      logicalName: `benchmark.${index}`,
      framework: "ctest",
      kind: "case",
      disabled: false,
      labels: []
    })),
    diagnostics: []
  } as unknown as ProtocolTestCatalog;
}

class BenchmarkClient implements ExtensionProtocolClient {
  readonly catalog = benchmarkCatalog();

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    return {
      capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
      diagnostics: [],
      projects: [{
        projectId: "benchmark-project",
        sourceUri: "file:///benchmark",
        buildProfiles: [{ buildProfileId: "benchmark-profile" }]
      }],
      toolchains: [],
      workspaceGeneration: "benchmark-workspace",
      workspaceUri: "file:///benchmark"
    } as unknown as WorkspaceSnapshot;
  }

  async discoverTests(_input: TestDiscoveryInput): Promise<never> { return {} as never; }
  async getTestCatalog(_input: CatalogGetInput): Promise<ProtocolTestCatalog> { return this.catalog; }
  async runTests(_input: TestRunInput): Promise<never> { throw new Error("benchmark does not run tests"); }
  async getTestRun(_runId: string): Promise<ProtocolTestRun> { throw new Error("benchmark has no runs"); }
  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    return new EventSubscription(afterSequence);
  }
  close(): void {}
}

test("10,000 item catalog keeps Test Item identity for the same revision", async (t) => {
  const client = new BenchmarkClient();
  const mutationCounts: BenchmarkMutationCounts = {
    create: 0,
    add: 0,
    delete: 0,
    replace: 0
  };
  const root = new BenchmarkCollection(mutationCounts);
  const controller: TestingController = {
    items: root,
    createTestItem: (id, label, uri) => {
      mutationCounts.create++;
      return {
        id,
        label,
        uri,
        children: new BenchmarkCollection(mutationCounts)
      };
    },
    dispose() {}
  };
  const host: TestingApiHost = {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: true }),
    createTestController: () => controller,
    showErrorMessage() {}
  };
  const adapter = new TestingApiAdapter(host, () => client, () => "trusted");
  t.after(() => adapter.close());

  const startedAt = performance.now();
  await adapter.refresh();
  const elapsedMs = performance.now() - startedAt;
  const container = root.get("benchmark-container");
  assert.ok(container);
  const children = container.children as BenchmarkCollection;
  assert.equal(children.entries.size, ITEM_COUNT);
  const itemReferences = new Map(children.entries);
  assert.equal(itemReferences.size, ITEM_COUNT);
  assert.deepEqual(mutationCounts, {
    create: ITEM_COUNT + 1,
    add: 0,
    delete: 0,
    replace: 2
  });
  const mutationsBeforeSameRevision = { ...mutationCounts };

  await adapter.refresh();

  const currentContainer = root.get("benchmark-container");
  assert.equal(currentContainer, container);
  const currentChildren = currentContainer?.children as BenchmarkCollection;
  assert.equal(currentChildren.entries.size, ITEM_COUNT);
  for (const [id, reference] of itemReferences) {
    assert.equal(currentChildren.get(id), reference, `${id} identity changed for the same revision`);
  }
  assert.deepEqual(mutationCounts, mutationsBeforeSameRevision);
  const replacementCount = mutationCounts.replace - mutationsBeforeSameRevision.replace;
  t.diagnostic(JSON.stringify({
    runtime: `node-${process.versions.node}`,
    platform: `${process.platform}-${process.arch}`,
    itemCount: ITEM_COUNT,
    revision: REVISION,
    elapsedMs: Number(elapsedMs.toFixed(3)),
    replacementCount
  }));
});
