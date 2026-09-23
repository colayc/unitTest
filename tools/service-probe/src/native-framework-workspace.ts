import { createHash } from "node:crypto";
import { execFile as execFileCallback } from "node:child_process";
import { realpathSync } from "node:fs";
import { copyFile, cp, lstat, mkdir, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { promisify } from "node:util";
import type { FrameworkId, FrameworkPlatform, FrameworkToolchainFamily } from "./native-framework-report.js";
import type { F1FrameworkIdentity } from "./native-framework-matrix.js";

const MATRIX_FILES = Object.freeze([
  "CMakeLists.txt",
  "contract.json",
  "malformed_cpputest.cpp",
  "malformed_unity.c",
  "opaque.c",
]);
const FIXTURE_FILES = Object.freeze({
  cpputest: Object.freeze([
    ".unit-test-ide/workspace.json", "CMakeLists.txt", "fixture.json", "tests/framework_tests.cpp",
  ]),
  unity: Object.freeze([
    ".unit-test-ide/workspace.json", "CMakeLists.txt", "cmock.yml", "fixture.json", "include/Dependency.h",
    "mocks/MockDependency.c", "mocks/MockDependency.h", "mocks/cmock-generation.json",
    "tests/fixture_runtime.c", "tests/framework_tests.c",
  ]),
});
const PLATFORM_FAMILIES = Object.freeze({
  linux: Object.freeze(["gcc", "clang"] as const),
  win32: Object.freeze(["msvc", "clang-cl"] as const),
}) satisfies Readonly<Record<FrameworkPlatform, readonly FrameworkToolchainFamily[]>>;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const COMMIT = /^[0-9a-f]{40}$/u;
const BUILD_PROFILE = /^[0-9a-f]{64}$/u;
const execFile = promisify(execFileCallback);
const WINDOWS_INPUT_DIRECTORY = Object.freeze({ cpputest: "c", unity: "u", cmock: "m" });

export interface FrameworkWorkspaceStageOptions {
  readonly repositoryRoot: string;
  readonly stageRoot: string;
  readonly ownershipId: string;
  readonly candidateCommit: string;
  readonly platform: FrameworkPlatform;
  readonly family: FrameworkToolchainFamily;
  readonly frameworkId: FrameworkId;
  readonly preparedFrameworkRoots: Readonly<Record<"cpputest" | "unity" | "cmock", string>>;
  readonly cmakeHelper: string;
  readonly unityRunnerGenerator: string;
}

export interface StagedFrameworkWorkspace {
  readonly serviceDirectory: string;
  readonly workspaceRoot: string;
  readonly buildRoot: string;
  readonly family: FrameworkToolchainFamily;
  readonly frameworkId: FrameworkId;
}

export async function stageFrameworkWorkspace(
  options: FrameworkWorkspaceStageOptions,
): Promise<StagedFrameworkWorkspace> {
  validateClosedOptions(options);
  const repositoryRoot = resolve(options.repositoryRoot);
  await requireDirectDirectory(repositoryRoot, "repository root");
  const stageRoot = resolve(options.stageRoot);
  const platformName = options.platform === "win32" ? "windows" : "linux";
  const expectedStageRoot = join(
    repositoryRoot, ".native-e2e", "framework-work", ".staging", options.ownershipId, platformName, options.family, options.frameworkId,
  );
  if (!samePath(stageRoot, expectedStageRoot)) {
    throw new Error("owned staging root must use the fixed framework-work layout");
  }
  const existing = await lstat(stageRoot).catch(missingOnly);
  if (existing !== undefined) throw new Error("staging ownership cannot be established for an existing stage");

  const preparedFrameworkRoots = {
    cpputest: await requireContainedDirectory(repositoryRoot, options.preparedFrameworkRoots.cpputest, "prepared CppUTest root"),
    unity: await requireContainedDirectory(repositoryRoot, options.preparedFrameworkRoots.unity, "prepared Unity root"),
    cmock: await requireContainedDirectory(repositoryRoot, options.preparedFrameworkRoots.cmock, "prepared CMock root"),
  };
  const cmakeHelper = await requireContainedFile(repositoryRoot, options.cmakeHelper, "CMake helper");
  const unityRunnerGenerator = await requireContainedFile(repositoryRoot, options.unityRunnerGenerator, "Unity generator");
  const matrixRoot = join(repositoryRoot, "testdata", "framework-matrix");
  const fixtureRoots = {
    cpputest: join(repositoryRoot, "testdata", "frameworks", "cpputest"),
    unity: join(repositoryRoot, "testdata", "frameworks", "unity"),
  } as const;
  await validateInventory(matrixRoot, MATRIX_FILES, "framework matrix");
  await validateInventory(fixtureRoots.cpputest, FIXTURE_FILES.cpputest, "CppUTest fixture");
  await validateInventory(fixtureRoots.unity, FIXTURE_FILES.unity, "Unity fixture");
  const contract = parseContract(await readFile(join(matrixRoot, "contract.json")), options.frameworkId);

  await createVerifiedStageAncestors(repositoryRoot, dirname(stageRoot));
  await mkdir(stageRoot, { recursive: false, mode: 0o700 });
  let owned = false;
  try {
    await writeCanonical(join(stageRoot, "owner.json"), {
      schemaVersion: 1,
      invocationId: options.ownershipId,
      platform: options.platform,
      candidate: options.candidateCommit,
    });
    owned = true;
    const serviceDirectory = join(stageRoot, "service");
    // Keep the workspace as a sibling of service/ so the Service and publisher
    // can enforce their fixed disjoint-root contract. Windows CMake inputs are
    // shortened separately below, before they reach MSVC's generated paths.
    const workspaceRoot = join(stageRoot, "workspace");
    const matrixDestination = join(workspaceRoot, "source", "framework-matrix");
    await mkdir(serviceDirectory, { recursive: false, mode: 0o700 });
    await mkdir(matrixDestination, { recursive: true, mode: 0o700 });
    for (const name of MATRIX_FILES) await copyFile(join(matrixRoot, name), join(matrixDestination, name));
    for (const frameworkId of ["cpputest", "unity"] as const) {
      const destination = join(workspaceRoot, "source", "frameworks", frameworkId);
      for (const name of FIXTURE_FILES[frameworkId]) {
        const target = join(destination, ...name.split("/"));
        await mkdir(dirname(target), { recursive: true, mode: 0o700 });
        await copyFile(join(fixtureRoots[frameworkId], ...name.split("/")), target);
      }
    }
    if (options.platform === "win32" && options.frameworkId === "cpputest") {
      // F1 identity is validated against the repository fixture before this
      // stage is created. Keep that locked source unchanged, but shorten only
      // the generated workspace's CppUTest binary-directory label so MSVC's
      // absolute /Fo path remains below MAX_PATH.
      const fixtureCMake = join(workspaceRoot, "source", "frameworks", "cpputest", "CMakeLists.txt");
      const source = await readFile(fixtureCMake, "utf8");
      const shortened = source.replace(
        '"${CMAKE_BINARY_DIR}/upstream-cpputest"',
        '"${CMAKE_BINARY_DIR}/u"',
      );
      if (shortened === source) throw new Error("CppUTest fixture binary directory is not replaceable");
      await writeFile(fixtureCMake, shortened, { flag: "w", mode: 0o600 });
    }
    await mkdir(join(workspaceRoot, ".unit-test-ide"), { recursive: true, mode: 0o700 });
    const inputsRoot = join(workspaceRoot, ".unit-test-ide", "inputs");
    const preparedRoot = join(inputsRoot, "prepared");
    await mkdir(preparedRoot, { recursive: true, mode: 0o700 });
    for (const [frameworkId, source] of Object.entries(preparedFrameworkRoots) as Array<[keyof typeof preparedFrameworkRoots, string]>) {
      await copyOwnedDirectory(source, join(preparedRoot, frameworkId));
    }
    const helperDestination = join(inputsRoot, "cmake", "UnitTestIDE.cmake");
    await mkdir(dirname(helperDestination), { recursive: true, mode: 0o700 });
    await copyFile(cmakeHelper, helperDestination);
    const generatorDestination = join(inputsRoot, "tools", options.platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator");
    await mkdir(dirname(generatorDestination), { recursive: true, mode: 0o700 });
    await copyFile(unityRunnerGenerator, generatorDestination);
    const stagedInputs = {
      // MSVC hashes long source-relative object paths into a C_/... suffix;
      // even the dedicated C:\\utide workspace can exceed its 128-character
      // object-path limit when the validated inputs live below workspace/.
      // Keep the canonical workspace copies for ownership/evidence, and point
      // Windows CMake at a second Service-owned copy directly below the
      // workspace root so compiler input paths remain short enough for MSVC
      // while every File API path remains inside the Service boundary.
      cpputest: options.platform === "win32" ? join(workspaceRoot, WINDOWS_INPUT_DIRECTORY.cpputest) : join(preparedRoot, "cpputest"),
      unity: options.platform === "win32" ? join(workspaceRoot, WINDOWS_INPUT_DIRECTORY.unity) : join(preparedRoot, "unity"),
      cmock: options.platform === "win32" ? join(workspaceRoot, WINDOWS_INPUT_DIRECTORY.cmock) : join(preparedRoot, "cmock"),
    } as const;
    if (options.platform === "win32") {
      for (const [frameworkId, source] of Object.entries(preparedFrameworkRoots) as Array<[keyof typeof preparedFrameworkRoots, string]>) {
        await copyOwnedDirectory(source, join(workspaceRoot, WINDOWS_INPUT_DIRECTORY[frameworkId]));
      }
    }
    const cmakeInputs = options.platform === "win32"
      ? {
          cpputest: await requireContainedDirectory(repositoryRoot, await windowsShortPath(stagedInputs.cpputest), "short staged CppUTest root"),
          unity: await requireContainedDirectory(repositoryRoot, await windowsShortPath(stagedInputs.unity), "short staged Unity root"),
          cmock: await requireContainedDirectory(repositoryRoot, await windowsShortPath(stagedInputs.cmock), "short staged CMock root"),
        }
      : stagedInputs;
    await writeCanonical(join(workspaceRoot, ".unit-test-ide", "workspace.json"), workspaceConfiguration(options, contract));
    // Both the locked F1 fixtures and the matrix overlay must be descendants of
    // CMAKE_SOURCE_DIR; the Unity generator deliberately rejects source escapes.
    await writeFile(join(workspaceRoot, "source", "CMakeLists.txt"),
      projectConfiguration(options, cmakeInputs, helperDestination, generatorDestination),
      { flag: "wx", mode: 0o600 });
    return Object.freeze({
      serviceDirectory,
      workspaceRoot,
      buildRoot: join(serviceDirectory, "data", "build"),
      family: options.family,
      frameworkId: options.frameworkId,
    });
  } catch (error) {
    if (owned) {
      await validateOwnedFrameworkStage(stageRoot, options.ownershipId);
      await rm(stageRoot, { recursive: true });
    }
    throw error;
  }
}

async function windowsShortPath(value: string): Promise<string> {
  if (process.platform !== "win32") return value;
  const command = `for %I in ("${value.replaceAll('"', '""')}") do @echo %~sI`;
  const { stdout } = await execFile(process.env.ComSpec ?? "cmd.exe", ["/d", "/c", command], {
    windowsHide: true,
    windowsVerbatimArguments: true,
    maxBuffer: 1024 * 1024,
  });
  const short = stdout.split(/\r?\n/u).map((line) => line.trim()).filter(Boolean).at(-1);
  if (short === undefined || !isAbsolute(short)) throw new Error("Windows short path resolution failed");
  return short;
}

async function copyOwnedDirectory(source: string, destination: string): Promise<void> {
  await cp(source, destination, { recursive: true, dereference: false, errorOnExist: true, force: false });
  await validateCopiedDirectory(destination);
}

async function validateCopiedDirectory(root: string): Promise<void> {
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("staged framework input is unsafe");
  for (const name of await readdir(root)) {
    const path = join(root, name);
    const entry = await lstat(path);
    if (entry.isSymbolicLink()) throw new Error("staged framework input contains a symbolic link");
    if (entry.isDirectory()) await validateCopiedDirectory(path);
    else if (!entry.isFile()) throw new Error("staged framework input contains an unsupported entry");
  }
}

export async function validateOwnedFrameworkStage(stageRoot: string, ownershipId: string): Promise<void> {
  if (!isAbsolute(stageRoot) || !UUID.test(ownershipId)) throw new Error("staging ownership is invalid");
  await requireDirectDirectory(resolve(stageRoot), "staging ownership root");
  const ownerPath = join(resolve(stageRoot), "owner.json");
  await requireDirectFile(ownerPath, "staging ownership record");
  let owner: unknown;
  try { owner = JSON.parse(await readFile(ownerPath, "utf8")); }
  catch (error) { throw new Error("staging ownership record is invalid", { cause: error }); }
  if (!plainObject(owner) || !exactKeys(owner, ["candidate", "invocationId", "platform", "schemaVersion"]) ||
      owner.schemaVersion !== 1 || owner.invocationId !== ownershipId || !COMMIT.test(String(owner.candidate)) ||
      (owner.platform !== "linux" && owner.platform !== "win32")) {
    throw new Error("staging ownership does not match the current invocation");
  }
}

/** Proves the copied source evidence is still the repository's validated F1/matrix input. */
export async function verifyFrameworkWorkspaceSources(workspaceRoot: string, identity: F1FrameworkIdentity, contractBytes: Uint8Array): Promise<void> {
  const sha256 = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");
  const matrixRoot = join(workspaceRoot, "source/framework-matrix");
  await validateInventory(matrixRoot, MATRIX_FILES, "consumer matrix source");
  const contract = JSON.parse(Buffer.from(contractBytes).toString("utf8"));
  const expected: Readonly<Record<typeof MATRIX_FILES[number], string>> = {
    "contract.json": sha256(contractBytes),
    "CMakeLists.txt": contract.workspace.cmakeSha256,
    "opaque.c": contract.workspace.opaqueSourceSha256,
    "malformed_cpputest.cpp": contract.frameworks.cpputest.augmentationSha256,
    "malformed_unity.c": contract.frameworks.unity.augmentationSha256,
  };
  for (const name of MATRIX_FILES) {
    const path = await requireContainedFile(workspaceRoot, join(matrixRoot, name), "consumer matrix source");
    if (sha256(await readFile(path)) !== expected[name]) throw new Error("consumer matrix source digest mismatch");
  }
  for (const frameworkId of ["cpputest", "unity"] as const) {
    const root = join(workspaceRoot, "source/frameworks", frameworkId);
    await validateInventory(root, FIXTURE_FILES[frameworkId], "consumer framework source");
    const hash = createHash("sha256");
    for (const name of [...FIXTURE_FILES[frameworkId]].sort()) {
      const path = await requireContainedFile(workspaceRoot, join(root, name), "consumer framework source");
      const bytes = await readFile(path);
      const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes).replaceAll("\r\n", "\n");
      if (text.includes("\r") || text.includes("\0")) throw new Error("consumer framework source has unsafe text bytes");
      if (name === "fixture.json" && sha256(bytes) !== identity.fixtures[frameworkId].metadataSha256) {
        throw new Error("consumer framework source metadata digest mismatch");
      }
      hash.update(`f:${name}\0`).update(text, "utf8");
    }
    if (hash.digest("hex") !== identity.fixtures[frameworkId].sourceSha256) throw new Error("consumer framework source digest mismatch");
  }
}

export async function hashCompiledFrameworkExecutable(
  workRoot: string,
  platform: FrameworkPlatform,
  family: FrameworkToolchainFamily,
  frameworkId: FrameworkId,
): Promise<string> {
  return (await readCompiledFrameworkExecutable(workRoot, platform, family, frameworkId)).sha256;
}

export async function readCompiledFrameworkExecutable(
  workRoot: string,
  platform: FrameworkPlatform,
  family: FrameworkToolchainFamily,
  frameworkId: FrameworkId,
): Promise<Readonly<{ bytes: Buffer; profileId: string; sha256: string }>> {
  validateCombination(platform, family);
  if (!isAbsolute(workRoot)) throw new Error("framework work root must be absolute");
  const root = await requireDirectDirectory(resolve(workRoot), "framework work root");
  const platformName = platform === "win32" ? "windows" : "linux";
  const buildRoot = join(root, platformName, family, frameworkId, "service", "data", "build");
  requireContained(root, buildRoot, "framework build root");
  await requireDirectDirectory(buildRoot, `${frameworkId} framework build root`);
  const executableName = `phase9_${frameworkId}${platform === "win32" ? ".exe" : ""}`;
  const matches: string[] = [];
  for (const entry of await readdir(buildRoot, { withFileTypes: true })) {
    if (!BUILD_PROFILE.test(entry.name)) continue;
    const profileRoot = join(buildRoot, entry.name);
    const profileInfo = await lstat(profileRoot);
    if (!profileInfo.isDirectory() || profileInfo.isSymbolicLink()) throw new Error(`${frameworkId} framework build profile is unsafe`);
    const bin = join(profileRoot, "bin");
    const binInfo = await lstat(bin).catch(missingOnly);
    if (binInfo === undefined) continue;
    if (!binInfo.isDirectory() || binInfo.isSymbolicLink()) throw new Error(`${frameworkId} executable directory is unsafe`);
    const executable = join(bin, executableName);
    const executableInfo = await lstat(executable).catch(missingOnly);
    if (executableInfo === undefined) continue;
    if (!executableInfo.isFile() || executableInfo.isSymbolicLink()) throw new Error(`${frameworkId} compiled executable is unsafe`);
    matches.push(executable);
  }
  if (matches.length !== 1) throw new Error(`expected exactly one compiled ${frameworkId} executable`);
  const bytes = await readFile(matches[0]!);
  return { bytes, profileId: relative(buildRoot, matches[0]!).split(sep)[0]!, sha256: createHash("sha256").update(bytes).digest("hex") };
}

function validateClosedOptions(options: FrameworkWorkspaceStageOptions): void {
  if (!plainObject(options) || !exactKeys(options, [
    "candidateCommit", "cmakeHelper", "family", "frameworkId", "ownershipId", "platform",
    "preparedFrameworkRoots", "repositoryRoot", "stageRoot", "unityRunnerGenerator",
  ])) throw new Error("framework workspace staging options are not closed");
  if (!isAbsolute(options.repositoryRoot) || !isAbsolute(options.stageRoot)) throw new Error("framework staging paths must be absolute");
  if (!UUID.test(options.ownershipId)) throw new Error("framework staging ownership ID is invalid");
  if (!COMMIT.test(options.candidateCommit)) throw new Error("framework staging candidate is invalid");
  if (options.platform !== "linux" && options.platform !== "win32") throw new Error("framework platform is invalid");
  if (options.frameworkId !== "cpputest" && options.frameworkId !== "unity") throw new Error("framework ID is invalid");
  validateCombination(options.platform, options.family);
  if (!plainObject(options.preparedFrameworkRoots) || !exactKeys(options.preparedFrameworkRoots, ["cmock", "cpputest", "unity"])) {
    throw new Error("prepared framework roots are invalid");
  }
}

function validateCombination(platform: FrameworkPlatform, family: FrameworkToolchainFamily): void {
  if (platform !== "linux" && platform !== "win32") throw new Error("framework platform is invalid");
  if (!(PLATFORM_FAMILIES[platform] as readonly string[]).includes(family)) {
    throw new Error("framework toolchain is incompatible with the platform");
  }
}

async function validateInventory(rootValue: string, files: readonly string[], label: string): Promise<void> {
  const root = await requireDirectDirectory(rootValue, `${label} root`);
  const expectedFiles = new Set(files);
  const expectedDirectories = new Set(files.flatMap((name) => {
    const parts = name.split("/");
    return parts.slice(0, -1).map((_, index) => parts.slice(0, index + 1).join("/"));
  }));
  const found = new Set<string>();
  async function visit(directory: string, prefix: string): Promise<void> {
    const entries = await readdir(directory, { withFileTypes: true });
    for (const entry of entries) {
      const name = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      const path = join(directory, entry.name);
      const info = await lstat(path);
      if (info.isSymbolicLink()) throw new Error(`${label} contains a symbolic link`);
      if (info.isDirectory()) {
        if (!expectedDirectories.has(name)) throw new Error(`${label} contains an unlocked directory`);
        await visit(path, name);
      } else if (info.isFile() && expectedFiles.has(name)) {
        found.add(name);
      } else {
        throw new Error(`${label} contains an unlocked or unsafe entry`);
      }
    }
  }
  await visit(root, "");
  if (found.size !== expectedFiles.size) throw new Error(`${label} is missing a locked file`);
}

function workspaceConfiguration(options: FrameworkWorkspaceStageOptions, contract: MatrixNames): unknown {
  return {
    version: 2,
    projects: [{
      id: "framework-matrix",
      sourceDir: "source",
      // Use a verified Ninja profile for each family, including MSVC, avoiding
      // MSBuild's legacy path restrictions. Native host path limits still apply.
      fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
      // Leave the opaque CTest undeclared so the Service exercises its
      // fail-closed opaque fallback instead of probing it as the framework.
      tests: { containers: [contract.primary, contract.malformed].map((ctestName) => ({
        ctestName, framework: options.frameworkId,
      })) },
    }],
  };
}

function projectConfiguration(
  options: FrameworkWorkspaceStageOptions,
  roots: Readonly<Record<"cpputest" | "unity" | "cmock", string>>,
  cmakeHelper: string,
  unityRunnerGenerator: string,
): string {
  // Preset profiles have no verified toolchain binding and choose their own
  // binaryDir. Keep only fixture inputs here so Service fallback profiles own
  // compiler selection and service/data/build/<profile-id> output placement.
  // The producer stages this workspace under `.staging/<owner>` and the
  // publisher atomically moves it to the immutable runtime root. Absolute
  // paths would therefore point at the deleted staging tree for consumers.
  const workspaceInputs = options.platform === "win32"
    ? { cpputest: "../c", unity: "../u", cmock: "../m" }
    : {
        cpputest: "../.unit-test-ide/inputs/prepared/cpputest",
        unity: "../.unit-test-ide/inputs/prepared/unity",
        cmock: "../.unit-test-ide/inputs/prepared/cmock",
      };
  const helperInput = "../.unit-test-ide/inputs/cmake/UnitTestIDE.cmake";
  const generatorInput = `../.unit-test-ide/inputs/tools/unity-runner-generator${options.platform === "win32" ? ".exe" : ""}`;
  const variables: Record<string, string> = {
    UNIT_TEST_IDE_FRAMEWORK: options.frameworkId,
    UNIT_TEST_IDE_HELPER: helperInput,
    ...(options.frameworkId === "cpputest"
      ? { UNIT_TEST_IDE_CPPUTEST_ROOT: workspaceInputs.cpputest }
      : {
          UNIT_TEST_IDE_CMOCK_ROOT: workspaceInputs.cmock,
          UNIT_TEST_IDE_UNITY_ROOT: workspaceInputs.unity,
          UTIDE_UNITY_RUNNER_GENERATOR: generatorInput,
        }),
  };
  return [
    "cmake_minimum_required(VERSION 3.28)",
    // Locked framework trees include legacy CMake policy declarations. CMake
    // 4.x requires an explicit policy floor before entering those projects.
    "set(CMAKE_POLICY_VERSION_MINIMUM 3.5)",
    "project(unit_test_ide_framework_workspace LANGUAGES C CXX)",
    ...(options.platform === "win32" && (options.family === "msvc" || options.family === "clang-cl")
      ? [
          // Apply these after project() so compiler initialization cannot
          // replace the cache values. Keep the limit below the Windows MAX_PATH
          // boundary while allowing CMake to retain short relative source
          // paths; a lower value makes it encode the full C:\\utide path into
          // every object filename and recreates the compiler path failure.
          "set(CMAKE_OBJECT_PATH_MAX 240 CACHE STRING \"\" FORCE)",
          "set(CMAKE_TRY_COMPILE_CONFIGURATION Release CACHE STRING \"\" FORCE)",
          // F2 validates framework discovery and execution, not third-party
          // compiler debug databases. Embed debug information in each object
          // so CMake selects /Z7 instead of emitting a compiler /Fd database
          // path that can exceed Windows MAX_PATH after the consumer prefix
          // is applied.
          "set(CMAKE_MSVC_DEBUG_INFORMATION_FORMAT Embedded CACHE STRING \"\" FORCE)",
          // Do not append to CMake's default /debug /INCREMENTAL pair: clang-cl
          // may still open a linker PDB when /DEBUG:NONE appears after them.
          "set(CMAKE_EXE_LINKER_FLAGS_DEBUG \"/DEBUG:NONE\" CACHE STRING \"\" FORCE)",
          "set(CMAKE_SHARED_LINKER_FLAGS_DEBUG \"/DEBUG:NONE\" CACHE STRING \"\" FORCE)",
          "set(CMAKE_MODULE_LINKER_FLAGS_DEBUG \"/DEBUG:NONE\" CACHE STRING \"\" FORCE)",
        ]
      : []),
    ...(options.frameworkId === "cpputest"
      ? [
          // CppUTest 4.0 injects a forced MemoryLeakDetector header and
          // warning flags globally. The matrix validates framework discovery,
          // not the legacy allocator shim; keep this fixture independent of
          // CI include-path differences while preserving product diagnostics.
          "set(CPPUTEST_FLAGS OFF CACHE BOOL \"\" FORCE)",
          "set(MEMORY_LEAK_DETECTION OFF CACHE BOOL \"\" FORCE)",
        ]
      : []),
    ...Object.entries(variables).map(([name, value]) => {
      if (value.startsWith("../")) return `set(${name} "\${CMAKE_SOURCE_DIR}/${value}")`;
      return `set(${name} ${cmakeLiteral(value.split(sep).join("/"))})`;
    }),
    // The producer and consumer both run from the dedicated short physical
    // workspace on Windows. Keep the validated prepared roots in place; an
    // extra CMake file(COPY) pass is unnecessary and can fail when a consumer
    // workspace is recreated from the published runtime.
    ...(options.frameworkId === "cpputest"
      ? [
          // CppUTest's legacy headers include sibling files without the
          // `CppUTest/` prefix. Keep that sibling directory explicit so the
          // MSVC frontend resolves the same locked headers as clang-cl.
          "include_directories(${UNIT_TEST_IDE_CPPUTEST_ROOT}/include/CppUTest)",
          "if(WIN32)",
          "  include_directories(BEFORE \"${UNIT_TEST_IDE_CPPUTEST_ROOT}/include/CppUTest\")",
          "  include_directories(BEFORE \"${UNIT_TEST_IDE_CPPUTEST_ROOT}/include\")",
          "endif()",
        ]
      : []),
    "enable_testing()",
    "add_subdirectory(framework-matrix)",
    ...(options.platform === "win32" && (options.family === "msvc" || options.family === "clang-cl")
      ? [
          // CppUTest 4.0 unconditionally adds /WX to global MSVC flags. Keep
          // warning-as-error policy for product code, but prevent new
          // compiler-version warnings in this locked third-party fixture
          // from failing the producer build.
          "if(WIN32)",
          "  foreach(_utide_target CppUTest CppUTestExt CppUTestTests CppUTestExtTests phase9_cpputest phase9_cpputest_malformed phase9_matrix_opaque)",
          "    if(TARGET ${_utide_target})",
          "      target_compile_options(${_utide_target} PRIVATE /WX-)",
          "      target_compile_definitions(${_utide_target} PRIVATE CPPUTEST_MEM_LEAK_DETECTION_DISABLED)",
          "    endif()",
          "  endforeach()",
          "endif()",
        ]
      : []),
    "",
  ].join("\n");
}

function cmakeLiteral(value: string): string {
  // Bracket arguments preserve spaces, semicolons, quotes and ${...} literally.
  let equals = "";
  while (value.includes(`]${equals}]`)) equals += "=";
  return `[${equals}[${value}]${equals}]`;
}

interface MatrixNames { readonly primary: string; readonly malformed: string; readonly opaque: string }
function parseContract(bytes: Uint8Array, frameworkId: FrameworkId): MatrixNames {
  let value: unknown;
  try { value = JSON.parse(Buffer.from(bytes).toString("utf8")); }
  catch (error) { throw new Error("framework matrix contract is invalid", { cause: error }); }
  if (!plainObject(value) || !plainObject(value.frameworks) || !plainObject(value.frameworks[frameworkId])) {
    throw new Error("framework matrix contract is incomplete");
  }
  const framework = value.frameworks[frameworkId];
  const names = {
    primary: framework.primaryCTestName,
    malformed: framework.malformedCTestName,
    opaque: framework.opaqueCTestName,
  };
  if (Object.values(names).some((name) => typeof name !== "string" || name.length === 0 || name.includes("\0"))) {
    throw new Error("framework matrix contract container names are invalid");
  }
  return names as MatrixNames;
}

async function requireContainedDirectory(root: string, value: string, label: string): Promise<string> {
  if (!isAbsolute(value)) throw new Error(`${label} must be absolute`);
  const path = resolve(value);
  await requireDirectDirectory(path, label);
  requireContained(await realpath(root), await realpath(path), label);
  return path;
}

async function requireContainedFile(root: string, value: string, label: string): Promise<string> {
  if (!isAbsolute(value)) throw new Error(`${label} must be absolute`);
  const path = resolve(value);
  await requireDirectFile(path, label);
  const canonical = await realpath(path);
  requireContained(await realpath(root), canonical, label);
  return path;
}

async function requireDirectDirectory(path: string, label: string): Promise<string> {
  const info = await lstat(path).catch((error) => { throw new Error(`${label} is unavailable`, { cause: error }); });
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} is unsafe`);
  return realpath(path);
}

async function requireDirectFile(path: string, label: string): Promise<void> {
  const info = await lstat(path).catch((error) => { throw new Error(`${label} is unavailable`, { cause: error }); });
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`${label} is not a regular file`);
}

function requireContained(root: string, path: string, label: string): void {
  const child = relative(root, path);
  if (child === "" || child === ".." || child.startsWith(`..${sep}`) || isAbsolute(child)) {
    throw new Error(`${label} is outside the repository root`);
  }
}

async function createVerifiedStageAncestors(repositoryRoot: string, parent: string): Promise<void> {
  requireContained(repositoryRoot, parent, "staging ancestor");
  const canonicalRepositoryRoot = await realpath(repositoryRoot);
  const components = relative(repositoryRoot, parent).split(sep);
  let current = repositoryRoot;
  for (const component of components) {
    current = join(current, component);
    let info = await lstat(current).catch(missingOnly);
    if (info === undefined) {
      await mkdir(current, { recursive: false, mode: 0o700 });
      info = await lstat(current);
    }
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("staging ancestor is a symbolic link or unsafe");
    const canonical = await realpath(current);
    if (!samePath(canonical, current)) throw new Error("staging ancestor is not a direct directory");
    requireContained(canonicalRepositoryRoot, canonical, "staging ancestor");
  }
}

function samePath(left: string, right: string): boolean {
  const physical = (value: string): string => {
    try { return realpathSync.native(resolve(value)); }
    catch { return resolve(value); }
  };
  return process.platform === "win32"
    ? physical(left).toLowerCase() === physical(right).toLowerCase()
    : physical(left) === physical(right);
}

async function writeCanonical(path: string, value: unknown): Promise<void> {
  await writeFile(path, `${JSON.stringify(canonical(value), null, 2)}\n`, { flag: "wx", mode: 0o600 });
}

function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (plainObject(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  return value;
}

function plainObject(value: unknown): value is Record<string, any> {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function exactKeys(value: Record<string, any>, expected: readonly string[]): boolean {
  const actual = Reflect.ownKeys(value).sort();
  const wanted = [...expected].sort();
  return actual.length === wanted.length && actual.every((key, index) => typeof key === "string" && key === wanted[index]);
}

function missingOnly(error: unknown): undefined {
  if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
  throw error;
}
