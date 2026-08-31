import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, copyFile, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, stat, writeFile } from "node:fs/promises";
import { basename, dirname, extname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { verifyAppImage } from "./verify-appimage.mjs";
import { validateReleaseManifestRecord } from "../release-manifest-validation.mjs";
import { normalizePathTimestamp, normalizeTreeTimestamps, resolveSourceDateEpoch } from "../release-reproducibility.mjs";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const defaultAppRunTemplatePath = join(toolDirectory, "AppRun");
const defaultDesktopTemplatePath = join(toolDirectory, "unit-test-ide.desktop");
const defaultIconPath = join(toolDirectory, "unit-test-ide.svg");
const semverLike = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
const supportedKeys = [
  "appRunTemplatePath",
  "appimagetool",
  "architecture",
  "desktopTemplatePath",
  "expectedDigest",
  "iconPath",
  "output",
  "sourceDateEpoch",
  "stagingRoot",
  "verificationExtractor",
  "version",
];

function releaseFailure(code, message) {
  const error = new Error(`${code}: ${message}`);
  error.code = code;
  return error;
}

function requirePlainObject(value, name) {
  if (
    value === null ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", `${name} must be a plain object`);
  }
}

function requireKnownKeys(value, expected, name) {
  const actual = Object.keys(value);
  for (const key of actual) {
    if (!expected.includes(key)) {
      throw releaseFailure("RELEASE_PACKAGING_FAILED", `${name} has unexpected key: ${key}`);
    }
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
  return value.split("/").every((segment) => segment.length > 0 && segment !== "." && segment !== "..");
}

function withinRoot(rootPath, candidatePath) {
  const relativePath = relative(rootPath, candidatePath);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

async function sha256File(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function validateRealDirectory(path, label) {
  const resolvedPath = resolve(path);
  let info;
  try {
    info = await lstat(resolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT") throw releaseFailure("RELEASE_INPUT_MISSING", `${label} is required`);
    throw error;
  }
  if (info.isSymbolicLink()) throw releaseFailure("RELEASE_PACKAGING_FAILED", `${label} must not be a symlink`);
  if (!info.isDirectory()) throw releaseFailure("RELEASE_PACKAGING_FAILED", `${label} must be a directory`);
  return {
    path: resolvedPath,
    canonicalPath: await realpath(resolvedPath),
  };
}

async function validateRealFile(path, label, missingCode = "RELEASE_INPUT_MISSING") {
  const resolvedPath = resolve(path);
  let info;
  try {
    info = await lstat(resolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT") throw releaseFailure(missingCode, `${label} is required`);
    throw error;
  }
  if (info.isSymbolicLink()) throw releaseFailure("RELEASE_PACKAGING_FAILED", `${label} must not be a symlink`);
  if (!info.isFile()) throw releaseFailure("RELEASE_PACKAGING_FAILED", `${label} must be a file`);
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
      const relativePath = current.relativePath ? `${current.relativePath}/${entry.name}` : entry.name;
      if (!isPortableRelativePath(relativePath)) {
        throw releaseFailure("RELEASE_PACKAGING_FAILED", `unsafe staged path: ${relativePath}`);
      }
      const sourcePath = join(sourceRoot.path, ...relativePath.split("/"));
      const info = await lstat(sourcePath);
      if (info.isSymbolicLink()) {
        throw releaseFailure("RELEASE_PACKAGING_FAILED", `unsafe staged path: ${relativePath}`);
      }
      const canonicalPath = await realpath(sourcePath);
      if (!withinRoot(sourceRoot.canonicalPath, canonicalPath)) {
        throw releaseFailure("RELEASE_PACKAGING_FAILED", `unsafe staged path: ${relativePath}`);
      }
      const destinationPath = join(destinationRoot, ...relativePath.split("/"));
      if (info.isDirectory()) {
        await mkdir(destinationPath, { recursive: true });
        queue.push({ source: sourcePath, relativePath });
      } else if (info.isFile()) {
        await copyRegularFile(sourcePath, destinationPath);
      } else {
        throw releaseFailure("RELEASE_PACKAGING_FAILED", `unsupported staged entry: ${relativePath}`);
      }
    }
  }
}

function expectedDigestFromInput(input) {
  const digest = input.expectedDigest ?? process.env.RELEASE_APPIMAGETOOL_SHA256;
  if (typeof digest !== "string" || !digestPattern.test(digest)) {
    throw releaseFailure("RELEASE_CONFIG_MISSING", "RELEASE_APPIMAGETOOL_SHA256 must be a lowercase SHA-256 digest");
  }
  return digest;
}

function normalizedOutputPath(output) {
  if (typeof output !== "string" || output.trim().length === 0) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "output is required");
  }
  return resolve(output);
}

async function loadReleaseManifest(stagingRoot, version, architecture, sourceEpoch) {
  const manifestFile = await validateRealFile(join(stagingRoot.path, "release-manifest.json"), "release manifest");
  const manifest = JSON.parse(await readFile(manifestFile.path, "utf8"));
  try {
    validateReleaseManifestRecord(manifest, { platform: "linux", architecture, version });
  } catch (error) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", error.message);
  }
  if (manifest.generatedAt !== sourceEpoch.iso) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "release manifest generatedAt does not match SOURCE_DATE_EPOCH");
  }
  return {
    file: manifestFile,
    manifest,
  };
}

