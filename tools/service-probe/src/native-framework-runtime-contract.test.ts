import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

const moduleUrl = new URL("./native-framework-runtime-contract.js", import.meta.url).href;
const {
  buildFrameworkRuntimeManifest,
  parseFrameworkRuntimeManifest,
} = await import(moduleUrl) as {
  buildFrameworkRuntimeManifest(input: unknown): Record<string, any>;
  parseFrameworkRuntimeManifest(
    bytes: Uint8Array,
    expectedPlatform: "linux" | "win32",
    expectedContractSha256: string,
  ): Record<string, any>;
};

const candidateCommit = "0123456789abcdef0123456789abcdef01234567";
const contractSha256 = digest("framework matrix contract");

test("builder returns a canonical detached Windows runtime manifest", () => {
  const input = windowsManifest();
  const result = buildFrameworkRuntimeManifest(input);

  assert.deepEqual(result, input);
  assert.notEqual(result, input);
  assert.notEqual(result.toolchains, input.toolchains);
  assert.notEqual(result.toolchains[0], input.toolchains[0]);
  assert.notEqual(result.toolchains[0]!.frameworks[1]!.cMockProvenance,
    input.toolchains[0]!.frameworks[1]!.cMockProvenance);
  assert.notEqual(result.benchmark.allocationsPerOperation, input.benchmark.allocationsPerOperation);
  assert.deepEqual(result.toolchains.map(({ family }: { family: string }) => family), ["clang-cl", "msvc"]);
  assert.ok(result.toolchains.every(({ frameworks }: { frameworks: Array<{ frameworkId: string }> }) =>
    frameworks.map(({ frameworkId }) => frameworkId).join(",") === "cpputest,unity"
  ));
  assert.deepEqual(Object.keys(result), [
    "benchmark", "candidateCommit", "contractSha256", "platform", "schemaVersion", "toolchains",
  ]);

  input.toolchains[0]!.compilerVersion = "99.99";
  input.toolchains[0]!.frameworks[0]!.evidence.sourceArtifactSha256 = digest("mutated");
  input.benchmark.allocationsPerOperation[0] = 299_999;
  assert.equal(result.toolchains[0]!.compilerVersion, "18.1.8");
  assert.equal(result.toolchains[0]!.frameworks[0]!.evidence.sourceArtifactSha256,
    digest("source:clang-cl:cpputest"));
  assert.deepEqual(result.benchmark.allocationsPerOperation, [101, 102, 103]);
});

test("parser binds the manifest to the expected platform and contract", () => {
  const input = windowsManifest();
  const bytes = Buffer.from(`${JSON.stringify(input)}\n`);
  assert.deepEqual(parseFrameworkRuntimeManifest(bytes, "win32", contractSha256),
    buildFrameworkRuntimeManifest(input));
  assert.throws(() => parseFrameworkRuntimeManifest(bytes, "linux", contractSha256), /platform.*bound|platform.*expected/iu);
  assert.throws(() => parseFrameworkRuntimeManifest(bytes, "win32", digest("other contract")), /bound.*contract|contract.*bound/iu);
});

