#!/usr/bin/env bash
set -euo pipefail

script_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
update_script="$script_root/update.mjs"
verify_script="$script_root/linux/verify-appimage.mjs"
root=""
evidence=""
package=""
package_sha256=""
manifest=""
manifest_sha256=""
version=""
baseline_package=""
baseline_package_sha256=""
baseline_manifest=""
baseline_manifest_sha256=""
baseline_version=""

while (($# > 0)); do
  case "$1" in
    --root|--evidence|--package|--package-sha256|--manifest|--manifest-sha256|--version|--baseline-package|--baseline-package-sha256|--baseline-manifest|--baseline-manifest-sha256|--baseline-version)
      (($# >= 2)) || { echo "missing value for $1" >&2; exit 64; }
      name="${1#--}"
      name="${name//-/_}"
      printf -v "$name" '%s' "$2"
      shift 2
      ;;
    *) echo "unknown install smoke flag: $1" >&2; exit 64 ;;
  esac
done

for name in evidence package package_sha256 manifest manifest_sha256 version baseline_package baseline_package_sha256 baseline_manifest baseline_manifest_sha256 baseline_version; do
  [[ -n "${!name}" ]] || { echo "--${name//_/-} is required" >&2; exit 64; }
done
for name in package_sha256 manifest_sha256 baseline_package_sha256 baseline_manifest_sha256; do
  [[ "${!name}" =~ ^[0-9a-f]{64}$ ]] || { echo "--${name//_/-} must be a lowercase SHA-256 digest" >&2; exit 64; }
done
for path in "$package" "$manifest" "$baseline_package" "$baseline_manifest"; do
  [[ -f "$path" && ! -L "$path" ]] || { echo "package input must be a real file: $path" >&2; exit 64; }
done
package="$(realpath -- "$package")"
manifest="$(realpath -- "$manifest")"
baseline_package="$(realpath -- "$baseline_package")"
baseline_manifest="$(realpath -- "$baseline_manifest")"
[[ "$(sha256sum -- "$package" | cut -d' ' -f1)" == "$package_sha256" ]] || { echo "package SHA-256 mismatch" >&2; exit 1; }
[[ "$(sha256sum -- "$baseline_package" | cut -d' ' -f1)" == "$baseline_package_sha256" ]] || { echo "baseline package SHA-256 mismatch" >&2; exit 1; }

if [[ -z "$root" ]]; then root="$(mktemp -u "${TMPDIR:-/tmp}/unit-test-ide-install-smoke.XXXXXXXX")"; fi
root="$(realpath -m -- "$root")"
evidence="$(realpath -m -- "$evidence")"
[[ "$root" != "/" ]] || { echo "refusing to use filesystem root" >&2; exit 64; }
case "$evidence" in "$root"/*) echo "--evidence must be outside the disposable smoke root" >&2; exit 64 ;; esac
[[ ! -e "$root" ]] || { echo "disposable smoke root already exists: $root" >&2; exit 64; }
mkdir -p -- "$root" "$(dirname -- "$evidence")"
cleanup() { rm -rf -- "$root"; }
trap cleanup EXIT

node "$verify_script" --image "$package" --manifest "$manifest" --require-digest >/dev/null
node "$verify_script" --image "$baseline_package" --manifest "$baseline_manifest" --require-digest >/dev/null
node -e '
  const fs = require("node:fs");
  const [path, version, packageSha, manifestSha] = process.argv.slice(1);
  const value = JSON.parse(fs.readFileSync(path, "utf8"));
  if (value.version !== version || value.packageSha256 !== packageSha || value.releaseManifestSha256 !== manifestSha) process.exit(1);
' "$manifest" "$version" "$package_sha256" "$manifest_sha256"
node -e '
  const fs = require("node:fs");
  const [path, version, packageSha, manifestSha] = process.argv.slice(1);
  const value = JSON.parse(fs.readFileSync(path, "utf8"));
  if (value.version !== version || value.packageSha256 !== packageSha || value.releaseManifestSha256 !== manifestSha) process.exit(1);
' "$baseline_manifest" "$baseline_version" "$baseline_package_sha256" "$baseline_manifest_sha256"

extract_payload() {
  local image="$1" destination="$2"
  mkdir -p -- "$destination"
  chmod u+x -- "$image"
  (cd -- "$destination" && "$image" --appimage-extract >/dev/null)
  printf '%s\n' "$destination/squashfs-root/usr/lib/unit-test-ide"
}
target_payload="$(extract_payload "$package" "$root/target-appimage")"
baseline_payload="$(extract_payload "$baseline_package" "$root/baseline-appimage")"

node "$update_script" smoke \
  --platform linux \
  --root "$root/lifecycle" \
  --evidence "$evidence" \
  --package "$package" \
  --package-sha256 "$package_sha256" \
  --manifest-sha256 "$manifest_sha256" \
  --version "$version" \
  --artifact "$target_payload" \
  --baseline-artifact "$baseline_payload" >/dev/null

node -e '
  const fs = require("node:fs");
  const [path, packageSha, manifestSha, version, baselineVersion] = process.argv.slice(1);
  const value = JSON.parse(fs.readFileSync(path, "utf8"));
  if (value.platform !== "linux" || value.packageSha256 !== packageSha || value.manifestSha256 !== manifestSha || value.version !== version || value.rollbackVersion !== baselineVersion || value.outcomes?.upgradeLaunch !== "failed-as-expected" || value.outcomes?.rollbackLaunch !== "pass" || value.outcomes?.packageResidueAbsent !== "pass") process.exit(1);
' "$evidence" "$package_sha256" "$manifest_sha256" "$version" "$baseline_version"
