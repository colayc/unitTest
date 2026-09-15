import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  SCENARIO_IDS,
  buildBaseline,
  summarizeSamples,
  validateBaseline
} from "./performance.mjs";
import { createIdentityRefreshFixture, IDENTITY_ITEM_COUNT } from "./testing-api-identity-fixture.mjs";

test("shared Testing API fixture preserves identity across same-revision refresh", () => {
  const fixture = createIdentityRefreshFixture();
  assert.equal(fixture.refresh().items.size, IDENTITY_ITEM_COUNT);
  assert.equal(fixture.refreshSameRevision().identityPreserved, true);
});

test("performance baseline has the exact closed schema and six bounded scenarios", () => {
  const baseline = buildBaseline({ candidateCommit: "0123456789abcdef0123456789abcdef01234567" });
  assert.deepEqual(Object.keys(baseline).sort(), ["candidateCommit", "hardware", "runtime", "scenarios", "schemaVersion"]);
  assert.deepEqual(baseline.scenarios.map(({ id }) => id), SCENARIO_IDS);
  assert.equal(baseline.schemaVersion, 1);
  assert.match(baseline.candidateCommit, /^[0-9a-f]{40}$/);
  assert.equal(validateBaseline(baseline), true);
  for (const scenario of baseline.scenarios) {
    assert.equal(scenario.sampleCount, 5);
    assert.equal(scenario.warmupCount, 1);
    const samples = scenario.samplesMs ?? scenario.samplesBytes;
    assert.equal(samples.length, 5);
    assert.ok(samples.every(Number.isFinite));
    assert.ok(Number.isFinite(scenario.median));
    assert.ok(Number.isFinite(scenario.p95));
    assert.ok(Number.isFinite(scenario.coefficientOfVariation));
    assert.ok(scenario.coefficientOfVariation <= 0.20);
    assert.ok(scenario.correctness && scenario.correctness.passed > 0);
  }
});

test("summarization rejects unstable and non-finite samples", () => {
  assert.throws(() => summarizeSamples([1, 2, 3, 4, 100]), /coefficient of variation/u);
  assert.throws(() => summarizeSamples([1, 2, Number.NaN, 4, 5]), /finite/u);
});

test("baseline redacts sensitive strings and absolute paths", () => {
  const serialized = JSON.stringify(buildBaseline({ candidateCommit: "0123456789abcdef0123456789abcdef01234567" }));
  assert.doesNotMatch(serialized, /C:\\|\/home\/|token|secret|password|Bearer/iu);
  assert.doesNotMatch(serialized, /[A-Za-z]:\\/u);
  assert.ok(serialized.length < 250_000);
});

test("CLI rejects malformed arguments and writes JSON for the fixed interface", () => {
  for (const args of [[], ["--out"], ["--bad", "x"], ["--out", "-secret"]]) {
    assert.notEqual(spawnSync(process.execPath, ["tools/phase9/performance.mjs", ...args], { encoding: "utf8" }).status, 0);
  }
  const directory = mkdtempSync(join(tmpdir(), "phase9-perf-"));
  try {
    const output = join(directory, "baseline.json");
    execFileSync(process.execPath, ["tools/phase9/performance.mjs", "--out", output], { encoding: "utf8" });
    assert.equal(validateBaseline(JSON.parse(readFileSync(output, "utf8"))), true);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("validator rejects mutated summaries, correctness, and scenario fields", () => {
  const baseline = buildBaseline({ candidateCommit: "0123456789abcdef0123456789abcdef01234567" });
  const mutate = (fn) => { const copy = structuredClone(baseline); fn(copy); return copy; };
  assert.equal(validateBaseline(mutate((v) => { v.extra = true; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].median++; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].samplesBytes = [1, 1, 1, 1, 1]; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].correctness.failed = 1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].correctness.extra = 1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].samplesMs[0] = -1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].coefficientOfVariation = 0.21; })), false);
});
