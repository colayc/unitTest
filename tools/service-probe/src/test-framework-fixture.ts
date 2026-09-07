import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

export interface TestFrameworkWorkspace {
  readonly buildDirectory: string;
  readonly testExecutable: string;
}

/** Test-only fixture selector; it is never serialized into Workspace config. */
export type TestFramework = "cpputest" | "unity";

export interface TestFrameworkFixtureOptions {
  readonly framework?: TestFramework;
  /** Linux-only test seam for already-verified framework inputs. */
  readonly linuxFrameworkInputs?: LinuxFrameworkFixtureInputs;
  readonly platform?: NodeJS.Platform;
}

export interface LinuxFrameworkFixtureInputs {
  readonly cpputestRoot: string;
  readonly unityRoot: string;
  readonly cmakeHelper: string;
  readonly unityRunnerGenerator: string;
}

export function testFixtureExecutableName(
  platform: NodeJS.Platform = process.platform
): string {
  return platform === "win32" ? "fixture-app.exe" : "fixture-app";
}

export async function prepareTestFrameworkWorkspace(
  workspaceDirectory: string,
  options: TestFrameworkFixtureOptions = {}
): Promise<TestFrameworkWorkspace> {
  if (!workspaceDirectory) {
    throw new Error("test framework workspace path is required");
  }
  const framework = options.framework ?? "cpputest";
  const platform = options.platform ?? process.platform;
  if (options.linuxFrameworkInputs !== undefined && platform !== "linux") {
    throw new Error("Linux framework fixture inputs are only valid on Linux");
  }
  const linuxInputs = options.linuxFrameworkInputs;
  const configurationDirectory = join(
    workspaceDirectory,
    ".unit-test-ide"
  );
  const buildDirectory = join(
    workspaceDirectory,
    "build-fixture"
  );
  const executableDirectory = join(buildDirectory, "bin");
  const testExecutable = join(
    executableDirectory,
    testFixtureExecutableName()
  );
  await mkdir(configurationDirectory, { recursive: true });
  await writeFile(
    join(configurationDirectory, "workspace.json"),
    JSON.stringify({
      version: 2,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { configurations: ["Debug"] },
        tests: {
          containers: [{
            ctestName: "framework-tests",
            framework
          }]
        }
      }]
    })
  );
  await writeFile(
    join(workspaceDirectory, "CMakeLists.txt"),
    [
      "cmake_minimum_required(VERSION 3.25)",
      `project(test_framework_fixture LANGUAGES ${framework === "unity" ? "C" : "CXX"})`,
      ...(linuxInputs === undefined ? [] : linuxFrameworkCmake(linuxInputs, framework)),
      `add_executable(fixture-app ${framework === "unity" ? (linuxInputs === undefined ? "main.c" : "fixture-unity.c") : "main.cpp"})`,
      ...(framework === "unity" && linuxInputs !== undefined ? [] : ["add_test(NAME framework-tests COMMAND fixture-app)"]),
      ""
    ].join("\n")
  );
  await writeFile(
    join(workspaceDirectory, framework === "unity" ? (linuxInputs === undefined ? "main.c" : "fixture-unity.c") : "main.cpp"),
    framework === "unity" && linuxInputs !== undefined
      ? "#include <unity.h>\nvoid test_fixture_passes(void) { TEST_ASSERT_TRUE(1); }\n"
      : "int main(void) { return 0; }\n"
  );
  await writeFile(
    join(workspaceDirectory, "CMakePresets.json"),
    JSON.stringify({
      version: 6,
      configurePresets: [{
        name: "fixture",
        generator: "Ninja",
        binaryDir: "${sourceDir}/build-fixture"
      }]
    })
  );
  return { buildDirectory, testExecutable };
}

function linuxFrameworkCmake(inputs: LinuxFrameworkFixtureInputs, framework: TestFramework): string[] {
  for (const [name, value] of Object.entries(inputs)) {
    if (!value || value.includes("\0")) throw new Error(`Linux framework fixture ${name} is invalid`);
  }
  return [
    `set(UTIDE_UNITY_RUNNER_GENERATOR "${inputs.unityRunnerGenerator}")`,
    `include("${inputs.cmakeHelper}")`,
    ...(framework === "unity"
      ? [
          `add_library(unit_test_ide_unity STATIC "${inputs.unityRoot}/src/unity.c")`,
          `target_include_directories(unit_test_ide_unity PUBLIC "${inputs.unityRoot}/src")`,
          "target_link_libraries(fixture-app PRIVATE unit_test_ide_unity)",
          "unit_test_ide_add_unity_test(TEST framework-tests TARGET fixture-app TEST_SOURCES fixture-unity.c)"
        ]
      : [
          `add_subdirectory("${inputs.cpputestRoot}" "\${CMAKE_BINARY_DIR}/unit-test-ide-cpputest" EXCLUDE_FROM_ALL)`,
          "target_link_libraries(fixture-app PRIVATE CppUTest)"
        ])
  ];
}
