import { basename, isAbsolute, join } from "node:path";
import type * as vscodeTypes from "vscode";
import type { ServiceStatus, TrustState } from "./contracts.js";
import {
  registerCommands,
  presentManagerError,
  type CommandContext,
  type CommandHost,
  type CommandManager,
  type DisposableLike,
  type OutputChannelLike,
  type StatusBarLike
} from "./commands.js";
import { ServiceManager, type ServiceManagerOptions } from "./service-manager.js";
import {
  TestingApiAdapter,
  type TestingApiHost,
  type TestingController
} from "./testing-api.js";
import { TrustGate, type WorkspaceSnapshot } from "./trust-gate.js";

const DEFAULT_STOP_TIMEOUT_MS = 2_000;
export const EXTENSION_ACTIVATION_MARKER = "UNIT_TEST_IDE_EXTENSION_ACTIVATED";

export interface ExtensionWorkspaceSnapshot extends WorkspaceSnapshot {
  workspaceRoot?: string;
}

export interface ExtensionHost extends CommandHost {
  readonly context: CommandContext;
  readonly extensionPath?: string;
  readonly dataDirectory?: string;
  readonly developmentMode?: boolean;
  workspaceSnapshot(): ExtensionWorkspaceSnapshot;
  configuration<T>(key: string, fallback: T): T;
  createOutputChannel(name: string): OutputChannelLike;
  createStatusBarItem(): StatusBarLike;
  createTestController?: TestingApiHost["createTestController"];
  onDidChangeWorkspaceFolders(listener: () => void | Promise<void>): DisposableLike;
  onDidGrantWorkspaceTrust(listener: () => void | Promise<void>): DisposableLike;
}

export type LifecycleManager = CommandManager;

export interface ExtensionControllerOptions {
  manager?: LifecycleManager;
  managerFactory?: (options: ServiceManagerOptions) => LifecycleManager;
  stopTimeoutMs?: number;
}

const TRUST_STATUS_TEXT: Record<Exclude<TrustState, "trusted">, string> = {
  "no-workspace": "Unit Test: No Workspace",
  "blocked-multi-root": "Unit Test: Multi-Root Workspace",
  "blocked-untrusted": "Unit Test: Untrusted Workspace"
};

const SERVICE_STATUS_TEXT: Record<ServiceStatus["state"], string> = {
  stopped: "Unit Test: Service Stopped",
  starting: "Unit Test: Starting Service",
  running: "Unit Test: Service Ready",
  stopping: "Unit Test: Stopping Service",
  failed: "Unit Test: Service Failed"
};

class StatusProjection {
  #trustState: TrustState = "no-workspace";
  #serviceStatus: ServiceStatus = { state: "stopped" };

  constructor(
    private readonly item: StatusBarLike,
    private readonly readTrust: () => TrustState,
    private readonly readActive: () => boolean
  ) {}

  get trustState(): TrustState {
    return this.#trustState;
  }

  projectTrust(state: TrustState): void {
    this.#trustState = state;
    this.#render();
  }

  refreshTrust(): TrustState {
    const state = this.readTrust();
    this.projectTrust(state);
    return state;
  }

  isActive(): boolean {
    return this.readActive();
  }

  projectService(status: ServiceStatus): void {
    this.#serviceStatus = status;
    this.#render();
  }

