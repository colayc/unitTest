import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

import {
  createTestGenerationScenarioPlan,
  runNativeTestGenerationScenarioPlan,
  runTestGenerationScenarioPlan,
  TEST_GENERATION_SCENARIO_IDS,
} from "./test-generation-e2e.js";

const fixtureRoot = join(import.meta.dirname, "../fixtures/test-generation");

test("scenario plan contains every required scope, oracle, fault, and recovery case", () => {
  const plan = createTestGenerationScenarioPlan();
  assert.deepEqual(plan, TEST_GENERATION_SCENARIO_IDS);
  assert.equal(new Set(plan).size, plan.length);
  for (const required of ["function-scope", "class-scope", "file-scope", "target-scope", "workspace-scope", "coverage-gap-scope", "verified-oracle", "characterization-oracle", "no-safe-target", "unsatisfied-branch", "compile-failure", "suite-regression", "no-coverage-delta", "service-restart", "stale-snapshot", "acceptance-conflict", "repeat-acceptance", "one-click-refresh"]) {
    assert.equal(plan.includes(required as typeof plan[number]), true, required);
  }
  assert.equal(plan.filter((id) => id.startsWith("cancel-")).length, 6);
});

test("scenario executor is ordered and cannot silently omit a case", async () => {
  const result = await runTestGenerationScenarioPlan(async (id) => ({ id, status: "passed", mode: "offline-static" }));
  assert.equal(result.length, TEST_GENERATION_SCENARIO_IDS.length);
  assert.deepEqual(result.map(({ id }) => id), TEST_GENERATION_SCENARIO_IDS);
  await assert.rejects(runTestGenerationScenarioPlan(async (id) => ({ id: id === "function-scope" ? "class-scope" : id, status: "passed", mode: "offline-static" })), /out of order/u);
});

test("native provider remains an explicit external gate", async () => {
  await assert.rejects(runNativeTestGenerationScenarioPlan(), /external Task 15 gate/u);
});

test("committed CppUTest and Unity fixtures are deterministic and do not use mock libraries", async () => {
  for (const [framework, files] of Object.entries({
    cpputest: ["CMakeLists.txt", "unit-test-ide.json", "include/classifier.hpp", "src/classifier.cpp", "tests/classifier_test.cpp"],
    unity: ["CMakeLists.txt", "unit-test-ide.json", "include/classifier.h", "src/classifier.c", "tests/classifier_test.c"],
  })) {
    const contents = await Promise.all(files.map((file) => readFile(join(fixtureRoot, framework, file), "utf8")));
    assert.doesNotMatch(contents.join("\n"), /CppUMock|CMock|Mock|Stub/u);
    assert.doesNotMatch(contents.join("\n"), /[A-Za-z]:\\|(?:^|\s)\//u);
    const config = JSON.parse(contents[1]!);
    assert.equal(config.projects[0].tests.containers[0].framework, framework);
  }
});
