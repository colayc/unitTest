import { execFile as execFileCallback, type ExecFileException } from "node:child_process";
import { createHash } from "node:crypto";
import dns from "node:dns";
import dnsPromises from "node:dns/promises";
import http from "node:http";
import http2 from "node:http2";
import https from "node:https";
import { syncBuiltinESMExports } from "node:module";
import { mkdir, mkdtemp, open, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import net from "node:net";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const DIGEST = /^[0-9a-f]{64}$/u;
const VERSION = /^\d+(?:\.\d+){1,2}$/u;
const NETWORK_NAMES = new Set([
  "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
  "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR",
]);
const HOME_NAMES = new Set(["HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA"]);

export type BundlePlatform = "windows-x64" | "linux-x64";
export type NegativeEvidence = "rejected" | "environment-blocked";

export interface CoverageSmokeOutcome {
  readonly selfCheck: "passed" | "failed";
  readonly descriptor: "passed" | "failed" | "skipped";
  readonly negative: NegativeEvidence;
}

export interface CoverageBundleReport {
  readonly schemaVersion: 1;
  readonly platform: BundlePlatform;
  readonly manifestDigest: string;
  readonly sourceManifestDigest: string;
  readonly versions: { readonly python: string; readonly gcovr: string };
  readonly licenses: readonly string[];
  readonly smoke: CoverageSmokeOutcome;
}

export interface CoverageBundleReportInput {
  readonly manifestDigest: string;
  readonly sourceManifestDigest: string;
  readonly platform: BundlePlatform;
  readonly pythonVersion: string;
  readonly gcovrVersion: string;
  readonly licenses: readonly string[];
  readonly smoke: CoverageSmokeOutcome;
}

export interface CoverageBundleProbeOptions {
  readonly bundleRoot: string;
  readonly reportPath: string;
  readonly platform?: BundlePlatform;
  readonly environment?: NodeJS.ProcessEnv;
  readonly cwd?: string;
  readonly sourceManifestPath?: string;
}

interface ResolvedManifest {
  readonly schemaVersion: number;
  readonly platform: BundlePlatform;
  readonly pythonVersion: string;
  readonly gcovrVersion: string;
}

interface LicenseManifest {
  readonly python?: { readonly license?: string };
  readonly gcovr?: { readonly license?: string };
  readonly packages?: readonly { readonly license?: string }[];
}

const blockedMessage = "coverage bundle network guard blocked network access";
let guardInstalled = false;

/** Remove all host-provided Python, package-manager, proxy, registry and home hints. */
export function sanitizeCoverageEnvironment(environment: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const sanitized: NodeJS.ProcessEnv = {};
  for (const [name, value] of Object.entries(environment)) {
    const upper = name.toUpperCase();
    if (
      upper.startsWith("PYTHON") || upper.startsWith("PIP_") || upper.startsWith("CONDA_") ||
      upper.startsWith("NPM_CONFIG_") || NETWORK_NAMES.has(upper) || HOME_NAMES.has(upper) ||
      upper === "LANG" || upper === "LANGUAGE" || upper.startsWith("LC_") ||
      upper === "VIRTUAL_ENV" || upper === "PYTHONUSERBASE"
    ) continue;
    sanitized[name] = value;
  }
  return sanitized;
}

/** Install a process-local guard for every Node DNS/socket/HTTP entry point. */
export function installCoverageBundleNetworkGuard(): () => void {
  if (guardInstalled) throw new Error("coverage bundle network guard is already installed");
  guardInstalled = true;
  const restorers: Array<() => void> = [];
  try {
    replace(dns, "lookup", blockedNetwork, restorers);
    replace(dns, "resolve", blockedNetwork, restorers);
    replace(dnsPromises, "lookup", blockedNetwork, restorers);
    replace(dnsPromises, "resolve", blockedNetwork, restorers);
    replace(net, "connect", blockedNetwork, restorers);
    replace(net, "createConnection", blockedNetwork, restorers);
    replace(http, "request", blockedNetwork, restorers);
    replace(http, "get", blockedNetwork, restorers);
    replace(https, "request", blockedNetwork, restorers);
    replace(https, "get", blockedNetwork, restorers);
    replace(http2, "connect", blockedNetwork, restorers);
    replace(globalThis, "fetch", blockedNetwork, restorers);
    syncBuiltinESMExports();
  } catch (error) {
    for (const restore of restorers.reverse()) restore();
    syncBuiltinESMExports();
    guardInstalled = false;
    throw error;
  }
  let restored = false;
  return () => {
    if (restored) return;
    restored = true;
    for (const restore of restorers.reverse()) restore();
    syncBuiltinESMExports();
    guardInstalled = false;
  };
}

function blockedNetwork(): never {
  throw new Error(blockedMessage);
}

function replace(target: object, property: PropertyKey, value: unknown, restorers: Array<() => void>): void {
  const descriptor = Object.getOwnPropertyDescriptor(target, property);
  if (!descriptor || !descriptor.configurable && !descriptor.writable) {
    throw new Error(`coverage network guard cannot bind ${String(property)}`);
  }
  Object.defineProperty(target, property, { ...descriptor, value });
  restorers.push(() => Object.defineProperty(target, property, descriptor));
}

export function buildCoverageBundleReport(input: CoverageBundleReportInput): CoverageBundleReport {
  if (!DIGEST.test(input.manifestDigest)) throw new Error("invalid coverage manifest digest");
  if (!DIGEST.test(input.sourceManifestDigest)) throw new Error("invalid source manifest digest");
  if (input.platform !== "windows-x64" && input.platform !== "linux-x64") throw new Error("invalid coverage platform");
  if (!VERSION.test(input.pythonVersion) || !VERSION.test(input.gcovrVersion)) throw new Error("invalid coverage version");
  if (!Array.isArray(input.licenses) || input.licenses.length === 0 || input.licenses.some((value) => !/^[A-Za-z0-9.+-]+$/u.test(value))) {
    throw new Error("invalid coverage license list");
  }
  const smoke = input.smoke;
  if (!smoke || !["passed", "failed"].includes(smoke.selfCheck) || !["passed", "failed", "skipped"].includes(smoke.descriptor)) {
    throw new Error("invalid coverage smoke outcome");
  }
  classifyNegativeEvidence({ status: smoke.negative });
  return {
    schemaVersion: 1,
    platform: input.platform,
    manifestDigest: input.manifestDigest,
    sourceManifestDigest: input.sourceManifestDigest,
    versions: { python: input.pythonVersion, gcovr: input.gcovrVersion },
    licenses: [...new Set(input.licenses)].sort(),
    smoke: { ...smoke },
  };
}

export function validateCoverageBundleReport(report: CoverageBundleReport): void {
  exactKeys(report, ["schemaVersion", "platform", "manifestDigest", "sourceManifestDigest", "versions", "licenses", "smoke"], "coverage report");
  if (report.schemaVersion !== 1 || (report.platform !== "windows-x64" && report.platform !== "linux-x64")) throw new Error("invalid coverage report");
  exactKeys(report.versions, ["python", "gcovr"], "coverage report versions");
  if (!DIGEST.test(report.manifestDigest) || !DIGEST.test(report.sourceManifestDigest) || !report.versions || !VERSION.test(report.versions.python) || !VERSION.test(report.versions.gcovr)) throw new Error("invalid coverage report identity");
  if (!Array.isArray(report.licenses) || report.licenses.length === 0 || report.licenses.some((license) => !/^[A-Za-z0-9.+-]+$/u.test(license))) throw new Error("invalid coverage report licenses");
  if (report.licenses.some((license, index) => index > 0 && report.licenses[index - 1]! >= license)) throw new Error("coverage report licenses must be unique and sorted");
  exactKeys(report.smoke, ["selfCheck", "descriptor", "negative"], "coverage report smoke");
  if (!report.smoke || !["passed", "failed"].includes(report.smoke.selfCheck) || !["passed", "failed", "skipped"].includes(report.smoke.descriptor)) throw new Error("invalid coverage report smoke");
  classifyNegativeEvidence({ status: report.smoke.negative });
}

function exactKeys(value: unknown, expected: readonly string[], label: string): void {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) throw new Error(`${label} has unexpected fields`);
}

