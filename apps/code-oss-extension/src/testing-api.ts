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
  #closed = false;

  constructor(
    private readonly host: TestingApiHost,
    private readonly clientProvider: TestingClientProvider,
    private readonly readTrust: () => TrustState
  ) {
    this.#controller = host.createTestController("unitTestIde.tests", "Unit Test IDE");
  }

  get controller(): TestingController {
    return this.#controller;
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

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#controller.dispose();
  }

  dispose(): void {
    this.close();
  }
}
