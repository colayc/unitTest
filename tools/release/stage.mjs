import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFile, chmod, lstat, mkdir, mkdtemp, readdir, readFile, realpath, rename, rm, stat, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { buildReleaseManifest } from "./manifest.mjs";
import { validateCodeOssRuntime } from "./code-oss-runtime.mjs";
import { isPortableReleasePath } from "./portable-path.mjs";
import { normalizeTreeTimestamps, resolveSourceDateEpoch } from "./release-reproducibility.mjs";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
const semverLike = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const requiredKeys = [
  "architecture",
  "cmakeRoot",
  "codeOssRoot",
  "codeOssSha256",
  "coverageRoot",
  "outRoot",
  "platform",
  "service",
  "version",
];
const cliFlagMap = new Map([
  ["--platform", "platform"],
  ["--architecture", "architecture"],
  ["--version", "version"],
  ["--code-oss-root", "codeOssRoot"],
  ["--code-oss-sha256", "codeOssSha256"],
  ["--service", "service"],
  ["--cmake-root", "cmakeRoot"],
  ["--coverage-root", "coverageRoot"],
  ["--out", "outRoot"],
]);

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

function releaseInputMissing(message) {
  const error = new Error(message);
  error.code = "RELEASE_INPUT_MISSING";
  return error;
}

