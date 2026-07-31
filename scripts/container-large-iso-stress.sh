#!/usr/bin/env bash
set -euo pipefail

out_dir=${1:-.container-results/large-iso-stress}
file_count=${LFL_STRESS_FILE_COUNT:-200000}
memory_limit=${LFL_STRESS_MEMORY:-1g}

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
Running large ISO stress test in a privileged Linux container.
  files inside nested SquashFS: $file_count
  container memory limit:        $memory_limit
  binary:                        $repo/$bin -> /usr/local/bin/lfl:ro
  output:                        $out_dir -> /out
MSG

docker run --rm -i --privileged --memory "$memory_limit" \
  -e LFL_STRESS_FILE_COUNT="$file_count" \
  -v "$repo/$bin:/usr/local/bin/lfl:ro" \
  -v "$out_dir:/out" \
  debian:bookworm-slim \
  bash -s <<'CONTAINER'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

apt-get update >/tmp/apt-update.log
apt-get install -y --no-install-recommends \
  bash ca-certificates coreutils findutils libarchive-tools mount util-linux \
  squashfs-tools xorriso zip unzip tar gzip xz-utils zstd cpio rpm rpm2cpio \
  python3 time >/tmp/apt-install.log

work=/work
iso_root=$work/iso-root
many_root=$work/many-root
mkdir -p "$iso_root" "$many_root" /out

python3 - <<'PYCONTAINER'
import gzip
import lzma
import os
from pathlib import Path
import tarfile
import zipfile
import io

count = int(os.environ["LFL_STRESS_FILE_COUNT"])
work = Path("/work")
iso_root = work / "iso-root"
many_root = work / "many-root"

(iso_root / "Packages").mkdir(parents=True, exist_ok=True)
(iso_root / "images").mkdir(parents=True, exist_ok=True)
(iso_root / "RPMS").mkdir(parents=True, exist_ok=True)
(iso_root / "README.txt").write_text("large stress ISO fixture\n")

for i in range(1, count + 1):
    d = many_root / "bulk" / f"dir{(i - 1) // 1000:04d}"
    d.mkdir(parents=True, exist_ok=True)
    (d / f"file{i:06d}.txt").write_text(f"payload {i}\n")

zip_path = iso_root / "Packages" / "payload.zip"
inner_tgz = work / "inner.tgz"
with tarfile.open(inner_tgz, "w:gz") as tf:
    data = b"nested tar gzip\n"
    info = tarfile.TarInfo("nested-tar/deep.txt")
    info.size = len(data)
    tf.addfile(info, fileobj=io.BytesIO(data))
with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    zf.writestr("zip-1.txt", "zip payload\n")
    zf.write(inner_tgz, "inner.tgz")

