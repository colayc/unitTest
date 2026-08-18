import { randomBytes } from "node:crypto";
import {
  TestSelectionModeV13,
  type EventSubscription,
  type ProtocolTestCatalog,
  type TestRunInput
} from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "./protocol-client.js";
import type { TrustState } from "./contracts.js";
import { redactServiceError } from "./service-resources.js";

export interface TestingWorkspaceSnapshot {
  folderCount: number;
  isTrusted: boolean;
  workspaceRoot?: string;
}

export interface TestingDisposable {
  dispose(): unknown;
}

export interface TestingTestItem {
  readonly id: string;
  label: string;
  children?: TestingTestItemCollection;
  canResolveChildren?: boolean;
  error?: string | Error;
  readonly uri?: unknown;
  sourceLocation?: TestingSourceLocation;
  diagnostics?: readonly TestingDiagnostic[];
  description?: string;
}

export interface TestingSourceLocation {
  uri: string;
  line?: number;
  column?: number;
  navigable: boolean;
  provenance: string;
}

export interface TestingDiagnostic {
  code: string;
  message: string;
  severity: string;
  category: string;
  sourceUri?: string;
  line?: number;
  column?: number;
}

export interface TestingCatalogState {
  projectId: string;
  profileId: string;
  revision: string;
}

interface CatalogTreeItem {
  id: string;
  containerId: string;
  parentId?: string;
  displayName: string;
  framework: string;
  disabled: boolean;
  labels: readonly string[];
  sourceLocation?: TestingSourceLocation;
}

interface CatalogTreeContainer {
  id: string;
  displayName: string;
  framework: string;
  disabled: boolean;
  labels: readonly string[];
  sourceLocation?: TestingSourceLocation;
}

export interface TestingTestItemCollection {
  add(item: TestingTestItem): void;
  delete(id: string): void;
  get(id: string): TestingTestItem | undefined;
  replace(items: readonly TestingTestItem[]): void;
}

export type TestingRefreshHandler = (token?: unknown) => void | Promise<void>;

export interface TestingRunRequest {
  include?: readonly TestingTestItem[];
  exclude?: readonly TestingTestItem[];
}

export type TestingRunProfileHandler = (
  request: TestingRunRequest,
  token?: unknown
) => void | Promise<void>;

export interface TestingRunProfile extends TestingDisposable {
  readonly label: string;
}

export interface TestingRun extends TestingDisposable {
  started(item: TestingTestItem): void;
  passed(item: TestingTestItem, duration?: number): void;
  failed(item: TestingTestItem, message: string | Error, duration?: number): void;
  skipped(item: TestingTestItem): void;
  errored(item: TestingTestItem, message: string | Error, duration?: number): void;
  end(): void;
}

interface ActiveTestingRun {
  readonly client: ExtensionProtocolClient;
  readonly run: TestingRun;
  readonly runId: string;
  readonly projectId: string;
  readonly profileId: string;
  readonly catalogRevision: string;
  readonly containersById: ReadonlyMap<string, TestingTestItem>;
  readonly itemsById: ReadonlyMap<string, TestingTestItem>;
  readonly terminalTargets: ReadonlySet<TestingTestItem>;
  readonly unfinished: Set<TestingTestItem>;
  finalizing: boolean;
  ended: boolean;
}

interface TestingRunSnapshot {
  readonly containersById: ReadonlyMap<string, TestingTestItem>;
  readonly itemsById: ReadonlyMap<string, TestingTestItem>;
  readonly terminalTargets: ReadonlySet<TestingTestItem>;
  readonly unfinished: Set<TestingTestItem>;
}

export interface TestingEventSubscription {
  close(): void;
}

export interface TestingController extends TestingDisposable {
  readonly items?: TestingTestItemCollection;
  refreshHandler?: TestingRefreshHandler;
  createTestItem?(id: string, label: string, uri?: unknown): TestingTestItem;
  createRunProfile?(
    label: string,
    kind: "run",
    handler: TestingRunProfileHandler,
    isDefault?: boolean
  ): TestingRunProfile;
  createTestRun?(request?: TestingRunRequest, name?: string, persist?: boolean): TestingRun;
}

export interface TestingApiHost {
  workspaceSnapshot(): TestingWorkspaceSnapshot;
  createTestController(id: string, label: string): TestingController;
  showErrorMessage(message: string): void | PromiseLike<unknown>;
}

