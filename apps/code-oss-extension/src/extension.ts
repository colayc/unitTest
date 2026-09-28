import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { basename, isAbsolute, join } from "node:path";
import { TestGenerationScopeV15, type CoverageSourceSnapshotV14 } from "@unit-test-ide/test-client";
import type * as vscodeTypes from "vscode";
import type { ServiceStatus, TrustState } from "./contracts.js";
import {
  registerCommands,
  registerCoverageCommands,
  registerManagedTestCommands,
  registerTestGenerationCommands,
  presentManagerError,
  type CommandContext,
  type CommandHost,
  type CommandManager,
  type DisposableLike,
  type OutputChannelLike,
  type StatusBarLike
} from "./commands.js";
import { createCoverageController, type CoverageController } from "./coverage-controller.js";
import { CoverageDetailTree, type CoverageFilter, type CoverageTreeNode } from "./coverage-detail-tree.js";
import { CoverageDecorations, type LineDecoration } from "./coverage-decorations.js";
import { coverageTreeItem } from "./coverage-viewer.js";
import { verifyCoverageDetailLocation } from "./coverage-sources.js";
import { ServiceManager, type ServiceManagerOptions } from "./service-manager.js";
import {
  TestingApiAdapter,
  type TestingApiHost
} from "./testing-api.js";
import { createVSCodeTestingController } from "./vscode-testing-bridge.js";
import { TrustGate, type WorkspaceSnapshot } from "./trust-gate.js";
import { createTestGenerationController, type TestGenerationController } from "./test-generation-controller.js";
import { ManagedTestReviewController } from "./managed-test-review.js";

const DEFAULT_STOP_TIMEOUT_MS = 2_000;
export const EXTENSION_ACTIVATION_MARKER = "UNIT_TEST_IDE_EXTENSION_ACTIVATED";
const DEVELOPMENT_ACTIVATION_MARKER_FILE = "activation.marker";

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
  openCoverageHtml?: (html: string) => void | PromiseLike<void>;
  openCoverageSource?: (path: string) => void | PromiseLike<void>;
  openCoverageLocation?: (path: string, line: number) => void | PromiseLike<void>;
  setCoverageDetailsAvailable?: (available: boolean) => void | PromiseLike<void>;
  setManagedTestsAvailable?: (available: boolean) => void | PromiseLike<void>;
  setManagedReviewReady?: (ready: boolean) => void | PromiseLike<void>;
  createCoverageDetailView?: (tree: CoverageDetailTree) => CoverageDetailView;
  pickCoverageSource?: (sources: readonly CoverageSourceSnapshotV14[]) => CoverageSourceSnapshotV14 | undefined | PromiseLike<CoverageSourceSnapshotV14 | undefined>;
  showInformationMessage?: (message: string) => void | PromiseLike<unknown>;
  confirmGeneration?: (message: string) => boolean | PromiseLike<boolean>;
  openGenerationDiff?: (title: string, diff: string) => void | PromiseLike<void>;
  openManagedCaseDiff?: (title: string, diff: string) => void | PromiseLike<void>;
  pickManagedConflictChoice?: (caseId: string, choices: readonly ("keep-current" | "use-generated" | "convert-to-manual")[]) => "keep-current" | "use-generated" | "convert-to-manual" | undefined | PromiseLike<"keep-current" | "use-generated" | "convert-to-manual" | undefined>;
  pickManagedReviewId?: (records: readonly { caseId: string; reviewId: string }[]) => string | undefined | PromiseLike<string | undefined>;
  pickGenerationCandidate?: (candidates: readonly { candidateId: string; kind: string; label: string }[]) => { candidateId: string; kind: string; label: string } | undefined | PromiseLike<{ candidateId: string; kind: string; label: string } | undefined>;
  pickGenerationSelection?: (scope: TestGenerationScopeV15) => unknown | PromiseLike<unknown>;
  workspaceRoot?: () => string | undefined;
  createTestController?: TestingApiHost["createTestController"];
  onDidChangeWorkspaceFolders(listener: () => void | Promise<void>): DisposableLike;
  onDidGrantWorkspaceTrust(listener: () => void | Promise<void>): DisposableLike;
}

export interface CoverageDetailView extends DisposableLike {
  refresh(): void;
  showDecorations(path: string, items: readonly LineDecoration[]): void;
  clearDecorations(): void;
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
  #coverageController: CoverageController | undefined;
  #detailTree: CoverageDetailTree | undefined;
  #detailView: CoverageDetailView | undefined;
  #decorations: CoverageDecorations | undefined;
  #decorationPath: string | undefined;
  #detailRefreshEpoch = 0;
  #generationController: TestGenerationController | undefined;
  #managedReview: ManagedTestReviewController | undefined;
  #managedCommands: DisposableLike[] = [];
  #managedRefreshEpoch = 0;
  #generationWorkspaceGeneration = "";
  #generationProjectId = "";
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

