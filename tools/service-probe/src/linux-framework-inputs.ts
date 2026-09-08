import { createHash } from "node:crypto";
import { execFile as execFileCallback } from "node:child_process";
import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";
import { promisify } from "node:util";

const DIGEST = /^[0-9a-f]{64}$/u;
const execFile = promisify(execFileCallback);
const EXPECTED: Readonly<Record<LinuxFrameworkID, Readonly<{ version: string; license: string; url: string; filename: string; sha256: string; sourceDirectory: string; treeSha256: string }>>> = {
  cpputest: {
    version: "4.0",
    license: "BSD-3-Clause",
    url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz",
    filename: "cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7", sourceDirectory: "cpputest-4.0", treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
  },
  unity: {
    version: "2.6.1",
    license: "MIT",
    url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz",
    filename: "Unity-2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292", sourceDirectory: "Unity-2.6.1", treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae"
  }
};
const EXPECTED_FIXTURE_TOOLS = {
  cmakeHelper: { path: "sdk/cmake/UnitTestIDE.cmake", sha256: "2297b37584d134b901f0da0dea5d60d67853a496dbf18874c220643ffd2cd2da" },
  unityRunnerGenerator: { name: "unity-runner-generator", schemaVersion: 1, version: "1.0.0", runnerProtocol: "utide.runner.v1" }
} as const;

export type LinuxFrameworkID = "cpputest" | "unity";

export interface LinuxFrameworkInputManifest {
  readonly schemaVersion: 1;
  readonly platform: "linux-x64";
  readonly fixtureTools: typeof EXPECTED_FIXTURE_TOOLS;
  readonly frameworks: readonly LinuxFrameworkInput[];
}

export interface LinuxFrameworkInput {
  readonly id: LinuxFrameworkID;
  readonly version: string;
  readonly source: {
    readonly filename: string;
    readonly url: string;
    readonly sha256: string;
  };
  readonly license: string;
  readonly sourceDirectory: string;
  readonly treeSha256: string;
}

export interface LinuxFrameworkInputBoundaryOptions {
  readonly manifest: LinuxFrameworkInputManifest;
  readonly cacheRoot: string;
  readonly sourceRoot: string;
  readonly helperPath: string;
  readonly generatorPath: string;
  readonly repositoryRoot?: string;
}

export interface LinuxFrameworkInputBoundary {
  readonly frameworks: readonly LinuxFrameworkID[];
  readonly identityDigest: string;
  /** Test-only process environment; it is never placed in Workspace config. */
  readonly environment: Readonly<Record<string, string>>;
}

export interface ResolvedLinuxFrameworkTree {
  readonly id: LinuxFrameworkID;
  readonly treeSha256: string;
}

export function validateLinuxFrameworkInputManifest(value: unknown): LinuxFrameworkInputManifest {
  const manifest = closedObject(value, ["schemaVersion", "platform", "fixtureTools", "frameworks"], "Linux framework input manifest");
  if (manifest.schemaVersion !== 1 || manifest.platform !== "linux-x64" || !Array.isArray(manifest.frameworks) || manifest.frameworks.length !== 2) {
    throw new Error("Linux framework input manifest has an invalid identity");
  }
  const fixtureTools = closedObject(manifest.fixtureTools, ["cmakeHelper", "unityRunnerGenerator"], "Linux framework fixture tools");
  const helper = closedObject(fixtureTools.cmakeHelper, ["path", "sha256"], "Linux framework CMake helper");
  const generator = closedObject(fixtureTools.unityRunnerGenerator, ["name", "schemaVersion", "version", "runnerProtocol"], "Linux framework Unity generator");
  if (helper.path !== EXPECTED_FIXTURE_TOOLS.cmakeHelper.path || helper.sha256 !== EXPECTED_FIXTURE_TOOLS.cmakeHelper.sha256 || generator.name !== EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator.name || generator.schemaVersion !== EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator.schemaVersion || generator.version !== EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator.version || generator.runnerProtocol !== EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator.runnerProtocol) throw new Error("Linux framework fixture tools are not locked");
  const observed = new Set<string>();
  for (const candidate of manifest.frameworks) {
    const item = closedObject(candidate, ["id", "version", "source", "license", "sourceDirectory", "treeSha256"], "Linux framework input");
    if (item.id !== "cpputest" && item.id !== "unity") throw new Error("Linux framework input has an invalid ID");
    const expected = EXPECTED[item.id];
    const source = closedObject(item.source, ["filename", "url", "sha256"], "Linux framework input source");
    if (
      observed.has(item.id) || item.version !== expected.version || item.license !== expected.license ||
      source.url !== expected.url || source.filename !== expected.filename || source.sha256 !== expected.sha256 ||
      item.sourceDirectory !== expected.sourceDirectory || item.treeSha256 !== expected.treeSha256 || !safeFilename(source.filename) || typeof source.sha256 !== "string" || !DIGEST.test(source.sha256) || typeof item.treeSha256 !== "string" || !DIGEST.test(item.treeSha256) || !safeDirectory(item.sourceDirectory)
    ) {
      throw new Error("Linux framework input is not locked");
    }
    observed.add(item.id);
  }
  if (!observed.has("cpputest") || !observed.has("unity")) throw new Error("Linux framework input manifest omits a required framework");
  return value as LinuxFrameworkInputManifest;
}

