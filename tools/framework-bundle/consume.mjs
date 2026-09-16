import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { readFrameworkManifest } from "./manifest.mjs";
const repositoryRoot = resolve(import.meta.dirname, "..", "..");

function parseArguments(arguments_) {
  if (arguments_.length !== 2 || arguments_[0] !== "--cmake" || !arguments_[1].startsWith("/") || arguments_[1].includes("\0")) throw new Error("usage: consume.mjs --cmake <absolute-linux-cmake>");
  return arguments_[1];
}

export async function consumeLockedFrameworks(cmake) {
  if (process.platform !== "linux") throw new Error("locked framework consumption requires a Linux runner");
  const { prepareLinuxFrameworkInputs } = await import("../service-probe/dist/linux-framework-inputs.js");
  const { verifyFrameworkFixtures } = await import("./verify-fixtures.mjs");
  const manifestPath = join(repositoryRoot, "tools", "framework-bundle", "manifest.json");
  const lockedManifest = await readFrameworkManifest(manifestPath);
  const manifestBytes = await readFile(manifestPath);
  const manifestSha256 = (await import("node:crypto")).createHash("sha256").update(manifestBytes).digest("hex");
  if (manifestSha256 !== lockedManifest.manifestSha256) throw new Error("framework manifest reader digest mismatch");
  const sourceRoot = join(repositoryRoot, ".superpowers", "runtime", "framework-bundle", "v2", manifestSha256);
  const boundary = await prepareLinuxFrameworkInputs({
    manifest: lockedManifest.manifest,
    cacheRoot: join(repositoryRoot, ".superpowers", "cache", "framework-bundle"),
    sourceRoot,
    helperPath: join(repositoryRoot, "sdk", "cmake", "UnitTestIDE.cmake"),
    generatorPath: join(repositoryRoot, "build", "unity-runner-generator"),
    repositoryRoot,
    manifestSha256
  });
  await verifyFrameworkFixtures({
    repositoryRoot, cmake, generator: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR,
    toolchains: ["gcc"], frameworks: ["cpputest", "unity"], platform: "linux",
    frameworkInputs: { cpputestRoot: boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT, unityRoot: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT, cmockRoot: boundary.environment.UNIT_TEST_IDE_TEST_CMOCK_ROOT, helper: boundary.environment.UNIT_TEST_IDE_TEST_CMAKE_HELPER }
  });
  process.stdout.write(`${JSON.stringify({ frameworkBoundary: "verified", identityDigest: boundary.identityDigest })}\n`);
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  consumeLockedFrameworks(parseArguments(process.argv.slice(2))).catch((error) => {
    process.stderr.write(`framework-consume: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
