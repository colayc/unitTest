import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { EventSubscription } from "@unit-test-ide/test-client";
import type {
  CatalogGetInput,
  ProtocolTestCatalog,
  ProtocolTestRun,
  TestDiscoveryInput,
  TestRunInput,
  WorkspaceSnapshot
} from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";
import {
  TestingApiAdapter,
  type TestingApiHost,
  type TestingTestItem,
  type TestingTestItemCollection
} from "../src/testing-api.js";

class FakeCollection implements TestingTestItemCollection {
  readonly entries = new Map<string, TestingTestItem>();
  replaceCalls = 0;

  add(item: TestingTestItem): void { this.entries.set(item.id, item); }
  delete(id: string): void { this.entries.delete(id); }
  get(id: string): TestingTestItem | undefined { return this.entries.get(id); }
  replace(items: readonly TestingTestItem[]): void {
    this.replaceCalls++;
    this.entries.clear();
    for (const item of items) this.entries.set(item.id, item);
  }
}

function fakeItem(id: string, label: string, uri?: unknown): TestingTestItem {
  return { id, label, uri, children: new FakeCollection() };
}

function catalog(revision = "catalog-r1"): ProtocolTestCatalog {
  return {
    projectId: "a-project",
    profileId: "a-profile",
    revision,
    generatedAt: new Date("2026-08-18T00:00:00.000Z"),
    partial: false,
    containers: [{
      id: "container-a",
      projectId: "a-project",
      displayName: "Alpha container",
      ctestLogicalName: "alpha",
      framework: "unity",
      disabled: false,
      labels: ["fast"],
      capabilities: {
        canDiscoverCases: true,
        canReportMockDetails: false,
        canReportSkipped: true,
        canReportSourceLocation: true,
        canRunCase: true
      },
      sourceLocation: {
        uri: "file:///workspace/alpha.cpp",
        line: 4,
        column: 2,
        navigable: true,
        provenance: "test-declaration"
      }
    }],
    items: [
      {
        id: "suite-a",
        containerId: "container-a",
        displayName: "Alpha suite",
        logicalName: "AlphaSuite",
        framework: "unity",
        kind: "suite",
        disabled: false,
        labels: ["fast"]
      },
      {
        id: "case-a",
        containerId: "container-a",
        parentId: "suite-a",
        displayName: "disabled case",
        logicalName: "AlphaSuite.disabled",
        framework: "unity",
        kind: "case",
        disabled: true,
        labels: ["fast", "disabled"],
        sourceLocation: {
          uri: "file:///workspace/alpha.cpp",
          line: 9,
          column: 3,
          navigable: true,
          provenance: "test-declaration"
        }
      }
    ],
    diagnostics: [{
      category: "configuration_error",
      code: "partial-discovery",
      message: "catalog diagnostic",
      severity: "warning",
      sourceUri: "file:///workspace/alpha.cpp",
      line: 4,
      column: 2
    }]
  } as unknown as ProtocolTestCatalog;
}

class FakeProtocolClient implements ExtensionProtocolClient {
  inspectCalls = 0;
  readonly discoveryCalls: TestDiscoveryInput[] = [];
  readonly catalogCalls: CatalogGetInput[] = [];
  readonly subscriptionCalls: number[] = [];
  readonly callOrder: string[] = [];
  workspace: WorkspaceSnapshot = {
    capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
    diagnostics: [],
    projects: [],
    toolchains: [],
    workspaceGeneration: "trusted-workspace",
    workspaceUri: "file:///workspace"
  };
  catalogResult: ProtocolTestCatalog | undefined;
  catalogResults: Array<ProtocolTestCatalog | Error> = [];
  subscription: EventSubscription | undefined;

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    this.inspectCalls++;
    this.callOrder.push("inspect");
    return this.workspace;
  }

  async discoverTests(input: TestDiscoveryInput): Promise<never> {
    this.discoveryCalls.push(input);
    this.callOrder.push("discover");
    return {} as never;
  }

  async getTestCatalog(input: CatalogGetInput): Promise<ProtocolTestCatalog> {
    this.catalogCalls.push(input);
    this.callOrder.push("catalog");
    const next = this.catalogResults.shift();
    if (next instanceof Error) throw next;
    if (next) return next;
    if (!this.catalogResult) throw new Error("catalog is not published");
    return this.catalogResult;
  }

  async runTests(_input: TestRunInput): Promise<never> {
    throw new Error("not used by the host-contract test");
  }

  async getTestRun(_runId: string): Promise<ProtocolTestRun> {
    throw new Error("not used by the host-contract test");
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    this.subscriptionCalls.push(afterSequence);
    const subscription = this.subscription ?? new EventSubscription(afterSequence);
    this.subscription = undefined;
    if (!subscription.closed && subscription.lastSequence === afterSequence) subscription.close();
    return subscription;
  }

  close(): void {}
}

