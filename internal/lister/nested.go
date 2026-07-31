package lister

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const defaultMaxNestedDepth = 8

func nestedDepth(opts Options) int {
	if opts.MaxNestedDepth > 0 {
		return opts.MaxNestedDepth
	}
	return defaultMaxNestedDepth
}

func isNestedCandidate(name string, size uint32) bool {
	return size > 0 && hasArchiveSuffix(name)
}

func hasArchiveSuffix(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{
		".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst", ".tzst", ".squashfs", ".img", ".zip", ".jar", ".war", ".rpm", ".cpio", ".cpio.gz", ".cpio.xz", ".cpio.zst", ".gz", ".bz2", ".xz", ".zst",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func listArchiveBytes(data []byte, depth int) ([]Entry, error) {
	var entries []Entry
	err := listArchiveBytesTo(data, depth, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listArchiveBytesTo(data []byte, depth int, emit EntryFunc) error {
	return listArchivePayloadTo("", data, depth, emit)
}

func listNestedArchiveBytes(parent string, data []byte, depth int) ([]Entry, error) {
	var entries []Entry
	err := listNestedArchiveBytesTo(parent, data, depth, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listNestedArchiveBytesTo(parent string, data []byte, depth int, emit EntryFunc) error {
	return listArchivePayloadTo(parent, data, depth, emit)
}

// listArchiveFileTo is the low-memory recursive path for archive payloads
// that already exist as files. It lets large nested ISO/package payloads spill
// to disk instead of forcing them into a single heap allocation.
func listArchiveFileTo(parent, path string, depth int, emit EntryFunc) error {
	if depth <= 0 {
		return nil
	}
	head, err := readFilePrefix(path, 64*1024)
	if err != nil {
		return err
	}
	if !hasArchiveMagic(head) {
		return nil
	}

	switch {
	case isZip(head):
		return listZipFileTo(parent, path, depth, emit)
	case isGzip(head):
		return listCompressedFileTo(parent, path, depth, "gzip", emit, func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		})
	case isBzip2(head):
		return listCompressedFileTo(parent, path, depth, "bzip2", emit, func(r io.Reader) (io.Reader, error) {
			return bzip2.NewReader(r), nil
		})
	case isXZ(head):
		return listExternalCompressedFileTo(parent, path, depth, "xz", emit, "xz", "-dc")
	case isZstd(head):
		return listExternalCompressedFileTo(parent, path, depth, "zstd", emit, "zstd", "-dc")
	case isSquashFS(head):
		return listSquashFSFileTo(parent, path, emit)
	case isRPM(head):
		return listRPMFileTo(parent, path, depth, emit)
	case isTar(head):
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return listTarPayloadTo(parent, tar.NewReader(f), depth, "tar", emit)
	case isCPIONewc(head):
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return listCPIOPayloadTo(parent, f, depth, "cpio", emit)
	default:
		return nil
	}
}

func listCompressedFileTo(parent, path string, depth int, format string, emit EntryFunc, open func(io.Reader) (io.Reader, error)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cr, err := open(f)
	if err != nil {
		return err
	}
	if closer, ok := cr.(io.Closer); ok {
		defer closer.Close()
	}
	return listCompressedStreamTo(parent, cr, depth, format, emit)
}

func listCompressedStreamTo(parent string, r io.Reader, depth int, format string, emit EntryFunc) error {
	br := bufio.NewReader(r)
	peek, _ := br.Peek(512)

	if isTar(peek) {
		return listTarPayloadTo(parent, tar.NewReader(br), depth, format+".tar", emit)
	}
	if isCPIONewc(peek) {
		return listCPIOPayloadTo(parent, br, depth, format+".cpio", emit)
	}

	childPath := nestedPath(parent, "content")
	if hasArchiveMagic(peek) {
		tmpName, size, cleanup, err := copyStreamToTemp(br, "lfl-compressed-*")
		if err != nil {
			return err
		}
		defer cleanup()
		if err := emit(Entry{Path: childPath, Type: "file", Size: size, Format: format, Comment: compressedComment(parent, format)}); err != nil {
			return err
		}
		return listArchiveFileTo(childPath, tmpName, depth-1, emit)
	}

	size, err := io.Copy(io.Discard, br)
	if err != nil {
		return err
	}
	return emit(Entry{Path: childPath, Type: "file", Size: size, Format: format, Comment: compressedComment(parent, format)})
}

func listExternalCompressedFileTo(parent, path string, depth int, format string, emit EntryFunc, argv ...string) error {
	if len(argv) == 0 {
		return nil
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return emit(Entry{Path: nestedPath(parent, "content"), Type: "file", Format: format, Comment: "compressed stream; helper not installed for recursive expansion"})
	}
	args := append([]string{}, argv[1:]...)
	args = append(args, path)
	cmd := exec.Command(argv[0], args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	listErr := listCompressedStreamTo(parent, stdout, depth, format, emit)
	if listErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return listErr
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("decompress %s: %w: %s", format, err, msg)
		}
		return fmt.Errorf("decompress %s: %w", format, err)
	}
	return nil
}

// listArchivePayloadTo is the central recursive dispatcher for supported payloads.
func listArchivePayloadTo(parent string, data []byte, depth int, emit EntryFunc) error {
	if depth <= 0 || len(data) == 0 {
		return nil
	}
	head := data
	if len(head) > 64*1024 {
		head = head[:64*1024]
	}

	switch {
	case isZip(head):
		return listZipPayloadTo(parent, data, depth, emit)
	case isGzip(head):
		return listCompressedPayloadTo(parent, data, depth, "gzip", emit, func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		})
	case isBzip2(head):
		return listCompressedPayloadTo(parent, data, depth, "bzip2", emit, func(r io.Reader) (io.Reader, error) {
			return bzip2.NewReader(r), nil
		})
	case isXZ(head):
		return listExternalCompressedPayloadTo(parent, data, depth, "xz", emit, "xz", "-dc")
	case isZstd(head):
		return listExternalCompressedPayloadTo(parent, data, depth, "zstd", emit, "zstd", "-dc")
	case isSquashFS(head):
		return listSquashFSPayloadTo(parent, data, emit)
	case isRPM(head):
		return listRPMPayloadTo(parent, data, depth, emit)
	case isTar(head):
		return listTarPayloadTo(parent, tar.NewReader(bytes.NewReader(data)), depth, "tar", emit)
	case isCPIONewc(head):
		return listCPIOPayloadTo(parent, bytes.NewReader(data), depth, "cpio", emit)
	default:
		return nil
	}
}

func listArchivePayload(parent string, data []byte, depth int) ([]Entry, error) {
	var entries []Entry
	err := listArchivePayloadTo(parent, data, depth, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listZipPayload(parent string, data []byte, depth int) ([]Entry, error) {
	var entries []Entry
	err := listZipPayloadTo(parent, data, depth, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listZipPayloadTo(parent string, data []byte, depth int, emit EntryFunc) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		typ := "file"
		if f.FileInfo().IsDir() {
			typ = "dir"
		}
		childPath := nestedPath(parent, f.Name)
		if err := emit(Entry{Path: childPath, Type: typ, Size: int64(f.UncompressedSize64), Format: "zip", Comment: archiveComment(parent, "zip")}); err != nil {
			return err
		}
		if typ != "file" || !hasArchiveSuffix(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmpName, _, cleanup, readErr := copyStreamToTemp(rc, "lfl-zip-entry-*")
		closeErr := rc.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			cleanup()
			return closeErr
		}
		if err := listArchiveFileTo(childPath, tmpName, depth-1, emit); err != nil {
			cleanup()
			return err
		}
		cleanup()
	}
	return nil
}

func listZipFileTo(parent, path string, depth int, emit EntryFunc) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		typ := "file"
		if f.FileInfo().IsDir() {
			typ = "dir"
		}
		childPath := nestedPath(parent, f.Name)
		if err := emit(Entry{Path: childPath, Type: typ, Size: int64(f.UncompressedSize64), Format: "zip", Comment: archiveComment(parent, "zip")}); err != nil {
			return err
		}
		if typ != "file" || !hasArchiveSuffix(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmpName, _, cleanup, readErr := copyStreamToTemp(rc, "lfl-zip-entry-*")
		closeErr := rc.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			cleanup()
			return closeErr
		}
		if err := listArchiveFileTo(childPath, tmpName, depth-1, emit); err != nil {
			cleanup()
			return err
		}
		cleanup()
	}
	return nil
}

func listTarPayload(parent string, tr *tar.Reader, depth int, format string) ([]Entry, error) {
	var entries []Entry
	err := listTarPayloadTo(parent, tr, depth, format, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listTarPayloadTo(parent string, tr *tar.Reader, depth int, format string, emit EntryFunc) error {
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		typ := "file"
		switch h.Typeflag {
		case tar.TypeDir:
			typ = "dir"
		case tar.TypeSymlink, tar.TypeLink:
			typ = "link"
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name == "" {
			continue
		}
		childPath := nestedPath(parent, name)
		if err := emit(Entry{Path: childPath, Type: typ, Size: h.Size, Format: format, Comment: archiveComment(parent, format)}); err != nil {
			return err
		}
		if typ != "file" || !hasArchiveSuffix(name) {
			continue
		}
		tmpName, _, cleanup, err := copyStreamToTemp(tr, "lfl-tar-entry-*")
		if err != nil {
			return err
		}
		if err := listArchiveFileTo(childPath, tmpName, depth-1, emit); err != nil {
			cleanup()
			return err
		}
		cleanup()
	}
	return nil
}

func listCompressedPayload(parent string, data []byte, depth int, format string, open func(io.Reader) (io.Reader, error)) ([]Entry, error) {
	var entries []Entry
	err := listCompressedPayloadTo(parent, data, depth, format, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	}, open)
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listCompressedPayloadTo(parent string, data []byte, depth int, format string, emit EntryFunc, open func(io.Reader) (io.Reader, error)) error {
	cr, err := open(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if closer, ok := cr.(io.Closer); ok {
		defer closer.Close()
	}
	return listCompressedStreamTo(parent, cr, depth, format, emit)
}

func listCPIOPayload(parent string, r io.Reader, depth int, format string) ([]Entry, error) {
	var entries []Entry
	err := listCPIOPayloadTo(parent, r, depth, format, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listCPIOPayloadTo(parent string, r io.Reader, depth int, format string, emit EntryFunc) error {
	for {
		h := make([]byte, 110)
		if _, err := io.ReadFull(r, h); err != nil {
			return err
		}
		if string(h[:6]) != "070701" && string(h[:6]) != "070702" {
			return io.ErrUnexpectedEOF
		}
		mode, err := parseHexField(h[14:22])
		if err != nil {
			return err
		}
		size, err := parseHexField(h[54:62])
		if err != nil {
			return err
		}
		nameSize, err := parseHexField(h[94:102])
		if err != nil {
			return err
		}
		nameBytes := make([]byte, nameSize)
		if _, err := io.ReadFull(r, nameBytes); err != nil {
			return err
		}
		if err := skipPadding(r, pad4(110+int64(nameSize))); err != nil {
			return err
		}
		name := strings.TrimRight(string(nameBytes), "\x00")
		if name == "TRAILER!!!" {
			break
		}
		typ := "file"
		switch mode & 0170000 {
		case 0040000:
			typ = "dir"
		case 0120000:
			typ = "link"
		}
		childPath := nestedPath(parent, name)
		if name != "" {
			if err := emit(Entry{Path: childPath, Type: typ, Size: int64(size), Format: format, Comment: archiveComment(parent, format)}); err != nil {
				return err
			}
		}
		if typ == "file" && hasArchiveSuffix(name) && depth > 0 {
			tmpName, cleanup, err := copyNToTemp(r, int64(size), "lfl-cpio-entry-*")
			if err != nil {
				return err
			}
			if err := listArchiveFileTo(childPath, tmpName, depth-1, emit); err != nil {
				cleanup()
				return err
			}
			cleanup()
		} else if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return err
		}
		if err := skipPadding(r, pad4(int64(size))); err != nil {
			return err
		}
	}
	return nil
}

func listSquashFSPayload(parent string, data []byte) ([]Entry, error) {
	var entries []Entry
	err := listSquashFSPayloadTo(parent, data, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listSquashFSPayloadTo(parent string, data []byte, emit EntryFunc) error {
	tmp, err := os.CreateTemp("", "lfl-squashfs-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return listSquashFSFileTo(parent, name, emit)
}

func listSquashFSFileTo(parent, path string, emit EntryFunc) error {
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		return emit(Entry{Path: nestedPath(parent, "content"), Type: "file", Format: "squashfs", Comment: "SquashFS image; install unsquashfs for recursive expansion"})
	}
	cmd := exec.Command("unsquashfs", "-ll", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry, ok := squashFSEntry(parent, scanner.Text())
		if !ok {
			continue
		}
		if err := emit(entry); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("list squashfs: %w: %s", err, msg)
		}
		return fmt.Errorf("list squashfs: %w", err)
	}
	return nil
}

func squashFSEntry(parent, line string) (Entry, bool) {
	idx := strings.Index(line, "squashfs-root")
	if idx < 0 {
		return Entry{}, false
	}
	name := strings.TrimPrefix(line[idx:], "squashfs-root")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return Entry{}, false
	}
	typ := "file"
	if strings.HasPrefix(line, "d") {
		typ = "dir"
	} else if strings.HasPrefix(line, "l") {
		typ = "link"
	}
	return Entry{Path: nestedPath(parent, name), Type: typ, Format: "squashfs", Comment: archiveComment(parent, "squashfs")}, true
}

func listExternalCompressedPayload(parent string, data []byte, depth int, format string, argv ...string) ([]Entry, error) {
	var entries []Entry
	err := listExternalCompressedPayloadTo(parent, data, depth, format, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	}, argv...)
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listExternalCompressedPayloadTo(parent string, data []byte, depth int, format string, emit EntryFunc, argv ...string) error {
	if len(argv) == 0 {
		return nil
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return emit(Entry{Path: nestedPath(parent, "content"), Type: "file", Format: format, Comment: "compressed stream; helper not installed for recursive expansion"})
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(data)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	listErr := listCompressedStreamTo(parent, stdout, depth, format, emit)
	if listErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return listErr
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("decompress %s: %w: %s", format, err, msg)
		}
		return fmt.Errorf("decompress %s: %w", format, err)
	}
	return nil
}

func readFilePrefix(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// copyStreamToTemp creates a seekable spill file for archive formats like ZIP,
// RPM, and SquashFS whose parsers cannot operate on a forward-only stream.
func copyStreamToTemp(r io.Reader, pattern string) (string, int64, func(), error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", 0, nil, err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	size, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		cleanup()
		return "", 0, nil, copyErr
	}
	if closeErr != nil {
		cleanup()
		return "", 0, nil, closeErr
	}
	return name, size, cleanup, nil
}

func copyNToTemp(r io.Reader, size int64, pattern string) (string, func(), error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	_, copyErr := io.CopyN(tmp, r, size)
	closeErr := tmp.Close()
	if copyErr != nil {
		cleanup()
		return "", nil, copyErr
	}
	if closeErr != nil {
		cleanup()
		return "", nil, closeErr
	}
	return name, cleanup, nil
}

func prefixArchiveEntries(parent string, entries []Entry, format string) []Entry {
	for i := range entries {
		entries[i].Path = nestedPath(parent, entries[i].Path)
		entries[i].Comment = archiveComment(parent, format)
	}
	sortEntries(entries)
	return entries
}

func nestedPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "!" + child
}

func archiveComment(parent, format string) string {
	if parent == "" {
		return format + " entry"
	}
	return "inside compressed file " + parent
}

func compressedComment(parent, format string) string {
	if parent == "" {
		return "decompressed " + format + " single-file stream"
	}
	return "decompressed single-file stream from " + parent
}

func hasArchiveMagic(data []byte) bool {
	head := data
	if len(head) > 64*1024 {
		head = head[:64*1024]
	}
	return isZip(head) || isGzip(head) || isBzip2(head) || isXZ(head) || isZstd(head) || isSquashFS(head) || isRPM(head) || isTar(head) || isCPIONewc(head)
}
