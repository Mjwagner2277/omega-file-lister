# Linux File Lister

`lfl` lists file names inside archives and disk images without extracting them.

The primary fast path is a native ISO-9660 scanner that reads directory extents
directly with `io.ReaderAt`, avoiding mounts and full-image extraction. Common
streaming formats are handled natively where Go's standard library supports
them, with Linux tool fallbacks for broader archive coverage.

## Supported inputs

- ISO-9660 images, including basic Rock Ridge names
- tar, tar.gz, tar.bz2, tgz, tbz2
- zip, jar, war
- gzip and bzip2 single-file streams
- cpio `newc` archives
- rpm packages with supported payload compressors
- fallback listing through installed tools: `bsdtar`, `tar`, `7z`, `unrar`,
  `rpm2cpio`, `xz`, `zstd`, `gzip`, `bzip2`

## Build

```sh
go build ./cmd/lfl
```

## Usage

```sh
lfl path/to/archive.iso
lfl -json path/to/package.rpm
lfl -workers 16 path/to/image.iso
```

The default output is one path per line. JSON output emits records with path,
type, size, and source format.

