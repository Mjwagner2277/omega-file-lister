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
)

func ListMountedISO(ctx context.Context, path string, opts Options) ([]Entry, error) {
	mountPoint, err := os.MkdirTemp("", "lfl-iso-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(mountPoint)

	if out, err := exec.CommandContext(ctx, "mount", "-o", "loop,ro", path, mountPoint).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mount ISO read-only: %w: %s", err, bytes.TrimSpace(out))
	}
	defer exec.CommandContext(context.Background(), "umount", mountPoint).Run()

	var entries []Entry
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
		if typ != "file" || !hasArchiveSuffix(rel) {
			return nil
		}
		head, err := readFilePrefix(full, 64*1024)
		if err != nil || !hasArchiveMagic(head) {
			return nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		nested, err := listNestedArchiveBytes(rel, data, nestedDepth(opts))
		if err != nil {
			return err
		}
		entries = append(entries, nested...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
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
