import { lstat, mkdir, open, readFile, rename, rm } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import type { NativeScenarioResult, PreparedCMakeBundle } from "./native-build.js";
import {
  buildFrameworkPlatformReport,
  type FrameworkPlatform,
  type FrameworkPlatformReport,
} from "./native-framework-report.js";

export interface NativeToolchainReport {
  schemaVersion: 1;
  platform: NodeJS.Platform;
  architecture: string;
  cmake: {
    version: string;
    archiveSha256: string;
  };
  results: readonly NativeScenarioResult[];
}

export type CoverageBackend = "linux-gcc" | "linux-clang" | "windows-clang-cl";

export interface CoverageBackendRow {
  readonly schemaVersion: 1;
  readonly candidateCommit: string;
  readonly backend: CoverageBackend;
  readonly status: "passed";
  readonly runnerImage: string;
  // SHA-256 of the producer's verified compiler/toolchain identity. GCC's
  // snapshot identity is not misrepresented as an executable-file digest.
  readonly compiler: { readonly family: "gcc" | "clang" | "clang-cl"; readonly version: string; readonly sha256: string };
  readonly summary: {
    readonly functions: { readonly covered: number; readonly total: number };
    readonly lines: { readonly covered: number; readonly total: number };
    readonly branches: { readonly covered: number; readonly total: number };
  };
  readonly sourceArtifactSha256: string;
}

export interface CoverageBackendReport {
  readonly schemaVersion: 1;
  readonly candidateCommit: string;
  readonly rows: readonly CoverageBackendRow[];
}

const coverageBackends = ["linux-gcc", "linux-clang", "windows-clang-cl"] as const;
const sha256 = /^[0-9a-f]{64}$/u;
const commitSha = /^[0-9a-f]{40}$/u;

function exactKeys(value: unknown, keys: readonly string[], label: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value) ||
    Object.keys(value).sort().join("\0") !== [...keys].sort().join("\0")) {
    throw new Error(`coverage backend ${label} must be closed`);
  }
  return value as Record<string, unknown>;
}

function validMetric(value: unknown): boolean {
  const metric = exactKeys(value, ["covered", "total"], "coverage totals");
  return Number.isSafeInteger(metric.covered) && Number.isSafeInteger(metric.total) &&
    (metric.covered as number) > 0 && (metric.covered as number) <= (metric.total as number);
}

function validateSummary(value: unknown): void {
  const summary = exactKeys(value, ["functions", "lines", "branches"], "coverage totals");
  if (![summary.functions, summary.lines, summary.branches].every(validMetric)) {
    throw new Error("required coverage backend has invalid coverage totals");
  }
}

function validateCoverageBackendRow(value: unknown, candidateCommit: string, expected: CoverageBackend): asserts value is CoverageBackendRow {
  const row = exactKeys(value, ["schemaVersion", "candidateCommit", "backend", "status", "runnerImage", "compiler", "summary", "sourceArtifactSha256"], "row");
  if (row.schemaVersion !== 1 || row.backend !== expected || row.status !== "passed") {
    throw new Error("required coverage backend is missing or skipped");
  }
  if (row.candidateCommit !== candidateCommit) throw new Error("coverage backend candidate mismatch");
  if (typeof row.runnerImage !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/u.test(row.runnerImage)) {
    throw new Error("coverage backend runner image is invalid");
  }
  const compiler = exactKeys(row.compiler, ["family", "version", "sha256"], "compiler");
  const expectedFamily = expected === "linux-gcc" ? "gcc" : expected === "linux-clang" ? "clang" : "clang-cl";
  if (compiler.family !== expectedFamily || typeof compiler.version !== "string" ||
    !/^[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[+.-][A-Za-z0-9]+)*$/u.test(compiler.version) ||
    typeof compiler.sha256 !== "string" || !sha256.test(compiler.sha256)) {
    throw new Error("coverage backend compiler identity is invalid");
  }
  validateSummary(row.summary);
  if (typeof row.sourceArtifactSha256 !== "string" || !sha256.test(row.sourceArtifactSha256)) {
    throw new Error("coverage backend source digest is invalid");
  }
}

export function buildCoverageBackendReport(candidateCommit: string, input: readonly unknown[]): CoverageBackendReport {
  if (!commitSha.test(candidateCommit)) throw new Error("coverage backend candidate is invalid");
  if (input.length !== coverageBackends.length) throw new Error("required coverage backend rows are incomplete");
  for (let index = 0; index < coverageBackends.length; index++) {
    validateCoverageBackendRow(input[index], candidateCommit, coverageBackends[index]!);
  }
  if (new Set((input as readonly CoverageBackendRow[]).map((row) => row.sourceArtifactSha256)).size !== input.length) {
    throw new Error("required coverage backend source digests must be unique");
  }
  return { schemaVersion: 1, candidateCommit, rows: input as readonly CoverageBackendRow[] };
}

