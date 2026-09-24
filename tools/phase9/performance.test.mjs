import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import os, { tmpdir } from "node:os";
import { join } from "node:path";
import { performance } from "node:perf_hooks";
import test, { mock } from "node:test";
import {
  SCENARIO_IDS,
  buildBaseline,
  memoryScenario,
  runDiscoveryIdentityScenario,
  summarizeSamples,
  timedScenario,
  validateBaseline
} from "./performance.mjs";
import { IDENTITY_ITEM_COUNT } from "../../apps/code-oss-extension/dist/test/testing-api-benchmark-support.mjs";

const CANDIDATE_COMMIT = "0123456789abcdef0123456789abcdef01234567";
let baselinePromise;
const rssReadings = [];
const hardwareReadings = { cpus: [], totalMemoryBytes: [] };
const baseline = () => baselinePromise ??= (async () => {
  const memoryUsage = process.memoryUsage;
  const probe = mock.method(process, "memoryUsage", (...args) => {
    const usage = memoryUsage(...args);
    rssReadings.push(usage.rss);
    return usage;
  });
  const cpus = os.cpus;
  const totalmem = os.totalmem;
  const cpuProbe = mock.method(os, "cpus", () => {
    const value = cpus();
    hardwareReadings.cpus.push(value.length);
    return value;
  });
  const memoryProbe = mock.method(os, "totalmem", () => {
    const value = totalmem();
    hardwareReadings.totalMemoryBytes.push(value);
    return value;
  });
  try {
    return await buildBaseline({ candidateCommit: CANDIDATE_COMMIT });
  } finally {
    probe.mock.restore();
    cpuProbe.mock.restore();
    memoryProbe.mock.restore();
  }
})();

// Hand-authored stable schema fixture keeps malformed-input checks independent
// of timing noise and exercises portable recorded hardware metadata.
function validSchemaBaseline() {
  return {
    schemaVersion: 1,
    candidateCommit: CANDIDATE_COMMIT,
    runtime: { node: process.versions.node, platform: process.platform, arch: process.arch },
    hardware: { cpus: 2, totalMemoryBytes: 8589934592 },
    scenarios: [
      ["discovery-10000", 10000], ["filter", 1000], ["cancel", 1],
      ["memory", 1048576], ["startup", 1], ["report", 1]
    ].map(([id, expected]) => ({
      id,
      [id === "memory" ? "samplesBytes" : "samplesMs"]: [10, 10, 10, 10, 10],
      sampleCount: 5, warmupCount: 1, median: 10, p95: 10, min: 10, max: 10,
      coefficientOfVariation: 0,
      correctness: { expected, observed: expected, passed: expected, failed: 0 }
    }))
  };
}

test("validator requires successful exact correctness records in every scenario", () => {
  assert.equal(validateBaseline(validSchemaBaseline()), true);
  for (let index = 0; index < 6; index++) {
    for (const change of [
      (c) => { c.observed--; c.passed--; c.failed = 1; },
      (c) => { c.observed--; },
      (c) => { c.passed--; },
      (c) => { c.failed = 1; },
      (c) => { c.extra = "secret"; },
      ...["expected", "observed", "passed", "failed"].map((key) => (c) => { delete c[key]; }),
      (c) => { c.expected = c.observed = c.passed = -1; },
      (c) => { c.expected = c.observed = c.passed = 0.5; },
      (c) => { c.expected = c.observed = c.passed = Number.MAX_SAFE_INTEGER + 1; }
    ]) {
      const value = validSchemaBaseline();
      change(value.scenarios[index].correctness);
      assert.equal(validateBaseline(value), false, JSON.stringify(value.scenarios[index].correctness));
    }
  }
});

test("validator binds correctness to each scenario's fixed expected count", async (t) => {
  for (let index = 0; index < 6; index++) {
    const scenario = validSchemaBaseline().scenarios[index];
    for (const expected of [0, scenario.correctness.expected + 1]) {
      await t.test(`${scenario.id} rejects expected ${expected}`, () => {
        const value = validSchemaBaseline();
        value.scenarios[index].correctness = { expected, observed: expected, passed: expected, failed: 0 };
        assert.equal(validateBaseline(value), false);
      });
    }
  }
});

