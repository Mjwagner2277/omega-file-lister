package lister

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

type isoArchiveCandidate struct {
	rel string
}

func ListISOArchive(ctx context.Context, path string, opts Options, emit EntryFunc) error {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		return fmt.Errorf("non-sudo ISO listing requires bsdtar/libarchive; install bsdtar or rerun with -mount-iso: %w", err)
	}
	reportProgress(opts, ProgressEvent{Stage: "scan", Path: path, Message: "listing ISO without mounting"})

	cmd := exec.CommandContext(ctx, "bsdtar", "-tf", path)
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
	count := 0
	var candidates []isoArchiveCandidate
	for scanner.Scan() {
		rel := cleanISOArchivePath(scanner.Text())
		if rel == "" {
			continue
		}
		typ := "file"
		if strings.HasSuffix(rel, "/") {
			typ = "dir"
			rel = strings.TrimRight(rel, "/")
		}
		if rel == "" {
			continue
		}
		if err := emit(Entry{Path: rel, Type: typ, Format: "iso/archive", Comment: "ISO archive entry"}); err != nil {
			_ = cmd.Wait()
			return err
		}
		count++
		if typ == "file" && hasArchiveSuffix(rel) {
			candidates = append(candidates, isoArchiveCandidate{rel: rel})
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("list ISO with bsdtar: %w: %s", err, msg)
		}
		return fmt.Errorf("list ISO with bsdtar: %w", err)
	}

	reportProgress(opts, ProgressEvent{Stage: "scan", Path: path, Count: count, Total: len(candidates), Message: "ISO archive listing complete"})
	nestedCount, err := expandISOArchiveCandidates(ctx, path, candidates, opts, emit)
	if err != nil {
		return err
	}
	reportProgress(opts, ProgressEvent{Stage: "done", Path: path, Count: count + nestedCount, Message: "listed entries"})
	return nil
}

func cleanISOArchivePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	if path == "." {
		return ""
	}
	return path
}

func expandISOArchiveCandidates(ctx context.Context, isoPath string, candidates []isoArchiveCandidate, opts Options, emit EntryFunc) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}
	if workers > 64 {
		workers = 64
	}
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: len(candidates), Workers: workers, Message: "expanding ISO archive candidates"})

	jobs := make(chan isoArchiveCandidate)
	results := make(chan Entry, workers)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range jobs {
				err := expandISOArchiveCandidateTo(ctx, isoPath, candidate, opts, func(entry Entry) error {
					select {
					case results <- entry:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				if err != nil {
					select {
					case errs <- err:
					default:
					}
					continue
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, candidate := range candidates {
			select {
			case <-ctx.Done():
				return
			case jobs <- candidate:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	count := 0
	var emitErr error
	for entry := range results {
		if emitErr != nil {
			continue
		}
		if err := emit(entry); err != nil {
			emitErr = err
			continue
		}
		count++
	}
	if emitErr != nil {
		return count, emitErr
	}
	select {
	case err := <-errs:
		return count, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return count, err
	}
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: count, Total: len(candidates), Workers: workers, Message: "ISO archive expansion complete"})
	return count, nil
}

func expandISOArchiveCandidate(ctx context.Context, isoPath string, candidate isoArchiveCandidate, opts Options) ([]Entry, error) {
	var entries []Entry
	err := expandISOArchiveCandidateTo(ctx, isoPath, candidate, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func expandISOArchiveCandidateTo(ctx context.Context, isoPath string, candidate isoArchiveCandidate, opts Options, emit EntryFunc) error {
	tmp, err := os.CreateTemp("", "lfl-iso-candidate-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	cmd := exec.CommandContext(ctx, "bsdtar", "-xOf", isoPath, candidate.rel)
	cmd.Stdout = tmp
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = tmp.Close()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("extract %s from ISO: %w: %s", candidate.rel, err, msg)
		}
		return fmt.Errorf("extract %s from ISO: %w", candidate.rel, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return listArchiveFileTo(candidate.rel, tmpName, nestedDepth(opts), emit)
}
