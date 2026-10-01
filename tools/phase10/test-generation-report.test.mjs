import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import schema from "./test-generation-report.schema.json" with { type: "json" };

import { buildManagedCoverageLocalReport, buildMatrixReport, buildToolchainReport } from "./test-generation-report.mjs";

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
    evidenceKind: "local-static",
    producerReceiptSha256: null,
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
  const result = buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", blocks: matrixBlocks() });
  assert.equal(result.overallStatus, "rejected");
  assert.equal(result.blocks.length, 8);
  assert.deepEqual(result.blocks.map(({ platform, toolchainFamily, framework }) => `${platform}:${toolchainFamily}:${framework}`), [
    "linux:gcc:cpputest", "linux:gcc:unity", "linux:clang:cpputest", "linux:clang:unity",
    "win32:msvc:cpputest", "win32:msvc:unity", "win32:clang-cl:cpputest", "win32:clang-cl:unity",
  ]);
  const missing = matrixBlocks().slice(1);
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", blocks: missing }), /complete|toolchain/u);
  const duplicate = matrixBlocks(); duplicate[1] = { ...duplicate[0] };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", blocks: duplicate }), /duplicate|toolchain/u);
});

test("matrix rejects artifact substitution and paths", () => {
  const blocks = matrixBlocks();
  blocks[1] = { ...blocks[1], outputArtifactSha256: blocks[0].outputArtifactSha256 };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", blocks }), /artifact.*digest|duplicate/u);
  const unsafe = matrixBlocks(); unsafe[0] = { ...unsafe[0], compiler: { ...unsafe[0].compiler, version: "/usr/bin/gcc" } };
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "local-static", blocks: unsafe }), /path|compiler/u);
});

test("checked-in JSON schema accepts a validated toolchain report", () => {
  const ajv = new Ajv2020({ strict: true });
  const validate = ajv.compile(schema);
  assert.equal(validate(block("linux", "gcc", "cpputest", 0)), true);
});

test("local-static evidence is rejected as native evidence and coverage may not regress", () => {
  const input = block("linux", "gcc", "cpputest", 0);
  assert.throws(() => buildToolchainReport({ ...input, evidenceKind: "external-native-receipt", producerReceiptSha256: null }), /external native producer receipt/u);
  const regressed = { ...input, coverage: { ...input.coverage, final: { ...input.coverage.final, branches: { covered: 0, total: 30 } } } };
  assert.throws(() => buildToolchainReport(regressed), /coverage metrics cannot regress/u);
  assert.throws(() => buildMatrixReport({ schemaVersion: 1, candidateCommit: commit, evidenceKind: "external-native-receipt", blocks: [input] }), /external producer receipt/u);
});

test("managed coverage local report names all eight native blocks without claiming a pass", () => {
  const report = buildManagedCoverageLocalReport({ candidateCommit: commit });
  assert.equal(report.schemaVersion, 2);
  assert.equal(report.candidateCommit, commit);
  assert.equal(report.releaseReady, false);
  assert.equal(report.evidenceKind, "local-missing-hosted");
  assert.deepEqual(report.deferredReleaseBlockers, ["windows-signing", "third-party-license-legal"]);
  assert.deepEqual(report.blocks.map((block) => block.id), [
    "windows-msvc-cpputest", "windows-msvc-unity", "windows-clangcl-cpputest", "windows-clangcl-unity",
    "linux-gcc-cpputest", "linux-gcc-unity", "linux-clang-cpputest", "linux-clang-unity",
  ]);
  for (const block of report.blocks) {
    assert.equal(block.status, "MISSING");
    assert.deepEqual(Object.values(block.evidence), Array(16).fill(null));
  }
  const ajv = new Ajv2020({ strict: true });
  ajv.addSchema(schema);
  const validate = ajv.compile({ $ref: `${schema.$id}#/$defs/managedCoverageLocalReport` });
  assert.equal(validate(report), true, JSON.stringify(validate.errors));
});

test("managed coverage local report rejects candidate substitution and invented hosted results", () => {
  assert.throws(() => buildManagedCoverageLocalReport({ candidateCommit: "not-a-sha" }), /candidate commit/u);
  assert.throws(() => buildManagedCoverageLocalReport({ candidateCommit: commit, blocks: [] }), /closed/u);
  assert.throws(() => buildManagedCoverageLocalReport({ candidateCommit: commit, releaseReady: true }), /closed/u);
});

test("local managed report CLI writes an immutable missing-evidence file", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase10-managed-gate-"));
  const output = join(root, "report.json");
  try {
    execFileSync(process.execPath, ["tools/phase10/test-generation-report.mjs", "--local-managed", "--candidate", commit, "--out", output]);
    const report = JSON.parse(await readFile(output, "utf8"));
    assert.equal(report.releaseReady, false);
    assert.equal(report.blocks.length, 8);
    assert.throws(() => execFileSync(process.execPath, ["tools/phase10/test-generation-report.mjs", "--local-managed", "--candidate", commit, "--out", output], { stdio: "ignore" }));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
