import { appendFile, mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import {
  encodeCanonicalJson,
  phase9Failure,
  readCanonicalJson,
  writeCanonicalJson,
} from "./canonical-json.mjs";
import {
  artifactNameForP8Gate,
  createP8FoundationReports,
  createP8ProducerReport,
  p8ReportDigest,
} from "./p8-report.mjs";

const MAX_INPUT_BYTES = 64 * 1024;
const OUTPUTS = Object.freeze({
  "P8-INSTALL-LIFECYCLE-LINUX": "install_lifecycle_linux_artifact_name",
  "P8-INSTALL-LIFECYCLE-WINDOWS": "install_lifecycle_windows_artifact_name",
  "P8-LICENSE-AUDIT": "license_audit_artifact_name",
  "P8-LINUX-APPIMAGE-PACKAGE": "linux_appimage_package_artifact_name",
  "P8-QUALIFICATION-UNSIGNED": "qualification_unsigned_artifact_name",
  "P8-RUNTIME-PRODUCER-PROVENANCE": "runtime_producer_provenance_artifact_name",
  "P8-WINDOWS-MSIX-PACKAGE": "windows_msix_package_artifact_name",
});

function invalid(label) {
  throw phase9Failure("PHASE9_P8_REPORT_INVALID", `${label} is invalid`);
}

function hasExactKeys(value, keys) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const actual = Object.keys(value).sort((left, right) => left.localeCompare(right, "en"));
  const expected = [...keys].sort((left, right) => left.localeCompare(right, "en"));
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
}

function createReports(input) {
  if (input.mode === "producer") {
    if (!hasExactKeys(input, ["schemaVersion", "mode", "candidateCommit", "runId", "runAttempt", "artifacts"])
        || input.schemaVersion !== 1) invalid("producer report input");
    const report = createP8ProducerReport(input);
    return { [report.gateId]: report };
  }
  if (input.mode === "foundation") {
    if (!hasExactKeys(input, [
      "schemaVersion", "mode", "candidateCommit", "runId", "runAttempt", "producerRun", "releaseVersion", "packages", "signing",
    ]) || input.schemaVersion !== 1) invalid("foundation report input");
    return createP8FoundationReports(input);
  }
  invalid("report mode");
}

export async function createP8ReportArtifacts(input, outDirectory) {
  if (typeof outDirectory !== "string" || outDirectory.length === 0 || outDirectory.includes("\0")) invalid("output directory");
  const reports = createReports(input);
  await mkdir(outDirectory);
  const manifestReports = [];
  for (const gateId of Object.keys(reports).sort((left, right) => left.localeCompare(right, "en"))) {
    const report = reports[gateId];
    const digest = p8ReportDigest(report);
    const artifactName = artifactNameForP8Gate(gateId, report.runAttempt, digest);
    const reportDirectory = join(outDirectory, gateId.toLowerCase());
    await mkdir(reportDirectory);
    await writeCanonicalJson(join(reportDirectory, "p8-report.json"), report);
    manifestReports.push({ artifactName, digest, file: `${gateId.toLowerCase()}/p8-report.json`, gateId });
  }
  const manifest = { schemaVersion: 1, reports: manifestReports };
  await writeCanonicalJson(join(outDirectory, "manifest.json"), manifest);
  return manifest;
}

function parseArguments(args) {
  const flags = ["--input", "--out-dir", "--github-output"];
  if (args.length !== flags.length * 2
      || flags.some((flag, index) => args[index * 2] !== flag)
      || args.some((value, index) => index % 2 === 1 && (typeof value !== "string" || value.length === 0 || value.includes("\0")))) {
    invalid("arguments");
  }
  return { input: args[1], outDirectory: args[3], githubOutput: args[5] };
}

async function main(args) {
  const options = parseArguments(args);
  const input = await readCanonicalJson(options.input, { label: "P8 report input", maxBytes: MAX_INPUT_BYTES });
  const manifest = await createP8ReportArtifacts(input, options.outDirectory);
  const lines = manifest.reports.map(({ artifactName, gateId }) => `${OUTPUTS[gateId]}=${artifactName}\n`).join("");
  await appendFile(options.githubOutput, lines, { encoding: "utf8" });
  process.stdout.write(encodeCanonicalJson(manifest));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    const code = error?.code === "PHASE9_P8_REPORT_INVALID" ? error.code : "PHASE9_P8_REPORT_INVALID";
    process.stderr.write(`${code}: report generation failed\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({ OUTPUTS });
