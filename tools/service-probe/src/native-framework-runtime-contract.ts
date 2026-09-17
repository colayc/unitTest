import type {
  CMockProvenance,
  FrameworkBenchmarkEvidence,
  FrameworkId,
  FrameworkPlatform,
  FrameworkToolchainFamily,
} from "./native-framework-report.js";

const DIGEST = /^[0-9a-f]{64}$/u;
const COMMIT = /^[0-9a-f]{40}$/u;
const VERSION = /^[0-9]+(?:\.[0-9]+){1,3}$/u;
const MAX_TIMEOUT_MS = 120_000;

const MANIFEST_KEYS = [
  "benchmark", "candidateCommit", "contractSha256", "platform", "schemaVersion", "toolchains",
] as const;
const TOOLCHAIN_KEYS = ["compilerSha256", "compilerVersion", "family", "frameworks"] as const;
const FRAMEWORK_KEYS = [
  "catalogArtifactSha256", "dependencySha256", "dependencyTreeSha256", "dependencyVersion",
  "evidence", "frameworkId", "stableIdDigest", "timeoutMs",
] as const;
const EVIDENCE_KEYS = [
  "executableArtifactSha256", "sourceArtifactSha256", "sourceLocationDigest",
] as const;
const CMOCK_KEYS = [
  "generatedAtRuntime", "generatorVersion", "inputSha256", "manifestSha256", "outputSha256", "revision",
] as const;
const BENCHMARK_KEYS = [
  "allocationBudgetPerOperation", "allocationsPerOperation", "catalogArtifactSha256", "catalogRevision",
  "id", "itemCount", "sampleCount", "stableIdDigest", "status",
] as const;

const PLATFORM_TOOLCHAINS: Readonly<Record<FrameworkPlatform, readonly FrameworkToolchainFamily[]>> = {
  linux: ["clang", "gcc"],
  win32: ["clang-cl", "msvc"],
};
const FRAMEWORKS = ["cpputest", "unity"] as const satisfies readonly FrameworkId[];

export interface FrameworkRuntimeFramework {
  readonly frameworkId: FrameworkId;
  readonly dependencyVersion: string;
  readonly dependencySha256: string;
  readonly dependencyTreeSha256: string;
  readonly catalogArtifactSha256: string;
  readonly stableIdDigest: string;
  readonly timeoutMs: number;
  readonly evidence: {
    readonly sourceArtifactSha256: string;
    readonly sourceLocationDigest: string;
    readonly executableArtifactSha256: string;
  };
  readonly cMockProvenance?: Readonly<CMockProvenance>;
}

export interface FrameworkRuntimeToolchain {
  readonly family: FrameworkToolchainFamily;
  readonly compilerVersion: string;
  readonly compilerSha256: string;
  readonly frameworks: readonly FrameworkRuntimeFramework[];
}

type RuntimeBenchmark = Readonly<Omit<FrameworkBenchmarkEvidence, "startedAt" | "finishedAt">> & {
  readonly allocationsPerOperation: readonly [number, number, number];
};

export interface FrameworkRuntimeManifest {
  readonly schemaVersion: 1;
  readonly candidateCommit: string;
  readonly contractSha256: string;
  readonly platform: FrameworkPlatform;
  readonly toolchains: readonly FrameworkRuntimeToolchain[];
  readonly benchmark: RuntimeBenchmark;
}

export function parseFrameworkRuntimeManifest(
  bytes: Uint8Array,
  expectedPlatform: FrameworkPlatform,
  expectedContractSha256: string,
): FrameworkRuntimeManifest {
  digest(expectedContractSha256, "expected contract digest");
  let input: unknown;
  try {
    input = JSON.parse(Buffer.from(bytes).toString("utf8"));
  } catch {
    fail("framework runtime manifest is not valid JSON");
  }
  const manifest = buildFrameworkRuntimeManifest(input);
  if (manifest.platform !== expectedPlatform) fail("framework runtime manifest platform is not bound to the expected platform");
  if (manifest.contractSha256 !== expectedContractSha256) fail("framework runtime manifest is not bound to the matrix contract");
  return manifest;
}

