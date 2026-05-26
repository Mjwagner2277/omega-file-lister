//go:build linux

package lister

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

type mountedCandidate struct {
	full string
	rel  string
}

func ListMountedISO(ctx context.Context, path string, opts Options) ([]Entry, error) {
	reportProgress(opts, ProgressEvent{Stage: "mount", Path: path, Message: "creating temporary mount point"})
	mountPoint, err := os.MkdirTemp("", "lfl-iso-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(mountPoint)

	reportProgress(opts, ProgressEvent{Stage: "mount", Path: path, Message: "mounting ISO read-only"})
	if out, err := exec.CommandContext(ctx, "mount", "-o", "loop,ro", path, mountPoint).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mount ISO read-only: %w: %s", err, bytes.TrimSpace(out))
	}
	defer func() {
		reportProgress(opts, ProgressEvent{Stage: "unmount", Path: path, Message: "unmounting ISO"})
		exec.CommandContext(context.Background(), "umount", mountPoint).Run()
	}()

	var entries []Entry
	var candidates []mountedCandidate
	reportProgress(opts, ProgressEvent{Stage: "walk", Path: path, Message: "walking mounted filesystem"})
	err = filepath.WalkDir(mountPoint, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == mountPoint {
			return nil
		}
		rel, err := filepath.Rel(mountPoint, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		typ := "file"
		if d.IsDir() {
			typ = "dir"
		} else if info.Mode()&os.ModeSymlink != 0 {
			typ = "link"
		}
		entries = append(entries, Entry{Path: rel, Type: typ, Size: info.Size(), Format: "iso/mount", Comment: "mounted ISO filesystem entry"})
		if typ == "file" && hasArchiveSuffix(rel) {
			candidates = append(candidates, mountedCandidate{full: full, rel: rel})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	reportProgress(opts, ProgressEvent{Stage: "walk", Path: path, Count: len(entries), Total: len(candidates), Message: "mounted filesystem walk complete"})
	nested, err := expandMountedCandidates(ctx, candidates, opts)
	if err != nil {
		return nil, err
	}
	entries = append(entries, nested...)
	sortEntries(entries)
	reportProgress(opts, ProgressEvent{Stage: "done", Path: path, Count: len(entries), Message: "listed entries"})
	return entries, nil
}

func expandMountedCandidates(ctx context.Context, candidates []mountedCandidate, opts Options) ([]Entry, error) {
	if len(candidates) == 0 {
		return nil, nil
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
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: len(candidates), Workers: workers, Message: "expanding mounted archive candidates"})

	jobs := make(chan mountedCandidate)
	results := make(chan []Entry, workers)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range jobs {
				select {
				case <-ctx.Done():
					select {
					case errs <- ctx.Err():
					default:
					}
					return
				default:
				}
				entries, err := expandMountedCandidate(candidate, opts)
				if err != nil {
					select {
					case errs <- err:
					default:
					}
					continue
				}
				if len(entries) > 0 {
					results <- entries
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

	var entries []Entry
	for batch := range results {
		entries = append(entries, batch...)
	}
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: len(entries), Total: len(candidates), Workers: workers, Message: "mounted archive expansion complete"})
	return entries, nil
}

func expandMountedCandidate(candidate mountedCandidate, opts Options) ([]Entry, error) {
	head, err := readFilePrefix(candidate.full, 64*1024)
	if err != nil || !hasArchiveMagic(head) {
		return nil, nil
	}
	data, err := os.ReadFile(candidate.full)
	if err != nil {
		return nil, err
	}
	return listNestedArchiveBytes(candidate.rel, data, nestedDepth(opts))
}

func readFilePrefix(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}
