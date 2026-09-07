#!/usr/bin/env bash
# Reproduces raw gcovr 8.6 format 0.14 evidence on Linux CI. It intentionally
# uses only the prepared product-owned Linux bundle and the system GCC/gcov
# pair under test. The checked-in parser fixtures are deterministic minimized
# projections; this command writes the original raw evidence and its SHA-256.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../../.." && pwd)
bundle_root=${UNIT_TEST_IDE_COVERAGE_BUNDLE_ROOT:-"$repo_root/.superpowers/runtime/coverage-bundle/linux-x64"}
testdata="$repo_root/apps/test-service/internal/coverageparser/gcovr/testdata"
python="$bundle_root/python/bin/python3"
runner="$bundle_root/app/gcovr-runner.pyz"

test -x "$python"
test -f "$runner"
test -f "$bundle_root/manifest.resolved.json"
command -v gcc >/dev/null
command -v gcov >/dev/null

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cp "$testdata/fixture-source.c" "$scratch/fixture.c"
gcc --coverage -O0 -g "$scratch/fixture.c" -o "$scratch/fixture"
"$scratch/fixture" >/dev/null
root=$(realpath "$scratch")
cat >"$scratch/descriptor.json" <<EOF
{"schemaVersion":1,"root":"$root","objectDirectory":"$root","gcovExecutable":"$(command -v gcov)","outputPath":"$root/raw-coverage.json"}
EOF
env -i PATH="$PATH" "$python" -I -S "$runner" "$scratch/descriptor.json"
source_sha=$(sha256sum "$testdata/fixture-source.c" | awk '{print $1}')
raw_sha=$(sha256sum "$scratch/raw-coverage.json" | awk '{print $1}')
manifest_sha=$(sha256sum "$repo_root/tools/coverage-bundle/manifest.json" | awk '{print $1}')
printf 'source-sha256=%s\nmanifest-sha256=%s\nraw-output-sha256=%s\n' "$source_sha" "$manifest_sha" "$raw_sha"
if [[ -n "${UNIT_TEST_IDE_GCOVR_FIXTURE_RAW_OUTPUT:-}" ]]; then
  cp "$scratch/raw-coverage.json" "$UNIT_TEST_IDE_GCOVR_FIXTURE_RAW_OUTPUT"
  sha256sum "$UNIT_TEST_IDE_GCOVR_FIXTURE_RAW_OUTPUT"
fi
