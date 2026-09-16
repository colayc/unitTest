import assert from "node:assert/strict";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";
import schema from "./p4-report.schema.json" with { type: "json" };

const ajv = new Ajv2020({ allErrors: true, strict: true });
ajv.compile(schema);
const validateScenarioSet = ajv.getSchema(`${schema.$id}#/$defs/scenarioSet`);
const validatePlatformToolchains = ajv.getSchema(`${schema.$id}#/$defs/platformToolchains`);

const scenarioIds = [
  "all", "assertion-failure", "cancel", "crash", "discovery", "failed-rerun",
  "filter", "malformed-output", "mock-failure", "opaque-fallback", "reconnect-replay",
  "repeat", "service-restart", "single", "skip", "stale-catalog", "timeout",
];

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
