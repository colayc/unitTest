import { execFile } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

import { encodeCanonicalJson, phase9Failure } from "./canonical-json.mjs";
import { evaluateRecordedMatrix, loadPhase9Inputs } from "./validate.mjs";

const execFileAsync = promisify(execFile);
const STATUSES = ["PASS", "MISSING", "FAILED", "DEFERRED"];

function jsonProjection(matrix) {
  const output = {
    schemaVersion: matrix.schemaVersion,
    catalogComplete: matrix.catalogComplete,
    releaseReady: matrix.releaseReady,
    evaluationMode: matrix.evaluationMode,
    candidateCommit: matrix.candidateCommit,
    currentCommit: matrix.currentCommit,
    counts: matrix.counts,
    gates: [...matrix.gates]
      .sort((left, right) => left.id.localeCompare(right.id, "en"))
      .map((gate) => {
        const row = { id: gate.id, status: gate.status };
        for (const key of ["receiptId", "artifactAvailability", "reason"]) {
          if (gate[key] !== undefined) row[key] = gate[key];
        }
        return row;
      }),
  };
  if (matrix.recordedByCommit !== undefined) output.recordedByCommit = matrix.recordedByCommit;
  return output;
}

export function renderMatrixJson(matrix) {
  return encodeCanonicalJson(jsonProjection(matrix));
}

function escapeCell(value) {
  return String(value ?? "")
    .replaceAll("\\", "\\\\")
    .replaceAll("|", "\\|")
    .replaceAll("`", "\\`")
    .replaceAll("\r", "\\r")
    .replaceAll("\n", "\\n");
}

function evidenceCell(gate) {
  const values = [];
  if (gate.receiptId !== undefined) values.push(gate.receiptId);
  if (gate.artifactAvailability !== undefined) values.push(`(${gate.artifactAvailability})`);
  return values.join(" ");
}

export function renderMatrixMarkdown(matrix) {
  const counts = matrix.counts;
  const gates = [...matrix.gates].sort((left, right) => left.id.localeCompare(right.id, "en"));
  const lines = [
    "# Phase 9 Gate Matrix",
    "",
    `- Candidate commit: \`${escapeCell(matrix.candidateCommit)}\``,
    `- Recorded by commit: \`${escapeCell(matrix.recordedByCommit ?? matrix.currentCommit)}\``,
    `- Evaluation mode: ${escapeCell(matrix.evaluationMode)}`,
    `- Catalog complete: ${String(matrix.catalogComplete)}`,
    `- Release ready: ${String(matrix.releaseReady)}`,
    "",
    "## Status summary",
    "",
    "| Status | Count |",
    "|---|---:|",
    `| PASS | ${counts.pass} |`,
    `| MISSING | ${counts.missing} |`,
    `| FAILED | ${counts.failed} |`,
    `| DEFERRED | ${counts.deferred} |`,
    "",
    "## Gates",
    "",
    "| Gate | Phase | Category | Status | Evidence | Reason |",
    "|---|---:|---|---|---|---|",
  ];
  for (const gate of gates) {
    lines.push(`| ${escapeCell(gate.id)} | ${escapeCell(gate.phase)} | ${escapeCell(gate.category)} | ${escapeCell(gate.status)} | ${escapeCell(evidenceCell(gate))} | ${escapeCell(gate.reason)} |`);
  }
  return `${lines.join("\n")}\n`;
}

export async function writeMatrixOutputs({ matrix, jsonPath, markdownPath, check = false }) {
  const json = renderMatrixJson(matrix);
  const markdown = renderMatrixMarkdown(matrix);
  if (check) {
    let actualJson;
    let actualMarkdown;
    try {
      [actualJson, actualMarkdown] = await Promise.all([readFile(jsonPath), readFile(markdownPath)]);
    } catch (error) {
      throw phase9Failure("PHASE9_MATRIX_DRIFT", "matrix output is missing", error);
    }
    if (!actualJson.equals(Buffer.from(json, "utf8")) || !actualMarkdown.equals(Buffer.from(markdown, "utf8"))) {
      throw phase9Failure("PHASE9_MATRIX_DRIFT", "matrix output differs from expected bytes");
    }
    return;
  }
  await Promise.all([mkdir(dirname(jsonPath), { recursive: true }), mkdir(dirname(markdownPath), { recursive: true })]);
  await Promise.all([writeFile(jsonPath, json, "utf8"), writeFile(markdownPath, markdown, "utf8")]);
}

async function repositoryHead(repositoryRoot) {
  const { stdout } = await execFileAsync("git", ["-C", repositoryRoot, "rev-parse", "HEAD"], { encoding: "utf8", windowsHide: true });
  return stdout.trim();
}

async function repositoryState(repositoryRoot, candidateCommit) {
  const currentCommit = await repositoryHead(repositoryRoot);
  try {
    await execFileAsync("git", ["-C", repositoryRoot, "merge-base", "--is-ancestor", candidateCommit, currentCommit], { windowsHide: true });
  } catch (error) {
    throw phase9Failure("PHASE9_EVIDENCE_UNTRUSTED", "candidate ancestry is invalid", error);
  }
  const { stdout } = await execFileAsync("git", [
    "-C", repositoryRoot, "diff", "--name-only", "--diff-filter=ACDMRTUXB", `${candidateCommit}..${currentCommit}`,
  ], { encoding: "utf8", windowsHide: true });
  return { currentCommit, changedPaths: stdout.split(/\r?\n/u).filter(Boolean) };
}

function parseArguments(args) {
  const required = ["registry", "baseline", "receipts", "repository-root", "json-out", "markdown-out"];
  const values = {};
  let index = 0;
  while (index < args.length) {
    const flag = args[index++];
    if (flag === "--check") {
      if (values.check) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "command line arguments");
      values.check = true;
      continue;
    }
    const name = flag?.startsWith("--") ? flag.slice(2) : "";
    const value = args[index++];
    if (!required.includes(name) || values[name] !== undefined || value === undefined || value.length === 0 || value.startsWith("--")) {
      throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "command line arguments");
    }
    values[name] = value;
  }
  if (required.some((name) => values[name] === undefined)) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "command line arguments");
  return values;
}

async function main() {
  const args = parseArguments(process.argv.slice(2));
  const inputs = await loadPhase9Inputs({ registryPath: args.registry, baselinePath: args.baseline, receiptsDirectory: args.receipts });
  const state = await repositoryState(args["repository-root"], inputs.baseline.candidateCommit);
  const matrix = evaluateRecordedMatrix({ ...inputs, ...state });
  const currentCommit = state.currentCommit;
  matrix.recordedByCommit = currentCommit;
  matrix.gates = matrix.gates.map((gate) => {
    const definition = inputs.registry.gates.find(({ id }) => id === gate.id);
    return { ...gate, phase: definition?.phase, category: definition?.category };
  });
  await writeMatrixOutputs({ matrix, jsonPath: args["json-out"], markdownPath: args["markdown-out"], check: args.check === true });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    const code = typeof error?.code === "string" && error.code.startsWith("PHASE9_") ? error.code : "PHASE9_GATE_SCHEMA_INVALID";
    process.stderr.write(`${code}: rendering failed\n`);
    process.exitCode = 1;
  });
}
