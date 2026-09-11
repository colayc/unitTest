import { copyFile, lstat, mkdir, mkdtemp, readdir, rename, rm, readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const failureCode = "RELEASE_QUALIFIED_STAGING_FAILED";
const digestPattern = /^[0-9a-f]{64}$/u;
const versionPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const requiredKeys = ["linuxLicenseAudit", "linuxManifest", "linuxManifestSha256", "linuxPackage", "linuxPackageManifest", "outRoot", "qualification", "version", "windowsLicenseAudit", "windowsManifest", "windowsManifestSha256", "windowsPackage"].sort((a, b) => a.localeCompare(b, "en"));
function stagingFailure(message) { const error = new Error(`${failureCode}: ${message}`); error.code = failureCode; return error; }
async function sha256File(path) { return createHash("sha256").update(await readFile(path)).digest("hex"); }
function destinationRecords(input) { return [["Windows package", input.windowsPackage, `unit-test-ide-${input.version}.msix`], ["Windows manifest", input.windowsManifest, `unit-test-ide-${input.version}.windows-x64.release-manifest.json`], ["Windows license audit", input.windowsLicenseAudit, "license-audit-windows.json"], ["Linux package", input.linuxPackage, `unit-test-ide-${input.version}.AppImage`], ["Linux package manifest", input.linuxPackageManifest, `unit-test-ide-${input.version}.AppImage.sha256.json`], ["Linux manifest", input.linuxManifest, `unit-test-ide-${input.version}.linux-x64.release-manifest.json`], ["Linux license audit", input.linuxLicenseAudit, "license-audit-linux.json"], ["Qualification", input.qualification, "release-qualification.json"]]; }
function validateShape(input) {
  if (input === null || typeof input !== "object" || Array.isArray(input) || Object.getPrototypeOf(input) !== Object.prototype) throw stagingFailure("input must be a plain object");
  const keys = Object.keys(input).sort((a, b) => a.localeCompare(b, "en"));
  if (keys.length !== requiredKeys.length || keys.some((key, i) => key !== requiredKeys[i])) throw stagingFailure("input keys are invalid");
  if (typeof input.version !== "string" || !versionPattern.test(input.version)) throw stagingFailure("version is invalid");
  for (const key of ["windowsManifestSha256", "linuxManifestSha256"]) if (typeof input[key] !== "string" || !digestPattern.test(input[key])) throw stagingFailure(`${key} is invalid`);
  for (const [label, source, expected] of destinationRecords(input)) { if (typeof source !== "string" || source.length === 0) throw stagingFailure(`${label} path is invalid`); const sourceName = label.endsWith("manifest") && !label.includes("package") ? `unit-test-ide-${input.version}.release-manifest.json` : expected; if (basename(source) !== sourceName) throw stagingFailure(`${label} basename is invalid`); }
}
async function validateSources(input) { for (const [label, source] of destinationRecords(input)) { const info = await lstat(source); if (info.isSymbolicLink()) throw stagingFailure(`${label} must be a real file`); if (!info.isFile()) throw stagingFailure(`${label} must be a regular file`); } }
async function pathExists(path) { try { await lstat(path); return true; } catch (error) { if (error.code === "ENOENT") return false; throw error; } }

export async function stageQualifiedRelease(input) {
  let temporaryRoot;
  try {
    validateShape(input); await validateSources(input);
    if (await sha256File(input.windowsManifest) !== input.windowsManifestSha256) throw stagingFailure("Windows manifest SHA-256 does not match");
    if (await sha256File(input.linuxManifest) !== input.linuxManifestSha256) throw stagingFailure("Linux manifest SHA-256 does not match");
    const finalRoot = resolve(input.outRoot); if (await pathExists(finalRoot)) throw stagingFailure("qualified output already exists");
    await mkdir(dirname(finalRoot), { recursive: true }); temporaryRoot = await mkdtemp(`${finalRoot}.tmp-`);
    const records = destinationRecords(input); for (const [, source, destination] of records) await copyFile(source, join(temporaryRoot, destination));
    if (await sha256File(join(temporaryRoot, records[1][2])) !== input.windowsManifestSha256) throw stagingFailure("Windows manifest SHA-256 does not match");
    if (await sha256File(join(temporaryRoot, records[5][2])) !== input.linuxManifestSha256) throw stagingFailure("Linux manifest SHA-256 does not match");
    const entries = await readdir(temporaryRoot, { withFileTypes: true }); const files = entries.map(({ name }) => name).sort((a, b) => a.localeCompare(b, "en")); const expectedFiles = records.map(([, , destination]) => destination).sort((a, b) => a.localeCompare(b, "en"));
    if (files.length !== expectedFiles.length || files.some((name, i) => name !== expectedFiles[i])) throw stagingFailure("qualified output has an unexpected file set");
    if (entries.some((entry) => !entry.isFile() || entry.isSymbolicLink())) throw stagingFailure("qualified output contains a non-regular file");
    await rename(temporaryRoot, finalRoot); temporaryRoot = undefined; return { outputRoot: finalRoot, files };
  } catch (error) { if (temporaryRoot) { try { await rm(temporaryRoot, { recursive: true, force: true }); } catch {} } if (error?.code === failureCode) throw error; throw stagingFailure("qualified release assembly failed"); }
}
const cliMap = new Map([["--version", "version"], ["--out", "outRoot"], ["--windows-package", "windowsPackage"], ["--windows-manifest", "windowsManifest"], ["--windows-manifest-sha256", "windowsManifestSha256"], ["--windows-license-audit", "windowsLicenseAudit"], ["--linux-package", "linuxPackage"], ["--linux-package-manifest", "linuxPackageManifest"], ["--linux-manifest", "linuxManifest"], ["--linux-manifest-sha256", "linuxManifestSha256"], ["--linux-license-audit", "linuxLicenseAudit"], ["--qualification", "qualification"]]);
async function main(args) { const input = {}; for (let i = 0; i < args.length; i += 1) { const flag = args[i]; const key = cliMap.get(flag); if (!key) { const safeFlag = typeof flag === "string" && /^--[A-Za-z0-9-]+$/u.test(flag) ? flag : "<token>"; throw stagingFailure(`unknown argument: ${safeFlag}`); } if (Object.hasOwn(input, key)) throw stagingFailure(`duplicate argument: ${flag}`); if (i + 1 >= args.length || args[i + 1].startsWith("--")) throw stagingFailure(`missing value for ${flag}`); input[key] = args[++i]; } const result = await stageQualifiedRelease(input); process.stdout.write(`${JSON.stringify(result)}\n`); }
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) { main(process.argv.slice(2)).catch((error) => { const safe = error?.code === failureCode ? error : stagingFailure("qualified release assembly failed"); console.error(safe.message); process.exitCode = 1; }); }
