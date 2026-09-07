import assert from "node:assert/strict";
import { access, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  prepareTestFrameworkWorkspace,
  type TestFramework,
  testFixtureExecutableName
} from "./test-framework-fixture.js";

test("test framework workspace is closed and pins the fixture executable", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-framework-fixture-"));
  try {
    const workspace = join(root, "workspace");
    const prepared = await prepareTestFrameworkWorkspace(
      workspace
    );
    assert.equal(
      prepared.testExecutable,
      join(
        workspace,
        "build-fixture",
        "bin",
        testFixtureExecutableName()
      )
    );
    await assert.rejects(
      access(prepared.testExecutable),
      /ENOENT/,
      "the Service-owned build must materialize the executable"
    );
    const config = JSON.parse(
      await readFile(
        join(workspace, ".unit-test-ide", "workspace.json"),
        "utf8"
      )
    ) as {
      version: number;
      projects: Array<{
        tests: {
          containers: Array<{
            ctestName: string;
            framework: string;
          }>;
        };
      }>;
    };
    assert.equal(config.version, 2);
    assert.deepEqual(config.projects[0]?.tests.containers, [{
      ctestName: "framework-tests",
      framework: "cpputest"
    }]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("test-only framework fixture can declare a closed Unity C workspace", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-framework-unity-fixture-"));
  try {
    const framework: TestFramework = "unity";
    const workspace = join(root, "workspace");
    await prepareTestFrameworkWorkspace(workspace, { framework });
    const cmake = await readFile(join(workspace, "CMakeLists.txt"), "utf8");
    assert.match(cmake, /LANGUAGES C/u);
    assert.match(cmake, /fixture-app main\.c/u);
    const config = JSON.parse(await readFile(
      join(workspace, ".unit-test-ide", "workspace.json"), "utf8"
    )) as { projects: Array<{ tests: { containers: Array<{ framework: string }> } }> };
    assert.deepEqual(config.projects[0]?.tests.containers, [{
      ctestName: "framework-tests",
      framework: "unity"
    }]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("Linux test-only fixture accepts only an explicit verified framework seam", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-framework-linux-seam-"));
  try {
    const workspace = join(root, "workspace");
    const inputs = {
      cpputestRoot: join(root, "cpputest"),
      unityRoot: join(root, "unity"),
      cmakeHelper: join(root, "UnitTestIDE.cmake"),
      unityRunnerGenerator: join(root, "unity-runner-generator")
    };
    await prepareTestFrameworkWorkspace(workspace, {
      framework: "unity",
      platform: "linux",
      linuxFrameworkInputs: inputs
    });
    const cmake = await readFile(join(workspace, "CMakeLists.txt"), "utf8");
    assert.match(cmake, /UTIDE_UNITY_RUNNER_GENERATOR/u);
    assert.match(cmake, /UnitTestIDE\.cmake/u);
    assert.match(cmake, /Unity/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
