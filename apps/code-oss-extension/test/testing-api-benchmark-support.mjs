export const IDENTITY_ITEM_COUNT = 10_000;
export const IDENTITY_REVISION = "benchmark-r1";

export function createIdentityCatalogItems() {
  return Array.from({ length: IDENTITY_ITEM_COUNT }, (_, index) => ({
    id: `benchmark-item-${index.toString().padStart(5, "0")}`,
    containerId: "benchmark-container",
    displayName: `Benchmark item ${index}`,
    logicalName: `benchmark.${index}`,
    framework: "ctest",
    kind: "case",
    disabled: false,
    labels: []
  }));
}

class BenchmarkCollection {
  entries = new Map();

  constructor(mutationCounts) {
    this.mutationCounts = mutationCounts;
  }

  add(item) {
    this.mutationCounts.add++;
    this.entries.set(item.id, item);
  }

  delete(id) {
    this.mutationCounts.delete++;
    this.entries.delete(id);
  }

  get(id) {
    return this.entries.get(id);
  }

  replace(items) {
    this.mutationCounts.replace++;
    this.entries.clear();
    for (const item of items) this.entries.set(item.id, item);
  }
}

class BenchmarkSubscription {
  #closed = false;
  #queue = [];
  #waiters = [];
  lastSequence;

  constructor(afterSequence) {
    this.lastSequence = afterSequence;
  }

  get closed() {
    return this.#closed;
  }

  push(event) {
    if (this.#closed) return;
    this.lastSequence = event.sequence;
    const waiter = this.#waiters.shift();
    if (waiter) waiter({ value: event, done: false });
    else this.#queue.push(event);
  }

  next() {
    const event = this.#queue.shift();
    if (event) return Promise.resolve({ value: event, done: false });
    if (this.#closed) return Promise.resolve({ value: undefined, done: true });
    return new Promise((resolve) => this.#waiters.push(resolve));
  }

  close() {
    if (this.#closed) return;
    this.#closed = true;
    for (const waiter of this.#waiters.splice(0)) waiter({ value: undefined, done: true });
  }
}

function benchmarkCatalog() {
  return {
    projectId: "benchmark-project",
    profileId: "benchmark-profile",
    revision: IDENTITY_REVISION,
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
    items: createIdentityCatalogItems(),
    diagnostics: []
  };
}

class BenchmarkClient {
  catalog = benchmarkCatalog();
  catalogCalls = [];

  async inspectWorkspace() {
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
    };
  }

  async discoverTests() {
    return { status: "completed" };
  }

  async getTestCatalog(input) {
    this.catalogCalls.push(input);
    const offset = input.cursor === undefined ? 0 : Number(input.cursor);
    const entries = [...this.catalog.containers, ...this.catalog.items];
    const page = entries.slice(offset, offset + 200);
    const nextOffset = offset + page.length;
    return {
      ...this.catalog,
      containers: page.filter((entry) => "capabilities" in entry),
      items: page.filter((entry) => !("capabilities" in entry)),
      ...(nextOffset < entries.length ? { nextCursor: String(nextOffset) } : { nextCursor: undefined })
    };
  }

  async runTests() {
    throw new Error("benchmark does not run tests");
  }

  async getTestRun() {
    throw new Error("benchmark has no runs");
  }

  async subscribeEvents(afterSequence) {
    const subscription = new BenchmarkSubscription(afterSequence);
    queueMicrotask(() => subscription.push({
      sequence: afterSequence + 1,
      event: "test.catalog.published",
      payload: { projectId: "benchmark-project", profileId: "benchmark-profile" }
    }));
    return subscription;
  }

  close() {}
}

export function createTestingApiIdentityFixture(TestingApiAdapter) {
  const client = new BenchmarkClient();
  const mutationCounts = { create: 0, add: 0, delete: 0, replace: 0 };
  const root = new BenchmarkCollection(mutationCounts);
  const controller = {
    items: root,
    createTestItem(id, label, uri) {
      mutationCounts.create++;
      return { id, label, uri, children: new BenchmarkCollection(mutationCounts) };
    },
    dispose() {}
  };
  const host = {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: true }),
    createTestController: () => controller,
    showErrorMessage() {}
  };
  const adapter = new TestingApiAdapter(host, () => client, () => "trusted");

  return {
    async run() {
      try {
        await adapter.refresh();
        const firstContainer = root.get("benchmark-container");
        const firstChildren = firstContainer?.children;
        const itemReferences = new Map(firstChildren?.entries ?? []);
        const mutationsAfterFirstRefresh = { ...mutationCounts };

        await adapter.refresh();

        const currentContainer = root.get("benchmark-container");
        const currentChildren = currentContainer?.children;
        let verifiedIdentityCount = 0;
        for (const [id, reference] of itemReferences) {
          if (currentChildren?.get(id) === reference) verifiedIdentityCount++;
        }
        return {
          adapterRefreshCount: 2,
          itemCount: currentChildren?.entries.size ?? 0,
          verifiedIdentityCount,
          identityPreserved:
            currentContainer === firstContainer &&
            verifiedIdentityCount === IDENTITY_ITEM_COUNT &&
            mutationCounts.create === mutationsAfterFirstRefresh.create &&
            mutationCounts.add === mutationsAfterFirstRefresh.add &&
            mutationCounts.delete === mutationsAfterFirstRefresh.delete &&
            mutationCounts.replace === mutationsAfterFirstRefresh.replace,
          catalogCallCount: client.catalogCalls.length,
          mutationCounts: { ...mutationCounts }
        };
      } finally {
        adapter.close();
      }
    }
  };
}