export async function prepareLinuxFrameworkInputs(options: LinuxFrameworkInputBoundaryOptions): Promise<LinuxFrameworkInputBoundary> {
  const manifest = validateLinuxFrameworkInputManifest(options.manifest);
  const cacheRoot = absoluteDirectory(options.cacheRoot, "cache root");
  const sourceRoot = absoluteDirectory(options.sourceRoot, "source root");
  const repositoryRoot = absoluteDirectory(options.repositoryRoot ?? resolve(import.meta.dirname, "../../.."), "repository root");
  const roots = new Map<LinuxFrameworkID, string>();
  for (const framework of manifest.frameworks) {
    const archive = join(cacheRoot, `${framework.source.sha256}-${framework.source.filename}`);
    if (await digestFile(archive) !== framework.source.sha256) throw new Error(`Linux framework input archive digest mismatch: ${framework.id}`);
    const sourceDirectory = childDirectory(sourceRoot, framework.sourceDirectory, `${framework.id} source directory`);
    await requiredFrameworkFile(sourceDirectory, framework.id === "cpputest" ? "CMakeLists.txt" : "src/unity.c", framework.id);
    roots.set(framework.id, sourceDirectory);
  }
  const helper = await regularFileWithin(repositoryRoot, options.helperPath, "UnitTestIDE helper");
  const generator = await regularFileWithin(repositoryRoot, options.generatorPath, "Unity runner generator");
  const expectedHelperPath = await realpath(resolve(repositoryRoot, manifest.fixtureTools.cmakeHelper.path));
  if (helper.path !== expectedHelperPath || helper.digest !== manifest.fixtureTools.cmakeHelper.sha256) throw new Error("Linux framework CMake helper digest mismatch");
  await verifyUnityRunnerGenerator(generator.path, manifest.fixtureTools.unityRunnerGenerator);
  const resolved = await readResolvedFrameworkTrees(sourceRoot, manifest);
  const trees = await verifyResolvedFrameworkTrees(sourceRoot, manifest, resolved);
  const identityDigest = createHash("sha256").update(JSON.stringify({
    schemaVersion: manifest.schemaVersion,
    platform: manifest.platform,
    fixtureTools: manifest.fixtureTools,
    frameworks: [...manifest.frameworks].sort((left, right) => left.id.localeCompare(right.id)).map(({ id, version, source, license, sourceDirectory }) => ({ id, version, source, license, sourceDirectory, treeSha256: trees.find((tree) => tree.id === id)?.treeSha256 })),
    helperSha256: helper.digest,
    generatorSha256: generator.digest
  })).digest("hex");
  return {
    frameworks: ["cpputest", "unity"],
    identityDigest,
    environment: Object.freeze({
      UNIT_TEST_IDE_TEST_CPPUTEST_ROOT: roots.get("cpputest")!,
      UNIT_TEST_IDE_TEST_UNITY_ROOT: roots.get("unity")!,
      UNIT_TEST_IDE_TEST_CMAKE_HELPER: helper.path,
      UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR: generator.path
    })
  };
}

/** Verifies recursively measured source trees against bootstrap-published identities. */
export async function verifyResolvedFrameworkTrees(
  sourceRoot: string,
  manifest: LinuxFrameworkInputManifest,
  expected: readonly ResolvedLinuxFrameworkTree[] | undefined
): Promise<readonly ResolvedLinuxFrameworkTree[]> {
  const root = absoluteDirectory(sourceRoot, "source root");
  const actual: ResolvedLinuxFrameworkTree[] = [];
  for (const framework of validateLinuxFrameworkInputManifest(manifest).frameworks) {
    const sourceDirectory = childDirectory(root, framework.sourceDirectory, `${framework.id} source directory`);
    await requiredFrameworkFile(sourceDirectory, framework.id === "cpputest" ? "CMakeLists.txt" : "src/unity.c", framework.id);
    actual.push({ id: framework.id, treeSha256: await directoryDigest(sourceDirectory) });
  }
  actual.sort((left, right) => left.id.localeCompare(right.id));
  if (expected !== undefined) {
    if (expected.length !== actual.length) throw new Error("Linux framework resolved tree identity is incomplete");
    for (const tree of actual) {
      const locked = expected.find((candidate) => candidate.id === tree.id);
      if (locked === undefined || !DIGEST.test(locked.treeSha256) || locked.treeSha256 !== tree.treeSha256) throw new Error(`Linux framework tree digest mismatch: ${tree.id}`);
    }
  }
  return actual;
}

