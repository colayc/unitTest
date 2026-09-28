import type {
  ManagedConflictChoiceV16,
  ManagedReviewApplyResultV16,
  ManagedReviewCaseV16,
  ManagedReviewV16,
  ManagedTestRecordPageV16,
  ManagedTestStatusV16
} from "@unit-test-ide/test-client";
import { supportsManagedTests, type TrustState } from "./contracts.js";
import type { ExtensionManagedProtocolClient, ExtensionProtocolClient } from "./protocol-client.js";

export interface ManagedReviewContext {
  readonly trust: TrustState;
  readonly client?: ExtensionProtocolClient;
  readonly projectId?: string;
  readonly workspaceGeneration?: string;
  readonly coverageReportId?: string;
}

export interface ManagedReviewControllerOptions {
  readonly readContext: () => ManagedReviewContext;
  readonly onStateChanged?: (state: ManagedReviewState) => void;
}

export interface ManagedReviewState {
  readonly review?: ManagedReviewV16;
  readonly choices: Readonly<Record<string, ManagedChoice>>;
  readonly canApply: boolean;
  readonly applying: boolean;
}

export type ManagedApplyOutcome =
  | { readonly state: "confirmed"; readonly result: ManagedReviewApplyResultV16 }
  | { readonly state: "uncertain"; readonly reviewId: string; readonly reviewDigest: string }
  | { readonly state: "cancelled"; readonly reviewId: string; readonly reviewDigest: string };

export type ManagedChoice = "keep-current" | "use-generated" | "convert-to-manual";
export type ManagedStatusFilter = "current" | "stale" | "conflicted" | "orphaned" | "invalid";
type ManagedClient = ExtensionProtocolClient & ExtensionManagedProtocolClient & { getCapabilities: NonNullable<ExtensionProtocolClient["getCapabilities"]> };
type BoundContext = ManagedReviewContext & { client: ManagedClient; projectId: string; workspaceGeneration: string; coverageReportId: string };
const HEX_32 = /^[0-9a-f]{32}$/;
const HEX_64 = /^[0-9a-f]{64}$/;
const CASE_ID = /^utc_[0-9a-f]{32}$/;
const CHOICES = new Set<string>(["keep-current", "use-generated", "convert-to-manual"]);
const STATUSES = new Set<string>(["current", "stale", "conflicted", "orphaned", "invalid"]);

function managedClient(client: ExtensionProtocolClient | undefined): client is ManagedClient {
  return !!client?.getCapabilities && !!client.getManagedReview && !!client.applyManagedReview && !!client.listManagedTests;
}

function checkCase(value: ManagedReviewCaseV16): void {
  if (!CASE_ID.test(value.caseId) || !HEX_64.test(value.acceptedDigest) || !HEX_64.test(value.currentDigest) || !HEX_64.test(value.generatedDigest) || !STATUSES.has(value.status)) {
    throw new Error("The managed review contains an invalid case or digest.");
  }
  if (value.diff !== undefined && (typeof value.diff !== "string" || value.diff.length > 32_768 || value.diff.includes("\0"))) {
    throw new Error("The managed review preview is invalid.");
  }
  if (value.status === "conflicted" && !value.diff) throw new Error("The conflicted case has no review preview.");
}

export class ManagedTestReviewController {
  #epoch = 0;
  #binding: BoundContext | undefined;
  #review: ManagedReviewV16 | undefined;
  #choices = new Map<string, ManagedChoice>();
  #applying = false;
  #applyToken = 0;
  #displayed = false;

  constructor(private readonly options: ManagedReviewControllerOptions) {}

