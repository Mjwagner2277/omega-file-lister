package lister

import (
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ListCPIONewc(r io.Reader, format string) ([]Entry, error) {
	var entries []Entry
	for {
		h := make([]byte, 110)
		if _, err := io.ReadFull(r, h); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, err
			}
			return nil, err
		}
		if string(h[:6]) != "070701" && string(h[:6]) != "070702" {
			return nil, fmt.Errorf("unsupported cpio magic %q", string(h[:6]))
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
		skipPadding(r, pad4(110+int64(nameSize)))
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
		if name != "" {
			entries = append(entries, Entry{Path: name, Type: typ, Size: int64(size), Format: format})
		}
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return nil, err
		}
		skipPadding(r, pad4(int64(size)))
	}
	sortEntries(entries)
	return entries, nil
}

func parseHexField(b []byte) (uint64, error) {
	dst := make([]byte, hex.DecodedLen(len(b)))
	if _, err := hex.Decode(dst, b); err == nil {
		return strconv.ParseUint(string(b), 16, 64)
	}
	return strconv.ParseUint(string(b), 16, 64)
}

func pad4(n int64) int64 {
	if rem := n % 4; rem != 0 {
		return 4 - rem
	}
	return 0
}

func skipPadding(r io.Reader, n int64) error {
	if n == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, n)
	return err
}
