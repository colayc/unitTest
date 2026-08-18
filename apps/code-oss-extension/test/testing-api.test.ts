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
  type TestingRun,
  type TestingRunProfileHandler,
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
  readonly runCalls: TestRunInput[] = [];
  readonly getRunCalls: string[] = [];
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
  activeSubscription: EventSubscription | undefined;
  afterInspect: (() => void) | undefined;
  afterDiscover: (() => void) | undefined;
  afterSubscribe: (() => void) | undefined;
  runResult: unknown = {
    kind: "testRun",
    taskId: "task-a",
    status: "running",
    createdAt: new Date("2026-08-18T00:00:00.000Z"),
    lastSequence: 0,
    projectId: "a-project",
    profileId: "a-profile",
    catalogRevision: "catalog-r1",
    runId: "run-a",
    repeatCount: 1
  };
  runResults: unknown[] = [];
  getRunResult: ProtocolTestRun | undefined;
  getRunErrors: Error[] = [];

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    this.inspectCalls++;
    this.callOrder.push("inspect");
    this.afterInspect?.();
    return this.workspace;
  }

  async discoverTests(input: TestDiscoveryInput): Promise<never> {
    this.discoveryCalls.push(input);
    this.callOrder.push("discover");
    this.afterDiscover?.();
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

  async runTests(input: TestRunInput): Promise<never> {
    this.runCalls.push(input);
    return (this.runResults.shift() ?? this.runResult) as never;
  }

  async getTestRun(runId: string): Promise<ProtocolTestRun> {
    this.getRunCalls.push(runId);
    const failure = this.getRunErrors.shift();
    if (failure) throw failure;
    if (!this.getRunResult) throw new Error("test run is not available");
    return this.getRunResult;
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    this.subscriptionCalls.push(afterSequence);
    this.activeSubscription?.close();
    const subscription = this.subscription ?? new EventSubscription(afterSequence);
    this.subscription = undefined;
    this.activeSubscription = subscription;
    this.afterSubscribe?.();
    return subscription;
  }

  emit(event: Parameters<EventSubscription["push"]>[0]): boolean {
    const subscription = this.activeSubscription;
    if (!subscription) return false;
    const accepted = subscription.push(event);
    if (!accepted) subscription.close();
    return accepted;
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
  options: {
    folderCount?: number;
    trusted?: boolean;
    trust?: "trusted" | "blocked-untrusted";
    clientProvider?: () => ExtensionProtocolClient | undefined;
    workspaceSnapshot?: () => { folderCount: number; isTrusted: boolean; workspaceRoot?: string };
    readTrust?: () => "trusted" | "blocked-untrusted";
  } = {}
) {
  const items = new FakeCollection();
  const errors: string[] = [];
  const profiles: Array<{
    label: string;
    handler: TestingRunProfileHandler;
    kind: unknown;
    isDefault: boolean | undefined;
    disposed: boolean;
  }> = [];
  const runs: Array<{
    started: string[];
    passed: Array<{ id: string; duration?: number }>;
    failed: Array<{ id: string; message: string; duration?: number }>;
    skipped: string[];
    errored: Array<{ id: string; message: string; duration?: number }>;
    ends: number;
  }> = [];
  const controller = {
    dispose() {},
    items,
    createTestItem: fakeItem,
    createRunProfile(label: string, handler: TestingRunProfileHandler, kind?: unknown, isDefault?: boolean) {
      const captured = { label, handler, kind, isDefault, disposed: false };
      profiles.push(captured);
      return { label, dispose: () => { captured.disposed = true; } };
    },
    createTestRun() {
      const captured = {
        started: [] as string[],
        passed: [] as Array<{ id: string; duration?: number }>,
        failed: [] as Array<{ id: string; message: string; duration?: number }>,
        skipped: [] as string[],
        errored: [] as Array<{ id: string; message: string; duration?: number }>,
        ends: 0
      };
      runs.push(captured);
      const run: TestingRun = {
        started: (item) => { captured.started.push(item.id); },
        passed: (item, duration) => { captured.passed.push({ id: item.id, ...(duration === undefined ? {} : { duration }) }); },
        failed: (item, message, duration) => {
          captured.failed.push({ id: item.id, message: String(message), ...(duration === undefined ? {} : { duration }) });
        },
        skipped: (item) => { captured.skipped.push(item.id); },
        errored: (item, message, duration) => {
          captured.errored.push({ id: item.id, message: String(message), ...(duration === undefined ? {} : { duration }) });
        },
        end: () => { captured.ends++; },
        dispose() {}
      };
      return run;
    }
  };
  const host: TestingApiHost = {
    workspaceSnapshot: options.workspaceSnapshot ?? (() => ({
      folderCount: options.folderCount ?? 1,
      isTrusted: options.trusted ?? true,
      workspaceRoot: "C:\\workspace"
    })),
    createTestController: () => controller,
    showErrorMessage: (message) => { errors.push(message); }
  };
  return {
    adapter: new TestingApiAdapter(
      host,
      options.clientProvider ?? (() => client),
      options.readTrust ?? (() => options.trust ?? "trusted")
    ),
    items,
    errors,
    profiles,
    runs
  };
}

async function eventually(assertion: () => void): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt++) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setImmediate(resolve));
    }
  }
  throw lastError;
}

