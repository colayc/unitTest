import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { FRAMEWORK_DEPENDENCY_IDS, parseFrameworkManifestBytes, validateFrameworkManifest } from "./manifest.mjs";

const directory = dirname(fileURLToPath(import.meta.url));
const manifestFixture = () => JSON.parse(readFileSync(join(directory, "manifest.json"), "utf8"));
const clone = (value) => JSON.parse(JSON.stringify(value));

function invalidManifestFixtures() {
  const manifest = manifestFixture();
  const wrongId = clone(manifest); wrongId.frameworks[0].id = "CppUTest";
  const wrongLicense = clone(manifest); wrongLicense.frameworks[1].license.sha256 = "0".repeat(64);
  const wrongOrder = clone(manifest); wrongOrder.frameworks.reverse();
  const extraField = clone(manifest); extraField.frameworks[2].unexpected = true;
  const wrongContainer = clone(manifest); wrongContainer.fixtureTools.cmockGenerator.containerImage = "docker.io/evil/ruby";
  const unsafePath = clone(manifest); unsafePath.frameworks[0].license.path = "../COPYING";
  return [wrongId, wrongLicense, wrongOrder, extraField, wrongContainer, unsafePath];
}

test("schema v2 is canonical and locks exactly three dependencies", () => {
  const value = manifestFixture();
  assert.deepEqual(validateFrameworkManifest(value), value);
  assert.deepEqual(FRAMEWORK_DEPENDENCY_IDS, ["cpputest", "unity", "cmock"]);
  assert.deepEqual(parseFrameworkManifestBytes(Buffer.from(`${JSON.stringify(value, null, 2)}\n`)), value);
});

test("schema v2 rejects identity, license, order, case, and extra-field drift", () => {
  for (const value of invalidManifestFixtures()) {
    assert.throws(() => validateFrameworkManifest(value), (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID");
  }
});

test("manifest bytes reject duplicate keys and non-canonical encoding", () => {
  assert.throws(() => parseFrameworkManifestBytes(Buffer.from('{"schemaVersion":2,"schemaVersion":2}\n')), (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID");
  assert.throws(() => parseFrameworkManifestBytes(Buffer.from("{\"schemaVersion\":2}")), (error) => error?.code === "FRAMEWORK_MANIFEST_INVALID");
});
