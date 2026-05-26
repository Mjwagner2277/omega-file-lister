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
