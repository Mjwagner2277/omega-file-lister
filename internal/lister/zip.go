package lister

import "archive/zip"

func listZip(path string) ([]Entry, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	entries := make([]Entry, 0, len(zr.File))
	for _, f := range zr.File {
		typ := "file"
		if f.FileInfo().IsDir() {
			typ = "dir"
		}
		entries = append(entries, Entry{Path: f.Name, Type: typ, Size: int64(f.UncompressedSize64), Format: "zip"})
	}
	sortEntries(entries)
	return entries, nil
}