function completedRun(runId = "run-a", revision = "catalog-r1"): ProtocolTestRun {
  return {
    runId,
    taskId: "task-a",
    projectId: "a-project",
    profileId: "a-profile",
    catalogRevision: revision,
    resultRevision: "results-r1",
    toolchainId: "toolchain-a",
    status: "completed",
    outcome: "passed",
    incomplete: false,
    selectionSnapshot: { mode: "all", containerIds: [], itemIds: [] },
    summary: {
      total: 3, completed: 3, passed: 1, failed: 1, skipped: 1,
      errored: 0, timedOut: 0, cancelled: 0, notRun: 0, iterations: 1
    }
  } as unknown as ProtocolTestRun;
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

  assert.deepEqual(client.subscriptionCalls, [0]);
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

test("refresh stops after the bounded catalog polling backoff is exhausted", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  const { adapter } = testingHarness(client);

  await assert.rejects(() => adapter.refresh(), /Test catalog refresh did not complete/);

  assert.equal(client.catalogCalls.length, 8);
});

test("refresh rejects a catalog response for another project or profile", async (t) => {
  for (const scope of ["project", "profile"] as const) {
    await t.test(scope, async () => {
      const client = new FakeProtocolClient();
      client.workspace = workspaceWithProfiles();
      client.catalogResult = {
        ...catalog(),
        ...(scope === "project" ? { projectId: "other-project" } : { profileId: "other-profile" })
      } as ProtocolTestCatalog;
      const { adapter, errors } = testingHarness(client);

      await assert.rejects(() => adapter.refresh(), /selected workspace/);
      assert.match(errors[0] ?? "", /selected workspace/);
    });
  }
});

test("refresh revalidates trust after inspect before it starts discovery", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  let trusted = true;
  client.afterInspect = () => { trusted = false; };
  const { adapter } = testingHarness(client, {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: trusted }),
    readTrust: () => trusted ? "trusted" : "blocked-untrusted"
  });

  await adapter.refresh();

  assert.equal(client.inspectCalls, 1);
  assert.deepEqual(client.discoveryCalls, []);
  assert.deepEqual(client.subscriptionCalls, []);
  assert.deepEqual(client.catalogCalls, []);
});

test("refresh revalidates the same session after discovery before subscription", async () => {
  const client = new FakeProtocolClient();
  const replacement = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  let current: ExtensionProtocolClient | undefined = client;
  client.afterDiscover = () => { current = replacement; };
  const { adapter } = testingHarness(client, { clientProvider: () => current });

  await adapter.refresh();

  assert.equal(client.inspectCalls, 1);
  assert.equal(client.discoveryCalls.length, 1);
  assert.deepEqual(client.subscriptionCalls, []);
  assert.deepEqual(client.catalogCalls, []);
});

