import { randomBytes } from "node:crypto";
import {
  TestGenerationFrameworkV15,
  TestGenerationScopeV15,
  TestGenerationFrameworkV16,
  TestGenerationScopeV16,
  type TestGenerationAcceptInput,
  type TestGenerationCandidatePageV15,
  type TestGenerationEventPageV15,
  type TestGenerationRunV15,
  type TestGenerationStartInput,
  type TestGenerationStartInputV16
} from "@unit-test-ide/test-client";
import type { TrustState } from "./contracts.js";
import type { ExtensionGenerationProtocolClient, ExtensionProtocolClient } from "./protocol-client.js";

export type GenerationSelection = {
  readonly scope: TestGenerationStartInput["scope"];
  readonly file?: TestGenerationStartInput["file"];
  readonly symbolId?: TestGenerationStartInput["symbolId"];
  readonly targetId?: TestGenerationStartInput["targetId"];
  readonly coverageReportId?: TestGenerationStartInput["coverageReportId"];
  readonly framework?: TestGenerationStartInput["framework"];
  readonly goals?: TestGenerationStartInput["goals"];
  readonly budgets?: TestGenerationStartInput["budgets"];
};

export type ManagedGenerationSelection =
  | { readonly scope: "symbol"; readonly functionId: string; readonly coverageReportId: string; readonly fileId?: never; readonly coverageGapId?: never }
  | { readonly scope: "file"; readonly fileId: string; readonly coverageReportId: string; readonly functionId?: never; readonly coverageGapId?: never }
  | { readonly scope: "coverage-gap"; readonly coverageGapId: string; readonly coverageReportId: string; readonly functionId?: never; readonly fileId?: never };

export interface GenerationContext {
  readonly trust: TrustState;
  readonly client?: ExtensionProtocolClient;
  readonly projectId?: string;
  readonly workspaceGeneration?: string;
}

export type TestGenerationControllerStateName = "idle" | "unavailable" | "starting" | "running" | "preview" | "accepting" | "accepted" | "cancelled" | "failed";

export interface TestGenerationControllerState {
  readonly state: TestGenerationControllerStateName;
  readonly run?: TestGenerationRunV15;
  readonly candidates?: TestGenerationCandidatePageV15;
  readonly events?: TestGenerationEventPageV15;
  readonly detail?: string;
}

/** Identity of the preview that the user actually inspected before confirmation. */
export interface GenerationPreviewBinding {
  readonly runId: string;
  readonly candidateId: string;
  readonly candidateSetDigest: string;
  readonly candidateArtifactDigest: string;
  readonly candidateCodeDigest: string;
  readonly diffDigest: string;
  readonly confirmationDigest: string;
}

export interface TestGenerationControllerOptions {
  readonly readContext: () => GenerationContext;
  readonly onStateChanged?: (state: TestGenerationControllerState) => void;
  readonly maxEventPageSize?: number;
  readonly maxCandidatePageSize?: number;
  readonly sleep?: (milliseconds: number) => Promise<void>;
}

const DEFAULT_BUDGETS: TestGenerationStartInput["budgets"] = {
  candidateCount: 8,
  concurrency: 1,
  memoryMiB: 512,
  wallTimeMs: 60_000
};
const DEFAULT_GOALS: TestGenerationStartInput["goals"] = {
  branchPercent: 100,
  functionPercent: 100,
  linePercent: 100
};

function cloneState(state: TestGenerationControllerState): TestGenerationControllerState {
  return {
    ...state,
    run: state.run === undefined ? undefined : { ...state.run, preview: state.run.preview === undefined ? undefined : { ...state.run.preview } },
    candidates: state.candidates === undefined ? undefined : { ...state.candidates, items: state.candidates.items.map((candidate) => ({ ...candidate, diagnostics: candidate.diagnostics.map((diagnostic) => ({ ...diagnostic })), plannedEdits: candidate.plannedEdits.map((edit) => ({ ...edit })), baselineCoverage: { ...candidate.baselineCoverage }, deltaCoverage: { ...candidate.deltaCoverage }, assertionProvenance: { ...candidate.assertionProvenance } })) },
    events: state.events === undefined ? undefined : { ...state.events, items: state.events.items.map((event) => ({ ...event })) }
  };
}

function generationClient(client: ExtensionProtocolClient | undefined): ExtensionGenerationProtocolClient {
  if (!client?.getCapabilities || !client.listTestGenerationTargets || !client.startTestGeneration || !client.getTestGenerationRun || !client.cancelTestGeneration || !client.replayTestGenerationEvents || !client.listTestGenerationCandidates || !client.acceptTestGeneration) {
    throw new Error("Protocol v1.5 test-generation capability is unavailable.");
  }
  return client as ExtensionGenerationProtocolClient;
}