function workspaceWithProfiles(): WorkspaceSnapshot {
  return {
    capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
    diagnostics: [],
    projects: [
      {
        projectId: "z-project",
        sourceUri: "file:///workspace/z",
        buildProfiles: [{ buildProfileId: "z-profile" }]
      },
      {
        projectId: "a-project",
        sourceUri: "file:///workspace/a",
        buildProfiles: [{ buildProfileId: "z-profile" }, { buildProfileId: "a-profile" }]
      }
    ],
    toolchains: [],
    workspaceGeneration: "trusted-workspace",
    workspaceUri: "file:///workspace"
  } as unknown as WorkspaceSnapshot;
}

function testingHarness(
  client: FakeProtocolClient,
  options: { folderCount?: number; trusted?: boolean; trust?: "trusted" | "blocked-untrusted" } = {}
) {
  const items = new FakeCollection();
  const errors: string[] = [];
  const controller = {
    dispose() {},
    items,
    createTestItem: fakeItem
  };
  const host: TestingApiHost = {
    workspaceSnapshot: () => ({
      folderCount: options.folderCount ?? 1,
      isTrusted: options.trusted ?? true,
      workspaceRoot: "C:\\workspace"
    }),
    createTestController: () => controller,
    showErrorMessage: (message) => { errors.push(message); }
  };
  return { adapter: new TestingApiAdapter(host, () => client, () => options.trust ?? "trusted"), items, errors };
}

test("refresh inspects sorted workspace, discovers once, and maps a stable nested catalog tree", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, items } = testingHarness(client);

  await adapter.refresh();

  assert.equal(client.inspectCalls, 1);
  assert.deepEqual(client.discoveryCalls.map(({ projectId, profileId }) => ({ projectId, profileId })), [
    { projectId: "a-project", profileId: "a-profile" }
  ]);
  assert.match(client.discoveryCalls[0]?.idempotencyKey ?? "", /^[a-f0-9]{32,}$/);
  assert.deepEqual(client.catalogCalls, [{ projectId: "a-project", profileId: "a-profile" }]);
  assert.deepEqual(client.callOrder, ["inspect", "discover", "catalog"]);
  assert.equal(adapter.catalogState?.revision, "catalog-r1");

  const container = items.get("container-a");
  const suite = container?.children?.get("suite-a");
  const disabled = suite?.children?.get("case-a");
  assert.equal(container?.label, "Alpha container");
  assert.equal(container?.uri, "file:///workspace/alpha.cpp");
  assert.equal(suite?.parent, container);
  assert.equal(disabled?.parent, suite);
  assert.equal(disabled?.id, "case-a");
  assert.match(String(disabled?.error), /disabled/i);
  assert.deepEqual(disabled?.sourceLocation, {
    uri: "file:///workspace/alpha.cpp",
    line: 9,
    column: 3,
    navigable: true,
    provenance: "test-declaration"
  });
  assert.equal(container?.diagnostics?.[0]?.code, "partial-discovery");
});

test("refresh does not rebuild the tree when the catalog revision is unchanged", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog("catalog-r1");
  const { adapter, items } = testingHarness(client);

  await adapter.refresh();
  const firstContainer = items.get("container-a");
  const replaceCalls = items.replaceCalls;
  await adapter.refresh();

  assert.equal(items.get("container-a"), firstContainer);
  assert.equal(items.replaceCalls, replaceCalls);
});