test("validator closes runtime metadata and rejects injected or non-runtime strings", () => {
  for (const runtime of [null, [], "secret", 1, {},
    { ...validSchemaBaseline().runtime, extra: "C:\\private\\secret" }]) {
    const value = validSchemaBaseline();
    value.runtime = runtime;
    assert.equal(validateBaseline(value), false);
  }
  for (const key of ["node", "platform", "arch"]) {
    for (const replacement of [undefined, null, 1, {}, [], "", "C:\\private\\secret", "/home/private", "Bearer token", "not-runtime"]) {
      const value = validSchemaBaseline();
      if (replacement === undefined) delete value.runtime[key];
      else value.runtime[key] = replacement;
      assert.equal(validateBaseline(value), false, `${key}: ${JSON.stringify(replacement)}`);
    }
  }
});

test("validator accepts canonical runtime metadata recorded on a different host", () => {
  for (const runtime of [
    { node: "24.18.0", platform: "linux", arch: "arm64" },
    { node: "24.19.0", platform: "win32", arch: "x64" }
  ]) {
    const value = validSchemaBaseline();
    value.runtime = runtime;
    assert.equal(validateBaseline(value), true);
  }
  for (const node of ["24", "24.18", "v24.18.0", "024.18.0", "24.018.0", "24.18.00", "24.18.0\n", "24.18.0 secret"]) {
    const value = validSchemaBaseline();
    value.runtime.node = node;
    assert.equal(validateBaseline(value), false);
  }
});

test("validator closes hardware metadata and requires positive safe integer measurements", () => {
  assert.equal(validateBaseline(validSchemaBaseline()), true);
  for (const hardware of [null, [], "secret", 1, {},
    { ...validSchemaBaseline().hardware, extra: "/home/private/secret" }]) {
    const value = validSchemaBaseline();
    value.hardware = hardware;
    assert.equal(validateBaseline(value), false);
  }
  for (const key of ["cpus", "totalMemoryBytes"]) {
    for (const replacement of [undefined, null, "1", {}, [], 0, -1, 0.5, NaN, Infinity,
      Number.MAX_SAFE_INTEGER + 1, "C:\\private\\secret", "/home/private", "Bearer token"]) {
      const value = validSchemaBaseline();
      if (replacement === undefined) delete value.hardware[key];
      else value.hardware[key] = replacement;
      assert.equal(validateBaseline(value), false, `${key}: ${String(replacement)}`);
    }
  }
});

test("baseline publishes actual runtime and OS-probed CPU and total memory measurements", async () => {
  const value = await baseline();
  assert.deepEqual(value.runtime, { node: process.versions.node, platform: process.platform, arch: process.arch });
  assert.equal(hardwareReadings.cpus.length, 1);
  assert.equal(hardwareReadings.totalMemoryBytes.length, 1);
  assert.deepEqual(value.hardware, {
    cpus: hardwareReadings.cpus[0], totalMemoryBytes: hardwareReadings.totalMemoryBytes[0]
  });
  assert.ok(Number.isSafeInteger(value.hardware.cpus) && value.hardware.cpus > 0);
  assert.ok(Number.isSafeInteger(value.hardware.totalMemoryBytes) && value.hardware.totalMemoryBytes > 0);
});

test("discovery scenario executes the shared TestingApiAdapter identity fixture", async () => {
  assert.equal(await runDiscoveryIdentityScenario(), IDENTITY_ITEM_COUNT);
});

test("timed scenarios reject intermittent incorrect results throughout warm-up and samples", async (t) => {
  let now = 0;
  const clock = mock.method(performance, "now", () => ++now);
  try {
    // Three warm-up operations, then five samples with three operations each.
    // Include early/middle warm-up, early/middle measured work, and the last result.
    for (const incorrectAt of [0, 1, 3, 7, 17]) {
      await t.test(`incorrect operation ${incorrectAt}`, async () => {
        let calls = 0;
        await assert.rejects(timedScenario("intermittent", () => {
          return calls++ === incorrectAt ? 0 : 2;
        }, 2, 3), /intermittent: correctness mismatch/u);
      });
    }
  } finally {
    clock.mock.restore();
  }
});

