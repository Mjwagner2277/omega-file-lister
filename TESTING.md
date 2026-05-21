# Testing and Benchmarks

The integration suite looks for the Debian ISO fixture in this order:

1. `LFL_DEBIAN_ISO`
2. `/private/tmp/debian-13.5.0-amd64-netinst.iso`

If the ISO is not present, the Debian integration test and benchmark are skipped.
The unit suite creates temporary fixtures for archive types that are not covered
by the Debian ISO: ZIP, tar, tar.gz, gzip single-file streams, cpio `newc`, and
tar.bz2 when the `bzip2` helper is installed.

## Latest Local Results

Environment:

- Host: macOS, Apple M4 Pro
- Go: go1.26.3 darwin/arm64
- Debian fixture: `/private/tmp/debian-13.5.0-amd64-netinst.iso`, 755 MiB
- Debian entries listed with nested compressed-file expansion: 14,627

Commands:

```sh
GOCACHE=/private/tmp/file-lister/.cache/go-build \
GOMODCACHE=/private/tmp/file-lister/.cache/go-mod \
go test ./...

GOCACHE=/private/tmp/file-lister/.cache/go-build \
GOMODCACHE=/private/tmp/file-lister/.cache/go-mod \
go test -run '^$' -bench BenchmarkListDebianISO -benchmem -count 5 ./internal/lister
```

Results after nested ISO expansion and streaming gzip optimization:

```text
BenchmarkListDebianISO-12       1    1486539000 ns/op    276860304 B/op    468115 allocs/op
BenchmarkListDebianISO-12       1    1563266917 ns/op    276865384 B/op    468118 allocs/op
BenchmarkListDebianISO-12       1    1476105292 ns/op    276868344 B/op    468114 allocs/op
```

The scanner still avoids reading the full ISO. It walks directory extents, reads
only compressed/archive candidate file extents, and streams single-file gzip, bzip2, xz, or
zstd payloads to avoid retaining large decompressed package indexes in memory.
This benchmark now includes nested compressed-file expansion, so it is not
directly comparable to the earlier directory-only ISO benchmark.

## Rocky boot ISO SquashFS check

With `unsquashfs` installed, `lfl /private/tmp/Rocky-9-latest-x86_64-boot.iso`
listed 60,767 entries, including files under `IMAGES/install.img!`. The flat ISO
tree from `bsdtar -tf` lists about 31 entries, which confirms that the large
count discrepancy comes from files inside the SquashFS `install.img` payload.

The ISO path also merges `bsdtar`/libarchive catalog entries case-insensitively
with the native ISO walk. This avoids double-counting ISO-9660 uppercase names
versus Rock Ridge/Joliet-style names while still including entries that only
libarchive can see in repacked images.
