const DIGEST = /^[0-9a-f]{64}$/u;
const COMMIT = /^[0-9a-f]{40}$/u;
const VERSION = /^[0-9]+(?:\.[0-9]+){1,3}$/u;
const TIMESTAMP = /^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]\.[0-9]{3}Z$/u;

export const FRAMEWORK_SCENARIO_IDS = [
  "all", "assertion-failure", "cancel", "crash", "discovery", "failed-rerun",
  "filter", "malformed-output", "mock-failure", "opaque-fallback", "reconnect-replay",
  "repeat", "service-restart", "single", "skip", "stale-catalog", "timeout",
] as const;

const SCENARIO_RESULTS = {
  all: ["failed", "aggregate"],
  "assertion-failure": ["failed", "assertion"],
  cancel: ["cancelled", "cancelled"],
  crash: ["errored", "crash"],
  discovery: ["passed", "discovery"],
  "failed-rerun": ["failed", "assertion"],
  filter: ["passed", "selection"],
  "malformed-output": ["errored", "malformed-output"],
  "mock-failure": ["failed", "mock-expectation"],
  "opaque-fallback": ["passed", "opaque-fallback"],
  "reconnect-replay": ["passed", "replay"],
  repeat: ["passed", "repeat"],
  "service-restart": ["interrupted", "service-restarted"],
  single: ["passed", "test"],
  skip: ["skipped", "ignored"],
  "stale-catalog": ["rejected", "stale-catalog"],
  timeout: ["timed-out", "timeout"],
} as const;

const PLATFORM_TOOLCHAINS = {
  linux: ["clang", "gcc"],
  win32: ["clang-cl", "msvc"],
} as const;

const F1_FRAMEWORKS = {
  cpputest: {
    version: "4.0",
    sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7",
    treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
  },
  unity: {
    version: "2.6.1",
    sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
    treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
  },
} as const;

const F1_CMOCK_PROVENANCE: CMockProvenance = {
  revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
  generatorVersion: "2.7.0",
  inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
  outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
  manifestSha256: "2f08cfd45b9374a5331f0484d53b466c3813312d046e5226c64754c0c986f87b",
  generatedAtRuntime: false,
};

export type FrameworkPlatform = keyof typeof PLATFORM_TOOLCHAINS;
export type FrameworkToolchainFamily = typeof PLATFORM_TOOLCHAINS[FrameworkPlatform][number];
export type FrameworkId = keyof typeof F1_FRAMEWORKS;
export type FrameworkScenarioId = typeof FRAMEWORK_SCENARIO_IDS[number];
type ScenarioOutcome = typeof SCENARIO_RESULTS[FrameworkScenarioId][0];
type ScenarioClassification = typeof SCENARIO_RESULTS[FrameworkScenarioId][1];

export interface FrameworkScenarioEvidence {
  id: FrameworkScenarioId;
  status: "passed";
  candidateCommit: string;
  platform: FrameworkPlatform;
  toolchainFamily: FrameworkToolchainFamily;
  frameworkId: FrameworkId;
  catalogRevision: string;
  sourceArtifactSha256: string;
  sourceLocationDigest: string;
  executableArtifactSha256: string;
  resultArtifactSha256: string;
  resultArtifactSizeBytes: number;
  startedAt: string;
  finishedAt: string;
  observedOutcome: ScenarioOutcome;
  classification: ScenarioClassification;
}

export interface CMockProvenance {
  revision: string;
  generatorVersion: string;
  inputSha256: string;
  outputSha256: string;
  manifestSha256: string;
  generatedAtRuntime: false;
}

export interface FrameworkEvidence {
  id: FrameworkId;
  dependencyVersion: string;
  dependencySha256: string;
  dependencyTreeSha256: string;
  catalogRevision: string;
  catalogArtifactSha256: string;
  sourceArtifactSha256: string;
  sourceLocationDigest: string;
  executableArtifactSha256: string;
  stableIdDigest: string;
  cMockProvenance?: CMockProvenance;
  scenarios: FrameworkScenarioEvidence[];
}

