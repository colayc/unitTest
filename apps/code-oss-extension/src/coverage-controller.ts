import { randomBytes } from "node:crypto";
import {
  TestSelectionModeV14,
  type CoverageReport,
  type CoverageRun,
  type CoverageRunInput,
} from "@unit-test-ide/test-client";
import type { TrustState } from "./contracts.js";
import {
  type ExtensionCoverageProtocolClient,
  type ExtensionProtocolClient
} from "./protocol-client.js";
import type { TestingCatalogState } from "./testing-api.js";
import { redactServiceError } from "./service-resources.js";

export type CoverageControllerStateName =
  | "idle"
  | "starting"
  | "running"
  | "available"
  | "unavailable"
  | "stopped";

export interface CoverageControllerState {
  readonly state: CoverageControllerStateName;
  readonly coverageRunId?: string;
  readonly taskId?: string;
  readonly reportId?: string;
  readonly completeness?: CoverageReport["completeness"];
  readonly summary?: CoverageReport["summary"];
  readonly toolProvenance?: CoverageReport["toolProvenance"];
  readonly sources?: CoverageReport["sources"];
  readonly detail?: string;
}

export interface CoverageContext {
  readonly trust: TrustState;
  readonly client: ExtensionProtocolClient | undefined;
  readonly serviceRunning: boolean;
  readonly workspaceGeneration: string;
  readonly catalog: TestingCatalogState | undefined;
  readonly coverageProfileId: string;
}

export interface CoverageStartOptions {
  readonly catalogRevision: string;
  readonly selection?: CoverageRunInput["selection"];
  readonly repeatCount?: number;
  readonly timeoutMs?: number;
}

export interface CoverageControllerOptions {
  readonly readContext: () => CoverageContext;
  readonly onStateChanged?: (state: CoverageControllerState) => void;
  readonly sleep?: (milliseconds: number) => Promise<void>;
}

const DEFAULT_TIMEOUT_MS = 30_000;
const DEFAULT_REPEAT_COUNT = 1;
const MAX_POLL_ATTEMPTS = 60;
const POLL_DELAY_MS = 100;

function coverageClient(client: ExtensionProtocolClient | undefined): ExtensionCoverageProtocolClient {
  if (!client?.startCoverage || !client.getCoverageRun || !client.getCoverageReport) {
    throw new Error("Protocol v1.4 coverage capability is unavailable.");
  }
  return client as ExtensionCoverageProtocolClient;
}

function defaultSelection(): CoverageRunInput["selection"] {
  return { mode: TestSelectionModeV14.All };
}

function copyState(state: CoverageControllerState): CoverageControllerState {
  return {
    ...state,
    completeness: state.completeness === undefined ? undefined : {
      outcome: state.completeness.outcome,
      reasons: [...state.completeness.reasons]
    },
    summary: state.summary === undefined ? undefined : {
      lines: { ...state.summary.lines },
      branches: { ...state.summary.branches },
      functions: { ...state.summary.functions }
    },
    toolProvenance: state.toolProvenance === undefined ? undefined : {
      ...state.toolProvenance,
      compiler: { ...state.toolProvenance.compiler },
      driver: { ...state.toolProvenance.driver },
      collector: { ...state.toolProvenance.collector }
    },
    sources: state.sources === undefined ? undefined : state.sources.map((source) => ({ ...source }))
  };
}

export class CoverageController {
  #state: CoverageControllerState = { state: "idle" };
  #closed = false;
  #operation = 0;

  constructor(private readonly options: CoverageControllerOptions) {}

