import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

import {
  createTestGenerationScenarioPlan,
  executeNativeManagedGeneration,
  runNativeTestGenerationScenarioPlan,
  runTestGenerationScenarioPlan,
  TEST_GENERATION_SCENARIO_IDS,
} from "./test-generation-e2e.js";

const fixtureRoot = join(import.meta.dirname, "../fixtures/test-generation");
const digest = (value: string) => value.repeat(64).slice(0, 64);
const workspaceGeneration = digest("1");
const coverageReportId = digest("2");
const refreshedCoverageReportId = digest("d");
const fileId = digest("3");
const functionId = digest("4");
const runId = digest("5");
const reviewDigest = digest("6");
const summary = {
  functions: { covered: 1, total: 2, coveredDelta: 0 },
  lines: { covered: 2, total: 4, coveredDelta: 0 },
  branches: { covered: 1, total: 3, coveredDelta: 0 },
};
const refreshedSummary = {
  functions: { covered: 2, total: 2, coveredDelta: 1 },
  lines: { covered: 3, total: 4, coveredDelta: 1 },
  branches: { covered: 2, total: 3, coveredDelta: 1 },
};

test("scenario plan contains every required scope, oracle, fault, and recovery case", () => {
  const plan = createTestGenerationScenarioPlan();
  assert.deepEqual(plan, TEST_GENERATION_SCENARIO_IDS);
  assert.equal(new Set(plan).size, plan.length);
  for (const required of ["function-scope", "class-scope", "file-scope", "target-scope", "workspace-scope", "coverage-gap-scope", "verified-oracle", "characterization-oracle", "no-safe-target", "unsatisfied-branch", "compile-failure", "suite-regression", "no-coverage-delta", "service-restart", "stale-snapshot", "acceptance-conflict", "repeat-acceptance", "one-click-refresh"]) {
    assert.equal(plan.includes(required as typeof plan[number]), true, required);
  }
  assert.equal(plan.filter((id) => id.startsWith("cancel-")).length, 6);
  for (const required of [
    "managed-rerun-byte-stable", "managed-unmanaged-bytes-preserved", "managed-edited-conflict-zero-write",
    "managed-accepted-update-idempotent", "coverage-project-file-function-totals",
    "coverage-navigation-lines", "coverage-no-metric-regression",
  ]) assert.equal(plan.includes(required as typeof plan[number]), true, required);
});

test("scenario executor is ordered and cannot silently omit a case", async () => {
  const result = await runTestGenerationScenarioPlan(async (id) => ({ id, status: "passed", mode: "offline-static" }));
  assert.equal(result.length, TEST_GENERATION_SCENARIO_IDS.length);
  assert.deepEqual(result.map(({ id }) => id), TEST_GENERATION_SCENARIO_IDS);
  await assert.rejects(runTestGenerationScenarioPlan(async (id) => ({ id: id === "function-scope" ? "class-scope" : id, status: "passed", mode: "offline-static" })), /out of order/u);
});

test("native provider executes every required scenario and rejects non-native or incomplete results", async () => {
  const visited: string[] = [];
  const results = await runNativeTestGenerationScenarioPlan(async (id) => {
    visited.push(id);
    return { id, status: "passed", mode: "native" };
  });
  assert.deepEqual(visited, TEST_GENERATION_SCENARIO_IDS);
  assert.equal(results.length, TEST_GENERATION_SCENARIO_IDS.length);

  await assert.rejects(
    runNativeTestGenerationScenarioPlan(async (id) => ({ id, status: "passed", mode: "offline-static" })),
    /native test-generation scenario did not pass natively/u,
  );
  await assert.rejects(
    runNativeTestGenerationScenarioPlan(async (id) => ({ id, status: id === "function-scope" ? "failed" : "passed", mode: "native" })),
    /native test-generation scenario did not pass natively/u,
  );
});