export function buildFrameworkRuntimeManifest(input: unknown): FrameworkRuntimeManifest {
  const manifest = closedObject(input, MANIFEST_KEYS, "framework runtime manifest");
  if (manifest.schemaVersion !== 1) fail("framework runtime schema version is invalid");
  commit(manifest.candidateCommit, "candidate commit");
  digest(manifest.contractSha256, "contract digest");
  const platform = oneOf(manifest.platform, ["linux", "win32"] as const, "platform");
  if (!Array.isArray(manifest.toolchains)) fail("framework runtime toolchains are invalid");
  const expectedToolchains = PLATFORM_TOOLCHAINS[platform];
  if (manifest.toolchains.length !== expectedToolchains.length) fail("framework runtime toolchain order is invalid");

  const toolchains = manifest.toolchains.map((candidate, toolchainIndex) => {
    const toolchain = closedObject(candidate, TOOLCHAIN_KEYS, "framework runtime toolchain");
    const family = expectedToolchains[toolchainIndex]!;
    if (toolchain.family !== family) fail("framework runtime toolchain order is invalid");
    version(toolchain.compilerVersion, "compiler version");
    digest(toolchain.compilerSha256, "compiler digest");
    if (!Array.isArray(toolchain.frameworks) || toolchain.frameworks.length !== FRAMEWORKS.length) {
      fail("framework runtime framework set is invalid");
    }
    const frameworks = toolchain.frameworks.map((candidateFramework, frameworkIndex) =>
      validateFramework(candidateFramework, FRAMEWORKS[frameworkIndex]!)
    );
    return {
      family,
      compilerVersion: toolchain.compilerVersion,
      compilerSha256: toolchain.compilerSha256,
      frameworks,
    } as FrameworkRuntimeToolchain;
  });
  const benchmark = validateBenchmark(manifest.benchmark);
  return canonicalClone({
    schemaVersion: 1,
    candidateCommit: manifest.candidateCommit,
    contractSha256: manifest.contractSha256,
    platform,
    toolchains,
    benchmark,
  }) as FrameworkRuntimeManifest;
}

function validateFramework(input: unknown, expectedId: FrameworkId): FrameworkRuntimeFramework {
  const keys = expectedId === "unity" ? [...FRAMEWORK_KEYS, "cMockProvenance"] : FRAMEWORK_KEYS;
  const framework = closedObject(input, keys, "framework runtime framework");
  if (framework.frameworkId !== expectedId) fail("framework runtime framework order is invalid");
  version(framework.dependencyVersion, "dependency version");
  for (const [key, label] of [
    ["dependencySha256", "dependency digest"],
    ["dependencyTreeSha256", "dependency tree digest"],
    ["catalogArtifactSha256", "catalog artifact digest"],
    ["stableIdDigest", "stable ID digest"],
  ] as const) digest(framework[key], label);
  if (!Number.isSafeInteger(framework.timeoutMs) || framework.timeoutMs < 1 || framework.timeoutMs > MAX_TIMEOUT_MS) {
    fail("framework runtime timeout is invalid");
  }
  const evidence = closedObject(framework.evidence, EVIDENCE_KEYS, "framework runtime evidence");
  for (const key of EVIDENCE_KEYS) digest(evidence[key], `framework evidence ${key}`);
  const cMockProvenance = expectedId === "unity" ? validateCMockProvenance(framework.cMockProvenance) : undefined;
  return {
    frameworkId: expectedId,
    dependencyVersion: framework.dependencyVersion,
    dependencySha256: framework.dependencySha256,
    dependencyTreeSha256: framework.dependencyTreeSha256,
    catalogArtifactSha256: framework.catalogArtifactSha256,
    stableIdDigest: framework.stableIdDigest,
    timeoutMs: framework.timeoutMs,
    evidence: {
      sourceArtifactSha256: evidence.sourceArtifactSha256,
      sourceLocationDigest: evidence.sourceLocationDigest,
      executableArtifactSha256: evidence.executableArtifactSha256,
    },
    ...(cMockProvenance === undefined ? {} : { cMockProvenance }),
  } as FrameworkRuntimeFramework;
}

