import { execFile as execFileCallback } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const repositoryRoot = resolve(import.meta.dirname, "..", "..");

function parseArguments(arguments_) {
  if (arguments_.length !== 2 || arguments_[0] !== "--cmake" || !arguments_[1].startsWith("/") || arguments_[1].includes("\0")) throw new Error("usage: consume.mjs --cmake <absolute-linux-cmake>");
  return arguments_[1];
}

export async function consumeLockedFrameworks(cmake) {
  if (process.platform !== "linux") throw new Error("locked framework consumption requires a Linux runner");
  const { prepareLinuxFrameworkInputs } = await import("../service-probe/dist/linux-framework-inputs.js");
  const { prepareTestFrameworkWorkspace } = await import("../service-probe/dist/test-framework-fixture.js");
  const sourceRoot = join(repositoryRoot, ".superpowers", "runtime", "framework-bundle", "linux-x64");
  const boundary = await prepareLinuxFrameworkInputs({
    manifest: JSON.parse(await readFile(join(repositoryRoot, "tools", "framework-bundle", "manifest.json"), "utf8")),
    cacheRoot: join(repositoryRoot, ".superpowers", "cache", "framework-bundle"),
    sourceRoot,
    helperPath: join(repositoryRoot, "sdk", "cmake", "UnitTestIDE.cmake"),
    generatorPath: join(repositoryRoot, "build", "unity-runner-generator"),
    repositoryRoot
  });
  const root = await mkdtemp(join(tmpdir(), "unit-test-ide-locked-frameworks-"));
  try {
    for (const framework of ["cpputest", "unity"]) {
      const workspace = join(root, framework);
      await prepareTestFrameworkWorkspace(workspace, {
        framework,
        platform: "linux",
        linuxFrameworkInputs: {
          cpputestRoot: boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT,
          unityRoot: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT,
          cmakeHelper: boundary.environment.UNIT_TEST_IDE_TEST_CMAKE_HELPER,
          unityRunnerGenerator: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR
        }
      });
      await execFile(cmake, ["--preset", "fixture", "-DCMAKE_POLICY_VERSION_MINIMUM=3.5"], { cwd: workspace, shell: false, timeout: 120_000, maxBuffer: 1024 * 1024 });
      await execFile(cmake, ["--build", "build-fixture"], { cwd: workspace, shell: false, timeout: 120_000, maxBuffer: 1024 * 1024 });
      await execFile("ctest", ["--test-dir", "build-fixture", "--output-on-failure"], { cwd: workspace, shell: false, timeout: 120_000, maxBuffer: 1024 * 1024 });
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
  process.stdout.write(`${JSON.stringify({ frameworkBoundary: "verified", identityDigest: boundary.identityDigest })}\n`);
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  consumeLockedFrameworks(parseArguments(process.argv.slice(2))).catch((error) => {
    process.stderr.write(`framework-consume: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
