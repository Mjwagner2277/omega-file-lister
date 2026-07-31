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
	MaxNestedDepth int
	Workers        int

	// MountISO forces ISO inputs through a Linux read-only loop mount.
	MountISO bool

	// MountRoot is the parent directory for temporary ISO mount points.
	MountRoot string

	// SudoMount runs ISO mount/umount through sudo for non-root users.
	SudoMount bool

	Progress func(ProgressEvent)
}

type ProgressEvent struct {
	Stage   string
	Path    string
	Count   int
	Total   int
	Workers int
	Message string
}

type Entry struct {
	Path    string `json:"path"`
	Type    string `json:"type,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Format  string `json:"format"`
	Comment string `json:"comment,omitempty"`
}

type EntryFunc func(Entry) error

func List(ctx context.Context, path string, opts Options) ([]Entry, error) {
	var entries []Entry
	err := ListTo(ctx, path, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func ListTo(ctx context.Context, path string, opts Options, emit EntryFunc) error {
	if emit == nil {
		return errors.New("nil entry emit function")
	}
	reportProgress(opts, ProgressEvent{Stage: "open", Path: path, Message: "opening input"})
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	if looksISO(file, head) {
		if opts.MountISO {
			reportProgress(opts, ProgressEvent{Stage: "detect", Path: path, Message: "detected ISO image; using Linux mount path"})
			return ListMountedISOTo(ctx, path, opts, emit)
		}
		reportProgress(opts, ProgressEvent{Stage: "detect", Path: path, Message: "detected ISO image; using non-sudo archive reader"})
		return ListISOArchive(ctx, path, opts, emit)
	}
	if isRPM(head) {
		reportProgress(opts, ProgressEvent{Stage: "detect", Path: path, Message: "detected RPM package"})
		if _, err := exec.LookPath("rpm2cpio"); err == nil {
			count := 0
			err := listRPMViaRPM2CPIOTo(ctx, path, opts, func(entry Entry) error {
				count++
				return emit(entry)
			})
			if err != nil {
				return err
			}
			reportProgress(opts, ProgressEvent{Stage: "done", Path: path, Count: count, Message: "listed entries"})
			return nil
		}
		entries, err := listWithFallback(ctx, path)
		if err != nil {
			return err
		}
		return emitEntries(entries, emit)
	}
	if isZip(head) || isGzip(head) || isBzip2(head) || isXZ(head) || isZstd(head) || isSquashFS(head) || isTar(head) || isCPIONewc(head) {
		reportProgress(opts, ProgressEvent{Stage: "expand", Path: path, Message: "streaming and expanding archive entries"})
		count := 0
		err = listArchiveFileTo("", path, nestedDepth(opts), func(entry Entry) error {
			count++
			return emit(entry)
		})
		if err != nil {
			return err
		}
		reportProgress(opts, ProgressEvent{Stage: "done", Path: path, Count: count, Message: "listed entries"})
		return nil
	}

	reportProgress(opts, ProgressEvent{Stage: "fallback", Path: path, Message: "trying external listing helpers"})
	return listWithFallbackTo(ctx, path, emit)
}

func emitEntries(entries []Entry, emit EntryFunc) error {
	for _, entry := range entries {
		if err := emit(entry); err != nil {
			return err
		}
	}
	return nil
}

func reportProgress(opts Options, event ProgressEvent) {
	if opts.Progress != nil {
		opts.Progress(event)
	}
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
	var entries []Entry
	err := listWithFallbackTo(ctx, path, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listWithFallbackTo(ctx context.Context, path string, emit EntryFunc) error {
	candidates := [][]string{
		{"bsdtar", "-tf", path},
		{"tar", "-tf", path},
		{"7z", "l", "-ba", "-slt", path},
		{"unrar", "lb", path},
	}
	var errs []error
	for _, argv := range candidates {
		if err := runListCommandTo(ctx, argv, emit); err == nil {
			return nil
		} else {
			errs = append(errs, err)
		}
	}
	return fmt.Errorf("unsupported format or missing helper tools: %w", errors.Join(errs...))
}

func runListCommand(ctx context.Context, argv []string) ([]Entry, error) {
	var entries []Entry
	err := runListCommandTo(ctx, argv, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func runListCommandTo(ctx context.Context, argv []string, emit EntryFunc) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
	format := "external/" + argv[0]
	for scanner.Scan() {
		line := scanner.Text()
		if argv[0] == "7z" {
			if !strings.HasPrefix(line, "Path = ") {
				continue
			}
			line = strings.TrimPrefix(line, "Path = ")
		}
		p := strings.TrimSpace(line)
		if p == "" || p == "." {
			continue
		}
		if err := emit(Entry{Path: p, Type: "file", Format: format}); err != nil {
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
			return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, msg)
		}
		return err
	}
	return nil
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
