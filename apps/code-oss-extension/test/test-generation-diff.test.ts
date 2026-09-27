import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { createGenerationDiffReview, diffDigest, readGenerationDiff, redactGenerationDiffPaths } from "../src/test-generation-diff.js";
import type { TestGenerationRunV15 } from "@unit-test-ide/test-client";

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
