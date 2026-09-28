import type { CoverageSourceSnapshotV14, ManagedTestRecordPageV16 } from "@unit-test-ide/test-client";
import type { ServiceStatus, TrustState } from "./contracts.js";
import type { ExtensionProtocolClient } from "./protocol-client.js";
import type { CoverageControllerState } from "./coverage-controller.js";
import type { CoverageFilter, CoverageTreeNode } from "./coverage-detail-tree.js";
import { openCoverageHtml } from "./coverage-viewer.js";
import { openCoverageSource as verifyAndOpenCoverageSource } from "./coverage-sources.js";
import { redactServiceError } from "./service-resources.js";
import type { GenerationPreviewBinding, GenerationSelection, ManagedGenerationSelection, TestGenerationControllerState } from "./test-generation-controller.js";
import { createGenerationDiffReview, createManagedCaseReview, redactGenerationDiffPaths } from "./test-generation-diff.js";
import { buildGenerationResults, renderGenerationResults, renderManagedRecords } from "./test-generation-results.js";
import type { ManagedApplyOutcome, ManagedChoice, ManagedReviewState, ManagedStatusFilter } from "./managed-test-review.js";
import { TestGenerationScopeV15 } from "@unit-test-ide/test-client";
import { isAbsolute, relative, resolve } from "node:path";

export interface DisposableLike {
  dispose(): unknown;
}

export interface CommandContext {
  subscriptions: DisposableLike[];
}

export interface OutputChannelLike extends DisposableLike {
  appendLine(value: string): void;
}

export interface StatusBarLike extends DisposableLike {
  text: string;
  show(): void;
}

export interface LifecycleSession {
  readonly client: ExtensionProtocolClient;
}

export interface CommandManager {
  readonly status: ServiceStatus;
  readonly session: LifecycleSession | undefined;
  start(): Promise<LifecycleSession>;
  stop(): Promise<void>;
}

export interface CommandStatus {
  readonly trustState: TrustState;
  isActive(): boolean;
  refreshTrust(): TrustState;
  projectService(status: ServiceStatus): void;
}

export interface CommandHost {
  registerCommand(command: string, handler: (...args: unknown[]) => void | Promise<void>): DisposableLike;
  showErrorMessage(message: string): void | PromiseLike<unknown>;
}

export interface TestGenerationCommandHost extends CommandHost {
  showInformationMessage?: (message: string) => void | PromiseLike<unknown>;
  confirmGeneration?: (message: string) => boolean | PromiseLike<boolean>;
  openGenerationDiff?: (title: string, diff: string) => void | PromiseLike<void>;
  pickGenerationCandidate?: (candidates: readonly GenerationCandidateChoice[]) => GenerationCandidateChoice | undefined | PromiseLike<GenerationCandidateChoice | undefined>;
  pickGenerationSelection?: (scope: GenerationSelection["scope"]) => unknown | PromiseLike<unknown>;
  workspaceRoot?: () => string | undefined;
}

export interface GenerationCandidateChoice {
  readonly candidateId: string;
  readonly kind: string;
  readonly label: string;
}

export interface TestGenerationCommandController {
  getState(): TestGenerationControllerState;
  start(selection: GenerationSelection): Promise<TestGenerationControllerState>;
  refresh(): Promise<TestGenerationControllerState>;
  accept(candidateId: string, confirmCharacterization?: boolean, displayed?: GenerationPreviewBinding): Promise<TestGenerationControllerState>;
  cancel(): Promise<TestGenerationControllerState>;
}

export interface ManagedGenerationCommandController {
  startManaged(selection: ManagedGenerationSelection): Promise<unknown>;
}

export interface ManagedReviewCommandController {
  available(): Promise<boolean>;
  list(status?: ManagedStatusFilter): Promise<ManagedTestRecordPageV16>;
  load(reviewId: string): Promise<ManagedReviewState>;
  getState(): ManagedReviewState;
  choose(caseId: string, choice: ManagedChoice): ManagedReviewState;
  markDisplayed(reviewDigest: string): ManagedReviewState;
  apply(digest: string): Promise<ManagedApplyOutcome>;
  reject(): void;
}

