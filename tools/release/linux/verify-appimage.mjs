import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, mkdtemp, readFile, readdir, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, posix, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { validateReleaseManifestRecord } from "../release-manifest-validation.mjs";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const defaultAppRunPath = join(toolDirectory, "AppRun");
const defaultDesktopTemplatePath = join(toolDirectory, "unit-test-ide.desktop");
const defaultIconPath = join(toolDirectory, "unit-test-ide.svg");
const semverLike = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
const fixedPaths = {
  appRun: "AppRun",
  desktopEntry: "unit-test-ide.desktop",
  icon: "unit-test-ide.svg",
  launcher: "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss",
  releaseManifestPath: "usr/lib/unit-test-ide/release-manifest.json",
  payloadRoot: "usr/lib/unit-test-ide",
};
const sidecarKeys = [
  "appRun",
  "appimagetoolSha256",
  "architecture",
  "desktopEntry",
  "launcher",
  "packageFile",
  "packageSha256",
  "platform",
  "product",
  "releaseManifestPath",
  "releaseManifestSha256",
  "schemaVersion",
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
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${name} must be a plain object`);
  }
}

function requireExactKeys(value, expected, name) {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (
    actual.length !== wanted.length ||
    actual.some((key, index) => key !== wanted[index])
  ) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${name} has unexpected keys: ${actual.join(",")}`);
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

async function sha256File(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function collectDirectoryFiles(rootPath) {
  const files = new Map();

  async function walk(currentPath, currentRelative = "") {
    const entries = await readdir(currentPath, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = currentRelative ? `${currentRelative}/${entry.name}` : entry.name;
      const absolutePath = join(rootPath, ...relativePath.split("/"));
      if (!isPortableRelativePath(relativePath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsafe AppImage entry: ${relativePath}`);
      }
      if (entry.isDirectory()) {
        await walk(absolutePath, relativePath);
        continue;
      }
      if (!entry.isFile()) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsupported AppImage entry: ${relativePath}`);
      }
      const info = await stat(absolutePath);
      const bytes = await readFile(absolutePath);
      files.set(relativePath, {
        size: info.size,
        sha256: createHash("sha256").update(bytes).digest("hex"),
        executable: (info.mode & 0o111) !== 0,
        content: bytes,
      });
    }
  }

  await walk(rootPath);
  return files;
}

function looksLikeFakeEnvelope(buffer) {
  try {
    const parsed = JSON.parse(buffer.toString("utf8"));
    return parsed?.marker === "UNIT_TEST_IDE_FAKE_APPIMAGE";
  } catch {
    return false;
  }
}

async function extractAppImage(imagePath) {
  const extractionRoot = await mkdtemp(join(tmpdir(), "release-appimage-extract-"));
  try {
    const result = spawnSync(imagePath, ["--appimage-extract"], {
      cwd: extractionRoot,
      encoding: "utf8",
      env: {
        ...process.env,
        APPIMAGE_EXTRACT_AND_RUN: "1",
      },
      windowsHide: true,
    });
    if (result.status !== 0) {
      throw releaseFailure(
        "RELEASE_VERIFICATION_FAILED",
        `unable to extract AppImage: ${result.stderr || result.stdout || "unknown extraction failure"}`,
      );
    }
    const extractedRoot = join(extractionRoot, "squashfs-root");
    const info = await lstat(extractedRoot);
    if (!info.isDirectory()) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage extraction did not produce squashfs-root");
    }
    return {
      files: await collectDirectoryFiles(extractedRoot),
      cleanup: async () => rm(extractionRoot, { recursive: true, force: true }),
    };
  } catch (error) {
    await rm(extractionRoot, { recursive: true, force: true });
    throw error;
  }
}

async function readImageFiles(imagePath, extractor) {
  const resolvedImagePath = resolve(imagePath);
  const info = await lstat(resolvedImagePath);
  if (!info.isFile()) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "image must be an AppImage file");
  }
  if (extractor) {
    const extracted = await extractor(resolvedImagePath);
    if (!extracted || !(extracted.files instanceof Map) || typeof extracted.cleanup !== "function") {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "test extractor returned an invalid extraction result");
    }
    for (const [relativePath, file] of extracted.files.entries()) {
      if (!isPortableRelativePath(relativePath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsafe extracted AppImage path: ${relativePath}`);
      }
      if (!file || !Buffer.isBuffer(file.content) || typeof file.executable !== "boolean") {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid extracted AppImage entry: ${relativePath}`);
      }
      file.size = file.content.length;
      file.sha256 = createHash("sha256").update(file.content).digest("hex");
    }
    return extracted;
  }

  const bytes = await readFile(resolvedImagePath);
  if (looksLikeFakeEnvelope(bytes)) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "fake AppImage envelope is not accepted by the public verifier");
  }
  if (process.platform === "win32") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "native AppImage extraction is unavailable on Windows");
  }
  return extractAppImage(resolvedImagePath);
}

