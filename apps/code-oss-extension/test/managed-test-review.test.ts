import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { ManagedTestReviewController } from "../src/managed-test-review.js";
import { ABSENT_BLOCK_DIGEST_V16 } from "@unit-test-ide/test-client";

const generation = "a".repeat(64);
const reportId = "b".repeat(32);
const reviewId = "c".repeat(32);
const reviewDigest = "d".repeat(64);
const caseId = `utc_${"e".repeat(32)}`;
const digest = (value: string) => createHash("sha256").update(value).digest("hex");
const generated = "+TEST(foo)\n";
function artifactDigest(value: { reviewDigest: string; cases: { caseId: string; diff?: string }[]; scaffoldPreviews?: { key: string; diffDigest: string }[] }): string {
  const cases = value.cases.map((item) => `c:${item.caseId}:${digest(item.diff ?? "")}\n`).sort();
  const scaffolds = (value.scaffoldPreviews ?? []).map((item) => `s:${item.key}:${item.diffDigest}\n`).sort();
  return digest(`managed-review-preview-v1\n${value.reviewDigest}\n${cases.join("")}${scaffolds.join("")}`);
}
const reviewBody = {
  reviewId, reviewDigest, workspaceGeneration: generation, coverageReportId: reportId,
  cases: [{ caseId, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: digest(generated), diff: generated }]
};
const review = { ...reviewBody, previewArtifactDigest: artifactDigest(reviewBody) };

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

test("review records closed choices but cannot Apply without authoritative preview proof", async () => {
  const f = fixture();
  const model = await f.controller.load(reviewId);
  assert.equal(model.canApply, false);
  f.controller.markDisplayed(model.review!.reviewDigest);
  await assert.rejects(() => f.controller.apply(model.review!.reviewDigest), /preview unavailable/i);
  for (const choice of ["keep-current", "use-generated", "convert-to-manual"] as const) {
    f.controller.choose(caseId, choice);
    assert.equal(f.controller.getState().canApply, false);
    assert.equal(f.controller.getState().choices[caseId], choice);
    await assert.rejects(() => f.controller.apply(model.review!.reviewDigest), /preview unavailable/i);
  }
  assert.equal(f.applied.length, 0);
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

test("first-time review identifies absent ancestor but cannot Apply", async () => {
  const f = fixture();
  f.setReview({ ...review, cases: [{ ...review.cases[0], acceptedDigest: ABSENT_BLOCK_DIGEST_V16, absentSides: ["accepted"] }] });
  const state = await f.controller.load(reviewId);
  assert.deepEqual(state.review?.cases[0]?.absentSides, ["accepted"]);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.deepEqual(f.applied, []);
});

test("review rejects malformed absent-side sentinel rather than accepting a fabricated ancestor", async () => {
  const f = fixture();
  for (const absentSides of [["accepted", "accepted"], ["wrong"]]) {
    f.setReview({ ...review, cases: [{ ...review.cases[0], acceptedDigest: ABSENT_BLOCK_DIGEST_V16, absentSides }] });
    await assert.rejects(() => f.controller.load(reviewId), /absent|digest/i);
  }
  f.setReview({ ...review, cases: [{ ...review.cases[0], acceptedDigest: "1".repeat(64), absentSides: ["accepted"] }] });
  await assert.rejects(() => f.controller.load(reviewId), /absent|digest/i);
  assert.equal(f.applied.length, 0);
});

test("v1.5 capability downgrade keeps managed review unavailable", async () => {
  const f = fixture();
  f.client.getCapabilities = async () => ({ testGeneration: true });
  assert.equal(await f.controller.available(), false);
  await assert.rejects(() => f.controller.load(reviewId), /unavailable/i);
  assert.equal(f.applied.length, 0);
});

test("repeated Apply attempts and cancelled fetch never send a decision", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "keep-current");
  const [first, second] = await Promise.allSettled([f.controller.apply(reviewDigest), f.controller.apply(reviewDigest)]);
  assert.equal(first.status, "rejected");
  assert.equal(second.status, "rejected");
  assert.equal(f.applied.length, 0);
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

test("ordinary review still requires authoritative preview proof without conflict choices", async () => {
  const f = fixture();
  f.setReview({ ...review, cases: [{ ...review.cases[0], status: "current" }] });
  const loaded = await f.controller.load(reviewId);
  assert.equal(loaded.canApply, false);
  f.controller.markDisplayed(loaded.review!.reviewDigest);
  assert.equal(f.controller.getState().canApply, false);
  await assert.rejects(() => f.controller.apply(loaded.review!.reviewDigest), /preview unavailable/i);
  assert.deepEqual(f.applied, []);
});

test("digest-only review loads but cannot authorize Apply without a verified preview", async () => {
  const f = fixture();
  f.setReview({ ...review, cases: [{ ...review.cases[0], diff: undefined }] });
  const loaded = await f.controller.load(reviewId);
  assert.equal(loaded.review?.cases[0]?.diff, undefined);
  f.controller.choose(caseId, "keep-current");
  f.controller.markDisplayed(reviewDigest);
  assert.equal(f.controller.getState().canApply, false);
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.equal(f.applied.length, 0);
});

test("fabricated or mismatched diff cannot authorize Apply without a manifest-bound preview artifact", async () => {
  const f = fixture();
  const forged = { ...review, cases: [{ ...review.cases[0]!, diff: "+FABRICATED()\n" }] };
  f.setReview(forged);
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  assert.equal(f.controller.getState().previewAvailable, false);
  assert.equal(f.controller.getState().canApply, false);
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.equal(f.applied.length, 0);
});

test("fabricated diff and a matching self-asserted preview hash still cannot authorize Apply", async () => {
  const f = fixture();
  const forged = { ...review, cases: [{ ...review.cases[0]!, diff: "+FABRICATED()\n" }] };
  f.setReview({ ...forged, previewArtifactDigest: artifactDigest(forged) });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  assert.equal(f.controller.getState().previewAvailable, false);
  assert.equal(f.controller.getState().canApply, false);
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.equal(f.applied.length, 0);
});

test("preview changed after display cannot dispatch even when review digest remains unchanged", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  f.setReview({ ...review, cases: [{ ...review.cases[0], diff: "+CHANGED()\n" }] });
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview|stale/i);
  assert.equal(f.applied.length, 0);
});

