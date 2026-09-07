import { createHash } from "node:crypto";
import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";

const DIGEST = /^[0-9a-f]{64}$/u;
const EXPECTED: Readonly<Record<LinuxFrameworkID, Readonly<{ version: string; license: string; url: string; filename: string; sha256: string; sourceDirectory: string }>>> = {
  cpputest: {
    version: "4.0",
    license: "BSD-3-Clause",
    url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz",
    filename: "cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7", sourceDirectory: "cpputest-4.0"
  },
  unity: {
    version: "2.6.1",
    license: "MIT",
    url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz",
    filename: "Unity-2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292", sourceDirectory: "Unity-2.6.1"
  }
};

export type LinuxFrameworkID = "cpputest" | "unity";

export interface LinuxFrameworkInputManifest {
  readonly schemaVersion: 1;
  readonly platform: "linux-x64";
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

export function validateLinuxFrameworkInputManifest(value: unknown): LinuxFrameworkInputManifest {
  const manifest = closedObject(value, ["schemaVersion", "platform", "frameworks"], "Linux framework input manifest");
  if (manifest.schemaVersion !== 1 || manifest.platform !== "linux-x64" || !Array.isArray(manifest.frameworks) || manifest.frameworks.length !== 2) {
    throw new Error("Linux framework input manifest has an invalid identity");
  }
  const observed = new Set<string>();
  for (const candidate of manifest.frameworks) {
    const item = closedObject(candidate, ["id", "version", "source", "license", "sourceDirectory"], "Linux framework input");
    if (item.id !== "cpputest" && item.id !== "unity") throw new Error("Linux framework input has an invalid ID");
    const expected = EXPECTED[item.id];
    const source = closedObject(item.source, ["filename", "url", "sha256"], "Linux framework input source");
    if (
      observed.has(item.id) || item.version !== expected.version || item.license !== expected.license ||
      source.url !== expected.url || source.filename !== expected.filename || source.sha256 !== expected.sha256 ||
      item.sourceDirectory !== expected.sourceDirectory || !safeFilename(source.filename) || typeof source.sha256 !== "string" || !DIGEST.test(source.sha256) || !safeDirectory(item.sourceDirectory)
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
  const helper = await regularFileWithin(repositoryRoot, options.helperPath, "UnitTestIDE helper");
  const generator = await regularFileWithin(repositoryRoot, options.generatorPath, "Unity runner generator");
  const roots = new Map<LinuxFrameworkID, string>();
  const trees = new Map<LinuxFrameworkID, string>();
  for (const framework of manifest.frameworks) {
    const archive = join(cacheRoot, `${framework.source.sha256}-${framework.source.filename}`);
    if (await digestFile(archive) !== framework.source.sha256) throw new Error(`Linux framework input archive digest mismatch: ${framework.id}`);
    const sourceDirectory = childDirectory(sourceRoot, framework.sourceDirectory, `${framework.id} source directory`);
    await requiredFrameworkFile(sourceDirectory, framework.id === "cpputest" ? "CMakeLists.txt" : "src/unity.c", framework.id);
    roots.set(framework.id, sourceDirectory);
    trees.set(framework.id, await directoryDigest(sourceDirectory));
  }
  const identityDigest = createHash("sha256").update(JSON.stringify({
    schemaVersion: manifest.schemaVersion,
    platform: manifest.platform,
    frameworks: [...manifest.frameworks].sort((left, right) => left.id.localeCompare(right.id)).map(({ id, version, source, license, sourceDirectory }) => ({ id, version, source, license, sourceDirectory, treeSha256: trees.get(id) })),
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
  const path = await realpath(resolve(value));
  if (relative(root, path).startsWith("..") || relative(root, path) === "") throw new Error(`Linux framework ${label} escapes repository identity`);
  const metadata = await lstat(path);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} must be a regular file`);
  return { path, digest: await digestFile(path) };
}

async function requiredFrameworkFile(root: string, leaf: string, label: string): Promise<void> {
  const metadata = await lstat(root);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} source directory is invalid`);
  await regularFileWithin(root, join(root, leaf), `${label} source marker`);
}

async function digestFile(path: string): Promise<string> {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function directoryDigest(root: string): Promise<string> {
  const hash = createHash("sha256");
  async function visit(directory: string, prefix: string): Promise<void> {
    for (const entry of (await readdir(directory, { withFileTypes: true })).sort((left, right) => left.name.localeCompare(right.name))) {
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
