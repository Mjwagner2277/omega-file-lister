#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 /path/to/image.iso [output-dir]" >&2
  exit 2
fi

iso=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
out_dir=${2:-.container-results}
mkdir -p "$out_dir"
out_dir=$(cd "$out_dir" && pwd)

arch=$(docker version --format '{{.Server.Arch}}')
case "$arch" in
  arm64|aarch64) goarch=arm64 ;;
  amd64|x86_64) goarch=amd64 ;;
  *) echo "unsupported Docker server arch: $arch" >&2; exit 1 ;;
esac

bin=$(GOARCH="$goarch" scripts/build-linux-binary.sh ".cache/lfl-linux-$goarch")
repo=$(pwd)

cat >&2 <<MSG
Running a privileged Linux container so ISO mount -o loop,ro can work.
Mounted host paths are intentionally narrow:
  binary: $repo/$bin -> /lfl:ro
  iso:    $iso -> /input.iso:ro
  output: $out_dir -> /out
MSG

docker run --rm --privileged \
  -v "$repo/$bin:/lfl:ro" \
  -v "$iso:/input.iso:ro" \
  -v "$out_dir:/out" \
  debian:bookworm-slim \
  sh -lc 'apt-get update >/dev/null && apt-get install -y --no-install-recommends mount util-linux squashfs-tools libarchive-tools xz-utils zstd time >/dev/null && /usr/bin/time -p /lfl /input.iso >/out/mounted.out && wc -l /out/mounted.out'
