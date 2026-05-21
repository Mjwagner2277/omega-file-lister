package lister

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const isoBlockSize = 2048

type isoDirTask struct {
	extent uint32
	size   uint32
	prefix string
}

type isoFileTask struct {
	extent uint32
	size   uint32
	path   string
}

func ListISO(ctx context.Context, path string, r io.ReaderAt, imageSize int64, opts Options) ([]Entry, error) {
	pvd := make([]byte, isoBlockSize)
	if _, err := r.ReadAt(pvd, 16*isoBlockSize); err != nil {
		return nil, err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, fmt.Errorf("not a primary ISO-9660 volume descriptor")
	}
	root, err := parseISORecord(pvd[156:])
	if err != nil {
		return nil, err
	}

	var entries []Entry
	var nestedFiles []isoFileTask
	stack := []isoDirTask{{extent: root.extent, size: root.size, prefix: ""}}
	for len(stack) > 0 {
		last := len(stack) - 1
		task := stack[last]
		stack = stack[:last]
		next, candidates, err := scanISODir(r, imageSize, task, &entries)
		if err != nil {
			return nil, err
		}
		stack = append(stack, next...)
		nestedFiles = append(nestedFiles, candidates...)
	}

	for _, candidate := range nestedFiles {
		head, err := readISOFilePrefix(r, imageSize, candidate, 64*1024)
		if err != nil {
			return nil, err
		}
		if !hasArchiveMagic(head) {
			continue
		}
		data, err := readISOFile(r, imageSize, candidate)
		if err != nil {
			return nil, err
		}
		nested, err := listNestedArchiveBytes(candidate.path, data, nestedDepth(opts))
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}

	if path != "" {
		external, err := listISOWithBSDTar(ctx, path, entries, nestedDepth(opts))
		if err == nil {
			entries = append(entries, external...)
		}
	}

	sortEntries(entries)
	return entries, nil
}

func scanISODir(r io.ReaderAt, imageSize int64, task isoDirTask, entries *[]Entry) ([]isoDirTask, []isoFileTask, error) {
	offset := int64(task.extent) * isoBlockSize
	size := int64(task.size)
	if offset < 0 || size < 0 || offset+size > imageSize {
		return nil, nil, fmt.Errorf("invalid ISO directory extent %d size %d", task.extent, task.size)
	}
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, nil, err
	}

	var dirs []isoDirTask
	var candidates []isoFileTask
	for pos := 0; pos < len(buf); {
		length := int(buf[pos])
		if length == 0 {
			pos = ((pos / isoBlockSize) + 1) * isoBlockSize
			continue
		}
		if pos+length > len(buf) {
			return nil, nil, fmt.Errorf("short ISO directory record")
		}
		rec, err := parseISORecord(buf[pos : pos+length])
		if err != nil {
			return nil, nil, err
		}
		pos += length
		if rec.name == "." || rec.name == ".." || rec.name == "" {
			continue
		}
		full := rec.name
		if task.prefix != "" {
			full = task.prefix + "/" + rec.name
		}
		if rec.isDir {
			dirs = append(dirs, isoDirTask{extent: rec.extent, size: rec.size, prefix: full})
			*entries = append(*entries, Entry{Path: full, Type: "dir", Size: int64(rec.size), Format: "iso9660", Comment: "ISO-9660 directory record"})
			continue
		}
		*entries = append(*entries, Entry{Path: full, Type: "file", Size: int64(rec.size), Format: "iso9660", Comment: "ISO-9660 file extent"})
		if isNestedCandidate(full, rec.size) {
			candidates = append(candidates, isoFileTask{extent: rec.extent, size: rec.size, path: full})
		}
	}
	return dirs, candidates, nil
}

func readISOFile(r io.ReaderAt, imageSize int64, file isoFileTask) ([]byte, error) {
	return readISOFilePrefix(r, imageSize, file, int64(file.size))
}

func readISOFilePrefix(r io.ReaderAt, imageSize int64, file isoFileTask, limit int64) ([]byte, error) {
	offset := int64(file.extent) * isoBlockSize
	size := int64(file.size)
	if offset < 0 || size < 0 || offset+size > imageSize {
		return nil, fmt.Errorf("invalid ISO file extent %d size %d for %s", file.extent, file.size, file.path)
	}
	if limit > 0 && size > limit {
		size = limit
	}
	data := make([]byte, size)
	if _, err := r.ReadAt(data, offset); err != nil && err != io.EOF {
		return nil, err
	}
	return data, nil
}

type isoRecord struct {
	extent uint32
	size   uint32
	name   string
	isDir  bool
}

func parseISORecord(b []byte) (isoRecord, error) {
	if len(b) < 34 {
		return isoRecord{}, fmt.Errorf("short ISO directory record")
	}
	length := int(b[0])
	if length == 0 {
		return isoRecord{}, fmt.Errorf("empty ISO directory record")
	}
	if length > len(b) {
		b = b[:length]
	}
	nameLen := int(b[32])
	if 33+nameLen > len(b) {
		return isoRecord{}, fmt.Errorf("invalid ISO file identifier length")
	}
	nameBytes := b[33 : 33+nameLen]
	name := decodeISOName(nameBytes)
	if rr := rockRidgeName(b[33+nameLen:]); rr != "" {
		name = rr
	}
	return isoRecord{
		extent: binary.LittleEndian.Uint32(b[2:6]),
		size:   binary.LittleEndian.Uint32(b[10:14]),
		name:   name,
		isDir:  b[25]&0x02 != 0,
	}, nil
}

func decodeISOName(b []byte) string {
	if len(b) == 1 && b[0] == 0 {
		return "."
	}
	if len(b) == 1 && b[0] == 1 {
		return ".."
	}
	name := strings.TrimRight(string(b), " ")
	if semi := strings.LastIndexByte(name, ';'); semi >= 0 {
		name = name[:semi]
	}
	return strings.TrimRight(name, ".")
}

func rockRidgeName(systemUse []byte) string {
	systemUse = bytes.TrimRight(systemUse, "\x00")
	for len(systemUse) >= 4 {
		sig := string(systemUse[:2])
		length := int(systemUse[2])
		if length < 4 || length > len(systemUse) {
			break
		}
		if sig == "NM" && length > 5 {
			flags := systemUse[4]
			if flags&0x06 == 0 {
				return string(systemUse[5:length])
			}
		}
		systemUse = systemUse[length:]
	}
	return ""
}
