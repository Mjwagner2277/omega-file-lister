# Benchmarks

Benchmarks were run in a privileged Debian `bookworm-slim` Linux container on
May 26, 2026. The container mounted only the `lfl` binary, three ISO fixtures,
the benchmark script, and the output directory. The container reported 12 CPUs
and had `unsquashfs` and `rpm2cpio` installed.

## ISO Runs

| Fixture | ISO size | Nested payload | Listed entries | Runs (real sec) | Average |
| --- | ---: | --- | ---: | --- | ---: |
| `lfl-small.iso` | 900 KiB | tar.gz with 20 files | 152 | 0.00, 0.00, 0.00 | 0.000s |
| `lfl-medium.iso` | 11 MiB | zip with 500 files | 5,601 | 0.15, 0.20, 0.18 | 0.177s |
| `lfl-large.iso` | 46 MiB | SquashFS with 10,000 files | 30,301 | 1.16, 1.31, 1.00 | 1.157s |

The large ISO produced the same 30,301 entries with `-workers 1` and
`-workers 8`. Runtime improved from 1.33s to 1.06s on this fixture. This worker
pool is used for mounted-ISO nested archive expansion; the mounted filesystem
walk remains deterministic and serial.

## Streaming Memory Comparison

A focused Linux RSS comparison was run with the `v0.2.8` tag and the current
working tree. Each fixture contains 100,000 entries and was run three times with
`/usr/bin/time -v` in a Debian container.

| Fixture | Version | Avg max RSS | Runs max RSS (KB) | Listed entries |
| --- | --- | ---: | --- | ---: |
| `many.zip` | `v0.2.8` | 50.0 MiB | 52,304, 50,672, 50,648 | 100,000 |
| `many.zip` | current | 35.7 MiB | 36,368, 36,584, 36,572 | 100,000 |
| `many.tar` | `v0.2.8` | 147.6 MiB | 151,148, 151,144, 151,200 | 100,000 |
| `many.tar` | current | 11.1 MiB | 11,328, 11,408, 11,340 | 100,000 |

The TAR fixture shows the biggest drop because the current code streams from the
archive file path instead of reading the full 98 MiB tar file into memory before
listing entries. ZIP still needs central-directory metadata, but it no longer
loads the entire ZIP payload as one byte slice.

## Large Nested ISO Stress Run

The repo now includes `scripts/container-large-iso-stress.sh` to reproduce a
large nested-payload case. The script generates a synthetic ISO containing a
SquashFS image with 200,000 files plus nested ZIP, tar.gz, tar.xz, CPIO, RPM,
gzip, and xz payloads. It then runs both the default non-sudo ISO reader and the
mounted ISO path in a privileged Debian container with a 1 GiB memory limit.

| Mode | Listed entries | Nested candidates | Elapsed | Max RSS | Verification |
| --- | ---: | ---: | ---: | ---: | --- |
| default non-sudo ISO reader | 200,224 | 8 | 0:00.87 | 14,176 KB | passed |
| `-mount-iso` | 200,224 | 8 | 0:00.88 | 14,176 KB | passed |

Representative verified nested paths:

```text
images/filesystem.squashfs!bulk/dir0000/file000001.txt
images/filesystem.squashfs!bulk/dir0199/file200000.txt
Packages/payload.zip!inner.tgz!nested-tar/deep.txt
images/bundle.tar.gz!bundle/nested.zip!nested-zip.txt
images/bundle.tar.xz!xz-bundle/deep-xz.txt
Packages/direct.cpio!cpio-file.txt
RPMS/stress.rpm!./opt/lfl-stress/rpm-file.txt
```

The generated ISO is small because the files compress heavily, but the listing
work still exercises a mounted/default ISO scan with hundreds of thousands of
nested filesystem entries and multiple nested archive formats.

Reproduce with:

```sh
scripts/container-large-iso-stress.sh .container-results/large-iso-stress
```

Tune the stress size or container memory limit with:

```sh
LFL_STRESS_FILE_COUNT=300000 LFL_STRESS_MEMORY=2g \
  scripts/container-large-iso-stress.sh .container-results/large-iso-stress
```

## Verification

The ISO checks confirmed that mounted ISO entries and nested archive entries are
both emitted:

```text
archivetar.gz!nested-1.txt
payload.zip!zip-1.txt
filesystem.squashfs!etc1/squash-1.conf
```

Standalone compressed/archive checks were also run in the same Linux container:

| Format | Verification path |
| --- | --- |
| zip | `tmp/lfl-compress/src/alpha.txt` |
| tar | `alpha.txt` |
| tar.gz | `alpha.txt` |
| gzip | `content` |
| cpio | `alpha.txt` |
| rpm | `./opt/lfl-fixture/rpm-file.txt` |

To reproduce the container run after building a Linux binary, use:

```sh
docker run --rm --privileged \
  -v /path/to/lfl-linux:/usr/local/bin/lfl:ro \
  -v /path/to/lfl-small.iso:/fixtures/lfl-small.iso:ro \
  -v /path/to/lfl-medium.iso:/fixtures/lfl-medium.iso:ro \
  -v /path/to/lfl-large.iso:/fixtures/lfl-large.iso:ro \
  -v "$PWD/scripts/container-benchmark.sh:/usr/local/bin/container-benchmark:ro" \
  -v "$PWD/.container-results:/out" \
  debian:bookworm-slim bash /usr/local/bin/container-benchmark
```

## Public Distro ISO Runs

These benchmarks use public Linux distribution installer ISOs downloaded on May
26, 2026. Each ISO was mounted read-only in the same privileged Debian Linux
container, and each run recursively expanded supported compressed/archive files
inside the mounted ISO view.

| Distro ISO | Source image | ISO size | Listed entries | Nested entries | Runs (real sec) | Average |
| --- | --- | ---: | ---: | ---: | --- | ---: |
| Alpine Linux | `alpine-standard-3.23.0-x86_64.iso` | 344 MiB | 132 | 3 | 0.01, 0.00, 0.00 | 0.003s |
| Debian | `debian-13.5.0-amd64-netinst.iso` | 755 MiB | 14,627 | 13,126 | 2.66, 2.60, 2.64 | 2.633s |
| Rocky Linux | `Rocky-9-latest-x86_64-boot.iso` | 1.3 GiB | 60,765 | 60,735 | 10.05, 9.29, 9.18 | 9.507s |

Nested expansion samples from the public ISO run:

```text
apks/x86_64/APKINDEX.tar.gz!APKINDEX
dists/trixie/main/binary-amd64/Packages.gz!content
images/install.img!boot/.vmlinuz-5.14.0-611.5.1.el9_7.x86_64.hmac
```

The Rocky result is the best real-world stress case in this group: the ISO root
is small, but `images/install.img` expands into tens of thousands of entries.
