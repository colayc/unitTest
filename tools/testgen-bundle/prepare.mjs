import { execFile as execFileCallback } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import { chmod, copyFile, lstat, mkdir, mkdtemp, readFile, realpath, rename, rm, writeFile } from "node:fs/promises";
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

async function listArchivePaths(archivePath) {
  const { stdout } = await execFile("tar", ["-tf", archivePath], {
    encoding: "utf8",
    maxBuffer: 128 * 1024 * 1024,
    windowsHide: true,
  });
  const entries = stdout.split(/\r?\n/u).filter(Boolean);
  validateArchiveEntries(entries);
  return entries;
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

async function verifyNoSelectedArchiveLinks(extractRoot, spec) {
  const root = join(extractRoot, spec.archiveRoot);
  for (const path of [spec.executable, spec.resourceDir, `${spec.resourceDir}/include`]) {
    let current = root;
    for (const segment of path.split("/")) {
      current = join(current, segment);
      const info = await lstat(current);
      if (info.isSymbolicLink()) throw new Error(`selected archive path is a symlink or reparse point: ${path}`);
    }
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
  const sourceManifest = validateSourceManifest(manifest ?? JSON.parse(sourceBytes.toString("utf8")));
  const key = platform ?? (process.platform === "win32" ? "windows-x64" : process.platform === "linux" ? "linux-x64" : "");
  const spec = sourceManifest.platforms[key];
  if (!spec) throw new Error("unsupported Clang bundle platform");
  const resolvedCacheRoot = resolve(cacheRoot);
  const finalRoot = join(resolvedCacheRoot, key);
  try {
    await lstat(finalRoot);
    return await checkBundle({ root: finalRoot, platform: key, manifest: sourceManifest, manifestBytes: sourceBytes });
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  await mkdir(resolvedCacheRoot, { recursive: true });
  const temporaryRoot = await mkdtemp(join(resolvedCacheRoot, ".prepare-"));
  try {
    const archivePath = join(temporaryRoot, spec.archive.filename);
    await downloadArchive(spec.archive.url, archivePath, maximumArchiveBytes);
    const archiveInfo = await lstat(archivePath);
    if (!archiveInfo.isFile() || archiveInfo.isSymbolicLink() || archiveInfo.size > maximumArchiveBytes ||
        await sha256File(archivePath) !== spec.archive.sha256) {
      throw new Error("Clang archive SHA-256 digest mismatch");
    }
    const entries = await listArchivePaths(archivePath);
    const selected = [
      `${spec.archiveRoot}/${spec.executable}`,
      `${spec.archiveRoot}/${spec.resourceDir}/include/`,
    ];
    for (const path of selected) {
      if (!entries.includes(path) && !entries.includes(path.replace(/\/$/u, ""))) {
        throw new Error(`missing selected archive entry: ${path}`);
      }
    }
    const extractRoot = join(temporaryRoot, "extracted");
    const bundleRoot = join(temporaryRoot, "bundle");
    await mkdir(extractRoot);
    await mkdir(bundleRoot);
    await copyInventoriedArchiveFiles(archivePath, extractRoot, bundleRoot, spec);
    await verifyNoSelectedArchiveLinks(extractRoot, spec);
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
    await checkBundle({ root: bundleRoot, platform: key, manifest: sourceManifest, manifestBytes: sourceBytes });
    await rename(bundleRoot, finalRoot);
    return await checkBundle({ root: finalRoot, platform: key, manifest: sourceManifest, manifestBytes: sourceBytes });
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
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
