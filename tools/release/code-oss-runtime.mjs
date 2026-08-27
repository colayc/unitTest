import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { createReadStream } from "node:fs";
import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { isAbsolute, join, posix, relative, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

const layouts = {
  windows: { launcherRelativePath: "Code - OSS.exe", requireExecutable: false },
  linux: { launcherRelativePath: "code-oss", requireExecutable: true },
};

const identity = {
  applicationName: "code-oss",
  licenseName: "MIT",
  nameShort: "Code - OSS",
};

const digestPattern = /^[0-9a-f]{64}$/u;
const execFileAsync = promisify(execFile);
const windowsReparsePointCommand = "$root=New-Object IO.DirectoryInfo($env:CODE_OSS_RUNTIME_ROOT); function Test-ReparsePoint([IO.FileSystemInfo]$node) { if (($node.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { return $true }; if ($node -is [IO.DirectoryInfo]) { foreach ($child in $node.EnumerateFileSystemInfos()) { if (Test-ReparsePoint $child) { return $true } } }; return $false }; [Console]::Out.Write([int](Test-ReparsePoint $root))";

function releaseInputError(code, message) {
  const error = new Error(`${code}: ${message}`);
  error.code = code;
  return error;
}

function withinRoot(rootPath, candidatePath) {
  const relativePath = relative(rootPath, candidatePath);
  return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

function portableRelativePath(value) {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value.includes("\\") ||
    value.includes(":") ||
    /[\u0000-\u001f\u007f]/u.test(value) ||
    posix.isAbsolute(value) ||
    isAbsolute(value) ||
    posix.normalize(value) !== value
  ) {
    return false;
  }
  return value.split("/").every((segment) => {
    if (
      segment.length === 0 ||
      segment === "." ||
      segment === ".." ||
      segment.startsWith(" ") ||
      segment.endsWith(".") ||
      segment.endsWith(" ") ||
      /[<>:"|?*\\]/u.test(segment)
    ) {
      return false;
    }
    const base = segment.split(".", 1)[0].toUpperCase();
    return !(["CON", "PRN", "AUX", "NUL"].includes(base) || /^(COM|LPT)[1-9]$/u.test(base));
  });
}

async function assertNoWindowsReparsePoints(rootPath) {
  if (process.platform !== "win32") return false;
  try {
    const { stdout } = await execFileAsync("powershell.exe", [
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      windowsReparsePointCommand,
    ], {
      encoding: "utf8",
      env: { ...process.env, CODE_OSS_RUNTIME_ROOT: rootPath },
      windowsHide: true,
    });
    if (stdout.trim() === "1") {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a reparse point");
    }
  } catch (error) {
    if (error?.code === "RELEASE_INPUT_INVALID") throw error;
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree reparse-point state cannot be inspected");
  }
}

async function scanRuntimeTree(rootPath, canonicalRoot) {
  const files = new Map();
  const exactPaths = new Set();
  const caseInsensitivePaths = new Map();

  async function scanDirectory(currentPath, currentRelativePath = "") {
    let canonicalCurrentPath;
    let entries;
    try {
      canonicalCurrentPath = await realpath(currentPath);
      entries = await readdir(currentPath, { withFileTypes: true });
    } catch {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree cannot be read");
    }
    if (!withinRoot(canonicalRoot, canonicalCurrentPath)) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree escapes the runtime root");
    }
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));

    for (const entry of entries) {
      const relativePath = currentRelativePath ? `${currentRelativePath}/${entry.name}` : entry.name;
      if (!portableRelativePath(relativePath)) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a non-portable path");
      }
      const lowerCasePath = relativePath.toLowerCase();
      const aliasedPath = caseInsensitivePaths.get(lowerCasePath);
      if (aliasedPath !== undefined && aliasedPath !== relativePath) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a case-insensitive path alias");
      }
      caseInsensitivePaths.set(lowerCasePath, relativePath);
      exactPaths.add(relativePath);

      const entryPath = join(currentPath, entry.name);
      let info;
      let canonicalEntryPath;
      try {
        info = await lstat(entryPath);
        canonicalEntryPath = await realpath(entryPath);
      } catch (error) {
        if (error?.code === "RELEASE_INPUT_INVALID") throw error;
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree entry cannot be resolved");
      }
      if (entry.isSymbolicLink() || info.isSymbolicLink()) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a symbolic link or reparse point");
      }
      if (!withinRoot(canonicalRoot, canonicalEntryPath)) {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree entry escapes the runtime root");
      }
      if (info.isDirectory()) {
        await scanDirectory(entryPath, relativePath);
      } else if (info.isFile()) {
        files.set(relativePath, { canonicalPath: canonicalEntryPath, info });
      } else {
        throw releaseInputError("RELEASE_INPUT_INVALID", "runtime tree contains a special entry");
      }
    }
  }

  await scanDirectory(rootPath);
  return { files, exactPaths };
}