export type TestingClientProvider = () => ExtensionProtocolClient | undefined;

export class TestingApiAdapter implements TestingDisposable {
  readonly #controller: TestingController;
  readonly #runProfile: TestingRunProfile | undefined;
  #closed = false;
  #refreshGeneration = 0;
  #eventCursor = 0;
  #catalogState: TestingCatalogState | undefined;
  #containersById = new Map<string, TestingTestItem>();
  #itemsById = new Map<string, TestingTestItem>();
  #activeRuns = new Map<string, ActiveTestingRun>();
  #eventSubscription: EventSubscription | undefined;
  #eventClient: ExtensionProtocolClient | undefined;
  #eventStart: Promise<void> | undefined;
  #catalogWaiters = new Set<{
    client: ExtensionProtocolClient;
    projectId: string;
    profileId: string;
    resolve: (published: boolean) => void;
  }>();

  constructor(
    private readonly host: TestingApiHost,
    private readonly clientProvider: TestingClientProvider,
    private readonly readTrust: () => TrustState
  ) {
    this.#controller = host.createTestController("unitTestIde.tests", "Unit Test IDE");
    this.#controller.refreshHandler = () => this.refresh();
    this.#runProfile = this.#controller.createRunProfile?.(
      "Run Tests",
      "run",
      (request) => this.#run(request),
      true
    );
  }

  get controller(): TestingController {
    return this.#controller;
  }

  get catalogState(): Readonly<TestingCatalogState> | undefined {
    return this.#catalogState;
  }

  readWorkspace(): TestingWorkspaceSnapshot {
    return this.host.workspaceSnapshot();
  }

  currentTrust(): TrustState {
    return this.readTrust();
  }

  currentClient(): ExtensionProtocolClient | undefined {
    return this.clientProvider();
  }

  async presentError(error: unknown): Promise<void> {
    await this.host.showErrorMessage(redactServiceError(error, []).message);
  }

  async refresh(): Promise<void> {
    const generation = ++this.#refreshGeneration;
    const client = this.#assertTrustedClient();
    if (!client) return;

    try {
      const workspace = await client.inspectWorkspace();
      if (!this.#isCurrentRefresh(generation, client)) return;
      const project = [...workspace.projects]
        .sort((left, right) => left.projectId.localeCompare(right.projectId))[0];
      const profile = [...(project?.buildProfiles ?? [])]
        .sort((left, right) => left.buildProfileId.localeCompare(right.buildProfileId))[0];
      if (!project || !profile) throw new Error("No project build profile is available for test discovery.");

      await client.discoverTests({
        idempotencyKey: randomBytes(16).toString("hex"),
        projectId: project.projectId,
        profileId: profile.buildProfileId
      });
      if (!this.#isCurrentRefresh(generation, client)) return;
      const catalog = await this.#awaitCatalogOrPoll(client, project.projectId, profile.buildProfileId, generation);
      if (!catalog || !this.#isCurrentRefresh(generation, client)) return;
      this.#reconcileTree(catalog);
    } catch (error) {
      if (generation !== this.#refreshGeneration) return;
      this.#abortAllRuns("Test run cancelled because the test catalog refresh failed.");
      this.#clearTree();
      const redacted = redactServiceError(error, []);
      await this.host.showErrorMessage(redacted.message);
      throw redacted;
    }
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#refreshGeneration++;
    this.#abortAllRuns("Test run cancelled because the testing adapter was closed.");
    this.#eventSubscription?.close();
    this.#runProfile?.dispose();
    this.#controller.dispose();
  }

  dispose(): void {
    this.close();
  }

  #assertTrustedClient(expectedClient?: ExtensionProtocolClient): ExtensionProtocolClient | undefined {
    const workspace = this.readWorkspace();
    const client = this.currentClient();
    if (
      this.#closed ||
      this.currentTrust() !== "trusted" ||
      workspace.folderCount !== 1 ||
      !workspace.isTrusted ||
      !client ||
      (this.#eventClient !== undefined && this.#eventClient !== client) ||
      (expectedClient !== undefined && client !== expectedClient)
    ) {
      this.#abortAllRuns("Test run cancelled because the trusted testing session changed.");
      this.#clearTree();
      return undefined;
    }
    return client;
  }

  async #awaitCatalogOrPoll(
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string,
    generation: number
  ): Promise<ProtocolTestCatalog | undefined> {
    if (!this.#isCurrentRefresh(generation, client)) return undefined;
    const publication = this.#waitForCatalogPublication(client, projectId, profileId);
    try {
      await this.#ensureEventPump(client);
      if (!this.#isCurrentRefresh(generation, client)) return undefined;
      if (await publication.result) return this.#readCatalog(client, projectId, profileId, generation);
      return this.#pollCatalog(client, projectId, profileId, generation);
    } finally {
      publication.cancel();
    }
  }

  #waitForCatalogPublication(client: ExtensionProtocolClient, projectId: string, profileId: string): {
    result: Promise<boolean>;
    cancel(): void;
  } {
    let resolve!: (published: boolean) => void;
    const waiter = { client, projectId, profileId, resolve: (published: boolean) => resolve(published) };
    const result = new Promise<boolean>((complete) => { resolve = complete; });
    this.#catalogWaiters.add(waiter);
    const deadline = setTimeout(() => {
      if (!this.#catalogWaiters.delete(waiter)) return;
      resolve(false);
    }, 50);
    return {
      result,
      cancel: () => {
        clearTimeout(deadline);
        if (this.#catalogWaiters.delete(waiter)) resolve(false);
      }
    };
  }

  async #pollCatalog(
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string,
    generation: number
  ): Promise<ProtocolTestCatalog | undefined> {
    const delays = [0, 50, 100, 200, 400, 800, 1600, 3200];
    let lastError: unknown;
    for (const delay of delays) {
      if (delay) await this.#delay(delay);
      if (!this.#isCurrentRefresh(generation, client)) return undefined;
      try {
        const catalog = await this.#readCatalog(client, projectId, profileId, generation);
        if (!catalog) return undefined;
        return catalog;
      } catch (error) {
        if (error instanceof CatalogScopeError) throw error;
        lastError = error;
      }
    }
    void lastError;
    throw new Error("Test catalog refresh did not complete.");
  }

  async #readCatalog(
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string,
    generation: number
  ): Promise<ProtocolTestCatalog | undefined> {
    const pages: ProtocolTestCatalog[] = [];
    const seenCursors = new Set<string>();
    let cursor: string | undefined;
    for (let pageIndex = 0; pageIndex < 1_000; pageIndex++) {
      if (!this.#isCurrentRefresh(generation, client)) return undefined;
      const catalog = await client.getTestCatalog({
        projectId,
        profileId,
        ...(cursor === undefined ? {} : { cursor })
      });
      if (!this.#isCurrentRefresh(generation, client)) return undefined;
      if (catalog.projectId !== projectId || catalog.profileId !== profileId) throw new CatalogScopeError();
      if (pages[0] && catalog.revision !== pages[0].revision) throw new CatalogPaginationError();
      pages.push(catalog);
      if (catalog.nextCursor === undefined) {
        const first = pages[0];
        if (!first) throw new CatalogPaginationError();
        return {
          ...first,
          containers: pages.flatMap((page) => [...page.containers]),
          items: pages.flatMap((page) => [...page.items]),
          nextCursor: undefined
        } as unknown as ProtocolTestCatalog;
      }
      if (seenCursors.has(catalog.nextCursor)) throw new CatalogPaginationError();
      seenCursors.add(catalog.nextCursor);
      cursor = catalog.nextCursor;
    }
    throw new CatalogPaginationError();
  }

  #isCurrentRefresh(generation: number, client: ExtensionProtocolClient): boolean {
    return generation === this.#refreshGeneration && this.#assertTrustedClient(client) !== undefined;
  }

  #reconcileTree(catalog: ProtocolTestCatalog): void {
    if (
      this.#catalogState?.projectId === catalog.projectId &&
      this.#catalogState?.profileId === catalog.profileId &&
      this.#catalogState?.revision === catalog.revision
    ) return;
    for (const active of [...this.#activeRuns.values()]) {
      if (
        active.projectId !== catalog.projectId ||
        active.profileId !== catalog.profileId ||
        active.catalogRevision !== catalog.revision
      ) this.#abortRun(active, "Test run cancelled because its catalog revision is no longer current.");
    }
    const root = this.#controller.items;
    const create = this.#controller.createTestItem;
    if (!root || !create) throw new Error("Testing API host cannot create a test tree.");

    const diagnostics = catalog.diagnostics.map((diagnostic) => ({
      code: diagnostic.code,
      message: diagnostic.message,
      severity: diagnostic.severity,
      category: diagnostic.category,
      ...(diagnostic.sourceUri === undefined ? {} : { sourceUri: diagnostic.sourceUri }),
      ...(diagnostic.line === undefined ? {} : { line: diagnostic.line }),
      ...(diagnostic.column === undefined ? {} : { column: diagnostic.column })
    }));
    const catalogItems: CatalogTreeItem[] = catalog.items.map((item) => ({
      id: item.id,
      containerId: item.containerId,
      ...(item.parentId === undefined ? {} : { parentId: item.parentId }),
      displayName: item.displayName,
      framework: item.framework,
      disabled: item.disabled,
      labels: item.labels,
      ...(item.sourceLocation === undefined ? {} : { sourceLocation: item.sourceLocation })
    }));
    const itemById = new Map(catalogItems.map((item) => [item.id, item]));
    const nextContainersById = new Map<string, TestingTestItem>();
    const nextItemsById = new Map<string, TestingTestItem>();
    const childrenByParent = new Map<string, CatalogTreeItem[]>();
    for (const item of catalogItems) {
      const parentKey = item.parentId ?? `container:${item.containerId}`;
      const children = childrenByParent.get(parentKey) ?? [];
      children.push(item);
      childrenByParent.set(parentKey, children);
    }

    const makeItem = (itemId: string): TestingTestItem => {
      const source = itemById.get(itemId);
      if (!source) throw new Error("Test catalog tree references an unknown item.");
      const testItem = create(source.id, source.displayName, source.sourceLocation?.uri);
      nextItemsById.set(source.id, testItem);
      this.#applyCatalogMetadata(testItem, source, diagnostics);
      const children = (childrenByParent.get(source.id) ?? [])
        .sort((left, right) => left.id.localeCompare(right.id))
        .map((child) => {
          const childItem = makeItem(child.id);
          return childItem;
        });
      if (children.length) {
        if (!testItem.children) throw new Error("Testing API host cannot create nested test items.");
        testItem.children.replace(children);
      }
      return testItem;
    };

    const catalogContainers: CatalogTreeContainer[] = catalog.containers.map((container) => ({
      id: container.id,
      displayName: container.displayName,
      framework: container.framework,
      disabled: container.disabled,
      labels: container.labels,
      ...(container.sourceLocation === undefined ? {} : { sourceLocation: container.sourceLocation })
    }));
    const containers = catalogContainers
      .sort((left, right) => left.id.localeCompare(right.id))
      .map((container) => {
        const item = create(container.id, container.displayName, container.sourceLocation?.uri);
        nextContainersById.set(container.id, item);
        this.#applyCatalogMetadata(item, container, diagnostics);
        const children = (childrenByParent.get(`container:${container.id}`) ?? [])
          .sort((left, right) => left.id.localeCompare(right.id))
          .map((child) => {
            const childItem = makeItem(child.id);
            return childItem;
          });
        if (children.length) {
          if (!item.children) throw new Error("Testing API host cannot create nested test items.");
          item.children.replace(children);
        }
        return item;
      });
    root.replace(containers);
    this.#containersById = nextContainersById;
    this.#itemsById = nextItemsById;
    this.#catalogState = {
      projectId: catalog.projectId,
      profileId: catalog.profileId,
      revision: catalog.revision
    };
  }

  #applyCatalogMetadata(
    target: TestingTestItem,
    source: CatalogTreeContainer | CatalogTreeItem,
    diagnostics: readonly TestingDiagnostic[]
  ): void {
    target.description = `${source.framework}${source.labels.length ? ` · ${source.labels.join(", ")}` : ""}`;
    if (source.sourceLocation) target.sourceLocation = { ...source.sourceLocation };
    target.diagnostics = diagnostics;
    if (source.disabled) target.error = "Disabled";
  }

  #clearTree(): void {
    this.#controller.items?.replace([]);
    this.#catalogState = undefined;
    this.#containersById.clear();
    this.#itemsById.clear();
  }

  async #run(request: TestingRunRequest): Promise<void> {
    const testRun = this.#controller.createTestRun?.(request);
    const requestedTargets = request.include === undefined
      ? [...this.#containersById.values(), ...this.#itemsById.values()]
      : [...request.include];
    const client = this.#assertTrustedClient();
    const state = this.#catalogState;
    const selection = this.#selectionFor(request);
    if (!testRun || !client || !state || !selection) {
      if (testRun) this.#markTargets(testRun, requestedTargets, "Test run rejected because its selection or trusted session is not current.");
      testRun?.end();
      return;
    }
    const snapshot = this.#snapshotForSelection(selection);

    let active: ActiveTestingRun | undefined;
    try {
      const task = await client.runTests({
        idempotencyKey: randomBytes(16).toString("hex"),
        projectId: state.projectId,
        profileId: state.profileId,
        catalogRevision: state.revision,
        selection,
        repeatCount: 1
      });
      if (!this.#assertTrustedClient(client) || !this.#isCurrentCatalog(state)) {
        this.#markSnapshot(testRun, snapshot, "Test run cancelled because the trusted testing session or catalog changed.");
        testRun.end();
        return;
      }
      const started = task as unknown as {
        kind?: string;
        runId?: string;
        projectId?: string;
        profileId?: string;
        catalogRevision?: string;
      };
      if (
        started.kind !== "testRun" ||
        typeof started.runId !== "string" ||
        started.projectId !== state.projectId ||
        started.profileId !== state.profileId ||
        started.catalogRevision !== state.revision
      ) {
        this.#markSnapshot(testRun, snapshot, "Test run returned a stale or invalid run response.");
        testRun.end();
        return;
      }
      active = {
        client,
        run: testRun,
        runId: started.runId,
        projectId: state.projectId,
        profileId: state.profileId,
        catalogRevision: state.revision,
        ...snapshot,
        finalizing: false,
        ended: false
      };
      const previous = this.#activeRuns.get(active.runId);
      if (previous) this.#abortRun(previous, "Test run was superseded by a duplicate run identifier.");
      this.#activeRuns.set(active.runId, active);
      await this.#ensureEventPump(client);
      if (!this.#assertTrustedClient(client) || !this.#isCurrentRunCatalog(active)) {
        this.#abortRun(active, "Test run cancelled because its catalog revision is no longer current.");
      }
    } catch (error) {
      const redacted = redactServiceError(error, []);
      if (active) this.#abortRun(active, redacted.message);
      else {
        this.#markSnapshot(testRun, snapshot, redacted.message);
        testRun.end();
      }
      await this.host.showErrorMessage(redacted.message);
    }
  }

  #selectionFor(request: TestingRunRequest): TestRunInput["selection"] | undefined {
    if (request.exclude?.length) return undefined;
    const include = request.include;
    if (!include?.length) return { mode: TestSelectionModeV13.All };
    const unique = [...new Map(include.map((item) => [item.id, item])).values()];
    if (unique.every((item) => this.#containersById.get(item.id) === item)) {
      return { mode: TestSelectionModeV13.Containers, containerIds: unique.map((item) => item.id).sort() };
    }
    if (unique.every((item) => this.#itemsById.get(item.id) === item)) {
      return { mode: TestSelectionModeV13.Items, itemIds: unique.map((item) => item.id).sort() };
    }
    return undefined;
  }

  #snapshotForSelection(selection: TestRunInput["selection"]): TestingRunSnapshot {
    const containersById = new Map(this.#containersById);
    const itemsById = new Map(this.#itemsById);
    const targets = selection.mode === TestSelectionModeV13.Containers
      ? selection.containerIds.map((id) => containersById.get(id)).filter((item): item is TestingTestItem => item !== undefined)
      : selection.mode === TestSelectionModeV13.Items
      ? selection.itemIds.map((id) => itemsById.get(id)).filter((item): item is TestingTestItem => item !== undefined)
      : [...containersById.values()];
    return {
      containersById,
      itemsById,
      terminalTargets: new Set(targets),
      unfinished: new Set(targets)
    };
  }

  async #ensureEventPump(client: ExtensionProtocolClient): Promise<void> {
    if (this.#eventSubscription && this.#eventClient === client && !this.#eventSubscription.closed) return;
    if (this.#eventStart) return this.#eventStart;
    const start = (async () => {
      const subscription = await client.subscribeEvents(this.#eventCursor);
      if (!this.#assertTrustedClient(client)) {
        subscription.close();
        return;
      }
      this.#eventSubscription?.close();
      this.#eventSubscription = subscription;
      this.#eventClient = client;
      void this.#consumeEvents(client, subscription);
    })();
    this.#eventStart = start;
    try {
      await start;
    } finally {
      if (this.#eventStart === start) this.#eventStart = undefined;
    }
  }

  async #consumeEvents(client: ExtensionProtocolClient, subscription: EventSubscription): Promise<void> {
    try {
      while (!this.#closed && this.#eventSubscription === subscription) {
        const next = await subscription.next();
        this.#eventCursor = Math.max(this.#eventCursor, subscription.lastSequence);
        if (next.done) break;
        if (!this.#assertTrustedClient(client)) return;
        this.#dispatchCatalogEvent(client, next.value);
        await this.#dispatchRunEvent(client, next.value);
      }
    } catch (error) {
      if (!this.#closed && this.#assertTrustedClient(client)) {
        await this.host.showErrorMessage(redactServiceError(error, []).message);
      }
    } finally {
      if (this.#eventSubscription === subscription) {
        this.#eventSubscription = undefined;
        this.#eventClient = undefined;
        if (!this.#closed && this.#assertTrustedClient(client)) {
          await Promise.all([...this.#activeRuns.values()]
            .filter((active) => active.client === client)
            .map((active) => this.#convergeRun(active, "Test event stream closed before a final result was observed.")));
        }
      }
    }
  }

  #dispatchCatalogEvent(client: ExtensionProtocolClient, raw: unknown): void {
    const event = raw as { event?: string; payload?: { projectId?: string; profileId?: string } };
    if (event.event !== "test.catalog.published") return;
    for (const waiter of [...this.#catalogWaiters]) {
      if (waiter.client !== client || waiter.projectId !== event.payload?.projectId || waiter.profileId !== event.payload?.profileId) continue;
      this.#catalogWaiters.delete(waiter);
      waiter.resolve(true);
    }
  }

  async #dispatchRunEvent(client: ExtensionProtocolClient, raw: unknown): Promise<void> {
    const event = raw as {
      event?: string;
      payload?: { runId?: string; itemId?: string; containerId?: string; outcome?: string; result?: {
        itemId?: string;
        outcome?: string;
        durationMs?: number;
        failureDetails?: Array<{ message?: string }>;
      } };
    };
    const payload = event.payload;
    const active = payload?.runId === undefined ? undefined : this.#activeRuns.get(payload.runId);
    if (!active || active.client !== client || active.ended) return;
    if (!this.#isCurrentRunCatalog(active)) {
      this.#abortRun(active, "Test run cancelled because its catalog revision is no longer current.");
      return;
    }
    if (event.event === "test.run.finished") {
      await this.#convergeRun(active);
      return;
    }
    if (event.event === "test.container.started" || event.event === "test.container.finished") {
      const container = payload?.containerId === undefined ? undefined : active.containersById.get(payload.containerId);
      if (!container) return;
      if (event.event === "test.container.started") {
        active.run.started(container);
        return;
      }
      active.unfinished.delete(container);
      this.#applyOutcome(active.run, container, payload?.outcome, undefined, "Test container");
      return;
    }
    const itemId = event.event === "test.item.finished" ? payload?.result?.itemId : payload?.itemId;
    const item = itemId === undefined ? undefined : active.itemsById.get(itemId);
    if (!item) return;
    if (event.event === "test.item.started") {
      active.run.started(item);
      return;
    }
    if (event.event !== "test.item.finished") return;
    const result = payload?.result;
    const duration = typeof result?.durationMs === "number" && result.durationMs >= 0 ? result.durationMs : undefined;
    const details = result?.failureDetails
      ?.map((detail) => detail.message)
      .filter((message): message is string => typeof message === "string" && message.length > 0)
      .join("\n");
    active.unfinished.delete(item);
    this.#applyOutcome(active.run, item, result?.outcome, duration, "Test", details);
  }

  #applyOutcome(
    run: TestingRun,
    item: TestingTestItem,
    outcome: string | undefined,
    duration: number | undefined,
    subject: string,
    details?: string
  ): void {
    const message = redactServiceError(new Error(details || `${subject} ${outcome ?? "result"}`), []).message;
    switch (outcome) {
      case "passed": run.passed(item, duration); return;
      case "failed": run.failed(item, message, duration); return;
      case "skipped": run.skipped(item); return;
      default: run.errored(item, message, duration);
    }
  }

  async #convergeRun(active: ActiveTestingRun, interruption?: string): Promise<void> {
    if (active.finalizing || active.ended) return;
    active.finalizing = true;
    try {
      if (!this.#assertTrustedClient(active.client) || !this.#isCurrentRunCatalog(active)) {
        this.#markUnfinished(active, "Test run cancelled because its catalog revision is no longer current.");
        return;
      }
      const result = await active.client.getTestRun(active.runId);
      if (!this.#assertTrustedClient(active.client) || !this.#isCurrentRunCatalog(active)) {
        this.#markUnfinished(active, "Test run cancelled because its catalog revision is no longer current.");
        return;
      }
      if (
        result.runId !== active.runId ||
        result.projectId !== active.projectId ||
        result.profileId !== active.profileId ||
        result.catalogRevision !== active.catalogRevision
      ) {
        this.#markTerminalOutcome(active, "Test run returned a result for a different testing catalog.");
      } else if (result.status !== "completed" || result.incomplete || result.outcome === undefined) {
        this.#markUnfinished(active, interruption ?? "Test run did not reach a final complete result.");
      } else if (result.outcome !== "passed") {
        this.#markTerminalOutcome(active, `Test run completed with ${result.outcome}.`);
      } else if (active.unfinished.size) {
        this.#markUnfinished(active, "Test run completed without an item result.");
      }
    } catch (error) {
      this.#markUnfinished(active, redactServiceError(error, []).message);
      if (!this.#closed) await this.host.showErrorMessage(redactServiceError(error, []).message);
    } finally {
      this.#endRun(active);
    }
  }

  #isCurrentCatalog(state: TestingCatalogState): boolean {
    return this.#isCurrentCatalogRevision(state.revision) &&
      this.#catalogState?.projectId === state.projectId &&
      this.#catalogState?.profileId === state.profileId;
  }

  #isCurrentCatalogRevision(revision: string): boolean {
    return this.#catalogState?.revision === revision;
  }

  #isCurrentRunCatalog(active: ActiveTestingRun): boolean {
    return this.#catalogState?.projectId === active.projectId &&
      this.#catalogState?.profileId === active.profileId &&
      this.#catalogState?.revision === active.catalogRevision;
  }

  #markUnfinished(active: ActiveTestingRun, message: string): void {
    const redacted = redactServiceError(new Error(message), []).message;
    for (const item of active.unfinished) active.run.errored(item, redacted);
    active.unfinished.clear();
  }

  #markTerminalOutcome(active: ActiveTestingRun, message: string): void {
    const redacted = redactServiceError(new Error(message), []).message;
    for (const item of active.terminalTargets) active.run.errored(item, redacted);
    active.unfinished.clear();
  }

  #markSnapshot(run: TestingRun, snapshot: TestingRunSnapshot, message: string): void {
    const redacted = redactServiceError(new Error(message), []).message;
    for (const item of snapshot.unfinished) run.errored(item, redacted);
    snapshot.unfinished.clear();
  }

  #markTargets(run: TestingRun, targets: readonly TestingTestItem[], message: string): void {
    const redacted = redactServiceError(new Error(message), []).message;
    for (const item of new Set(targets)) run.errored(item, redacted);
  }

  #abortRun(active: ActiveTestingRun, message: string): void {
    if (active.ended) return;
    this.#markUnfinished(active, message);
    this.#endRun(active);
  }

  #abortAllRuns(message: string): void {
    const subscription = this.#eventSubscription;
    this.#eventSubscription = undefined;
    this.#eventClient = undefined;
    subscription?.close();
    for (const active of [...this.#activeRuns.values()]) this.#abortRun(active, message);
  }

  #endRun(active: ActiveTestingRun): void {
    if (active.ended) return;
    active.ended = true;
    this.#activeRuns.delete(active.runId);
    active.run.end();
  }

  #delay(milliseconds: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, milliseconds));
  }
}

class CatalogScopeError extends Error {
  constructor() {
    super("Test catalog response did not match the selected workspace.");
  }
}

class CatalogPaginationError extends Error {
  constructor() {
    super("Test catalog pagination did not produce one stable bounded snapshot.");
  }
}