test("builder rejects open, reordered, duplicated, path-bearing, and malformed runtime inputs", () => {
  const cases: ReadonlyArray<readonly [string, (manifest: ReturnType<typeof windowsManifest>) => void, RegExp]> = [
    ["top-level extra field", (value) => { (value as any).command = "compiler"; }, /unexpected|missing/iu],
    ["nested extra field", (value) => { (value.toolchains[0]!.frameworks[0]! as any).shell = "cmd"; }, /unexpected|missing/iu],
    ["path-bearing string", (value) => { value.toolchains[0]!.frameworks[0]!.dependencyVersion = "C:\\deps\\4.0"; }, /path-bearing/iu],
    ["wrong toolchain order", (value) => { value.toolchains.reverse(); }, /toolchain.*order|toolchains.*invalid/iu],
    ["duplicate toolchain", (value) => { value.toolchains[1] = structuredClone(value.toolchains[0]!); }, /toolchain.*order|toolchains.*invalid/iu],
    ["wrong framework order", (value) => { value.toolchains[0]!.frameworks.reverse(); }, /framework.*order|framework set|unexpected.*missing/iu],
    ["missing CMock evidence", (value) => { delete value.toolchains[0]!.frameworks[1]!.cMockProvenance; }, /CMock.*required|unexpected.*missing/iu],
    ["CMock on CppUTest", (value) => { value.toolchains[0]!.frameworks[0]!.cMockProvenance = structuredClone(value.toolchains[0]!.frameworks[1]!.cMockProvenance); }, /CMock|unexpected.*missing/iu],
    ["invalid candidate commit", (value) => { value.candidateCommit = value.candidateCommit.toUpperCase(); }, /candidate commit.*invalid/iu],
    ["invalid compiler version", (value) => { value.toolchains[0]!.compilerVersion = "clang 18.1"; }, /compiler version.*invalid/iu],
    ["invalid digest", (value) => { value.toolchains[0]!.compilerSha256 = "A".repeat(64); }, /compiler digest.*invalid/iu],
    ["invalid timeout", (value) => { value.toolchains[0]!.frameworks[0]!.timeoutMs = 0; }, /timeout.*invalid/iu],
    ["invalid benchmark identity", (value) => { value.benchmark.itemCount = 9_999; }, /benchmark fields.*invalid/iu],
    ["invalid benchmark samples", (value) => { value.benchmark.allocationsPerOperation = [1, 2]; }, /benchmark allocations.*invalid/iu],
    ["benchmark over budget", (value) => { value.benchmark.allocationsPerOperation[1] = 300_001; }, /benchmark allocations.*invalid/iu],
  ];

  for (const [name, mutate, pattern] of cases) {
    const input = windowsManifest();
    mutate(input);
    assert.throws(() => buildFrameworkRuntimeManifest(input), pattern, name);
  }
});

test("parser rejects invalid JSON and a non-lowercase expected contract digest", () => {
  assert.throws(
    () => parseFrameworkRuntimeManifest(Buffer.from("{"), "win32", contractSha256),
    /not valid JSON/iu,
  );
  assert.throws(
    () => parseFrameworkRuntimeManifest(Buffer.from(JSON.stringify(windowsManifest())), "win32", "A".repeat(64)),
    /expected contract digest.*invalid/iu,
  );
});

function windowsManifest() {
  const framework = (family: "clang-cl" | "msvc", frameworkId: "cpputest" | "unity") => ({
    frameworkId,
    dependencyVersion: frameworkId === "cpputest" ? "4.0" : "2.6.1",
    dependencySha256: digest(`dependency:${frameworkId}`),
    dependencyTreeSha256: digest(`tree:${frameworkId}`),
    catalogArtifactSha256: digest(`catalog:${family}:${frameworkId}`),
    stableIdDigest: digest(`stable:${family}:${frameworkId}`),
    timeoutMs: 120_000,
    evidence: {
      sourceArtifactSha256: digest(`source:${family}:${frameworkId}`),
      sourceLocationDigest: digest(`locations:${family}:${frameworkId}`),
      executableArtifactSha256: digest(`executable:${family}:${frameworkId}`),
    },
    ...(frameworkId === "unity" ? {
      cMockProvenance: {
        revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
        generatorVersion: "2.7.0",
        inputSha256: digest("cmock input"),
        outputSha256: digest("cmock output"),
        manifestSha256: digest("cmock manifest"),
        generatedAtRuntime: false as const,
      },
    } : {}),
  });
  const toolchain = (family: "clang-cl" | "msvc", compilerVersion: string) => ({
    family,
    compilerVersion,
    compilerSha256: digest(`compiler:${family}`),
    frameworks: [framework(family, "cpputest"), framework(family, "unity")],
  });
  return {
    schemaVersion: 1 as const,
    platform: "win32" as const,
    candidateCommit,
    contractSha256,
    toolchains: [toolchain("clang-cl", "18.1.8"), toolchain("msvc", "19.44.35222.0")],
    benchmark: {
      id: "catalog-10000" as const,
      itemCount: 10_000,
      sampleCount: 3,
      allocationBudgetPerOperation: 300_000,
      allocationsPerOperation: [101, 102, 103],
      catalogRevision: digest("benchmark catalog"),
      catalogArtifactSha256: digest("benchmark artifact"),
      stableIdDigest: digest("benchmark stable ID"),
      status: "passed" as const,
    },
  };
}

function digest(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}