  #render(): void {
    this.item.text = this.#trustState === "trusted"
      ? SERVICE_STATUS_TEXT[this.#serviceStatus.state]
      : TRUST_STATUS_TEXT[this.#trustState];
    this.item.show();
  }
}

function bundledServiceExecutable(extensionPath: string): string {
  return join(
    extensionPath,
    "bin",
    process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"
  );
}

function resolveServiceExecutable(host: ExtensionHost, extensionPath: string): string {
  const configured = host.configuration("serviceExecutable", "").trim();
  if (!configured) return bundledServiceExecutable(extensionPath);
  if (!host.developmentMode) {
    throw new Error("unitTestIde.serviceExecutable overrides are development-only");
  }
  const absolute = isAbsolute(configured);
  const normalizedBase = basename(configured.replaceAll("\\", "/")).toLowerCase();
  const expectedBase = process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service";
  if (!absolute || normalizedBase !== expectedBase) {
    throw new Error("unitTestIde.serviceExecutable must be an absolute unit-test-service executable");
  }
  return configured;
}

function createManager(
  host: ExtensionHost,
  snapshot: ExtensionWorkspaceSnapshot,
  factory: (options: ServiceManagerOptions) => LifecycleManager
): LifecycleManager {
  const extensionPath = host.extensionPath ?? process.cwd();
  return factory({
    serviceExecutable: resolveServiceExecutable(host, extensionPath),
    workspaceRoot: snapshot.workspaceRoot ?? extensionPath,
    dataDirectory: host.dataDirectory ?? join(extensionPath, ".unit-test-ide"),
    timeoutMs: host.configuration("serviceStartupTimeoutMs", 10_000),
    trusted: () => {
      const current = host.workspaceSnapshot();
      return current.folderCount === 1 && current.isTrusted;
    }
  });
}

class WorkspaceLifecycleManager implements LifecycleManager {
  #delegate: LifecycleManager | undefined;
  #workspaceRoot: string | undefined;

  constructor(
    private readonly host: ExtensionHost,
    private readonly factory: (options: ServiceManagerOptions) => LifecycleManager
  ) {}

  get status(): ServiceStatus {
    return this.#delegate?.status ?? { state: "stopped" };
  }

  get session(): LifecycleManager["session"] {
    return this.#delegate?.session;
  }

  async start(): ReturnType<LifecycleManager["start"]> {
    const snapshot = this.host.workspaceSnapshot();
    if (snapshot.folderCount !== 1 || !snapshot.workspaceRoot) {
      throw new Error("a single workspace folder is required");
    }
    if (this.#delegate && this.#workspaceRoot !== snapshot.workspaceRoot) {
      await this.#delegate.stop();
      this.#delegate = undefined;
    }
    if (!this.#delegate) {
      this.#delegate = createManager(this.host, snapshot, this.factory);
      this.#workspaceRoot = snapshot.workspaceRoot;
    }
    return this.#delegate.start();
  }

  async stop(): Promise<void> {
    const manager = this.#delegate;
    if (!manager) return;
    await manager.stop();
  }
}

function settleWithin(promise: Promise<unknown>, timeoutMs: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, timeoutMs);
    promise.then(
      () => { clearTimeout(timer); resolve(); },
      () => { clearTimeout(timer); resolve(); }
    );
  });
}

class ExtensionController {
  readonly #gate = new TrustGate();
  readonly #manager: LifecycleManager;
  readonly #output: OutputChannelLike;
  readonly #status: StatusProjection;
  readonly #stopTimeoutMs: number;
  #testingAdapter: TestingApiAdapter | undefined;
  #testingSessionRoot: string | undefined;
  #activated = false;
  #deactivating = false;
  #deactivation: Promise<void> | undefined;
  #transitionTail: Promise<void> = Promise.resolve();
  #workspaceRoot: string | undefined;

