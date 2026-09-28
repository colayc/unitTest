import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { ManagedTestReviewController } from "../src/managed-test-review.js";

const generation = "a".repeat(64);
const reportId = "b".repeat(32);
const reviewId = "c".repeat(32);
const reviewDigest = "d".repeat(64);
const caseId = `utc_${"e".repeat(32)}`;
const digest = (value: string) => createHash("sha256").update(value).digest("hex");
const generated = "+TEST(foo)\n";
const review = {
  reviewId, reviewDigest, workspaceGeneration: generation, coverageReportId: reportId,
  cases: [{ caseId, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: digest(generated), diff: generated }]
};

function fixture() {
  const applied: unknown[] = [];
  let trust: "trusted" | "blocked-untrusted" = "trusted";
  let workspaceGeneration = generation;
  let session: any;
  let serverReview: any = structuredClone(review);
  const client: any = {
    async getCapabilities() { return { managedTests: true, maxManagedTestPageSize: 100 }; },
    async getManagedReview() { return structuredClone(serverReview); },
    async listManagedTests(input: any) { return { workspaceGeneration, coverageReportId: reportId, items: [{ caseId, fileId: "f".repeat(32), functionId: "0".repeat(32), status: input.status ?? "current", acceptedDigest: "1".repeat(64), currentDigest: "1".repeat(64) }] }; },
    async applyManagedReview(input: unknown) { applied.push(input); return { reviewId, reviewDigest, applied: true }; }
  };
  session = client;
  const controller = new ManagedTestReviewController({ readContext: () => ({ trust, client: session, projectId: "core", workspaceGeneration, coverageReportId: reportId }) });
  return { controller, client, applied, setTrust(value: typeof trust) { trust = value; }, setGeneration(value: string) { workspaceGeneration = value; }, setSession(value: any) { session = value; }, setReview(value: any) { serverReview = value; } };
}

test("review requires all conflicted cases to have a closed choice and sends no preview bytes", async () => {
  const f = fixture();
  const model = await f.controller.load(reviewId);
  assert.equal(model.canApply, false);
  f.controller.markDisplayed(model.review!.reviewDigest);
  await assert.rejects(() => f.controller.apply(model.review!.reviewDigest), /unresolved/i);
  for (const choice of ["keep-current", "use-generated", "convert-to-manual"] as const) {
    f.controller.choose(caseId, choice);
    assert.equal(f.controller.getState().canApply, true);
    await f.controller.apply(model.review!.reviewDigest);
    assert.deepEqual(f.applied.at(-1), { reviewId, reviewDigest, resolutions: [{ caseId, choice }] });
    await f.controller.load(reviewId);
    f.controller.markDisplayed(reviewDigest);
  }
});

test("stale digest, trust revocation and changed workspace or session cannot publish", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  await assert.rejects(() => f.controller.apply("9".repeat(64)), /stale/i);
  f.setTrust("blocked-untrusted");
  await assert.rejects(() => f.controller.apply(reviewDigest), /trust|workspace/i);
  f.setTrust("trusted");
  f.setGeneration("9".repeat(64));
  await assert.rejects(() => f.controller.apply(reviewDigest), /stale|workspace/i);
  f.setGeneration(generation);
  f.setSession({ ...f.client });
  await assert.rejects(() => f.controller.apply(reviewDigest), /stale|session/i);
  assert.equal(f.applied.length, 0);
});

test("malformed preview digest and stale server review fail closed", async () => {
  const f = fixture();
  f.setReview({ ...review, cases: [{ ...review.cases[0], generatedDigest: "not-a-digest" }] });
  await assert.rejects(() => f.controller.load(reviewId), /digest/i);
  f.setReview({ ...review, workspaceGeneration: "9".repeat(64) });
  await assert.rejects(() => f.controller.load(reviewId), /workspace/i);
  assert.equal(f.applied.length, 0);
});

test("double apply and cancelled fetch do not send a second decision", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "keep-current");
  const [first, second] = await Promise.allSettled([f.controller.apply(reviewDigest), f.controller.apply(reviewDigest)]);
  assert.equal(first.status, "fulfilled");
  assert.equal(second.status, "rejected");
  assert.equal(f.applied.length, 1);
  let release: (() => void) | undefined;
  f.client.getManagedReview = async () => { await new Promise<void>((resolve) => { release = resolve; }); return review; };
  const pending = f.controller.load(reviewId);
  await new Promise<void>((resolve) => setImmediate(resolve));
  f.controller.reject();
  release!();
  await assert.rejects(pending, /cancelled|stale/i);
});

test("maintenance filtering preserves all five service statuses and never mutates records", async () => {
  const f = fixture();
  for (const status of ["current", "stale", "conflicted", "orphaned", "invalid"] as const) {
    const page = await f.controller.list(status);
    assert.equal(page.items[0]?.status, status);
  }
  assert.equal(f.applied.length, 0);
});

