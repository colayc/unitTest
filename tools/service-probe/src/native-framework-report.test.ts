import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  buildFrameworkPlatformReport,
  validateFrameworkScenarioSet,
  type FrameworkPlatformReportInput,
  type FrameworkScenarioEvidence,
} from "./native-framework-report.js";

const candidateCommit = "1".repeat(40);
const scenarioIds = [
  "all", "assertion-failure", "cancel", "crash", "discovery", "failed-rerun",
  "filter", "malformed-output", "mock-failure", "opaque-fallback", "reconnect-replay",
  "repeat", "service-restart", "single", "skip", "stale-catalog", "timeout",
] as const;
const scenarioResults = {
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

function digest(label: string): string {
  return createHash("sha256").update(label).digest("hex");
}

function platformInput(platform: "linux" | "win32" = "linux"): FrameworkPlatformReportInput {
  const families = platform === "linux" ? ["clang", "gcc"] as const : ["clang-cl", "msvc"] as const;
  return {
    schemaVersion: 1,
    candidateCommit,
    sourceCommit: candidateCommit,
    platform,
    architecture: "x64",
    executionMode: "native",
    publication: "atomic-after-cleanup",
    startedAt: "2026-09-16T00:00:00.000Z",
    finishedAt: "2026-09-16T00:20:00.000Z",
    toolchains: families.map((family) => ({
      family,
      compilerVersion: family === "msvc" ? "19.44.35228.0" : "22.1.8",
      compilerSha256: digest(`compiler:${family}`),
      frameworks: (["cpputest", "unity"] as const).map((frameworkId) => {
        const catalogRevision = digest(`catalog:${family}:${frameworkId}`);
        const sourceArtifactSha256 = digest(`source:${frameworkId}`);
        const sourceLocationDigest = digest(`locations:${frameworkId}`);
        const executableArtifactSha256 = digest(`executable:${family}:${frameworkId}`);
        return {
          id: frameworkId,
          dependencyVersion: frameworkId === "cpputest" ? "4.0" : "2.6.1",
          dependencySha256: frameworkId === "cpputest"
            ? "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
            : "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
          dependencyTreeSha256: frameworkId === "cpputest"
            ? "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
            : "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
          catalogRevision,
          catalogArtifactSha256: digest(`catalog-artifact:${family}:${frameworkId}`),
          sourceArtifactSha256,
          sourceLocationDigest,
          executableArtifactSha256,
          stableIdDigest: digest(`stable:${frameworkId}`),
          ...(frameworkId === "unity" ? {
            cMockProvenance: {
              revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
              generatorVersion: "2.7.0",
              inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
              outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
              manifestSha256: "038b46e53f833097d6311564c3d647c418ba254e1b129ada161ab40f3b0d961a",
              generatedAtRuntime: false as const,
            },
          } : {}),
          scenarios: scenarioIds.map((id, index) => ({
            id,
            status: "passed" as const,
            candidateCommit,
            platform,
            toolchainFamily: family,
            frameworkId,
            catalogRevision,
            sourceArtifactSha256,
            sourceLocationDigest,
            executableArtifactSha256,
            resultArtifactSha256: digest(`result:${platform}:${family}:${frameworkId}:${id}`),
            resultArtifactSizeBytes: 1024 + index,
            startedAt: `2026-09-16T00:00:${String(10 + index).padStart(2, "0")}.000Z`,
            finishedAt: `2026-09-16T00:01:${String(10 + index).padStart(2, "0")}.000Z`,
            observedOutcome: scenarioResults[id][0],
            classification: scenarioResults[id][1],
          })),
        };
      }),
    })),
    benchmark: {
      id: "catalog-10000",
      itemCount: 10000,
      sampleCount: 3,
      allocationBudgetPerOperation: 300000,
      allocationsPerOperation: [210120, 210120, 210120],
      catalogRevision: digest("benchmark-catalog"),
      catalogArtifactSha256: digest("benchmark-artifact"),
      stableIdDigest: digest("benchmark-stable"),
      startedAt: "2026-09-16T00:18:00.000Z",
      finishedAt: "2026-09-16T00:19:00.000Z",
      status: "passed",
    },
  };
}

test("builder returns a detached canonical platform report", () => {
  const input = platformInput();
  const report = buildFrameworkPlatformReport(input);
  assert.deepEqual(report, input);
  assert.notEqual(report, input);
  assert.notEqual(report.toolchains, input.toolchains);
  assert.deepEqual(Object.keys(report), [...Object.keys(report)].sort());
  assert.deepEqual(Object.keys(report.toolchains[0]!.frameworks[0]!), [...Object.keys(report.toolchains[0]!.frameworks[0]!)].sort());
});

test("builder rejects missing report fields and extra keys", () => {
  const missing = platformInput() as unknown as Record<string, unknown>;
  delete missing.publication;
  assert.throws(() => buildFrameworkPlatformReport(missing as unknown as FrameworkPlatformReportInput), /report.*fields|publication/iu);

  const extra = { ...platformInput(), workspacePath: "C:\\private\\checkout" };
  assert.throws(() => buildFrameworkPlatformReport(extra as unknown as FrameworkPlatformReportInput), /unexpected.*field|path/iu);
});

test("scenario validator rejects duplicate, reordered, and unsafe scenario IDs", () => {
  const scenarios = platformInput().toolchains[0]!.frameworks[0]!.scenarios;
  validateFrameworkScenarioSet(scenarios);
  const duplicate = scenarios.map((item) => ({ ...item }));
  duplicate[1] = { ...duplicate[1]!, id: "all" } as FrameworkScenarioEvidence;
  assert.throws(() => validateFrameworkScenarioSet(duplicate), /scenario.*order|scenario.*id/iu);
  const reordered = scenarios.map((item) => ({ ...item }));
  [reordered[0], reordered[1]] = [reordered[1]!, reordered[0]!];
  assert.throws(() => validateFrameworkScenarioSet(reordered), /scenario.*order|scenario.*id/iu);
  assert.throws(() => validateFrameworkScenarioSet([{ ...scenarios[0], id: "C:\\tmp" }]), /scenario.*order|scenario.*id/iu);
});

test("builder rejects path-bearing strings and reports missing either platform toolchain", () => {
  const pathBearing = platformInput();
  pathBearing.toolchains[0]!.compilerVersion = "/usr/bin/clang";
  assert.throws(() => buildFrameworkPlatformReport(pathBearing), /path|compiler/iu);

  const incomplete = platformInput();
  incomplete.toolchains.pop();
  assert.throws(() => buildFrameworkPlatformReport(incomplete), /toolchain/iu);
});

test("builder binds the committed F1 digests and immutable CMock provenance", () => {
  const wrongTree = platformInput();
  wrongTree.toolchains[0]!.frameworks[0]!.dependencyTreeSha256 = "0".repeat(64);
  assert.throws(() => buildFrameworkPlatformReport(wrongTree), /F1|dependency.*identity/iu);

  const mutable = platformInput();
  mutable.toolchains[0]!.frameworks[1]!.cMockProvenance!.generatedAtRuntime = true as false;
  assert.throws(() => buildFrameworkPlatformReport(mutable), /CMock.*provenance|runtime/iu);

  const frameworkManifestSubstitution = platformInput();
  frameworkManifestSubstitution.toolchains[0]!.frameworks[1]!.cMockProvenance!.manifestSha256 =
    "2f08cfd45b9374a5331f0484d53b466c3813312d046e5226c64754c0c986f87b";
  assert.throws(() => buildFrameworkPlatformReport(frameworkManifestSubstitution), /CMock.*provenance/iu);
});

test("builder rejects duplicate result artifact digests across the platform report", () => {
  const input = platformInput();
  input.toolchains[1]!.frameworks[1]!.scenarios[16]!.resultArtifactSha256 =
    input.toolchains[0]!.frameworks[0]!.scenarios[0]!.resultArtifactSha256;
  assert.throws(() => buildFrameworkPlatformReport(input), /duplicate.*result|result.*digest/iu);
});
