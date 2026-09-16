import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import Ajv2020 from "ajv/dist/2020.js";
import { readCMockGeneration } from "../framework-bundle/cmock-provenance.mjs";
import { readFrameworkManifest } from "../framework-bundle/manifest.mjs";
import { encodeCanonicalJson, readCanonicalJson } from "./canonical-json.mjs";
import { buildMatrixReport } from "./p4-report.mjs";
import schema from "./p4-report.schema.json" with { type: "json" };

const execFileAsync = promisify(execFile);
const candidateCommit = "a".repeat(40);

const ajv = new Ajv2020({ allErrors: true, strict: true });
ajv.compile(schema);
const validateScenarioSet = ajv.getSchema(`${schema.$id}#/$defs/scenarioSet`);
const validatePlatformToolchains = ajv.getSchema(`${schema.$id}#/$defs/platformToolchains`);

const scenarioIds = [
  "all", "assertion-failure", "cancel", "crash", "discovery", "failed-rerun",
  "filter", "malformed-output", "mock-failure", "opaque-fallback", "reconnect-replay",
  "repeat", "service-restart", "single", "skip", "stale-catalog", "timeout",
];

test("committed CMock provenance digest comes from the closed F1 reader", async () => {
  const { manifest, manifestSha256 } = await readFrameworkManifest();
  const provenance = await readCMockGeneration("testdata/frameworks/unity/mocks/cmock-generation.json", {
    root: process.cwd(),
    manifest,
    manifestSha256,
  });
  assert.equal(manifestSha256, "2f08cfd45b9374a5331f0484d53b466c3813312d046e5226c64754c0c986f87b");
  assert.equal(provenance.cMockProvenanceSha256, "4f0a73e5decc2402930fc4d609d1640150fe6addb30e20cc9900e1cf418520a8");
  assert.notEqual(provenance.cMockProvenanceSha256, manifestSha256);
});

test("schema exposes a closed exact-order scenario-set contract", () => {
  assert.equal(typeof validateScenarioSet, "function");
  const scenarios = scenarioIds.map((id) => ({ id }));
  assert.equal(validateScenarioSet(scenarios), true, JSON.stringify(validateScenarioSet.errors));
  const reordered = structuredClone(scenarios);
  [reordered[0], reordered[1]] = [reordered[1], reordered[0]];
  assert.equal(validateScenarioSet(reordered), false);
  const duplicate = structuredClone(scenarios);
  duplicate[1].id = "all";
  assert.equal(validateScenarioSet(duplicate), false);
});

test("schema exposes the exact toolchain pair for each platform", () => {
  assert.equal(typeof validatePlatformToolchains, "function");
  assert.equal(validatePlatformToolchains({ platform: "linux", toolchains: [{ family: "clang" }, { family: "gcc" }] }), true);
  assert.equal(validatePlatformToolchains({ platform: "linux", toolchains: [{ family: "clang" }] }), false);
  assert.equal(validatePlatformToolchains({ platform: "win32", toolchains: [{ family: "clang-cl" }, { family: "msvc" }] }), true);
  assert.equal(validatePlatformToolchains({ platform: "win32", toolchains: [{ family: "msvc" }, { family: "clang-cl" }] }), false);
});

test("matrix builder binds two platform reports, eight framework blocks, and 136 detached scenario records", () => {
  const windows = platformReport("win32");
  const linux = platformReport("linux");
  const matrix = buildMatrixReport({ candidateCommit, windows, linux });
  assert.equal(matrix.candidateCommit, candidateCommit);
  assert.deepEqual(matrix.platforms.map(({ platform }) => platform), ["linux", "win32"]);
  assert.equal(matrix.platforms.flatMap(({ toolchains }) => toolchains.flatMap(({ frameworks }) => frameworks)).length, 8);
  assert.equal(matrix.platforms.flatMap(({ toolchains }) => toolchains.flatMap(
    ({ frameworks }) => frameworks.flatMap(({ scenarios }) => scenarios),
  )).length, 136);

  linux.toolchains[0].frameworks[0].scenarios[0].candidateCommit = "f".repeat(40);
  assert.equal(matrix.platforms[0].toolchains[0].frameworks[0].scenarios[0].candidateCommit, candidateCommit);
});

test("matrix CLI consumes the exact framework-report artifact layout and writes deterministic canonical bytes", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "p4-native-framework-matrix-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const windows = join(root, ".native-e2e", "framework-inputs", "windows", "framework-report.json");
  const linux = join(root, ".native-e2e", "framework-inputs", "linux", "framework-report.json");
  const output = join(root, ".superpowers", "phase9", "p4", "native-framework-matrix-report.json");
  await mkdir(join(root, ".native-e2e", "framework-inputs", "windows"), { recursive: true });
  await mkdir(join(root, ".native-e2e", "framework-inputs", "linux"), { recursive: true });
  await writeFile(windows, encodeCanonicalJson(platformReport("win32")));
  await writeFile(linux, encodeCanonicalJson(platformReport("linux")));
  const arguments_ = [
    join(import.meta.dirname, "p4-report.mjs"),
    "--windows", windows,
    "--linux", linux,
    "--candidate", candidateCommit,
    "--out", output,
  ];
  await execFileAsync(process.execPath, arguments_);
  const first = await readFile(output);
  await execFileAsync(process.execPath, arguments_);
  assert.deepEqual(await readFile(output), first);
  const matrix = await readCanonicalJson(output, { label: "native framework matrix", maxBytes: 1024 * 1024 });
  assert.equal(matrix.platforms.length, 2);
  assert.equal(matrix.platforms.flatMap(({ toolchains }) => toolchains).length, 4);
});

