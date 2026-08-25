import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, mkdtemp, readFile, readdir, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, extname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const semverLike = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
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

function decodeFakeAppImageEnvelope(buffer) {
  let envelope;
  try {
    envelope = JSON.parse(buffer.toString("utf8"));
  } catch {
    return null;
  }
  if (envelope?.marker !== "UNIT_TEST_IDE_FAKE_APPIMAGE") return null;
  requirePlainObject(envelope, "fake AppImage envelope");
  requirePlainObject(envelope.files, "fake AppImage envelope files");
  const files = new Map();
  for (const [relativePath, entry] of Object.entries(envelope.files)) {
    requirePlainObject(entry, `fake AppImage entry ${relativePath}`);
    if (!isPortableRelativePath(relativePath)) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsafe AppImage entry: ${relativePath}`);
    }
    if (!digestPattern.test(entry.sha256)) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid AppImage entry digest: ${relativePath}`);
    }
    if (!Number.isSafeInteger(entry.size) || entry.size < 0) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid AppImage entry size: ${relativePath}`);
    }
    if (typeof entry.executable !== "boolean" || typeof entry.contentBase64 !== "string") {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `invalid AppImage entry shape: ${relativePath}`);
    }
    const content = Buffer.from(entry.contentBase64, "base64");
    files.set(relativePath, {
      size: entry.size,
      sha256: entry.sha256,
      executable: entry.executable,
      content,
    });
  }
  return files;
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

async function readImageFiles(imagePath) {
  const resolvedImagePath = resolve(imagePath);
  const info = await lstat(resolvedImagePath);
  if (info.isDirectory()) {
    return {
      files: await collectDirectoryFiles(resolvedImagePath),
      cleanup: async () => {},
    };
  }
  const bytes = await readFile(resolvedImagePath);
  const fakeEnvelope = decodeFakeAppImageEnvelope(bytes);
  if (fakeEnvelope) {
    return {
      files: fakeEnvelope,
      cleanup: async () => {},
    };
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
  for (const key of ["packageFile", "releaseManifestPath", "launcher", "desktopEntry", "appRun"]) {
    if (!isPortableRelativePath(manifest[key])) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", `unsafe AppImage manifest path: ${key}`);
    }
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

function parseDesktopEntry(bytes) {
  return bytes.toString("utf8");
}

export async function verifyAppImage({ image, manifest, requireDigest = false }) {
  if (typeof image !== "string" || image.trim().length === 0) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "image is required");
  }
  if (typeof manifest !== "string" || manifest.trim().length === 0) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "manifest is required");
  }

  const manifestPath = resolve(manifest);
  const sidecarManifest = validateSidecarManifest(JSON.parse(await readFile(manifestPath, "utf8")));
  const resolvedImagePath = resolve(image);
  const imageInfo = await lstat(resolvedImagePath);
  if (!imageInfo.isFile() && !imageInfo.isDirectory()) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "image must be a file or extracted AppDir");
  }

  if (imageInfo.isFile()) {
    if (basename(resolvedImagePath) !== sidecarManifest.packageFile) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage filename does not match the digest manifest");
    }
    const imageDigest = await sha256File(resolvedImagePath);
    if (requireDigest && imageDigest !== sidecarManifest.packageSha256) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppImage digest does not match the digest manifest");
    }
  } else if (requireDigest) {
    throw releaseFailure("RELEASE_VERIFICATION_FAILED", "digest verification requires an AppImage file");
  }

  const extraction = await readImageFiles(resolvedImagePath);
  try {
    const appRun = requireFile(extraction.files, sidecarManifest.appRun, "AppRun");
    const desktopEntry = requireFile(extraction.files, sidecarManifest.desktopEntry, "desktop entry");
    const launcher = requireFile(extraction.files, sidecarManifest.launcher, "launcher");
    const embeddedManifest = requireFile(
      extraction.files,
      sidecarManifest.releaseManifestPath,
      "embedded release manifest",
    );

    if (!appRun.executable) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "AppRun must be executable");
    }
    if (!launcher.executable) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "launcher must be executable");
    }

    const desktopText = parseDesktopEntry(desktopEntry.content);
    const execLine = desktopText.split(/\r?\n/u).find((line) => line.startsWith("Exec="));
    if (execLine !== `Exec=${sidecarManifest.launcher}`) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "desktop entry does not point at the staged launcher");
    }

    const embeddedDigest = createHash("sha256").update(embeddedManifest.content).digest("hex");
    if (embeddedDigest !== sidecarManifest.releaseManifestSha256) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest digest does not match");
    }

    const releaseManifest = JSON.parse(embeddedManifest.content.toString("utf8"));
    requirePlainObject(releaseManifest, "embedded release manifest");
    if (releaseManifest.platform !== "linux" || releaseManifest.architecture !== sidecarManifest.architecture) {
      throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest platform metadata is invalid");
    }
    for (const artifact of releaseManifest.artifacts ?? []) {
      if (!artifact?.relativePath || !isPortableRelativePath(artifact.relativePath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe artifact path");
      }
      requireFile(extraction.files, `usr/lib/unit-test-ide/${artifact.relativePath}`, `artifact ${artifact.id}`);
    }
    for (const licensePath of releaseManifest.licenses ?? []) {
      if (!isPortableRelativePath(licensePath)) {
        throw releaseFailure("RELEASE_VERIFICATION_FAILED", "embedded release manifest contains an unsafe license path");
      }
      requireFile(extraction.files, `usr/lib/unit-test-ide/${licensePath}`, `license ${licensePath}`);
    }

    return {
      packageSha256: sidecarManifest.packageSha256,
      releaseManifestSha256: sidecarManifest.releaseManifestSha256,
      launcher: sidecarManifest.launcher,
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