test("refresh revalidates the same session after subscription before catalog access", async () => {
  const client = new FakeProtocolClient();
  const replacement = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  let current: ExtensionProtocolClient | undefined = client;
  client.afterSubscribe = () => { current = replacement; };
  const { adapter } = testingHarness(client, { clientProvider: () => current });

  await adapter.refresh();

  assert.equal(client.inspectCalls, 1);
  assert.equal(client.discoveryCalls.length, 1);
  assert.deepEqual(client.subscriptionCalls, [0]);
  assert.deepEqual(client.catalogCalls, []);
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
      const { adapter } = testingHarness(client, {
        ...item,
        ...(item.noSession ? { clientProvider: () => undefined } : {})
      });

      await adapter.refresh();
      assert.equal(client.inspectCalls, 0);
      assert.deepEqual(client.discoveryCalls, []);
      assert.deepEqual(client.subscriptionCalls, []);
      assert.deepEqual(client.catalogCalls, []);
    });
  }
});

test("run profile converts root, container, and item selections into stable protocol IDs", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, items, profiles } = testingHarness(client);
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);
  assert.equal(profile.kind, "run");
  assert.equal(profile.isDefault, true);

  await profile.handler({});
  await profile.handler({ include: [items.get("container-a") as TestingTestItem] });
  const suite = items.get("container-a")?.children?.get("suite-a");
  await profile.handler({ include: [suite as TestingTestItem] });

  assert.equal(profiles.length, 1);
  assert.deepEqual(client.runCalls.map(({ idempotencyKey: _idempotencyKey, ...input }) => input), [
    {
      projectId: "a-project", profileId: "a-profile", catalogRevision: "catalog-r1",
      selection: { mode: "all" }, repeatCount: 1
    },
    {
      projectId: "a-project", profileId: "a-profile", catalogRevision: "catalog-r1",
      selection: { mode: "containers", containerIds: ["container-a"] }, repeatCount: 1
    },
    {
      projectId: "a-project", profileId: "a-profile", catalogRevision: "catalog-r1",
      selection: { mode: "items", itemIds: ["suite-a"] }, repeatCount: 1
    }
  ]);
  for (const request of client.runCalls) assert.match(request.idempotencyKey, /^[a-f0-9]{32}$/);
  adapter.close();
});

test("concurrent runs share one subscription and dispatch matching run events without cancelling either run", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  client.runResults = [
    { ...client.runResult as object, runId: "run-a" },
    { ...client.runResult as object, runId: "run-b" }
  ];
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  await profile.handler({});
  assert.deepEqual(client.subscriptionCalls, [0]);
  const subscription = client.activeSubscription;
  assert.ok(subscription);
  subscription.push({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);
  subscription.push({ sequence: 2, event: "test.item.started", payload: { runId: "run-b", itemId: "case-a" } } as never);

  await eventually(() => {
    assert.deepEqual(runs[0]?.started, ["suite-a"]);
    assert.deepEqual(runs[1]?.started, ["case-a"]);
    assert.equal(runs[0]?.ends, 0);
    assert.equal(runs[1]?.ends, 0);
  });
  adapter.close();
});

test("container events update their catalog container and failure details are redacted", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  const subscription = client.activeSubscription;
  assert.ok(subscription);
  subscription.push({ sequence: 1, event: "test.container.started", payload: { runId: "run-a", containerId: "container-a" } } as never);
  subscription.push({ sequence: 2, event: "test.container.finished", payload: {
    runId: "run-a", containerId: "container-a", outcome: "failed"
  } } as never);
  subscription.push({ sequence: 3, event: "test.item.finished", payload: {
    runId: "run-a", result: {
      itemId: "case-a", outcome: "failed", failureDetails: [{ message: "failed at C:\\secret-session\\token" }]
    }
  } } as never);

  await eventually(() => {
    assert.deepEqual(runs[0]?.started, ["container-a"]);
    assert.deepEqual(runs[0]?.failed.slice(0, 1), [{ id: "container-a", message: "Test container failed" }]);
    assert.doesNotMatch(runs[0]?.failed[1]?.message ?? "", /secret-session|token/);
  });
  adapter.close();
});

test("catalog revision replacement aborts an old run before queued old events can update the new tree", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog("catalog-r1");
  client.getRunResult = completedRun("run-a", "catalog-r1");
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  client.catalogResult = catalog("catalog-r2");
  await adapter.refresh();
  client.activeSubscription?.push({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);

  await eventually(() => assert.ok((runs[0]?.errored.length ?? 0) > 0));
  assert.deepEqual(runs[0]?.started, []);
  adapter.close();
});

