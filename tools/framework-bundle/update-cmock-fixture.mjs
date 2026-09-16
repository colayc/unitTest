import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { lstat, mkdir, open, readdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { aggregateOutputDigest, validateCMockGeneration } from "./cmock-provenance.mjs";
import { frameworkFailure, readFrameworkManifest } from "./manifest.mjs";
import { verifyPreparedFrameworkBundle } from "./prepare.mjs";

const execFile = promisify(execFileCallback);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const outputNames = Object.freeze(["MockDependency.c", "MockDependency.h"]);
const image = "docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556";
const configPath = "testdata/frameworks/unity/cmock.yml";
const headerPath = "testdata/frameworks/unity/include/Dependency.h";
const outputPath = "testdata/frameworks/unity/mocks";
const configBytes = Buffer.from("---\n:cmock:\n  :mock_path: /out\n  :mock_prefix: Mock\n  :mock_suffix: ''\n  :plugins: []\n  :fail_on_unexpected_calls: true\n  :when_no_prototypes: :error\n  :verbosity: 0\n");
const headerBytes = Buffer.from("#ifndef UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\n#define UNIT_TEST_IDE_PHASE9_DEPENDENCY_H\n\nint Dependency_Read(int channel);\n\n#endif\n");
const timeout = 5 * 60 * 1000;

function failure(code, message, cause) { return frameworkFailure(code, message, cause); }
function sha256(bytes) { return createHash("sha256").update(bytes).digest("hex"); }
function nonce(prefix) { return `${prefix}${process.pid}-${randomBytes(10).toString("hex")}`; }
function mount(source, destination, readonly) { return `type=bind,src=${resolve(source)},dst=${destination}${readonly ? ",readonly" : ""}`; }
function dockerExecutable() { return process.platform === "win32" ? "docker.exe" : "docker"; }

export function buildDockerArguments(input) {
  if (!input || typeof input !== "object" || [input.cmockRoot, input.unityRoot, input.fixtureRoot, input.outputRoot].some((value) => typeof value !== "string" || value.length === 0)) throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", "generator paths are invalid");
  return [
    "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64",
    "--mount", mount(input.cmockRoot, "/cmock", true),
    "--mount", mount(input.unityRoot, "/cmock/vendor/unity", true),
    "--mount", mount(input.fixtureRoot, "/fixture", true),
    "--mount", mount(input.outputRoot, "/out", false),
    image, "ruby", "/cmock/lib/cmock.rb", "-o/fixture/cmock.yml", "/fixture/include/Dependency.h"
  ];
}

async function regularDirectory(path, label) {
  let stat;
  try { stat = await lstat(path); } catch (error) { throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", `${label} is missing`, error); }
  if (!stat.isDirectory() || stat.isSymbolicLink()) throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", `${label} is unsafe`);
}
async function readFixedFile(path, expected, label) {
  let stat, bytes;
  try { stat = await lstat(path); bytes = await readFile(path); } catch (error) { throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `${label} is missing`, error); }
  if (!stat.isFile() || stat.isSymbolicLink() || !bytes.equals(expected)) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `${label} differs from the fixed input`);
  return bytes;
}
function assertGeneratedBytes(path, bytes) {
  let text;
  try { text = new TextDecoder("utf-8", { fatal: true }).decode(bytes); } catch (error) { throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output is not UTF-8: ${path}`, error); }
  if (text.includes("\r")) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output uses CRLF: ${path}`);
  if (/(?:[A-Za-z]:[\\/][^\s"')]+|(?:^|[\s"'(=])\/(?:[A-Za-z0-9_.~-]+\/)+[A-Za-z0-9_.~-]+)/mu.test(text)) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output contains an absolute path: ${path}`);
  if (/Generated on|\b20\d\d-\d\d-\d\d(?:T|\s)\d\d:\d\d/u.test(text)) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output contains a timestamp: ${path}`);
}
async function readClosedOutput(root) {
  let entries;
  try { entries = await readdir(root, { withFileTypes: true }); } catch (error) { throw failure("CMOCK_GENERATION_OUTPUT_INVALID", "generator output directory is missing", error); }
  if (entries.length !== outputNames.length || entries.some((entry) => !outputNames.includes(entry.name) || !entry.isFile() || entry.isSymbolicLink())) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", "generator output is not the closed file set");
  const files = [];
  for (const name of outputNames) {
    const path = join(root, name);
    let stat, bytes;
    try { stat = await lstat(path); bytes = await readFile(path); } catch (error) { throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output is missing: ${name}`, error); }
    if (!stat.isFile() || stat.isSymbolicLink()) throw failure("CMOCK_GENERATION_OUTPUT_INVALID", `generated output is unsafe: ${name}`);
    assertGeneratedBytes(name, bytes);
    files.push({ path: name, bytes });
  }
  return files;
}
function sameFiles(left, right) { return left.length === right.length && left.every((file, index) => file.path === right[index]?.path && file.bytes.equals(right[index].bytes)); }
function provenance(manifest, manifestSha256, configuration, input, files) {
  const cmock = manifest.frameworks.find((framework) => framework.id === "cmock");
  const generator = manifest.fixtureTools.cmockGenerator;
  return {
    schemaVersion: 1,
    cmock: { version: cmock.version, tag: cmock.tag, revision: cmock.revision },
    generator: { entrypoint: generator.entrypoint, version: generator.version, containerImage: generator.containerImage, containerPlatform: generator.containerPlatform, containerDigest: generator.containerDigest },
    configuration: { path: configPath, sha256: sha256(configuration) },
    input: { path: headerPath, sha256: sha256(input) },
    outputs: files.map((file) => ({ path: file.path, sha256: sha256(file.bytes) })),
    outputSha256: aggregateOutputDigest(files),
    frameworkManifestSha256: manifestSha256,
    generatedAtRuntime: false
  };
}
async function runGenerator(input, operations) {
  const args = buildDockerArguments(input);
  try {
    if (operations.runGenerator) await operations.runGenerator({ command: dockerExecutable(), arguments: args, outputRoot: input.outputRoot, timeout });
    else await execFile(dockerExecutable(), args, { shell: false, windowsHide: true, timeout, maxBuffer: 8 * 1024 * 1024, env: { ...process.env, LANG: "C", LC_ALL: "C" } });
  } catch (error) { throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", "locked generator execution failed", error); }
}
async function publish(target, stage, stagingRoot, operations) {
  const backup = join(dirname(target), nonce(".cmock-backup-"));
  let moved = false;
  try {
    try { await rename(target, backup); moved = true; } catch (error) { if (error?.code !== "ENOENT") throw error; }
    try { await rename(stage, target); } catch (error) {
      if (moved) await rename(backup, target);
      throw error;
    }
    try { await (operations.cleanupBackup ?? ((path) => rm(path, { recursive: true, force: true })))(backup); } catch { /* Publication has committed; stale backup is safe for later cleanup. */ }
  } catch (error) { throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", "cannot atomically publish generated fixture", error); }
  finally { try { await rm(stagingRoot, { recursive: true, force: true }); } catch { /* The committed target remains authoritative. */ } }
}

async function acquireFixtureLock(target) {
  const path = `${target}.update.lock`;
  try { return { path, handle: await open(path, "wx", 0o600) }; }
  catch (error) { throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", error?.code === "EEXIST" ? "another CMock fixture update is active" : "cannot acquire fixture update lock", error); }
}
async function releaseFixtureLock(lock) {
  try { await lock.handle.close(); } catch { /* A closed handle does not invalidate the published fixture. */ }
  try { await rm(lock.path, { force: true }); } catch { /* A stale lock fails closed on the next invocation. */ }
}

export async function updateCMockFixture(options = {}) {
  const root = resolve(options.repositoryRoot ?? repositoryRoot);
  const operations = options.operations ?? {};
  const target = join(root, outputPath);
  const lock = await acquireFixtureLock(target);
  try {
    const locked = await (operations.readManifest ?? readFrameworkManifest)(join(root, "tools", "framework-bundle", "manifest.json"));
    const { manifest, manifestSha256 } = locked;
    const preparedRoot = join(root, ".superpowers", "runtime", "framework-bundle", "v2", manifestSha256);
    try { await regularDirectory(preparedRoot, "prepared framework source"); await (operations.verifyPreparedBundle ?? verifyPreparedFrameworkBundle)({ root: preparedRoot, manifest, manifestSha256 }); } catch (error) { if (error?.code === "CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED") throw error; throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", "prepared framework source is not trusted", error); }
    const cmock = manifest.frameworks.find((framework) => framework.id === "cmock");
    const unity = manifest.frameworks.find((framework) => framework.id === "unity");
    const generator = manifest.fixtureTools.cmockGenerator;
    if (!cmock || !unity || !generator || generator.containerImage !== "docker.io/library/ruby" || generator.containerTag !== "3.3.6-bookworm" || generator.containerPlatform !== "linux/amd64" || generator.containerDigest !== image.slice(image.indexOf("@") + 1) || generator.entrypoint !== "lib/cmock.rb") throw failure("CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED", "manifest generator identity is not trusted");
    const cmockRoot = join(preparedRoot, cmock.sourceDirectory);
    const unityRoot = join(preparedRoot, unity.sourceDirectory);
    await regularDirectory(cmockRoot, "prepared CMock source");
    await regularDirectory(unityRoot, "prepared Unity source");
    const fixtureRoot = join(root, "testdata", "frameworks", "unity");
    const [configuration, input] = await Promise.all([readFixedFile(join(root, configPath), configBytes, "CMock configuration"), readFixedFile(join(root, headerPath), headerBytes, "CMock input header")]);
    const stagingRoot = join(dirname(target), nonce(".cmock-generation-"));
    await mkdir(stagingRoot, { recursive: false, mode: 0o700 });
    try {
    const runs = [join(stagingRoot, "run-a"), join(stagingRoot, "run-b")];
    for (const outputRoot of runs) { await mkdir(outputRoot, { recursive: false, mode: 0o700 }); await runGenerator({ cmockRoot, unityRoot, fixtureRoot, outputRoot }, operations); }
    const [left, right] = await Promise.all(runs.map(readClosedOutput));
    if (!sameFiles(left, right)) throw failure("CMOCK_GENERATION_NONDETERMINISTIC", "locked generator produced different output bytes");
    const value = provenance(manifest, manifestSha256, configuration, input, left);
    validateCMockGeneration(value, { manifest, manifestSha256, configurationSha256: sha256(configuration), inputSha256: sha256(input), outputSha256ByPath: Object.fromEntries(left.map((file) => [file.path, sha256(file.bytes)])), outputSha256: aggregateOutputDigest(left) });
    const stage = join(stagingRoot, "publish");
    await mkdir(stage, { recursive: false, mode: 0o700 });
    await Promise.all(left.map((file) => writeFile(join(stage, file.path), file.bytes, { flag: "wx", mode: 0o600 })));
    await writeFile(join(stage, "cmock-generation.json"), `${JSON.stringify(value, null, 2)}\n`, { flag: "wx", mode: 0o600 });
    await publish(target, stage, stagingRoot, operations);
    return value;
    } catch (error) { await rm(stagingRoot, { recursive: true, force: true }); throw error; }
  } finally { await releaseFixtureLock(lock); }
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) updateCMockFixture().then((value) => process.stdout.write(`${JSON.stringify(value)}\n`)).catch((error) => { process.stderr.write(`cmock-fixture-update: ${error.message}\n`); process.exitCode = 1; });
