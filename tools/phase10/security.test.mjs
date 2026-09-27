import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { buildToolchainReport } from "./test-generation-report.mjs";

const digest = (value) => createHash("sha256").update(value).digest("hex");
const valid = {
  schemaVersion: 1, candidateCommit: "d".repeat(40), evidenceKind: "local-static", producerReceiptSha256: null, platform: "linux", toolchainFamily: "clang", framework: "unity",
  compiler: { family: "clang", version: "18.1.8", sha256: digest("compiler") },
  frameworkIdentity: { version: "2.6.1", sha256: digest("framework"), treeSha256: digest("tree") }, cmake: { version: "3.30.0", sha256: digest("cmake") },
  coverage: { baseline: { functions: { covered: 1, total: 3 }, lines: { covered: 1, total: 3 }, branches: { covered: 1, total: 3 } }, final: { functions: { covered: 2, total: 3 }, lines: { covered: 2, total: 3 }, branches: { covered: 2, total: 3 } } },
  candidates: { generated: 1, retained: 1, rejected: 0 }, tests: [{ id: "safe", candidateKind: "verified", assertionCount: 1, sourceArtifactSha256: digest("source"), outputArtifactSha256: digest("output") }],
  faultScenarios: ["cancel-before-build", "compile-failure", "service-restart"].map((id) => ({ id, status: "passed", artifactSha256: digest(id) })), inputArtifactSha256: digest("input"), outputArtifactSha256: digest("result"),
  performance: { durationMs: 1, budgetMs: 2, peakMemoryBytes: 1, memoryBudgetBytes: 2 }, mutation: { total: 1, killed: 1, requiredKilled: 1 },
};

test("report rejects path traversal, absolute paths, and shell-shaped identities", () => {
  for (const compilerVersion of ["../../bin/clang", "C:\\\\Windows\\\\clang.exe", "$(id)", "clang; echo secret"]) {
    assert.throws(() => buildToolchainReport({ ...valid, compiler: { ...valid.compiler, version: compilerVersion } }), /path|invalid|compiler/u);
  }
  assert.throws(() => buildToolchainReport({ ...valid, tests: [{ ...valid.tests[0], id: "../escape" }] }), /generated test IDs|invalid/u);
  assert.throws(() => buildToolchainReport({ ...valid, candidateCommit: "$(git rev-parse HEAD)" }), /candidate commit/u);
});

test("report rejects skipped fault evidence, flood-shaped arrays, and secret-bearing extra fields", () => {
  const skipped = valid.faultScenarios.map((item, index) => index === 0 ? { ...item, status: "skipped" } : item);
  assert.throws(() => buildToolchainReport({ ...valid, faultScenarios: skipped }), /fault scenario/u);
  assert.throws(() => buildToolchainReport({ ...valid, tests: new Array(10001).fill(valid.tests[0]) }), /retained test count|duplicated/u);
  assert.throws(() => buildToolchainReport({ ...valid, secret: "TOKEN=do-not-publish" }), /closed/u);
});
