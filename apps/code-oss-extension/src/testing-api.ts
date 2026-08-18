import { randomBytes } from "node:crypto";
import { TestSelectionModeV13, type ProtocolTestCatalog, type TestRunInput } from "@unit-test-ide/test-client";
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
  parent?: TestingTestItem;
  children?: TestingTestItemCollection;
  canResolveChildren?: boolean;
  error?: string | Error;
  uri?: unknown;
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
  readonly catalogRevision: string;
  subscription?: TestingEventSubscription & {
    readonly lastSequence: number;
    next(): Promise<IteratorResult<unknown>>;
  };
  finalizing: boolean;
  ended: boolean;
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
    handler: TestingRunProfileHandler,
    kind?: unknown,
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
  #eventCursor = 0;
  #catalogState: TestingCatalogState | undefined;
  #containersById = new Map<string, TestingTestItem>();
  #itemsById = new Map<string, TestingTestItem>();
  #activeRuns = new Map<string, ActiveTestingRun>();

  constructor(
    private readonly host: TestingApiHost,
    private readonly clientProvider: TestingClientProvider,
    private readonly readTrust: () => TrustState
  ) {
    this.#controller = host.createTestController("unitTestIde.tests", "Unit Test IDE");
    this.#controller.refreshHandler = () => this.refresh();
    this.#runProfile = this.#controller.createRunProfile?.(
      "Run Tests",
      (request) => this.#run(request),
      undefined,
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
    const client = this.#assertTrustedClient();
    if (!client) return;

    try {
      const workspace = await client.inspectWorkspace();
      if (!this.#assertTrustedClient(client)) return;
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
      if (!this.#assertTrustedClient(client)) return;
      const catalog = await this.#awaitCatalogOrPoll(client, project.projectId, profile.buildProfileId);
      if (!catalog || !this.#assertTrustedClient(client)) return;
      this.#reconcileTree(catalog);
    } catch (error) {
      this.#clearTree();
      const redacted = redactServiceError(error, []);
      await this.host.showErrorMessage(redacted.message);
      throw redacted;
    }
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    for (const active of [...this.#activeRuns.values()]) this.#endRun(active);
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
      (expectedClient !== undefined && client !== expectedClient)
    ) {
      this.#clearTree();
      return undefined;
    }
    return client;
  }

  async #awaitCatalogOrPoll(
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string
  ): Promise<ProtocolTestCatalog | undefined> {
    if (!this.#assertTrustedClient(client)) return undefined;
    const subscription = await client.subscribeEvents(this.#eventCursor).catch(() => undefined);
    try {
      if (!this.#assertTrustedClient(client)) return undefined;
      if (subscription) {
        const published = await this.#awaitCatalogPublished(subscription, client, projectId, profileId);
        if (!this.#assertTrustedClient(client)) return undefined;
        if (published) return this.#readCatalog(client, projectId, profileId);
      }
      return this.#pollCatalog(client, projectId, profileId);
    } finally {
      if (subscription) {
        this.#eventCursor = Math.max(this.#eventCursor, subscription.lastSequence);
        subscription.close();
      }
    }
  }

  async #awaitCatalogPublished(
    subscription: Awaited<ReturnType<ExtensionProtocolClient["subscribeEvents"]>>,
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string
  ): Promise<boolean> {
    const deadline = this.#delay(50).then(() => undefined);
    while (true) {
      const next = await Promise.race([subscription.next(), deadline]);
      if (!next || next.done) return false;
      if (!this.#assertTrustedClient(client)) return false;
      this.#eventCursor = Math.max(this.#eventCursor, next.value.sequence);
      if (
        next.value.event === "test.catalog.published" &&
        next.value.payload.projectId === projectId &&
        next.value.payload.profileId === profileId
      ) return true;
    }
  }

  async #pollCatalog(
    client: ExtensionProtocolClient,
    projectId: string,
    profileId: string
  ): Promise<ProtocolTestCatalog | undefined> {
    const delays = [0, 50, 100, 200, 400, 800, 1600, 3200];
    let lastError: unknown;
    for (const delay of delays) {
      if (delay) await this.#delay(delay);
      if (!this.#assertTrustedClient(client)) return undefined;
      try {
        const catalog = await this.#readCatalog(client, projectId, profileId);
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
    profileId: string
  ): Promise<ProtocolTestCatalog | undefined> {
    if (!this.#assertTrustedClient(client)) return undefined;
    const catalog = await client.getTestCatalog({ projectId, profileId });
    if (!this.#assertTrustedClient(client)) return undefined;
    if (catalog.projectId !== projectId || catalog.profileId !== profileId) {
      throw new CatalogScopeError();
    }
    return catalog;
  }

  #reconcileTree(catalog: ProtocolTestCatalog): void {
    if (
      this.#catalogState?.projectId === catalog.projectId &&
      this.#catalogState?.profileId === catalog.profileId &&
      this.#catalogState?.revision === catalog.revision
    ) return;
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
          childItem.parent = testItem;
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
            childItem.parent = item;
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
    const client = this.#assertTrustedClient();
    const state = this.#catalogState;
    const selection = this.#selectionFor(request);
    if (!testRun || !client || !state || !selection) {
      testRun?.end();
      return;
    }

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
        testRun.end();
        return;
      }
      const active: ActiveTestingRun = {
        client,
        run: testRun,
        runId: started.runId,
        catalogRevision: state.revision,
        finalizing: false,
        ended: false
      };
      this.#activeRuns.set(active.runId, active);
      await this.#watchRun(active);
    } catch (error) {
      testRun.end();
      const redacted = redactServiceError(error, []);
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

  async #watchRun(active: ActiveTestingRun): Promise<void> {
    try {
      const subscription = await active.client.subscribeEvents(this.#eventCursor);
      if (!this.#assertTrustedClient(active.client) || active.ended || active.finalizing) {
        subscription.close();
        this.#endRun(active);
        return;
      }
      active.subscription = subscription;
      void this.#consumeRunEvents(active);
    } catch (error) {
      if (!this.#closed && this.#assertTrustedClient(active.client)) {
        const redacted = redactServiceError(error, []);
        await this.host.showErrorMessage(redacted.message);
      }
      this.#endRun(active);
    }
  }

  async #consumeRunEvents(active: ActiveTestingRun): Promise<void> {
    const subscription = active.subscription;
    if (!subscription) return;
    try {
      while (!active.ended && !active.finalizing) {
        const next = await subscription.next();
        this.#eventCursor = Math.max(this.#eventCursor, subscription.lastSequence);
        if (next.done) {
          await this.#convergeRun(active);
          return;
        }
        if (!this.#assertTrustedClient(active.client)) {
          this.#endRun(active);
          return;
        }
        await this.#applyRunEvent(active, next.value);
      }
    } catch (error) {
      if (!this.#closed && this.#assertTrustedClient(active.client)) {
        const redacted = redactServiceError(error, []);
        await this.host.showErrorMessage(redacted.message);
      }
      this.#endRun(active);
    }
  }

  async #applyRunEvent(active: ActiveTestingRun, raw: unknown): Promise<void> {
    const event = raw as {
      event?: string;
      payload?: { runId?: string; itemId?: string; result?: {
        itemId?: string;
        outcome?: string;
        durationMs?: number;
        failureDetails?: Array<{ message?: string }>;
      } };
    };
    const payload = event.payload;
    const runId = payload?.runId;
    if (runId !== active.runId) return;
    if (event.event === "test.run.finished") {
      await this.#convergeRun(active);
      return;
    }
    const itemId = event.event === "test.item.finished" ? payload?.result?.itemId : payload?.itemId;
    const item = itemId === undefined ? undefined : this.#itemsById.get(itemId);
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
    const message = redactServiceError(new Error(details || `Test ${result?.outcome ?? "result"}`), []).message;
    switch (result?.outcome) {
      case "passed": active.run.passed(item, duration); return;
      case "failed": active.run.failed(item, message, duration); return;
      case "skipped": active.run.skipped(item); return;
      case "errored":
      case "cancelled":
      case "timed_out":
      case "not_run": active.run.errored(item, message, duration); return;
      default: active.run.errored(item, message, duration);
    }
  }

  async #convergeRun(active: ActiveTestingRun): Promise<void> {
    if (active.finalizing || active.ended) return;
    active.finalizing = true;
    active.subscription?.close();
    this.#eventCursor = Math.max(this.#eventCursor, active.subscription?.lastSequence ?? this.#eventCursor);
    try {
      if (!this.#assertTrustedClient(active.client) || !this.#isCurrentCatalogRevision(active.catalogRevision)) return;
      const result = await active.client.getTestRun(active.runId);
      if (!this.#assertTrustedClient(active.client) || !this.#isCurrentCatalogRevision(active.catalogRevision)) return;
      if (
        result.runId !== active.runId ||
        result.projectId !== this.#catalogState?.projectId ||
        result.profileId !== this.#catalogState?.profileId ||
        result.catalogRevision !== active.catalogRevision
      ) return;
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

  #endRun(active: ActiveTestingRun): void {
    if (active.ended) return;
    active.ended = true;
    active.subscription?.close();
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
