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
