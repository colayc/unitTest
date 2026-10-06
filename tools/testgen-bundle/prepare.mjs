import { execFile as execFileCallback } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import { chmod, copyFile, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import { checkBundle, validateSourceManifest } from "./check.mjs";

const execFile = promisify(execFileCallback);
const toolDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDir, "..", "..");
const maximumArchiveBytes = 2_200_000_000;
const maximumLicenseBytes = 64 * 1024;
const sourceHosts = new Set(["github.com", "raw.githubusercontent.com"]);
const redirectHosts = new Set(["release-assets.githubusercontent.com", "objects.githubusercontent.com", "raw.githubusercontent.com"]);

function safeArchivePath(path) {
  return typeof path === "string" && path.length > 0 && !path.startsWith("/") && !/^[A-Za-z]:/u.test(path) &&
    !path.includes("\\") && !path.includes("\0") &&
    !path.split("/").some((part) => part === ".." || part === ".");
}

export function validateArchiveEntries(entries) {
  const seen = new Set();
  for (const entry of entries) {
    if (!safeArchivePath(entry)) throw new Error(`unsafe archive traversal or absolute path: ${entry}`);
    const path = entry.replace(/\/$/u, "");
    const folded = path.toLowerCase();
    if (seen.has(folded)) throw new Error(`archive duplicate or case-alias path: ${path}`);
    seen.add(folded);
  }
  return entries;
}

function selectedPathSets(spec) {
  const files = new Set(spec.files.map(({ path }) => `${spec.archiveRoot}/${path}`));
  const directories = new Set();
  for (const file of files) {
    const parts = file.split("/");
    for (let count = 1; count < parts.length; count += 1) {
      directories.add(parts.slice(0, count).join("/"));
    }
  }
  return { files, directories };
}

export function validateSelectedArchiveEntries(entries, spec) {
  validateArchiveEntries(entries.map(({ path }) => path));
  const { files, directories } = selectedPathSets(spec);
  const includePrefix = `${spec.archiveRoot}/${spec.resourceDir}/include`;
  const seenFiles = new Set();
  for (const { path, type, target } of entries) {
    const normalized = path.replace(/\/$/u, "");
    const selected = files.has(normalized) || directories.has(normalized) || normalized.startsWith(`${includePrefix}/`);
    if (!selected) continue;
    if (type === "l" || type === "h") {
      throw new Error(`selected archive link is forbidden: ${path}${target ? ` -> ${target}` : ""}`);
    }
    if (files.has(normalized)) {
      if (type !== "-") throw new Error(`selected archive file has unsupported type: ${path}`);
      seenFiles.add(normalized);
    } else if (directories.has(normalized)) {
      if (type !== "d") throw new Error(`selected archive directory has unsupported type: ${path}`);
    } else {
      throw new Error(`unlisted selected archive entry: ${path}`);
    }
  }
  for (const file of files) {
    if (!seenFiles.has(file)) throw new Error(`missing selected archive file: ${file}`);
  }
  return entries;
}

export async function verifyExtractedSelection(extractRoot, spec) {
  const sourceRoot = join(extractRoot, spec.archiveRoot);
  const { files, directories } = selectedPathSets(spec);
  const seenFiles = new Set();
  const seenDirs = new Set();
  async function walk(path, relativePath) {
    const info = await lstat(path);
    if (info.isSymbolicLink()) throw new Error(`selected extraction contains a link: ${relativePath}`);
    if (info.isDirectory()) {
      if (!directories.has(relativePath)) throw new Error(`extra selected extraction directory: ${relativePath}`);
      seenDirs.add(relativePath);
      for (const name of await readdir(path)) await walk(join(path, name), `${relativePath}/${name}`);
    } else if (info.isFile()) {
      if (!files.has(relativePath)) throw new Error(`unlisted selected extraction file: ${relativePath}`);
      seenFiles.add(relativePath);
    } else {
      throw new Error(`unsupported selected extraction entry: ${relativePath}`);
    }
  }
  await walk(sourceRoot, spec.archiveRoot);
  for (const path of files) if (!seenFiles.has(path)) throw new Error(`missing selected extraction file: ${path}`);
  for (const path of directories) if (!seenDirs.has(path)) throw new Error(`missing selected extraction directory: ${path}`);
}