test("returned review state cannot mutate held preview bytes", async () => {
  const f = fixture();
  const scaffold = "scaffold:tests/generated/src/a_test.cpp";
  const scaffoldDiff = "+TEST(scaffold)\n";
  const withScaffold = { ...review, conflictKeys: [caseId, scaffold], scaffoldPreviews: [{ key: scaffold, diff: scaffoldDiff, diffDigest: digest(scaffoldDiff) }] };
  f.setReview({ ...withScaffold, previewArtifactDigest: artifactDigest(withScaffold) });
  const state = await f.controller.load(reviewId);
  (state.review!.cases[0] as any).diff = "+FABRICATED()\n";
  (state.review!.scaffoldPreviews![0] as any).diff = "+FABRICATED()\n";
  assert.equal(f.controller.getState().review!.cases[0]!.diff, generated);
  assert.equal(f.controller.getState().review!.scaffoldPreviews![0]!.diff, scaffoldDiff);
});

test("only service-advertised scaffold conflict keys can be chosen, never applied", async () => {
  const f = fixture();
  const scaffold = "scaffold:tests/generated/src/a_test.cpp";
  const scaffoldDiff = "--- a/tests/generated/src/a_test.cpp\n+++ b/tests/generated/src/a_test.cpp\n";
  const withScaffold = { ...review, conflictKeys: [caseId, scaffold], scaffoldPreviews: [{ key: scaffold, diff: scaffoldDiff, diffDigest: digest(scaffoldDiff) }] };
  f.setReview({ ...withScaffold, previewArtifactDigest: artifactDigest(withScaffold) });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  assert.throws(() => f.controller.choose("scaffold:tests/generated/other_test.cpp", "keep-current"), /invalid/i);
  f.controller.choose(caseId, "keep-current");
  assert.equal(f.controller.getState().canApply, false);
  f.controller.choose(scaffold, "use-generated");
  assert.equal(f.controller.getState().canApply, false);
  assert.equal(f.controller.getState().choices[scaffold], "use-generated");
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.deepEqual(f.applied, []);
});

test("multiple conflicts preserve independent choices without dispatch", async () => {
  const f = fixture();
  const otherId = `utc_${"f".repeat(32)}`;
  const withSecondCase = { ...review, cases: [{ ...review.cases[0]!, caseId: otherId }, review.cases[0]!] };
  f.setReview({ ...withSecondCase, previewArtifactDigest: artifactDigest(withSecondCase) });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(otherId, "keep-current");
  assert.equal(f.controller.getState().canApply, false);
  f.controller.choose(caseId, "convert-to-manual");
  assert.deepEqual(f.controller.getState().choices, { [caseId]: "convert-to-manual", [otherId]: "keep-current" });
  assert.equal(f.controller.getState().canApply, false);
  await assert.rejects(() => f.controller.apply(reviewDigest), /preview unavailable/i);
  assert.deepEqual(f.applied, []);
});

test("Apply availability stays disabled after choices and display without durable preview proof", async () => {
  const f = fixture();
  const ready: boolean[] = [];
  const controller = new ManagedTestReviewController({ readContext: () => ({ trust: "trusted", client: f.client, projectId: "core", workspaceGeneration: generation, coverageReportId: reportId }), onStateChanged: (state) => ready.push(state.canApply) });
  await controller.load(reviewId);
  assert.equal(ready.at(-1), false);
  controller.choose(caseId, "keep-current");
  assert.equal(ready.at(-1), false);
  controller.markDisplayed(reviewDigest);
  assert.equal(ready.at(-1), false);
  await assert.rejects(() => controller.apply(reviewDigest), /preview unavailable/i);
  assert.equal(ready.at(-1), false);
  assert.equal(f.applied.length, 0);
});

test("reject and workspace invalidation clear local choices without a write", async () => {
  const f = fixture();
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "use-generated");
  f.controller.reject();
  assert.equal(f.controller.getState().review, undefined);
  await f.controller.load(reviewId);
  f.controller.invalidate();
  assert.equal(f.controller.getState().review, undefined);
  const newGeneration = "9".repeat(64);
  f.setGeneration(newGeneration);
  f.setReview({ ...review, workspaceGeneration: newGeneration });
  await f.controller.load(reviewId);
  f.controller.markDisplayed(reviewDigest);
  f.controller.choose(caseId, "keep-current");
  assert.equal(f.controller.getState().canApply, false);
  assert.equal(f.applied.length, 0);
});