test("a Test Item from a replaced catalog revision is rejected without a new run subscription", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog("catalog-r1");
  const { adapter, items, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const oldItem = items.get("container-a")?.children?.get("suite-a");
  assert.ok(oldItem);
  client.catalogResult = catalog("catalog-r2");
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);
  const runCount = client.runCalls.length;
  const subscriptionCount = client.subscriptionCalls.length;
  await profile.handler({ include: [oldItem] });

  assert.equal(client.runCalls.length, runCount);
  assert.equal(client.subscriptionCalls.length, subscriptionCount);
  assert.equal(runs[0]?.ends, 1);
  adapter.close();
});

test("an event sequence gap closes the shared stream and converges every active run", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  client.getRunResult = { ...completedRun(), incomplete: true } as ProtocolTestRun;
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  client.runResults = [{ ...client.runResult as object, runId: "run-a" }, { ...client.runResult as object, runId: "run-b" }];
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  await profile.handler({});
  assert.equal(client.emit({ sequence: 2, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never), false);

  await eventually(() => {
    assert.deepEqual(client.getRunCalls.sort(), ["run-a", "run-b"]);
    assert.ok((runs[0]?.errored.length ?? 0) > 0);
    assert.ok((runs[1]?.errored.length ?? 0) > 0);
  });
  adapter.close();
});

test("closed event streams converge incomplete and failed snapshots as errored items", async (t) => {
  for (const scenario of ["running", "incomplete", "get-error"] as const) {
    await t.test(scenario, async () => {
      const client = new FakeProtocolClient();
      client.workspace = workspaceWithProfiles();
      client.catalogResult = catalog();
      client.getRunResult = {
        ...completedRun(),
        ...(scenario === "running" ? { status: "running", outcome: undefined } : {}),
        ...(scenario === "incomplete" ? { incomplete: true } : {})
      } as ProtocolTestRun;
      if (scenario === "get-error") client.getRunErrors.push(new Error("transport C:\\secret-session\\token"));
      const { adapter, profiles, runs } = testingHarness(client);
      await adapter.refresh();
      const profile = profiles[0];
      assert.ok(profile);
      await profile.handler({});
      client.activeSubscription?.close();

      await eventually(() => {
        assert.equal(runs[0]?.ends, 1);
        assert.ok((runs[0]?.errored.length ?? 0) > 0);
      });
      assert.doesNotMatch(runs[0]?.errored[0]?.message ?? "", /secret-session|token/);
      adapter.close();
    });
  }
});

test("trust loss aborts every active run without further protocol calls", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  let trusted = true;
  const { adapter, profiles, runs } = testingHarness(client, {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: trusted }),
    readTrust: () => trusted ? "trusted" : "blocked-untrusted"
  });
  await adapter.refresh();
  client.runResults = [{ ...client.runResult as object, runId: "run-a" }, { ...client.runResult as object, runId: "run-b" }];
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  await profile.handler({});
  trusted = false;
  client.activeSubscription?.push({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);

  await eventually(() => {
    assert.equal(runs[0]?.ends, 1);
    assert.equal(runs[1]?.ends, 1);
    assert.ok((runs[0]?.errored.length ?? 0) > 0);
    assert.ok((runs[1]?.errored.length ?? 0) > 0);
  });
  assert.deepEqual(client.getRunCalls, []);
  adapter.close();
});

test("session replacement aborts every active run without calls through the replacement client", async () => {
  const client = new FakeProtocolClient();
  const replacement = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  let current: ExtensionProtocolClient | undefined = client;
  const { adapter, profiles, runs } = testingHarness(client, { clientProvider: () => current });
  await adapter.refresh();
  client.runResults = [{ ...client.runResult as object, runId: "run-a" }, { ...client.runResult as object, runId: "run-b" }];
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  await profile.handler({});
  current = replacement;
  client.emit({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);

  await eventually(() => {
    assert.equal(runs[0]?.ends, 1);
    assert.equal(runs[1]?.ends, 1);
    assert.ok((runs[0]?.errored.length ?? 0) > 0);
    assert.ok((runs[1]?.errored.length ?? 0) > 0);
  });
  assert.deepEqual(replacement.subscriptionCalls, []);
  assert.deepEqual(replacement.getRunCalls, []);
  adapter.close();
});

test("run profile rejects unknown IDs and stale run revisions without subscribing", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const profile = profiles[0];
  assert.ok(profile);

  await profile.handler({ include: [{ ...fakeItem("unknown-item", "Unknown") }] });
  assert.equal(client.runCalls.length, 0);
  assert.equal(runs[0]?.ends, 1);

  client.runResult = { ...client.runResult as object, catalogRevision: "catalog-r0" };
  await profile.handler({});
  assert.equal(client.runCalls.length, 1);
  assert.deepEqual(client.subscriptionCalls, [0]);
  assert.equal(runs[1]?.ends, 1);
  adapter.close();
});