  getState(): ManagedReviewState {
    const review = this.#review;
    return {
      review: review ? { ...review, cases: review.cases.map((item) => ({ ...item })) } : undefined,
      choices: Object.fromEntries(this.#choices),
      canApply: !!review && this.#displayed && !this.#applying && this.#currentBinding() && review.cases.every((item) => item.status !== "conflicted" || this.#choices.has(item.caseId)),
      applying: this.#applying
    };
  }

  async available(): Promise<boolean> {
    const context = this.options.readContext();
    if (context.trust !== "trusted" || !managedClient(context.client) || !context.projectId || !context.workspaceGeneration || !context.coverageReportId) return false;
    try {
      const capabilities = await context.client.getCapabilities();
      return this.#sameContext(context) && supportsManagedTests(capabilities);
    } catch { return false; }
  }

  async list(status?: ManagedStatusFilter): Promise<ManagedTestRecordPageV16> {
    const context = await this.#authorize();
    if (status !== undefined && !STATUSES.has(status)) throw new Error("Invalid managed-test status filter.");
    const result = await context.client.listManagedTests({ projectId: context.projectId, workspaceGeneration: context.workspaceGeneration, coverageReportId: context.coverageReportId, ...(status ? { status: status as ManagedTestStatusV16 } : {}) });
    this.#assertSame(context);
    if (result.workspaceGeneration !== context.workspaceGeneration || result.coverageReportId !== context.coverageReportId || result.items.some((item) => status && item.status !== status)) {
      throw new Error("Managed-test records are stale for this workspace or filter.");
    }
    return result;
  }

  async load(reviewId: string): Promise<ManagedReviewState> {
    if (this.#applying) throw new Error("A managed review is already applying.");
    if (!HEX_32.test(reviewId)) throw new Error("Invalid managed review ID.");
    const epoch = ++this.#epoch;
    this.#clear();
    const context = await this.#authorize();
    this.#assertEpoch(epoch);
    const cases: ManagedReviewCaseV16[] = [];
    let cursor: string | undefined;
    let anchor: ManagedReviewV16 | undefined;
    const seenCursors = new Set<string>();
    do {
      const page = await context.client.getManagedReview({ reviewId, limit: 100, ...(cursor ? { cursor } : {}) });
      this.#assertEpoch(epoch);
      this.#assertSame(context);
      if (page.reviewId !== reviewId || !HEX_64.test(page.reviewDigest) || page.workspaceGeneration !== context.workspaceGeneration || page.coverageReportId !== context.coverageReportId ||
        (anchor && page.reviewDigest !== anchor.reviewDigest)) throw new Error("The managed review digest or workspace is stale.");
      for (const item of page.cases) checkCase(item);
      cases.push(...page.cases);
      if (cases.length > 10_000 || new Set(cases.map((item) => item.caseId)).size !== cases.length) throw new Error("Managed review cases are ambiguous or unbounded.");
      anchor ??= page;
      cursor = page.nextCursor;
      if (cursor && seenCursors.has(cursor)) throw new Error("Managed review pagination did not advance.");
      if (cursor) seenCursors.add(cursor);
    } while (cursor);
    if (!anchor || cases.length === 0) throw new Error("The managed review is empty.");
    this.#binding = context;
    this.#review = { ...anchor, cases, nextCursor: undefined };
    this.#notify();
    return this.getState();
  }

  choose(caseId: string, choice: ManagedChoice): ManagedReviewState {
    if (this.#applying) throw new Error("A managed review is already applying.");
    if (!CHOICES.has(choice) || !this.#review?.cases.some((item) => item.caseId === caseId && item.status === "conflicted")) throw new Error("The managed review choice is invalid.");
    if (!this.#currentBinding()) throw new Error("The managed review is stale for this workspace or session.");
    this.#choices.set(caseId, choice);
    this.#notify();
    return this.getState();
  }

  /** Called only after every service-supplied preview has actually been shown. */
  markDisplayed(reviewDigest: string): ManagedReviewState {
    if (this.#applying || !this.#review || !this.#currentBinding() || this.#review.reviewDigest !== reviewDigest) throw new Error("The displayed managed review is stale.");
    this.#displayed = true;
    this.#notify();
    return this.getState();
  }

  reject(): void {
    if (this.#applying) throw new Error("A managed review is already applying.");
    this.#epoch++;
    this.#clear();
  }

  /** Revoke a workspace/session-bound view even when an RPC is already in flight. */
  invalidate(): void {
    this.#epoch++;
    this.#applyToken++;
    this.#applying = false;
    this.#clear();
  }

  async apply(displayedReviewDigest: string): Promise<ManagedApplyOutcome> {
    if (this.#applying) throw new Error("A managed review is already applying.");
    const review = this.#review;
    const binding = this.#binding;
    if (!review || !binding || !this.#currentBinding()) throw new Error("The managed review is stale for this workspace or session.");
    if (displayedReviewDigest !== review.reviewDigest) throw new Error("The displayed managed review digest is stale.");
    if (!this.#displayed) throw new Error("The managed review preview has not been displayed.");
    if (!review.cases.every((item) => item.status !== "conflicted" || this.#choices.has(item.caseId))) throw new Error("The managed review has unresolved conflicts.");
    const epoch = this.#epoch;
    const applyToken = ++this.#applyToken;
    this.#applying = true;
    this.#notify();
    let dispatched = false;
    try {
      const fresh = await binding.client.getManagedReview({ reviewId: review.reviewId, limit: 100 });
      this.#assertEpoch(epoch);
      this.#assertSame(binding);
      if (fresh.reviewDigest !== review.reviewDigest || fresh.workspaceGeneration !== review.workspaceGeneration || fresh.coverageReportId !== review.coverageReportId) throw new Error("The managed review digest is stale.");
      const resolutions = [...this.#choices].sort(([left], [right]) => left.localeCompare(right)).map(([caseId, choice]) => ({ caseId, choice }));
      // From this point on, a transport failure or workspace invalidation
      // cannot establish that the service did not commit the decision.
      dispatched = true;
      const result = await binding.client.applyManagedReview({ reviewId: review.reviewId, reviewDigest: review.reviewDigest, resolutions: resolutions as { caseId: string; choice: ManagedConflictChoiceV16 }[] });
      if (result.reviewId !== review.reviewId || result.reviewDigest !== review.reviewDigest || !result.applied) {
        if (applyToken === this.#applyToken) this.#clear();
        return { state: "uncertain", reviewId: review.reviewId, reviewDigest: review.reviewDigest };
      }
      if (applyToken === this.#applyToken) this.#clear();
      return { state: "confirmed", result };
    } catch (error) {
      if (!dispatched) {
        if (epoch !== this.#epoch || !this.#sameContext(binding)) return { state: "cancelled", reviewId: review.reviewId, reviewDigest: review.reviewDigest };
        throw error;
      }
      if (applyToken === this.#applyToken) this.#clear();
      return { state: "uncertain", reviewId: review.reviewId, reviewDigest: review.reviewDigest };
    } finally {
      if (applyToken === this.#applyToken) { this.#applying = false; this.#notify(); }
    }
  }

  async #authorize(): Promise<BoundContext> {
    const context = this.options.readContext();
    if (context.trust !== "trusted") throw new Error("Trust this workspace to review managed tests.");
    if (!managedClient(context.client) || !context.projectId || !context.workspaceGeneration || !context.coverageReportId) throw new Error("Managed tests are unavailable for this workspace or service session.");
    const capabilities = await context.client.getCapabilities();
    this.#assertSame(context);
    if (!supportsManagedTests(capabilities)) throw new Error("Protocol v1.6 managed-test capability is unavailable.");
    return context as BoundContext;
  }

  #clear(): void { this.#binding = undefined; this.#review = undefined; this.#choices.clear(); this.#displayed = false; this.#notify(); }
  #notify(): void { this.options.onStateChanged?.(this.getState()); }
  #assertEpoch(epoch: number): void { if (epoch !== this.#epoch) throw new Error("The managed review was cancelled or became stale."); }
  #sameContext(expected: ManagedReviewContext): boolean {
    const current = this.options.readContext();
    return current.trust === "trusted" && current.client === expected.client && current.projectId === expected.projectId && current.workspaceGeneration === expected.workspaceGeneration && current.coverageReportId === expected.coverageReportId;
  }
  #assertSame(expected: ManagedReviewContext): void { if (!this.#sameContext(expected)) throw new Error("The managed review is stale for this workspace or service session."); }
  #currentBinding(): boolean { return !!this.#binding && this.#sameContext(this.#binding); }
}
