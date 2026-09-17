import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, lstat, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import {
  hashCompiledFrameworkExecutable,
  stageFrameworkWorkspace,
  validateOwnedFrameworkStage,
  type FrameworkWorkspaceStageOptions,
} from "./native-framework-workspace.js";

const MATRIX_FILES = [
  "CMakeLists.txt", "contract.json", "malformed_cpputest.cpp", "malformed_unity.c", "opaque.c",
] as const;
const FIXTURE_FILES = {
  cpputest: [".unit-test-ide/workspace.json", "CMakeLists.txt", "fixture.json", "tests/framework_tests.cpp"],
  unity: [
    ".unit-test-ide/workspace.json", "CMakeLists.txt", "cmock.yml", "fixture.json", "include/Dependency.h",
    "mocks/MockDependency.c", "mocks/MockDependency.h", "mocks/cmock-generation.json",
    "tests/fixture_runtime.c", "tests/framework_tests.c",
  ],
} as const;

test("actual staged Unity configure keeps F1 and malformed sources inside the CMake source root", async (t) => {
  const input = await workspaceFixture(t);
  const repository = resolve(import.meta.dirname, "../../..");
  for (const path of ["testdata/frameworks", "testdata/framework-matrix", "sdk/cmake"]) {
    await cp(join(repository, path), join(input.repositoryRoot, path), { recursive: true });
  }
  // Configuration resolves these dependency source files; compilation is not needed here.
  for (const name of ["unity", "cmock"]) await write(input.repositoryRoot, `.prepared/${name}/src/${name}.c`, "/* configure fixture */\n");
  const execute = (command: string, args: string[]) => {
    const result = spawnSync(command, args, { cwd: repository, encoding: "utf8", windowsHide: true, timeout: 120_000 });
    assert.equal(result.status, 0, result.error?.message ?? `${result.stdout}\n${result.stderr}`);
    return result.stdout;
  };
  const generator = join(input.repositoryRoot, process.platform === "win32" ? "generator.exe" : "generator");
  execute("go", ["build", "-o", generator, "./apps/test-service/cmd/unity-runner-generator"]);
  const platform = process.platform === "win32" ? "win32" : "linux";
  const family = platform === "win32" ? "msvc" : "gcc";
  const stage = await stageFrameworkWorkspace({
    ...input.options, platform, family, frameworkId: "unity", unityRunnerGenerator: generator,
    stageRoot: join(input.repositoryRoot, ".native-e2e/framework-work/.staging", input.options.ownershipId, platform === "win32" ? "windows" : "linux", family, "unity"),
  });
  const workspace = JSON.parse(await readFile(join(stage.workspaceRoot, ".unit-test-ide/workspace.json"), "utf8"));
  const build = join(input.repositoryRoot, "configure-test");
  execute(process.env.CMAKE ?? "cmake", ["-S", join(stage.workspaceRoot, workspace.projects[0].sourceDir), "-B", build,
    "-DUNIT_TEST_IDE_FRAMEWORK=unity", `-DUNIT_TEST_IDE_HELPER=${input.options.cmakeHelper}`,
    `-DUNIT_TEST_IDE_UNITY_ROOT=${input.options.preparedFrameworkRoots.unity}`,
    `-DUNIT_TEST_IDE_CMOCK_ROOT=${input.options.preparedFrameworkRoots.cmock}`, `-DUTIDE_UNITY_RUNNER_GENERATOR=${generator}`]);
  for (const [name, source] of [["unity.framework", "frameworks/unity/tests/framework_tests.c"], ["unity.matrix.malformed", "framework-matrix/malformed_unity.c"]]) {
    const manifest = JSON.parse(await readFile(join(build, ".unit-test-ide", createHash("sha256").update(name!).digest("hex"), "manifest.json"), "utf8"));
    assert.deepEqual(manifest.sources, [source]);
    assert.ok(manifest.sources.every((path: string) => !path.split("/").includes("..")));
  }
});