export function classifyNegativeEvidence(result: { readonly status: unknown; readonly code?: unknown }): NegativeEvidence {
  if (result.status === "rejected" && (result.code === undefined || result.code === 2)) return "rejected";
  if (result.status === "environment-blocked" && result.code === undefined) return "environment-blocked";
  if (result.status === "error" && (result.code === "ENOENT" || result.code === "EPERM")) return "environment-blocked";
  throw new Error("missing negative evidence or negative case was accepted");
}

export async function writeCoverageBundleReport(path: string, report: CoverageBundleReport): Promise<void> {
  validateCoverageBundleReport(report);
  if (!isAbsolute(path) || path.includes("\0")) throw new Error("coverage report path must be absolute");
  await mkdir(dirname(path), { recursive: true, mode: 0o700 });
  const temporary = `${path}.${process.pid}.tmp`;
  const handle = await open(temporary, "wx", 0o600);
  let closed = false;
  try {
    await handle.writeFile(`${JSON.stringify(report, null, 2)}\n`, "utf8");
    await handle.sync();
  } catch (error) {
    await handle.close().then(() => { closed = true; }).catch(() => undefined);
    await rm(temporary, { force: true });
    throw error;
  } finally {
    if (!closed) {
      await handle.close();
      closed = true;
    }
  }
  try {
    await rename(temporary, path);
  } catch (error) {
    await rm(temporary, { force: true });
    throw error;
  }
}