  getState(): CoverageControllerState {
    return copyState(this.#state);
  }

  async start(input: CoverageStartOptions): Promise<CoverageControllerState> {
    if (this.#closed) throw new Error("coverage controller is disposed");
    const operation = ++this.#operation;
    const context = this.#assertStartContext(input);
    this.#publish({ state: "starting" });
    try {
      const client = coverageClient(context.client);
      const request: CoverageRunInput = {
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: context.workspaceGeneration,
        projectId: context.catalog!.projectId,
        coverageProfileId: context.coverageProfileId,
        catalogRevision: input.catalogRevision,
        selection: input.selection === undefined ? defaultSelection() : structuredClone(input.selection),
        repeatCount: input.repeatCount ?? DEFAULT_REPEAT_COUNT,
        timeoutMs: input.timeoutMs ?? DEFAULT_TIMEOUT_MS
      };
      const started = await client.startCoverage(request);
      this.#assertCurrent(operation, context, input.catalogRevision);
      this.#publish({ state: "running", coverageRunId: started.coverageRunId, taskId: started.taskId });
      const finished = await this.#waitForFinished(client, started, operation, context, input.catalogRevision);
      this.#assertCurrent(operation, context, input.catalogRevision);
      if (!finished.reportId) {
        throw new Error("Coverage run finished without a report.");
      }
      const report = await client.getCoverageReport(finished.reportId);
      this.#assertCurrent(operation, context, input.catalogRevision);
      if (report.coverageRunId !== finished.coverageRunId || report.testRunId !== finished.testRunId) {
        throw new Error("Coverage report identity does not match its run.");
      }
      this.#publish({
        state: "available",
        coverageRunId: finished.coverageRunId,
        taskId: finished.taskId,
        reportId: report.reportId,
        completeness: report.completeness,
        summary: report.summary,
        toolProvenance: report.toolProvenance,
        sources: report.sources
      });
      return this.getState();
    } catch (error) {
      if (operation !== this.#operation || this.#closed) throw error;
      const detail = redactServiceError(error, []).message;
      this.#publish({ state: "unavailable", detail });
      throw new Error(detail);
    }
  }

  async startCurrent(): Promise<CoverageControllerState> {
    const context = this.#assertContext();
    return this.start({ catalogRevision: context.catalog!.revision });
  }

  async refresh(runId: string): Promise<CoverageControllerState> {
    if (this.#closed) throw new Error("coverage controller is disposed");
    const operation = ++this.#operation;
    const context = this.#assertContext();
    const client = coverageClient(context.client);
    try {
      const run = await client.getCoverageRun(runId);
      this.#assertCurrent(operation, context, run.catalogRevision);
      if (!run.reportId) throw new Error("Coverage run has no report yet.");
      const report = await client.getCoverageReport(run.reportId);
      this.#assertCurrent(operation, context, run.catalogRevision);
      if (report.coverageRunId !== run.coverageRunId || report.testRunId !== run.testRunId) {
        throw new Error("Coverage report identity does not match its run.");
      }
      this.#publish({
        state: "available",
        coverageRunId: run.coverageRunId,
        taskId: run.taskId,
        reportId: report.reportId,
        completeness: report.completeness,
        summary: report.summary,
        toolProvenance: report.toolProvenance,
        sources: report.sources
      });
      return this.getState();
    } catch (error) {
      const detail = redactServiceError(error, []).message;
      this.#publish({ state: "unavailable", detail });
      throw new Error(detail);
    }
  }

  async refreshCurrent(): Promise<CoverageControllerState> {
    const runId = this.#state.coverageRunId;
    if (!runId) throw new Error("No coverage run is available to refresh.");
    return this.refresh(runId);
  }

  setTrustState(trust: TrustState): void {
    if (trust !== "trusted" && this.#state.state !== "stopped") {
      this.#operation++;
      this.#publish({ state: "stopped", detail: "Coverage stopped because workspace trust is no longer available." });
    }
  }

  dispose(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#operation++;
    this.#publish({ state: "stopped" });
  }

  #assertContext(): CoverageContext {
    const context = this.options.readContext();
    if (context.trust !== "trusted") throw new Error("A trusted workspace is required for coverage.");
    if (!context.serviceRunning || !context.client) throw new Error("Service is not running.");
    if (!context.catalog) throw new Error("A current test catalog is required for coverage.");
    if (context.coverageProfileId.length === 0) throw new Error("A coverage profile is required.");
    return context;
  }

  #assertStartContext(input: CoverageStartOptions): CoverageContext {
    if (!input.catalogRevision) throw new Error("A catalog revision is required.");
    const context = this.#assertContext();
    if (context.catalog!.revision !== input.catalogRevision) {
      throw new Error("Coverage selection does not match the current catalog revision.");
    }
    return context;
  }

  #assertCurrent(operation: number, captured: CoverageContext, catalogRevision: string): void {
    if (this.#closed || operation !== this.#operation) throw new Error("Coverage operation is no longer current.");
    const current = this.#assertContext();
    if (current.client !== captured.client || current.workspaceGeneration !== captured.workspaceGeneration) {
      throw new Error("Coverage session changed while the operation was running.");
    }
    if (current.catalog?.projectId !== captured.catalog?.projectId ||
      current.catalog?.profileId !== captured.catalog?.profileId ||
      current.catalog?.revision !== catalogRevision) {
      throw new Error("Coverage catalog changed while the operation was running.");
    }
  }

  async #waitForFinished(
    client: ExtensionCoverageProtocolClient,
    initial: CoverageRun,
    operation: number,
    captured: CoverageContext,
    catalogRevision: string
  ): Promise<CoverageRun> {
    let run = initial;
    for (let attempt = 0; attempt < MAX_POLL_ATTEMPTS; attempt++) {
      this.#assertCurrent(operation, captured, catalogRevision);
      if (run.status === "finished") return run;
      this.#publish({ state: "running", coverageRunId: run.coverageRunId, taskId: run.taskId });
      await (this.options.sleep ?? ((milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds))))(POLL_DELAY_MS);
      this.#assertCurrent(operation, captured, catalogRevision);
      run = await client.getCoverageRun(initial.coverageRunId);
    }
    throw new Error("Coverage run did not finish within the polling budget.");
  }

  #publish(next: CoverageControllerState): void {
    this.#state = copyState(next);
    this.options.onStateChanged?.(this.getState());
  }
}

export function createCoverageController(options: CoverageControllerOptions): CoverageController {
  return new CoverageController(options);
}