export async function publishCheckedBundle({ candidateRoot, finalRoot, temporaryRoot, verify }) {
  await verify(candidateRoot);
  let previousExists = false;
  try {
    await lstat(finalRoot);
    previousExists = true;
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  if (previousExists) {
    try {
      return await verify(finalRoot);
    } catch {
      // Only a verified existing cache can win a concurrent preparation race.
    }
  }
  const previousRoot = join(temporaryRoot, "replaced-cache");
  let movedPrevious = false;
  let published = false;
  let result;
  try {
    if (previousExists) {
      await rename(finalRoot, previousRoot);
      movedPrevious = true;
    }
    await rename(candidateRoot, finalRoot);
    published = true;
    result = await verify(finalRoot);
  } catch (error) {
    try {
      if (published) await rename(finalRoot, candidateRoot);
      if (movedPrevious) await rename(previousRoot, finalRoot);
    } catch (rollbackError) {
      const failure = new AggregateError([error, rollbackError], "Clang cache publication rollback failed");
      failure.preserveTemporaryRoot = true;
      throw failure;
    }
    throw error;
  }
  if (movedPrevious) await rm(previousRoot, { recursive: true, force: true });
  return result;
}

export { validateSourceManifest };

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

function withinRoot(root, candidate) {
  const part = relative(root, candidate);
  return part === "" || (!part.startsWith("..") && !isAbsolute(part));
}

async function downloadVerifiedSource(url, destination, maximumBytes) {
  let current = url;
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    const parsed = new URL(current);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password ||
        !(redirects === 0 ? sourceHosts : redirectHosts).has(parsed.hostname) ||
        (redirects === 0 && (parsed.search || parsed.hash))) {
      throw new Error(`source URL is outside the reviewed HTTPS boundary: ${current}`);
    }
    const response = await fetch(current, { redirect: "manual", signal: AbortSignal.timeout(15 * 60_000) });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get("location");
      if (!location) throw new Error("source redirect lacks Location");
      current = new URL(location, current).href;
      continue;
    }
    if (!response.ok || !response.body) throw new Error(`source download failed: HTTP ${response.status}`);
    const statedLength = Number(response.headers.get("content-length"));
    if (Number.isFinite(statedLength) && statedLength > maximumBytes) throw new Error("source download exceeds byte budget");
    let total = 0;
    const counter = new Transform({
      transform(chunk, _encoding, callback) {
        total += chunk.length;
        if (total > maximumBytes) callback(new Error("source download exceeds byte budget"));
        else callback(null, chunk);
      },
    });
    await pipeline(Readable.fromWeb(response.body), counter, createWriteStream(destination, { flags: "wx" }));
    return;
  }
  throw new Error("source redirect limit exceeded");
}

export function parseArchiveListing(stdout, spec) {
  const lines = stdout.split(/\r?\n/u).filter(Boolean);
  const archiveRootPrefix = `${spec.archiveRoot}/`;
  const entries = lines.map((line) => {
    const type = line[0];
    // GNU tar and bsdtar use different numbers of date/owner columns. The
    // reviewed archive root is the stable delimiter; parse from it instead of
    // assuming one platform-specific verbose-column layout.
    const pathStart = line.indexOf(archiveRootPrefix);
    if (pathStart < 0) throw new Error(`archive verbose listing has no reviewed root: ${line}`);
    const marker = type === "l" ? " -> " : type === "h" ? " link to " : "";
    const listed = line.slice(pathStart);
    const markerIndex = marker ? listed.lastIndexOf(marker) : -1;
    return {
      path: markerIndex >= 0 ? listed.slice(0, markerIndex) : listed,
      type,
      target: markerIndex >= 0 ? listed.slice(markerIndex + marker.length) : undefined,
    };
  });
  return validateSelectedArchiveEntries(entries, spec);
}

async function listArchiveEntries(archivePath, spec) {
  const options = {
    encoding: "utf8",
    maxBuffer: 128 * 1024 * 1024,
    windowsHide: true,
  };
  // A verbose listing already contains every archive path and its entry type.
  // Running `tar -tf` and `tar -tvf` in parallel decompresses the same large
  // LLVM archive twice and can exceed the Windows job timeout. Parse one
  // detailed listing instead while retaining the existing link/type checks.
  const { stdout } = await execFile("tar", ["-tvf", archivePath], options);
  return parseArchiveListing(stdout, spec);
}

async function copyInventoriedArchiveFiles(archivePath, extractRoot, bundleRoot, spec) {
  const selected = [
    `${spec.archiveRoot}/${spec.executable}`,
    `${spec.archiveRoot}/${spec.resourceDir}/include`,
  ];
  await execFile("tar", ["-xf", archivePath, "-C", extractRoot, ...selected], {
    maxBuffer: 1024 * 1024,
    windowsHide: true,
  });
  await verifyExtractedSelection(extractRoot, spec);
  const sourceRoot = join(extractRoot, spec.archiveRoot);
  const canonicalRoot = await realpath(sourceRoot);
  for (const file of spec.files) {
    const source = join(sourceRoot, ...file.path.split("/"));
    const info = await lstat(source);
    if (!info.isFile() || info.isSymbolicLink() || info.size !== file.size ||
        !withinRoot(canonicalRoot, await realpath(source))) {
      throw new Error(`archive entry is missing, linked, or substituted: ${file.path}`);
    }
    if (await sha256File(source) !== file.sha256) throw new Error(`archive file SHA-256 digest mismatch: ${file.path}`);
    const destination = join(bundleRoot, ...file.path.split("/"));
    await mkdir(dirname(destination), { recursive: true });
    await copyFile(source, destination);
    if (file.path === spec.executable && spec.target === "linux-x64") await chmod(destination, 0o755);
  }
}