test("staging creates one closed owned framework workspace with canonical configuration", async (t) => {
  const fixture = await workspaceFixture(t);
  const staged = await stageFrameworkWorkspace(fixture.options);

  assert.deepEqual(Object.keys(staged).sort(), ["buildRoot", "family", "frameworkId", "serviceDirectory", "workspaceRoot"]);
  assert.equal(staged.family, "gcc");
  assert.equal(staged.frameworkId, "cpputest");
  assert.deepEqual(await tree(fixture.stageRoot), [
    "owner.json",
    "service/",
    "workspace/",
    "workspace/.unit-test-ide/",
    "workspace/.unit-test-ide/workspace.json",
    "workspace/source/",
    "workspace/source/CMakeLists.txt",
    "workspace/source/CMakePresets.json",
    "workspace/source/framework-matrix/",
    "workspace/source/framework-matrix/CMakeLists.txt",
    "workspace/source/framework-matrix/contract.json",
    "workspace/source/framework-matrix/malformed_cpputest.cpp",
    "workspace/source/framework-matrix/malformed_unity.c",
    "workspace/source/framework-matrix/opaque.c",
    "workspace/source/frameworks/",
    "workspace/source/frameworks/cpputest/",
    "workspace/source/frameworks/cpputest/.unit-test-ide/",
    "workspace/source/frameworks/cpputest/.unit-test-ide/workspace.json",
    "workspace/source/frameworks/cpputest/CMakeLists.txt",
    "workspace/source/frameworks/cpputest/fixture.json",
    "workspace/source/frameworks/cpputest/tests/",
    "workspace/source/frameworks/cpputest/tests/framework_tests.cpp",
    "workspace/source/frameworks/unity/",
    "workspace/source/frameworks/unity/.unit-test-ide/",
    "workspace/source/frameworks/unity/.unit-test-ide/workspace.json",
    "workspace/source/frameworks/unity/CMakeLists.txt",
    "workspace/source/frameworks/unity/cmock.yml",
    "workspace/source/frameworks/unity/fixture.json",
    "workspace/source/frameworks/unity/include/",
    "workspace/source/frameworks/unity/include/Dependency.h",
    "workspace/source/frameworks/unity/mocks/",
    "workspace/source/frameworks/unity/mocks/cmock-generation.json",
    "workspace/source/frameworks/unity/mocks/MockDependency.c",
    "workspace/source/frameworks/unity/mocks/MockDependency.h",
    "workspace/source/frameworks/unity/tests/",
    "workspace/source/frameworks/unity/tests/fixture_runtime.c",
    "workspace/source/frameworks/unity/tests/framework_tests.c",
  ]);

  assert.deepEqual(JSON.parse(await readFile(join(fixture.stageRoot, "owner.json"), "utf8")), {
    candidate: "1".repeat(40), invocationId: "01234567-89ab-4def-8123-456789abcdef", platform: "linux", schemaVersion: 1,
  });
  const workspace = JSON.parse(await readFile(join(staged.workspaceRoot, ".unit-test-ide", "workspace.json"), "utf8"));
  assert.deepEqual(workspace, {
    projects: [{
      fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
      id: "framework-matrix", sourceDir: "source",
      tests: { containers: [
        { ctestName: "cpputest.framework", framework: "cpputest" },
        { ctestName: "cpputest.matrix.malformed", framework: "cpputest" },
        { ctestName: "cpputest.matrix.opaque", framework: "cpputest" },
      ] },
    }], version: 2,
  });
  const presets = JSON.parse(await readFile(join(staged.workspaceRoot, "source", "CMakePresets.json"), "utf8"));
  assert.equal(presets.configurePresets[0].cacheVariables.UNIT_TEST_IDE_FRAMEWORK, "cpputest");
  assert.equal(presets.configurePresets[0].cacheVariables.CMAKE_C_COMPILER, "gcc");
  assert.equal(presets.configurePresets[0].cacheVariables.CMAKE_CXX_COMPILER, "g++");
  assert.equal(presets.configurePresets[0].cacheVariables.UTIDE_UNITY_RUNNER_GENERATOR, undefined);
  assert.equal(await readFile(join(staged.workspaceRoot, ".unit-test-ide", "workspace.json"), "utf8"), `${JSON.stringify(workspace, null, 2)}\n`);
  await validateOwnedFrameworkStage(fixture.stageRoot, fixture.options.ownershipId);
});