export interface FrameworkToolchainEvidence {
  family: FrameworkToolchainFamily;
  compilerVersion: string;
  compilerSha256: string;
  frameworks: FrameworkEvidence[];
}

export interface FrameworkBenchmarkEvidence {
  id: "catalog-10000";
  itemCount: 10000;
  sampleCount: 3;
  allocationBudgetPerOperation: 300000;
  allocationsPerOperation: [number, number, number];
  catalogRevision: string;
  catalogArtifactSha256: string;
  stableIdDigest: string;
  startedAt: string;
  finishedAt: string;
  status: "passed";
}

export interface FrameworkPlatformReportInput {
  schemaVersion: 1;
  candidateCommit: string;
  sourceCommit: string;
  platform: FrameworkPlatform;
  architecture: "x64";
  executionMode: "native";
  publication: "atomic-after-cleanup";
  startedAt: string;
  finishedAt: string;
  toolchains: FrameworkToolchainEvidence[];
  benchmark: FrameworkBenchmarkEvidence;
}

export type FrameworkPlatformReport = FrameworkPlatformReportInput;

const REPORT_KEYS = ["schemaVersion", "candidateCommit", "sourceCommit", "platform", "architecture", "executionMode", "publication", "startedAt", "finishedAt", "toolchains", "benchmark"] as const;
const TOOLCHAIN_KEYS = ["family", "compilerVersion", "compilerSha256", "frameworks"] as const;
const FRAMEWORK_KEYS = ["id", "dependencyVersion", "dependencySha256", "dependencyTreeSha256", "catalogRevision", "catalogArtifactSha256", "sourceArtifactSha256", "sourceLocationDigest", "executableArtifactSha256", "stableIdDigest", "scenarios"] as const;
const SCENARIO_KEYS = ["id", "status", "candidateCommit", "platform", "toolchainFamily", "frameworkId", "catalogRevision", "sourceArtifactSha256", "sourceLocationDigest", "executableArtifactSha256", "resultArtifactSha256", "resultArtifactSizeBytes", "startedAt", "finishedAt", "observedOutcome", "classification"] as const;
const BENCHMARK_KEYS = ["id", "itemCount", "sampleCount", "allocationBudgetPerOperation", "allocationsPerOperation", "catalogRevision", "catalogArtifactSha256", "stableIdDigest", "startedAt", "finishedAt", "status"] as const;
const CMOCK_KEYS = ["revision", "generatorVersion", "inputSha256", "outputSha256", "manifestSha256", "generatedAtRuntime"] as const;

export function validateFrameworkScenarioSet(value: unknown): asserts value is readonly FrameworkScenarioEvidence[] {
  if (!Array.isArray(value) || value.length !== FRAMEWORK_SCENARIO_IDS.length) fail("framework scenario order is invalid");
  for (let index = 0; index < FRAMEWORK_SCENARIO_IDS.length; index += 1) {
    const scenario = closedObject(value[index], SCENARIO_KEYS, "framework scenario");
    const id = FRAMEWORK_SCENARIO_IDS[index]!;
    const result = SCENARIO_RESULTS[id];
    if (scenario.id !== id || scenario.status !== "passed" || scenario.observedOutcome !== result[0] || scenario.classification !== result[1]) {
      fail("framework scenario ID, order, or result is invalid");
    }
    commit(scenario.candidateCommit, "scenario candidate commit");
    oneOf(scenario.platform, ["linux", "win32"], "scenario platform");
    oneOf(scenario.toolchainFamily, ["clang", "gcc", "clang-cl", "msvc"], "scenario toolchain");
    oneOf(scenario.frameworkId, ["cpputest", "unity"], "scenario framework");
    for (const key of ["catalogRevision", "sourceArtifactSha256", "sourceLocationDigest", "executableArtifactSha256", "resultArtifactSha256"] as const) digest(scenario[key], `scenario ${key}`);
    positiveInteger(scenario.resultArtifactSizeBytes, "scenario artifact size");
    interval(scenario.startedAt, scenario.finishedAt, "scenario");
  }
}