export async function runCoverageBundleProbe(options: CoverageBundleProbeOptions): Promise<CoverageBundleReport> {
  if (!isAbsolute(options.bundleRoot) || !isAbsolute(options.reportPath) || options.bundleRoot.includes("\0") || options.reportPath.includes("\0")) {
    throw new Error("coverage probe paths must be absolute");
  }
  await mkdir(dirname(options.reportPath), { recursive: true, mode: 0o700 });
  const bundleRoot = resolve(options.bundleRoot);
  const resolvedPath = join(bundleRoot, "manifest.resolved.json");
  const manifestBytes = await readFile(resolvedPath);
  const resolved = JSON.parse(manifestBytes.toString("utf8")) as ResolvedManifest;
  if (resolved.schemaVersion !== 1 || (resolved.platform !== "windows-x64" && resolved.platform !== "linux-x64")) throw new Error("invalid resolved coverage manifest");
  const platform = options.platform ?? resolved.platform;
  if (platform !== resolved.platform) throw new Error("coverage bundle platform mismatch");
  const manifestDigest = createHash("sha256").update(manifestBytes).digest("hex");
  const sourceManifestPath = options.sourceManifestPath ?? join(resolve(import.meta.dirname, "../../.."), "tools", "coverage-bundle", "manifest.json");
  if (!isAbsolute(sourceManifestPath) || sourceManifestPath.includes("\0")) throw new Error("source manifest path must be absolute");
  const sourceManifestDigest = createHash("sha256").update(await readFile(sourceManifestPath)).digest("hex");
  const licenseBytes = await readFile(join(bundleRoot, "licenses", "dependencies.json"), "utf8");
  const licenses = licensesFromManifest(JSON.parse(licenseBytes) as LicenseManifest);
  const executable = platform === "windows-x64" ? join(bundleRoot, "python", "python.exe") : join(bundleRoot, "python", "bin", "python3");
  const application = join(bundleRoot, "app", "gcovr-runner.pyz");
  await stat(executable);
  await stat(application);
  const environment = sanitizeCoverageEnvironment(options.environment ?? process.env);
  const cwd = options.cwd ?? dirname(options.reportPath);
  const run = async (args: readonly string[]) => execFile(executable, ["-I", "-S", application, ...args], {
    cwd,
    env: environment,
    windowsHide: true,
    timeout: 60_000,
    maxBuffer: 1024 * 1024,
  });
  let selfCheck: "passed" | "failed" = "failed";
  let executionBlocked = false;
  try {
    const result = await run(["--self-check"]);
    const value = JSON.parse(result.stdout.trim()) as { python?: string; gcovr?: string };
    if (value.python !== resolved.pythonVersion || value.gcovr !== resolved.gcovrVersion) throw new Error("coverage runner version mismatch");
    selfCheck = "passed";
  } catch (error) {
    const code = (error as ExecFileException & { code?: string | number }).code;
    if (code === "ENOENT" || code === "EPERM") {
      executionBlocked = true;
    } else {
      throw new Error(`coverage bundle self-check failed: ${error instanceof Error ? error.message : String(error)}`, { cause: error });
    }
  }
  if (executionBlocked) {
    const report = buildCoverageBundleReport({
      manifestDigest,
      sourceManifestDigest,
      platform,
      pythonVersion: resolved.pythonVersion,
      gcovrVersion: resolved.gcovrVersion,
      licenses,
      smoke: { selfCheck, descriptor: "skipped", negative: "environment-blocked" },
    });
    await writeCoverageBundleReport(options.reportPath, report);
    return report;
  }
  const smokeRoot = await mkdtemp(join(dirname(options.reportPath), `.coverage-probe-${process.pid}-`));
  const descriptorRoot = join(smokeRoot, "root");
  const descriptorObjects = join(smokeRoot, "objects");
  const descriptorOutput = join(smokeRoot, "coverage.json");
  const descriptorPath = join(smokeRoot, "descriptor.json");
  await mkdir(descriptorRoot, { recursive: true, mode: 0o700 });
  await mkdir(descriptorObjects, { recursive: true, mode: 0o700 });
  await writeFile(descriptorPath, `${JSON.stringify({
    schemaVersion: 1,
    root: descriptorRoot,
    objectDirectory: descriptorObjects,
    gcovExecutable: executable,
    outputPath: descriptorOutput,
  })}\n`, { flag: "wx", mode: 0o600 });
  let descriptor: "passed" | "failed" = "failed";
  try {
    await run([descriptorPath]);
    const output = JSON.parse(await readFile(descriptorOutput, "utf8")) as { files?: unknown };
    if (!output || !Array.isArray(output.files)) throw new Error("coverage descriptor smoke produced invalid JSON");
    descriptor = "passed";
  } catch (error) {
    await rm(smokeRoot, { recursive: true, force: true });
    throw new Error(`coverage descriptor smoke failed: ${error instanceof Error ? error.message : String(error)}`, { cause: error });
  }
  const negativePath = join(smokeRoot, "negative.json");
  await writeFile(negativePath, `${JSON.stringify({ schemaVersion: 1, unknown: true })}\n`, { flag: "wx", mode: 0o600 });
  let negative: NegativeEvidence;
  try {
    try {
      await run([negativePath]);
      throw new Error("negative descriptor was accepted");
    } catch (error) {
      const code = (error as ExecFileException & { code?: string | number }).code;
      if (code === "ENOENT" || code === "EPERM") negative = classifyNegativeEvidence({ status: "error", code });
      else if (typeof code === "number") negative = classifyNegativeEvidence({ status: "rejected", code });
      else throw error;
    }
  } finally {
    await rm(smokeRoot, { recursive: true, force: true });
  }
  const report = buildCoverageBundleReport({
    manifestDigest,
    sourceManifestDigest,
    platform,
    pythonVersion: resolved.pythonVersion,
    gcovrVersion: resolved.gcovrVersion,
    licenses,
    smoke: { selfCheck, descriptor, negative },
  });
  await writeCoverageBundleReport(options.reportPath, report);
  return report;
}

