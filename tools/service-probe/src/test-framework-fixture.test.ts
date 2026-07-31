import assert from "node:assert/strict";
import { access, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  prepareTestFrameworkWorkspace,
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