function withinRoot(rootPath, candidatePath) {
  const relativePath = relative(rootPath, candidatePath);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function validateRealDirectory(path, label, { missingCode = "RELEASE_INPUT_MISSING" } = {}) {
  const resolvedPath = resolve(path);
  let info;
  try {
    info = await lstat(resolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT" && missingCode) throw releaseInputMissing(`${label} is required`);
    throw error;
  }
  if (info.isSymbolicLink()) throw new Error(`${label} must not be a reparse point`);
  if (!info.isDirectory()) throw new Error(`${label} must be a directory`);
  return {
    path: resolvedPath,
    canonicalPath: await realpath(resolvedPath),
  };
}

async function validateRealFile(path, label) {
  const resolvedPath = resolve(path);
  let info;
  try {
    info = await lstat(resolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT") throw releaseInputMissing(`${label} is required`);
    throw error;
  }
  if (info.isSymbolicLink()) throw new Error(`${label} must not be a reparse point`);
  if (!info.isFile()) throw new Error(`${label} must be a file`);
  return {
    path: resolvedPath,
    canonicalPath: await realpath(resolvedPath),
  };
}

async function copyRegularFile(sourcePath, destinationPath) {
  await mkdir(dirname(destinationPath), { recursive: true });
  await copyFile(sourcePath, destinationPath);
  const sourceInfo = await stat(sourcePath);
  await chmod(destinationPath, sourceInfo.mode);
}

async function copyTree(sourceRoot, destinationRoot) {
  const queue = [{ source: sourceRoot.path, relativePath: "" }];
  while (queue.length > 0) {
    const current = queue.shift();
    const entries = await readdir(current.source, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = current.relativePath
        ? `${current.relativePath}/${entry.name}`
        : entry.name;
      if (!isPortableReleasePath(relativePath)) {
        throw new Error(`unsafe staged path: ${relativePath}`);
      }
      const sourcePath = join(sourceRoot.path, ...relativePath.split("/"));
      const info = await lstat(sourcePath);
      if (info.isSymbolicLink()) {
        throw new Error(`unsafe staged path: ${relativePath}`);
      }
      const canonicalPath = await realpath(sourcePath);
      if (!withinRoot(sourceRoot.canonicalPath, canonicalPath)) {
        throw new Error(`unsafe staged path: ${relativePath}`);
      }
      const destinationPath = join(destinationRoot, ...relativePath.split("/"));
      if (info.isDirectory()) {
        await mkdir(destinationPath, { recursive: true });
        queue.push({ source: sourcePath, relativePath });
      } else if (info.isFile()) {
        await copyRegularFile(sourcePath, destinationPath);
      } else {
        throw new Error(`unsupported staged entry: ${relativePath}`);
      }
    }
  }
}

async function collectFiles(rootPath, prefix = "") {
  const results = [];
  async function walk(currentPath, currentRelative) {
    const entries = await readdir(currentPath, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = currentRelative ? `${currentRelative}/${entry.name}` : entry.name;
      const absolutePath = join(rootPath, ...relativePath.split("/"));
      const info = await lstat(absolutePath);
      if (info.isSymbolicLink()) throw new Error(`unsafe staged path: ${relativePath}`);
      if (info.isDirectory()) {
        await walk(absolutePath, relativePath);
      } else if (info.isFile()) {
        results.push(prefix ? `${prefix}/${relativePath}` : relativePath);
      } else {
        throw new Error(`unsupported staged entry: ${relativePath}`);
      }
    }
  }
  await walk(rootPath, "");
  return results;
}

function isLicenseRelativePath(relativePath) {
  const basename = relativePath.split("/").at(-1) ?? "";
  return relativePath.split("/").includes("licenses")
    || /^(?:licen[cs]e(?:s)?|notice(?:s)?|copying)(?:$|[._-].+)$/iu.test(basename)
    || /^thirdpartynotices(?:$|[._-].+)$/iu.test(basename)
    || /(?:^|[._-])(?:licen[cs]e|notice)(?:$|(?:\.[^.]+)+$)/iu.test(basename);
}

async function copyLicenseSet(sourceRoot, destinationRoot, namespace) {
  const files = await collectFiles(sourceRoot.path);
  const copied = [];
  for (const relativePath of files) {
    if (!isLicenseRelativePath(relativePath)) continue;
    const sourcePath = join(sourceRoot.path, ...relativePath.split("/"));
    const stagedRelativePath = `licenses/${namespace}/${relativePath}`;
    const destinationPath = join(destinationRoot, ...stagedRelativePath.split("/"));
    await copyRegularFile(sourcePath, destinationPath);
    copied.push(stagedRelativePath);
  }
  copied.sort((left, right) => left.localeCompare(right, "en"));
  return copied;
}

function artifactKind(relativePath) {
  if (relativePath.startsWith("app/extensions/unit-test-ide/")) return "extension";
  if (relativePath.startsWith("bundles/cmake/")) return "bundle-cmake";
  if (relativePath.startsWith("bundles/coverage/")) return "bundle-coverage";
  if (relativePath.startsWith("app/code-oss-runtime/")) return "runtime";
  if (relativePath === "service/unit-test-service" || relativePath === "service/unit-test-service.exe") return "service";
  return "payload";
}

function artifactId(relativePath, index) {
  const normalized = relativePath
    .toLowerCase()
    .replace(/[^a-z0-9]+/gu, "-")
    .replace(/^-+|-+$/gu, "");
  return normalized.length > 0 ? normalized : `artifact-${index + 1}`;
}

function executableArtifact(relativePath, mode) {
  return relativePath === "app/code-oss-runtime/code-oss"
    || relativePath === "app/code-oss-runtime/Code - OSS.exe"
    || relativePath === "service/unit-test-service"
    || relativePath === "service/unit-test-service.exe"
    || (mode & 0o111) !== 0;
}

async function sha256File(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function buildArtifacts(stagingRoot) {
  const files = await collectFiles(stagingRoot);
  const artifacts = [];
  const ids = new Set();
  for (const relativePath of files) {
    if (relativePath === "release-manifest.json" || relativePath.startsWith("licenses/")) {
      continue;
    }
    const absolutePath = join(stagingRoot, ...relativePath.split("/"));
    const info = await stat(absolutePath);
    let id = artifactId(relativePath, artifacts.length);
    if (ids.has(id)) {
      id = `${id}-${createHash("sha256").update(relativePath).digest("hex").slice(0, 8)}`;
    }
    ids.add(id);
    artifacts.push({
      id,
      kind: artifactKind(relativePath),
      relativePath,
      size: info.size,
      sha256: await sha256File(absolutePath),
      executable: executableArtifact(relativePath, info.mode),
    });
  }
  return artifacts;
}

function currentSourceCommit(root) {
  return execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: root,
    encoding: "utf8",
    windowsHide: true,
  }).trim();
}

function usage() {
  return "Usage: node tools/release/stage.mjs --platform <windows|linux> --architecture <x64> --version <semver> --code-oss-root <dir> --code-oss-sha256 <lowercase-sha256> --service <file> --cmake-root <dir> --coverage-root <dir> --out <dir>";
}

function parseCliArguments(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--help") {
      return { help: true };
    }
    const key = cliFlagMap.get(argument);
    if (!key) {
      throw new Error(`unknown stage flag: ${argument}`);
    }
    if (Object.hasOwn(values, key)) {
      throw new Error(`duplicate stage flag: ${argument}`);
    }
    const value = argv[index + 1];
    if (!value || value.startsWith("--")) {
      throw new Error(`missing value for ${argument}`);
    }
    values[key] = value;
    index += 1;
  }
  return values;
}

function normalizeInput(input) {
  requirePlainObject(input, "release stage input");
  for (const key of requiredKeys) {
    if (!(key in input) || typeof input[key] !== "string" || input[key].trim().length === 0) {
      throw releaseInputMissing(`${key} is required`);
    }
  }
  if (!semverLike.test(input.version)) throw new Error("version must be semver-like");
  if (!["windows", "linux"].includes(input.platform)) throw new Error("platform must be windows or linux");
  if (input.architecture !== "x64") throw new Error("architecture must be x64");
  return {
    platform: input.platform,
    architecture: input.architecture,
    version: input.version,
    codeOssRoot: input.codeOssRoot,
    codeOssSha256: input.codeOssSha256,
    service: input.service,
    cmakeRoot: input.cmakeRoot,
    coverageRoot: input.coverageRoot,
    outRoot: input.outRoot,
    extensionRoot: input.extensionRoot ?? join(repositoryRoot, "apps", "code-oss-extension"),
    sourceCommit: input.sourceCommit ?? currentSourceCommit(input.repositoryRoot ?? repositoryRoot),
    sourceDateEpoch: input.sourceDateEpoch,
    repositoryRoot: input.repositoryRoot ?? repositoryRoot,
  };
}