export interface NativeLLVMFixtureEvidence {
  readonly compilerVersion: string;
  readonly compilerSha256: string;
  readonly summary: CoverageBackendRow["summary"];
  readonly coverageDocumentSha256: string;
}

export function parseNativeLLVMFixtureLog(log: string): NativeLLVMFixtureEvidence {
  if (log.length > 1024 * 1024 || !/--- PASS: TestNativeLinuxLLVMFixture \(/u.test(log) ||
    !/--- PASS: TestNativeLinuxLLVMFixtureCancellationUsesProductionOwners \(/u.test(log) ||
    /--- (?:SKIP|FAIL): TestNativeLinuxLLVMFixture/u.test(log) ||
    !/\bok\s+unit-test-ide\.local\/test-service\/internal\/coveragellvm\s/u.test(log) ||
    !/\bok\s+unit-test-ide\.local\/test-service\/internal\/coverageexec\s/u.test(log)) {
    throw new Error("required native LLVM fixture did not pass");
  }
  const markers = [...log.matchAll(/UTIDE_NATIVE_LLVM_EVIDENCE=(\{[^\r\n]*\})/gu)];
  if (markers.length !== 1) throw new Error("required native LLVM fixture evidence is missing or duplicated");
  let parsed: unknown;
  try { parsed = JSON.parse(markers[0]![1]!); } catch { throw new Error("required native LLVM fixture evidence is invalid"); }
  const evidence = exactKeys(parsed, ["compilerVersion", "compilerSha256", "summary", "coverageDocumentSha256"], "native LLVM fixture");
  if (typeof evidence.compilerVersion !== "string" || !/^[0-9]+\.[0-9]+(?:\.[0-9]+)?$/u.test(evidence.compilerVersion) ||
    typeof evidence.compilerSha256 !== "string" || !sha256.test(evidence.compilerSha256) ||
    typeof evidence.coverageDocumentSha256 !== "string" || !sha256.test(evidence.coverageDocumentSha256)) {
    throw new Error("required native LLVM fixture compiler identity is invalid");
  }
  validateSummary(evidence.summary);
  return evidence as unknown as NativeLLVMFixtureEvidence;
}

async function readClosedJson(path: string): Promise<unknown> {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || info.size < 2 || info.size > 1024 * 1024) {
    throw new Error("required coverage backend report is unsafe");
  }
  const bytes = await readFile(path);
  const parsed: unknown = JSON.parse(bytes.toString("utf8"));
  if (!bytes.equals(Buffer.from(`${JSON.stringify(parsed)}\n`, "utf8"))) {
    throw new Error("required coverage backend report is not canonical");
  }
  return parsed;
}

async function publishClosedJson(path: string, value: unknown): Promise<void> {
  if (!isAbsolute(path) || path.includes("\0")) throw new Error("coverage backend output path is invalid");
  const directory = resolve(path, "..");
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("coverage backend output directory is unsafe");
  const bytes = Buffer.from(`${JSON.stringify(value)}\n`, "utf8");
  const temporary = `${path}.${process.pid}.tmp`;
  if (await lstat(path).catch(() => undefined)) throw new Error("coverage backend output already exists");
  let handle;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, path);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    throw error;
  }
}

export async function mainCoverageBackendReport(args: readonly string[]): Promise<void> {
  const options = new Map<string, string>();
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index];
    const value = args[index + 1];
    if (!name?.startsWith("--") || value === undefined || options.has(name)) throw new Error("coverage backend arguments are invalid");
    options.set(name, value);
  }
  const mode = options.get("--mode");
  const candidate = options.get("--candidate") ?? "";
  const out = options.get("--out") ?? "";
  if (!commitSha.test(candidate) || !isAbsolute(out)) throw new Error("coverage backend candidate/output is invalid");
  if (mode === "fixture") {
    if (options.size !== 5 || !options.has("--log") || !options.has("--runner-image")) throw new Error("coverage backend fixture arguments are invalid");
    const logPath = options.get("--log")!;
    const logInfo = await lstat(logPath);
    if (!logInfo.isFile() || logInfo.isSymbolicLink() || logInfo.size < 1 || logInfo.size > 1024 * 1024) {
      throw new Error("required native LLVM fixture log is unsafe");
    }
    const log = await readFile(logPath, "utf8");
    const fixture = parseNativeLLVMFixtureLog(log);
    const row = {
      schemaVersion: 1, candidateCommit: candidate, backend: "linux-clang", status: "passed",
      runnerImage: options.get("--runner-image"),
      compiler: { family: "clang", version: fixture.compilerVersion, sha256: fixture.compilerSha256 },
      summary: fixture.summary, sourceArtifactSha256: fixture.coverageDocumentSha256,
    };
    validateCoverageBackendRow(row, candidate, "linux-clang");
    await publishClosedJson(out, row);
    return;
  }
  if (mode === "matrix") {
    if (options.size !== 6 || !options.has("--gcc") || !options.has("--clang") || !options.has("--windows")) throw new Error("coverage backend matrix arguments are invalid");
    const rows = await Promise.all(["--gcc", "--clang", "--windows"].map((key) => readClosedJson(options.get(key)!)));
    await publishClosedJson(out, buildCoverageBackendReport(candidate, rows));
    return;
  }
  throw new Error("coverage backend mode is invalid");
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  mainCoverageBackendReport(process.argv.slice(2)).catch((error: unknown) => {
    process.stderr.write(`native-report: ${error instanceof Error ? error.message : "validation failed"}\n`);
    process.exitCode = 1;
  });
}

