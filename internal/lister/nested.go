package lister

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
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
	return listArchivePayload("", data, depth)
}

func listNestedArchiveBytes(parent string, data []byte, depth int) ([]Entry, error) {
	return listArchivePayload(parent, data, depth)
}

// listArchivePayload is the central recursive dispatcher for supported payloads.
func listArchivePayload(parent string, data []byte, depth int) ([]Entry, error) {
	if depth <= 0 || len(data) == 0 {
		return nil, nil
	}
	head := data
	if len(head) > 64*1024 {
		head = head[:64*1024]
	}

	switch {
	case isZip(head):
		return listZipPayload(parent, data, depth)
	case isGzip(head):
		return listCompressedPayload(parent, data, depth, "gzip", func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		})
	case isBzip2(head):
		return listCompressedPayload(parent, data, depth, "bzip2", func(r io.Reader) (io.Reader, error) {
			return bzip2.NewReader(r), nil
		})
	case isXZ(head):
		return listExternalCompressedPayload(parent, data, depth, "xz", "xz", "-dc")
	case isZstd(head):
		return listExternalCompressedPayload(parent, data, depth, "zstd", "zstd", "-dc")
	case isSquashFS(head):
		return listSquashFSPayload(parent, data)
	case isRPM(head):
		return listRPMPayload(parent, data, depth)
	case isTar(head):
		return listTarPayload(parent, tar.NewReader(bytes.NewReader(data)), depth, "tar")
	case isCPIONewc(head):
		return listCPIOPayload(parent, bytes.NewReader(data), depth, "cpio")
	default:
		return nil, nil
	}
}

func listZipPayload(parent string, data []byte, depth int) ([]Entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(zr.File))
	for _, f := range zr.File {
		typ := "file"
		if f.FileInfo().IsDir() {
			typ = "dir"
		}
		childPath := nestedPath(parent, f.Name)
		entries = append(entries, Entry{Path: childPath, Type: typ, Size: int64(f.UncompressedSize64), Format: "zip", Comment: archiveComment(parent, "zip")})
		if typ != "file" || !hasArchiveSuffix(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		payload, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		nested, err := listArchivePayload(childPath, payload, depth-1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}
	sortEntries(entries)
	return entries, nil
}

func listTarPayload(parent string, tr *tar.Reader, depth int, format string) ([]Entry, error) {
	var entries []Entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
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
		entries = append(entries, Entry{Path: childPath, Type: typ, Size: h.Size, Format: format, Comment: archiveComment(parent, format)})
		if typ != "file" || !hasArchiveSuffix(name) {
			continue
		}
		payload, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		nested, err := listArchivePayload(childPath, payload, depth-1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}
	sortEntries(entries)
	return entries, nil
}

func listCompressedPayload(parent string, data []byte, depth int, format string, open func(io.Reader) (io.Reader, error)) ([]Entry, error) {
	cr, err := open(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(cr)
	peek, _ := br.Peek(512)

	if isTar(peek) {
		return listTarPayload(parent, tar.NewReader(br), depth, format+".tar")
	}
	if isCPIONewc(peek) {
		return listCPIOPayload(parent, br, depth, format+".cpio")
	}

	childPath := nestedPath(parent, "content")
	if hasArchiveMagic(peek) {
		payload, err := io.ReadAll(br)
		if err != nil {
			return nil, err
		}
		entries := []Entry{{Path: childPath, Type: "file", Size: int64(len(payload)), Format: format, Comment: compressedComment(parent, format)}}
		nested, err := listArchivePayload(childPath, payload, depth-1)
		if err != nil {
			return nil, err
		}
		return append(entries, nested...), nil
	}

	size, err := io.Copy(io.Discard, br)
	if err != nil {
		return nil, err
	}
	return []Entry{{Path: childPath, Type: "file", Size: size, Format: format, Comment: compressedComment(parent, format)}}, nil
}

func listCPIOPayload(parent string, r io.Reader, depth int, format string) ([]Entry, error) {
	var entries []Entry
	for {
		h := make([]byte, 110)
		if _, err := io.ReadFull(r, h); err != nil {
			return nil, err
		}
		if string(h[:6]) != "070701" && string(h[:6]) != "070702" {
			return nil, io.ErrUnexpectedEOF
		}
		mode, err := parseHexField(h[14:22])
		if err != nil {
			return nil, err
		}
		size, err := parseHexField(h[54:62])
		if err != nil {
			return nil, err
		}
		nameSize, err := parseHexField(h[94:102])
		if err != nil {
			return nil, err
		}
		nameBytes := make([]byte, nameSize)
		if _, err := io.ReadFull(r, nameBytes); err != nil {
			return nil, err
		}
		if err := skipPadding(r, pad4(110+int64(nameSize))); err != nil {
			return nil, err
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
			entries = append(entries, Entry{Path: childPath, Type: typ, Size: int64(size), Format: format, Comment: archiveComment(parent, format)})
		}
		if typ == "file" && hasArchiveSuffix(name) && depth > 0 {
			payload := make([]byte, size)
			if _, err := io.ReadFull(r, payload); err != nil {
				return nil, err
			}
			nested, err := listArchivePayload(childPath, payload, depth-1)
			if err != nil {
				return nil, err
			}
			entries = append(entries, nested...)
		} else if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return nil, err
		}
		if err := skipPadding(r, pad4(int64(size))); err != nil {
			return nil, err
		}
	}
	sortEntries(entries)
	return entries, nil
}

func listSquashFSPayload(parent string, data []byte) ([]Entry, error) {
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		return []Entry{{Path: nestedPath(parent, "content"), Type: "file", Format: "squashfs", Comment: "SquashFS image; install unsquashfs for recursive expansion"}}, nil
	}
	tmp, err := os.CreateTemp("", "lfl-squashfs-*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	cmd := exec.Command("unsquashfs", "-ll", name)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(string(out), "\n") {
		idx := strings.Index(line, "squashfs-root")
		if idx < 0 {
			continue
		}
		name := strings.TrimPrefix(line[idx:], "squashfs-root")
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			continue
		}
		typ := "file"
		if strings.HasPrefix(line, "d") {
			typ = "dir"
		} else if strings.HasPrefix(line, "l") {
			typ = "link"
		}
		entries = append(entries, Entry{Path: nestedPath(parent, name), Type: typ, Format: "squashfs", Comment: archiveComment(parent, "squashfs")})
	}
	sortEntries(entries)
	return entries, nil
}

func listExternalCompressedPayload(parent string, data []byte, depth int, format string, argv ...string) ([]Entry, error) {
	if len(argv) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return []Entry{{Path: nestedPath(parent, "content"), Type: "file", Format: format, Comment: "compressed stream; helper not installed for recursive expansion"}}, nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(data)
	payload, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	childPath := nestedPath(parent, "content")
	if hasArchiveMagic(payload) {
		return listArchivePayload(parent, payload, depth)
	}
	return []Entry{{Path: childPath, Type: "file", Size: int64(len(payload)), Format: format, Comment: compressedComment(parent, format)}}, nil
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
