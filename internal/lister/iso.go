package lister

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const isoBlockSize = 2048

type isoDirTask struct {
	extent uint32
	size   uint32
	prefix string
}

func ListISO(r io.ReaderAt, imageSize int64, opts Options) ([]Entry, error) {
	workers := opts.ISOWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > 64 {
		workers = 64
	}

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

	tasks := make(chan isoDirTask, workers*4)
	results := make(chan Entry, workers*64)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	var pending int64 = 1
	var closeOnce sync.Once

	finishTask := func() {
		if atomic.AddInt64(&pending, -1) == 0 {
			closeOnce.Do(func() { close(tasks) })
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				if err := scanISODir(r, imageSize, task, tasks, results, &pending); err != nil {
					select {
					case errs <- err:
					default:
					}
				}
				finishTask()
			}
		}()
	}

	tasks <- isoDirTask{extent: root.extent, size: root.size, prefix: ""}

	go func() {
		wg.Wait()
		close(results)
	}()

	var entries []Entry
	for entry := range results {
		entries = append(entries, entry)
	}
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	sortEntries(entries)
	return entries, nil
}

func scanISODir(r io.ReaderAt, imageSize int64, task isoDirTask, tasks chan<- isoDirTask, results chan<- Entry, pending *int64) error {
	offset := int64(task.extent) * isoBlockSize
	size := int64(task.size)
	if offset < 0 || size < 0 || offset+size > imageSize {
		return fmt.Errorf("invalid ISO directory extent %d size %d", task.extent, task.size)
	}
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, offset); err != nil && err != io.EOF {
		return err
	}

	for pos := 0; pos < len(buf); {
		length := int(buf[pos])
		if length == 0 {
			pos = ((pos / isoBlockSize) + 1) * isoBlockSize
			continue
		}
		if pos+length > len(buf) {
			return fmt.Errorf("short ISO directory record")
		}
		rec, err := parseISORecord(buf[pos : pos+length])
		if err != nil {
			return err
		}
		pos += length
		if rec.name == "." || rec.name == ".." || rec.name == "" {
			continue
		}
		full := path.Join(task.prefix, rec.name)
		typ := "file"
		if rec.isDir {
			typ = "dir"
			atomic.AddInt64(pending, 1)
			go func(next isoDirTask) { tasks <- next }(isoDirTask{extent: rec.extent, size: rec.size, prefix: full})
		}
		results <- Entry{Path: full, Type: typ, Size: int64(rec.size), Format: "iso9660"}
	}
	return nil
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
