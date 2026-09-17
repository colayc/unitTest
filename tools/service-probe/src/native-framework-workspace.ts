import { createHash } from "node:crypto";
import { copyFile, lstat, mkdir, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import type { FrameworkId, FrameworkPlatform, FrameworkToolchainFamily } from "./native-framework-report.js";

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
  const repositoryRoot = await requireDirectDirectory(resolve(options.repositoryRoot), "repository root");
  const stageRoot = resolve(options.stageRoot);
  requireContained(repositoryRoot, stageRoot, "owned staging root");
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

  await mkdir(dirname(stageRoot), { recursive: true, mode: 0o700 });
  const parent = await realpath(dirname(stageRoot));
  requireContained(repositoryRoot, parent, "owned staging parent");
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
    await mkdir(join(workspaceRoot, ".unit-test-ide"), { recursive: true, mode: 0o700 });
    await writeCanonical(join(workspaceRoot, ".unit-test-ide", "workspace.json"), workspaceConfiguration(options, contract));
    await writeCanonical(join(matrixDestination, "CMakePresets.json"), presetConfiguration(
      options, preparedFrameworkRoots, cmakeHelper, unityRunnerGenerator,
    ));
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

export async function hashCompiledFrameworkExecutable(
  workRoot: string,
  platform: FrameworkPlatform,
  family: FrameworkToolchainFamily,
  frameworkId: FrameworkId,
): Promise<string> {
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
  return createHash("sha256").update(await readFile(matches[0]!)).digest("hex");
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
      sourceDir: "source/framework-matrix",
      fallback: { configurations: ["Debug"], preferredGenerator: generator(options.family) },
      tests: { containers: [contract.primary, contract.malformed, contract.opaque].map((ctestName) => ({
        ctestName, framework: options.frameworkId,
      })) },
    }],
  };
}

function presetConfiguration(
  options: FrameworkWorkspaceStageOptions,
  roots: Readonly<Record<"cpputest" | "unity" | "cmock", string>>,
  cmakeHelper: string,
  unityRunnerGenerator: string,
): unknown {
  const cacheVariables: Record<string, string> = {
    CMAKE_BUILD_TYPE: "Debug",
    UNIT_TEST_IDE_FRAMEWORK: options.frameworkId,
    UNIT_TEST_IDE_HELPER: cmakeHelper,
    ...(options.frameworkId === "cpputest"
      ? { UNIT_TEST_IDE_CPPUTEST_ROOT: roots.cpputest }
      : {
          UNIT_TEST_IDE_CMOCK_ROOT: roots.cmock,
          UNIT_TEST_IDE_UNITY_ROOT: roots.unity,
          UTIDE_UNITY_RUNNER_GENERATOR: unityRunnerGenerator,
        }),
    ...compilerVariables(options.family),
  };
  return {
    version: 6,
    configurePresets: [{
      name: `${options.family}-debug`,
      displayName: `${options.family} Debug`,
      generator: generator(options.family),
      binaryDir: "${sourceDir}/build/${presetName}",
      cacheVariables,
      ...(options.family === "msvc" ? { architecture: "x64" } : {}),
    }],
    buildPresets: [{ name: `${options.family}-debug`, configurePreset: `${options.family}-debug`, configuration: "Debug" }],
  };
}

function compilerVariables(family: FrameworkToolchainFamily): Record<string, string> {
  if (family === "gcc") return { CMAKE_C_COMPILER: "gcc", CMAKE_CXX_COMPILER: "g++" };
  if (family === "clang") return { CMAKE_C_COMPILER: "clang", CMAKE_CXX_COMPILER: "clang++" };
  if (family === "clang-cl") return { CMAKE_C_COMPILER: "clang-cl", CMAKE_CXX_COMPILER: "clang-cl" };
  return {};
}

function generator(family: FrameworkToolchainFamily): string {
  return family === "msvc" ? "Visual Studio 17 2022" : "Ninja";
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
  const path = await requireDirectDirectory(resolve(value), label);
  requireContained(root, path, label);
  return path;
}

async function requireContainedFile(root: string, value: string, label: string): Promise<string> {
  if (!isAbsolute(value)) throw new Error(`${label} must be absolute`);
  const path = resolve(value);
  await requireDirectFile(path, label);
  const canonical = await realpath(path);
  if (canonical !== path) throw new Error(`${label} is not a direct regular file`);
  requireContained(root, canonical, label);
  return canonical;
}

async function requireDirectDirectory(path: string, label: string): Promise<string> {
  const info = await lstat(path).catch((error) => { throw new Error(`${label} is unavailable`, { cause: error }); });
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} is unsafe`);
  const canonical = await realpath(path);
  if (canonical !== path) throw new Error(`${label} is not a direct directory`);
  return canonical;
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