async function readResolvedFrameworkTrees(sourceRoot: string, manifest: LinuxFrameworkInputManifest): Promise<readonly ResolvedLinuxFrameworkTree[]> {
  const path = join(sourceRoot, "manifest.resolved.json");
  const metadata = await lstat(path);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error("Linux framework resolved manifest is invalid");
  const resolved = closedObject(JSON.parse(await readFile(path, "utf8")), ["schemaVersion", "platform", "fixtureTools", "frameworks"], "Linux framework resolved manifest");
  if (resolved.schemaVersion !== 1 || resolved.platform !== "linux-x64" || !Array.isArray(resolved.frameworks) || resolved.frameworks.length !== 2) throw new Error("Linux framework resolved manifest has an invalid identity");
  if (JSON.stringify(resolved.fixtureTools) !== JSON.stringify(manifest.fixtureTools)) throw new Error("Linux framework resolved fixture tools are not locked");
  const result: ResolvedLinuxFrameworkTree[] = [];
  for (const item of resolved.frameworks) {
    const candidate = closedObject(item, ["id", "version", "source", "license", "sourceDirectory", "treeSha256"], "Linux framework resolved input");
    if (candidate.id !== "cpputest" && candidate.id !== "unity") throw new Error("Linux framework resolved input has an invalid ID");
    const locked = manifest.frameworks.find((framework) => framework.id === candidate.id);
    const source = closedObject(candidate.source, ["filename", "sha256"], "Linux framework resolved input source");
    if (!locked || candidate.version !== locked.version || candidate.license !== locked.license || candidate.sourceDirectory !== locked.sourceDirectory || source.filename !== locked.source.filename || source.sha256 !== locked.source.sha256 || candidate.treeSha256 !== locked.treeSha256 || typeof candidate.treeSha256 !== "string" || !DIGEST.test(candidate.treeSha256)) throw new Error("Linux framework resolved input is not locked");
    result.push({ id: candidate.id, treeSha256: candidate.treeSha256 });
  }
  if (new Set(result.map((item) => item.id)).size !== 2) throw new Error("Linux framework resolved manifest has duplicate inputs");
  return result;
}

function closedObject(value: unknown, expected: readonly string[], label: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) throw new Error(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) throw new Error(`${label} has unexpected fields`);
  return value as Record<string, unknown>;
}

function safeFilename(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value);
}

function safeDirectory(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value) && !value.includes("..") && !value.includes("/") && !value.includes("\\");
}

function absoluteDirectory(value: string, label: string): string {
  if (typeof value !== "string" || value.includes("\0") || !isAbsolute(value)) throw new Error(`Linux framework ${label} must be an absolute path`);
  return resolve(value);
}

function childDirectory(root: string, child: string, label: string): string {
  const path = resolve(root, child);
  if (relative(root, path).startsWith("..") || relative(root, path) === "") throw new Error(`Linux framework ${label} escapes its root`);
  return path;
}

async function regularFileWithin(root: string, value: string, label: string): Promise<{ path: string; digest: string }> {
  if (typeof value !== "string" || value.includes("\0") || !isAbsolute(value)) throw new Error(`Linux framework ${label} must be an absolute path`);
  const canonicalRoot = await realpath(root);
  const path = await realpath(resolve(value));
  if (relative(canonicalRoot, path).startsWith("..") || relative(canonicalRoot, path) === "") throw new Error(`Linux framework ${label} escapes repository identity`);
  const metadata = await lstat(path);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} must be a regular file`);
  return { path, digest: await digestFile(path) };
}

async function requiredFrameworkFile(root: string, leaf: string, label: string): Promise<void> {
  const metadata = await lstat(root);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} source directory is invalid`);
  await regularFileWithin(root, join(root, leaf), `${label} source marker`);
}

async function verifyUnityRunnerGenerator(path: string, expected: typeof EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator): Promise<void> {
  let decoded: unknown;
  try {
    const { stdout } = await execFile(path, ["--version=json-v1"], { encoding: "utf8", shell: false, windowsHide: true, timeout: 10_000, maxBuffer: 8 * 1024 });
    decoded = JSON.parse(stdout);
  } catch (error) {
    throw new Error("Linux framework Unity generator identity probe failed", { cause: error });
  }
  const actual = closedObject(decoded, ["schemaVersion", "name", "version", "runnerProtocol"], "Linux framework Unity generator identity");
  if (actual.schemaVersion !== expected.schemaVersion || actual.name !== expected.name || actual.version !== expected.version || actual.runnerProtocol !== expected.runnerProtocol) throw new Error("Linux framework Unity generator identity mismatch");
}

async function digestFile(path: string): Promise<string> {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function directoryDigest(root: string): Promise<string> {
  const hash = createHash("sha256");
  async function visit(directory: string, prefix: string): Promise<void> {
    for (const entry of (await readdir(directory, { withFileTypes: true })).sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0)) {
      const path = join(directory, entry.name);
      const relativeName = prefix ? `${prefix}/${entry.name}` : entry.name;
      const metadata = await lstat(path);
      if (metadata.isSymbolicLink() || (!metadata.isDirectory() && !metadata.isFile())) throw new Error("Linux framework source tree contains an unsafe entry");
      hash.update(`${metadata.isDirectory() ? "d" : "f"}:${relativeName}\0`);
      if (metadata.isDirectory()) await visit(path, relativeName);
      else hash.update(await readFile(path));
    }
  }
  await visit(root, "");
  return hash.digest("hex");
}
