package lister

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"io"
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
	if size == 0 {
		return false
	}
	return hasArchiveSuffix(name)
}

func hasArchiveSuffix(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{
		".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".zip", ".jar", ".war", ".cpio", ".cpio.gz", ".gz", ".bz2",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func listNestedArchiveBytes(parent string, data []byte, depth int) ([]Entry, error) {
	if depth <= 0 || len(data) == 0 {
		return nil, nil
	}
	head := data
	if len(head) > 64*1024 {
		head = head[:64*1024]
	}

	switch {
	case isZip(head):
		return listNestedZip(parent, data, depth)
	case isGzip(head):
		return listNestedCompressed(parent, data, depth, "gzip", func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		})
	case isBzip2(head):
		return listNestedCompressed(parent, data, depth, "bzip2", func(r io.Reader) (io.Reader, error) {
			return bzip2.NewReader(r), nil
		})
	case isTar(head):
		return listNestedTar(parent, data, depth, "tar")
	case isCPIONewc(head):
		entries, err := ListCPIONewc(bytes.NewReader(data), "cpio")
		if err != nil {
			return nil, err
		}
		return prefixNestedEntries(parent, entries, "expanded cpio archive"), nil
	default:
		return nil, nil
	}
}

func listNestedZip(parent string, data []byte, depth int) ([]Entry, error) {
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
		child := Entry{Path: parent + "!" + f.Name, Type: typ, Size: int64(f.UncompressedSize64), Format: "zip", Comment: "inside compressed file " + parent}
		entries = append(entries, child)
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
		nested, err := listNestedArchiveBytes(child.Path, payload, depth-1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}
	sortEntries(entries)
	return entries, nil
}

func listNestedTar(parent string, data []byte, depth int, format string) ([]Entry, error) {
	return listNestedTarReader(parent, tar.NewReader(bytes.NewReader(data)), depth, format)
}

func listNestedTarReader(parent string, tr *tar.Reader, depth int, format string) ([]Entry, error) {
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
		child := Entry{Path: parent + "!" + name, Type: typ, Size: h.Size, Format: format, Comment: "inside compressed file " + parent}
		entries = append(entries, child)
		if typ != "file" || !hasArchiveSuffix(name) {
			continue
		}
		payload, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		nested, err := listNestedArchiveBytes(child.Path, payload, depth-1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}
	sortEntries(entries)
	return entries, nil
}

func listNestedCompressed(parent string, data []byte, depth int, format string, open func(io.Reader) (io.Reader, error)) ([]Entry, error) {
	cr, err := open(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(cr)
	peek, _ := br.Peek(512)

	if isTar(peek) {
		return listNestedTarReader(parent, tar.NewReader(br), depth, format+".tar")
	}
	if isCPIONewc(peek) {
		entries, err := ListCPIONewc(br, format+".cpio")
		if err != nil {
			return nil, err
		}
		return prefixNestedEntries(parent, entries, "expanded "+format+" cpio archive"), nil
	}

	child := parent + "!content"
	if hasArchiveMagic(peek) {
		payload, err := io.ReadAll(br)
		if err != nil {
			return nil, err
		}
		entries := []Entry{{Path: child, Type: "file", Size: int64(len(payload)), Format: format, Comment: "decompressed single-file stream from " + parent}}
		nested, err := listNestedArchiveBytes(child, payload, depth-1)
		if err != nil {
			return nil, err
		}
		return append(entries, nested...), nil
	}

	size, err := io.Copy(io.Discard, br)
	if err != nil {
		return nil, err
	}
	return []Entry{{Path: child, Type: "file", Size: size, Format: format, Comment: "decompressed single-file stream from " + parent}}, nil
}

func prefixNestedEntries(parent string, entries []Entry, comment string) []Entry {
	for i := range entries {
		entries[i].Path = parent + "!" + entries[i].Path
		entries[i].Comment = comment + " from " + parent
	}
	sortEntries(entries)
	return entries
}

func hasArchiveMagic(data []byte) bool {
	head := data
	if len(head) > 64*1024 {
		head = head[:64*1024]
	}
	return isZip(head) || isGzip(head) || isBzip2(head) || isTar(head) || isCPIONewc(head)
}
