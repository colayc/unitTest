import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { decodeCoverageDocumentV1 } from "./decoder.js";

const fixture = async () =>
  JSON.parse(await readFile(
    new URL("../../coverage-schema/fixtures/v1/report.valid.json", import.meta.url),
    "utf8"
  ));

test("decoder returns a defensive canonical clone", async () => {
  const input = await fixture();
  const decoded = decodeCoverageDocumentV1(input);
  input.files[0].uri = "mutated.cpp";
  assert.equal(decoded.files[0]?.uri, "src/calculator.cpp");
});

test("decoder rejects cross-field and ordering violations", async () => {
  const valid = await fixture();
  const mutations = [
    (value: any) => { value.summary.lines.covered = 3; },
    (value: any) => { value.files[0].summary.lines.total = 3; },
    (value: any) => { value.files[0].lines.reverse(); },
    (value: any) => { value.files[0].uri = "../outside.cpp"; },
    (value: any) => { value.completeness = { outcome: "available", reasons: ["test_crashed"] }; }
  ];
  for (const mutate of mutations) {
    const candidate = structuredClone(valid);
    mutate(candidate);
    assert.throws(() => decodeCoverageDocumentV1(candidate), /invalid Coverage JSON v1:/);
  }
});
