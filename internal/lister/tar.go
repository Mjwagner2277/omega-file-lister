package lister

import (
	"archive/tar"
	"io"
	"strings"
)

func listTar(r io.Reader, format string) ([]Entry, error) {
	tr := tar.NewReader(r)
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
		entries = append(entries, Entry{Path: name, Type: typ, Size: h.Size, Format: format})
	}
	sortEntries(entries)
	return entries, nil
}