  constructor(
    private readonly host: ExtensionHost,
    options: ExtensionControllerOptions
  ) {
    this.#manager = options.manager ?? new WorkspaceLifecycleManager(
      host,
      options.managerFactory ?? ((managerOptions) => new ServiceManager(managerOptions))
    );
    this.#stopTimeoutMs = options.stopTimeoutMs ?? DEFAULT_STOP_TIMEOUT_MS;
    this.#output = host.createOutputChannel("Unit Test IDE");
    const statusItem = host.createStatusBarItem();
    this.#status = new StatusProjection(
      statusItem,
      () => this.#gate.update(this.host.workspaceSnapshot()),
      () => !this.#deactivating
    );
    host.context.subscriptions.push(this.#output, statusItem);
  }

  async activate(): Promise<void> {
    if (this.#activated) return;
    this.#activated = true;
    const snapshot = this.host.workspaceSnapshot();
    this.#workspaceRoot = snapshot.workspaceRoot;
    this.#status.projectTrust(this.#gate.update(snapshot));
    this.#status.projectService(this.#manager.status);
    const createTestController = this.host.createTestController;
    if (createTestController) {
      this.#testingAdapter = new TestingApiAdapter(
        {
          workspaceSnapshot: () => this.host.workspaceSnapshot(),
          createTestController: (id, label) => createTestController(id, label),
          showErrorMessage: (message) => this.host.showErrorMessage(message)
        },
        () => this.#testingClient(),
        () => this.#testingTrust()
      );
      this.host.context.subscriptions.push(this.#testingAdapter);
    }
    this.#bindTestingSession();

    registerCommands(
      this.host.context,
      this.#manager,
      () => this.#manager.session?.client,
      this.#output,
      this.#status,
      this.host,
      (state) => {
        if (state === "started") this.#bindTestingSession();
        else this.#invalidateTestingSession();
        return this.#refreshTesting();
      }
    );
    this.host.context.subscriptions.push(
      this.host.onDidChangeWorkspaceFolders(() => this.#enqueueReconcile()),
      this.host.onDidGrantWorkspaceTrust(() => this.#enqueueReconcile())
    );

    if (this.#status.trustState === "trusted" && this.host.configuration("autoStart", true)) {
      await this.#startService();
    }
    await this.#refreshTesting();
  }

  deactivate(): Promise<void> {
    if (this.#deactivation) return this.#deactivation;
    this.#deactivating = true;
    this.#testingAdapter?.close();
    const transitions = this.#transitionTail.catch(() => undefined);
    const stop = this.#manager.stop().catch(() => presentManagerError(
      this.host,
      this.#manager,
      "Unit Test: Service stop failed."
    ));
    const shutdown = Promise.allSettled([transitions, stop]);
    this.#deactivation = settleWithin(shutdown, this.#stopTimeoutMs).then(() => {
      this.#status.projectService(this.#manager.status);
    });
    return this.#deactivation;
  }

  #enqueueReconcile(): Promise<void> {
    if (this.#deactivating) return Promise.resolve();
    this.#revokeTestingSessionForWorkspaceChange();
    const next = this.#transitionTail.then(
      () => this.#reconcileWorkspace(),
      () => this.#reconcileWorkspace()
    );
    this.#transitionTail = next.catch(() => undefined);
    return next;
  }

  async #reconcileWorkspace(): Promise<void> {
    if (this.#deactivating) return;
    const previous = this.#status.trustState;
    const snapshot = this.host.workspaceSnapshot();
    const rootChanged = snapshot.workspaceRoot !== this.#workspaceRoot;
    this.#workspaceRoot = snapshot.workspaceRoot;
    const current = this.#gate.update(snapshot);
    this.#status.projectTrust(current);
    if (current === "trusted") {
      if (previous === "trusted" && rootChanged) {
        await this.#stopService();
        await this.#refreshTesting();
      }
      if (this.#deactivating) return;
      if ((previous !== "trusted" || rootChanged) && this.host.configuration("autoStart", true)) {
        await this.#startService();
      }
      await this.#refreshTesting();
      return;
    }
    await this.#refreshTesting();
    if (previous === "trusted" || this.#manager.status.state !== "stopped") {
      await this.#stopService();
    }
  }

  async #startService(): Promise<void> {
    if (this.#deactivating) return;
    this.#status.projectService({ state: "starting" });
    try {
      await this.#manager.start();
      this.#bindTestingSession();
    } catch {
      this.#invalidateTestingSession();
      await presentManagerError(
        this.host,
        this.#manager,
        "Unit Test: Service start failed."
      );
    } finally {
      this.#status.projectService(this.#manager.status);
    }
  }

  async #stopService(): Promise<void> {
    this.#status.projectService({ state: "stopping" });
    try {
      await this.#manager.stop();
    } catch {
      await presentManagerError(
        this.host,
        this.#manager,
        "Unit Test: Service stop failed."
      );
    } finally {
      this.#status.projectService(this.#manager.status);
    }
  }

  #testingTrust(): TrustState {
    return this.#gate.update(this.host.workspaceSnapshot());
  }

  #testingClient() {
    const snapshot = this.host.workspaceSnapshot();
    return this.#gate.update(snapshot) === "trusted" &&
      snapshot.workspaceRoot !== undefined &&
      snapshot.workspaceRoot === this.#testingSessionRoot
      ? this.#manager.session?.client
      : undefined;
  }

  async #refreshTesting(): Promise<void> {
    if (this.#deactivating) return;
    await this.#testingAdapter?.refresh().catch(() => undefined);
  }

  #bindTestingSession(): void {
    const snapshot = this.host.workspaceSnapshot();
    this.#testingSessionRoot = this.#gate.update(snapshot) === "trusted" && this.#manager.session
      ? snapshot.workspaceRoot
      : undefined;
  }

  #invalidateTestingSession(): void {
    this.#testingSessionRoot = undefined;
    void this.#refreshTesting();
  }

  #revokeTestingSessionForWorkspaceChange(): void {
    const snapshot = this.host.workspaceSnapshot();
    if (this.#gate.update(snapshot) !== "trusted" || snapshot.workspaceRoot !== this.#testingSessionRoot) {
      this.#invalidateTestingSession();
    }
  }

}