bundle_tgz = iso_root / "images" / "bundle.tar.gz"
with tarfile.open(bundle_tgz, "w:gz") as tf:
    data = b"tar gzip payload\n"
    info = tarfile.TarInfo("bundle/deep.txt")
    info.size = len(data)
    tf.addfile(info, fileobj=io.BytesIO(data))
    nested_zip = work / "nested.zip"
    with zipfile.ZipFile(nested_zip, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("nested-zip.txt", "zip inside tar\n")
    tf.add(nested_zip, arcname="bundle/nested.zip")

bundle_txz = iso_root / "images" / "bundle.tar.xz"
with tarfile.open(bundle_txz, "w:xz") as tf:
    data = b"tar xz payload\n"
    info = tarfile.TarInfo("xz-bundle/deep-xz.txt")
    info.size = len(data)
    tf.addfile(info, fileobj=io.BytesIO(data))

with gzip.open(iso_root / "Packages" / "single.txt.gz", "wb") as f:
    f.write(b"single gzip payload\n")
with lzma.open(iso_root / "Packages" / "single.txt.xz", "wb") as f:
    f.write(b"single xz payload\n")
PYCONTAINER

mksquashfs "$many_root" "$iso_root/images/filesystem.squashfs" \
  -quiet -noappend -processors "$(nproc)" >/tmp/mksquashfs.log

mkdir -p /work/cpio-src
printf 'cpio payload\n' >/work/cpio-src/cpio-file.txt
(
  cd /work/cpio-src
  find . -type f -printf '%P\n' | cpio -o -H newc >/work/iso-root/Packages/direct.cpio
) >/tmp/cpio.log 2>&1

mkdir -p /work/rpmbuild/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
cat >/work/rpmbuild/SPECS/lfl-stress.spec <<'RPM_SPEC'
Name: lfl-stress
Version: 1.0
Release: 1
Summary: lfl stress fixture rpm
License: MIT
BuildArch: noarch
%description
lfl stress fixture rpm
%install
mkdir -p %{buildroot}/opt/lfl-stress
printf rpm-stress > %{buildroot}/opt/lfl-stress/rpm-file.txt
%files
/opt/lfl-stress/rpm-file.txt
RPM_SPEC
rpmbuild --define "_topdir /work/rpmbuild" -bb /work/rpmbuild/SPECS/lfl-stress.spec >/tmp/rpmbuild.log 2>&1
cp /work/rpmbuild/RPMS/noarch/*.rpm /work/iso-root/RPMS/stress.rpm

xorrisofs -quiet -o /work/stress.iso /work/iso-root >/tmp/xorrisofs.log 2>&1
cp /work/stress.iso /out/stress.iso

run_lfl() {
  local mode=$1
  shift
  local run_dir="/work/run-$mode"
  mkdir -p "$run_dir"
  (
    cd "$run_dir"
    /usr/bin/time -v -o "/out/${mode}.time" \
      /usr/local/bin/lfl -workers "$(nproc)" "$@" /work/stress.iso \
      >"/out/${mode}.stdout" 2>"/out/${mode}.stderr"
    mv stress_iso_files "/out/${mode}_files"
  )
  wc -l <"/out/${mode}_files" >"/out/${mode}.lines"
}

run_lfl default
run_lfl mounted -mount-iso

last_dir=$(printf 'dir%04d' $(((LFL_STRESS_FILE_COUNT - 1) / 1000)))
last_file=$(printf 'file%06d.txt' "$LFL_STRESS_FILE_COUNT")

for mode in default mounted; do
  grep -F "images/filesystem.squashfs!bulk/dir0000/file000001.txt" "/out/${mode}_files" >"/out/${mode}.check-first"
  grep -F "images/filesystem.squashfs!bulk/${last_dir}/${last_file}" "/out/${mode}_files" >"/out/${mode}.check-last"
  grep -F "Packages/payload.zip!inner.tgz!nested-tar/deep.txt" "/out/${mode}_files" >"/out/${mode}.check-zip-tgz"
  grep -F "images/bundle.tar.gz!bundle/nested.zip!nested-zip.txt" "/out/${mode}_files" >"/out/${mode}.check-tar-zip"
  grep -F "images/bundle.tar.xz!xz-bundle/deep-xz.txt" "/out/${mode}_files" >"/out/${mode}.check-txz"
  grep -F "Packages/direct.cpio!cpio-file.txt" "/out/${mode}_files" >"/out/${mode}.check-cpio"
  grep -F "RPMS/stress.rpm!./opt/lfl-stress/rpm-file.txt" "/out/${mode}_files" >"/out/${mode}.check-rpm"
  printf '%s lines=%s max_rss_kb=%s elapsed=%s\n' \
    "$mode" \
    "$(cat /out/${mode}.lines)" \
    "$(awk -F: '/Maximum resident set size/ {gsub(/^[ \t]+/, "", $2); print $2}' /out/${mode}.time)" \
    "$(sed -n '/Elapsed (wall clock)/s/.*): //p' /out/${mode}.time)" \
    | tee -a /out/summary.txt
  tail -n 8 "/out/${mode}.stderr" >"/out/${mode}.progress-tail"
done

printf 'large ISO stress checks ok\n' | tee -a /out/summary.txt
CONTAINER
