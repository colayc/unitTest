import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { decodeCoverageDocumentV1 } from "./decoder.js";

const fixtureURL = new URL("../../coverage-schema/fixtures/v1/report.valid.json", import.meta.url);

const fixtureText = async () => readFile(fixtureURL, "utf8");

const fixture = async () =>
  JSON.parse(await fixtureText());

const invalidFixture = async (name: string) =>
  JSON.parse(await readFile(
    new URL(`../../coverage-schema/fixtures/v1/${name}`, import.meta.url),
    "utf8"
  ));

test("decoder returns a defensive canonical clone", async () => {
  const input = await fixture();
  const decoded = decodeCoverageDocumentV1(input);
  input.files[0].uri = "mutated.cpp";
  input.files[0].summary.lines.covered = 0;
  input.files[0].lines[0].branches.covered = 0;
  assert.equal(decoded.files[0]?.uri, "src/calculator.cpp");
  assert.equal(decoded.files[0]?.summary.lines.covered, 1);
  assert.equal(decoded.files[0]?.lines[0]?.branches.covered, 1);
  assert.notStrictEqual(decoded.files, input.files);
  assert.notStrictEqual(decoded.files[0]?.summary, input.files[0].summary);
});

test("decoder rejects every shared invalid fixture for its target structural rule", async () => {
  const cases = [
    ["report-native-path.invalid.json", /additional properties/],
    ["report-float.invalid.json", /summary\/lines\/covered must be integer/],
    ["report-unsafe-count.invalid.json", /summary\/lines\/total must be <= 9007199254740991/]
  ] as const;
  for (const [name, expected] of cases) {
    const candidate = await invalidFixture(name);
    assert.throws(() => decodeCoverageDocumentV1(candidate), expected);
  }
});

test("decoder accepts integer-valued decimal and exponent JSON number lexemes", async () => {
  const raw = await fixtureText();
  const integerMetric = '"lines": { "covered": 1, "total": 2 }';
  assert.equal(raw.split(integerMetric).length - 1, 2);
  for (const lexeme of ["1.0", "1e0"]) {
    const encoded = raw.replaceAll(
      integerMetric,
      `"lines": { "covered": ${lexeme}, "total": 2 }`
    );
    assert.notEqual(encoded, raw);
    const candidate = JSON.parse(encoded);
    assert.equal(decodeCoverageDocumentV1(candidate).summary.lines.covered, 1);
  }
});

test("decoder enforces UTF-8 byte limits on provenance versions", async () => {
  const cases = [
    ["provenance.compiler.version", (value: any, version: string) => { value.provenance.compiler.version = version; }],
    ["provenance.driver.version", (value: any, version: string) => { value.provenance.driver.version = version; }],
    ["provenance.collector.version", (value: any, version: string) => { value.provenance.collector.version = version; }],
    ["provenance.normalizerVersion", (value: any, version: string) => { value.provenance.normalizerVersion = version; }]
  ] as const;
  for (const [field, setVersion] of cases) {
    const atLimit = await fixture();
    setVersion(atLimit, "é".repeat(64));
    assert.equal(Buffer.byteLength("é".repeat(64), "utf8"), 128);
    assert.doesNotThrow(() => decodeCoverageDocumentV1(atLimit));

    const overLimit = await fixture();
    setVersion(overLimit, "é".repeat(128));
    assert.equal(Buffer.byteLength("é".repeat(128), "utf8"), 256);
    assert.throws(
      () => decodeCoverageDocumentV1(overLimit),
      new RegExp(`invalid Coverage JSON v1: ${field.replaceAll(".", "\\.")} is not 1\\.\\.128 UTF-8 bytes`)
    );
  }
});

test("decoder accepts a 4096-byte URI and rejects a longer multibyte URI", async () => {
  const withinLimit = await fixture();
  withinLimit.files[0].uri = "é".repeat(2046) + ".cpp";
  assert.equal(Buffer.byteLength(withinLimit.files[0].uri, "utf8"), 4096);
  assert.equal(decodeCoverageDocumentV1(withinLimit).files[0]?.uri, withinLimit.files[0].uri);

  const overLimit = await fixture();
  overLimit.files[0].uri = "é".repeat(2047) + ".cpp";
  assert.equal(Buffer.byteLength(overLimit.files[0].uri, "utf8"), 4098);
  assert.throws(
    () => decodeCoverageDocumentV1(overLimit),
    /invalid Coverage JSON v1: file\.uri is not 1\.\.4096 UTF-8 bytes/
  );
});

test("decoder rejects non-well-formed strings before UTF-8 conversion", async () => {
  const candidate = await fixture();
  candidate.files[0].uri = "src/\ud800.cpp";
  assert.throws(
    () => decodeCoverageDocumentV1(candidate),
    /invalid Coverage JSON v1: file\.uri is not a well-formed Unicode string/
  );
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
