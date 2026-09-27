import type { CoverageSourceSnapshotV14 } from "@unit-test-ide/test-client";
import type { ServiceStatus, TrustState } from "./contracts.js";
import type { ExtensionProtocolClient } from "./protocol-client.js";
import type { CoverageControllerState } from "./coverage-controller.js";
import { openCoverageHtml } from "./coverage-viewer.js";
import { openCoverageSource as verifyAndOpenCoverageSource } from "./coverage-sources.js";
import { redactServiceError } from "./service-resources.js";
import type { GenerationPreviewBinding, GenerationSelection, TestGenerationControllerState } from "./test-generation-controller.js";
import { createGenerationDiffReview, redactGenerationDiffPaths } from "./test-generation-diff.js";
import { buildGenerationResults, renderGenerationResults } from "./test-generation-results.js";
import { TestGenerationScopeV15 } from "@unit-test-ide/test-client";

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
  workspaceRoot?: () => string | undefined
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
}

function generationSelection(value: unknown, scope: GenerationSelection["scope"]): GenerationSelection {
  const input = typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
  return {
    scope,
    ...(typeof input.file === "string" ? { file: input.file } : {}),
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
    if (value === undefined && scope !== TestGenerationScopeV15.Workspace) {
      if (!host.pickGenerationSelection) {
        await host.showErrorMessage("Unit Test: Select a file, symbol, target, or coverage gap before generating tests.");
        return;
      }
      value = await host.pickGenerationSelection(scope);
      if (value === undefined) return;
    }
    await start(generationSelection(value, scope));
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
