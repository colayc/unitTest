import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import test from "node:test";
import { TestingApiAdapter } from "../src/testing-api.js";
import {
  createTestingApiIdentityFixture,
  IDENTITY_ITEM_COUNT,
  IDENTITY_REVISION
} from "./testing-api-benchmark-support.mjs";

test("10,000 item catalog keeps every Test Item identity for the same revision", async (t) => {
  const fixture = createTestingApiIdentityFixture(TestingApiAdapter);
  const startedAt = performance.now();
  const result = await fixture.run();
  const elapsedMs = performance.now() - startedAt;

  assert.equal(result.adapterRefreshCount, 2);
  assert.equal(result.itemCount, IDENTITY_ITEM_COUNT);
  assert.equal(result.verifiedIdentityCount, IDENTITY_ITEM_COUNT);
  assert.equal(result.identityPreserved, true);
  assert.equal(result.catalogCallCount, 102);
  assert.deepEqual(result.mutationCounts, {
    create: IDENTITY_ITEM_COUNT + 1,
    add: 0,
    delete: 0,
    replace: 2
  });
  t.diagnostic(JSON.stringify({
    runtime: `node-${process.versions.node}`,
    platform: `${process.platform}-${process.arch}`,
    itemCount: result.itemCount,
    verifiedIdentityCount: result.verifiedIdentityCount,
    revision: IDENTITY_REVISION,
    elapsedMs: Number(elapsedMs.toFixed(3)),
    replacementCountAfterSameRevision: 0
  }));
});
