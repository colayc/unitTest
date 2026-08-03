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
  assert.equal(validate(await fixture("tests-v2.valid.json")), true, JSON.stringify(validate.errors));
});

test("workspace schema rejects shell-shaped fields as additional properties", async () => {
  const validate = await compileSchema();
  assert.equal(validate(await fixture("shell.invalid.json")), false);
  assert.match(JSON.stringify(validate.errors), /additionalProperties/);
  assert.equal(validate(await fixture("tests-command.invalid.json")), false);
  assert.match(JSON.stringify(validate.errors), /additionalProperties|oneOf/);
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
    { ...minimal, version: 4 },
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

  assert.equal(validate(await fixture("tests-duplicate.invalid.json")), false);
  for (const [field, value] of Object.entries({
    args: ["--run", "all"],
    environment: { TOKEN: "secret" },
    executable: "C:/unsafe.exe",
    glob: "*",
    hook: "before",
    shell: true,
    workingDirectory: "C:/outside"
  })) {
    assert.equal(validate({
      version: 2,
      projects: [{
        id: "root",
        sourceDir: ".",
        tests: {
          containers: [{
            ctestName: "tests",
            framework: "cpputest",
            [field]: value
          }]
        }
      }]
    }), false, field);
  }
  assert.equal(validate({
    version: 1,
    projects: [{
      id: "root",
      sourceDir: ".",
      tests: { containers: [] }
    }]
  }), false);
  assert.equal(validate({
    version: 2,
    projects: [{
      id: "root",
      sourceDir: ".",
      tests: {
        containers: [{
          ctestPattern: ".*",
          framework: "cpputest"
        }]
      }
    }]
  }), false);
  assert.equal(validate({
    version: 2,
    projects: [{
      id: "root",
      sourceDir: ".",
      tests: {
        containers: [{
          ctestName: "tests",
          framework: "gtest"
        }]
      }
    }]
  }), false);
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
    },
    {
      version: 2,
      projects: [{ id: "root", sourceDir: "." }]
    },
    {
      version: 2,
      projects: [{
        id: "root",
        sourceDir: ".",
        tests: {}
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
    },
    {
      version: 2,
      projects: [{ id: "root", sourceDir: ".", tests: null }]
    },
    {
      version: 2,
      projects: [{
        id: "root",
        sourceDir: ".",
        tests: { containers: null }
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

test("workspace schema accepts closed v3 coverage profiles without widening v1 or v2", async () => {
  const validate = await compileSchema();
  assert.equal(validate(await fixture("coverage-v3.valid.json")), true, JSON.stringify(validate.errors));

  for (const version of [1, 2]) {
    assert.equal(validate({
      version,
      projects: [{ id: "app", sourceDir: "." }],
      coverageProfiles: [{ id: "coverage", baseBuildProfileId: "debug" }]
    }), false, `version ${version} accepted coverageProfiles`);
  }
});

test("workspace v3 schema rejects coverage injection, unsafe path, duplicates, and structural limits", async () => {
  const validate = await compileSchema();
  for (const name of [
    "coverage-command.invalid.json",
    "coverage-path.invalid.json",
    "coverage-duplicate.invalid.json"
  ]) {
    assert.equal(validate(await fixture(name)), false, name);
  }

  const base = {
    version: 3,
    projects: [{ id: "app", sourceDir: "." }],
    coverageProfiles: [{ id: "coverage", baseBuildProfileId: "debug", include: ["src/**"] }]
  };
  const forbidden = [
    "flags", "compilerArgs", "environment", "gcovrConfig", "script",
    "plugin", "driver", "threshold"
  ];
  for (const field of forbidden) {
    const value = structuredClone(base);
    value.coverageProfiles[0][field] = field === "environment" ? { TOKEN: "secret" } : "unsafe";
    assert.equal(validate(value), false, field);
  }

  const emptyExclude = structuredClone(base);
  emptyExclude.coverageProfiles[0].exclude = [];
  assert.equal(validate(emptyExclude), true, JSON.stringify(validate.errors));
  const emptyInclude = structuredClone(base);
  emptyInclude.coverageProfiles[0].include = [];
  assert.equal(validate(emptyInclude), false, "empty include");

  assert.equal(validate({
    ...base,
    coverageProfiles: Array.from({ length: 65 }, (_, index) => ({
      id: `coverage-${index}`,
      baseBuildProfileId: "debug"
    }))
  }), false, "65 profiles");
  assert.equal(validate({
    ...base,
    coverageProfiles: [{
      id: "coverage",
      baseBuildProfileId: "debug",
      include: Array.from({ length: 129 }, (_, index) => `src/file-${index}.cpp`)
    }]
  }), false, "129 includes");
});

test("workspace v3 schema accepts exact coarse coverage maxima", async () => {
  const validate = await compileSchema();
  const base = {
    version: 3,
    projects: [{ id: "app", sourceDir: "." }],
    coverageProfiles: [{ id: "coverage", baseBuildProfileId: "debug", include: ["src/**"] }]
  };
  const profilesAtMaximum = {
    ...base,
    coverageProfiles: Array.from({ length: 64 }, (_, index) => ({
      id: `coverage-${index}`,
      baseBuildProfileId: "debug"
    }))
  };
  assert.equal(validate(profilesAtMaximum), true, JSON.stringify(validate.errors));

  const listsAtMaximum = structuredClone(base);
  listsAtMaximum.coverageProfiles[0].include = Array.from(
    { length: 128 },
    (_, index) => `include-${index}`
  );
  listsAtMaximum.coverageProfiles[0].exclude = Array.from(
    { length: 128 },
    (_, index) => `exclude-${index}`
  );
  assert.equal(validate(listsAtMaximum), true, JSON.stringify(validate.errors));

  const unicodeAtMaximum = structuredClone(base);
  unicodeAtMaximum.coverageProfiles[0].include = ["界".repeat(512)];
  assert.equal(validate(unicodeAtMaximum), true, JSON.stringify(validate.errors));
});
