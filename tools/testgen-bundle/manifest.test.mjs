import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";

const source = new URL("./", import.meta.url);
const expectedPlatforms = ["linux-x64", "windows-x64"];
const officialArchiveDigests = {
  "linux-x64": "df0e1ecf16caf3489a272a5eea4eec9b0d82878f6477fa309504f918a0006384",
  "windows-x64": "d96c2cc1736f4eb7fa43cb9bbdf56d93551a9ae0a9aadb9c99c3c3b2b712a234",
};

async function readJson(name) {
  return JSON.parse(await readFile(new URL(name, source), "utf8"));
}

test("tracked Clang manifest is schema-valid and locks the two official LLVM 22.1.8 archives", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  const validate = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
  assert.equal(validate(manifest), true, JSON.stringify(validate.errors));
  assert.equal(manifest.clangVersion, "22.1.8");
  assert.deepEqual(Object.keys(manifest.platforms).sort(), expectedPlatforms);
  for (const key of expectedPlatforms) {
    const platform = manifest.platforms[key];
    assert.equal(platform.archive.sha256, officialArchiveDigests[key]);
    assert.match(platform.archive.url, /^https:\/\/github\.com\/llvm\/llvm-project\/releases\/download\/llvmorg-22\.1\.8\/[^?#]+$/u);
    assert.ok(platform.files.length > 0, `${key} must have an exact file inventory`);
    assert.ok(platform.licenses.length > 0, `${key} must have an upstream license inventory`);
    assert.ok(platform.files.some(({ path }) => path === platform.executable), `${key} executable must be inventoried`);
    assert.ok(platform.files.some(({ path }) => path.startsWith(`${platform.resourceDir}/`)), `${key} resource directory must be inventoried`);
    const paths = platform.files.map(({ path }) => path);
    assert.equal(new Set(paths).size, paths.length, `${key} duplicate file paths`);
    for (const file of platform.files) {
      assert.match(file.sha256, /^[0-9a-f]{64}$/u);
      assert.doesNotMatch(file.path, /(?:^|\/)\.\.(?:\/|$)|^[A-Za-z]:|^\//u);
    }
    for (const license of platform.licenses) {
      assert.match(license.sha256, /^[0-9a-f]{64}$/u);
      assert.equal(new URL(license.url).pathname.includes(manifest.sourceCommit), true);
      assert.ok(license.path.startsWith("licenses/"), `${key} license is outside licenses/`);
    }
  }
});

test("schema rejects a mutable release URL and a missing license inventory", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  const validate = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
  const mutable = structuredClone(manifest);
  mutable.platforms["windows-x64"].archive.url = "https://github.com/llvm/llvm-project/releases/latest/download/clang.tar.xz";
  assert.equal(validate(mutable), false);
  const unlicensed = structuredClone(manifest);
  unlicensed.platforms["linux-x64"].licenses = [];
  assert.equal(validate(unlicensed), false);
});

test("foundation packaging prepares, checks, and stages the matching offline Clang bundle", async () => {
  const workflow = await readFile(new URL("../../.github/workflows/foundation.yml", source), "utf8");
  for (const [platform, nextJob] of [["windows", "package-linux"], ["linux", "install-smoke-windows"]]) {
    const section = workflow.split(`  package-${platform}:\n`)[1]?.split(`  ${nextJob}:\n`)[0];
    assert.ok(section, `${platform} package job is missing`);
    const prepare = section.indexOf("pnpm prepare:testgen-bundle");
    const check = section.indexOf("pnpm check:testgen-bundle");
    const stage = section.indexOf("node tools/release/stage.mjs");
    assert.ok(prepare >= 0 && check > prepare && stage > check, `${platform} must verify Clang before staging`);
    const root = `.superpowers/cache/testgen-bundle/22.1.8/${platform}-x64`;
    assert.equal(section.split(`--testgen-root ${platform === "windows" ? "'" : ""}${root}${platform === "windows" ? "'" : ""}`).length - 1, 2);
  }
});
