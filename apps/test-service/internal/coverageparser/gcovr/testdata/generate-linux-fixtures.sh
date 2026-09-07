#!/usr/bin/env bash
# Generate auditable gcovr 8.6 raw evidence on Linux CI. No bundle-root
# override is accepted: arbitrary prepared bundles are not fixture evidence.
set -euo pipefail

readonly expected_manifest_sha256=62ce3b007ce12261f7d29484ab08ec5e85da63e150d45a2cd26d96b2b4bdb61a
readonly expected_platform=linux-x64
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../../.." && pwd -P)
readonly repo_root
readonly bundle_root="$repo_root/.superpowers/runtime/coverage-bundle/$expected_platform"
readonly testdata="$repo_root/apps/test-service/internal/coverageparser/gcovr/testdata"

die() { printf '%s\n' "fixture generation: $*" >&2; exit 1; }
[[ "$(uname -s)" == "Linux" ]] || die "Linux is required"
[[ -z "${UNIT_TEST_IDE_COVERAGE_BUNDLE_ROOT:-}" || "${UNIT_TEST_IDE_COVERAGE_BUNDLE_ROOT}" == "$bundle_root" ]] || die "unbound bundle root override is forbidden"
: "${UNIT_TEST_IDE_GCC:?set an explicitly pinned absolute GCC executable}"
: "${UNIT_TEST_IDE_GCOV:?set an explicitly pinned absolute gcov executable}"
: "${UNIT_TEST_IDE_GCC_VERSION:?set the explicitly pinned GCC/gcov version}"
: "${UNIT_TEST_IDE_GCOVR_ALLOWED_TOOLCHAIN_DIR:?set the allowed CI toolchain directory}"
: "${UNIT_TEST_IDE_GCOVR_FIXTURE_ARTIFACT_DIR:?set the required CI artifact directory}"
readonly gcc=$(realpath "$UNIT_TEST_IDE_GCC")
readonly gcov=$(realpath "$UNIT_TEST_IDE_GCOV")
[[ "$UNIT_TEST_IDE_GCC" == /* && "$UNIT_TEST_IDE_GCOV" == /* ]] || die "GCC and gcov must be absolute paths"
[[ -x "$gcc" && -x "$gcov" ]] || die "pinned GCC or gcov is not executable"
readonly allowed_toolchain_dir=$(realpath "$UNIT_TEST_IDE_GCOVR_ALLOWED_TOOLCHAIN_DIR")
[[ -d "$allowed_toolchain_dir" ]] || die "allowed toolchain directory must exist"
case "$gcc" in "$allowed_toolchain_dir"/*) ;; *) die "GCC is outside the allowed CI toolchain directory" ;; esac
case "$gcov" in "$allowed_toolchain_dir"/*) ;; *) die "gcov is outside the allowed CI toolchain directory" ;; esac
readonly artifact_dir=$(realpath "$UNIT_TEST_IDE_GCOVR_FIXTURE_ARTIFACT_DIR")
[[ -d "$artifact_dir" ]] || die "artifact directory must already exist"

# This verifies READY, exact layout, every output digest, and resolved inputs
# against the workspace lock at the only accepted Linux bundle location.
node "$repo_root/tools/coverage-bundle/prepare.mjs" --check
node -e '
const fs = require("node:fs"), crypto = require("node:crypto");
const [manifestPath, resolvedPath, expectedDigest, root] = process.argv.slice(1);
const manifestBytes = fs.readFileSync(manifestPath);
const manifest = JSON.parse(manifestBytes), resolved = JSON.parse(fs.readFileSync(resolvedPath, "utf8"));
const digest = crypto.createHash("sha256").update(manifestBytes).digest("hex");
if (digest !== expectedDigest || manifest.python?.version !== "3.14.6" || manifest.gcovr?.version !== "8.6" || resolved.schemaVersion !== 1 || resolved.platform !== "linux-x64" || resolved.pythonVersion !== manifest.python.version || resolved.gcovrVersion !== manifest.gcovr.version || !Array.isArray(resolved.outputs) || resolved.outputs.length === 0 || !fs.existsSync(`${root}/READY`)) process.exit(1);
' "$repo_root/tools/coverage-bundle/manifest.json" "$bundle_root/manifest.resolved.json" "$expected_manifest_sha256" "$bundle_root" || die "resolved bundle does not match the locked manifest"

normalize_semver() {
  local output=$1 token
  token=$(printf '%s\n' "$output" | grep -Eom1 '[0-9]+(\.[0-9]+)+' || true)
  [[ "$token" =~ ^[0-9]+(\.[0-9]+)+$ ]] || return 1
  printf '%s\n' "$token"
}
tool_version() {
  local output version
  output=$("$1" -dumpfullversion -dumpversion 2>/dev/null || true)
  version=$(normalize_semver "$output" || true)
  if [[ -z "$version" ]]; then
    output=$("$1" --version 2>&1)
    version=$(normalize_semver "$output" || true)
  fi
  [[ -n "$version" ]] || return 1
  printf '%s\n' "$version"
}
[[ "$UNIT_TEST_IDE_GCC_VERSION" =~ ^[0-9]+(\.[0-9]+)+$ ]] || die "CI toolchain version pin must be a semver"
readonly gcc_version=$(tool_version "$gcc")
readonly gcov_version=$(tool_version "$gcov")
[[ "$gcc_version" == "$UNIT_TEST_IDE_GCC_VERSION" && "$gcov_version" == "$UNIT_TEST_IDE_GCC_VERSION" ]] || die "GCC/gcov version does not match the CI pin"
readonly gcc_sha256=$(sha256sum "$gcc" | awk '{print $1}')
readonly gcov_sha256=$(sha256sum "$gcov" | awk '{print $1}')

readonly python="$bundle_root/python/bin/python3"
readonly runner="$bundle_root/app/gcovr-runner.pyz"
[[ -x "$python" && -f "$runner" ]] || die "locked bundle executable is missing"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cp "$testdata/fixture-source.c" "$scratch/fixture.c"
"$gcc" --coverage -O0 -g "$scratch/fixture.c" -o "$scratch/fixture"
"$scratch/fixture" >/dev/null
root=$(realpath "$scratch")
node -e '
const fs = require("node:fs"); const [path, root, gcov, outputPath] = process.argv.slice(1);
fs.writeFileSync(path, `${JSON.stringify({schemaVersion: 1, root, objectDirectory: root, gcovExecutable: gcov, outputPath})}\n`);
' "$scratch/descriptor.json" "$root" "$gcov" "$scratch/raw-coverage.json"
env -i PATH="$PATH" "$python" -I -S "$runner" "$scratch/descriptor.json"

readonly raw="$artifact_dir/raw-coverage.json"
readonly raw_sidecar="$artifact_dir/raw-coverage.json.sha256"
readonly canonical="$artifact_dir/gcovr-8.6.canonical.json"
readonly canonical_sidecar="$artifact_dir/gcovr-8.6.canonical.json.sha256"
cp "$scratch/raw-coverage.json" "$raw"
node -e '
const fs = require("node:fs"); const [input, output] = process.argv.slice(1);
fs.writeFileSync(output, `${JSON.stringify(JSON.parse(fs.readFileSync(input, "utf8")))}\n`);
' "$raw" "$canonical"
readonly source_sha256=$(sha256sum "$testdata/fixture-source.c" | awk '{print $1}')
readonly raw_sha256=$(sha256sum "$raw" | awk '{print $1}')
readonly canonical_sha256=$(sha256sum "$canonical" | awk '{print $1}')
printf '%s  %s\n' "$raw_sha256" "$(basename "$raw")" > "$raw_sidecar"
printf '%s  %s\n' "$canonical_sha256" "$(basename "$canonical")" > "$canonical_sidecar"
node -e '
const fs = require("node:fs");
const [output, manifestSHA256, sourceSHA256, rawSHA256, canonicalSHA256, allowedDirectory, gccPath, gccVersion, gccSHA256, gcovPath, gcovVersion, gcovSHA256] = process.argv.slice(1);
fs.writeFileSync(output, `${JSON.stringify({schemaVersion: 1, bundle: {manifestSHA256, platform: "linux-x64", pythonVersion: "3.14.6", gcovrVersion: "8.6"}, source: {path: "fixture-source.c", sha256: sourceSHA256}, toolchain: {allowedDirectory, gcc: {path: gccPath, version: gccVersion, sha256: gccSHA256}, gcov: {path: gcovPath, version: gcovVersion, sha256: gcovSHA256}}, artifacts: {raw: {path: "raw-coverage.json", sha256: rawSHA256, sidecar: "raw-coverage.json.sha256"}, canonical: {path: "gcovr-8.6.canonical.json", sha256: canonicalSHA256, sidecar: "gcovr-8.6.canonical.json.sha256"}}})}\n`);
' "$artifact_dir/generation.json" "$expected_manifest_sha256" "$source_sha256" "$raw_sha256" "$canonical_sha256" "$allowed_toolchain_dir" "$gcc" "$gcc_version" "$gcc_sha256" "$gcov" "$gcov_version" "$gcov_sha256"
printf 'source-sha256=%s\nmanifest-sha256=%s\nraw-output-sha256=%s\ncanonical-output-sha256=%s\ngcc-sha256=%s\ngcov-sha256=%s\n' "$source_sha256" "$expected_manifest_sha256" "$raw_sha256" "$canonical_sha256" "$gcc_sha256" "$gcov_sha256"