test("ordinary review needs no conflict choices but still requires the displayed digest", async () => {
  const f = fixture();
  f.setReview({ ...review, cases: [{ ...review.cases[0], status: "current" }] });
  const loaded = await f.controller.load(reviewId);
  assert.equal(loaded.canApply, false);
  f.controller.markDisplayed(loaded.review!.reviewDigest);
  assert.equal(f.controller.getState().canApply, true);
  await f.controller.apply(loaded.review!.reviewDigest);
  assert.deepEqual(f.applied, [{ reviewId, reviewDigest, resolutions: [] }]);
});

test("multiple conflicts require independent choices and preserve their sorted identity", async () => {
  const f = fixture();
  const otherId = `utc_${"f".repeat(32)}`;
  f.setReview({ ...review, cases: [{ ...review.cases[0], caseId: otherId }, review.cases[0]] });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(otherId, "keep-current");
  assert.equal(f.controller.getState().canApply, false);
  f.controller.choose(caseId, "convert-to-manual");
  await f.controller.apply(reviewDigest);
  assert.deepEqual(f.applied[0], { reviewId, reviewDigest, resolutions: [{ caseId, choice: "convert-to-manual" }, { caseId: otherId, choice: "keep-current" }] });
});

test("reject during an in-flight apply cannot falsely claim that no write happened", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  let release: (() => void) | undefined;
  f.client.applyManagedReview = async (input: unknown) => { f.applied.push(input); await new Promise<void>((resolve) => { release = resolve; }); return { reviewId, reviewDigest, applied: true }; };
  const pending = f.controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.throws(() => f.controller.reject(), /applying/i);
  await assert.rejects(() => f.controller.load(reviewId), /applying/i);
  release!();
  await pending;
  assert.equal(f.applied.length, 1);
});

test("session invalidation after dispatch preserves a confirmed service response", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  let release: (() => void) | undefined;
  f.client.applyManagedReview = async (input: unknown) => { f.applied.push(input); await new Promise<void>((resolve) => { release = resolve; }); return { reviewId, reviewDigest, applied: true }; };
  const pending = f.controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  f.controller.invalidate();
  assert.equal(f.controller.getState().review, undefined);
  release!();
  const outcome = await pending;
  assert.equal(outcome.state, "confirmed");
  assert.equal(f.applied.length, 1);
});

test("post-dispatch transport loss is uncertain, while pre-dispatch invalidation is cancelled", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  let release: (() => void) | undefined;
  f.client.applyManagedReview = async (input: unknown) => { f.applied.push(input); await new Promise<void>((resolve) => { release = resolve; }); throw new Error("connection closed"); };
  const pending = f.controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  f.controller.invalidate();
  release!();
  const outcome = await pending;
  assert.deepEqual(outcome, { state: "uncertain", reviewId, reviewDigest });
  assert.equal(f.applied.length, 1);

  const other = fixture();
  await other.controller.load(reviewId);
  other.controller.markDisplayed(reviewDigest);
  other.controller.choose(caseId, "use-generated");
  let releaseFetch: (() => void) | undefined;
  other.client.getManagedReview = async () => { await new Promise<void>((resolve) => { releaseFetch = resolve; }); return review; };
  const preDispatch = other.controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  other.controller.invalidate();
  releaseFetch!();
  assert.equal((await preDispatch).state, "cancelled");
  assert.equal(other.applied.length, 0);
});

test("Apply availability remains disabled until all choices are resolved and while applying", async () => {
  const f = fixture();
  const ready: boolean[] = [];
  const controller = new ManagedTestReviewController({ readContext: () => ({ trust: "trusted", client: f.client, projectId: "core", workspaceGeneration: generation, coverageReportId: reportId }), onStateChanged: (state) => ready.push(state.canApply) });
  await controller.load(reviewId);
  assert.equal(ready.at(-1), false);
  controller.choose(caseId, "keep-current");
  assert.equal(ready.at(-1), false);
  controller.markDisplayed(reviewDigest);
  assert.equal(ready.at(-1), true);
  let release: (() => void) | undefined;
  f.client.applyManagedReview = async () => { await new Promise<void>((resolve) => { release = resolve; }); return { reviewId, reviewDigest, applied: true }; };
  const pending = controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(ready.at(-1), false);
  release!();
  await pending;
  assert.equal(ready.at(-1), false);
});

test("an invalidated in-flight Apply cannot block or clear a later workspace review", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  let release: (() => void) | undefined;
  f.client.applyManagedReview = async () => { await new Promise<void>((resolve) => { release = resolve; }); return { reviewId, reviewDigest, applied: true }; };
  const oldApply = f.controller.apply(reviewDigest);
  await new Promise<void>((resolve) => setImmediate(resolve));
  f.controller.invalidate();
  const newGeneration = "9".repeat(64);
  f.setGeneration(newGeneration);
  f.setReview({ ...review, workspaceGeneration: newGeneration });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "keep-current");
  release!();
  assert.equal((await oldApply).state, "confirmed");
  assert.equal(f.controller.getState().canApply, true);
});