test("matrix validator rejects candidate-unbound and path-bearing report substitutions", () => {
  for (const mutate of [
    (windows) => { delete windows.sourceCommit; },
    (windows) => { windows.sourceCommit = "b".repeat(40); },
    (windows) => { windows.toolchains[0].frameworks[0].sourcePath = "C:\\agent\\secret\\fixture.cpp"; },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios[0].candidateCommit = "b".repeat(40); },
    (windows) => { windows.toolchains[0].frameworks[0].scenarios[0].hostPath = "/home/runner/work/fixture.cpp"; },
  ]) {
    const windows = platformReport("win32");
    mutate(windows);
    assert.throws(
      () => buildMatrixReport({ candidateCommit, windows, linux: platformReport("linux") }),
      /PHASE9_P4_REPORT_INVALID/u,
    );
  }
});

function platformReport(platform) {
  const families = platform === "win32" ? ["clang-cl", "msvc"] : ["clang", "gcc"];
  return {
    schemaVersion: 1,
    candidateCommit,
    sourceCommit: candidateCommit,
    platform,
    architecture: "x64",
    executionMode: "native",
    publication: "atomic-after-cleanup",
    startedAt: "2026-09-15T00:00:00.000Z",
    finishedAt: "2026-09-15T00:20:00.000Z",
    toolchains: families.map((family) => ({
      family,
      compilerVersion: family === "msvc" ? "19.44.35228.0" : "22.1.8",
      compilerSha256: digest(`compiler:${platform}:${family}`),
      frameworks: [framework(platform, family, "cpputest"), framework(platform, family, "unity")],
    })),
    benchmark: {
      id: "catalog-10000",
      itemCount: 10000,
      sampleCount: 3,
      allocationBudgetPerOperation: 300000,
      allocationsPerOperation: [100, 101, 102],
      catalogRevision: digest(`benchmark-catalog:${platform}`),
      catalogArtifactSha256: digest(`benchmark-artifact:${platform}`),
      stableIdDigest: "d".repeat(64),
      startedAt: "2026-09-15T00:02:00.000Z",
      finishedAt: "2026-09-15T00:03:00.000Z",
      status: "passed",
    },
  };
}

function framework(platform, family, id) {
  const catalogRevision = digest(`catalog:${platform}:${family}:${id}`);
  const sourceArtifactSha256 = digest(`source:${id}`);
  const sourceLocationDigest = digest(`locations:${id}`);
  const executableArtifactSha256 = digest(`executable:${platform}:${family}:${id}`);
  return {
    id,
    dependencyVersion: id === "cpputest" ? "4.0" : "2.6.1",
    dependencySha256: id === "cpputest"
      ? "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
      : "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
    dependencyTreeSha256: id === "cpputest"
      ? "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
      : "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
    catalogRevision,
    catalogArtifactSha256: digest(`catalog-artifact:${platform}:${family}:${id}`),
    sourceArtifactSha256,
    sourceLocationDigest,
    executableArtifactSha256,
    stableIdDigest: id === "cpputest" ? "b".repeat(64) : "c".repeat(64),
    ...(id === "unity" ? { cMockProvenance: {
      revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
      generatorVersion: "2.7.0",
      inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
      outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
      manifestSha256: "4f0a73e5decc2402930fc4d609d1640150fe6addb30e20cc9900e1cf418520a8",
      generatedAtRuntime: false,
    } } : {}),
    scenarios: scenarioIds.map((scenarioId, index) => ({
      id: scenarioId,
      status: "passed",
      candidateCommit,
      platform,
      toolchainFamily: family,
      frameworkId: id,
      catalogRevision,
      sourceArtifactSha256,
      sourceLocationDigest,
      executableArtifactSha256,
      resultArtifactSha256: digest(`result:${platform}:${family}:${id}:${scenarioId}`),
      resultArtifactSizeBytes: 1024 + index,
      startedAt: `2026-09-15T00:00:${String(10 + index).padStart(2, "0")}.000Z`,
      finishedAt: `2026-09-15T00:01:${String(10 + index).padStart(2, "0")}.000Z`,
      observedOutcome: scenarioResults[scenarioId][0],
      classification: scenarioResults[scenarioId][1],
    })),
  };
}

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
};

function digest(value) {
  return createHash("sha256").update(value).digest("hex");
}
