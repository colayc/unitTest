import assert from "node:assert/strict";
import { lstat } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import {
  buildLinuxGccCoverageEvidence,
  type LinuxGccCoverageEvidence
} from "./coverage-service-smoke-support.js";

const repositoryRoot = resolve(import.meta.dirname, "../../../..");
const linuxFixtureRoot = join(repositoryRoot, "apps", "code-oss-extension", "test", "fixtures");

/**
 * This is the Linux-only production smoke entrypoint. Task 9 supplies the
 * offline process-tree boundary and locked framework bootstrap; until then a
 * missing prerequisite is an explicit deferred native test, never a Windows
 * substitution or a PASS evidence artifact.
 */
test("Linux GCC production coverage smoke has a sealed Unix-socket fixture contract", {
  skip: process.platform !== "linux" ? "Linux GCC smoke runs only on Linux" : false
}, async (t) => {
  const bundleRoot = process.env.UNIT_TEST_IDE_TEST_COVERAGE_BUNDLE_ROOT;
  if (bundleRoot === undefined || !isExactAbsolute(bundleRoot)) {
    t.skip("DEFERRED: Task 9 must provide an explicit exact test-only coverage bundle seam");
    return;
  }
  for (const fixture of ["coverage", "coverage-unity"]) {
    try {
      await lstat(join(linuxFixtureRoot, fixture));
    } catch {
      assert.fail(`required Linux framework fixture is unavailable: ${fixture}`);
    }
  }
  // Task 9 replaces this deferred checkpoint with the offline Service process
  // tree. Keep the expected evidence shape exercised here so no Linux result
  // can be emitted without both framework cases and deterministic artifacts.
  const expected = buildLinuxGccCoverageEvidence(expectedEvidence());
  assert.equal(expected.platform, "linux-x64");
  t.skip("DEFERRED: Task 9 offline boundary and locked CppUTest/Unity bootstrap are required before native execution");
});

function isExactAbsolute(value: string): boolean {
  return value.startsWith("/") && !value.includes("\0") && value === resolve(value);
}

function expectedEvidence(): LinuxGccCoverageEvidence {
  return {
    schemaVersion: 1,
    platform: "linux-x64",
    toolchain: { family: "gcc", digest: "a".repeat(64) },
    bundleDigest: "b".repeat(64),
    cases: [
      { framework: "cpputest", testRunOutcome: "failed", coverageRunOutcome: "available", reportOutcome: "available", summary: { lines: { covered: 2, total: 3 }, branches: { covered: 1, total: 2 }, functions: { covered: 1, total: 1 } }, artifactDigest: "c".repeat(64) },
      { framework: "unity", testRunOutcome: "passed", coverageRunOutcome: "available", reportOutcome: "available", summary: { lines: { covered: 3, total: 3 }, branches: { covered: 2, total: 2 }, functions: { covered: 1, total: 1 } }, artifactDigest: "d".repeat(64) }
    ],
    determinism: { coverageJsonByteIdentical: true, sha256Identical: true },
    startedAt: "2026-09-07T00:00:00.000Z",
    finishedAt: "2026-09-07T00:00:01.000Z"
  };
}
