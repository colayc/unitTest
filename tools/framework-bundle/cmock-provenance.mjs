import { createHash } from "node:crypto";
import { lstat, readFile, readdir } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { frameworkFailure, portableRelativePath, sha256File } from "./manifest.mjs";

const digest = /^[0-9a-f]{64}$/u;
const outputPaths = Object.freeze(["MockDependency.c", "MockDependency.h"]);
const configurationPath = "testdata/frameworks/unity/cmock.yml";
const inputPath = "testdata/frameworks/unity/include/Dependency.h";
const outputDirectory = "testdata/frameworks/unity/mocks";

function failure(message, cause) { return frameworkFailure("CMOCK_PROVENANCE_INVALID", message, cause); }
function canonicalJson(value) { return `${JSON.stringify(value, null, 2)}\n`; }
function sha256(bytes) { return createHash("sha256").update(bytes).digest("hex"); }
function closedValue(actual, expected) {
  if (typeof expected !== "object" || expected === null) return Object.is(actual, expected);
  if (!actual || typeof actual !== "object" || Array.isArray(actual) !== Array.isArray(expected)) return false;
  if (Array.isArray(expected)) return actual.length === expected.length && actual.every((item, index) => closedValue(item, expected[index]));
  if (Object.getPrototypeOf(actual) !== Object.prototype) return false;
  const actualKeys = Object.keys(actual), expectedKeys = Object.keys(expected);
  return actualKeys.length === expectedKeys.length && actualKeys.every((key, index) => key === expectedKeys[index] && closedValue(actual[key], expected[key]));
}
function strictJson(bytes) {
  let text;
  try { text = new TextDecoder("utf-8", { fatal: true }).decode(bytes); } catch (error) { throw failure("provenance is not UTF-8", error); }
  let value; try { value = JSON.parse(text); } catch (error) { throw failure("provenance is not JSON", error); }
  if (text !== canonicalJson(value)) throw failure("provenance is not canonical JSON");
  return value;
}
function assertNoAlias(stat, label) { if (!stat.isFile() || stat.isSymbolicLink()) throw failure(`${label} is not a regular file`); }
async function regularFile(path, label) { let stat; try { stat = await lstat(path); } catch (error) { throw failure(`${label} is missing`, error); } assertNoAlias(stat, label); return readFile(path); }
function assertGeneratedBytes(path, bytes) {
  let text; try { text = new TextDecoder("utf-8", { fatal: true }).decode(bytes); } catch (error) { throw failure(`generated output is not UTF-8: ${path}`, error); }
  if (text.includes("\r")) throw failure(`generated output uses CRLF: ${path}`);
  if (/(?:[A-Za-z]:[\\/][^\s"')]+|(?:^|[\s"'(=])\/(?:[A-Za-z0-9_.~-]+\/)+[A-Za-z0-9_.~-]+)/mu.test(text)) throw failure(`generated output contains an absolute path: ${path}`);
  if (/Generated on|\b20\d\d-\d\d-\d\d(?:T|\s)\d\d:\d\d/u.test(text)) throw failure(`generated output contains a timestamp: ${path}`);
}

export function aggregateOutputDigest(files) {
  if (!Array.isArray(files) || files.length === 0) throw failure("generated output set is invalid");
  const seen = new Set(); const ordered = files.map((file) => {
    if (!file || typeof file !== "object" || !portableRelativePath(file.path) || !Buffer.isBuffer(file.bytes) || seen.has(file.path)) throw failure("generated output set is invalid");
    seen.add(file.path); return file;
  }).sort((left, right) => left.path.localeCompare(right.path, "en"));
  const hash = createHash("sha256"); for (const file of ordered) { hash.update(`f:${file.path}\0`); hash.update(file.bytes); }
  return hash.digest("hex");
}

export function validateCMockGeneration(value, expected) {
  if (!expected?.manifest || !digest.test(expected.manifestSha256 ?? "")) throw failure("expected CMock identity is invalid");
  const cmock = expected.manifest.frameworks?.find((framework) => framework.id === "cmock");
  const generator = expected.manifest.fixtureTools?.cmockGenerator;
  if (!cmock || !generator) throw failure("manifest does not contain CMock identity");
  const normalized = {
    schemaVersion: 1,
    cmock: { version: cmock.version, tag: cmock.tag, revision: cmock.revision },
    generator: { entrypoint: generator.entrypoint, version: generator.version, containerImage: generator.containerImage, containerPlatform: generator.containerPlatform, containerDigest: generator.containerDigest },
    configuration: { path: configurationPath, sha256: expected.configurationSha256 },
    input: { path: inputPath, sha256: expected.inputSha256 },
    outputs: outputPaths.map((path) => ({ path, sha256: expected.outputSha256ByPath?.[path] })),
    outputSha256: expected.outputSha256,
    frameworkManifestSha256: expected.manifestSha256,
    generatedAtRuntime: false
  };
  if (!digest.test(normalized.configuration.sha256 ?? "") || !digest.test(normalized.input.sha256 ?? "") || !digest.test(normalized.outputSha256 ?? "") || normalized.outputs.some((file) => !digest.test(file.sha256 ?? ""))) throw failure("expected CMock digests are invalid");
  if (!closedValue(value, normalized)) throw failure("provenance does not match the closed CMock identity");
  return value;
}

export async function readCMockGeneration(path, expected) {
  const root = resolve(expected?.root ?? dirname(dirname(dirname(dirname(path)))));
  const generationPath = resolve(path);
  if (relative(root, generationPath).startsWith("..")) throw failure("provenance path is outside the repository");
  const mocks = join(root, outputDirectory);
  const configuration = join(root, configurationPath), input = join(root, inputPath);
  const [configurationBytes, inputBytes, generationBytes] = await Promise.all([regularFile(configuration, configurationPath), regularFile(input, inputPath), regularFile(generationPath, `${outputDirectory}/cmock-generation.json`)]);
  let entries; try { entries = await readdir(mocks, { withFileTypes: true }); } catch (error) { throw failure("generated output directory is missing", error); }
  const expectedEntries = new Set([...outputPaths, "cmock-generation.json"]);
  if (entries.length !== expectedEntries.size || entries.some((entry) => !expectedEntries.has(entry.name) || !entry.isFile() || entry.isSymbolicLink())) throw failure("generated output directory is not a closed file set");
  const files = [];
  for (const output of outputPaths) { const bytes = await regularFile(join(mocks, output), `${outputDirectory}/${output}`); assertGeneratedBytes(output, bytes); files.push({ path: output, bytes }); }
  const expectedIdentity = {
    ...expected,
    configurationSha256: sha256(configurationBytes), inputSha256: sha256(inputBytes),
    outputSha256ByPath: Object.fromEntries(files.map((file) => [file.path, sha256(file.bytes)])), outputSha256: aggregateOutputDigest(files)
  };
  const value = strictJson(generationBytes); validateCMockGeneration(value, expectedIdentity);
  return { value, bytes: generationBytes, cMockProvenanceSha256: sha256(generationBytes), outputSha256: expectedIdentity.outputSha256 };
}

export const CMOCK_PROVENANCE_PATHS = Object.freeze({ configurationPath, inputPath, outputDirectory, outputPaths });
