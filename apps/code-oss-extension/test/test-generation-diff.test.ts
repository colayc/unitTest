import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { createGenerationDiffReview, createManagedCaseReview, diffDigest, readGenerationDiff, redactGenerationDiffPaths } from "../src/test-generation-diff.js";
import type { TestGenerationRunV15 } from "@unit-test-ide/test-client";
import { ABSENT_BLOCK_DIGEST_V16 } from "@unit-test-ide/test-client";

const id = "a".repeat(32);
const diff = "--- C:\\private\\workspace\\tests\\generated.cpp\n+++ C:\\private\\workspace\\tests\\generated.cpp\n@@ -0,0 +1 @@\n+TEST(foo)\n";
function run(value = diff): TestGenerationRunV15 {
  return { runId: id, taskId: "b".repeat(32), projectId: "core", workspaceGeneration: "c".repeat(64), state: "awaiting_confirmation", createdAt: new Date(0), lastSequence: 1, preview: { candidateSetDigest: "d".repeat(64), diff: value, diffDigest: createHash("sha256").update(value).digest("hex"), confirmationDigest: "e".repeat(64) } } as TestGenerationRunV15;
}

test("generation diff verifies the exact service bytes and digest", () => {
  const preview = readGenerationDiff(run());
  assert.equal(preview.diffDigest, diffDigest(diff));
  assert.equal(createGenerationDiffReview(run()).content, diff);
  assert.throws(() => readGenerationDiff({ ...run(), preview: { ...run().preview!, diffDigest: "f".repeat(64) } }), /stale/);
});

test("display-only diff redaction removes absolute workspace paths without changing acceptance digest", () => {
  const display = redactGenerationDiffPaths(diff);
  assert.doesNotMatch(display, /C:\\private\\workspace/);
  assert.match(display, /<workspace-path>/);
  assert.equal(diffDigest(diff), readGenerationDiff(run()).diffDigest);
});

test("managed case review presents bounded exact diff and all three service digests", () => {
  const model = createManagedCaseReview({ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64), diff: "+TEST(foo)\n" } as any, true);
  assert.equal(model.content, "+TEST(foo)\n");
  assert.equal(model.previewAvailable, true);
  assert.deepEqual(model.panes, { accepted: "1".repeat(64), current: "2".repeat(64), generated: "3".repeat(64) });
  const unverified = createManagedCaseReview({ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64), diff: "+FABRICATED()\n" } as any);
  assert.equal(unverified.previewAvailable, false);
  assert.match(unverified.content, /preview unavailable/i);
  const digestOnly = createManagedCaseReview({ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64) } as any);
  assert.equal(digestOnly.previewAvailable, false);
  assert.match(digestOnly.content, /preview unavailable/i);
  assert.match(createManagedCaseReview({ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64), diff: "" } as any).content, /preview unavailable/i);
});

test("managed preview labels absent ancestor separately from real empty block", () => {
  const value = { caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: ABSENT_BLOCK_DIGEST_V16, currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64), diff: "+TEST(foo)\n" } as any;
  assert.equal(createManagedCaseReview({ ...value, absentSides: ["accepted"] }).panes.accepted, "No accepted ancestor");
  assert.equal(createManagedCaseReview(value).panes.accepted, ABSENT_BLOCK_DIGEST_V16);
});