test("staging rejects unlocked, linked, incomplete, escaping, and incompatible inputs", async (t) => {
  const mutations: Array<[string, (fixture: Awaited<ReturnType<typeof workspaceFixture>>) => Promise<void>, RegExp]> = [
    ["extra matrix file", async ({ repositoryRoot }) => writeFile(join(repositoryRoot, "testdata", "framework-matrix", "extra.c"), "x"), /unlocked|extra/u],
    ["missing CMock output", async ({ repositoryRoot }) => rm(join(repositoryRoot, "testdata", "frameworks", "unity", "mocks", "MockDependency.c")), /missing.*locked|CMock/u],
    ["escaping prepared root", async (fixture) => { (fixture.options.preparedFrameworkRoots as { cpputest: string }).cpputest = join(fixture.repositoryRoot, "..", "outside"); }, /prepared.*root|outside/u],
    ["invalid generator", async (fixture) => { (fixture.options as { unityRunnerGenerator: string }).unityRunnerGenerator = join(fixture.repositoryRoot, "missing-generator"); }, /generator/u],
    ["incompatible family", async (fixture) => { (fixture.options as { family: string }).family = "msvc"; }, /toolchain is incompatible/u],
  ];
  for (const [name, mutate, expected] of mutations) {
    await t.test(name, async (t) => {
      const fixture = await workspaceFixture(t);
      await mutate(fixture);
      await assert.rejects(stageFrameworkWorkspace(fixture.options), expected);
    });
  }

  await t.test("source symlink", async (t) => {
    const fixture = await workspaceFixture(t);
    const path = join(fixture.repositoryRoot, "testdata", "framework-matrix", "opaque.c");
    await rm(path);
    try { await symlink(join(fixture.repositoryRoot, "generator"), path, "file"); }
    catch (error) { t.skip(`symlink unavailable: ${(error as NodeJS.ErrnoException).code}`); return; }
    await assert.rejects(stageFrameworkWorkspace(fixture.options), /symbolic link|regular file|unsafe/u);
  });
});

test("staging preserves an unknown prior stage and validates exact ownership", async (t) => {
  const fixture = await workspaceFixture(t);
  await mkdir(fixture.stageRoot, { recursive: true });
  await writeFile(join(fixture.stageRoot, "unknown"), "preserve");
  await assert.rejects(stageFrameworkWorkspace(fixture.options), /staging ownership/u);
  assert.equal(await readFile(join(fixture.stageRoot, "unknown"), "utf8"), "preserve");

  await rm(fixture.stageRoot, { recursive: true });
  await stageFrameworkWorkspace(fixture.options);
  await assert.rejects(validateOwnedFrameworkStage(fixture.stageRoot, "different-owner"), /staging ownership/u);
  await assert.rejects(validateOwnedFrameworkStage(join(fixture.stageRoot, ".."), fixture.options.ownershipId), /staging ownership/u);
});

test("staging accepts only the fixed repository framework-work coordinate", async (t) => {
  const fixture = await workspaceFixture(t);
  (fixture.options as { stageRoot: string }).stageRoot = join(fixture.repositoryRoot, ".owned-stage", "framework");
  await assert.rejects(stageFrameworkWorkspace(fixture.options), /fixed framework-work layout/u);
  await assert.rejects(lstat(fixture.options.stageRoot), (error: NodeJS.ErrnoException) => error.code === "ENOENT");
});

test("staging rejects a linked framework-work ancestor without mutating its outside target", async (t) => {
  const fixture = await workspaceFixture(t);
  const outside = await mkdtemp(join(tmpdir(), "utide-framework-outside-"));
  t.after(() => rm(outside, { recursive: true, force: true }));
  await mkdir(join(fixture.repositoryRoot, ".native-e2e"), { recursive: true });
  const linkedParent = join(fixture.repositoryRoot, ".native-e2e", "framework-work");
  try { await symlink(outside, linkedParent, process.platform === "win32" ? "junction" : "dir"); }
  catch (error) { t.skip(`directory link unavailable: ${(error as NodeJS.ErrnoException).code}`); return; }

  await assert.rejects(stageFrameworkWorkspace(fixture.options), /staging ancestor|symbolic link|unsafe/u);
  await assert.rejects(lstat(join(outside, "linux")), (error: NodeJS.ErrnoException) => error.code === "ENOENT");
});