function licensesFromManifest(manifest: LicenseManifest): string[] {
  const values = [manifest.python?.license, manifest.gcovr?.license, ...(manifest.packages ?? []).map((item) => item.license)];
  const result = values.filter((value): value is string => typeof value === "string" && /^[A-Za-z0-9.+-]+$/u.test(value));
  if (result.length < 2) throw new Error("coverage license manifest is incomplete");
  return [...new Set(result)].sort();
}

export const __testing = Object.freeze({ blockedMessage, licensesFromManifest });

async function main(arguments_: readonly string[] = process.argv.slice(2)): Promise<void> {
  if (arguments_.length !== 0 && (arguments_.length !== 4 || arguments_[0] !== "--bundle" || arguments_[2] !== "--report")) {
    throw new Error("usage: coverage-bundle probe [--bundle <root> --report <path>]");
  }
  const repositoryRoot = resolve(import.meta.dirname, "../../..");
  const platform: BundlePlatform = process.platform === "win32" ? "windows-x64" : "linux-x64";
  const bundleRoot = arguments_.length === 4
    ? arguments_[1]!
    : join(repositoryRoot, ".superpowers", "runtime", "coverage-bundle", platform);
  const reportPath = arguments_.length === 4
    ? arguments_[3]!
    : join(repositoryRoot, ".native-e2e", "artifacts", process.platform === "win32" ? "windows" : "linux", "coverage-bundle-report.json");
  const restore = installCoverageBundleNetworkGuard();
  try {
    await runCoverageBundleProbe({ bundleRoot, reportPath, platform, cwd: dirname(reportPath) });
  } finally {
    restore();
  }
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  main().catch((error: unknown) => {
    process.stderr.write(`coverage-bundle: ${error instanceof Error ? error.stack ?? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}

export const __cli = Object.freeze({ main });
