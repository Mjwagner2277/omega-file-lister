package lister

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Options struct {
	ISOWorkers     int
	MaxNestedDepth int
}

type Entry struct {
	Path    string `json:"path"`
	Type    string `json:"type,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Format  string `json:"format"`
	Comment string `json:"comment,omitempty"`
}

func List(ctx context.Context, path string, opts Options) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	if looksISO(file, head) {
		st, err := file.Stat()
		if err != nil {
			return nil, err
		}
		return ListISO(file, st.Size(), opts)
	}
	if isRPM(head) {
		return listRPM(ctx, path, opts)
	}
	if isZip(head) || isGzip(head) || isBzip2(head) || isXZ(head) || isZstd(head) || isSquashFS(head) || isTar(head) || isCPIONewc(head) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		entries, err := listArchiveBytes(data, nestedDepth(opts))
		if err != nil {
			return nil, err
		}
		sortEntries(entries)
		return entries, nil
	}

	return listWithFallback(ctx, path)
}

func listCompressedTarOrSingle(r io.Reader, format string, open func(io.Reader) (io.Reader, error)) ([]Entry, error) {
	cr, err := open(r)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(cr)
	peek, _ := br.Peek(512)
	if isTar(peek) {
		return listTar(br, format+".tar")
	}
	if isCPIONewc(peek) {
		return ListCPIONewc(br, format+".cpio")
	}
	return []Entry{{Path: "content", Type: "file", Format: format}}, nil
}

func listWithFallback(ctx context.Context, path string) ([]Entry, error) {
	candidates := [][]string{
		{"bsdtar", "-tf", path},
		{"tar", "-tf", path},
		{"7z", "l", "-ba", "-slt", path},
		{"unrar", "lb", path},
	}
	var errs []error
	for _, cmd := range candidates {
		entries, err := runListCommand(ctx, cmd)
		if err == nil {
			return entries, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("unsupported format or missing helper tools: %w", errors.Join(errs...))
}

func runListCommand(ctx context.Context, argv []string) ([]Entry, error) {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if argv[0] == "7z" {
		for _, block := range strings.Split(string(out), "\n\n") {
			for _, line := range strings.Split(block, "\n") {
				if strings.HasPrefix(line, "Path = ") {
					p := strings.TrimPrefix(line, "Path = ")
					if p != "" && p != "." {
						entries = append(entries, Entry{Path: p, Type: "file", Format: "external/7z"})
					}
				}
			}
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			p := strings.TrimSpace(scanner.Text())
			if p != "" {
				entries = append(entries, Entry{Path: p, Type: "file", Format: "external/" + argv[0]})
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	sortEntries(entries)
	return entries, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
}

func looksISO(r io.ReaderAt, head []byte) bool {
	if len(head) > 0x8001+5 && string(head[0x8001:0x8006]) == "CD001" {
		return true
	}
	buf := make([]byte, 6)
	_, err := r.ReadAt(buf, 0x8001)
	return err == nil && string(buf[:5]) == "CD001"
}

func isZip(b []byte) bool {
	return len(b) >= 4 && string(b[:4]) == "PK\x03\x04"
}

func isGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

func isBzip2(b []byte) bool {
	return len(b) >= 3 && string(b[:3]) == "BZh"
}

func isXZ(b []byte) bool {
	return len(b) >= 6 && bytes.Equal(b[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00})
}

func isZstd(b []byte) bool {
	return len(b) >= 4 && b[0] == 0x28 && b[1] == 0xb5 && b[2] == 0x2f && b[3] == 0xfd
}

func isSquashFS(b []byte) bool {
	return len(b) >= 4 && string(b[:4]) == "hsqs"
}

func isTar(b []byte) bool {
	return len(b) >= 265 && string(b[257:262]) == "ustar"
}

func isCPIONewc(b []byte) bool {
	return len(b) >= 6 && string(b[:6]) == "070701"
}

func isRPM(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xed && b[1] == 0xab && b[2] == 0xee && b[3] == 0xdb
}

func stripKnownArchiveSuffix(name string) string {
	base := filepath.Base(name)
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".txz", ".tzst", ".gz", ".bz2", ".xz", ".zst"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}
