import { createHash } from "node:crypto";
import { execFile as execFileCallback } from "node:child_process";
import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";
import { promisify } from "node:util";

const DIGEST = /^[0-9a-f]{64}$/u;
const REVISION = /^[0-9a-f]{40}$/u;
const execFile = promisify(execFileCallback);

export type FrameworkDependencyID = "cpputest" | "unity" | "cmock";
export type AdapterFrameworkID = "cpputest" | "unity";
export type LinuxFrameworkID = FrameworkDependencyID;
type LockedFramework = Readonly<{ version: string; tag: string; revision: string; url: string; filename: string; sha256: string; license: { spdx: string; path: string; sha256: string }; sourceDirectory: string; treeSha256: string }>;
const EXPECTED: Readonly<Record<FrameworkDependencyID, LockedFramework>> = {
  cpputest: { version: "4.0", tag: "v4.0", revision: "b9b841c56c524a10ccd40e88c3acaf9d5ec751c2", url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz", filename: "cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7", license: { spdx: "BSD-3-Clause", path: "COPYING", sha256: "d8fe282e4047197e1fbd6ef2527bde832a1514be6bd82fac7d1296ce184285c8" }, sourceDirectory: "cpputest-4.0", treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04" },
  unity: { version: "2.6.1", tag: "v2.6.1", revision: "cbcd08fa7de711053a3deec6339ee89cad5d2697", url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz", filename: "Unity-2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292", license: { spdx: "MIT", path: "LICENSE.txt", sha256: "907d9e859c6433703c0c183de3ddeaaf4baf3d517382f8f368b2c190fd2581d1" }, sourceDirectory: "Unity-2.6.1", treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae" },
  cmock: { version: "2.7.0", tag: "v2.7.0", revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af", url: "https://github.com/ThrowTheSwitch/CMock/archive/refs/tags/v2.7.0.tar.gz", filename: "CMock-2.7.0.tar.gz", sha256: "d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c", license: { spdx: "MIT", path: "LICENSE.txt", sha256: "f19bba29498b9405a86ab5fdc6bc58654fffb197603834e6d1423d583649b35c" }, sourceDirectory: "CMock-2.7.0", treeSha256: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3" }
};
const EXPECTED_FIXTURE_TOOLS = {
  cmakeHelper: { path: "sdk/cmake/UnitTestIDE.cmake", sha256: "a9e0ff8bfc676131f4812b69a7f063cf1a79a7b8d52d64845631613e5877f00b" },
  unityRunnerGenerator: { name: "unity-runner-generator", schemaVersion: 1, version: "1.0.0", runnerProtocol: "utide.runner.v1" },
  cmockGenerator: { frameworkId: "cmock", version: "2.7.0", entrypoint: "lib/cmock.rb", containerImage: "docker.io/library/ruby", containerTag: "3.3.6-bookworm", containerPlatform: "linux/amd64", containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556", generatedAtRuntime: false }
} as const;

export interface LinuxFrameworkInputManifest { readonly schemaVersion: 2; readonly platforms: readonly string[]; readonly fixtureTools: typeof EXPECTED_FIXTURE_TOOLS; readonly frameworks: readonly LinuxFrameworkInput[]; }
export interface LinuxFrameworkInput { readonly id: FrameworkDependencyID; readonly version: string; readonly tag: string; readonly revision: string; readonly source: { readonly filename: string; readonly url: string; readonly sha256: string }; readonly license: { readonly spdx: string; readonly path: string; readonly sha256: string }; readonly sourceDirectory: string; readonly treeSha256: string; }
export interface LinuxFrameworkInputBoundaryOptions { readonly manifest: LinuxFrameworkInputManifest; readonly manifestSha256: string; readonly cacheRoot: string; readonly sourceRoot: string; readonly helperPath: string; readonly generatorPath: string; readonly repositoryRoot?: string; readonly markStage?: (stage: string) => void; }
export interface LinuxFrameworkInputBoundary { readonly frameworks: readonly AdapterFrameworkID[]; readonly identityDigest: string; readonly environment: Readonly<Record<string, string>>; }
export interface ResolvedLinuxFrameworkTree { readonly id: FrameworkDependencyID; readonly treeSha256: string; }

export function validateLinuxFrameworkInputManifest(value: unknown): LinuxFrameworkInputManifest {
  const manifest = closedObject(value, ["schemaVersion", "platforms", "fixtureTools", "frameworks"], "Linux framework input manifest");
  if (manifest.schemaVersion !== 2 || JSON.stringify(manifest.platforms) !== JSON.stringify(["linux-x64", "windows-x64"]) || !Array.isArray(manifest.frameworks) || manifest.frameworks.length !== 3) throw new Error("Linux framework input manifest has an invalid identity");
  const tools = closedObject(manifest.fixtureTools, ["cmakeHelper", "unityRunnerGenerator", "cmockGenerator"], "Linux framework fixture tools");
  const helper = closedObject(tools.cmakeHelper, ["path", "sha256"], "Linux framework CMake helper");
  const generator = closedObject(tools.unityRunnerGenerator, ["name", "schemaVersion", "version", "runnerProtocol"], "Linux framework Unity generator");
  const cmock = closedObject(tools.cmockGenerator, ["frameworkId", "version", "entrypoint", "containerImage", "containerTag", "containerPlatform", "containerDigest", "generatedAtRuntime"], "Linux framework CMock generator");
  if (JSON.stringify(helper) !== JSON.stringify(EXPECTED_FIXTURE_TOOLS.cmakeHelper) || JSON.stringify(generator) !== JSON.stringify(EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator) || JSON.stringify(cmock) !== JSON.stringify(EXPECTED_FIXTURE_TOOLS.cmockGenerator)) throw new Error("Linux framework fixture tools are not locked");
  const observed = new Set<FrameworkDependencyID>();
  for (const candidate of manifest.frameworks) {
    const item = closedObject(candidate, ["id", "version", "tag", "revision", "source", "license", "sourceDirectory", "treeSha256"], "Linux framework input");
    if (item.id !== "cpputest" && item.id !== "unity" && item.id !== "cmock") throw new Error("Linux framework input has an invalid ID");
    const source = closedObject(item.source, ["filename", "url", "sha256"], "Linux framework input source");
    const license = closedObject(item.license, ["spdx", "path", "sha256"], "Linux framework input license");
    const expected = EXPECTED[item.id];
    if (observed.has(item.id) || item.version !== expected.version || item.tag !== expected.tag || item.revision !== expected.revision || !REVISION.test(item.revision as string) || source.filename !== expected.filename || source.url !== expected.url || source.sha256 !== expected.sha256 || !DIGEST.test(source.sha256 as string) || JSON.stringify(license) !== JSON.stringify(expected.license) || !DIGEST.test(license.sha256 as string) || item.sourceDirectory !== expected.sourceDirectory || item.treeSha256 !== expected.treeSha256 || !DIGEST.test(item.treeSha256 as string) || !safeFilename(source.filename) || !safeDirectory(item.sourceDirectory)) throw new Error("Linux framework input is not locked");
    observed.add(item.id);
  }
  if (observed.size !== 3 || !observed.has("cpputest") || !observed.has("unity") || !observed.has("cmock")) throw new Error("Linux framework input manifest omits a required framework");
  return value as LinuxFrameworkInputManifest;
}

export async function prepareLinuxFrameworkInputs(options: LinuxFrameworkInputBoundaryOptions): Promise<LinuxFrameworkInputBoundary> {
  const markStage = options.markStage ?? (() => {});
  markStage("framework-inputs:validate-manifest");
  const manifest = validateLinuxFrameworkInputManifest(options.manifest); if (!DIGEST.test(options.manifestSha256)) throw new Error("Linux framework manifest digest is required"); const cacheRoot = absoluteDirectory(options.cacheRoot, "cache root"); const sourceRoot = absoluteDirectory(options.sourceRoot, "source root"); const repositoryRoot = absoluteDirectory(options.repositoryRoot ?? resolve(import.meta.dirname, "../../.."), "repository root"); const roots = new Map<FrameworkDependencyID, string>();
  for (const framework of manifest.frameworks) { markStage(`framework-inputs:archive:${framework.id}`); const archive = join(cacheRoot, `${framework.source.sha256}-${framework.source.filename}`); if (await digestFile(archive) !== framework.source.sha256) throw new Error(`Linux framework input archive digest mismatch: ${framework.id}`); const source = childDirectory(sourceRoot, framework.sourceDirectory, `${framework.id} source directory`); await requiredFrameworkFile(source, marker(framework.id), framework.id); const license = await regularFileWithin(source, join(source, framework.license.path), `${framework.id} license`); if (license.digest !== framework.license.sha256) throw new Error(`Linux framework license digest mismatch: ${framework.id}`); roots.set(framework.id, source); }
  markStage("framework-inputs:helper-file");
  const helper = await regularFileWithin(repositoryRoot, options.helperPath, "UnitTestIDE helper");
  markStage("framework-inputs:generator-file");
  const generator = await regularFileWithin(repositoryRoot, options.generatorPath, "Unity runner generator");
  const expectedHelperPath = await realpath(resolve(repositoryRoot, manifest.fixtureTools.cmakeHelper.path));
  markStage("framework-inputs:helper-path");
  if (helper.path !== expectedHelperPath) throw new Error("Linux framework CMake helper path mismatch");
  markStage("framework-inputs:helper-digest");
  if (helper.digest !== manifest.fixtureTools.cmakeHelper.sha256) throw new Error(`Linux framework CMake helper digest mismatch: observed ${helper.digest}, expected ${manifest.fixtureTools.cmakeHelper.sha256}`);
  markStage("framework-inputs:generator-identity");
  await verifyUnityRunnerGenerator(generator.path, manifest.fixtureTools.unityRunnerGenerator);
  markStage("framework-inputs:resolved-manifest");
  const resolved = await readResolvedFrameworkTrees(sourceRoot, manifest, options.manifestSha256); markStage("framework-inputs:tree-digest"); const trees = await verifyResolvedFrameworkTrees(sourceRoot, manifest, resolved);
  const identityDigest = createHash("sha256").update(JSON.stringify({ schemaVersion: manifest.schemaVersion, platforms: manifest.platforms, fixtureTools: manifest.fixtureTools, frameworks: manifest.frameworks.map((framework) => ({ ...framework, treeSha256: trees.find((tree) => tree.id === framework.id)?.treeSha256 })), helperSha256: helper.digest, generatorSha256: generator.digest })).digest("hex");
  return { frameworks: ["cpputest", "unity"], identityDigest, environment: Object.freeze({ UNIT_TEST_IDE_TEST_CPPUTEST_ROOT: roots.get("cpputest")!, UNIT_TEST_IDE_TEST_UNITY_ROOT: roots.get("unity")!, UNIT_TEST_IDE_TEST_CMOCK_ROOT: roots.get("cmock")!, UNIT_TEST_IDE_TEST_CMAKE_HELPER: helper.path, UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR: generator.path }) };
}

export async function verifyResolvedFrameworkTrees(sourceRoot: string, manifest: LinuxFrameworkInputManifest, expected: readonly ResolvedLinuxFrameworkTree[] | undefined): Promise<readonly ResolvedLinuxFrameworkTree[]> {
  const root = absoluteDirectory(sourceRoot, "source root"); const actual: ResolvedLinuxFrameworkTree[] = [];
  for (const framework of validateLinuxFrameworkInputManifest(manifest).frameworks) { const source = childDirectory(root, framework.sourceDirectory, `${framework.id} source directory`); await requiredFrameworkFile(source, marker(framework.id), framework.id); actual.push({ id: framework.id, treeSha256: await directoryDigest(source) }); }
  actual.sort((a, b) => a.id.localeCompare(b.id)); if (expected !== undefined) { if (expected.length !== actual.length) throw new Error("Linux framework resolved tree identity is incomplete"); for (const tree of actual) { const locked = expected.find((candidate) => candidate.id === tree.id); if (!locked || !DIGEST.test(locked.treeSha256) || locked.treeSha256 !== tree.treeSha256) throw new Error(`Linux framework tree digest mismatch: ${tree.id}`); } } return actual;
}

export async function readResolvedFrameworkTrees(sourceRoot: string, manifest: LinuxFrameworkInputManifest, manifestSha256: string): Promise<readonly ResolvedLinuxFrameworkTree[]> {
  if (!DIGEST.test(manifestSha256)) throw new Error("Linux framework resolved manifest has an invalid identity");
  const path = join(sourceRoot, "manifest.resolved.json"); const metadata = await lstat(path); if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error("Linux framework resolved manifest is invalid"); const resolved = closedObject(JSON.parse(await readFile(path, "utf8")), ["schemaVersion", "manifestSha256", "platforms", "fixtureTools", "frameworks"], "Linux framework resolved manifest");
  if (resolved.schemaVersion !== 2 || JSON.stringify(resolved.platforms) !== JSON.stringify(manifest.platforms) || JSON.stringify(resolved.fixtureTools) !== JSON.stringify(manifest.fixtureTools) || !Array.isArray(resolved.frameworks) || resolved.frameworks.length !== 3 || resolved.manifestSha256 !== manifestSha256) throw new Error("Linux framework resolved manifest has an invalid identity");
  const result: ResolvedLinuxFrameworkTree[] = [];
  for (const item of resolved.frameworks) { const candidate = closedObject(item, ["id", "version", "tag", "revision", "source", "license", "sourceDirectory", "treeSha256"], "Linux framework resolved input"); if (candidate.id !== "cpputest" && candidate.id !== "unity" && candidate.id !== "cmock") throw new Error("Linux framework resolved input has an invalid ID"); const source = closedObject(candidate.source, ["filename", "sha256"], "Linux framework resolved input source"); const locked = manifest.frameworks.find((framework) => framework.id === candidate.id); if (!locked || candidate.version !== locked.version || candidate.tag !== locked.tag || candidate.revision !== locked.revision || candidate.sourceDirectory !== locked.sourceDirectory || source.filename !== locked.source.filename || source.sha256 !== locked.source.sha256 || JSON.stringify(candidate.license) !== JSON.stringify(locked.license) || candidate.treeSha256 !== locked.treeSha256) throw new Error("Linux framework resolved input is not locked"); result.push({ id: candidate.id, treeSha256: candidate.treeSha256 as string }); }
  if (new Set(result.map((item) => item.id)).size !== 3) throw new Error("Linux framework resolved manifest has duplicate inputs"); return result;
}

function marker(id: FrameworkDependencyID): string { return id === "cpputest" ? "CMakeLists.txt" : id === "unity" ? "src/unity.c" : "lib/cmock.rb"; }
function closedObject(value: unknown, expected: readonly string[], label: string): Record<string, unknown> { if (!value || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) throw new Error(`${label} must be an object`); const actual = Object.keys(value).sort(); const wanted = [...expected].sort(); if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) throw new Error(`${label} has unexpected fields`); return value as Record<string, unknown>; }
function safeFilename(value: unknown): value is string { return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value); }
function safeDirectory(value: unknown): value is string { return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]*$/u.test(value) && !value.includes("..") && !value.includes("/") && !value.includes("\\"); }
function absoluteDirectory(value: string, label: string): string { if (typeof value !== "string" || value.includes("\0") || !isAbsolute(value)) throw new Error(`Linux framework ${label} must be an absolute path`); return resolve(value); }
function childDirectory(root: string, child: string, label: string): string { const path = resolve(root, child); if (relative(root, path).startsWith("..") || relative(root, path) === "") throw new Error(`Linux framework ${label} escapes its root`); return path; }
async function regularFileWithin(root: string, value: string, label: string): Promise<{ path: string; digest: string }> { if (typeof value !== "string" || value.includes("\0") || !isAbsolute(value)) throw new Error(`Linux framework ${label} must be an absolute path`); const canonicalRoot = await realpath(root); const path = await realpath(resolve(value)); if (relative(canonicalRoot, path).startsWith("..") || relative(canonicalRoot, path) === "") throw new Error(`Linux framework ${label} escapes repository identity`); const metadata = await lstat(path); if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} must be a regular file`); return { path, digest: await digestFile(path) }; }
async function requiredFrameworkFile(root: string, leaf: string, label: string): Promise<void> { const metadata = await lstat(root); if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw new Error(`Linux framework ${label} source directory is invalid`); await regularFileWithin(root, join(root, leaf), `${label} source marker`); }
async function verifyUnityRunnerGenerator(path: string, expected: typeof EXPECTED_FIXTURE_TOOLS.unityRunnerGenerator): Promise<void> { let decoded: unknown; try { decoded = JSON.parse((await execFile(path, ["--version=json-v1"], { encoding: "utf8", shell: false, windowsHide: true, timeout: 10_000, maxBuffer: 8 * 1024 })).stdout); } catch (error) { throw new Error("Linux framework Unity generator identity probe failed", { cause: error }); } const actual = closedObject(decoded, ["schemaVersion", "name", "version", "runnerProtocol"], "Linux framework Unity generator identity"); if (actual.schemaVersion !== expected.schemaVersion || actual.name !== expected.name || actual.version !== expected.version || actual.runnerProtocol !== expected.runnerProtocol) throw new Error("Linux framework Unity generator identity mismatch"); }
async function digestFile(path: string): Promise<string> { return createHash("sha256").update(await readFile(path)).digest("hex"); }
async function directoryDigest(root: string): Promise<string> { const hash = createHash("sha256"); async function visit(directory: string, prefix: string): Promise<void> { for (const entry of (await readdir(directory, { withFileTypes: true })).sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0)) { const path = join(directory, entry.name); const name = prefix ? `${prefix}/${entry.name}` : entry.name; const metadata = await lstat(path); if (metadata.isSymbolicLink() || (!metadata.isDirectory() && !metadata.isFile())) throw new Error("Linux framework source tree contains an unsafe entry"); hash.update(`${metadata.isDirectory() ? "d" : "f"}:${name}\0`); if (metadata.isDirectory()) await visit(path, name); else hash.update(await readFile(path)); } } await visit(root, ""); return hash.digest("hex"); }