export interface ManagedTestCommandHost extends TestGenerationCommandHost {
  openManagedCaseDiff?: (title: string, diff: string) => void | PromiseLike<void>;
  pickManagedConflictChoice?: (caseId: string, choices: readonly ManagedChoice[]) => ManagedChoice | undefined | PromiseLike<ManagedChoice | undefined>;
  pickManagedReviewId?: (records: readonly { caseId: string; reviewId: string }[]) => string | undefined | PromiseLike<string | undefined>;
}

export interface CoverageCommandHost extends CommandHost {
  openCoverageHtml?: (html: string) => void | PromiseLike<void>;
  openCoverageSource?: (path: string) => void | PromiseLike<void>;
  pickCoverageSource?: (sources: readonly CoverageSourceSnapshotV14[]) => CoverageSourceSnapshotV14 | undefined | PromiseLike<CoverageSourceSnapshotV14 | undefined>;
  showInformationMessage?: (message: string) => void | PromiseLike<unknown>;
}

export interface CoverageCommandController {
  getState(): CoverageControllerState;
  startCurrent(): Promise<CoverageControllerState>;
  refreshCurrent(): Promise<CoverageControllerState>;
}

export interface CoverageDetailCommandController {
  available(): boolean;
  select(node: CoverageTreeNode): Promise<void>;
  filter(value: CoverageFilter): void;
  loadMore?(node: CoverageTreeNode): Promise<void>;
}

export type ClientProvider = () => ExtensionProtocolClient | undefined;

function decodeCoverageSource(value: unknown): CoverageSourceSnapshotV14 | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const candidate = value as { uri?: unknown; sha256?: unknown };
  return typeof candidate.uri === "string" && typeof candidate.sha256 === "string"
    ? { uri: candidate.uri, sha256: candidate.sha256 }
    : undefined;
}

const BLOCKED_MESSAGES: Record<Exclude<TrustState, "trusted">, string> = {
  "no-workspace": "Unit Test: Open a workspace to use the service.",
  "blocked-multi-root": "Unit Test: Multi-root workspaces are not supported.",
  "blocked-untrusted": "Unit Test: Trust this workspace to use the service."
};

export async function presentManagerError(
  host: Pick<CommandHost, "showErrorMessage">,
  manager: Pick<CommandManager, "status">,
  fallback: string
): Promise<void> {
  const message = manager.status.state === "failed" && manager.status.detail
    ? manager.status.detail
    : fallback;
  await host.showErrorMessage(redactServiceError(message, []).message);
}

export function registerCommands(
  context: CommandContext,
  manager: CommandManager,
  clientProvider: ClientProvider,
  output: OutputChannelLike,
  status: CommandStatus,
  host: CommandHost,
  onServiceChanged?: (state: "started" | "stopped") => void | Promise<void>
): void {
  const currentAuthorization = (): { allowed: boolean; message?: string } => {
    if (!status.isActive()) return { allowed: false };
    const trustState = status.refreshTrust();
    return trustState === "trusted"
      ? { allowed: true }
      : { allowed: false, message: BLOCKED_MESSAGES[trustState] };
  };

  const requireTrustedWorkspace = async (): Promise<boolean> => {
    const authorization = currentAuthorization();
    if (authorization.allowed) return true;
    if (authorization.message) await host.showErrorMessage(authorization.message);
    return false;
  };

  const startService = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    status.projectService({ state: "starting" });
    try {
      await manager.start();
      await onServiceChanged?.("started");
    } catch {
      await presentManagerError(host, manager, "Unit Test: Service start failed.");
    } finally {
      status.projectService(manager.status);
    }
  };

  const stopService = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    status.projectService({ state: "stopping" });
    try {
      await manager.stop();
      await onServiceChanged?.("stopped");
    } catch {
      await presentManagerError(host, manager, "Unit Test: Service stop failed.");
    } finally {
      status.projectService(manager.status);
    }
  };

  const inspectWorkspace = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    const client = clientProvider();
    if (!client) {
      await host.showErrorMessage("Unit Test: Service is not running.");
      return;
    }
    try {
      output.appendLine(JSON.stringify(await client.inspectWorkspace(), null, 2));
    } catch {
      await presentManagerError(host, manager, "Unit Test: Workspace inspection failed.");
      status.projectService(manager.status);
    }
  };

  context.subscriptions.push(
    host.registerCommand("unitTestIde.startService", startService),
    host.registerCommand("unitTestIde.stopService", stopService),
    host.registerCommand("unitTestIde.inspectWorkspace", inspectWorkspace)
  );
}

