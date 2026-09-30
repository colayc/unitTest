import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";

const source = new URL("./", import.meta.url);
const archive = {
  filename: "LLVM-22.1.8-Linux-X64.tar.xz",
  url: "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/LLVM-22.1.8-Linux-X64.tar.xz",
  sha256: "df0e1ecf16caf3489a272a5eea4eec9b0d82878f6477fa309504f918a0006384",
};

async function readJson(name) {
  return JSON.parse(await readFile(new URL(name, source), "utf8"));
}

test("Linux LLVM coverage manifest locks the reviewed archive and four tools", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  const validate = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
  assert.equal(validate(manifest), true, JSON.stringify(validate.errors));
  assert.equal(manifest.schemaVersion, 1);
  assert.equal(manifest.llvmVersion, "22.1.8");
  assert.equal(manifest.sourceCommit, "ca7933e47d3a3451d81e72ac174dcb5aa28b59d1");
  assert.equal(manifest.platform, "linux-x64");
  assert.deepEqual(manifest.archive, archive);
  assert.equal(manifest.archiveRoot, "LLVM-22.1.8-Linux-X64");
  assert.deepEqual(manifest.tools, {
    clang: "bin/clang",
    clangxx: "bin/clang++",
    llvmProfdata: "bin/llvm-profdata",
    llvmCov: "bin/llvm-cov",
  });
  assert.deepEqual(manifest.license, {
    path: "licenses/llvm/LICENSE.TXT",
    url: "https://raw.githubusercontent.com/llvm/llvm-project/ca7933e47d3a3451d81e72ac174dcb5aa28b59d1/LICENSE.TXT",
    sha256: "8d85c1057d742e597985c7d4e6320b015a9139385cff4cbae06ffc0ebe89afee",
    size: 15141,
  });
});

test("Linux LLVM manifest rejects mutable sources, unsafe paths, or incomplete tools", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  const validate = new Ajv2020({ allErrors: true, strict: true }).compile(schema);
  const mutable = structuredClone(manifest);
  mutable.archive.url = "https://github.com/llvm/llvm-project/releases/latest/download/LLVM.tar.xz";
  assert.equal(validate(mutable), false);
  const unsafe = structuredClone(manifest);
  unsafe.tools.clang = "../clang";
  assert.equal(validate(unsafe), false);
  const incomplete = structuredClone(manifest);
  delete incomplete.tools.llvmCov;
  assert.equal(validate(incomplete), false);
});
