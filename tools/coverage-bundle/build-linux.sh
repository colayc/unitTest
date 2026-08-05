#!/usr/bin/env bash
set -euo pipefail

readonly BUILDER_IMAGE='quay.io/pypa/manylinux_2_28_x86_64@sha256:c7123a4aebb153c1e45b8152f07a64bd950d65e630cfb633a029cc45ee21897c'
readonly PYTHON_VERSION='3.14.6'
readonly SOURCE_DATE_EPOCH=1785715200
# manylinux_2_28 fixes the minimum supported glibc ABI at 2.28.

if [[ "${1:-}" != '--inside-builder' ]]; then
  [[ $# -eq 4 ]] || { echo 'usage: build-linux.sh SOURCE OUTPUT SHA256 IMAGE' >&2; exit 2; }
  source_archive=$(realpath "$1")
  output=$(realpath -m "$2")
  expected_sha256=$3
  image=$4
  [[ "$image" == "$BUILDER_IMAGE" ]] || { echo 'unexpected Linux builder image' >&2; exit 2; }
  mkdir -p "$output"
  engine=${COVERAGE_BUNDLE_CONTAINER_ENGINE:-docker}
  exec "$engine" run --rm --network none \
    --mount "type=bind,src=$source_archive,dst=/input/Python-$PYTHON_VERSION.tgz,readonly" \
    --mount "type=bind,src=$output,dst=/out" \
    --mount "type=bind,src=$(realpath "$0"),dst=/work/build-linux.sh,readonly" \
    "$BUILDER_IMAGE" /bin/bash /work/build-linux.sh --inside-builder "$expected_sha256"
fi

[[ $# -eq 2 ]] || { echo 'invalid builder invocation' >&2; exit 2; }
expected_sha256=$2
export SOURCE_DATE_EPOCH PYTHONHASHSEED=0 LC_ALL=C TZ=UTC
printf '%s  %s\n' "$expected_sha256" "/input/Python-$PYTHON_VERSION.tgz" | sha256sum --check --strict
rm -rf /build/cpython /build/source /out/python
mkdir -p /build/source /build/cpython /out/python
tar --extract --gzip --file "/input/Python-$PYTHON_VERSION.tgz" --directory /build/source --no-same-owner --no-same-permissions
source_root="/build/source/Python-$PYTHON_VERSION"
[[ -x "$source_root/configure" ]] || { echo 'unexpected Python source layout' >&2; exit 2; }
cd /build/cpython
LDFLAGS='-Wl,-rpath,$ORIGIN/../lib' CFLAGS='-O2 -g0 -ffile-prefix-map=/build=/usr/src/python' \
  "$source_root/configure" \
    --prefix=/ \
    --enable-shared \
    --with-ensurepip=no \
    --disable-test-modules \
    --without-static-libpython
make -j1
make DESTDIR=/out/python install
rm -rf \
  /out/python/include \
  /out/python/share \
  /out/python/lib/pkgconfig \
  /out/python/lib/python3.14/ensurepip \
  /out/python/lib/python3.14/idlelib \
  /out/python/lib/python3.14/test \
  /out/python/lib/python3.14/tkinter \
  /out/python/lib/python3.14/config-*
find /out/python -type f \( -name '*.a' -o -name '*.pyc' -o -name 'pip*' \) -delete
find /out/python -type d \( -name '__pycache__' -o -name ensurepip -o -name idlelib -o -name test -o -name tests -o -name tkinter \) -prune -exec rm -rf '{}' +
while IFS= read -r -d '' link; do
  target=$(readlink -f "$link")
  rm "$link"
  cp "$target" "$link"
done < <(find /out/python -type l -print0)
if [[ -f /out/python/bin/python3.14 ]]; then cp /out/python/bin/python3.14 /out/python/bin/python3; fi
find /out/python/bin -type f ! -name python3 -delete
find /out/python -type f -exec touch --date="@$SOURCE_DATE_EPOCH" '{}' +
