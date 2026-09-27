import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import schema from "./test-generation-report.schema.json" with { type: "json" };

import { buildMatrixReport, buildToolchainReport } from "./test-generation-report.mjs";

const commit = "a".repeat(40);
const digest = (label) => createHash("sha256").update(label).digest("hex");

function coverage(base, final) {
  return {
    baseline: { functions: { covered: base, total: 10 }, lines: { covered: base, total: 20 }, branches: { covered: base, total: 30 } },
    final: { functions: { covered: final, total: 10 }, lines: { covered: final, total: 20 }, branches: { covered: final, total: 30 } },
  };
}

function block(platform, family, framework, index) {
  return {
    schemaVersion: 1,
    candidateCommit: commit,
    platform,
    toolchainFamily: family,
    framework,
    compiler: { family, version: "1.2.3", sha256: digest(`compiler-${index}`) },
    frameworkIdentity: { version: framework === "cpputest" ? "4.0" : "2.6.1", sha256: digest(`framework-${framework}`), treeSha256: digest(`tree-${framework}`) },
    cmake: { version: "3.30.0", sha256: digest(`cmake-${index}`) },
    coverage: coverage(2, 4),
    candidates: { generated: 3, retained: 2, rejected: 1 },
    tests: [
      { id: `case-${index}-1`, candidateKind: "verified", assertionCount: 1, sourceArtifactSha256: digest(`source-${index}-1`), outputArtifactSha256: digest(`output-${index}-1`) },
      { id: `case-${index}-2`, candidateKind: "characterization", assertionCount: 1, sourceArtifactSha256: digest(`source-${index}-2`), outputArtifactSha256: digest(`output-${index}-2`) },
    ],
    faultScenarios: [
      { id: "cancel-before-build", status: "passed", artifactSha256: digest(`fault-${index}-1`) },
      { id: "compile-failure", status: "passed", artifactSha256: digest(`fault-${index}-2`) },
      { id: "service-restart", status: "passed", artifactSha256: digest(`fault-${index}-3`) },
    ],
    inputArtifactSha256: digest(`input-${index}`),
    outputArtifactSha256: digest(`result-${index}`),
    performance: { durationMs: 1000 + index, budgetMs: 5000, peakMemoryBytes: 1000, memoryBudgetBytes: 10000 },
    mutation: { total: 4, killed: 4, requiredKilled: 3 },
  };
}

function matrixBlocks() {
  return [
    ["linux", "gcc"], ["linux", "clang"], ["win32", "msvc"], ["win32", "clang-cl"],
  ].flatMap(([platform, family], platformIndex) => [
    block(platform, family, "cpputest", platformIndex * 2),
    block(platform, family, "unity", platformIndex * 2 + 1),
  ]);
}

test("toolchain report is closed, coverage-improving, and detached", () => {
  const input = block("linux", "gcc", "cpputest", 0);
  const result = buildToolchainReport(input);
  assert.deepEqual(result, input);
  assert.notEqual(result, input);
  assert.throws(() => buildToolchainReport({ ...input, workspacePath: "C:\\\\secret" }), /closed|path/u);
  assert.throws(() => buildToolchainReport({ ...input, coverage: coverage(4, 4) }), /coverage.*increase/u);
  assert.throws(() => buildToolchainReport({ ...input, tests: input.tests.map((item, index) => index === 0 ? { ...item, assertionCount: 0 } : item) }), /assertion/u);
});

test("matrix report requires every toolchain/framework exactly once", () => {
  const result = buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, blocks: matrixBlocks() });
  assert.equal(result.overallStatus, "passed");
  assert.equal(result.blocks.length, 8);
  assert.deepEqual(result.blocks.map(({ platform, toolchainFamily, framework }) => `${platform}:${toolchainFamily}:${framework}`), [
    "linux:gcc:cpputest", "linux:gcc:unity", "linux:clang:cpputest", "linux:clang:unity",
    "win32:msvc:cpputest", "win32:msvc:unity", "win32:clang-cl:cpputest", "win32:clang-cl:unity",
  ]);
  const missing = matrixBlocks().slice(1);
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, blocks: missing }), /complete|toolchain/u);
  const duplicate = matrixBlocks(); duplicate[1] = { ...duplicate[0] };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, blocks: duplicate }), /duplicate|toolchain/u);
});

test("matrix rejects artifact substitution and paths", () => {
  const blocks = matrixBlocks();
  blocks[1] = { ...blocks[1], outputArtifactSha256: blocks[0].outputArtifactSha256 };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, blocks }), /artifact.*digest|duplicate/u);
  const unsafe = matrixBlocks(); unsafe[0] = { ...unsafe[0], compiler: { ...unsafe[0].compiler, version: "/usr/bin/gcc" } };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, blocks: unsafe }), /path|compiler/u);
});

test("checked-in JSON schema accepts a validated toolchain report", () => {
  const ajv = new Ajv2020({ strict: true });
  const validate = ajv.compile(schema);
  assert.equal(validate(block("linux", "gcc", "cpputest", 0)), true);
});
