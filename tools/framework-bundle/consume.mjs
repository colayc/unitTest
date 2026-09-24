import { createHash } from "node:crypto";
import { lstat, readFile, readdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { checkFrameworkBundle } from "./check.mjs";
import { frameworkFailure, readFrameworkManifest } from "./manifest.mjs";
const repositoryRoot = resolve(import.meta.dirname, "..", "..");

const lockedFixtures = Object.freeze({
  cpputest: Object.freeze({
    sourceSha256: "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2",
    metadataSha256: "6eef50ec7940e4a6b80891d0ff452ed503b5996de313df711ecad607185761ee",
    dependencies: Object.freeze(["cpputest"]),
    files: Object.freeze([".unit-test-ide/workspace.json", "CMakeLists.txt", "fixture.json", "tests/framework_tests.cpp"]),
  }),
  unity: Object.freeze({
    sourceSha256: "c1a3571d4670320c05b98fa6cc0794d616412aa1c925e97f4c2bdf4e41466a2b",
    metadataSha256: "1287993f09fb2d8079933ac89a85bd464122edac7b3c92621692a02484536333",
    dependencies: Object.freeze(["unity", "cmock"]),
    files: Object.freeze([
      ".unit-test-ide/workspace.json", "CMakeLists.txt", "cmock.yml", "fixture.json", "include/Dependency.h",
      "mocks/MockDependency.c", "mocks/MockDependency.h", "mocks/cmock-generation.json",
      "tests/fixture_runtime.c", "tests/framework_tests.c",
    ]),
  }),
});

function fixtureFailure(message, cause) {
  return frameworkFailure("FRAMEWORK_FIXTURE_VALIDATION_FAILED", message, cause);
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

async function regularBytes(path, label) {
  try {
    const metadata = await lstat(path);
    if (!metadata.isFile() || metadata.isSymbolicLink()) throw fixtureFailure(`${label} is not a regular file`);
    return await readFile(path);
  } catch (error) {
    if (error?.code === "FRAMEWORK_FIXTURE_VALIDATION_FAILED") throw error;
    throw fixtureFailure(`${label} cannot be read`, error);
  }
}

async function canonicalFixtureSourceDigest(root, locked, label) {
  let rootMetadata;
  try {
    rootMetadata = await lstat(root);
  } catch (error) {
    throw fixtureFailure(`${label} source cannot be inspected`, error);
  }
  if (!rootMetadata.isDirectory() || rootMetadata.isSymbolicLink()) throw fixtureFailure(`${label} source root is unsafe`);
  const expectedFiles = new Set(locked.files);
  const expectedDirectories = new Set(locked.files.flatMap((path) => {
    const parts = path.split("/");
    return parts.slice(0, -1).map((_, index) => parts.slice(0, index + 1).join("/"));
  }));
  const foundFiles = new Set();
  async function visit(directory, prefix) {
    let entries;
    try {
      entries = await readdir(directory, { withFileTypes: true });
    } catch (error) {
      throw fixtureFailure(`${label} source cannot be inspected`, error);
    }
    entries.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0);
    for (const entry of entries) {
      const relative = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      const metadata = await lstat(join(directory, entry.name));
      if (metadata.isSymbolicLink()) throw fixtureFailure(`${label} source contains a symbolic link`);
      if (metadata.isDirectory()) {
        if (!expectedDirectories.has(relative)) throw fixtureFailure(`${label} source contains an unlocked directory`);
        await visit(join(directory, entry.name), relative);
      } else if (metadata.isFile() && expectedFiles.has(relative)) {
        foundFiles.add(relative);
      } else {
        throw fixtureFailure(`${label} source contains an unlocked entry`);
      }
    }
  }
  await visit(root, "");
  if (foundFiles.size !== expectedFiles.size) throw fixtureFailure(`${label} source is missing a locked file`);
  const hash = createHash("sha256");
  for (const relative of [...locked.files].sort()) {
    const bytes = await regularBytes(join(root, ...relative.split("/")), `${label} source ${relative}`);
    let text;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    } catch (error) {
      throw fixtureFailure(`${label} source ${relative} is not UTF-8`, error);
    }
    text = text.replaceAll("\r\n", "\n");
    if (text.includes("\r") || text.includes("\0")) throw fixtureFailure(`${label} source ${relative} has unsafe text bytes`);
    hash.update(`f:${relative}\0`);
    hash.update(text, "utf8");
  }
  return hash.digest("hex");
}

async function loadLockedFixture(root, frameworkId, manifestSha256, treeSha256, cMockProvenanceSha256) {
  const locked = lockedFixtures[frameworkId];
  const fixtureRoot = join(root, "testdata", "frameworks", frameworkId);
  const sourceSha256 = await canonicalFixtureSourceDigest(fixtureRoot, locked, `${frameworkId} fixture`);
  if (sourceSha256 !== locked.sourceSha256) throw fixtureFailure(`${frameworkId} fixture source does not match the committed lock`);
  const metadataSha256 = sha256(await regularBytes(join(fixtureRoot, "fixture.json"), `${frameworkId} fixture metadata`));
  if (metadataSha256 !== locked.metadataSha256) throw fixtureFailure(`${frameworkId} fixture metadata does not match the committed lock`);
  const dependencies = locked.dependencies.map((id) => ({ id, treeSha256: treeSha256[id] }));
  const executableSha256 = sha256(Buffer.from(JSON.stringify({
    schemaVersion: 1,
    frameworkId,
    manifestSha256,
    sourceSha256,
    dependencies,
    ...(frameworkId === "unity" ? { cMockProvenanceSha256 } : {}),
  }), "utf8"));
  return Object.freeze({ metadataSha256, sourceSha256, executableSha256 });
}

export async function loadF1FrameworkIdentity(repositoryRootValue) {
  if (typeof repositoryRootValue !== "string" || repositoryRootValue.length === 0 || repositoryRootValue.includes("\0")) {
    throw fixtureFailure("repository root is invalid");
  }
  const root = resolve(repositoryRootValue);
  const manifestPath = join(root, "tools", "framework-bundle", "manifest.json");
  const { manifest, manifestSha256 } = await readFrameworkManifest(manifestPath);
  const checked = await checkFrameworkBundle({ repositoryRoot: root, manifestPath });
  if (checked.manifestSha256 !== manifestSha256) throw fixtureFailure("framework manifest identity changed during validation");
  const frameworkTreeSha256 = Object.freeze(Object.fromEntries(
    manifest.frameworks.map(({ id, treeSha256 }) => [id, treeSha256]),
  ));
  const fixtures = Object.freeze(Object.fromEntries(await Promise.all(
    Object.keys(lockedFixtures).map(async (frameworkId) => [frameworkId, await loadLockedFixture(
      root, frameworkId, manifestSha256, frameworkTreeSha256, checked.cMockProvenanceSha256,
    )]),
  )));
  return Object.freeze({
    manifestSha256,
    frameworkTreeSha256,
    cMockProvenanceSha256: checked.cMockProvenanceSha256,
    fixtures,
  });
}

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