function validateSidecarManifest(manifest) {
  requirePlainObject(manifest, "AppImage manifest");
  requireExactKeys(manifest, sidecarKeys, "AppImage manifest");
  if (manifest.schemaVersion !== 1 || manifest.product !== "unit-test-ide" || manifest.platform !== "linux") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "unsupported AppImage manifest identity");
  }
  if (!semverLike.test(manifest.version)) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage manifest version must be semver-like");
  }
  if (manifest.architecture !== "x64") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage manifest architecture must be x64");
  }
  if (manifest.appRun !== fixedPaths.appRun) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppRun path must be fixed");
  }
  if (manifest.desktopEntry !== fixedPaths.desktopEntry) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "desktop entry path must be fixed");
  }
  if (manifest.launcher !== fixedPaths.launcher) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "launcher path must be fixed");
  }
  if (manifest.releaseManifestPath !== fixedPaths.releaseManifestPath) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release-manifest path must be fixed");
  }
  if (!isPortableRelativePath(manifest.packageFile)) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "unsafe AppImage manifest packageFile");
  }
  for (const key of ["packageSha256", "releaseManifestSha256", "appimagetoolSha256"]) {
    if (!digestPattern.test(manifest[key])) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid AppImage manifest digest: ${key}`);
    }
  }
  if (/https?:\/\//iu.test(JSON.stringify(manifest))) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage manifest must not contain network URLs");
  }
  return manifest;
}

function requireFile(files, relativePath, label) {
  const file = files.get(relativePath);
  if (!file) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} is missing from the AppImage: ${relativePath}`);
  }
  return file;
}

async function renderExpectedDesktop(version) {
  const template = await readFile(defaultDesktopTemplatePath, "utf8");
  return Buffer.from(
    template
      .replaceAll("{{EXEC}}", fixedPaths.launcher)
      .replaceAll("{{VERSION}}", version),
    "utf8",
  );
}

function assertDesktopEntryContract(actualBuffer) {
  const actualText = actualBuffer.toString("utf8");
  const lines = actualText.split(/\r?\n/u);
  const execLine = lines.find((line) => line.startsWith("Exec="));
  const tryExecLine = lines.find((line) => line.startsWith("TryExec="));
  if (execLine !== `Exec=${fixedPaths.launcher}`) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "desktop entry Exec does not match the fixed launcher path");
  }
  if (tryExecLine !== `TryExec=${fixedPaths.launcher}`) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "desktop entry TryExec does not match the fixed launcher path");
  }
}

function assertBufferEquals(actual, expected, label) {
  if (!actual.equals(expected)) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} content does not match the fixed packaging contract`);
  }
}

function assertExecutable(actual, expected, label) {
  if (actual !== expected) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `${label} executable bit does not match the embedded manifest`);
  }
}

function assertArtifactFile(actual, artifact) {
  if (actual.size !== artifact.size) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} size does not match the embedded release manifest`);
  }
  if (actual.sha256 !== artifact.sha256) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} sha256 does not match the embedded release manifest`);
  }
  if (actual.executable !== artifact.executable) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} executable bit does not match the embedded release manifest`);
  }
}

function validateEmbeddedReleaseManifest(releaseManifest, sidecarManifest) {
  try {
    return validateReleaseManifestRecord(releaseManifest, {
      platform: "linux",
      architecture: sidecarManifest.architecture,
      version: sidecarManifest.version,
    });
  } catch (error) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", `embedded release manifest schema/semantics are invalid: ${error.message}`);
  }
}

function expectedPayloadPaths(releaseManifest) {
  const paths = new Set([
    fixedPaths.appRun,
    fixedPaths.desktopEntry,
    fixedPaths.icon,
    fixedPaths.releaseManifestPath,
  ]);
  for (const artifact of releaseManifest.artifacts) {
    if (!artifact?.relativePath || !isPortableRelativePath(artifact.relativePath)) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe artifact path");
    }
    paths.add(`${fixedPaths.payloadRoot}/${artifact.relativePath}`);
  }
  for (const license of releaseManifest.licenses) {
    if (!license || typeof license !== "object") {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an invalid license entry");
    }
    if (!isPortableRelativePath(license.path)) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe license path");
    }
    paths.add(`${fixedPaths.payloadRoot}/${license.path}`);
  }
  return paths;
}

