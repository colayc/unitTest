import { join } from "node:path";
import type * as vscodeTypes from "vscode";
import type { ServiceStatus, TrustState } from "./contracts.js";
import {
  registerCommands,
  type CommandContext,
  type CommandHost,
  type CommandManager,
  type DisposableLike,
  type OutputChannelLike,
  type StatusBarLike
} from "./commands.js";
import { ServiceManager } from "./service-manager.js";
import { TrustGate, type WorkspaceSnapshot } from "./trust-gate.js";

const DEFAULT_STOP_TIMEOUT_MS = 2_000;

export interface ExtensionWorkspaceSnapshot extends WorkspaceSnapshot {
  workspaceRoot?: string;
}

export interface ExtensionHost extends CommandHost {
  readonly context: CommandContext;
  readonly extensionPath?: string;
  readonly dataDirectory?: string;
  workspaceSnapshot(): ExtensionWorkspaceSnapshot;
  configuration<T>(key: string, fallback: T): T;
  createOutputChannel(name: string): OutputChannelLike;
  createStatusBarItem(): StatusBarLike;
  onDidChangeWorkspaceFolders(listener: () => void | Promise<void>): DisposableLike;
  onDidGrantWorkspaceTrust(listener: () => void | Promise<void>): DisposableLike;
}

export type LifecycleManager = CommandManager;

export interface ExtensionControllerOptions {
  manager?: LifecycleManager;
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

  constructor(private readonly item: StatusBarLike) {}

  get trustState(): TrustState {
    return this.#trustState;
  }

  projectTrust(state: TrustState): void {
    this.#trustState = state;
    this.#render();
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

function createManager(
  host: ExtensionHost,
  snapshot: ExtensionWorkspaceSnapshot
): ServiceManager {
  const extensionPath = host.extensionPath ?? process.cwd();
  return new ServiceManager({
    serviceExecutable: host.configuration(
      "serviceExecutable",
      bundledServiceExecutable(extensionPath)
    ),
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
  #delegate: ServiceManager | undefined;
  #workspaceRoot: string | undefined;

  constructor(private readonly host: ExtensionHost) {}

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
      this.#delegate = createManager(this.host, snapshot);
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
  #activated = false;
  #deactivation: Promise<void> | undefined;
  #transitionTail: Promise<void> = Promise.resolve();
  #workspaceRoot: string | undefined;

  constructor(
    private readonly host: ExtensionHost,
    options: ExtensionControllerOptions
  ) {
    this.#manager = options.manager ?? new WorkspaceLifecycleManager(host);
    this.#stopTimeoutMs = options.stopTimeoutMs ?? DEFAULT_STOP_TIMEOUT_MS;
    this.#output = host.createOutputChannel("Unit Test IDE");
    const statusItem = host.createStatusBarItem();
    this.#status = new StatusProjection(statusItem);
    host.context.subscriptions.push(this.#output, statusItem);
  }

  async activate(): Promise<void> {
    if (this.#activated) return;
    this.#activated = true;
    const snapshot = this.host.workspaceSnapshot();
    this.#workspaceRoot = snapshot.workspaceRoot;
    this.#status.projectTrust(this.#gate.update(snapshot));
    this.#status.projectService(this.#manager.status);

    registerCommands(
      this.host.context,
      this.#manager,
      () => this.#manager.session?.client,
      this.#output,
      this.#status,
      this.host
    );
    this.host.context.subscriptions.push(
      this.host.onDidChangeWorkspaceFolders(() => this.#enqueueReconcile()),
      this.host.onDidGrantWorkspaceTrust(() => this.#enqueueReconcile())
    );

    if (this.#status.trustState === "trusted" && this.host.configuration("autoStart", true)) {
      await this.#startService();
    }
  }

  deactivate(): Promise<void> {
    if (this.#deactivation) return this.#deactivation;
    const stop = this.#manager.stop().catch((error) => this.#reportManagerError(error));
    this.#deactivation = settleWithin(stop, this.#stopTimeoutMs).then(() => {
      this.#status.projectService(this.#manager.status);
    });
    return this.#deactivation;
  }

  #enqueueReconcile(): Promise<void> {
    const next = this.#transitionTail.then(
      () => this.#reconcileWorkspace(),
      () => this.#reconcileWorkspace()
    );
    this.#transitionTail = next.catch(() => undefined);
    return next;
  }

  async #reconcileWorkspace(): Promise<void> {
    const previous = this.#status.trustState;
    const snapshot = this.host.workspaceSnapshot();
    const rootChanged = snapshot.workspaceRoot !== this.#workspaceRoot;
    this.#workspaceRoot = snapshot.workspaceRoot;
    const current = this.#gate.update(snapshot);
    this.#status.projectTrust(current);
    if (current === "trusted") {
      if (previous === "trusted" && rootChanged) await this.#stopService();
      if ((previous !== "trusted" || rootChanged) && this.host.configuration("autoStart", true)) {
        await this.#startService();
      }
      return;
    }
    if (previous === "trusted" || this.#manager.status.state !== "stopped") {
      await this.#stopService();
    }
  }

  async #startService(): Promise<void> {
    this.#status.projectService({ state: "starting" });
    try {
      await this.#manager.start();
    } catch (error) {
      await this.#reportManagerError(error);
    } finally {
      this.#status.projectService(this.#manager.status);
    }
  }

  async #stopService(): Promise<void> {
    this.#status.projectService({ state: "stopping" });
    try {
      await this.#manager.stop();
    } catch (error) {
      await this.#reportManagerError(error);
    } finally {
      this.#status.projectService(this.#manager.status);
    }
  }

  async #reportManagerError(error: unknown): Promise<void> {
    await this.host.showErrorMessage(
      this.#manager.status.state === "failed" && this.#manager.status.detail
        ? this.#manager.status.detail
        : error instanceof Error ? error.message : String(error)
    );
  }
}

export function createExtensionController(
  host: ExtensionHost,
  options: ExtensionControllerOptions = {}
): { activate(): Promise<void>; deactivate(): Promise<void> } {
  return new ExtensionController(host, options);
}

function createVSCodeHost(
  vscode: typeof vscodeTypes,
  context: vscodeTypes.ExtensionContext
): ExtensionHost {
  return {
    context,
    extensionPath: context.extensionUri.fsPath,
    dataDirectory: context.globalStorageUri.fsPath,
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
  await controller.activate();
}

export async function deactivate(): Promise<void> {
  const controller = activeController;
  activeController = undefined;
  if (controller) await controller.deactivate();
}
