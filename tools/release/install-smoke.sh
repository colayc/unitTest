#!/usr/bin/env bash
set -euo pipefail

script_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
update_script="$script_root/update.mjs"
root=""
evidence=""

while (($# > 0)); do
  case "$1" in
    --root)
      [[ $# -ge 2 ]] || { echo "missing value for --root" >&2; exit 64; }
      root="$2"
      shift 2
      ;;
    --evidence)
      [[ $# -ge 2 ]] || { echo "missing value for --evidence" >&2; exit 64; }
      evidence="$2"
      shift 2
      ;;
    *)
      echo "unknown install smoke flag: $1" >&2
      exit 64
      ;;
  esac
done

[[ -n "$evidence" ]] || { echo "--evidence is required" >&2; exit 64; }
created_root=0
if [[ -z "$root" ]]; then
  root="$(mktemp -d "${TMPDIR:-/tmp}/unit-test-ide-install-smoke.XXXXXXXX")"
  created_root=1
fi

root="$(realpath -m -- "$root")"
evidence="$(realpath -m -- "$evidence")"
[[ "$root" != "/" ]] || { echo "refusing to use filesystem root" >&2; exit 64; }
case "$evidence" in
  "$root"/*)
    echo "--evidence must be outside the disposable smoke root" >&2
    exit 64
    ;;
esac
if ((created_root == 0)); then
  [[ ! -e "$root" ]] || { echo "disposable smoke root already exists: $root" >&2; exit 64; }
  mkdir -p -- "$root"
fi

mkdir -p -- "$(dirname -- "$evidence")"
cleanup() {
  rm -rf -- "$root"
}
trap cleanup EXIT

node "$update_script" smoke \
  --platform linux \
  --root "$root" \
  --evidence "$evidence" >/dev/null

node -e '
  const fs = require("node:fs");
  const evidence = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
  if (evidence.platform !== "linux" || evidence.outcomes?.packageResidueAbsent !== "pass") process.exit(1);
' "$evidence"