async function materializeDesktopEntry(templatePath, destinationPath, version) {
  const template = await readFile(templatePath, "utf8");
  const output = template
    .replaceAll("{{EXEC}}", "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss")
    .replaceAll("{{VERSION}}", version);
  await writeFile(destinationPath, output);
}

function runExternalTool(filePath, args, options) {
  const extension = extname(filePath).toLowerCase();
  if (extension === ".mjs" || extension === ".js" || extension === ".cjs") {
    return spawnSync(process.execPath, [filePath, ...args], options);
  }
  if (process.platform === "win32" && extension === ".ps1") {
    return spawnSync("powershell.exe", [
      "-NoProfile",
      "-ExecutionPolicy",
      "Bypass",
      "-File",
      filePath,
      ...args,
    ], options);
  }
  return spawnSync(filePath, args, options);
}

export async function packageAppImage(input) {
  requirePlainObject(input, "AppImage package input");
  requireKnownKeys(input, supportedKeys, "AppImage package input");
  if (typeof input.version !== "string" || !semverLike.test(input.version)) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "version must be semver-like");
  }
  if (input.architecture !== "x64") {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "architecture must be x64");
  }
  if (typeof input.stagingRoot !== "string" || input.stagingRoot.trim().length === 0) {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "stagingRoot is required");
  }
  if (typeof input.appimagetool !== "string" || input.appimagetool.trim().length === 0) {
    throw releaseFailure("RELEASE_TOOL_MISSING", "appimagetool is required");
  }
  if (input.verificationExtractor !== undefined && typeof input.verificationExtractor !== "function") {
    throw releaseFailure("RELEASE_PACKAGING_FAILED", "verificationExtractor must be a function when provided");
  }

  const sourceEpoch = resolveSourceDateEpoch(input.sourceDateEpoch);
  const expectedDigest = expectedDigestFromInput(input);
  const outputPath = normalizedOutputPath(input.output);
  const stagingRoot = await validateRealDirectory(input.stagingRoot, "staging root");
  const appimagetool = await validateRealFile(input.appimagetool, "appimagetool", "RELEASE_TOOL_MISSING");
  const appRunTemplate = await validateRealFile(
    input.appRunTemplatePath ?? defaultAppRunTemplatePath,
    "AppRun template",
    "RELEASE_TEMPLATE_MISSING",
  );
  const desktopTemplate = await validateRealFile(
    input.desktopTemplatePath ?? defaultDesktopTemplatePath,
    "desktop template",
    "RELEASE_TEMPLATE_MISSING",
  );
  const icon = await validateRealFile(
    input.iconPath ?? defaultIconPath,
    "icon template",
    "RELEASE_TEMPLATE_MISSING",
  );

  const actualToolDigest = await sha256File(appimagetool.path);
  if (actualToolDigest !== expectedDigest) {
    throw releaseFailure("RELEASE_TOOL_DIGEST_MISMATCH", "appimagetool digest does not match the pinned release configuration");
  }

  const { file: releaseManifestFile } = await loadReleaseManifest(stagingRoot, input.version, input.architecture, sourceEpoch);

  const outputParent = dirname(outputPath);
  await mkdir(outputParent, { recursive: true });
  const appDir = await mkdtemp(join(outputParent, ".appdir-"));
  try {
    const payloadRoot = join(appDir, "usr", "lib", "unit-test-ide");
    await copyTree(stagingRoot, payloadRoot);
    await copyRegularFile(appRunTemplate.path, join(appDir, "AppRun"));
    await chmod(join(appDir, "AppRun"), 0o755);
    await materializeDesktopEntry(desktopTemplate.path, join(appDir, "unit-test-ide.desktop"), input.version);
    await copyRegularFile(icon.path, join(appDir, "unit-test-ide.svg"));
    await normalizeTreeTimestamps(appDir, sourceEpoch);

    const result = runExternalTool(appimagetool.path, [appDir, outputPath], {
      cwd: outputParent,
      encoding: "utf8",
      env: {
        ...process.env,
        ARCH: "x86_64",
        SOURCE_DATE_EPOCH: String(sourceEpoch.seconds),
        VERSION: input.version,
      },
      windowsHide: true,
    });
    if (result.status !== 0) {
      throw releaseFailure(
        "RELEASE_PACKAGING_FAILED",
        result.stderr?.trim() || result.stdout?.trim() || "appimagetool failed",
      );
    }

    await normalizePathTimestamp(outputPath, sourceEpoch);
    const packageDigest = await sha256File(outputPath);
    const releaseManifestDigest = await sha256File(releaseManifestFile.path);
    const manifestPath = `${outputPath}.sha256.json`;
    const sidecarManifest = {
      schemaVersion: 1,
      product: "unit-test-ide",
      version: input.version,
      platform: "linux",
      architecture: input.architecture,
      packageFile: basename(outputPath),
      packageSha256: packageDigest,
      releaseManifestPath: "usr/lib/unit-test-ide/release-manifest.json",
      releaseManifestSha256: releaseManifestDigest,
      launcher: "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss",
      desktopEntry: "unit-test-ide.desktop",
      appRun: "AppRun",
      appimagetoolSha256: expectedDigest,
    };
    await writeFile(manifestPath, `${JSON.stringify(sidecarManifest, null, 2)}\n`);
    await normalizePathTimestamp(manifestPath, sourceEpoch);

    await verifyAppImage({
      image: outputPath,
      manifest: manifestPath,
      requireDigest: true,
      extractor: input.verificationExtractor,
    });

    return {
      output: outputPath,
      manifestPath,
      appDir,
    };
  } catch (error) {
    await rm(outputPath, { force: true });
    throw error;
  }
}