test("native managed executor uses only report-bound IDs and closes review publication", async () => {
  const calls: string[] = [];
  const client = {
    async getCapabilities() {
      calls.push("capabilities");
      return { coverageDetails: true, testGeneration: true, managedTests: true };
    },
    async getCoverageProject(input: unknown) {
      const reportId = (input as { coverageReportId: string }).coverageReportId;
      calls.push(reportId === coverageReportId ? "project" : "project:refreshed");
      assert.deepEqual(input, { workspaceGeneration, coverageReportId: reportId, projectId: "classifier" });
      return { workspaceGeneration, coverageReportId: reportId, projectId: "classifier", fileCount: 1, status: "current", reasons: [], summary: reportId === coverageReportId ? summary : refreshedSummary };
    },
    async listCoverageFiles(input: unknown) {
      const reportId = (input as { coverageReportId: string }).coverageReportId;
      calls.push(reportId === coverageReportId ? "files" : "files:refreshed");
      assert.deepEqual(input, { workspaceGeneration, coverageReportId: reportId, projectId: "classifier", limit: 200 });
      return { workspaceGeneration, coverageReportId: reportId, items: [{ fileId, relativePath: "src/classifier.c", sourceSha256: digest("7"), status: "current", reasons: [], functionCount: 1, summary: reportId === coverageReportId ? summary : refreshedSummary }] };
    },
    async listCoverageFunctions(input: unknown) {
      const reportId = (input as { coverageReportId: string }).coverageReportId;
      calls.push(reportId === coverageReportId ? "functions" : "functions:refreshed");
      assert.deepEqual(input, { workspaceGeneration, coverageReportId: reportId, fileId, limit: 200 });
      return { workspaceGeneration, coverageReportId: reportId, items: [{ functionId, fileId, qualifiedName: "classify", startLine: 1, endLine: 5, status: "current", reasons: [], summary: reportId === coverageReportId ? summary : refreshedSummary }] };
    },
    async startTestGeneration(input: Record<string, unknown>) {
      calls.push("start");
      assert.equal(input.scope, "symbol");
      assert.equal(input.framework, "unity");
      assert.equal(input.functionId, functionId);
      assert.equal(input.coverageReportId, coverageReportId);
      for (const forbidden of ["path", "command", "executable", "argv", "environment"]) assert.equal(forbidden in input, false);
      return { runId, taskId: digest("8"), projectId: "classifier", workspaceGeneration, state: "queued", createdAt: new Date(), lastSequence: 0 };
    },
    async getTestGenerationRun(id: string) {
      calls.push("poll");
      assert.equal(id, runId);
      return { runId, taskId: digest("8"), projectId: "classifier", workspaceGeneration, state: "awaiting_confirmation", createdAt: new Date(), lastSequence: 1 };
    },
    async getManagedReview(input: unknown) {
      calls.push("review");
      assert.deepEqual(input, { reviewId: runId, limit: 200 });
      return { reviewId: runId, reviewDigest, workspaceGeneration, coverageReportId, cases: [{ caseId: "utc_case_1", status: "current", acceptedDigest: digest("9"), currentDigest: digest("a"), generatedDigest: digest("b") }] };
    },
    async applyManagedReview(input: { reviewId: string; reviewDigest: string; resolutions: Array<{ caseId: string; choice: string }> }) {
      calls.push("apply");
      assert.deepEqual(input, { reviewId: runId, reviewDigest, resolutions: [{ caseId: "utc_case_1", choice: "use-generated" }] });
      return { reviewId: runId, reviewDigest, applied: true };
    },
    async listManagedTests(input: unknown) {
      calls.push("records");
      assert.deepEqual(input, { workspaceGeneration, coverageReportId: refreshedCoverageReportId, projectId: "classifier", fileId, status: "current", limit: 200 });
      return { workspaceGeneration, coverageReportId: refreshedCoverageReportId, items: [{ caseId: "utc_case_1", fileId, functionId, status: "current", acceptedDigest: digest("c"), currentDigest: digest("c") }] };
    },
  };

  const receipt = await executeNativeManagedGeneration({
    client: client as never,
    projectId: "classifier",
    workspaceGeneration,
    coverageReportId,
    fileId,
    functionId,
    framework: "unity",
    pollIntervalMs: 0,
    refreshCoverage: async () => {
      calls.push("refresh");
      return { workspaceGeneration, coverageReportId: refreshedCoverageReportId, fileId, functionId };
    },
  });
  assert.deepEqual(calls, [
    "capabilities", "project", "files", "functions", "start", "poll", "review", "apply", "refresh",
    "project:refreshed", "files:refreshed", "functions:refreshed", "records",
  ]);
  assert.equal(receipt.caseCount, 1);
  assert.equal(receipt.managedRecordCount, 1);
  assert.deepEqual(receipt.baseline, summary);
  assert.deepEqual(receipt.final, refreshedSummary);
  assert.deepEqual(receipt.fileBaseline, summary);
  assert.deepEqual(receipt.fileFinal, refreshedSummary);
  assert.deepEqual(receipt.functionBaseline, summary);
  assert.deepEqual(receipt.functionFinal, refreshedSummary);
});

test("committed CppUTest and Unity fixtures are deterministic and do not use mock libraries", async () => {
  for (const [framework, files] of Object.entries({
    cpputest: ["CMakeLists.txt", ".unit-test-ide/workspace.json", "include/classifier.hpp", "src/classifier.cpp", "tests/CMakeLists.txt", "tests/classifier_test.cpp"],
    unity: ["CMakeLists.txt", ".unit-test-ide/workspace.json", "include/classifier.h", "src/classifier.c", "tests/CMakeLists.txt", "tests/classifier_test.c"],
  })) {
    const contents = await Promise.all(files.map((file) => readFile(join(fixtureRoot, framework, file), "utf8")));
    assert.doesNotMatch(contents.join("\n"), /mock|stub/iu);
    assert.doesNotMatch(contents.join("\n"), /[A-Za-z]:\\|(?:^|\s)\//u);
    const config = JSON.parse(contents[1]!);
    assert.equal(config.projects[0].tests.containers[0].framework, framework);
    assert.match(contents[2]!, /int classify\(int value\)/u);
    assert.match(contents[3]!, /return -1[\s\S]*return 0[\s\S]*return 1/u);
    assert.doesNotMatch(contents[2]! + contents[3]!, /enum/iu);
    assert.match(contents[0]!, /add_subdirectory\(tests\)/u);
    const testsCMake = contents[4]!;
    assert.match(testsCMake, /add_executable\(classifier-tests classifier_test\.(?:c|cpp)\)/u);
    assert.match(
      testsCMake,
      framework === "cpputest"
        ? /target_link_libraries\(classifier-tests PRIVATE classifier CppUTest CppUTestExt\)/u
        : /add_library\(unity STATIC/u,
    );
    const existingTests = contents[5]!;
    assert.match(existingTests, /positive/iu);
    assert.doesNotMatch(existingTests, /negative|zero/iu);
  }
});
