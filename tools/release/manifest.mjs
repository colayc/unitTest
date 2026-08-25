import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, mkdir, readFile, realpath, stat, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const defaultConfigPath = join(toolDirectory, "release-config.json");
const schemaPath = join(toolDirectory, "manifest.schema.json");
const semverLike = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const commitSha = /^[0-9a-f]{40}$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
const supportedInputKeys = [
  "architecture",
  "artifacts",
  "licenses",
  "platform",
  "sourceCommit",
  "stagingRoot",
  "version",
];
const artifactKeys = ["executable", "id", "kind", "relativePath", "sha256", "size"];
const licenseKeys = ["path", "sha256", "size"];
const releaseConfigKeys = ["inputPath", "outputPath", "product", "schemaVersion"];

const cachedConfigs = new Map();
let cachedValidateManifest;

function requirePlainObject(value, name) {
  if (
    value === null ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) {
    throw new Error(`${name} must be a plain object`);
  }
}

function requireExactKeys(value, expected, name) {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (
    actual.length !== wanted.length ||
    actual.some((key, index) => key !== wanted[index])
  ) {
    throw new Error(`${name} has unexpected keys: ${actual.join(",")}`);
  }
}

function isPortableRelativePath(value) {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value.includes("\\") ||
    value.includes(":") ||
    posix.isAbsolute(value) ||
    isAbsolute(value) ||
    posix.normalize(value) !== value
  ) {
    return false;
  }
  const segments = value.split("/");
  return segments.every((segment) =>
    segment.length > 0 &&
    segment !== "." &&
    segment !== ".." &&
    !/[<>:"|?*]/u.test(segment) &&
    !segment.endsWith(".") &&
    !segment.endsWith(" ")
  );
}

function withinRoot(rootPath, candidatePath) {
  const relativePath = relative(rootPath, candidatePath);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) {
    hash.update(chunk);
  }
  return hash.digest("hex");
}

async function loadJson(path) {
  return JSON.parse(await readFile(path, "utf8"));
}

function validateReleaseConfig(config, configPath) {
  requirePlainObject(config, "release config");
  requireExactKeys(config, releaseConfigKeys, "release config");
  if (config.schemaVersion !== 1 || config.product !== "unit-test-ide") {
    throw new Error("unsupported release config");
  }
  if (!isPortableRelativePath(config.inputPath)) {
    throw new Error(`unsafe release config inputPath: ${config.inputPath}`);
  }
  if (!isPortableRelativePath(config.outputPath)) {
    throw new Error(`unsafe release config outputPath: ${config.outputPath}`);
  }
  return {
    schemaVersion: config.schemaVersion,
    product: config.product,
    inputPath: resolve(dirname(configPath), ...config.inputPath.split("/")),
    outputPath: resolve(dirname(configPath), ...config.outputPath.split("/")),
  };
}

async function readReleaseConfig(configPath = defaultConfigPath) {
  const resolvedConfigPath = resolve(configPath);
  if (cachedConfigs.has(resolvedConfigPath)) return cachedConfigs.get(resolvedConfigPath);
  const config = validateReleaseConfig(await loadJson(resolvedConfigPath), resolvedConfigPath);
  cachedConfigs.set(resolvedConfigPath, config);
  return config;
}

async function manifestValidator() {
  if (cachedValidateManifest) return cachedValidateManifest;
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const schema = await loadJson(schemaPath);
  cachedValidateManifest = ajv.compile(schema);
  return cachedValidateManifest;
}

async function resolvedCheckedPath(rootPath, canonicalRoot, relativePath, label) {
  let current = rootPath;
  for (const segment of relativePath.split("/")) {
    current = join(current, segment);
    const entryInfo = await lstat(current);
    if (entryInfo.isSymbolicLink()) {
      throw new Error(`unsafe ${label} path`);
    }
  }
  const canonicalPath = await realpath(current);
  if (!withinRoot(canonicalRoot, canonicalPath)) {
    throw new Error(`unsafe ${label} path`);
  }
  return current;
}

async function validatedLicense(stagingRoot, canonicalRoot, license) {
  if (!isPortableRelativePath(license)) {
    throw new Error(`unsafe license path: ${license}`);
  }
  const resolvedPath = resolve(stagingRoot, ...license.split("/"));
  const lexicalRelative = relative(stagingRoot, resolvedPath);
  if (!withinRoot(stagingRoot, resolvedPath) || lexicalRelative.includes("..")) {
    throw new Error(`unsafe license path: ${license}`);
  }
  const checkedPath = await resolvedCheckedPath(stagingRoot, canonicalRoot, license, "license");
  const entryInfo = await lstat(checkedPath);
  if (!entryInfo.isFile()) {
    throw new Error(`license must be a regular file: ${license}`);
  }
  const actualSize = (await stat(checkedPath)).size;
  const actualDigest = await sha256File(checkedPath);
  return {
    path: license,
    size: actualSize,
    sha256: actualDigest,
  };
}