export async function prepareBundle({
  manifest,
  manifestBytes,
  platform,
  cacheRoot = join(repositoryRoot, ".superpowers", "cache", "testgen-bundle", "22.1.8"),
  downloadArchive = downloadVerifiedSource,
} = {}) {
  const sourceBytes = manifestBytes ?? (manifest ? Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`) : await readFile(join(toolDir, "manifest.json")));
  const sourceManifest = validateSourceManifest(manifest ?? JSON.parse(sourceBytes.toString("utf8")), sourceBytes);
  const key = platform ?? (process.platform === "win32" ? "windows-x64" : process.platform === "linux" ? "linux-x64" : "");
  const spec = sourceManifest.platforms[key];
  if (!spec) throw new Error("unsupported Clang bundle platform");
  const resolvedCacheRoot = resolve(cacheRoot);
  const finalRoot = join(resolvedCacheRoot, key);
  try {
    await lstat(finalRoot);
    try {
      return await checkBundle({ root: finalRoot, platform: key, manifest: sourceManifest, manifestBytes: sourceBytes });
    } catch {
      // A corrupt or stale final cache is replaced only after a new tree verifies.
    }
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  await mkdir(resolvedCacheRoot, { recursive: true });
  const temporaryRoot = await mkdtemp(join(resolvedCacheRoot, ".prepare-"));
  let preserveTemporaryRoot = false;
  try {
    const archivePath = join(temporaryRoot, spec.archive.filename);
    await downloadArchive(spec.archive.url, archivePath, maximumArchiveBytes);
    const archiveInfo = await lstat(archivePath);
    if (!archiveInfo.isFile() || archiveInfo.isSymbolicLink() || archiveInfo.size > maximumArchiveBytes ||
        await sha256File(archivePath) !== spec.archive.sha256) {
      throw new Error("Clang archive SHA-256 digest mismatch");
    }
    const entries = await listArchiveEntries(archivePath, spec);
    const selected = [
      `${spec.archiveRoot}/${spec.executable}`,
      `${spec.archiveRoot}/${spec.resourceDir}/include/`,
    ];
    for (const path of selected) {
      if (!entries.some((entry) => entry.path === path || entry.path === path.replace(/\/$/u, ""))) {
        throw new Error(`missing selected archive entry: ${path}`);
      }
    }
    const extractRoot = join(temporaryRoot, "extracted");
    const bundleRoot = join(temporaryRoot, "bundle");
    await mkdir(extractRoot);
    await mkdir(bundleRoot);
    await copyInventoriedArchiveFiles(archivePath, extractRoot, bundleRoot, spec);
    for (const license of spec.licenses) {
      const destination = join(bundleRoot, ...license.path.split("/"));
      await mkdir(dirname(destination), { recursive: true });
      await downloadArchive(license.url, destination, maximumLicenseBytes);
      const info = await lstat(destination);
      if (!info.isFile() || info.isSymbolicLink() || info.size !== license.size ||
          await sha256File(destination) !== license.sha256) {
        throw new Error(`upstream license SHA-256 digest mismatch: ${license.path}`);
      }
    }
    const manifestSha256 = createHash("sha256").update(sourceBytes).digest("hex");
    await writeFile(join(bundleRoot, "manifest.json"), sourceBytes, { flag: "wx" });
    await writeFile(join(bundleRoot, "READY"), `${JSON.stringify({ schemaVersion: 1, platform: key, manifestSha256 })}\n`, { flag: "wx" });
    return await publishCheckedBundle({
      candidateRoot: bundleRoot,
      finalRoot,
      temporaryRoot,
      verify: (root) => checkBundle({ root, platform: key, manifest: sourceManifest, manifestBytes: sourceBytes }),
    });
  } catch (error) {
    preserveTemporaryRoot = error?.preserveTemporaryRoot === true;
    throw error;
  } finally {
    if (!preserveTemporaryRoot) await rm(temporaryRoot, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const platformFlag = args.indexOf("--platform");
  if (args.length > 2 || (args.length > 0 && (platformFlag !== 0 || !args[1]))) {
    process.stderr.write("Usage: node prepare.mjs [--platform <windows-x64|linux-x64>]\n");
    process.exitCode = 1;
  } else {
    prepareBundle({ platform: platformFlag === 0 ? args[1] : undefined })
      .then(({ root }) => process.stdout.write(`${root}\n`))
      .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
  }
}
