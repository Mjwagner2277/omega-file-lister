//go:build linux

package lister

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type mountedCandidate struct {
	full string
	rel  string
}

func ListMountedISO(ctx context.Context, path string, opts Options) ([]Entry, error) {
	var entries []Entry
	err := ListMountedISOTo(ctx, path, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func ListMountedISOTo(ctx context.Context, path string, opts Options, emit EntryFunc) error {
	reportProgress(opts, ProgressEvent{Stage: "mount", Path: path, Message: "creating temporary mount point"})
	if opts.MountRoot != "" {
		if err := os.MkdirAll(opts.MountRoot, 0755); err != nil {
			return err
		}
	}
	mountPoint, err := os.MkdirTemp(opts.MountRoot, "lfl-iso-*")
	if err != nil {
		return err
	}
	defer os.Remove(mountPoint)

	mountViaSudo := shouldUseSudoMount(opts)
	mountMessage := "mounting ISO read-only"
	if mountViaSudo {
		mountMessage = "mounting ISO read-only with sudo"
	}
	reportProgress(opts, ProgressEvent{Stage: "mount", Path: path, Message: mountMessage})
	if err := runMountCommand(ctx, mountViaSudo, "mount", "-o", "loop,ro", path, mountPoint); err != nil {
		return fmt.Errorf("mount ISO read-only: %w", err)
	}
	defer func() {
		unmountMessage := "unmounting ISO"
		if mountViaSudo {
			unmountMessage = "unmounting ISO with sudo"
		}
		reportProgress(opts, ProgressEvent{Stage: "unmount", Path: path, Message: unmountMessage})
		_ = runMountCommand(context.Background(), mountViaSudo, "umount", mountPoint)
	}()

	count := 0
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
		if err := emit(Entry{Path: rel, Type: typ, Size: info.Size(), Format: "iso/mount", Comment: "mounted ISO filesystem entry"}); err != nil {
			return err
		}
		count++
		if typ == "file" && hasArchiveSuffix(rel) {
			candidates = append(candidates, mountedCandidate{full: full, rel: rel})
		}
		return nil
	})
	if err != nil {
		return err
	}

	reportProgress(opts, ProgressEvent{Stage: "walk", Path: path, Count: count, Total: len(candidates), Message: "mounted filesystem walk complete"})
	nestedCount, err := expandMountedCandidatesTo(ctx, candidates, opts, emit)
	if err != nil {
		return err
	}
	reportProgress(opts, ProgressEvent{Stage: "done", Path: path, Count: count + nestedCount, Message: "listed entries"})
	return nil
}

func shouldUseSudoMount(opts Options) bool {
	return opts.SudoMount && os.Geteuid() != 0
}

func runMountCommand(ctx context.Context, useSudo bool, command string, args ...string) error {
	argv := append([]string{command}, args...)
	if useSudo {
		argv = append([]string{"sudo", "-p", "lfl sudo password: "}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(strings.Join([]string{stdout.String(), stderr.String()}, "\n"))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func expandMountedCandidates(ctx context.Context, candidates []mountedCandidate, opts Options) ([]Entry, error) {
	var entries []Entry
	_, err := expandMountedCandidatesTo(ctx, candidates, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func expandMountedCandidatesTo(ctx context.Context, candidates []mountedCandidate, opts Options, emit EntryFunc) (int, error) {
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
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: len(candidates), Workers: workers, Message: "expanding mounted archive candidates"})

	jobs := make(chan mountedCandidate)
	results := make(chan Entry, workers)
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
				err := expandMountedCandidateTo(candidate, opts, func(entry Entry) error {
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
	reportProgress(opts, ProgressEvent{Stage: "expand", Count: count, Total: len(candidates), Workers: workers, Message: "mounted archive expansion complete"})
	return count, nil
}

func expandMountedCandidate(candidate mountedCandidate, opts Options) ([]Entry, error) {
	var entries []Entry
	err := expandMountedCandidateTo(candidate, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func expandMountedCandidateTo(candidate mountedCandidate, opts Options, emit EntryFunc) error {
	head, err := readFilePrefix(candidate.full, 64*1024)
	if err != nil || !hasArchiveMagic(head) {
		return nil
	}
	data, err := os.ReadFile(candidate.full)
	if err != nil {
		return err
	}
	return listNestedArchiveBytesTo(candidate.rel, data, nestedDepth(opts), emit)
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
