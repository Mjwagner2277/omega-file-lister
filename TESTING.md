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
- Debian entries listed: 1,501

Commands:

```sh
GOCACHE=/private/tmp/file-lister/.cache/go-build \
GOMODCACHE=/private/tmp/file-lister/.cache/go-mod \
go test ./...

GOCACHE=/private/tmp/file-lister/.cache/go-build \
GOMODCACHE=/private/tmp/file-lister/.cache/go-mod \
go test -run '^$' -bench BenchmarkListDebianISO -benchmem -count 5 ./internal/lister
```

Results after ISO traversal optimization:

```text
BenchmarkListDebianISO-12    2263    521101 ns/op    1414853 B/op    3798 allocs/op
BenchmarkListDebianISO-12    2622    483635 ns/op    1414850 B/op    3798 allocs/op
BenchmarkListDebianISO-12    2346    516090 ns/op    1414851 B/op    3798 allocs/op
BenchmarkListDebianISO-12    2397    478205 ns/op    1414849 B/op    3798 allocs/op
BenchmarkListDebianISO-12    2215    522739 ns/op    1414851 B/op    3798 allocs/op
```

The optimization replaced goroutine-based ISO directory scheduling with a direct
stack walk over directory extents and replaced `path.Join` in the hot loop with
simple slash joining. Compared with the previous benchmark run in this workspace,
Debian ISO listing improved from roughly 0.54-0.56 ms/op and 4,318 allocs/op to
roughly 0.48-0.52 ms/op and 3,798 allocs/op.