test("timed scenarios preserve the fixed expected count when all repetitions succeed", async () => {
  let now = 0;
  let calls = 0;
  const clock = mock.method(performance, "now", () => ++now);
  try {
    const scenario = await timedScenario("correct", () => { calls++; return 2; }, 2, 3);
    assert.equal(calls, 18);
    assert.deepEqual(scenario.correctness, { expected: 2, observed: 2, passed: 2, failed: 0 });
    assert.deepEqual(scenario.samplesMs, [1, 1, 1, 1, 1]);
    assert.equal(scenario.warmupCount, 1);
    assert.equal(scenario.sampleCount, 5);
  } finally {
    clock.mock.restore();
  }
});

test("memory scenario rejects an incorrect first or middle allocation before a later success", async (t) => {
  const alloc = Buffer.alloc;
  for (const incorrectAt of [1, 3]) {
    await t.test(`incorrect allocation ${incorrectAt}`, () => {
      let calls = 0;
      const allocation = mock.method(Buffer, "alloc", (size, ...args) => {
        return alloc(calls++ === incorrectAt ? size - 1 : size, ...args);
      });
      try {
        assert.throws(() => memoryScenario(), /memory: correctness mismatch/u);
      } finally {
        allocation.mock.restore();
      }
    });
  }
});

test("performance baseline has the exact closed schema and six bounded scenarios", async () => {
  const value = await baseline();
  assert.deepEqual(Object.keys(value).sort(), ["candidateCommit", "hardware", "runtime", "scenarios", "schemaVersion"]);
  assert.deepEqual(value.scenarios.map(({ id }) => id), SCENARIO_IDS);
  assert.equal(value.schemaVersion, 1);
  assert.match(value.candidateCommit, /^[0-9a-f]{40}$/);
  assert.equal(validateBaseline(value), true);
  for (const scenario of value.scenarios) {
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

test("memory samples publish the actual post-allocation process RSS readings", async () => {
  const value = await baseline();
  // One warm-up RSS probe, then before/after probes for five allocations.
  assert.equal(rssReadings.length, 11);
  const memory = value.scenarios.find(({ id }) => id === "memory");
  assert.deepEqual(memory.samplesBytes, [
    rssReadings[2], rssReadings[4], rssReadings[6], rssReadings[8], rssReadings[10]
  ]);
  assert.deepEqual(memory.correctness, {
    expected: 1048576, observed: 1048576, passed: 1048576, failed: 0
  });
});

test("summarization rejects unstable and non-finite samples", () => {
  assert.throws(() => summarizeSamples([1, 2, 3, 4, 100]), /coefficient of variation/u);
  assert.throws(() => summarizeSamples([1, 2, Number.NaN, 4, 5]), /finite/u);
});

test("baseline redacts sensitive strings and absolute paths", async () => {
  const serialized = JSON.stringify(await baseline());
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

test("validator rejects mutated summaries, correctness, and scenario fields", async () => {
  const value = await baseline();
  const mutate = (fn) => { const copy = structuredClone(value); fn(copy); return copy; };
  assert.equal(validateBaseline(mutate((v) => { v.extra = true; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].median++; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].samplesBytes = [1, 1, 1, 1, 1]; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].correctness.failed = 1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].correctness.extra = 1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].samplesMs[0] = -1; })), false);
  assert.equal(validateBaseline(mutate((v) => { v.scenarios[0].coefficientOfVariation = 0.21; })), false);
  assert.equal(validateBaseline(mutate((v) => {
    const memory = v.scenarios.find(({ id }) => id === "memory");
    memory.samplesMs = memory.samplesBytes;
    delete memory.samplesBytes;
  })), false);
  for (const samples of [[-1, -1, -1, -1, -1], [1, 1, NaN, 1, 1], [1, 1, Infinity, 1, 1], [1, 1, 1, 1, 100]]) {
    assert.equal(validateBaseline(mutate((v) => {
      v.scenarios.find(({ id }) => id === "memory").samplesBytes = samples;
    })), false);
  }
});