test("refresh atomically replaces stale catalog children when the revision changes", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog("catalog-r1");
  const { adapter, items } = testingHarness(client);

  await adapter.refresh();
  const source = catalog("catalog-r2");
  const originalContainer = source.containers[0];
  assert.ok(originalContainer);
  client.catalogResult = {
    ...source,
    containers: [{ ...originalContainer, id: "container-b", displayName: "Beta container" }],
    items: []
  } as unknown as ProtocolTestCatalog;
  await adapter.refresh();

  assert.equal(items.get("container-a"), undefined);
  assert.equal(items.get("container-b")?.label, "Beta container");
});

test("refresh filters unrelated catalog publications and retains the matching event cursor", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog("catalog-r1");
  const firstSubscription = new EventSubscription(0);
  firstSubscription.push({
    sequence: 1,
    event: "test.catalog.published",
    payload: { projectId: "other-project", profileId: "a-profile" }
  } as never);
  firstSubscription.push({
    sequence: 2,
    event: "test.catalog.published",
    payload: { projectId: "a-project", profileId: "a-profile" }
  } as never);
  client.subscription = firstSubscription;
  const { adapter } = testingHarness(client);

  await adapter.refresh();
  client.catalogResult = catalog("catalog-r2");
  await adapter.refresh();

  assert.deepEqual(client.subscriptionCalls, [0, 2]);
  assert.equal(adapter.catalogState?.revision, "catalog-r2");
});

test("refresh falls back to bounded catalog polling when no catalog event is available", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResults = [new Error("catalog pending"), catalog("catalog-r1")];
  const { adapter } = testingHarness(client);

  await adapter.refresh();

  assert.equal(client.catalogCalls.length, 2);
  assert.equal(adapter.catalogState?.revision, "catalog-r1");
});

test("refresh rejects a catalog response for another project or profile", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = { ...catalog(), projectId: "other-project" } as ProtocolTestCatalog;
  const { adapter, errors } = testingHarness(client);

  await assert.rejects(() => adapter.refresh(), /selected workspace/);
  assert.match(errors[0] ?? "", /selected workspace/);
});

test("refresh does not issue protocol calls outside a trusted single-root session", async (t) => {
  const cases = [
    { name: "untrusted", trusted: false, trust: "blocked-untrusted" as const, folderCount: 1 },
    { name: "multi-root", trusted: true, trust: "trusted" as const, folderCount: 2 },
    { name: "no session", trusted: true, trust: "trusted" as const, folderCount: 1, noSession: true }
  ];

  for (const item of cases) {
    await t.test(item.name, async () => {
      const client = new FakeProtocolClient();
      const { adapter } = testingHarness(client, item);
      const noSessionAdapter = item.noSession
        ? new TestingApiAdapter({
          workspaceSnapshot: () => ({ folderCount: 1, isTrusted: true }),
          createTestController: () => ({ dispose() {} }),
          showErrorMessage: () => undefined
        }, () => undefined, () => "trusted")
        : adapter;

      await noSessionAdapter.refresh();
      assert.equal(client.inspectCalls, 0);
      assert.deepEqual(client.discoveryCalls, []);
      assert.deepEqual(client.catalogCalls, []);
    });
  }
});

test("TestingApiAdapter creates a fakeable host contract without a runtime vscode import", async () => {
  const errors: string[] = [];
  const client = new FakeProtocolClient();
  let controllerDisposed = 0;
  const host: TestingApiHost = {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: true, workspaceRoot: "C:\\workspace" }),
    createTestController(id, label) {
      assert.equal(id, "unitTestIde.tests");
      assert.equal(label, "Unit Test IDE");
      return { dispose: () => { controllerDisposed++; } };
    },
    showErrorMessage(message) { errors.push(message); }
  };

  const adapter = new TestingApiAdapter(host, () => client, () => "trusted");

  assert.equal(adapter.readWorkspace().isTrusted, true);
  assert.equal(adapter.currentTrust(), "trusted");
  assert.equal(adapter.currentClient(), client);
  await adapter.presentError(new Error("service failed at C:\\secret-session\\token"));
  assert.equal(errors.length, 1);
  assert.doesNotMatch(errors[0] ?? "", /secret-session|token/);
  adapter.close();
  assert.equal(controllerDisposed, 1);

  const source = await readFile(new URL("../src/testing-api.js", import.meta.url), "utf8");
  assert.doesNotMatch(source, /from\s+["']vscode["']|import\(["']vscode["']\)/);
});