export function buildFrameworkPlatformReport(input: FrameworkPlatformReportInput): FrameworkPlatformReport {
  const report = closedObject(input, REPORT_KEYS, "framework report");
  if (report.schemaVersion !== 1 || report.architecture !== "x64" || report.executionMode !== "native" || report.publication !== "atomic-after-cleanup") fail("framework report fields are invalid");
  commit(report.candidateCommit, "candidate commit");
  if (report.sourceCommit !== report.candidateCommit) fail("source commit must equal candidate commit");
  const platform = oneOf(report.platform, ["linux", "win32"] as const, "platform");
  const reportInterval = interval(report.startedAt, report.finishedAt, "report");
  if (!Array.isArray(report.toolchains)) fail("platform toolchains are invalid");
  const expectedToolchains = PLATFORM_TOOLCHAINS[platform];
  if (report.toolchains.length !== expectedToolchains.length) fail("platform toolchains are incomplete");
  const resultDigests = new Set<string>();
  const toolchains = report.toolchains.map((candidate, toolchainIndex) => {
    const toolchain = closedObject(candidate, TOOLCHAIN_KEYS, "framework toolchain");
    const family = expectedToolchains[toolchainIndex]!;
    if (toolchain.family !== family || typeof toolchain.compilerVersion !== "string" || !VERSION.test(toolchain.compilerVersion) || hasPath(toolchain.compilerVersion)) fail("compiler identity or toolchain order is invalid");
    digest(toolchain.compilerSha256, "compiler digest");
    if (!Array.isArray(toolchain.frameworks) || toolchain.frameworks.length !== 2) fail("framework set is incomplete");
    const frameworks = toolchain.frameworks.map((candidateFramework, frameworkIndex) => {
      const expectedId = (["cpputest", "unity"] as const)[frameworkIndex]!;
      const expectedKeys = expectedId === "unity" ? [...FRAMEWORK_KEYS, "cMockProvenance"] : FRAMEWORK_KEYS;
      const framework = closedObject(candidateFramework, expectedKeys, "framework evidence");
      const locked = F1_FRAMEWORKS[expectedId];
      if (framework.id !== expectedId || framework.dependencyVersion !== locked.version || framework.dependencySha256 !== locked.sha256 || framework.dependencyTreeSha256 !== locked.treeSha256) fail("F1 framework dependency identity is invalid");
      for (const key of ["catalogRevision", "catalogArtifactSha256", "sourceArtifactSha256", "sourceLocationDigest", "executableArtifactSha256", "stableIdDigest"] as const) digest(framework[key], `framework ${key}`);
      if (expectedId === "unity") validateCMockProvenance(framework.cMockProvenance);
      validateFrameworkScenarioSet(framework.scenarios);
      const scenarios = (framework.scenarios as FrameworkScenarioEvidence[]).map((scenario) => {
        if (scenario.candidateCommit !== report.candidateCommit || scenario.platform !== platform || scenario.toolchainFamily !== family || scenario.frameworkId !== expectedId || scenario.catalogRevision !== framework.catalogRevision || scenario.sourceArtifactSha256 !== framework.sourceArtifactSha256 || scenario.sourceLocationDigest !== framework.sourceLocationDigest || scenario.executableArtifactSha256 !== framework.executableArtifactSha256) fail("scenario evidence binding is invalid");
        within(scenario.startedAt, scenario.finishedAt, reportInterval, "scenario");
        if (resultDigests.has(scenario.resultArtifactSha256)) fail("duplicate result artifact digest");
        resultDigests.add(scenario.resultArtifactSha256);
        return { ...scenario };
      });
      return { ...framework, ...(expectedId === "unity" ? { cMockProvenance: { ...(framework.cMockProvenance as CMockProvenance) } } : {}), scenarios } as FrameworkEvidence;
    });
    return { ...toolchain, frameworks } as FrameworkToolchainEvidence;
  });
  const benchmark = validateBenchmark(report.benchmark, reportInterval);
  return canonicalClone({ ...report, toolchains, benchmark }) as FrameworkPlatformReport;
}

