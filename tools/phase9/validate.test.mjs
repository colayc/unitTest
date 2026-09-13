import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  encodeCanonicalJson,
  readCanonicalJson,
  writeCanonicalJson,
} from "./canonical-json.mjs";
import schema from "./gates.schema.json" with { type: "json" };

async function fixture(name, bytes) {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  fixtureRoots.push(root);
  const path = join(root, name);
  await writeFile(path, bytes);
  return path;
}
const fixtureRoots = [];
test.afterEach(async () => {
  await Promise.all(fixtureRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

test("canonical JSON recursively sorts object keys and ends with one newline", () => {
  assert.equal(
    encodeCanonicalJson({ z: 1, a: { y: 2, x: 3 }, list: [{ b: 2, a: 1 }] }),
    '{\n  "a": {\n    "x": 3,\n    "y": 2\n  },\n  "list": [\n    {\n      "a": 1,\n      "b": 2\n    }\n  ],\n  "z": 1\n}\n',
  );
});

test("reader rejects duplicate keys through canonical round-trip", async () => {
  const path = await fixture("duplicate.json", '{"gate":"a","gate":"b"}\n');
  await assert.rejects(
    readCanonicalJson(path, { label: "receipt", maxBytes: 1024 }),
    /PHASE9_GATE_SCHEMA_INVALID: receipt is not canonical JSON/u,
  );
});

test("reader rejects invalid UTF-8 and empty input", async () => {
  for (const bytes of [Buffer.from([0xc3, 0x28]), Buffer.alloc(0)]) {
    const path = await fixture("invalid.json", bytes);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID/u,
    );
  }
});

test("reader rejects non-canonical formatting and oversized input", async () => {
  for (const content of ['{"b":1,"a":2}\n', '{ "a": 1 }\n', "{}\n\n"]) {
    const path = await fixture("noncanonical.json", content);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID: input is not canonical JSON/u,
    );
  }
  const path = await fixture("large.json", Buffer.alloc(1025, 0x20));
  await assert.rejects(
    readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
    /PHASE9_GATE_SCHEMA_INVALID: input byte length is invalid/u,
  );
});

test("reader rejects arrays, null, and unsafe top-level primitives", async () => {
  for (const content of ["[]\n", "null\n", "true\n", "1\n", '"text"\n']) {
    const path = await fixture("primitive.json", content);
    await assert.rejects(
      readCanonicalJson(path, { label: "input", maxBytes: 1024 }),
      /PHASE9_GATE_SCHEMA_INVALID: input is not canonical JSON/u,
    );
  }
});

test("writer creates parents and writes canonical UTF-8 bytes", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  const path = join(root, "nested", "output.json");
  await writeCanonicalJson(path, { b: "值", a: 1 });
  assert.deepEqual(await readCanonicalJson(path, { label: "output", maxBytes: 1024 }), { a: 1, b: "值" });
});

test("writer rejects unsafe top-level values", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  try {
    for (const value of [[], null, true, 1, "text", undefined, Number.NaN]) {
      await assert.rejects(writeCanonicalJson(join(root, "unsafe.json"), value), /PHASE9_GATE_SCHEMA_INVALID/u);
    }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("schema contract is closed and contains the required definitions", () => {
  assert.equal(schema.$schema, "https://json-schema.org/draft/2020-12/schema");
  assert.equal(schema.$id, "https://unit-test-ide.invalid/schemas/phase9-gates-v1.json");
  for (const name of ["registry", "baseline", "githubActionsReceipt", "manualApprovalReceipt", "matrix", "runSnapshot", "jobSnapshot", "artifactSnapshot"]) {
    assert.ok(schema.$defs[name]);
  }
  const defs = schema.$defs;
  assert.deepEqual(defs.commit.pattern, "^[0-9a-f]{40}$");
  assert.deepEqual(defs.digest.pattern, "^[0-9a-f]{64}$");
  assert.deepEqual(defs.decimalId.pattern, "^[1-9][0-9]*$");
  assert.deepEqual(defs.status.enum, ["PASS", "MISSING", "FAILED", "DEFERRED"]);
  assert.deepEqual(defs.conclusion.enum, ["success", "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped"]);
  assert.deepEqual(defs.baseline.properties.evaluationMode.enum, ["historical", "candidate"]);
  assert.equal(defs.registry.properties.schemaVersion.const, 1);
  assert.equal(defs.baseline.properties.schemaVersion.const, 1);
  assert.equal(defs.matrix.properties.schemaVersion.const, 1);
  assert.equal(defs.receipt.oneOf.length, 2);
  const visit = (value) => {
    if (!value || typeof value !== "object") return;
    if (value.type === "object") assert.equal(value.additionalProperties, false);
    for (const child of Object.values(value)) visit(child);
  };
  visit(schema);
});
