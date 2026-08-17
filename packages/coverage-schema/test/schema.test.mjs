import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const load = async (relative) =>
  JSON.parse(await readFile(new URL(relative, import.meta.url), "utf8"));

test("Coverage JSON v1 accepts the canonical fixture and rejects structural violations", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validate = ajv.compile(await load("../schema/v1/coverage.schema.json"));
  assert.equal(validate(await load("../fixtures/v1/report.valid.json")), true, JSON.stringify(validate.errors));
  for (const name of [
    "report-native-path.invalid.json",
    "report-float.invalid.json",
    "report-unsafe-count.invalid.json"
  ]) {
    assert.equal(validate(await load("../fixtures/v1/" + name)), false, name + " passed");
  }
});

test("Coverage JSON v1 rejects forbidden operational metadata", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const validate = ajv.compile(await load("../schema/v1/coverage.schema.json"));
  const valid = await load("../fixtures/v1/report.valid.json");
  for (const [field, value] of Object.entries({
    runId: "11111111111111111111111111111111",
    artifactId: "22222222222222222222222222222222",
    timestamp: "2026-08-03T00:00:00Z",
    durationMs: 1,
    percentage: 50,
    command: "llvm-cov",
    environment: ["TOKEN=secret"]
  })) {
    assert.equal(validate({ ...valid, [field]: value }), false, "accepted " + field);
  }
});

test("Coverage JSON v1 invalid fixtures each contain exactly one planned mutation", async () => {
  const valid = await load("../fixtures/v1/report.valid.json");

  const nativePathFixture = await load("../fixtures/v1/report-native-path.invalid.json");
  const { nativePath, ...nativePathRestored } = nativePathFixture;
  assert.equal(nativePath, "C:\\workspace\\src\\calculator.cpp");
  assert.deepEqual(nativePathRestored, valid);

  const floatFixture = await load("../fixtures/v1/report-float.invalid.json");
  assert.equal(floatFixture.summary.lines.covered, 0.5);
  floatFixture.summary.lines.covered = valid.summary.lines.covered;
  assert.deepEqual(floatFixture, valid);

  const unsafeCountFixture = await load("../fixtures/v1/report-unsafe-count.invalid.json");
  assert.equal(unsafeCountFixture.summary.lines.total, 9007199254740992);
  unsafeCountFixture.summary.lines.total = valid.summary.lines.total;
  assert.deepEqual(unsafeCountFixture, valid);
});
