#!/usr/bin/env bash
set -euo pipefail

readonly BUILDER_IMAGE='quay.io/pypa/manylinux_2_28_x86_64@sha256:0c87ccb5996dab6c3b7612ee4fda7b80c4ab3c44a86c2541e4a872afdf4f131b'
readonly PYTHON_VERSION='3.14.6'
readonly SOURCE_DATE_EPOCH=1785715200
# manylinux_2_28 fixes the minimum supported glibc ABI at 2.28.

if [[ "${1:-}" != '--inside-builder' ]]; then
  [[ $# -eq 6 ]] || { echo 'usage: build-linux.sh SOURCE OUTPUT SHA256 IMAGE LZMA_SOURCE LZMA_SHA256' >&2; exit 2; }
  source_archive=$(realpath "$1")
  output=$(realpath -m "$2")
  expected_sha256=$3
  image=$4
  lzma_archive=$(realpath "$5")
  expected_lzma_sha256=$6
  [[ "$image" == "$BUILDER_IMAGE" ]] || { echo 'unexpected Linux builder image' >&2; exit 2; }
  mkdir -p "$output"
  host_uid=$(id -u)
  host_gid=$(id -g)
  engine=${COVERAGE_BUNDLE_CONTAINER_ENGINE:-docker}
  exec "$engine" run --rm --network none \
    --env "HOST_UID=$host_uid" \
    --env "HOST_GID=$host_gid" \
    --mount "type=bind,src=$source_archive,dst=/input/Python-$PYTHON_VERSION.tgz,readonly" \
    --mount "type=bind,src=$lzma_archive,dst=/input/xz-5.8.1.tar.gz,readonly" \
    --mount "type=bind,src=$output,dst=/out" \
    --mount "type=bind,src=$(realpath "$0"),dst=/work/build-linux.sh,readonly" \
    "$BUILDER_IMAGE" /bin/bash /work/build-linux.sh --inside-builder "$expected_sha256" "$expected_lzma_sha256"
fi

[[ $# -eq 3 ]] || { echo 'invalid builder invocation' >&2; exit 2; }
expected_sha256=$2
expected_lzma_sha256=$3
[[ "${HOST_UID:-}" =~ ^[0-9]+$ && "${HOST_GID:-}" =~ ^[0-9]+$ ]] || { echo 'invalid host ownership identity' >&2; exit 2; }
restore_output_ownership() {
  chown -R "$HOST_UID:$HOST_GID" /out
}
trap restore_output_ownership EXIT
export SOURCE_DATE_EPOCH PYTHONHASHSEED=0 LC_ALL=C TZ=UTC
printf '%s  %s\n' "$expected_sha256" "/input/Python-$PYTHON_VERSION.tgz" | sha256sum --check --strict
printf '%s  %s\n' "$expected_lzma_sha256" "/input/xz-5.8.1.tar.gz" | sha256sum --check --strict
rm -rf /build/cpython /build/lzma /build/source /out/python
mkdir -p /build/cpython /build/lzma /build/source /out/python
tar --extract --gzip --file "/input/Python-$PYTHON_VERSION.tgz" --directory /build/source --no-same-owner --no-same-permissions
tar --extract --gzip --file "/input/xz-5.8.1.tar.gz" --directory /build/source --no-same-owner --no-same-permissions
source_root="/build/source/Python-$PYTHON_VERSION"
lzma_root="/build/source/xz-5.8.1"
[[ -x "$source_root/configure" ]] || { echo 'unexpected Python source layout' >&2; exit 2; }
[[ -x "$lzma_root/configure" ]] || { echo 'unexpected XZ source layout' >&2; exit 2; }
printf '%s\n' '*disabled*' '_tkinter' > "$source_root/Modules/Setup.local"
cd /build/lzma
CFLAGS='-O2 -g0 -fPIC -ffile-prefix-map=/build=/usr/src/xz' \
  "$lzma_root/configure" \
    --prefix=/build/lzma/install \
    --disable-doc \
    --disable-nls \
    --disable-static \
    --enable-shared
make -j1
make install
cd /build/cpython
LIBLZMA_CFLAGS='-I/build/lzma/install/include' LIBLZMA_LIBS='-L/build/lzma/install/lib -llzma' \
LDFLAGS='-Wl,-rpath,\$ORIGIN/../lib' CFLAGS='-O2 -g0 -ffile-prefix-map=/build=/usr/src/python' \
  "$source_root/configure" \
    --prefix=/ \
    --enable-shared \
    --with-ensurepip=no \
    --disable-test-modules \
    --without-static-libpython
make -j1
make DESTDIR=/out/python install
lzma_runtime=$(find /build/lzma/install/lib -maxdepth 1 -type f -name 'liblzma.so.*' -print -quit)
[[ -f "$lzma_runtime" ]] || { echo 'built liblzma runtime library is missing' >&2; exit 2; }
cp -L "$lzma_runtime" /out/python/lib/liblzma.so.5
rm -rf \
  /out/python/include \
  /out/python/share \
  /out/python/lib/pkgconfig \
  /out/python/lib/python3.14/ensurepip \
  /out/python/lib/python3.14/idlelib \
  /out/python/lib/python3.14/test \
  /out/python/lib/python3.14/tkinter \
  /out/python/lib/python3.14/config-*
find /out/python -type l \( -iname 'libtcl*' -o -iname 'libtk*' \) -delete
find /out/python -type f \( -name '*.a' -o -name '*.pyc' -o -name 'pip*' -o -iname '_tkinter*' -o -iname 'tcl*.so*' -o -iname 'tk*.so*' -o -iname 'libtcl*' -o -iname 'libtk*' \) -delete
find /out/python -type d \( -name '__pycache__' -o -name ensurepip -o -name idlelib -o -name test -o -name tests -o -iname tkinter -o -iname 'tcl[0-9]*' -o -iname 'tk[0-9]*' -o -iname 'libtcl*' -o -iname 'libtk*' -o -iname 'lib-tk*' \) -prune -exec rm -rf '{}' +
while IFS= read -r -d '' link; do
  target=$(readlink -f "$link")
  rm "$link"
  cp "$target" "$link"
done < <(find /out/python -type l -print0)
if [[ -f /out/python/bin/python3.14 ]]; then cp /out/python/bin/python3.14 /out/python/bin/python3; fi
find /out/python/bin -type f ! -name python3 -delete
mv /out/python/bin/python3 /out/python/bin/python3.bin
cat > /out/python/bin/python3 <<'PYTHON_LAUNCHER'
#!/bin/sh
set -eu
self_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export LD_LIBRARY_PATH="$self_dir/../lib"
exec "$self_dir/python3.bin" "$@"
PYTHON_LAUNCHER
chmod 0755 /out/python/bin/python3
find /out/python -type f -exec touch --date="@$SOURCE_DATE_EPOCH" '{}' +