async function hashFile(path) {
  const hash = createHash("sha256");
  await new Promise((resolveHash, rejectHash) => {
    const stream = createReadStream(path);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", rejectHash);
    stream.on("end", resolveHash);
  });
  return hash.digest("hex");
}

export async function validateCodeOssRuntime({ root, platform, expectedLauncherSha256 } = {}) {
  const layout = layouts[platform];
  if (!layout || typeof root !== "string" || root.length === 0) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root and platform are required");
  }
  if (!digestPattern.test(expectedLauncherSha256 ?? "")) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "launcher digest must be a lowercase SHA-256");
  }

  const rootPath = resolve(root);
  let rootInfo;
  try {
    rootInfo = await lstat(rootPath);
  } catch (error) {
    if (error?.code === "ENOENT") throw releaseInputError("RELEASE_INPUT_MISSING", "runtime root is required");
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root cannot be inspected");
  }
  if (rootInfo.isSymbolicLink() || !rootInfo.isDirectory()) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root must be a real directory");
  }
  await assertNoWindowsReparsePoints(rootPath);
  let canonicalRoot;
  try {
    canonicalRoot = await realpath(rootPath);
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "runtime root cannot be resolved");
  }
  const tree = await scanRuntimeTree(rootPath, canonicalRoot);
  const requiredPaths = [layout.launcherRelativePath, "resources/app/product.json", "resources/app/package.json"];
  for (const requiredPath of requiredPaths) {
    if (!tree.exactPaths.has(requiredPath) || !tree.files.has(requiredPath)) {
      throw releaseInputError("RELEASE_INPUT_MISSING", `${requiredPath} is required`);
    }
  }

  const launcher = tree.files.get(layout.launcherRelativePath);
  if (layout.requireExecutable && (launcher.info.mode & 0o111) === 0) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "Linux launcher must be executable");
  }
  const product = tree.files.get("resources/app/product.json");

  let productMetadata;
  try {
    productMetadata = JSON.parse(await readFile(product.canonicalPath, "utf8"));
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "product metadata must be valid JSON");
  }
  if (
    productMetadata === null ||
    typeof productMetadata !== "object" ||
    Array.isArray(productMetadata) ||
    Object.getPrototypeOf(productMetadata) !== Object.prototype ||
    productMetadata.applicationName !== identity.applicationName ||
    productMetadata.licenseName !== identity.licenseName ||
    productMetadata.nameShort !== identity.nameShort
  ) {
    throw releaseInputError("RELEASE_INPUT_INVALID", "product metadata does not identify Code - OSS");
  }

  let launcherSha256;
  try {
    launcherSha256 = await hashFile(launcher.canonicalPath);
  } catch {
    throw releaseInputError("RELEASE_INPUT_INVALID", "platform launcher cannot be read");
  }
  if (launcherSha256 !== expectedLauncherSha256) {
    throw releaseInputError("RELEASE_INPUT_DIGEST_MISMATCH", "platform launcher digest does not match");
  }

  return {
    root: rootPath,
    canonicalRoot,
    launcherPath: launcher.canonicalPath,
    launcherRelativePath: layout.launcherRelativePath,
    launcherSha256,
    productIdentity: { ...identity },
  };
}

function parseCliArguments(argumentsList) {
  const flags = new Map();
  for (let index = 0; index < argumentsList.length; index += 2) {
    const flag = argumentsList[index];
    const value = argumentsList[index + 1];
    if (!new Set(["--platform", "--root", "--launcher-sha256"]).has(flag) || value === undefined || flags.has(flag)) {
      throw releaseInputError("RELEASE_INPUT_INVALID", "CLI requires --platform, --root, and --launcher-sha256");
    }
    flags.set(flag, value);
  }
  if (flags.size !== 3) throw releaseInputError("RELEASE_INPUT_INVALID", "CLI requires --platform, --root, and --launcher-sha256");
  return {
    platform: flags.get("--platform"),
    root: flags.get("--root"),
    expectedLauncherSha256: flags.get("--launcher-sha256"),
  };
}

async function runCli() {
  try {
    const options = parseCliArguments(process.argv.slice(2));
    const result = await validateCodeOssRuntime(options);
    process.stdout.write(`${JSON.stringify({
      schemaVersion: 1,
      platform: options.platform,
      launcherRelativePath: result.launcherRelativePath,
      launcherSha256: result.launcherSha256,
      applicationName: result.productIdentity.applicationName,
      nameShort: result.productIdentity.nameShort,
      licenseName: result.productIdentity.licenseName,
    })}\n`);
  } catch (error) {
    process.stderr.write(`${error?.message ?? "RELEASE_INPUT_INVALID: runtime validation failed"}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await runCli();
