import { isAbsolute, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const COMMIT = /^[0-9a-f]{40}$/u;
const DIGEST = /^[0-9a-f]{64}$/u;
const SAFE_TOKEN = /^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$/u;
const SAFE_ID = /^[a-z0-9][a-z0-9-]{0,127}$/u;
const TOOLCHAINS = Object.freeze([
  ["linux", "gcc"], ["linux", "clang"], ["win32", "msvc"], ["win32", "clang-cl"],
]);
const FRAMEWORKS = Object.freeze(["cpputest", "unity"]);
const REQUIRED_FAULTS = Object.freeze(["cancel-before-build", "compile-failure", "service-restart"]);
const METRICS = Object.freeze(["functions", "lines", "branches"]);
const TOP_KEYS = Object.freeze([
  "schemaVersion", "candidateCommit", "evidenceKind", "producerReceiptSha256", "platform", "toolchainFamily", "framework", "compiler",
  "frameworkIdentity", "cmake", "coverage", "candidates", "tests", "faultScenarios",
  "inputArtifactSha256", "outputArtifactSha256", "performance", "mutation",
]);

function fail(message) {
  throw new Error(`PHASE10_TESTGEN_REPORT_INVALID: ${message}`);
}

function exactObject(value, keys, label) {
  if (value === null || typeof value !== "object" || Array.isArray(value)
      || Object.getPrototypeOf(value) !== Object.prototype) fail(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail(`${label} is not a closed object (unexpected or missing fields)`);
  }
  return value;
}

function digest(value, label) {
  if (typeof value !== "string" || !DIGEST.test(value)) fail(`${label} digest is invalid`);
  return value;
}

function commit(value, label) {
  if (typeof value !== "string" || !COMMIT.test(value)) fail(`${label} is invalid`);
  return value;
}

function token(value, label) {
  if (typeof value !== "string" || !SAFE_TOKEN.test(value) || value.includes("/") || value.includes("\\")) {
    fail(`${label} contains a path or is invalid`);
  }
  return value;
}

function integer(value, label, minimum = 0) {
  if (!Number.isSafeInteger(value) || value < minimum) fail(`${label} is invalid`);
  return value;
}

function validateCoverage(value) {
  const coverage = exactObject(value, ["baseline", "final"], "coverage");
  for (const phase of ["baseline", "final"]) {
    const phaseValue = exactObject(coverage[phase], METRICS, `${phase} coverage`);
    for (const metric of METRICS) {
      const item = exactObject(phaseValue[metric], ["covered", "total"], `${phase} ${metric}`);
      integer(item.total, `${phase} ${metric} total`, 1);
      integer(item.covered, `${phase} ${metric} covered`);
      if (item.covered > item.total) fail(`${phase} ${metric} covered exceeds total`);
    }
  }
  if (!METRICS.some((metric) => coverage.final[metric].covered > coverage.baseline[metric].covered)) {
    fail("coverage must increase for at least one metric");
  }
  if (METRICS.some((metric) => coverage.final[metric].covered < coverage.baseline[metric].covered)) {
    fail("coverage metrics cannot regress");
  }
  for (const metric of METRICS) if (coverage.final[metric].total !== coverage.baseline[metric].total) {
    fail(`${metric} totals changed between baseline and final`);
  }
}

function validateToolchainReport(input) {
  const report = exactObject(input, TOP_KEYS, "toolchain report");
  if (report.schemaVersion !== 1) fail("schema version is invalid");
  commit(report.candidateCommit, "candidate commit");
  if (report.evidenceKind !== "local-static" && report.evidenceKind !== "external-native-receipt") fail("evidence kind is invalid");
  if (report.evidenceKind === "external-native-receipt") digest(report.producerReceiptSha256, "external native producer receipt");
  if (report.evidenceKind === "local-static" && report.producerReceiptSha256 !== null) fail("local-static evidence cannot carry an external producer receipt");
  if (!TOOLCHAINS.some(([platform, family]) => platform === report.platform && family === report.toolchainFamily)) {
    fail("platform/toolchain family is invalid");
  }
  if (!FRAMEWORKS.includes(report.framework)) fail("framework is invalid");
  const compiler = exactObject(report.compiler, ["family", "version", "sha256"], "compiler");
  if (compiler.family !== report.toolchainFamily) fail("compiler family mismatch");
  token(compiler.version, "compiler version"); digest(compiler.sha256, "compiler");
  const framework = exactObject(report.frameworkIdentity, ["version", "sha256", "treeSha256"], "framework identity");
  if (framework.version !== (report.framework === "cpputest" ? "4.0" : "2.6.1")) fail("framework version mismatch");
  token(framework.version, "framework version"); digest(framework.sha256, "framework"); digest(framework.treeSha256, "framework tree");
  const cmake = exactObject(report.cmake, ["version", "sha256"], "CMake identity");
  token(cmake.version, "CMake version"); digest(cmake.sha256, "CMake");
  validateCoverage(report.coverage);
  const candidates = exactObject(report.candidates, ["generated", "retained", "rejected"], "candidate counts");
  integer(candidates.generated, "generated candidates", 1); integer(candidates.retained, "retained candidates"); integer(candidates.rejected, "rejected candidates");
  if (candidates.retained + candidates.rejected !== candidates.generated || candidates.retained < 1) fail("candidate counts do not close");
  if (!Array.isArray(report.tests) || report.tests.length !== candidates.retained) fail("retained test count does not close");
  const testIds = new Set();
  for (const [index, value] of report.tests.entries()) {
    const item = exactObject(value, ["id", "candidateKind", "assertionCount", "sourceArtifactSha256", "outputArtifactSha256"], `generated test ${index}`);
    if (typeof item.id !== "string" || !SAFE_ID.test(item.id) || testIds.has(item.id)) fail("generated test IDs are invalid or duplicated");
    testIds.add(item.id);
    if (item.candidateKind !== "verified" && item.candidateKind !== "characterization") fail("generated test candidate kind is invalid");
    integer(item.assertionCount, "generated test assertion count", 1);
    digest(item.sourceArtifactSha256, "generated test source"); digest(item.outputArtifactSha256, "generated test output");
  }
  if (!Array.isArray(report.faultScenarios) || report.faultScenarios.length !== REQUIRED_FAULTS.length) fail("fault scenario set is incomplete");
  const faultIds = new Set();
  for (const [index, value] of report.faultScenarios.entries()) {
    const item = exactObject(value, ["id", "status", "artifactSha256"], `fault scenario ${index}`);
    if (item.id !== REQUIRED_FAULTS[index] || faultIds.has(item.id) || item.status !== "passed") fail("fault scenario set is invalid");
    faultIds.add(item.id); digest(item.artifactSha256, "fault scenario artifact");
  }
  digest(report.inputArtifactSha256, "input artifact"); digest(report.outputArtifactSha256, "output artifact");
  const performance = exactObject(report.performance, ["durationMs", "budgetMs", "peakMemoryBytes", "memoryBudgetBytes"], "performance");
  integer(performance.durationMs, "duration", 1); integer(performance.budgetMs, "duration budget", 1); integer(performance.peakMemoryBytes, "peak memory", 1); integer(performance.memoryBudgetBytes, "memory budget", 1);
  if (performance.durationMs > performance.budgetMs || performance.peakMemoryBytes > performance.memoryBudgetBytes) fail("performance budget exceeded");
  const mutation = exactObject(report.mutation, ["total", "killed", "requiredKilled"], "mutation");
  integer(mutation.total, "mutation total", 1); integer(mutation.killed, "mutations killed"); integer(mutation.requiredKilled, "required mutations", 1);
  if (mutation.killed > mutation.total || mutation.killed < mutation.requiredKilled) fail("mutation gate failed");
  return canonicalClone(report);
}

export function buildToolchainReport(input) {
  return validateToolchainReport(input);
}

export function buildMatrixReport(input) {
  const matrix = exactObject(input, ["schemaVersion", "candidateCommit", "evidenceKind", "blocks"], "matrix input");
  if (matrix.schemaVersion !== 1) fail("matrix schema version is invalid");
  commit(matrix.candidateCommit, "matrix candidate commit");
  if (matrix.evidenceKind !== "local-static") fail("matrix native evidence requires an external producer receipt");
  if (!Array.isArray(matrix.blocks) || matrix.blocks.length !== TOOLCHAINS.length * FRAMEWORKS.length) fail("matrix toolchain blocks are incomplete");
  const seen = new Set();
  const artifactDigests = new Set();
  const blocks = [];
  for (const [index, value] of matrix.blocks.entries()) {
    const block = buildToolchainReport(value);
    if (block.candidateCommit !== matrix.candidateCommit) fail("matrix candidate commit mismatch");
    const key = `${block.platform}:${block.toolchainFamily}:${block.framework}`;
    if (seen.has(key)) fail("duplicate matrix toolchain block");
    seen.add(key);
    for (const digestValue of [block.inputArtifactSha256, block.outputArtifactSha256, ...block.tests.flatMap((item) => [item.sourceArtifactSha256, item.outputArtifactSha256]), ...block.faultScenarios.map((item) => item.artifactSha256)]) {
      if (artifactDigests.has(digestValue)) fail("duplicate artifact digest across matrix");
      artifactDigests.add(digestValue);
    }
    blocks.push(block);
    if (index > 100) fail("matrix is too large");
  }
  const expected = TOOLCHAINS.flatMap(([platform, family]) => FRAMEWORKS.map((framework) => `${platform}:${family}:${framework}`));
  if (expected.some((key) => !seen.has(key))) fail("matrix toolchain blocks are incomplete");
  return { schemaVersion: 1, candidateCommit: matrix.candidateCommit, evidenceKind: "local-static", nativeEvidenceEligible: false, blocks, overallStatus: "rejected" };
}

function canonicalClone(value) {
  if (Array.isArray(value)) return value.map(canonicalClone);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalClone(value[key])]));
  }
  return value;
}