export function registerCoverageCommands(
  context: CommandContext,
  controller: CoverageCommandController,
  clientProvider: ClientProvider,
  status: CommandStatus,
  host: CoverageCommandHost,
  output: OutputChannelLike,
  workspaceRoot?: () => string | undefined,
  detail?: CoverageDetailCommandController
): void {
  const requireTrusted = async (): Promise<boolean> => {
    if (!status.isActive()) return false;
    const trust = status.refreshTrust();
    if (trust === "trusted") return true;
    await host.showErrorMessage(BLOCKED_MESSAGES[trust]);
    return false;
  };

  const runCoverage = async (): Promise<void> => {
    if (!await requireTrusted()) return;
    try {
      const state = await controller.startCurrent();
      output.appendLine(JSON.stringify({
        state: state.state,
        coverageRunId: state.coverageRunId,
        reportId: state.reportId,
        completeness: state.completeness,
        summary: state.summary,
        toolProvenance: state.toolProvenance,
        sources: state.sources
      }));
      await host.showInformationMessage?.("Unit Test: Coverage report is available.");
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };

  const refreshCoverage = async (): Promise<void> => {
    if (!await requireTrusted()) return;
    try {
      await controller.refreshCurrent();
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };

  const openReport = async (): Promise<void> => {
    if (!await requireTrusted()) return;
    const state = controller.getState();
    if (state.state !== "available" || !state.taskId) {
      await host.showErrorMessage("Unit Test: No completed coverage report is available.");
      return;
    }
    if (!host.openCoverageHtml) {
      await host.showErrorMessage("Unit Test: Coverage report viewer is unavailable.");
      return;
    }
    const client = clientProvider();
    if (!client?.listArtifacts || !client.readArtifact) {
      await host.showErrorMessage("Unit Test: Protocol artifact access is unavailable.");
      return;
    }
    try {
      const artifacts = await client.listArtifacts(state.taskId);
      const htmlArtifacts = artifacts.items.filter((artifact) => artifact.kind === "coverage-html");
      if (htmlArtifacts.length !== 1) throw new Error("Expected exactly one coverage HTML artifact.");
      const artifact = htmlArtifacts[0]!;
      const bytes = await client.readArtifact(artifact.artifactId);
      await openCoverageHtml({ openCoverageHtml: host.openCoverageHtml }, { kind: artifact.kind, bytes });
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };

  const openSource = async (value?: unknown): Promise<void> => {
    if (!await requireTrusted()) return;
    if (!host.openCoverageSource) {
      await host.showErrorMessage("Unit Test: Coverage source viewer is unavailable.");
      return;
    }
    const root = workspaceRoot?.();
    let source = decodeCoverageSource(value);
    if (value === undefined) {
      const candidates = controller.getState().sources ?? [];
      if (candidates.length === 0 || !host.pickCoverageSource) {
        await host.showErrorMessage("Unit Test: No coverage sources are available to open.");
        return;
      }
      source = await host.pickCoverageSource(candidates);
      if (!source) return;
    }
    if (!root || !source) {
      await host.showErrorMessage("Unit Test: A valid coverage source selection is required.");
      return;
    }
    try {
      await verifyAndOpenCoverageSource({ openCoverageSource: host.openCoverageSource }, root, source);
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };

  context.subscriptions.push(
    host.registerCommand("unitTestIde.runCoverage", runCoverage),
    host.registerCommand("unitTestIde.refreshCoverage", refreshCoverage),
    host.registerCommand("unitTestIde.openCoverageReport", openReport),
    host.registerCommand("unitTestIde.openCoverageSource", openSource)
  );
  if (detail) context.subscriptions.push(
    host.registerCommand("unitTestIde.openCoverageDetail", async (value) => {
      if (!await requireTrusted() || !detail.available()) return;
      if (typeof value !== "object" || value === null || !["file", "function"].includes((value as CoverageTreeNode).kind)) return;
      try { await detail.select(value as CoverageTreeNode); }
      catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
    }),
    host.registerCommand("unitTestIde.filterCoverageDetails", async (value) => {
      if (!await requireTrusted() || !detail.available()) return;
      if (value === "all" || value === "uncovered" || value === "regressed" || value === "incomplete") detail.filter(value);
    }),
    host.registerCommand("unitTestIde.loadMoreCoverageDetails", async (value) => {
      if (!await requireTrusted() || !detail.available() || !detail.loadMore) return;
      if (typeof value !== "object" || value === null || (value as CoverageTreeNode).kind !== "load-more") return;
      try { await detail.loadMore(value as CoverageTreeNode); }
      catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
    }),
    ...(["all", "uncovered", "regressed", "incomplete"] as const).map((filter) => host.registerCommand(`unitTestIde.coverageFilter.${filter}`, async () => {
      if (await requireTrusted() && detail.available()) detail.filter(filter);
    }))
  );
}

function safeRelativePath(value: string, workspaceRoot: string | undefined): string | undefined {
  const normalized = value.replaceAll("\\", "/");
  if (!workspaceRoot && isAbsolute(value)) return undefined;
  const candidate = workspaceRoot && isAbsolute(value)
    ? relative(resolve(workspaceRoot), resolve(value))
    : normalized;
  const relativePath = candidate.replaceAll("\\", "/");
  if (!relativePath || relativePath === "." || relativePath.startsWith("../") || relativePath === ".." || relativePath.startsWith("/") || /^[A-Za-z]:\//.test(relativePath)) return undefined;
  return relativePath;
}

function generationSelection(value: unknown, scope: GenerationSelection["scope"], workspaceRoot?: string): GenerationSelection | undefined {
  const input = typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
  const uriPath = typeof input.fsPath === "string" ? input.fsPath : typeof input.path === "string" ? input.path : undefined;
  const file = typeof input.file === "string" ? safeRelativePath(input.file, workspaceRoot) : uriPath ? safeRelativePath(uriPath, workspaceRoot) : undefined;
  if (scope === TestGenerationScopeV15.File && value !== undefined && !file) return undefined;
  if (scope !== TestGenerationScopeV15.File && uriPath) return undefined;
  return {
    scope,
    ...(file ? { file } : {}),
    ...(typeof input.symbolId === "string" ? { symbolId: input.symbolId } : {}),
    ...(typeof input.targetId === "string" ? { targetId: input.targetId } : {}),
    ...(typeof input.coverageReportId === "string" ? { coverageReportId: input.coverageReportId } : {}),
    ...(typeof input.framework === "string" ? { framework: input.framework as GenerationSelection["framework"] } : {}),
    ...(typeof input.goals === "object" && input.goals !== null ? { goals: input.goals as GenerationSelection["goals"] } : {}),
    ...(typeof input.budgets === "object" && input.budgets !== null ? { budgets: input.budgets as GenerationSelection["budgets"] } : {})
  };
}

export function registerTestGenerationCommands(
  context: CommandContext,
  controller: TestGenerationCommandController,
  status: CommandStatus,
  host: TestGenerationCommandHost,
  output: OutputChannelLike
): void {
  const requireTrusted = async (): Promise<boolean> => {
    if (!status.isActive()) return false;
    const trust = status.refreshTrust();
    if (trust === "trusted") return true;
    await host.showErrorMessage(BLOCKED_MESSAGES[trust]);
    return false;
  };

  const start = async (selection: GenerationSelection): Promise<void> => {
    if (!await requireTrusted()) return;
    try {
      await controller.start(selection);
      await host.showInformationMessage?.("Unit Test: Test generation started.");
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };
  const startForScope = async (value: unknown, scope: GenerationSelection["scope"]): Promise<void> => {
    const requiresInput = scope !== TestGenerationScopeV15.Workspace;
    let selection = value === undefined && requiresInput ? undefined : generationSelection(value, scope, host.workspaceRoot?.());
    if (value !== undefined && !selection) {
      const record = typeof value === "object" && value !== null ? value as Record<string, unknown> : undefined;
      const uriPath = typeof record?.fsPath === "string" ? record.fsPath : typeof record?.path === "string" ? record.path : undefined;
      const insideWorkspace = uriPath !== undefined && safeRelativePath(uriPath, host.workspaceRoot?.()) !== undefined;
      if (insideWorkspace && scope !== TestGenerationScopeV15.File) {
        // A URI identifies a source context, not an opaque symbol/target ID;
        // route it to the picker for that same scope.
        value = undefined;
        selection = undefined;
      } else {
        await host.showErrorMessage("Unit Test: The selected generation context is invalid or outside the workspace.");
        return;
      }
    }
    if (!selection && scope === TestGenerationScopeV15.Workspace) {
      await host.showErrorMessage("Unit Test: A valid workspace selection is required.");
      return;
    }
    if (!selection && scope !== TestGenerationScopeV15.Workspace) {
      if (!host.pickGenerationSelection) {
        await host.showErrorMessage("Unit Test: Select a file, symbol, target, or coverage gap before generating tests.");
        return;
      }
      value = await host.pickGenerationSelection(scope);
      if (value === undefined) {
        await host.showErrorMessage(`Unit Test: No usable ${scope} picker is available in this workspace.`);
        return;
      }
      selection = generationSelection(value, scope, host.workspaceRoot?.());
      if (!selection) {
        await host.showErrorMessage("Unit Test: The selected generation target is invalid or outside the workspace.");
        return;
      }
    }
    await start(selection!);
  };
  const review = async (): Promise<void> => {
    if (!await requireTrusted()) return;
    try {
      const state = await controller.refresh();
      if (!state.run || !state.candidates) {
        await host.showErrorMessage("Unit Test: No generated-test preview is available.");
        return;
      }
      const model = buildGenerationResults(state.run, state.candidates);
      output.appendLine(renderGenerationResults(model));
      const diff = createGenerationDiffReview(state.run);
      if (host.openGenerationDiff) await host.openGenerationDiff(diff.title, redactGenerationDiffPaths(diff.content));
      else await host.showInformationMessage?.("Unit Test: Generated-test preview is available in the output.");
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };
  const accept = async (value?: unknown): Promise<void> => {
    if (!await requireTrusted()) return;
    const input = typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
    try {
      // Refresh before showing the diff or asking for confirmation. The local
      // preview is display-only and cannot authorize a write.
      const state = await controller.refresh();
      let candidateId = typeof input.candidateId === "string" ? input.candidateId : undefined;
      if (!candidateId && state.candidates && host.pickGenerationCandidate) {
        const picked = await host.pickGenerationCandidate(state.candidates.items.map((item) => ({ candidateId: item.candidateId, kind: item.kind, label: `${item.kind}: ${item.candidateId}` })));
        candidateId = picked?.candidateId;
      }
      const candidate = candidateId === undefined ? undefined : state.candidates?.items.find((item) => item.candidateId === candidateId);
      if (!state.run || !candidate || !candidateId) {
        await host.showErrorMessage("Unit Test: The selected generated-test candidate is stale.");
        return;
      }
      let confirmCharacterization = input.confirmCharacterization === true;
      const diff = createGenerationDiffReview(state.run);
      if (host.openGenerationDiff) await host.openGenerationDiff(diff.title, redactGenerationDiffPaths(diff.content));
      else output.appendLine(redactGenerationDiffPaths(diff.content));
      if (!host.confirmGeneration) {
        await host.showErrorMessage("Unit Test: An explicit generated-test confirmation is required.");
        return;
      }
      if (!await host.confirmGeneration("Accept the exact generated-test diff?")) return;
      if (candidate.kind === "characterization") {
        if (!await host.confirmGeneration("This is a characterization test based on observed behavior. Confirm again to accept it.")) return;
        confirmCharacterization = true;
      }
      const displayed: GenerationPreviewBinding = {
        runId: state.run.runId,
        candidateId: candidate.candidateId,
        candidateSetDigest: state.run.preview?.candidateSetDigest ?? "",
        candidateArtifactDigest: candidate.artifactDigest,
        candidateCodeDigest: candidate.codeDigest,
        diffDigest: state.run.preview?.diffDigest ?? "",
        confirmationDigest: state.run.preview?.confirmationDigest ?? ""
      };
      await controller.accept(candidateId, confirmCharacterization, displayed);
      await host.showInformationMessage?.("Unit Test: Generated tests accepted.");
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };
  const cancel = async (): Promise<void> => {
    if (!await requireTrusted()) return;
    try {
      await controller.cancel();
      await host.showInformationMessage?.("Unit Test: Test generation cancelled.");
    } catch (error) {
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };

  context.subscriptions.push(
    host.registerCommand("unitTestIde.generateTests", () => startForScope(undefined, TestGenerationScopeV15.Workspace)),
    host.registerCommand("unitTestIde.generateTestsForSymbol", (value) => startForScope(value, TestGenerationScopeV15.Symbol)),
    host.registerCommand("unitTestIde.generateTestsForFile", (value) => startForScope(value, TestGenerationScopeV15.File)),
    host.registerCommand("unitTestIde.generateTestsForTarget", (value) => startForScope(value, TestGenerationScopeV15.Target)),
    host.registerCommand("unitTestIde.generateTestsForCoverageGap", (value) => startForScope(value, TestGenerationScopeV15.CoverageGap)),
    host.registerCommand("unitTestIde.reviewGeneratedTests", review),
    host.registerCommand("unitTestIde.acceptGeneratedTests", accept),
    host.registerCommand("unitTestIde.cancelTestGeneration", cancel)
  );
}

const MANAGED_CHOICES = ["keep-current", "use-generated", "convert-to-manual"] as const;
const MANAGED_STATUSES = new Set<string>(["current", "stale", "conflicted", "orphaned", "invalid"]);
const ID32 = /^[0-9a-f]{32}$/;

/** Register only while a trusted v1.6 managed-test capability is advertised. */
export function registerManagedTestCommands(
  context: CommandContext,
  generation: ManagedGenerationCommandController,
  review: ManagedReviewCommandController,
  status: CommandStatus,
  host: ManagedTestCommandHost,
  output: OutputChannelLike
): DisposableLike[] {
  const guard = async (): Promise<boolean> => {
    if (!status.isActive()) return false;
    const trust = status.refreshTrust();
    if (trust !== "trusted") { await host.showErrorMessage(BLOCKED_MESSAGES[trust]); return false; }
    if (!await review.available()) { await host.showErrorMessage("Unit Test: Protocol v1.6 managed tests are unavailable."); return false; }
    return true;
  };
  const generate = async (value: unknown, scope: ManagedGenerationSelection["scope"]): Promise<void> => {
    if (!await guard()) return;
    const input = typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
    const functionId = typeof input.functionId === "string" ? input.functionId : input.kind === "function" && typeof input.id === "string" ? input.id : undefined;
    const fileId = typeof input.fileId === "string" ? input.fileId : input.kind === "file" && typeof input.id === "string" ? input.id : undefined;
    const selection = scope === "symbol" && functionId && ID32.test(functionId)
      ? { scope, functionId } as const
      : scope === "file" && fileId && ID32.test(fileId)
        ? { scope, fileId } as const
        : scope === "coverage-gap" && typeof input.coverageGapId === "string" && ID32.test(input.coverageGapId) && typeof input.coverageReportId === "string" && ID32.test(input.coverageReportId)
          ? { scope, coverageGapId: input.coverageGapId, coverageReportId: input.coverageReportId } as const
          : undefined;
    if (!selection) { await host.showErrorMessage("Unit Test: Select a current, authoritative coverage function, file, or gap ID."); return; }
    try { await generation.startManaged(selection); }
    catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
  };
  const list = async (value?: unknown): Promise<void> => {
    if (!await guard()) return;
    const statusFilter = typeof value === "string" && MANAGED_STATUSES.has(value) ? value as ManagedStatusFilter : undefined;
    try { output.appendLine(renderManagedRecords((await review.list(statusFilter)).items)); }
    catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
  };
  const showReview = async (value?: unknown): Promise<void> => {
    if (!await guard()) return;
    try {
      const argument = typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
      let reviewId = typeof argument.reviewId === "string" && ID32.test(argument.reviewId) ? argument.reviewId : undefined;
      if (!reviewId && host.pickManagedReviewId) {
        const page = await review.list("conflicted");
        reviewId = await host.pickManagedReviewId(page.items.filter((item) => item.reviewId && ID32.test(item.reviewId)).map((item) => ({ caseId: item.caseId, reviewId: item.reviewId! })));
      }
      if (!reviewId || !ID32.test(reviewId)) throw new Error("Select an authoritative managed review ID.");
      const loaded = await review.load(reviewId);
      for (const item of loaded.review?.cases ?? []) {
        const display = createManagedCaseReview(item);
        output.appendLine(`${display.title} [${item.status}] accepted=${display.panes.accepted} current=${display.panes.current} generated=${display.panes.generated}`);
        if (host.openManagedCaseDiff) await host.openManagedCaseDiff(display.title, redactGenerationDiffPaths(display.content));
        else if (host.openGenerationDiff) await host.openGenerationDiff(display.title, redactGenerationDiffPaths(display.content));
        else output.appendLine(redactGenerationDiffPaths(display.content));
        if (item.status !== "conflicted") continue;
        const choice = await host.pickManagedConflictChoice?.(item.caseId, MANAGED_CHOICES);
        if (choice !== undefined) review.choose(item.caseId, choice);
      }
      if (loaded.review) review.markDisplayed(loaded.review.reviewDigest);
      if (!review.getState().canApply) await host.showInformationMessage?.("Unit Test: Resolve every managed-test conflict before Apply.");
    } catch (error) {
      try { review.reject(); } catch { /* An Apply already in flight cannot be revoked here. */ }
      await host.showErrorMessage(redactServiceError(error, []).message);
    }
  };
  const apply = async (): Promise<void> => {
    if (!await guard()) return;
    const state = review.getState();
    if (!state.canApply || !state.review) { await host.showErrorMessage("Unit Test: Resolve and review every managed-test change before Apply."); return; }
    if (!host.confirmGeneration || !await host.confirmGeneration("Apply this exact managed-test review?")) return;
    try {
      const outcome = await review.apply(state.review.reviewDigest);
      if (outcome.state === "confirmed") await host.showInformationMessage?.("Unit Test: Managed-test review applied and confirmed by the service.");
      else if (outcome.state === "uncertain") await host.showInformationMessage?.("Unit Test: Apply outcome is uncertain. Reconnect and reconcile the managed-test record before retrying; the service may have committed it.");
      else await host.showInformationMessage?.("Unit Test: Managed-test Apply cancelled before dispatch; no Apply request was sent.");
    }
    catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
  };
  const reject = async (): Promise<void> => {
    if (!await guard()) return;
    try {
      review.reject();
      await host.showInformationMessage?.("Unit Test: Managed-test review rejected locally; no files were changed.");
    } catch (error) { await host.showErrorMessage(redactServiceError(error, []).message); }
  };
  const registered = [
    host.registerCommand("unitTestIde.generateManagedTestsForFunction", (value) => generate(value, "symbol")),
    host.registerCommand("unitTestIde.generateManagedTestsForFile", (value) => generate(value, "file")),
    host.registerCommand("unitTestIde.generateManagedTestsForCoverageGap", (value) => generate(value, "coverage-gap")),
    host.registerCommand("unitTestIde.listManagedTests", list),
    host.registerCommand("unitTestIde.reviewManagedTests", showReview),
    host.registerCommand("unitTestIde.applyManagedReview", apply),
    host.registerCommand("unitTestIde.rejectManagedReview", reject)
  ];
  context.subscriptions.push(...registered);
  return registered;
}