test("run event mapping projects started and all result outcomes with failure details", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const subscription = client.activeSubscription;
  assert.ok(subscription);
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  const current = runs[0];
  assert.ok(current);

  subscription.push({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);
  subscription.push({ sequence: 2, event: "test.item.finished", payload: {
    runId: "run-a", result: { itemId: "suite-a", outcome: "passed", durationMs: 12, failureDetails: [] }
  } } as never);
  subscription.push({ sequence: 3, event: "test.item.finished", payload: {
    runId: "run-a", result: {
      itemId: "case-a", outcome: "failed", durationMs: 9,
      failureDetails: [{ message: "expected 1 but got 2" }]
    }
  } } as never);
  subscription.push({ sequence: 4, event: "test.item.finished", payload: {
    runId: "run-a", result: { itemId: "suite-a", outcome: "skipped", failureDetails: [] }
  } } as never);
  subscription.push({ sequence: 5, event: "test.item.finished", payload: {
    runId: "run-a", result: { itemId: "case-a", outcome: "errored", failureDetails: [{ message: "runner unavailable" }] }
  } } as never);

  await eventually(() => {
    assert.deepEqual(current.started, ["suite-a"]);
    assert.deepEqual(current.passed, [{ id: "suite-a", duration: 12 }]);
    assert.deepEqual(current.failed, [{ id: "case-a", message: "expected 1 but got 2", duration: 9 }]);
    assert.deepEqual(current.skipped, ["suite-a"]);
    assert.deepEqual(current.errored, [{ id: "case-a", message: "runner unavailable" }]);
  });
  adapter.close();
});

test("run completion and a closed event subscription converge through getTestRun", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  client.getRunResult = completedRun();
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const subscription = client.activeSubscription;
  assert.ok(subscription);
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  subscription.push({ sequence: 1, event: "test.run.finished", payload: { runId: "run-a" } } as never);

  await eventually(() => {
    assert.deepEqual(client.getRunCalls, ["run-a"]);
    assert.equal(runs[0]?.ends, 1);
  });
  assert.equal(subscription.closed, false);
  adapter.close();

  const secondClient = new FakeProtocolClient();
  secondClient.workspace = workspaceWithProfiles();
  secondClient.catalogResult = catalog();
  secondClient.getRunResult = completedRun();
  const second = testingHarness(secondClient);
  await second.adapter.refresh();
  const secondSubscription = secondClient.activeSubscription;
  assert.ok(secondSubscription);
  const secondProfile = second.profiles[0];
  assert.ok(secondProfile);
  await secondProfile.handler({});
  secondSubscription.close();
  await eventually(() => assert.deepEqual(secondClient.getRunCalls, ["run-a"]));
  second.adapter.close();
});

test("deactivate closes active run subscriptions before later events can update the TestRun", async () => {
  const client = new FakeProtocolClient();
  client.workspace = workspaceWithProfiles();
  client.catalogResult = catalog();
  const { adapter, profiles, runs } = testingHarness(client);
  await adapter.refresh();
  const subscription = client.activeSubscription;
  assert.ok(subscription);
  const profile = profiles[0];
  assert.ok(profile);
  await profile.handler({});
  adapter.close();
  subscription.push({ sequence: 1, event: "test.item.started", payload: { runId: "run-a", itemId: "suite-a" } } as never);

  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(subscription.closed, true);
  assert.deepEqual(runs[0]?.started, []);
  assert.equal(runs[0]?.ends, 1);
  assert.deepEqual(client.getRunCalls, []);
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
