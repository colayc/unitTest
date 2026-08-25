import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, mkdir, readFile, readdir, realpath, stat, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const schema = JSON.parse(await readFile(join(toolDirectory, "manifest.schema.json"), "utf8"));
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateManifest = ajv.compile(schema);

function auditFailure(message, cause) {
  const error = new Error(`RELEASE_LICENSE_AUDIT_FAILED: ${message}`, cause ? { cause } : undefined);
  error.code = "RELEASE_LICENSE_AUDIT_FAILED";
  return error;
}

function safeRelativePath(value) {
  return typeof value === "string"
    && value.length > 0
    && !value.includes("\\")
    && !value.includes(":")
    && !isAbsolute(value)
    && !posix.isAbsolute(value)
    && posix.normalize(value) === value
    && value.split("/").every((segment) => segment.length > 0 && segment !== "." && segment !== "..");
}

function withinRoot(root, candidate) {
  const relativePath = relative(root, candidate);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function requireRoot(root) {
  const path = resolve(root);
  let info;
  try {
    info = await lstat(path);
  } catch (error) {
    throw auditFailure("staging root is missing", error);
  }
  if (info.isSymbolicLink() || !info.isDirectory()) throw auditFailure("staging root must be a real directory");
  return { canonical: await realpath(path), path };
}

async function requireFile(root, relativePath, label) {
  if (!safeRelativePath(relativePath)) throw auditFailure(`${label} has an unsafe path: ${relativePath}`);
  let current = root.path;
  for (const segment of relativePath.split("/")) {
    current = join(current, segment);
    let info;
    try {
      info = await lstat(current);
    } catch (error) {
      throw auditFailure(`${label} is missing: ${relativePath}`, error);
    }
    if (info.isSymbolicLink()) throw auditFailure(`${label} is a reparse point: ${relativePath}`);
  }
  const info = await stat(current);
  if (!info.isFile()) throw auditFailure(`${label} must be a regular file: ${relativePath}`);
  if (!withinRoot(root.canonical, await realpath(current))) throw auditFailure(`${label} escapes staging root: ${relativePath}`);
  return { info, path: current };
}

async function readJson(root, relativePath, label) {
  const file = await requireFile(root, relativePath, label);
  try {
    return JSON.parse(await readFile(file.path, "utf8"));
  } catch (error) {
    throw auditFailure(`${label} is not valid JSON`, error);
  }
}

async function readOptionalJson(root, relativePath, label) {
  try {
    return await readJson(root, relativePath, label);
  } catch (error) {
    if (error?.cause?.code === "ENOENT") return null;
    throw error;
  }
}

function requireText(value, label) {
  if (typeof value !== "string" || value.trim().length === 0) throw auditFailure(`${label} is missing`);
}

function findNotice(licensePaths, suffix) {
  return licensePaths.find((path) => path === suffix || path.endsWith(`/${suffix}`));
}

function coverageIdentity(releaseManifest, sourceManifest, resolvedManifest) {
  if (sourceManifest !== null) {
    if (sourceManifest?.schemaVersion !== 1 || !Array.isArray(sourceManifest?.gcovr?.wheels)) {
      throw auditFailure("coverage bundle manifest has an unsupported schema");
    }
    return sourceManifest;
  }
  const expectedPlatform = `${releaseManifest.platform}-${releaseManifest.architecture}`;
  if (
    resolvedManifest?.schemaVersion !== 1
    || resolvedManifest?.platform !== expectedPlatform
    || !Array.isArray(resolvedManifest?.inputs?.wheels)
  ) {
    throw auditFailure("coverage resolved lock has an unsupported identity");
  }
  return {
    python: { version: resolvedManifest.pythonVersion },
    gcovr: { version: resolvedManifest.gcovrVersion, wheels: resolvedManifest.inputs.wheels },
  };
}

function auditCoverageDependencies(releaseManifest, sourceManifest, resolvedManifest, dependencies) {
  const coverageManifest = coverageIdentity(releaseManifest, sourceManifest, resolvedManifest);
  if (dependencies?.schemaVersion !== 1) {
    throw auditFailure("coverage license manifests have an unsupported schema");
  }
  if (!Array.isArray(coverageManifest?.gcovr?.wheels) || !Array.isArray(dependencies?.packages)) {
    throw auditFailure("coverage dependency lists are missing");
  }
  if (
    dependencies?.python?.version !== coverageManifest?.python?.version
    || dependencies?.gcovr?.version !== coverageManifest?.gcovr?.version
  ) {
    throw auditFailure("coverage dependency versions do not match the bundle manifest");
  }
  const listed = new Map();
  for (const dependency of dependencies.packages) {
    requireText(dependency?.project, "coverage dependency project");
    requireText(dependency?.version, `coverage dependency ${dependency?.project} version`);
    const id = `${dependency.project}@${dependency.version}`;
    if (listed.has(id)) throw auditFailure(`duplicate dependency notice: ${id}`);
    requireText(dependency.license, `${id} license`);
    requireText(dependency.licenseTextId, `${id} licenseTextId`);
    requireText(dependencies?.licenseTexts?.[dependency.licenseTextId], `${id} license text`);
    requireText(dependency.notice, `${id} notice`);
    requireText(dependency.licenseSource, `${id} license source`);
    listed.set(id, dependency);
  }
  const bundled = new Set();
  for (const wheel of coverageManifest.gcovr.wheels) {
    requireText(wheel?.project, "bundled dependency project");
    requireText(wheel?.version, `bundled dependency ${wheel?.project} version`);
    const id = `${wheel.project}@${wheel.version}`;
    if (bundled.has(id)) throw auditFailure(`duplicate bundled dependency: ${id}`);
    bundled.add(id);
    if (!listed.has(id)) throw auditFailure(`unlisted dependency: ${id}`);
  }
  for (const id of listed.keys()) {
    if (!bundled.has(id)) throw auditFailure(`dependency notice has no bundled dependency: ${id}`);
  }
  const licensePaths = releaseManifest.licenses.map(({ path }) => path);
  for (const [name, dependency] of [["Python", dependencies.python], ["gcovr", dependencies.gcovr]]) {
    requireText(dependency?.license, `${name} license`);
    requireText(dependency?.licenseFile, `${name} license file`);
    requireText(dependency?.licenseSource, `${name} license source`);
    if (!findNotice(licensePaths, `coverage/licenses/${dependency.licenseFile}`)) {
      throw auditFailure(`${name} license notice is missing from the closed release list`);
    }
  }
  if (!findNotice(licensePaths, "coverage/licenses/dependencies.json")) {
    throw auditFailure("coverage dependency notice is missing from the closed release list");
  }
}

function auditCmakeLicense(releaseManifest, cmakeManifest) {
  const licensePaths = releaseManifest.licenses.map(({ path }) => path);
  if (cmakeManifest === null) {
    const notices = licensePaths.filter((path) =>
      path.startsWith("licenses/cmake/")
      && /(?:^|\/)(?:licen[cs]e|notice|copying)(?:[.-].+)?$/iu.test(path),
    );
    if (notices.length === 0) throw auditFailure("CMake license notice is unlisted");
    return;
  }
  if (cmakeManifest?.schemaVersion !== 1 || typeof cmakeManifest?.archives !== "object") {
    throw auditFailure("CMake license manifest has an unsupported schema");
  }
  requireText(cmakeManifest.license, "CMake license identifier");
  const platformKey = releaseManifest.platform === "windows" ? "win32-x64" : "linux-x64";
  const archive = cmakeManifest.archives[platformKey];
  if (!archive) throw auditFailure(`CMake bundle does not list ${platformKey}`);
  requireText(archive.licensePath, `CMake ${platformKey} license path`);
  if (!safeRelativePath(archive.licensePath)) throw auditFailure("CMake license path is unsafe");
  if (!findNotice(licensePaths, `cmake/${archive.licensePath}`)) {
    throw auditFailure("CMake license notice is unlisted");
  }
}

async function collectLicenseFiles(root) {
  const licenseRoot = join(root.path, "licenses");
  let info;
  try {
    info = await lstat(licenseRoot);
  } catch (error) {
    throw auditFailure("license directory is missing", error);
  }
  if (info.isSymbolicLink() || !info.isDirectory()) throw auditFailure("license directory must be a real directory");
  const files = [];
  async function walk(current, prefix) {
    const entries = await readdir(current, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = prefix ? `${prefix}/${entry.name}` : entry.name;
      const path = join(current, entry.name);
      const entryInfo = await lstat(path);
      if (entryInfo.isSymbolicLink()) throw auditFailure(`license entry is a reparse point: licenses/${relativePath}`);
      if (entryInfo.isDirectory()) await walk(path, relativePath);
      else if (entryInfo.isFile()) files.push(`licenses/${relativePath}`);
      else throw auditFailure(`license entry is unsupported: licenses/${relativePath}`);
    }
  }
  await walk(licenseRoot, "");
  return files;
}

export async function auditLicenses(stagingRoot) {
  const root = await requireRoot(stagingRoot);
  const releaseManifest = await readJson(root, "release-manifest.json", "release manifest");
  if (!validateManifest(releaseManifest)) {
    throw auditFailure(`release manifest violates the closed contract: ${ajv.errorsText(validateManifest.errors)}`);
  }
  if (releaseManifest.product !== "unit-test-ide" || releaseManifest.schemaVersion !== 1) {
    throw auditFailure("release manifest has an unsupported identity");
  }
  const licenses = [...releaseManifest.licenses].sort((left, right) => left.path.localeCompare(right.path, "en"));
  const uniquePaths = new Set(licenses.map(({ path }) => path));
  if (uniquePaths.size !== licenses.length) throw auditFailure("release manifest has duplicate license paths");

  const [cmakeManifest, coverageManifest, coverageResolved, dependencies] = await Promise.all([
    readOptionalJson(root, "bundles/cmake/manifest.json", "CMake bundle manifest"),
    readOptionalJson(root, "bundles/coverage/manifest.json", "coverage bundle manifest"),
    readOptionalJson(root, "bundles/coverage/manifest.resolved.json", "coverage resolved lock"),
    readJson(root, "bundles/coverage/licenses/dependencies.json", "coverage dependency notice"),
  ]);
  auditCmakeLicense(releaseManifest, cmakeManifest);
  auditCoverageDependencies(releaseManifest, coverageManifest, coverageResolved, dependencies);

  const actualFiles = await collectLicenseFiles(root);
  const expectedFiles = licenses.map(({ path }) => path);
  if (
    actualFiles.length !== expectedFiles.length
    || actualFiles.some((path, index) => path !== expectedFiles[index])
  ) {
    const missing = expectedFiles.filter((path) => !actualFiles.includes(path));
    const unlisted = actualFiles.filter((path) => !uniquePaths.has(path));
    throw auditFailure(`license file set is not closed; missing=${missing.join(",")}; unlisted=${unlisted.join(",")}`);
  }
  for (const license of licenses) {
    const file = await requireFile(root, license.path, "license notice");
    if (file.info.size !== license.size) throw auditFailure(`license size does not match: ${license.path}`);
    if (await sha256File(file.path) !== license.sha256) throw auditFailure(`license hash does not match: ${license.path}`);
  }
  return licenses.map(({ path, sha256, size }) => ({ path, sha256, size }));
}

function parseCli(argv) {
  const values = new Map();
  const allowed = new Set(["--staging-root", "--out"]);
  for (let index = 0; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!allowed.has(flag)) throw auditFailure(`unknown flag: ${flag}`);
    if (typeof value !== "string" || value.length === 0 || value.startsWith("--")) {
      throw auditFailure(`missing value for ${flag}`);
    }
    if (values.has(flag)) throw auditFailure(`duplicate flag: ${flag}`);
    values.set(flag, value);
  }
  if (!values.has("--staging-root") || !values.has("--out")) {
    throw auditFailure("--staging-root and --out are required");
  }
  return {
    out: resolve(values.get("--out")),
    stagingRoot: resolve(values.get("--staging-root")),
  };
}

async function main(argv) {
  const input = parseCli(argv);
  const licenses = await auditLicenses(input.stagingRoot);
  const manifest = JSON.parse(await readFile(join(input.stagingRoot, "release-manifest.json"), "utf8"));
  const evidence = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version: manifest.version,
    platform: manifest.platform,
    sourceCommit: manifest.sourceCommit,
    licenses,
    passed: true,
  };
  await mkdir(dirname(input.out), { recursive: true });
  await writeFile(input.out, `${JSON.stringify(evidence, null, 2)}\n`);
  process.stdout.write(`${input.out}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