export async function writeNativeToolchainReport(
  artifactDirectory: string,
  platform: NodeJS.Platform,
  architecture: string,
  bundle: PreparedCMakeBundle,
  results: readonly NativeScenarioResult[],
): Promise<string> {
  if (!isAbsolute(artifactDirectory) || artifactDirectory.includes("\0")) {
    throw new Error("native artifact directory must be an absolute path");
  }
  const report = buildReport(platform, architecture, bundle, results);
  const directory = resolve(artifactDirectory);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("native artifact directory is unsafe");
  }
  const target = join(directory, "toolchain-report.json");
  const temporary = join(directory, `.toolchain-report-${process.pid}.tmp`);
  const bytes = Buffer.from(`${JSON.stringify(report, null, 2)}\n`, "utf8");
  let handle;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, target);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    throw error;
  }
  return target;
}

export async function verifyRequiredFrameworkReport(
  artifactDirectory: string,
  platform: FrameworkPlatform,
): Promise<FrameworkPlatformReport> {
  if (!isAbsolute(artifactDirectory) || artifactDirectory.includes("\0")) {
    throw new Error("native artifact directory must be an absolute path");
  }
  const target = join(resolve(artifactDirectory), "framework-report.json");
  const info = await lstat(target).catch(() => undefined);
  if (info === undefined) throw new Error("required framework report is missing");
  if (!info.isFile() || info.isSymbolicLink() || info.size < 1 || info.size > 16 * 1024 * 1024) {
    throw new Error("required framework report is unsafe");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(await readFile(target, "utf8"));
  } catch (error) {
    throw new Error("required framework report is invalid", { cause: error });
  }
  let report: FrameworkPlatformReport;
  try {
    report = buildFrameworkPlatformReport(parsed as FrameworkPlatformReport);
  } catch (error) {
    throw new Error("required framework report is invalid", { cause: error });
  }
  if (report.platform !== platform) throw new Error("required framework report platform is invalid");
  return report;
}

function buildReport(
  platform: NodeJS.Platform,
  architecture: string,
  bundle: PreparedCMakeBundle,
  results: readonly NativeScenarioResult[],
): NativeToolchainReport {
  if (
    platform !== "linux" && platform !== "win32" ||
    architecture !== "x64" ||
    !/^[0-9]+\.[0-9]+\.[0-9]+$/u.test(bundle.cmakeVersion) ||
    !/^[0-9a-f]{64}$/u.test(bundle.archiveSha256)
  ) {
    throw new Error("invalid native report identity");
  }
  for (const result of results) {
    if (
      result.platform !== platform ||
      !safeReportString(result.toolchainVersion) ||
      !safeReportString(result.generator) ||
      result.cmakeVersion !== bundle.cmakeVersion
    ) {
      throw new Error("invalid native scenario report");
    }
    for (const [name, status] of Object.entries(result.scenarios)) {
      if (!/^[a-z0-9][a-z0-9-]{0,63}$/u.test(name) || status !== "passed" && status !== "skipped") {
        throw new Error("invalid native scenario status");
      }
    }
  }
  return {
    schemaVersion: 1,
    platform,
    architecture,
    cmake: {
      version: bundle.cmakeVersion,
      archiveSha256: bundle.archiveSha256,
    },
    results,
  };
}

function safeReportString(value: string): boolean {
  return (
    value.length > 0 &&
    value.length <= 256 &&
    !value.includes("\0") &&
    !isAbsolute(value) &&
    !/^[A-Za-z]:[\\/]/u.test(value) &&
    !value.startsWith("\\\\")
  );
}

export const __testing = Object.freeze({ buildReport });
