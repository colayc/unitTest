import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { buildCoverageBackendReport, mainCoverageBackendReport, parseNativeLLVMFixtureLog } from "./native-report.js";

const candidateCommit = "a".repeat(40);
const digest = (value: string) => createHash("sha256").update(value).digest("hex");
const summary = {
  functions: { covered: 2, total: 2 },
  lines: { covered: 3, total: 4 },
  branches: { covered: 1, total: 2 },
};
const rows = (["linux-gcc", "linux-clang", "windows-clang-cl"] as const).map((backend) => ({
  schemaVersion: 1 as const,
  candidateCommit,
  backend,
  status: "passed" as const,
  runnerImage: backend === "windows-clang-cl" ? "windows-2025-vs2026" : "ubuntu-24.04",
  compiler: { family: backend === "windows-clang-cl" ? "clang-cl" as const : backend === "linux-clang" ? "clang" as const : "gcc" as const, version: "18.1.0", sha256: digest(backend) },
  summary,
  sourceArtifactSha256: digest(`source:${backend}`),
}));

test("coverage backend report requires exact real rows, SHA, and positive function/line/branch totals", () => {
  assert.deepEqual(buildCoverageBackendReport(candidateCommit, rows).rows.map((row) => row.backend), ["linux-gcc", "linux-clang", "windows-clang-cl"]);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.slice(1)), /required coverage backend/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, [...rows, rows[0]!]), /required coverage backend/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, status: "skipped" } : row)), /required coverage backend/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, candidateCommit: "b".repeat(40) } : row)), /candidate/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, summary: { ...summary, branches: { covered: 0, total: 0 } } } : row)), /coverage totals/u);
});

test("coverage backend report is closed, path-free, and digest-bound", () => {
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, compiler: { ...row.compiler, version: "/tmp/clang" } } : row)), /compiler/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, sourceArtifactSha256: "0".repeat(64), unexpected: "secret" } : row)), /closed/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, sourceArtifactSha256: "bad" } : row)), /digest/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, sourceArtifactSha256: rows[0]!.sourceArtifactSha256 } : row)), /unique/u);
  assert.throws(() => buildCoverageBackendReport(candidateCommit, rows.map((row, index) => index === 1 ? { ...row, runnerImage: "C:\\runner" } : row)), /runner/u);
});

test("Linux LLVM fixture log must contain one passing native result with observed totals and tool identity", () => {
  const evidence = { compilerVersion: "18.1.0", compilerSha256: digest("clang"), summary, coverageDocumentSha256: digest("document") };
  const line = `native_fixture_linux_test.go:99: UTIDE_NATIVE_LLVM_EVIDENCE=${JSON.stringify(evidence)}`;
  const log = `=== RUN   TestNativeLinuxLLVMFixture\n    ${line}\n--- PASS: TestNativeLinuxLLVMFixture (0.01s)\nPASS\nok  \tunit-test-ide.local/test-service/internal/coveragellvm 0.01s\n=== RUN   TestNativeLinuxLLVMFixtureCancellationUsesProductionOwners\n--- PASS: TestNativeLinuxLLVMFixtureCancellationUsesProductionOwners (0.01s)\nPASS\nok  \tunit-test-ide.local/test-service/internal/coverageexec 0.01s\n`;
  assert.deepEqual(parseNativeLLVMFixtureLog(log), evidence);
  assert.throws(() => parseNativeLLVMFixtureLog(log.replace("--- PASS:", "--- SKIP:")), /native LLVM fixture/u);
  assert.throws(() => parseNativeLLVMFixtureLog(`${log}${line}\n`), /native LLVM fixture/u);
  assert.throws(() => parseNativeLLVMFixtureLog(log.replace('"covered":2', '"covered":0')), /native LLVM fixture|coverage totals/u);
});

test("coverage report CLI publishes only canonical complete source rows", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "coverage-backend-report-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const paths = rows.map((_, index) => join(root, `${index}.json`));
  await Promise.all(rows.map((row, index) => writeFile(paths[index]!, `${JSON.stringify(row)}\n`)));
  const out = join(root, "matrix.json");
  const args = ["--mode", "matrix", "--gcc", paths[0]!, "--clang", paths[1]!, "--windows", paths[2]!, "--candidate", candidateCommit, "--out", out];
  await mainCoverageBackendReport(args);
  assert.deepEqual(JSON.parse(await readFile(out, "utf8")), buildCoverageBackendReport(candidateCommit, rows));
  await assert.rejects(mainCoverageBackendReport(args), /already exists/u);
  await writeFile(paths[1]!, ` ${JSON.stringify(rows[1])}\n`);
  await assert.rejects(mainCoverageBackendReport([...args.slice(0, -1), join(root, "second.json")]), /not canonical/u);
});
