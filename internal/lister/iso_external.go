package lister

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"
)

func listISOWithBSDTar(ctx context.Context, isoPath string, existing []Entry, depth int) ([]Entry, error) {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "bsdtar", "-tf", isoPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(existing))
	expandedParents := make(map[string]struct{})
	for _, entry := range existing {
		seen[entryKey(entry.Path)] = struct{}{}
		if parent, ok := strings.CutSuffix(entry.Path, "!content"); ok {
			expandedParents[entryKey(parent)] = struct{}{}
		}
		if idx := strings.IndexByte(entry.Path, '!'); idx > 0 {
			expandedParents[entryKey(entry.Path[:idx])] = struct{}{}
		}
	}

	var entries []Entry
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		name := normalizeExternalISOPath(scanner.Text())
		if name == "" {
			continue
		}
		key := entryKey(name)
		if _, ok := seen[key]; !ok {
			entries = append(entries, Entry{Path: name, Type: "file", Format: "iso/libarchive", Comment: "ISO entry discovered by bsdtar/libarchive"})
			seen[key] = struct{}{}
		}
		if _, ok := expandedParents[key]; ok {
			continue
		}
		if !hasArchiveSuffix(name) {
			continue
		}
		payload, err := extractISOEntryWithBSDTar(ctx, isoPath, name)
		if err != nil || !hasArchiveMagic(payload) {
			continue
		}
		nested, err := listNestedArchiveBytes(name, payload, depth)
		if err != nil {
			continue
		}
		for _, nestedEntry := range nested {
			nestedKey := entryKey(nestedEntry.Path)
			if _, ok := seen[nestedKey]; ok {
				continue
			}
			entries = append(entries, nestedEntry)
			seen[nestedKey] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func extractISOEntryWithBSDTar(ctx context.Context, isoPath, name string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bsdtar", "-xOf", isoPath, name)
	return cmd.Output()
}

func normalizeExternalISOPath(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/")
	return name
}

func entryKey(path string) string {
	return strings.ToLower(strings.Trim(path, "/"))
}
