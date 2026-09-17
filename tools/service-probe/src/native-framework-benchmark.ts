import childProcess from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, readFile, realpath } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import { validateBenchmark, type FrameworkRuntimeManifest } from "./native-framework-runtime-contract.js";

export type VerifiedFrameworkBenchmark = FrameworkRuntimeManifest["benchmark"];
const catalogRevision = "719c74062e19f57e40df62f5183f591bba6dde4941406dac2452f469e89da9d4";
const catalogArtifactSha256 = "3f9d35d363047ac75f39b8555b8ee70da4e6032501c7260f677c64a66cdbf50f";
const stableIdDigest = "68ec3e51f2471e3eacf58b59b94d39c614992f55ec8486f91afbcadc08971c7d";
// SHA-256 of the repository-owned benchmark source (LF canonicalized).
const benchmarkSourceSha256 = "8b446c32f60527bb643a7bc198f726ec92c382528ef289d29b20f8865d84c0a4";
const anchor = `UTIDE_CATALOG10000 ${catalogRevision} ${catalogArtifactSha256} ${stableIdDigest}`;

export function parseAuditedFrameworkBenchmark(output: string): VerifiedFrameworkBenchmark {
  if (typeof output !== "string" || output.length > 65536 || output.includes("\0")) throw new Error("framework benchmark output is invalid");
  const samples: number[] = [];
  let anchors = 0;
  let passed = 0;
  let completed = 0;
  for (const raw of output.split(/\r?\n/u)) {
    const line = raw.trim();
    if (line === "") continue;
    // Go may print the benchmark name before the first setup diagnostic.
    if (line.replace(/^BenchmarkCatalog10000(?:-\d+)?\s+/u, "") === anchor) { anchors++; continue; }
    const sample = /^(?:BenchmarkCatalog10000(?:-\d+)?\s+)?1\s+\d+(?:\.\d+)?\s+ns\/op\s+\d+\s+B\/op\s+(\d+)\s+allocs\/op$/u.exec(line);
    if (sample !== null) { samples.push(Number(sample[1])); continue; }
    if (line === "PASS") { passed++; continue; }
    if (/^ok\s+unit-test-ide\.local\/test-service\/internal\/testdomain\s+\d+(?:\.\d+)?s$/u.test(line)) { completed++; continue; }
    if (/^goos: (windows|linux)$/u.test(line) || /^goarch: (amd64|arm64)$/u.test(line) ||
        line === "pkg: unit-test-ide.local/test-service/internal/testdomain" || /^cpu: [A-Za-z0-9 ().,@+_-]+$/u.test(line)) continue;
    throw new Error("framework benchmark output is invalid");
  }
  if (anchors !== 3 || passed !== 1 || completed !== 1) throw new Error("framework benchmark identity or completion is invalid");
  return validateBenchmark({ id: "catalog-10000", itemCount: 10000, sampleCount: 3, allocationBudgetPerOperation: 300000,
    allocationsPerOperation: samples, catalogRevision, catalogArtifactSha256, stableIdDigest, status: "passed" });
}

export async function collectAuditedFrameworkBenchmark(repositoryRoot: string): Promise<VerifiedFrameworkBenchmark> {
  try {
    if (arguments.length !== 1 || typeof repositoryRoot !== "string" || !isAbsolute(repositoryRoot) ||
        resolve(repositoryRoot) !== resolve(import.meta.dirname, "../../..")) throw new Error("invalid benchmark root");
    const source = join(repositoryRoot, "apps/test-service/internal/testdomain/catalog_benchmark_test.go");
    const info = await lstat(source);
    if (!info.isFile() || info.isSymbolicLink() || await realpath(source) !== source ||
        createHash("sha256").update((await readFile(source, "utf8")).replaceAll("\r\n", "\n")).digest("hex") !== benchmarkSourceSha256) {
      throw new Error("invalid benchmark source");
    }
    const env: NodeJS.ProcessEnv = {};
    for (const [key, value] of Object.entries(process.env)) {
      if (["PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"].includes(key.toUpperCase())) env[key] = value;
    }
    Object.assign(env, { GOENV: "off", GOWORK: join(repositoryRoot, "go.work"), GOFLAGS: "-mod=readonly", GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", CGO_ENABLED: "0" });
    const output = await new Promise<string>((accept, reject) => {
      childProcess.execFile("go", ["test", "./apps/test-service/internal/testdomain", "-run=^$", "-bench=^BenchmarkCatalog10000$", "-benchmem", "-benchtime=1x", "-count=3"],
        { cwd: repositoryRoot, env, shell: false, windowsHide: true, timeout: 120000, maxBuffer: 65536, encoding: "utf8" },
        (error, stdout, stderr) => error !== null || stderr !== "" ? reject(new Error("benchmark execution failed")) : accept(stdout));
    });
    return parseAuditedFrameworkBenchmark(output);
  } catch {
    const error = new Error("framework benchmark collection failed");
    error.stack = error.message;
    throw error;
  }
}