    this.#detailTree = new CoverageDetailTree(() => {
      const catalog = this.#testingAdapter?.catalogState;
      const state = this.#coverageController?.getState();
      return {
        client: this.#testingClient(),
        workspaceGeneration: catalog?.workspaceGeneration ?? "",
        projectId: catalog?.projectId ?? "",
        reportId: state?.state === "available" ? state.reportId ?? "" : ""
      };
    });
    this.#detailView = this.host.createCoverageDetailView?.(this.#detailTree);
    if (this.#detailView) this.host.context.subscriptions.push(this.#detailView);
    void this.host.setCoverageDetailsAvailable?.(false);
    this.#decorations = new CoverageDecorations(() => {
      const binding = this.#detailTree?.binding;
      return { client: binding?.client, workspaceGeneration: binding?.workspaceGeneration ?? "", reportId: binding?.reportId ?? "", limit: this.#detailTree?.linePageLimit };
    }, (items) => {
      if (this.#decorationPath && items.length > 0) this.#detailView?.showDecorations(this.#decorationPath, items);
      else this.#detailView?.clearDecorations();
    });
    this.#coverageController = createCoverageController({
      readContext: () => {
        const catalog = this.#testingAdapter?.catalogState;
        return {
          trust: this.#testingTrust(),
          client: this.#testingClient(),
          serviceRunning: this.#manager.status.state === "running",
          workspaceGeneration: catalog?.workspaceGeneration ?? "",
          catalog,
          coverageProfileId: this.host.configuration("coverageProfileId", "coverage-debug")
        };
      },
      onStateChanged: () => { void this.#refreshCoverageDetails(); }
    });
    this.host.context.subscriptions.push(this.#coverageController);

    this.#generationController = createTestGenerationController({
      readContext: () => ({
        trust: this.#testingTrust(),
        client: this.#manager.session?.client,
        projectId: this.#generationProjectId || undefined,
        workspaceGeneration: this.#generationWorkspaceGeneration || undefined
      })
    });
    this.host.context.subscriptions.push(this.#generationController);
    this.#managedReview = new ManagedTestReviewController({ readContext: () => ({
      trust: this.#testingTrust(),
      client: this.#testingClient(),
      projectId: this.#testingAdapter?.catalogState?.projectId,
      workspaceGeneration: this.#testingAdapter?.catalogState?.workspaceGeneration,
      coverageReportId: this.#coverageController?.getState().reportId
    }), onStateChanged: (state) => { void this.host.setManagedReviewReady?.(state.canApply); } });
    void this.host.setManagedTestsAvailable?.(false);
    void this.host.setManagedReviewReady?.(false);

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
    registerCoverageCommands(
      this.host.context,
      this.#coverageController,
      () => this.#manager.session?.client,
      this.#status,
      this.host,
      this.#output,
      () => this.host.workspaceSnapshot().workspaceRoot,
      {
        available: () => this.#detailTree?.available ?? false,
        select: (node) => this.#openCoverageDetail(node),
        filter: (value) => this.#filterCoverageDetails(value),
        loadMore: async (node) => { await this.#detailTree?.children(node); this.#detailView?.refresh(); }
      }
    );
    registerTestGenerationCommands(
      this.host.context,
      this.#generationController,
      this.#status,
      this.host,
      this.#output
    );
    this.host.context.subscriptions.push(
      this.host.onDidChangeWorkspaceFolders(() => this.#enqueueReconcile()),
      this.host.onDidGrantWorkspaceTrust(() => this.#enqueueReconcile())
    );

    if (this.#status.trustState === "trusted" && this.host.configuration("autoStart", true)) {
      await this.#startService();
    }
    this.#refreshGenerationContext();
    await this.#refreshTesting();
  }

  deactivate(): Promise<void> {
    if (this.#deactivation) return this.#deactivation;
    this.#deactivating = true;
    this.#testingAdapter?.close();
    this.#clearCoverageDetails();
    this.#coverageController?.dispose();
    this.#generationController?.dispose();
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
      this.#refreshGenerationContext();
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
      this.#generationWorkspaceGeneration = "";
      this.#generationProjectId = "";
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
    return this.#manager.status.state === "running" &&
      this.#gate.update(snapshot) === "trusted" &&
      snapshot.workspaceRoot !== undefined &&
      snapshot.workspaceRoot === this.#testingSessionRoot
      ? this.#manager.session?.client
      : undefined;
  }

  async #refreshTesting(): Promise<void> {
    if (this.#deactivating) return;
    await this.#testingAdapter?.refresh().catch(() => undefined);
    this.#refreshGenerationContext();
  }

  #refreshGenerationContext(): void {
    const catalog = this.#testingAdapter?.catalogState;
    if (!catalog || this.#testingTrust() !== "trusted") {
      this.#generationWorkspaceGeneration = "";
      this.#generationProjectId = "";
      return;
    }
    this.#generationWorkspaceGeneration = catalog.workspaceGeneration;
    this.#generationProjectId = catalog.projectId;
  }

  #bindTestingSession(): void {
    const snapshot = this.host.workspaceSnapshot();
    this.#testingSessionRoot = this.#gate.update(snapshot) === "trusted" && this.#manager.session
      ? snapshot.workspaceRoot
      : undefined;
  }

  #invalidateTestingSession(): void {
    this.#testingSessionRoot = undefined;
    this.#clearCoverageDetails();
    void this.#refreshTesting();
  }

  #revokeTestingSessionForWorkspaceChange(): void {
    const snapshot = this.host.workspaceSnapshot();
    if (this.#gate.update(snapshot) !== "trusted" || snapshot.workspaceRoot !== this.#testingSessionRoot) {
      this.#invalidateTestingSession();
      this.#coverageController?.setTrustState(this.#gate.update(snapshot));
      this.#generationWorkspaceGeneration = "";
      this.#generationProjectId = "";
    }
  }

  #clearCoverageDetails(): void {
    this.#detailRefreshEpoch++;
    this.#clearManagedCommands();
    this.#detailTree?.invalidate();
    this.#decorationPath = undefined;
    this.#decorations?.clear();
    this.#detailView?.refresh();
    void this.host.setCoverageDetailsAvailable?.(false);
  }

  async #refreshCoverageDetails(): Promise<void> {
    this.#clearCoverageDetails();
    void this.#refreshManagedCommands();
    if (!this.#detailTree || this.#deactivating) return;
    const epoch = this.#detailRefreshEpoch;
    const available = await this.#detailTree.refresh();
    if (!available || this.#deactivating || epoch !== this.#detailRefreshEpoch) return;
    await this.host.setCoverageDetailsAvailable?.(true);
    if (epoch !== this.#detailRefreshEpoch) { void this.host.setCoverageDetailsAvailable?.(false); return; }
    this.#detailView?.refresh();
  }

  #clearManagedCommands(): void {
    this.#managedRefreshEpoch++;
    this.#managedReview?.invalidate();
    for (const command of this.#managedCommands) command.dispose();
    this.#managedCommands = [];
    void this.host.setManagedTestsAvailable?.(false);
    void this.host.setManagedReviewReady?.(false);
  }

  async #refreshManagedCommands(): Promise<void> {
    const epoch = this.#managedRefreshEpoch;
    const review = this.#managedReview;
    const generation = this.#generationController;
    if (!review || !generation || this.#deactivating || !await review.available() || epoch !== this.#managedRefreshEpoch || this.#deactivating) return;
    this.#managedCommands = registerManagedTestCommands(this.host.context, generation, review, this.#status, this.host, this.#output);
    await this.host.setManagedTestsAvailable?.(true);
    if (epoch !== this.#managedRefreshEpoch || this.#deactivating) this.#clearManagedCommands();
  }

  #filterCoverageDetails(value: CoverageFilter): void {
    this.#detailTree?.setFilter(value);
    this.#detailView?.refresh();
  }

  async #openCoverageDetail(node: CoverageTreeNode): Promise<void> {
    const tree = this.#detailTree;
    const binding = tree?.binding;
    const state = this.#coverageController?.getState();
    const root = this.host.workspaceSnapshot().workspaceRoot;
    if (!tree?.owns(node) || !binding || state?.state !== "available" || state.reportId !== binding.reportId || !root || !node.relativePath || !node.sourceSha256 || !this.host.openCoverageLocation) {
      throw new Error("Coverage detail location is no longer current.");
    }
    const verified = await verifyCoverageDetailLocation(root, node.relativePath, node.sourceSha256, state.sources ?? []);
    if (!tree.owns(node) || this.#coverageController?.getState().reportId !== binding.reportId) throw new Error("Coverage detail location changed while opening.");
    await this.host.openCoverageLocation(verified.path, node.startLine ?? 1);
    this.#decorationPath = verified.path;
    const fileId = tree.fileIdFor(node);
    if (fileId) await this.#decorations?.load(fileId, node.status, node.kind === "function" ? node.id : undefined);
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
  emitMarker: (marker: string) => void = (marker) => console.log(marker),
  publishDurableMarker: () => Promise<void> = async () => undefined
): Promise<void> {
  await controller.activate();
  await publishDurableMarker();
  emitMarker(EXTENSION_ACTIVATION_MARKER);
}

export async function writeDevelopmentActivationMarker(directory: string): Promise<void> {
  await mkdir(directory, { recursive: true });
  await writeFile(
    join(directory, DEVELOPMENT_ACTIVATION_MARKER_FILE),
    `${EXTENSION_ACTIVATION_MARKER}\n`,
    { encoding: "utf8", flag: "wx", mode: 0o600 }
  );
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
    openCoverageHtml: (html) => {
      const panel = vscode.window.createWebviewPanel(
        "unitTestIde.coverage",
        "Unit Test Coverage",
        vscode.ViewColumn.One,
        { enableScripts: true, retainContextWhenHidden: true }
      );
      panel.webview.html = html;
    },
    openCoverageSource: async (path) => {
      const document = await vscode.workspace.openTextDocument(vscode.Uri.file(path));
      await vscode.window.showTextDocument(document, vscode.ViewColumn.One);
    },
    openCoverageLocation: async (path, line) => {
      const document = await vscode.workspace.openTextDocument(vscode.Uri.file(path));
      const editor = await vscode.window.showTextDocument(document, vscode.ViewColumn.One);
      const position = new vscode.Position(Math.max(0, line - 1), 0);
      editor.selection = new vscode.Selection(position, position);
      editor.revealRange(new vscode.Range(position, position));
    },
    setCoverageDetailsAvailable: (available) => vscode.commands.executeCommand("setContext", "unitTestIde.coverageDetailsAvailable", available),
    setManagedTestsAvailable: (available) => vscode.commands.executeCommand("setContext", "unitTestIde.managedTestsAvailable", available),
    setManagedReviewReady: (ready) => vscode.commands.executeCommand("setContext", "unitTestIde.managedReviewReady", ready),
    createCoverageDetailView: (tree) => {
      const changed = new vscode.EventEmitter<CoverageTreeNode | undefined>();
      const provider: vscodeTypes.TreeDataProvider<CoverageTreeNode> = {
        onDidChangeTreeData: changed.event,
        getChildren: (node) => tree.children(node),
        getTreeItem: (node) => {
          const model = coverageTreeItem(node);
          const expandable = node.kind === "project" || node.kind === "file";
          const item = new vscode.TreeItem(model.label, expandable ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.None);
          item.id = `${node.kind}:${node.id}:${node.nextCursor ?? ""}`;
          item.description = model.description;
          item.tooltip = model.label;
          if (node.kind === "file" || node.kind === "function") item.contextValue = `coverage-${node.kind}`;
          if (node.kind === "file" || node.kind === "function") item.command = { title: "Open coverage location", command: "unitTestIde.openCoverageDetail", arguments: [node] };
          if (node.kind === "load-more") item.command = { title: "Load more coverage", command: "unitTestIde.loadMoreCoverageDetails", arguments: [node] };
          return item;
        }
      };
      const view = vscode.window.createTreeView("unitTestIde.coverageDetails", { treeDataProvider: provider });
      const colors = {
        covered: "gitDecoration.addedResourceForeground",
        uncovered: "gitDecoration.deletedResourceForeground",
        stale: "gitDecoration.modifiedResourceForeground",
        incomplete: "editorWarning.foreground"
      } as const;
      const types = Object.fromEntries(Object.entries(colors).map(([style, color]) => [style, vscode.window.createTextEditorDecorationType({
        isWholeLine: true, borderStyle: "solid", borderWidth: "0 0 0 2px", borderColor: new vscode.ThemeColor(color)
      })])) as Record<LineDecoration["style"], vscodeTypes.TextEditorDecorationType>;
      let decorated: vscodeTypes.TextEditor | undefined;
      const clear = () => { if (decorated) for (const type of Object.values(types)) decorated.setDecorations(type, []); decorated = undefined; };
      const changedEditor = vscode.window.onDidChangeActiveTextEditor((editor) => { if (editor !== decorated) clear(); });
      return {
        refresh: () => changed.fire(undefined),
        showDecorations: (path, lines) => {
          const editor = vscode.window.activeTextEditor;
          if (!editor || editor.document.uri.fsPath !== path) { clear(); return; }
          if (decorated !== editor) clear();
          decorated = editor;
          for (const style of Object.keys(types) as LineDecoration["style"][]) editor.setDecorations(types[style], lines.filter((item) => item.style === style).map((item) => ({
            range: new vscode.Range(Math.max(0, item.line - 1), 0, Math.max(0, item.line - 1), 0),
            hoverMessage: item.label
          })));
        },
        clearDecorations: clear,
        dispose: () => { clear(); changedEditor.dispose(); view.dispose(); changed.dispose(); for (const type of Object.values(types)) type.dispose(); }
      };
    },
    showInformationMessage: (message) => vscode.window.showInformationMessage(message),
    confirmGeneration: async (message) => Boolean(await vscode.window.showInformationMessage(message, { modal: true }, "Accept") === "Accept"),
    openGenerationDiff: async (title, diff) => {
      const document = await vscode.workspace.openTextDocument({ content: diff, language: "diff" });
      await vscode.window.showTextDocument(document, { viewColumn: vscode.ViewColumn.One, preview: false });
      void title;
    },
    openManagedCaseDiff: async (title, diff) => {
      const document = await vscode.workspace.openTextDocument({ content: diff, language: "diff" });
      await vscode.window.showTextDocument(document, { viewColumn: vscode.ViewColumn.One, preview: false });
      void title;
    },
    pickManagedConflictChoice: async (caseId, choices) => {
      const picked = await vscode.window.showQuickPick(choices.map((choice) => ({ label: choice, choice })), { placeHolder: `Resolve managed test ${caseId}` });
      return picked?.choice;
    },
    pickManagedReviewId: async (records) => {
      const picked = await vscode.window.showQuickPick(records.map((record) => ({ label: record.caseId, description: record.reviewId, reviewId: record.reviewId })), { placeHolder: "Select a managed-test review" });
      return picked?.reviewId;
    },
    pickGenerationCandidate: async (candidates) => {
      const picked = await vscode.window.showQuickPick(
        candidates.map((candidate) => ({ label: candidate.label, description: candidate.kind, candidate })),
        { placeHolder: "Select a generated-test candidate" }
      );
      return picked?.candidate;
    },
    pickGenerationSelection: async (scope) => {
      const editor = vscode.window.activeTextEditor;
      if (scope === TestGenerationScopeV15.File && editor) {
        return { file: vscode.workspace.asRelativePath(editor.document.uri, false) };
      }
      // These opaque identifiers must come from a service-backed picker, not
      // from hand-entered text. Until the corresponding service picker is
      // wired, fail closed instead of guessing an ID or another target.
      void editor;
      return undefined;
    },
    workspaceRoot: () => vscode.workspace.workspaceFolders?.length === 1
      ? vscode.workspace.workspaceFolders[0]!.uri.fsPath
      : undefined,
    pickCoverageSource: async (sources) => {
      const picked = await vscode.window.showQuickPick(
        sources.map((source) => ({ label: source.uri, description: source.sha256, source })),
        { placeHolder: "Select a coverage source" }
      );
      return picked?.source;
    },
    createStatusBarItem: () => vscode.window.createStatusBarItem(
      "unitTestIde.status",
      vscode.StatusBarAlignment.Left,
      100
    ),
    createTestController: (id, label) => createVSCodeTestingController(
      vscode,
      vscode.tests.createTestController(id, label)
    ),
    registerCommand: (command, handler) => vscode.commands.registerCommand(command, handler),
    onDidChangeWorkspaceFolders: (listener) => vscode.workspace.onDidChangeWorkspaceFolders(listener),
    onDidGrantWorkspaceTrust: (listener) => vscode.workspace.onDidGrantWorkspaceTrust(listener),
    showErrorMessage: (message) => vscode.window.showErrorMessage(message)
  };
}

let activeController: { deactivate(): Promise<void> } | undefined;

export async function activate(context: vscodeTypes.ExtensionContext): Promise<void> {
  const vscode = createRequire(import.meta.url)("vscode") as typeof vscodeTypes;
  const controller = createExtensionController(createVSCodeHost(vscode, context));
  activeController = controller;
  const publishDurableMarker = (
    process.env.UNIT_TEST_IDE_HOST_SMOKE === "1" &&
    (context.extensionMode === vscode.ExtensionMode.Development || context.extensionMode === vscode.ExtensionMode.Test)
  )
    ? () => writeDevelopmentActivationMarker(context.globalStorageUri.fsPath)
    : undefined;
  await activateControllerWithMarker(controller, undefined, publishDurableMarker);
}

export async function deactivate(): Promise<void> {
  const controller = activeController;
  activeController = undefined;
  if (controller) await controller.deactivate();
}