export async function verifyAppImage({ image, manifest, requireDigest = false, extractor } = {}) {
  if (typeof image !== "string" || image.trim().length === 0) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "image is required");
  }
  if (typeof manifest !== "string" || manifest.trim().length === 0) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "manifest is required");
  }
  if (extractor !== undefined && typeof extractor !== "function") {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "extractor must be a function when provided");
  }

  const manifestPath = resolve(manifest);
  const sidecarManifest = validateSidecarManifest(JSON.parse(await readFile(manifestPath, "utf8")));
  const resolvedImagePath = resolve(image);
  const imageInfo = await lstat(resolvedImagePath);
  if (!imageInfo.isFile()) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "image must be an AppImage file");
  }
  if (basename(resolvedImagePath) !== sidecarManifest.packageFile) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage filename does not match the digest manifest");
  }
  const imageDigest = await sha256File(resolvedImagePath);
  if (requireDigest && imageDigest !== sidecarManifest.packageSha256) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage digest does not match the digest manifest");
  }

  const extraction = await readImageFiles(resolvedImagePath, extractor);
  try {
    const appRun = requireFile(extraction.files, fixedPaths.appRun, "AppRun");
    const desktopEntry = requireFile(extraction.files, fixedPaths.desktopEntry, "desktop entry");
    const icon = requireFile(extraction.files, fixedPaths.icon, "icon");
    const launcher = requireFile(extraction.files, fixedPaths.launcher, "launcher");
    const embeddedManifest = requireFile(extraction.files, fixedPaths.releaseManifestPath, "embedded release manifest");

    const expectedAppRun = await readFile(defaultAppRunPath);
    assertBufferEquals(appRun.content, expectedAppRun, "AppRun");
    assertExecutable(appRun.executable, true, "AppRun");

    const expectedDesktop = await renderExpectedDesktop(sidecarManifest.version);
    assertDesktopEntryContract(desktopEntry.content);
    assertBufferEquals(desktopEntry.content, expectedDesktop, "desktop entry");
    assertExecutable(desktopEntry.executable, false, "desktop entry");

    const expectedIcon = await readFile(defaultIconPath);
    assertBufferEquals(icon.content, expectedIcon, "icon");
    assertExecutable(icon.executable, false, "icon");

    const embeddedDigest = createHash("sha256").update(embeddedManifest.content).digest("hex");
    if (embeddedDigest !== sidecarManifest.releaseManifestSha256) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest digest does not match");
    }

    const releaseManifest = validateEmbeddedReleaseManifest(
      JSON.parse(embeddedManifest.content.toString("utf8")),
      sidecarManifest,
    );

    let launcherArtifact;
    for (const artifact of releaseManifest.artifacts) {
      if (typeof artifact.id !== "string" || !isPortableRelativePath(artifact.relativePath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe artifact path");
      }
      if (!digestPattern.test(artifact.sha256)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} digest is invalid`);
      }
      if (!Number.isSafeInteger(artifact.size) || artifact.size < 0) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} size is invalid`);
      }
      if (typeof artifact.executable !== "boolean") {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `artifact ${artifact.id} executable flag is invalid`);
      }
      const extractedFile = requireFile(
        extraction.files,
        `${fixedPaths.payloadRoot}/${artifact.relativePath}`,
        `artifact ${artifact.id}`,
      );
      assertArtifactFile(extractedFile, artifact);
      if (artifact.relativePath === "app/code-oss-runtime/code-oss") {
        launcherArtifact = artifact;
      }
    }
    if (!launcherArtifact) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest is missing the fixed launcher artifact");
    }
    assertArtifactFile(launcher, launcherArtifact);
    if (!launcherArtifact.executable) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest launcher must be executable");
    }

    for (const license of releaseManifest.licenses) {
      if (!license || typeof license !== "object") {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an invalid license entry");
      }
      if (!isPortableRelativePath(license.path)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe license path");
      }
      if (!Number.isSafeInteger(license.size) || license.size < 0) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `license ${license.path} size is invalid`);
      }
      if (!digestPattern.test(license.sha256)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `license ${license.path} digest is invalid`);
      }
      const extractedLicense = requireFile(
        extraction.files,
        `${fixedPaths.payloadRoot}/${license.path}`,
        `license ${license.path}`,
      );
      if (extractedLicense.size !== license.size) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `license ${license.path} size does not match the embedded release manifest`);
      }
      if (extractedLicense.sha256 !== license.sha256) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `license ${license.path} sha256 does not match the embedded release manifest`);
      }
    }

    const expectedPaths = expectedPayloadPaths(releaseManifest);
    for (const extractedPath of extraction.files.keys()) {
      if (!expectedPaths.has(extractedPath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unexpected payload path: ${extractedPath}`);
      }
    }

    return {
      packageSha256: sidecarManifest.packageSha256,
      releaseManifestSha256: sidecarManifest.releaseManifestSha256,
      launcher: fixedPaths.launcher,
    };
  } finally {
    await extraction.cleanup();
  }
}

function usage() {
  return "Usage: node tools/release/linux/verify-appimage.mjs --image <path> --manifest <path> [--require-digest]";
}

function parseCliArguments(argv) {
  let image;
  let manifest;
  let requireDigest = false;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--image") {
      image = argv[++index];
    } else if (argument === "--manifest") {
      manifest = argv[++index];
    } else if (argument === "--require-digest") {
      requireDigest = true;
    } else if (argument === "--help") {
      return { help: true };
    } else {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unknown verify flag: ${argument}`);
    }
  }
  return { image, manifest, requireDigest };
}

async function main(argv) {
  const parsed = parseCliArguments(argv);
  if (parsed.help) {
    process.stdout.write(`${usage()}\n`);
    return;
  }
  const result = await verifyAppImage(parsed);
  process.stdout.write(`${JSON.stringify(result)}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.stderr.write(`${usage()}\n`);
    process.exitCode = 1;
  });
}