export async function stageRelease(input, {
  validateRuntime = validateCodeOssRuntime,
} = {}) {
  const normalized = normalizeInput(input);
  const sourceEpoch = resolveSourceDateEpoch(normalized.sourceDateEpoch);
  const [validated, service, cmakeRoot, coverageRoot, extensionRoot] = await Promise.all([
    validateRuntime({
      root: normalized.codeOssRoot,
      platform: normalized.platform,
      expectedLauncherSha256: normalized.codeOssSha256,
    }),
    validateRealFile(normalized.service, "service binary"),
    validateRealDirectory(normalized.cmakeRoot, "cmake bundle root"),
    validateRealDirectory(normalized.coverageRoot, "coverage bundle root"),
    validateRealDirectory(normalized.extensionRoot, "extension root"),
  ]);
  const runtimeSource = { path: validated.root, canonicalPath: validated.canonicalRoot };
  const extensionDist = await validateRealDirectory(join(extensionRoot.path, "dist"), "extension dist");
  const extensionManifest = await validateRealFile(join(extensionRoot.path, "package.json"), "extension manifest");

  const parentRoot = join(resolve(normalized.outRoot), "staging", normalized.version);
  await mkdir(parentRoot, { recursive: true });
  const finalRoot = join(parentRoot, `${normalized.platform}-${normalized.architecture}`);
  let finalExists = false;
  try {
    await lstat(finalRoot);
    finalExists = true;
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  if (finalExists) {
    throw new Error(`staging root already exists: ${finalRoot}`);
  }

  const temporaryRoot = await mkdtemp(join(parentRoot, `.stage-${normalized.platform}-${normalized.architecture}-`));
  try {
    const serviceName = normalized.platform === "windows" ? "unit-test-service.exe" : "unit-test-service";
    await copyTree(runtimeSource, join(temporaryRoot, "app", "code-oss-runtime"));
    await copyRegularFile(service.path, join(temporaryRoot, "service", serviceName));
    await copyRegularFile(extensionManifest.path, join(temporaryRoot, "app", "extensions", "unit-test-ide", "package.json"));
    await copyTree(extensionDist, join(temporaryRoot, "app", "extensions", "unit-test-ide", "dist"));
    await copyTree(cmakeRoot, join(temporaryRoot, "bundles", "cmake"));
    await copyTree(coverageRoot, join(temporaryRoot, "bundles", "coverage"));
    const stagedRuntimeRoot = join(temporaryRoot, "app", "code-oss-runtime");
    const stagedRuntime = await validateRuntime({
      root: stagedRuntimeRoot,
      platform: normalized.platform,
      expectedLauncherSha256: normalized.codeOssSha256,
    });
    const stagedRuntimeSource = { path: stagedRuntime.root, canonicalPath: stagedRuntime.canonicalRoot };
    const [codeOssLicenses, cmakeLicenses, coverageLicenses] = await Promise.all([
      copyLicenseSet(stagedRuntimeSource, temporaryRoot, "code-oss"),
      copyLicenseSet(cmakeRoot, temporaryRoot, "cmake"),
      copyLicenseSet(coverageRoot, temporaryRoot, "coverage"),
    ]);
    const licenses = [...codeOssLicenses, ...cmakeLicenses, ...coverageLicenses]
      .sort((left, right) => left.localeCompare(right, "en"));
    const artifacts = await buildArtifacts(temporaryRoot);
    const manifest = await buildReleaseManifest({
      version: normalized.version,
      platform: normalized.platform,
      architecture: normalized.architecture,
      stagingRoot: temporaryRoot,
      sourceCommit: normalized.sourceCommit,
      artifacts,
      licenses,
    }, { sourceDateEpoch: sourceEpoch.seconds });
    const manifestPath = join(temporaryRoot, "release-manifest.json");
    await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
    await normalizeTreeTimestamps(temporaryRoot, sourceEpoch);
    await rename(temporaryRoot, finalRoot);
    return {
      manifest,
      manifestPath: join(finalRoot, "release-manifest.json"),
      stagingRoot: finalRoot,
    };
  } catch (error) {
    await rm(temporaryRoot, { recursive: true, force: true });
    throw error;
  }
}

async function main(argv) {
  const parsed = parseCliArguments(argv);
  if (parsed.help) {
    process.stdout.write(`${usage()}\n`);
    return;
  }
  const result = await stageRelease(parsed);
  process.stdout.write(`${result.stagingRoot}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.stderr.write(`${usage()}\n`);
    process.exitCode = 1;
  });
}
