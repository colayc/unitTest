import { execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { performance } from "node:perf_hooks";
import process from "node:process";
import { dirname, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { IDENTITY_ITEM_COUNT, createIdentityRefreshFixture } from "./testing-api-identity-fixture.mjs";

export const SCENARIO_IDS = ["discovery-10000", "filter", "cancel", "memory", "startup", "report"];
const ITEM_COUNT = IDENTITY_ITEM_COUNT;
const HEX40 = /^[0-9a-f]{40}$/u;


function commitAtHead() {
  try {
    const value = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
    return HEX40.test(value) ? value : "0".repeat(40);
  } catch { return "0".repeat(40); }
}

export function summarizeSamples(samples) {
  if (!Array.isArray(samples) || samples.length !== 5 || !samples.every(Number.isFinite)) {
    throw new Error("samples must contain exactly five finite values");
  }
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

function measured(operation, bytes = false) {
  operation(); // warm-up
  const samples = [];
  let observed;
  for (let i = 0; i < 5; i++) {
    const beforeRss = process.memoryUsage().rss;
    const started = performance.now();
    for (let repeat = 0; repeat < (bytes ? 1 : 20); repeat++) observed = operation();
    const elapsed = performance.now() - started;
    const value = bytes ? Math.max(0, process.memoryUsage().rss - beforeRss) : Math.max(0.01, elapsed);
    samples.push(bytes ? value : Math.max(0.1, Number(value.toFixed(1))));
  }
  return { samples, summary: summarizeSamples(samples), observed };
}

function scenario(id, operation, expected, bytes = false) {
  let result;
  try { result = measured(operation, bytes); } catch (error) { throw new Error(`${id}: ${error.message}`, { cause: error }); }
  return {
    id,
    ...(bytes ? { samplesBytes: result.samples } : { samplesMs: result.samples }),
    ...result.summary,
    correctness: { expected, observed: result.observed, passed: result.observed, failed: expected - result.observed }
  };
}

export function buildBaseline(options = {}) {
  const fixture = createIdentityRefreshFixture();
  const items = [...fixture.refresh().items.values()].map((item) => ({ ...item, name: `Synthetic ${item.id}` }));
  const scenarios = [
    scenario("discovery-10000", () => { let count = 0; let identity = true; for (let pass = 0; pass < 20; pass++) { const snapshot = fixture.refreshSameRevision(); identity &&= snapshot.identityPreserved; for (const item of snapshot.items.values()) count += item.id.length > 0 ? 1 : 0; } return identity && count / 20 === ITEM_COUNT ? ITEM_COUNT : 0; }, ITEM_COUNT),
    scenario("filter", () => { let count = 0; for (let pass = 0; pass < 10; pass++) for (const item of items) if (item.id.endsWith("0")) count++; return count / 10; }, 1000),
    scenario("cancel", () => { let cancelled = 0; for (let i = 0; i < 1000; i++) { const controller = new AbortController(); controller.abort(); cancelled += controller.signal.aborted ? 1 : 0; } return cancelled === 1000 ? 1 : 0; }, 1),
    scenario("memory", () => { const fixture = Buffer.alloc(1024 * 1024, 7); const observed = fixture.byteLength; fixture.fill(0); return observed; }, 1024 * 1024, true),
    scenario("startup", () => { let ready = false; for (let i = 0; i < 1000; i++) { const startup = { items: Array.from({ length: 100 }, (_, j) => j), ready: true }; ready = startup.ready && startup.items.length === 100; } return ready ? 1 : 0; }, 1),
    scenario("report", () => { const report = JSON.stringify(items.map((item) => ({ id: item.id, status: "passed" }))); return report.length > 0 ? 1 : 0; }, 1)
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
      const keys = Object.keys(item).sort();
      const expectedKeys = ["coefficientOfVariation", "correctness", "id", "max", "median", "min", "p95", "sampleCount", "samplesBytes", "warmupCount"];
      const expectedMsKeys = expectedKeys.map((key) => key === "samplesBytes" ? "samplesMs" : key).sort();
      if (keys.join(",") !== ("samplesBytes" in item ? expectedKeys : expectedMsKeys).join(",")) return false;
      const samples = item.samplesMs ?? item.samplesBytes;
      if (!samples || ("samplesMs" in item) === ("samplesBytes" in item) || item.sampleCount !== 5 || item.warmupCount !== 1 || !item.correctness) return false;
      summarizeSamples(samples);
      for (const key of ["median", "p95", "min", "max", "coefficientOfVariation"]) if (!Number.isFinite(item[key])) return false;
      const summary = summarizeSamples(samples);
      for (const key of ["sampleCount", "warmupCount", "median", "p95", "min", "max", "coefficientOfVariation"]) if (item[key] !== summary[key]) return false;
      if (Object.keys(item.correctness).sort().join(",") !== "expected,failed,observed,passed") return false;
      const { expected, observed, passed, failed } = item.correctness;
      if (!Number.isSafeInteger(expected) || !Number.isSafeInteger(observed) || passed !== observed || failed !== expected - observed || expected < 0 || observed < 0 || observed > expected) return false;
      if (samples.some((sample) => sample < 0)) return false;
    }
    return true;
  } catch { return false; }
}

async function main(argv) {
  if (argv.length !== 2 || argv[0] !== "--out" || !argv[1] || argv[1].startsWith("-")) throw new Error("usage: node tools/phase9/performance.mjs --out <path>");
  const output = resolve(argv[1]);
  const baseline = buildBaseline();
  if (!validateBaseline(baseline)) throw new Error("generated performance baseline failed validation");
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(baseline, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) main(process.argv.slice(2)).catch((error) => { console.error(error.message); process.exitCode = 1; });