function isTerminal(state: TestGenerationRunV15["state"]): boolean {
  return state === "awaiting_confirmation" || state === "accepted" || state === "cancelled" || state === "failed" || state === "rejected";
}

export class TestGenerationController {
  #state: TestGenerationControllerState = { state: "idle" };
  #closed = false;
  #operation = 0;
  #epoch = 0;
  #runId: string | undefined;
  #lastSequence = 0;
  #events = new Map<number, TestGenerationEventPageV15["items"][number]>();

  constructor(private readonly options: TestGenerationControllerOptions) {}

  getState(): TestGenerationControllerState { return cloneState(this.#state); }

  async start(selection: GenerationSelection): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    const operation = ++this.#operation;
    const epoch = ++this.#epoch;
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client, epoch);
    this.#assertFresh(epoch, context, client);
    const request: TestGenerationStartInput = {
      ...selection,
      idempotencyKey: randomBytes(16).toString("hex"),
      projectId: context.projectId!,
      workspaceGeneration: context.workspaceGeneration!,
      framework: selection.framework ?? TestGenerationFrameworkV15.Auto,
      goals: selection.goals ?? DEFAULT_GOALS,
      budgets: selection.budgets ?? DEFAULT_BUDGETS
    };
    this.#publish({ state: "starting" });
    try {
      const run = await client.startTestGeneration(request);
      this.#assertCurrent(operation);
      this.#assertFresh(epoch, context, client);
      this.#setRun(run, epoch);
      await this.refresh();
      return this.getState();
    } catch (error) {
      if (operation === this.#operation && this.#state.state !== "unavailable") this.#publish({ state: "failed", detail: errorMessage(error) });
      throw error;
    }
  }

  async startManaged(selection: ManagedGenerationSelection): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    const ids = selection.scope === "symbol" ? [selection.functionId, selection.coverageReportId] : selection.scope === "file" ? [selection.fileId, selection.coverageReportId] : [selection.coverageGapId, selection.coverageReportId];
    if (ids.some((id) => !/^[0-9a-f]{32}$/.test(id))) throw new Error("Invalid authoritative managed-generation selection ID.");
    const operation = ++this.#operation;
    const epoch = ++this.#epoch;
    const context = this.#assertContext();
    const client = generationClient(context.client);
    const capabilities = await client.getCapabilities();
    this.#assertFresh(epoch, context, client);
    if (!("managedTests" in capabilities) || capabilities.managedTests !== true || capabilities.testGeneration !== true) throw new Error("Protocol v1.6 managed test generation is unavailable.");
    const request: TestGenerationStartInputV16 = {
      ...selection,
      scope: selection.scope === "symbol" ? TestGenerationScopeV16.Symbol : selection.scope === "file" ? TestGenerationScopeV16.File : TestGenerationScopeV16.CoverageGap,
      idempotencyKey: randomBytes(16).toString("hex"),
      projectId: context.projectId!,
      workspaceGeneration: context.workspaceGeneration!,
      framework: TestGenerationFrameworkV16.Auto,
      goals: DEFAULT_GOALS,
      budgets: DEFAULT_BUDGETS
    };
    this.#publish({ state: "starting" });
    try {
      const run = await client.startTestGeneration(request);
      this.#assertCurrent(operation);
      this.#assertFresh(epoch, context, client);
      this.#setRun(run as unknown as TestGenerationRunV15, epoch);
      return this.refresh();
    } catch (error) {
      if (operation === this.#operation && this.#state.state !== "unavailable") this.#publish({ state: "failed", detail: errorMessage(error) });
      throw error;
    }
  }

  async restore(runId: string): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    const epoch = ++this.#epoch;
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client, epoch);
    this.#assertFresh(epoch, context, client);
    const run = await client.getTestGenerationRun(runId);
    this.#assertFresh(epoch, context, client);
    this.#assertRunContext(run, context);
    this.#setRun(run, epoch);
    await this.refresh();
    return this.getState();
  }

  async listTargets(input?: { projectId?: string; workspaceGeneration?: string }): Promise<Awaited<ReturnType<ExtensionGenerationProtocolClient["listTestGenerationTargets"]>>> {
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client);
    return client.listTestGenerationTargets({ projectId: input?.projectId ?? context.projectId!, workspaceGeneration: input?.workspaceGeneration ?? context.workspaceGeneration! });
  }

  async refresh(): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    const epoch = ++this.#epoch;
    if (!this.#runId) return this.getState();
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client, epoch);
    this.#assertFresh(epoch, context, client);
    const run = await client.getTestGenerationRun(this.#runId);
    this.#assertFresh(epoch, context, client);
    this.#assertRunContext(run, context);
    this.#setRun(run, epoch);
    const replay = await client.replayTestGenerationEvents({ runId: run.runId, afterSequence: this.#lastSequence, limit: this.options.maxEventPageSize ?? 128 });
    this.#assertFresh(epoch, context, client);
    this.#mergeEvents(replay);
    if (run.state === "awaiting_confirmation") {
      const candidates = await client.listTestGenerationCandidates({ runId: run.runId, limit: this.options.maxCandidatePageSize ?? 128 });
      this.#assertFresh(epoch, context, client);
      this.#publish({ state: "preview", run, candidates, events: this.#eventPage() });
    } else if (run.state === "accepted") {
      this.#publish({ state: "accepted", run, events: this.#eventPage() });
    } else if (run.state === "cancelled") {
      this.#publish({ state: "cancelled", run, events: this.#eventPage() });
    } else if (run.state === "failed" || run.state === "rejected") {
      this.#publish({ state: "failed", run, events: this.#eventPage(), detail: run.state });
    } else {
      this.#publish({ state: "running", run, events: this.#eventPage() });
    }
    return this.getState();
  }

  /** Bounded reconnect-safe polling for progress views; callers choose the budget. */
  async poll(maxAttempts = 60, delayMs = 250): Promise<TestGenerationControllerState> {
    if (!Number.isSafeInteger(maxAttempts) || maxAttempts < 1 || maxAttempts > 600) throw new Error("invalid test-generation poll budget");
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const state = await this.refresh();
      if (state.state === "preview" || state.state === "accepted" || state.state === "cancelled" || state.state === "failed" || state.state === "unavailable") return state;
      if (attempt + 1 < maxAttempts) await (this.options.sleep?.(delayMs) ?? new Promise<void>((resolve) => setTimeout(resolve, delayMs)));
    }
    return this.getState();
  }

  async accept(candidateId: string, confirmCharacterization = false, displayed?: GenerationPreviewBinding): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    if (!this.#runId) throw new Error("No active test-generation run.");
    const epoch = ++this.#epoch;
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client, epoch);
    this.#assertFresh(epoch, context, client);
    // Always refetch the run and candidate page. Local preview state is display-only.
    const run = await client.getTestGenerationRun(this.#runId);
    this.#assertFresh(epoch, context, client);
    this.#assertRunContext(run, context);
    if (!run.preview?.confirmationDigest) throw new Error("The service has not produced a confirmable preview.");
    const candidates = await client.listTestGenerationCandidates({ runId: run.runId, limit: this.options.maxCandidatePageSize ?? 128 });
    this.#assertFresh(epoch, context, client);
    const candidate = candidates.items.find((item) => item.candidateId === candidateId);
    if (!candidate) throw new Error("The selected generated-test candidate is no longer available.");
    if (displayed && (
      displayed.runId !== run.runId ||
      displayed.candidateId !== candidate.candidateId ||
      displayed.candidateSetDigest !== run.preview.candidateSetDigest ||
      displayed.candidateArtifactDigest !== candidate.artifactDigest ||
      displayed.candidateCodeDigest !== candidate.codeDigest ||
      displayed.diffDigest !== run.preview.diffDigest ||
      displayed.confirmationDigest !== run.preview.confirmationDigest
    )) {
      this.#publish({ state: "preview", run, candidates, detail: "The generated-test preview changed; review the refreshed preview before accepting." });
      throw new Error("Generated-test preview is stale; review the refreshed preview before accepting.");
    }
    if (candidate.kind === "characterization" && !confirmCharacterization) throw new Error("Characterization candidates require explicit confirmation.");
    this.#publish({ state: "accepting", run, candidates });
    try {
      this.#assertFresh(epoch, context, client);
      const accepted = await client.acceptTestGeneration({ runId: run.runId, candidateId, confirmationDigest: run.preview.confirmationDigest, confirmCharacterization } satisfies TestGenerationAcceptInput);
      this.#assertFresh(epoch, context, client);
      this.#setRun(accepted, epoch);
      return this.refresh();
    } catch (error) {
      // A late RPC failure must not restore a preview belonging to the old
      // workspace/session. The caller can explicitly refresh the new session.
      if (this.#isFreshContext(context, client, epoch)) {
        this.#publish({ state: "preview", run, candidates, detail: errorMessage(error) });
      } else if (!this.#closed && epoch === this.#epoch) {
        this.#publish({ state: "unavailable", detail: "The workspace or service session changed during acceptance." });
      }
      throw error;
    }
  }

  async cancel(): Promise<TestGenerationControllerState> {
    this.#assertOpen();
    if (!this.#runId) return this.getState();
    const epoch = ++this.#epoch;
    const context = this.#assertContext();
    const client = await this.#assertCapability(context.client, epoch);
    this.#assertFresh(epoch, context, client);
    const run = await client.cancelTestGeneration(this.#runId);
    this.#assertFresh(epoch, context, client);
    this.#assertRunContext(run, context);
    this.#setRun(run, epoch);
    return this.refresh();
  }

  dispose(): void { this.#closed = true; this.#operation++; this.#state = { state: "idle" }; }

  #assertOpen(): void { if (this.#closed) throw new Error("test-generation controller is disposed"); }

  #assertContext(): GenerationContext {
    const context = this.options.readContext();
    if (context.trust !== "trusted") throw new Error("Unit Test: Trust this workspace to use test generation.");
    if (!context.projectId || !context.workspaceGeneration) throw new Error("Unit Test: A single inspected workspace is required.");
    return context;
  }

  async #assertCapability(client: ExtensionProtocolClient | undefined, epoch?: number): Promise<ExtensionGenerationProtocolClient> {
    const generation = generationClient(client);
    const capabilities = await generation.getCapabilities();
    if (!("testGeneration" in capabilities) || capabilities.testGeneration !== true) {
      if (epoch === undefined || epoch === this.#epoch) this.#publish({ state: "unavailable", detail: "The service does not advertise offline test generation." });
      throw new Error("Protocol v1.5 test-generation capability is unavailable.");
    }
    return generation;
  }

  #assertRunContext(run: TestGenerationRunV15, context: GenerationContext): void {
    if (run.projectId !== context.projectId || run.workspaceGeneration !== context.workspaceGeneration) throw new Error("The generation run is stale for this workspace.");
  }

  #setRun(run: TestGenerationRunV15, epoch?: number): void {
    if (epoch !== undefined) this.#assertEpoch(epoch);
    if (this.#runId !== run.runId) {
      this.#runId = run.runId;
      this.#lastSequence = 0;
      this.#events.clear();
    }
    this.#publish({ state: isTerminal(run.state) ? (run.state === "awaiting_confirmation" ? "preview" : run.state === "accepted" ? "accepted" : run.state === "cancelled" ? "cancelled" : "failed") : "running", run });
  }

  #mergeEvents(page: TestGenerationEventPageV15): void {
    for (const event of page.items) this.#events.set(event.sequence, event);
    this.#lastSequence = Math.max(this.#lastSequence, page.nextAfterSequence, ...page.items.map((event) => event.sequence));
  }

  #eventPage(): TestGenerationEventPageV15 {
    const limit = this.options.maxEventPageSize ?? 128;
    const items = [...this.#events.values()].sort((left, right) => left.sequence - right.sequence).slice(-limit);
    return { items, nextAfterSequence: this.#lastSequence };
  }

  #publish(state: TestGenerationControllerState): void {
    this.#state = cloneState(state);
    this.options.onStateChanged?.(this.getState());
  }

  #assertCurrent(operation: number): void { if (operation !== this.#operation) throw new Error("The generation run became stale."); }

  #assertEpoch(epoch: number): void {
    if (this.#closed || epoch !== this.#epoch) throw new Error("The generation operation became stale.");
  }

  #assertFresh(epoch: number, expected: GenerationContext, client: ExtensionGenerationProtocolClient): void {
    this.#assertEpoch(epoch);
    if (!this.#isFreshContext(expected, client, epoch)) throw new Error("The workspace or service session changed during test generation.");
  }

  #isFreshContext(expected: GenerationContext, client: ExtensionGenerationProtocolClient, epoch: number): boolean {
    if (this.#closed || epoch !== this.#epoch) return false;
    const current = this.options.readContext();
    return current.trust === "trusted" && current.projectId === expected.projectId && current.workspaceGeneration === expected.workspaceGeneration && current.client === client;
  }
}

function errorMessage(error: unknown): string { return error instanceof Error ? error.message : String(error); }

export function createTestGenerationController(options: TestGenerationControllerOptions): TestGenerationController {
  return new TestGenerationController(options);
}

export { TestGenerationFrameworkV15, TestGenerationScopeV15 };