test("compiled executable hashing accepts one fixed regular build artifact", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "utide-framework-executable-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const buildRoot = join(root, "linux", "gcc", "cpputest", "service", "data", "build");
  const profile = join(buildRoot, "a".repeat(64), "bin");
  await mkdir(profile, { recursive: true });
  await writeFile(join(profile, "phase9_cpputest"), "compiled bytes");
  assert.equal(
    await hashCompiledFrameworkExecutable(root, "linux", "gcc", "cpputest"),
    createHash("sha256").update("compiled bytes").digest("hex"),
  );
  const duplicate = join(buildRoot, "b".repeat(64), "bin");
  await mkdir(duplicate, { recursive: true });
  await writeFile(join(duplicate, "phase9_cpputest"), "duplicate");
  await assert.rejects(hashCompiledFrameworkExecutable(root, "linux", "gcc", "cpputest"), /exactly one/u);
  await assert.rejects(hashCompiledFrameworkExecutable(root, "linux", "msvc", "cpputest"), /toolchain is incompatible/u);
});

async function workspaceFixture(t: test.TestContext) {
  const repositoryRoot = await mkdtemp(join(tmpdir(), "utide-framework-workspace-"));
  t.after(() => rm(repositoryRoot, { recursive: true, force: true }));
  for (const name of MATRIX_FILES) await write(repositoryRoot, `testdata/framework-matrix/${name}`, matrixContent(name));
  for (const [framework, files] of Object.entries(FIXTURE_FILES)) {
    for (const name of files) await write(repositoryRoot, `testdata/frameworks/${framework}/${name}`, `${framework}:${name}\n`);
  }
  const prepared = join(repositoryRoot, ".prepared");
  for (const name of ["cpputest", "unity", "cmock"]) await mkdir(join(prepared, name), { recursive: true });
  await write(repositoryRoot, "sdk/cmake/UnitTestIDE.cmake", "# helper\n");
  await write(repositoryRoot, "generator", "generator\n");
  const stageRoot = join(repositoryRoot, ".native-e2e", "framework-work", ".staging", "01234567-89ab-4def-8123-456789abcdef", "linux", "gcc", "cpputest");
  const options: FrameworkWorkspaceStageOptions = {
    repositoryRoot, stageRoot, platform: "linux", family: "gcc", frameworkId: "cpputest",
    ownershipId: "01234567-89ab-4def-8123-456789abcdef", candidateCommit: "1".repeat(40),
    preparedFrameworkRoots: {
      cpputest: join(prepared, "cpputest"), unity: join(prepared, "unity"), cmock: join(prepared, "cmock"),
    },
    cmakeHelper: join(repositoryRoot, "sdk", "cmake", "UnitTestIDE.cmake"),
    unityRunnerGenerator: join(repositoryRoot, "generator"),
  };
  return { repositoryRoot, stageRoot, options };
}

function matrixContent(name: typeof MATRIX_FILES[number]): string {
  if (name !== "contract.json") return `${name}\n`;
  return JSON.stringify({
    schemaVersion: 1,
    frameworks: {
      cpputest: { primaryCTestName: "cpputest.framework", malformedCTestName: "cpputest.matrix.malformed", opaqueCTestName: "cpputest.matrix.opaque" },
      unity: { primaryCTestName: "unity.framework", malformedCTestName: "unity.matrix.malformed", opaqueCTestName: "unity.matrix.opaque" },
    },
  });
}

async function write(root: string, relative: string, content: string): Promise<void> {
  const path = join(root, ...relative.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, content);
}

async function tree(root: string): Promise<string[]> {
  const result: string[] = [];
  async function visit(directory: string, prefix: string): Promise<void> {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relative = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      const info = await lstat(join(directory, entry.name));
      result.push(`${relative}${info.isDirectory() ? "/" : ""}`);
      if (info.isDirectory()) await visit(join(directory, entry.name), relative);
    }
  }
  await visit(root, "");
  return result;
}