export function createExtensionController(
  host: ExtensionHost,
  options: ExtensionControllerOptions = {}
): { activate(): Promise<void>; deactivate(): Promise<void> } {
  return new ExtensionController(host, options);
}

export async function activateControllerWithMarker(
  controller: { activate(): Promise<void> },
  emitMarker: (marker: string) => void = (marker) => console.log(marker)
): Promise<void> {
  await controller.activate();
  emitMarker(EXTENSION_ACTIVATION_MARKER);
}

function createVSCodeHost(
  vscode: typeof vscodeTypes,
  context: vscodeTypes.ExtensionContext
): ExtensionHost {
  return {
    context,
    extensionPath: context.extensionUri.fsPath,
    dataDirectory: context.globalStorageUri.fsPath,
    developmentMode: context.extensionMode === vscode.ExtensionMode.Development ||
      context.extensionMode === vscode.ExtensionMode.Test,
    workspaceSnapshot: () => {
      const folders = vscode.workspace.workspaceFolders;
      return {
        folderCount: folders?.length ?? 0,
        isTrusted: vscode.workspace.isTrusted,
        ...(folders?.length === 1 ? { workspaceRoot: folders[0]!.uri.fsPath } : {})
      };
    },
    configuration: (key, fallback) => vscode.workspace
      .getConfiguration("unitTestIde")
      .get(key, fallback),
    createOutputChannel: (name) => vscode.window.createOutputChannel(name),
    createStatusBarItem: () => vscode.window.createStatusBarItem(
      "unitTestIde.status",
      vscode.StatusBarAlignment.Left,
      100
    ),
    createTestController: (id, label) => vscode.tests.createTestController(
      id,
      label
    ) as unknown as TestingController,
    registerCommand: (command, handler) => vscode.commands.registerCommand(command, handler),
    onDidChangeWorkspaceFolders: (listener) => vscode.workspace.onDidChangeWorkspaceFolders(listener),
    onDidGrantWorkspaceTrust: (listener) => vscode.workspace.onDidGrantWorkspaceTrust(listener),
    showErrorMessage: (message) => vscode.window.showErrorMessage(message)
  };
}

let activeController: { deactivate(): Promise<void> } | undefined;

export async function activate(context: vscodeTypes.ExtensionContext): Promise<void> {
  const vscode = await import("vscode");
  const controller = createExtensionController(createVSCodeHost(vscode, context));
  activeController = controller;
  await activateControllerWithMarker(controller);
}

export async function deactivate(): Promise<void> {
  const controller = activeController;
  activeController = undefined;
  if (controller) await controller.deactivate();
}