function validateCMockProvenance(value: unknown): asserts value is CMockProvenance {
  const provenance = closedObject(value, CMOCK_KEYS, "CMock provenance");
  if (CMOCK_KEYS.some((key) => provenance[key] !== F1_CMOCK_PROVENANCE[key])) fail("CMock provenance does not match immutable F1 evidence");
}

function validateBenchmark(value: unknown, outer: { started: number; finished: number }): FrameworkBenchmarkEvidence {
  const benchmark = closedObject(value, BENCHMARK_KEYS, "framework benchmark");
  if (benchmark.id !== "catalog-10000" || benchmark.itemCount !== 10000 || benchmark.sampleCount !== 3 || benchmark.allocationBudgetPerOperation !== 300000 || benchmark.status !== "passed") fail("framework benchmark fields are invalid");
  if (!Array.isArray(benchmark.allocationsPerOperation) || benchmark.allocationsPerOperation.length !== 3 || benchmark.allocationsPerOperation.some((value) => !Number.isInteger(value) || value < 0 || value > 300000)) fail("framework benchmark allocations are invalid");
  for (const key of ["catalogRevision", "catalogArtifactSha256", "stableIdDigest"] as const) digest(benchmark[key], `benchmark ${key}`);
  within(benchmark.startedAt, benchmark.finishedAt, outer, "benchmark");
  return { ...benchmark, allocationsPerOperation: [...benchmark.allocationsPerOperation] } as FrameworkBenchmarkEvidence;
}

function closedObject(value: unknown, keys: readonly string[], label: string): Record<string, any> {
  if (value === null || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) fail(`${label} must be a plain object`);
  const actual = Object.keys(value as object).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) fail(`${label} has unexpected or missing fields`);
  for (const item of Object.values(value as object)) if (typeof item === "string" && (item.includes("\0") || hasPath(item))) fail(`${label} contains a path-bearing string`);
  return value as Record<string, any>;
}

function hasPath(value: string): boolean { return value.includes("/") || value.includes("\\"); }
function canonicalClone(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalClone);
  if (value !== null && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalClone((value as Record<string, unknown>)[key])]));
  return value;
}
function fail(message: string): never { throw new Error(message); }
function digest(value: unknown, label: string): asserts value is string { if (typeof value !== "string" || !DIGEST.test(value)) fail(`${label} is invalid`); }
function commit(value: unknown, label: string): asserts value is string { if (typeof value !== "string" || !COMMIT.test(value)) fail(`${label} is invalid`); }
function positiveInteger(value: unknown, label: string): asserts value is number { if (!Number.isInteger(value) || (value as number) < 1 || (value as number) > 1_073_741_824) fail(`${label} is invalid`); }
function oneOf<const T extends readonly string[]>(value: unknown, values: T, label: string): T[number] { if (typeof value !== "string" || !values.includes(value)) fail(`${label} is invalid`); return value as T[number]; }
function interval(startedAt: unknown, finishedAt: unknown, label: string): { started: number; finished: number } {
  if (typeof startedAt !== "string" || typeof finishedAt !== "string" || !TIMESTAMP.test(startedAt) || !TIMESTAMP.test(finishedAt)) fail(`${label} interval is invalid`);
  const started = Date.parse(startedAt); const finished = Date.parse(finishedAt);
  if (!Number.isFinite(started) || !Number.isFinite(finished) || new Date(started).toISOString() !== startedAt || new Date(finished).toISOString() !== finishedAt || started >= finished) fail(`${label} interval is invalid`);
  return { started, finished };
}
function within(startedAt: unknown, finishedAt: unknown, outer: { started: number; finished: number }, label: string): void { const value = interval(startedAt, finishedAt, label); if (value.started < outer.started || value.finished > outer.finished) fail(`${label} interval escapes report interval`); }
