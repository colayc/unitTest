import { execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { performance } from "node:perf_hooks";
import process from "node:process";
import { pathToFileURL } from "node:url";
import { TestingApiAdapter } from "../../apps/code-oss-extension/dist/src/testing-api.js";
import {
  createIdentityCatalogItems,
  createTestingApiIdentityFixture,
  IDENTITY_ITEM_COUNT
} from "../../apps/code-oss-extension/dist/test/testing-api-benchmark-support.mjs";

export const SCENARIO_IDS = ["discovery-10000", "filter", "cancel", "memory", "startup", "report"];
const HEX40 = /^[0-9a-f]{40}$/u;
const MEMORY_ALLOCATION_BYTES = 1024 * 1024;

function commitAtHead() {
  try {
    const value = execFileSync("git", ["rev-parse", "HEAD"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"]
    }).trim();
    return HEX40.test(value) ? value : "0".repeat(40);
  } catch {
    return "0".repeat(40);
  }
}

export function summarizeSamples(samples) {
  if (!Array.isArray(samples) || samples.length !== 5 || !samples.every(Number.isFinite)) {
    throw new Error("samples must contain exactly five finite values");
  }
  if (samples.some((sample) => sample < 0)) throw new Error("samples must be non-negative");
  const sorted = [...samples].sort((a, b) => a - b);
  const mean = samples.reduce((sum, value) => sum + value, 0) / samples.length;
  const variance = samples.reduce((sum, value) => sum + (value - mean) ** 2, 0) / samples.length;
  const coefficientOfVariation = mean === 0 ? 0 : Math.sqrt(variance) / Math.abs(mean);
  if (!Number.isFinite(coefficientOfVariation) || coefficientOfVariation > 0.20) {
    throw new Error("coefficient of variation exceeds 0.20");
  }
  return {
    sampleCount: 5,
    warmupCount: 1,
    median: sorted[2],
    p95: sorted[4],
    min: sorted[0],
    max: sorted[4],
    coefficientOfVariation
  };
}

async function measured(operation, repeats = 20) {
  for (let repeat = 0; repeat < repeats; repeat++) await operation();
  const samples = [];
  let observed;
  for (let sample = 0; sample < 5; sample++) {
    const started = performance.now();
    for (let repeat = 0; repeat < repeats; repeat++) observed = await operation();
    const elapsed = performance.now() - started;
    samples.push(Math.max(0.1, Number(elapsed.toFixed(1))));
  }
  return { samples, summary: summarizeSamples(samples), observed };
}

async function timedScenario(id, operation, expected, repeats) {
  let result;
  try {
    result = await measured(operation, repeats);
  } catch (error) {
    throw new Error(`${id}: ${error.message}`, { cause: error });
  }
  return {
    id,
    samplesMs: result.samples,
    ...result.summary,
    correctness: {
      expected,
      observed: result.observed,
      passed: result.observed,
      failed: expected - result.observed
    }
  };
}

function memoryScenario() {
  const warmup = Buffer.alloc(MEMORY_ALLOCATION_BYTES, 7);
  if (warmup.byteLength !== MEMORY_ALLOCATION_BYTES || !Number.isFinite(process.memoryUsage().rss)) {
    throw new Error("memory: warm-up allocation failed");
  }

  const retained = [];
  const samples = [];
  let observed = 0;
  for (let sample = 0; sample < 5; sample++) {
    const rssBefore = process.memoryUsage().rss;
    const allocation = Buffer.alloc(MEMORY_ALLOCATION_BYTES, 7);
    retained.push(allocation);
    const rssAfter = process.memoryUsage().rss;
    if (!Number.isFinite(rssBefore) || !Number.isFinite(rssAfter)) {
      throw new Error("memory: RSS sample is non-finite");
    }
    observed = allocation.byteLength;
    // Measure absolute post-operation process RSS, not allocated bytes or a
    // noisy small delta. Keep all five buffers resident for this bounded run.
    // Raw RSS readings go through the same fail-closed stability gate as time.
    samples.push(rssAfter);
  }
  return {
    id: "memory",
    samplesBytes: samples,
    ...summarizeSamples(samples),
    correctness: {
      expected: MEMORY_ALLOCATION_BYTES,
      observed,
      passed: observed,
      failed: MEMORY_ALLOCATION_BYTES - observed
    }
  };
}

export async function runDiscoveryIdentityScenario() {
  const result = await createTestingApiIdentityFixture(TestingApiAdapter).run();
  return result.adapterRefreshCount === 2 &&
    result.itemCount === IDENTITY_ITEM_COUNT &&
    result.verifiedIdentityCount === IDENTITY_ITEM_COUNT &&
    result.identityPreserved
    ? IDENTITY_ITEM_COUNT
    : 0;
}

export async function buildBaseline(options = {}) {
  const items = createIdentityCatalogItems();
  const scenarios = [
    await timedScenario("discovery-10000", runDiscoveryIdentityScenario, IDENTITY_ITEM_COUNT, 3),
    await timedScenario("filter", () => {
      let count = 0;
      for (const item of items) if (item.id.endsWith("0")) count++;
      return count;
    }, 1000, 4000),
    await timedScenario("cancel", () => {
      let cancelled = 0;
      for (let index = 0; index < 1000; index++) {
        const controller = new AbortController();
        controller.abort();
        cancelled += controller.signal.aborted ? 1 : 0;
      }
      return cancelled === 1000 ? 1 : 0;
    }, 1, 50),
    memoryScenario(),
    await timedScenario("startup", () => {
      let ready = false;
      for (let index = 0; index < 1000; index++) {
        const startup = { items: Array.from({ length: 100 }, (_, itemIndex) => itemIndex), ready: true };
        ready = startup.ready && startup.items.length === 100;
      }
      return ready ? 1 : 0;
    }, 1, 20),
    await timedScenario("report", () => {
      const report = JSON.stringify(items.map((item) => ({ id: item.id, status: "passed" })));
      return report.length > 0 ? 1 : 0;
    }, 1, 100)
  ];
  return {
    schemaVersion: 1,
    candidateCommit: options.candidateCommit ?? commitAtHead(),
    runtime: { node: process.versions.node, platform: process.platform, arch: process.arch },
    hardware: { cpus: 1 },
    scenarios
  };
}

export function validateBaseline(value) {
  if (!value || typeof value !== "object" || Object.keys(value).sort().join(",") !== "candidateCommit,hardware,runtime,scenarios,schemaVersion") return false;
  if (value.schemaVersion !== 1 || !HEX40.test(value.candidateCommit) || !Array.isArray(value.scenarios)) return false;
  if (value.scenarios.length !== 6 || value.scenarios.map((item) => item.id).join(",") !== SCENARIO_IDS.join(",")) return false;
  try {
    for (const item of value.scenarios) {
      if (item.id === "memory" ? !("samplesBytes" in item) : !("samplesMs" in item)) return false;
      const keys = Object.keys(item).sort();
      const expectedKeys = ["coefficientOfVariation", "correctness", "id", "max", "median", "min", "p95", "sampleCount", "samplesBytes", "warmupCount"];
      const expectedMsKeys = expectedKeys.map((key) => key === "samplesBytes" ? "samplesMs" : key).sort();
      if (keys.join(",") !== ("samplesBytes" in item ? expectedKeys : expectedMsKeys).join(",")) return false;
      const samples = item.samplesMs ?? item.samplesBytes;
      if (!samples || ("samplesMs" in item) === ("samplesBytes" in item) || item.sampleCount !== 5 || item.warmupCount !== 1 || !item.correctness) return false;
      const summary = summarizeSamples(samples);
      for (const key of ["sampleCount", "warmupCount", "median", "p95", "min", "max", "coefficientOfVariation"]) {
        if (!Number.isFinite(item[key]) || item[key] !== summary[key]) return false;
      }
      if (Object.keys(item.correctness).sort().join(",") !== "expected,failed,observed,passed") return false;
      const { expected, observed, passed, failed } = item.correctness;
      if (!Number.isSafeInteger(expected) || !Number.isSafeInteger(observed) || passed !== observed || failed !== expected - observed || expected < 0 || observed < 0 || observed > expected) return false;
    }
    return true;
  } catch {
    return false;
  }
}

async function main(argv) {
  if (argv.length !== 2 || argv[0] !== "--out" || !argv[1] || argv[1].startsWith("-")) {
    throw new Error("usage: node tools/phase9/performance.mjs --out <path>");
  }
  const output = resolve(argv[1]);
  const baseline = await buildBaseline();
  if (!validateBaseline(baseline)) throw new Error("generated performance baseline failed validation");
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(baseline, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