function usage() {
  return "Usage: node tools/release/linux/package-appimage.mjs --staging-root <dir> --output <file> --appimagetool <file> --version <semver> --architecture <x64>";
}

function parseCliArguments(argv) {
  const parsed = {};
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--help") {
      return { help: true };
    }
    if (!argument.startsWith("--")) {
      throw releaseFailure("RELEASE_PACKAGING_FAILED", `unknown package flag: ${argument}`);
    }
    const value = argv[index + 1];
    if (!value || value.startsWith("--")) {
      throw releaseFailure("RELEASE_PACKAGING_FAILED", `missing value for ${argument}`);
    }
    if (argument === "--staging-root") {
      parsed.stagingRoot = value;
    } else if (argument === "--output") {
      parsed.output = value;
    } else if (argument === "--appimagetool") {
      parsed.appimagetool = value;
    } else if (argument === "--version") {
      parsed.version = value;
    } else if (argument === "--architecture") {
      parsed.architecture = value;
    } else {
      throw releaseFailure("RELEASE_PACKAGING_FAILED", `unknown package flag: ${argument}`);
    }
    index += 1;
  }
  return parsed;
}

async function main(argv) {
  const parsed = parseCliArguments(argv);
  if (parsed.help) {
    process.stdout.write(`${usage()}\n`);
    return;
  }
  const result = await packageAppImage(parsed);
  process.stdout.write(`${JSON.stringify({ image: result.output, manifestPath: result.manifestPath })}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.stderr.write(`${usage()}\n`);
    process.exitCode = 1;
  });
}
