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
- Debian entries listed with nested compressed-file expansion: 12,546

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
BenchmarkListDebianISO-12       1    1513953875 ns/op    208168968 B/op    323389 allocs/op
BenchmarkListDebianISO-12       1    1445554459 ns/op    208168744 B/op    323385 allocs/op
BenchmarkListDebianISO-12       1    1441386000 ns/op    208160512 B/op    323382 allocs/op
BenchmarkListDebianISO-12       1    1523847291 ns/op    208168712 B/op    323383 allocs/op
BenchmarkListDebianISO-12       1    1430671208 ns/op    208160496 B/op    323381 allocs/op
```

The scanner still avoids reading the full ISO. It walks directory extents, reads
only compressed/archive candidate file extents, and streams single-file gzip or
bzip2 payloads to avoid retaining large decompressed package indexes in memory.
This benchmark now includes nested compressed-file expansion, so it is not
directly comparable to the earlier directory-only ISO benchmark.
