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
      `add_executable(fixture-app ${framework === "unity" ? "main.c" : "main.cpp"})`,
      "add_test(NAME framework-tests COMMAND fixture-app)",
      ""
    ].join("\n")
  );
  await writeFile(
    join(workspaceDirectory, framework === "unity" ? "main.c" : "main.cpp"),
    "int main(void) { return 0; }\n"
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
