#!/usr/bin/env bash
# Reject absent CI evidence and verify a movable raw/canonical artifact.
set -euo pipefail
[[ $# -eq 1 ]] || { printf '%s\n' 'usage: verify-linux-fixtures.sh <artifact-dir>' >&2; exit 2; }
[[ -d "$1" ]] || { printf '%s\n' 'raw artifact is required: artifact directory is missing' >&2; exit 1; }
artifact_dir=$(realpath "$1")
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../../.." && pwd -P)
generation="$artifact_dir/generation.json"
[[ -f "$generation" ]] || { printf 'raw artifact is required: %s\n' "$generation" >&2; exit 1; }

# Node validates the closed generation schema before using any metadata path.
# Sidecars use only fixed basenames, so this remains valid after artifact move.
node -e '
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto");
const [artifactDir, generationPath, provenancePath] = process.argv.slice(1);
const fail = message => { throw new Error(message); };
const exactKeys = (value, keys, label) => {
  if (value === null || typeof value !== "object" || Array.isArray(value)) fail(`${label} must be an object`);
  const actual = Object.keys(value).sort(), expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) fail(`${label} has unexpected fields`);
};
const digest = value => typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
const semver = value => typeof value === "string" && /^[0-9]+(?:\.[0-9]+)+$/.test(value);
const absolute = value => typeof value === "string" && path.posix.isAbsolute(value) && !value.includes("\\") && !value.split("/").includes("..");
const child = (value, root) => value.startsWith(`${root}/`);
const hash = file => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const safeName = value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value) && path.posix.basename(value) === value;
const generated = JSON.parse(fs.readFileSync(generationPath, "utf8"));
const provenance = JSON.parse(fs.readFileSync(provenancePath, "utf8"));
exactKeys(generated, ["schemaVersion", "bundle", "source", "toolchain", "artifacts"], "generation");
if (generated.schemaVersion !== 1) fail("unsupported generation schema");
exactKeys(generated.bundle, ["manifestSHA256", "platform", "pythonVersion", "gcovrVersion"], "bundle");
if (!digest(generated.bundle.manifestSHA256) || generated.bundle.manifestSHA256 !== provenance.bundle?.manifestSHA256 || generated.bundle.platform !== "linux-x64" || generated.bundle.pythonVersion !== "3.14.6" || generated.bundle.gcovrVersion !== "8.6") fail("bundle metadata verification failed");
exactKeys(generated.source, ["path", "sha256"], "source");
if (generated.source.path !== "fixture-source.c" || !digest(generated.source.sha256) || generated.source.sha256 !== provenance.generator?.sourceSHA256) fail("source metadata verification failed");
exactKeys(generated.toolchain, ["allowedDirectory", "gcc", "gcov"], "toolchain");
if (!absolute(generated.toolchain.allowedDirectory)) fail("toolchain metadata verification failed");
for (const name of ["gcc", "gcov"]) {
  const tool = generated.toolchain[name];
  exactKeys(tool, ["path", "version", "sha256"], `toolchain.${name}`);
  if (!absolute(tool.path) || !child(tool.path, generated.toolchain.allowedDirectory) || !semver(tool.version) || !digest(tool.sha256)) fail("toolchain metadata verification failed");
}
exactKeys(generated.artifacts, ["raw", "canonical"], "artifacts");
const expected = {raw: ["raw-coverage.json", "raw-coverage.json.sha256"], canonical: ["gcovr-8.6.canonical.json", "gcovr-8.6.canonical.json.sha256"]};
for (const name of ["raw", "canonical"]) {
  const record = generated.artifacts[name];
  exactKeys(record, ["path", "sha256", "sidecar"], `artifacts.${name}`);
  if (!safeName(record.path) || !safeName(record.sidecar)) fail("artifact metadata verification failed: path traversal");
  if (record.path !== expected[name][0] || record.sidecar !== expected[name][1] || !digest(record.sha256)) fail("artifact metadata verification failed");
  const payload = path.join(artifactDir, record.path), sidecar = path.join(artifactDir, record.sidecar);
  if (!fs.existsSync(payload) || !fs.existsSync(sidecar)) fail("raw artifact is required");
  const sidecarContent = fs.readFileSync(sidecar, "utf8");
  if (sidecarContent !== `${record.sha256}  ${record.path}\n` || hash(payload) !== record.sha256) fail("artifact sidecar verification failed");
}
const raw = path.join(artifactDir, generated.artifacts.raw.path), canonical = path.join(artifactDir, generated.artifacts.canonical.path);
if (fs.readFileSync(canonical, "utf8") !== `${JSON.stringify(JSON.parse(fs.readFileSync(raw, "utf8")))}\n`) fail("canonical fixture transform verification failed");
' "$artifact_dir" "$generation" "$repo_root/apps/test-service/internal/coverageparser/gcovr/testdata/provenance.json" || { printf '%s\n' 'raw artifact or toolchain metadata verification failed' >&2; exit 1; }
raw="$artifact_dir/raw-coverage.json"
cd "$repo_root"
GOENV=off GOTOOLCHAIN=local UNIT_TEST_IDE_GCOVR_RAW_FIXTURE="$raw" go test ./apps/test-service/internal/coverageparser/gcovr -run '^TestParseGCovrRawLinuxArtifact$' -count=1
GOENV=off GOTOOLCHAIN=local go test ./apps/test-service/internal/coverageparser/gcovr -run '^TestGCovrFixtureProvenancePinsBundleAndFixtureDigests$' -count=1
