#!/usr/bin/env bash
# Reject absent CI evidence and verify raw/canonical artifact byte identity.
set -euo pipefail
[[ $# -eq 1 ]] || { printf '%s\n' 'usage: verify-linux-fixtures.sh <artifact-dir>' >&2; exit 2; }
artifact_dir=$(realpath "$1")
[[ -d "$artifact_dir" ]] || { printf '%s\n' 'raw artifact is required: artifact directory is missing' >&2; exit 1; }
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../../.." && pwd -P)
raw="$artifact_dir/raw-coverage.json"
raw_sidecar="$artifact_dir/raw-coverage.json.sha256"
canonical="$artifact_dir/gcovr-8.6.canonical.json"
canonical_sidecar="$artifact_dir/gcovr-8.6.canonical.json.sha256"
generation="$artifact_dir/generation.json"
for required in "$raw" "$raw_sidecar" "$canonical" "$canonical_sidecar" "$generation"; do
  [[ -f "$required" ]] || { printf 'raw artifact is required: %s\n' "$required" >&2; exit 1; }
done
sha256sum --status --check "$raw_sidecar"
sha256sum --status --check "$canonical_sidecar"
node -e '
const fs = require("node:fs"), crypto = require("node:crypto");
const [raw, canonical, generationPath, provenancePath] = process.argv.slice(1);
const generated = JSON.parse(fs.readFileSync(generationPath, "utf8")), provenance = JSON.parse(fs.readFileSync(provenancePath, "utf8"));
const hash = path => crypto.createHash("sha256").update(fs.readFileSync(path)).digest("hex");
if (generated.schemaVersion !== 1 || generated.bundle?.manifestSHA256 !== provenance.bundle?.manifestSHA256 || generated.bundle?.platform !== "linux-x64" || generated.bundle?.pythonVersion !== "3.14.6" || generated.bundle?.gcovrVersion !== "8.6" || generated.source?.sha256 !== provenance.generator?.sourceSHA256 || generated.artifacts?.raw?.sha256 !== hash(raw) || generated.artifacts?.canonical?.sha256 !== hash(canonical) || fs.readFileSync(canonical, "utf8") !== `${JSON.stringify(JSON.parse(fs.readFileSync(raw, "utf8")))}\n`) process.exit(1);
' "$raw" "$canonical" "$generation" "$repo_root/apps/test-service/internal/coverageparser/gcovr/testdata/provenance.json" || { printf '%s\n' 'raw artifact or canonical fixture transform verification failed' >&2; exit 1; }
cd "$repo_root"
GOENV=off GOTOOLCHAIN=local UNIT_TEST_IDE_GCOVR_RAW_FIXTURE="$raw" go test ./apps/test-service/internal/coverageparser/gcovr -run '^TestParseGCovrRawLinuxArtifact$' -count=1
GOENV=off GOTOOLCHAIN=local go test ./apps/test-service/internal/coverageparser/gcovr -run '^TestGCovrFixtureProvenancePinsBundleAndFixtureDigests$' -count=1
