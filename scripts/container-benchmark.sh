#!/usr/bin/env bash
set -euo pipefail

apt-get update >/tmp/apt-update.log
: >/out/benchmark.log

apt-get install -y --no-install-recommends \
  util-linux mount squashfs-tools time zip unzip tar gzip bzip2 xz-utils zstd \
  rpm2cpio cpio rpm ca-certificates >/tmp/apt-install.log

printf "cpu_count=%s\n" "$(nproc)" | tee /out/environment.txt
command -v unsquashfs | tee -a /out/environment.txt
command -v rpm2cpio | tee -a /out/environment.txt

for iso in small medium large; do
  for i in 1 2 3; do
    /usr/bin/time -p -o "/out/${iso}-${i}.time" \
      lfl "/fixtures/lfl-${iso}.iso" >"/out/${iso}-${i}.out"
    {
      printf "%s run %s lines " "$iso" "$i"
      wc -l <"/out/${iso}-${i}.out"
      cat "/out/${iso}-${i}.time"
    } | tee -a /out/benchmark.log
  done
done

/usr/bin/time -p -o /out/large-workers1.time \
  lfl -workers 1 /fixtures/lfl-large.iso >/out/large-workers1.out
/usr/bin/time -p -o /out/large-workers8.time \
  lfl -workers 8 /fixtures/lfl-large.iso >/out/large-workers8.out

{
  printf "large workers1 lines "
  wc -l </out/large-workers1.out
  cat /out/large-workers1.time
  printf "large workers8 lines "
  wc -l </out/large-workers8.out
  cat /out/large-workers8.time
} | tee -a /out/benchmark.log

grep -F "archivetar.gz!nested-1.txt" /out/small-1.out >/out/check-small.txt
grep -F "payload.zip!zip-1.txt" /out/medium-1.out >/out/check-medium.txt
grep -F "filesystem.squashfs!etc1/squash-1.conf" /out/large-1.out >/out/check-large.txt

mkdir -p /tmp/lfl-compress/src /tmp/lfl-compress/rpmbuild/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
printf alpha >/tmp/lfl-compress/src/alpha.txt
printf beta >/tmp/lfl-compress/src/beta.txt
zip -q /tmp/lfl-compress/sample.zip /tmp/lfl-compress/src/alpha.txt /tmp/lfl-compress/src/beta.txt
tar -C /tmp/lfl-compress/src -cf /tmp/lfl-compress/sample.tar alpha.txt beta.txt
tar -C /tmp/lfl-compress/src -czf /tmp/lfl-compress/sample.tar.gz alpha.txt beta.txt
gzip -c /tmp/lfl-compress/src/alpha.txt >/tmp/lfl-compress/sample.txt.gz
find /tmp/lfl-compress/src -type f -printf "%P\n" |
  cpio -o -H newc -D /tmp/lfl-compress/src >/tmp/lfl-compress/sample.cpio

cat >/tmp/lfl-compress/rpmbuild/SPECS/lfl-fixture.spec <<'RPM_SPEC'
Name: lfl-fixture
Version: 1.0
Release: 1
Summary: fixture rpm
License: MIT
BuildArch: noarch
%description
fixture rpm
%install
mkdir -p %{buildroot}/opt/lfl-fixture
printf rpm-fixture > %{buildroot}/opt/lfl-fixture/rpm-file.txt
%files
/opt/lfl-fixture/rpm-file.txt
RPM_SPEC

rpmbuild --define "_topdir /tmp/lfl-compress/rpmbuild" \
  -bb /tmp/lfl-compress/rpmbuild/SPECS/lfl-fixture.spec >/tmp/rpmbuild.log

for f in sample.zip sample.tar sample.tar.gz sample.txt.gz sample.cpio; do
  lfl "/tmp/lfl-compress/${f}" >"/out/${f}.out"
done
rpmfile="$(find /tmp/lfl-compress/rpmbuild/RPMS -name "*.rpm" | head -n 1)"
lfl "$rpmfile" >/out/sample.rpm.out

grep -F "alpha.txt" /out/sample.zip.out >/out/check-zip.txt
grep -F "alpha.txt" /out/sample.tar.out >/out/check-tar.txt
grep -F "alpha.txt" /out/sample.tar.gz.out >/out/check-targz.txt
grep -F "content" /out/sample.txt.gz.out >/out/check-gzip.txt
grep -F "alpha.txt" /out/sample.cpio.out >/out/check-cpio.txt
grep -F "opt/lfl-fixture/rpm-file.txt" /out/sample.rpm.out >/out/check-rpm.txt
printf "format checks ok\n" | tee -a /out/benchmark.log
