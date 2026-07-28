import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";

const fixtureDirectory = "apps/test-service/internal/workspace/testdata";

async function loadJSON(path) {
  return JSON.parse(await readFile(path, "utf8"));
}

async function fixture(name) {
  return loadJSON(`${fixtureDirectory}/${name}`);
}

async function compileSchema() {
  const schema = await loadJSON("apps/test-service/internal/workspace/workspace.schema.json");
  return new Ajv2020({ allErrors: true, strict: true }).compile(schema);
}

test("workspace schema accepts supported structured configuration", async () => {
  const validate = await compileSchema();
  assert.equal(validate(await fixture("minimal.valid.json")), true, JSON.stringify(validate.errors));
  assert.equal(validate(await fixture("manual-toolchains.valid.json")), true, JSON.stringify(validate.errors));
});

test("workspace schema rejects shell-shaped fields as additional properties", async () => {
  const validate = await compileSchema();
  assert.equal(validate(await fixture("shell.invalid.json")), false);
  assert.match(JSON.stringify(validate.errors), /additionalProperties/);
});

test("workspace schema rejects unsafe paths, duplicates, limits, and versions", async () => {
  const validate = await compileSchema();
  const minimal = await fixture("minimal.valid.json");
  const clang = {
    id: "linux-clang",
    family: "clang",
    cCompiler: "/usr/bin/clang",
    cppCompiler: "/usr/bin/clang++"
  };

  const invalidConfigurations = [
    { ...minimal, version: 2 },
    { ...minimal, unknown: true },
    {
      version: 1,
      projects: [{ id: "outside", sourceDir: "/outside" }]
    },
    {
      version: 1,
      projects: [{ id: "outside", sourceDir: "C:/outside" }]
    },
    {
      version: 1,
      projects: [minimal.projects[0], structuredClone(minimal.projects[0])]
    },
    {
      version: 1,
      toolchains: [clang, structuredClone(clang)]
    },
    {
      version: 1,
      projects: Array.from({ length: 65 }, (_, index) => ({
        id: `project-${index}`,
        sourceDir: `project-${index}`
      }))
    },
    {
      version: 1,
      toolchains: Array.from({ length: 65 }, (_, index) => ({
        ...clang,
        id: `toolchain-${index}`
      }))
    }
  ];

  for (const configuration of invalidConfigurations) {
    assert.equal(validate(configuration), false, JSON.stringify(configuration));
  }
});

test("workspace schema enforces family-discriminated manual toolchains", async () => {
  const validate = await compileSchema();
  const invalidToolchains = [
    {
      id: "clang-with-msvc-field",
      family: "clang",
      cCompiler: "/usr/bin/clang",
      cppCompiler: "/usr/bin/clang++",
      installationId: "VisualStudio.18.Release"
    },
    {
      id: "msvc-with-compiler-field",
      family: "msvc",
      cCompiler: "C:/LLVM/bin/clang-cl.exe",
      installationId: "VisualStudio.18.Release",
      toolsetVersion: "14.50",
      hostArchitecture: "x64",
      targetArchitecture: "x64"
    }
  ];

  for (const toolchain of invalidToolchains) {
    assert.equal(validate({ version: 1, toolchains: [toolchain] }), false);
    assert.match(JSON.stringify(validate.errors), /additionalProperties|oneOf/);
  }
});

test("workspace schema accepts missing optional fields and rejects explicit null", async () => {
  const validate = await compileSchema();
  const missingOptionalFields = [
    { version: 1, projects: [], toolchains: [] },
    { version: 1, cmake: {}, toolchains: [] },
    { version: 1, cmake: {}, projects: [] },
    { version: 1, cmake: {} },
    { version: 1, projects: [{ id: "root", sourceDir: "." }] },
    {
      version: 1,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { preferredGenerator: "Ninja" }
      }]
    },
    {
      version: 1,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { configurations: ["Debug"] }
      }]
    }
  ];
  for (const configuration of missingOptionalFields) {
    assert.equal(validate(configuration), true, JSON.stringify(validate.errors));
  }

  const nullOptionalFields = [
    { version: 1, cmake: null },
    { version: 1, projects: null },
    { version: 1, toolchains: null },
    { version: 1, cmake: { executable: null } },
    {
      version: 1,
      projects: [{ id: "root", sourceDir: ".", fallback: null }]
    },
    {
      version: 1,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { configurations: null }
      }]
    },
    {
      version: 1,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { preferredGenerator: null }
      }]
    }
  ];
  for (const configuration of nullOptionalFields) {
    assert.equal(validate(configuration), false, JSON.stringify(configuration));
  }
});

test("workspace JSON parsing uses the last repeated object member before schema validation", async () => {
  const validate = await compileSchema();
  const repeatedCMake = JSON.parse(
    `{"version":1,"cmake":{"executable":"C:/Tools/CMake/bin/cmake.exe"},"cmake":{}}`
  );
  assert.deepEqual(repeatedCMake.cmake, {});
  assert.equal(validate(repeatedCMake), true, JSON.stringify(validate.errors));

  const repeatedFallback = JSON.parse(
    `{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"configurations":["Debug"],"preferredGenerator":"Ninja"},"fallback":{}}]}`
  );
  assert.deepEqual(repeatedFallback.projects[0].fallback, {});
  assert.equal(validate(repeatedFallback), true, JSON.stringify(validate.errors));
});