function validateCMockProvenance(input: unknown): Readonly<CMockProvenance> {
  const provenance = closedObject(input, CMOCK_KEYS, "CMock provenance");
  commit(provenance.revision, "CMock revision");
  version(provenance.generatorVersion, "CMock generator version");
  digest(provenance.inputSha256, "CMock input digest");
  digest(provenance.outputSha256, "CMock output digest");
  digest(provenance.manifestSha256, "CMock manifest digest");
  if (provenance.generatedAtRuntime !== false) fail("CMock generated-at-runtime evidence is invalid");
  return { ...provenance } as unknown as Readonly<CMockProvenance>;
}

function validateBenchmark(input: unknown): RuntimeBenchmark {
  const benchmark = closedObject(input, BENCHMARK_KEYS, "framework runtime benchmark");
  if (
    benchmark.id !== "catalog-10000" ||
    benchmark.itemCount !== 10_000 ||
    benchmark.sampleCount !== 3 ||
    benchmark.allocationBudgetPerOperation !== 300_000 ||
    benchmark.status !== "passed"
  ) fail("framework runtime benchmark fields are invalid");
  if (
    !Array.isArray(benchmark.allocationsPerOperation) ||
    benchmark.allocationsPerOperation.length !== 3 ||
    benchmark.allocationsPerOperation.some((value) =>
      !Number.isSafeInteger(value) || value < 0 || value > 300_000
    )
  ) fail("framework runtime benchmark allocations are invalid");
  for (const key of ["catalogRevision", "catalogArtifactSha256", "stableIdDigest"] as const) {
    digest(benchmark[key], `framework benchmark ${key}`);
  }
  return {
    id: "catalog-10000",
    itemCount: 10_000,
    sampleCount: 3,
    allocationBudgetPerOperation: 300_000,
    allocationsPerOperation: [...benchmark.allocationsPerOperation] as [number, number, number],
    catalogRevision: benchmark.catalogRevision,
    catalogArtifactSha256: benchmark.catalogArtifactSha256,
    stableIdDigest: benchmark.stableIdDigest,
    status: "passed",
  };
}

function closedObject(value: unknown, keys: readonly string[], label: string): Record<string, any> {
  if (
    value === null || typeof value !== "object" || Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) fail(`${label} must be a plain object`);
  const actual = Reflect.ownKeys(value).sort(codePointCompare);
  const expected = [...keys].sort(codePointCompare);
  if (
    actual.length !== expected.length ||
    actual.some((key, index) => typeof key !== "string" || key !== expected[index])
  ) fail(`${label} has unexpected or missing fields`);
  for (const item of Object.values(value)) {
    if (typeof item === "string" && (item.includes("\0") || item.includes("/") || item.includes("\\"))) {
      fail(`${label} contains a path-bearing string`);
    }
  }
  return value as Record<string, any>;
}

function canonicalClone(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalClone);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort(codePointCompare).map((key) => [
      key, canonicalClone((value as Record<string, unknown>)[key]),
    ]));
  }
  return value;
}

function codePointCompare(left: PropertyKey, right: PropertyKey): number {
  return String(left) < String(right) ? -1 : String(left) > String(right) ? 1 : 0;
}
function fail(message: string): never { throw new Error(message); }
function digest(value: unknown, label: string): asserts value is string {
  if (typeof value !== "string" || !DIGEST.test(value)) fail(`${label} is invalid`);
}
function commit(value: unknown, label: string): asserts value is string {
  if (typeof value !== "string" || !COMMIT.test(value)) fail(`${label} is invalid`);
}
function version(value: unknown, label: string): asserts value is string {
  if (typeof value !== "string" || !VERSION.test(value)) fail(`${label} is invalid`);
}
function oneOf<const T extends readonly string[]>(value: unknown, values: T, label: string): T[number] {
  if (typeof value !== "string" || !values.includes(value)) fail(`${label} is invalid`);
  return value as T[number];
}