async function main(argv) {
  if (argv.length !== 6 || argv[0] !== "--reports" || argv[2] !== "--candidate" || argv[4] !== "--out") fail("arguments are invalid");
  const { lstat, readFile, writeFile } = await import("node:fs/promises");
  const inputPath = argv[1];
  const outputPath = argv[5];
  if (typeof inputPath !== "string" || typeof outputPath !== "string" || !isAbsolute(inputPath) || !isAbsolute(outputPath) || inputPath.includes("\0") || outputPath.includes("\0")) fail("report paths are invalid");
  const inputInfo = await lstat(inputPath).catch(() => undefined);
  if (inputInfo === undefined || !inputInfo.isFile() || inputInfo.isSymbolicLink() || inputInfo.size < 2 || inputInfo.size > 8 * 1024 * 1024) fail("report input is unsafe");
  const reports = JSON.parse(await readFile(inputPath, "utf8"));
  const matrix = buildMatrixReport({ schemaVersion: 1, candidateCommit: argv[3], evidenceKind: "local-static", blocks: reports });
  await writeFile(outputPath, `${JSON.stringify(matrix)}\n`, { encoding: "utf8", flag: "wx", mode: 0o600 });
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : "report validation failed"}\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({ REQUIRED_FAULTS, TOOLCHAINS, FRAMEWORKS });