async function validatedExistingLicense(stagingRoot, canonicalRoot, license) {
  requirePlainObject(license, "license");
  requireExactKeys(license, licenseKeys, "license");
  if (!isPortableRelativePath(license.path)) {
    throw new Error(`unsafe license path: ${license.path}`);
  }
  if (!Number.isSafeInteger(license.size) || license.size < 0) {
    throw new Error(`license size must be a non-negative safe integer: ${license.path}`);
  }
  if (!digestPattern.test(license.sha256)) {
    throw new Error(`license digest must be lowercase SHA-256: ${license.path}`);
  }

  const resolvedPath = resolve(stagingRoot, ...license.path.split("/"));
  const lexicalRelative = relative(stagingRoot, resolvedPath);
  if (!withinRoot(stagingRoot, resolvedPath) || lexicalRelative.includes("..")) {
    throw new Error(`unsafe license path: ${license.path}`);
  }
  const checkedPath = await resolvedCheckedPath(stagingRoot, canonicalRoot, license.path, "license");
  const entryInfo = await lstat(checkedPath);
  if (!entryInfo.isFile()) {
    throw new Error(`license must be a regular file: ${license.path}`);
  }
  const actualSize = (await stat(checkedPath)).size;
  if (actualSize !== license.size) {
    throw new Error(`license size mismatch: ${license.path}`);
  }
  const actualDigest = await sha256File(checkedPath);
  if (actualDigest !== license.sha256) {
    throw new Error(`license sha256 mismatch: ${license.path}`);
  }
  return {
    path: license.path,
    size: license.size,
    sha256: license.sha256,
  };
}

async function validatedArtifact(stagingRoot, canonicalRoot, artifact) {
  requirePlainObject(artifact, "artifact");
  requireExactKeys(artifact, artifactKeys, "artifact");
  if (!isPortableRelativePath(artifact.relativePath)) {
    throw new Error("unsafe artifact path");
  }
  if (!digestPattern.test(artifact.sha256)) {
    throw new Error(`artifact digest must be lowercase SHA-256: ${artifact.id}`);
  }
  if (!Number.isSafeInteger(artifact.size) || artifact.size < 0) {
    throw new Error(`artifact size must be a non-negative safe integer: ${artifact.id}`);
  }
  if (typeof artifact.id !== "string" || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/u.test(artifact.id)) {
    throw new Error("artifact id is invalid");
  }
  if (typeof artifact.kind !== "string" || !/^[a-z][a-z0-9-]*$/u.test(artifact.kind)) {
    throw new Error(`artifact kind is invalid: ${artifact.id}`);
  }
  if (typeof artifact.executable !== "boolean") {
    throw new Error(`artifact executable flag is invalid: ${artifact.id}`);
  }

  const resolvedPath = resolve(stagingRoot, ...artifact.relativePath.split("/"));
  const lexicalRelative = relative(stagingRoot, resolvedPath);
  if (!withinRoot(stagingRoot, resolvedPath) || lexicalRelative.includes("..")) {
    throw new Error("unsafe artifact path");
  }
  const checkedPath = await resolvedCheckedPath(stagingRoot, canonicalRoot, artifact.relativePath, "artifact");
  const entryInfo = await lstat(checkedPath);
  if (!entryInfo.isFile()) {
    throw new Error(`artifact must be a regular file: ${artifact.relativePath}`);
  }
  const actualSize = (await stat(checkedPath)).size;
  if (actualSize !== artifact.size) {
    throw new Error(`artifact size mismatch: ${artifact.id}`);
  }
  const actualDigest = await sha256File(checkedPath);
  if (actualDigest !== artifact.sha256) {
    throw new Error(`artifact sha256 mismatch: ${artifact.id}`);
  }

  return {
    id: artifact.id,
    kind: artifact.kind,
    relativePath: artifact.relativePath,
    size: artifact.size,
    sha256: artifact.sha256,
    executable: artifact.executable,
  };
}

export async function buildReleaseManifest(input, options = {}) {
  requirePlainObject(input, "release manifest input");
  requireExactKeys(input, supportedInputKeys, "release manifest input");
  if (!semverLike.test(input.version)) {
    throw new Error("release version must be semver-like");
  }
  if (typeof input.platform !== "string" || input.platform.length === 0) {
    throw new Error("platform is required");
  }
  if (typeof input.architecture !== "string" || input.architecture.length === 0) {
    throw new Error("architecture is required");
  }
  if (!commitSha.test(input.sourceCommit)) {
    throw new Error("sourceCommit must be a 40-character lowercase git SHA");
  }
  if (!Array.isArray(input.artifacts) || input.artifacts.length === 0) {
    throw new Error("artifacts must be a non-empty array");
  }
  if (!Array.isArray(input.licenses)) {
    throw new Error("licenses must be an array");
  }
  const stagingRoot = resolve(input.stagingRoot);
  const stagingInfo = await lstat(stagingRoot);
  if (stagingInfo.isSymbolicLink() || !stagingInfo.isDirectory()) {
    throw new Error("stagingRoot must be a real directory");
  }
  const canonicalRoot = await realpath(stagingRoot);
  const licenses = [];
  const licensePaths = new Set();
  for (const license of input.licenses) {
    const normalizedLicense = typeof license === "string"
      ? await validatedLicense(stagingRoot, canonicalRoot, license)
      : await validatedExistingLicense(stagingRoot, canonicalRoot, license);
    if (licensePaths.has(normalizedLicense.path)) {
      throw new Error(`duplicate license path: ${normalizedLicense.path}`);
    }
    licensePaths.add(normalizedLicense.path);
    licenses.push(normalizedLicense);
  }
  licenses.sort((left, right) => left.path.localeCompare(right.path, "en"));
  const ids = new Set();
  const artifacts = [];
  for (const artifact of input.artifacts) {
    if (ids.has(artifact.id)) {
      throw new Error(`duplicate artifact id: ${artifact.id}`);
    }
    ids.add(artifact.id);
    artifacts.push(await validatedArtifact(stagingRoot, canonicalRoot, artifact));
  }
  artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));

  const config = options.releaseConfig ?? await readReleaseConfig(options.configPath);
  const manifest = {
    schemaVersion: 1,
    product: config.product,
    version: input.version,
    platform: input.platform,
    architecture: input.architecture,
    sourceCommit: input.sourceCommit,
    artifacts,
    licenses,
    generatedAt: new Date().toISOString(),
  };
  const validate = await manifestValidator();
  if (!validate(manifest)) {
    throw new Error(`invalid release manifest: ${validate.errors?.map(({ instancePath, message }) => `${instancePath || "/"} ${message}`).join("; ")}`);
  }
  return manifest;
}

export function toDeterministicManifestBytes(manifest) {
  requirePlainObject(manifest, "release manifest");
  const stable = {
    schemaVersion: manifest.schemaVersion,
    product: manifest.product,
    version: manifest.version,
    platform: manifest.platform,
    architecture: manifest.architecture,
    sourceCommit: manifest.sourceCommit,
    artifacts: manifest.artifacts,
    licenses: manifest.licenses,
  };
  return Buffer.from(JSON.stringify(stable), "utf8");
}

function usage() {
  return "Usage: node tools/release/manifest.mjs [--config <path>] [--input <path>] [--output <path>]";
}

function normalizedBuildInput(input, inputPath) {
  requirePlainObject(input, "release manifest input");
  if (typeof input.stagingRoot === "string" && !isAbsolute(input.stagingRoot)) {
    return {
      ...input,
      stagingRoot: resolve(dirname(inputPath), ...input.stagingRoot.split("/")),
    };
  }
  return input;
}

async function main(argv) {
  let configPath = defaultConfigPath;
  let inputPath;
  let outputPath;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--config") {
      configPath = argv[++index];
    } else if (argument === "--input") {
      inputPath = argv[++index];
    } else if (argument === "--output") {
      outputPath = argv[++index];
    } else if (argument === "--help") {
      process.stdout.write(`${usage()}\n`);
      return;
    } else {
      throw new Error(`unknown argument: ${argument}`);
    }
  }
  const releaseConfig = await readReleaseConfig(configPath);
  const effectiveInputPath = inputPath ? resolve(inputPath) : releaseConfig.inputPath;
  const effectiveOutputPath = outputPath ? resolve(outputPath) : releaseConfig.outputPath;
  const manifest = await buildReleaseManifest(
    normalizedBuildInput(await loadJson(effectiveInputPath), effectiveInputPath),
    { releaseConfig },
  );
  const bytes = `${JSON.stringify(manifest, null, 2)}\n`;
  if (effectiveOutputPath) {
    await mkdir(dirname(effectiveOutputPath), { recursive: true });
    await writeFile(effectiveOutputPath, bytes);
    return;
  }
  process.stdout.write(bytes);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.stderr.write(`${usage()}\n`);
    process.exitCode = 1;
  });
}
