package lister

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func TestListTar(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	must(t, tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir}))
	must(t, tw.WriteHeader(&tar.Header{Name: "dir/file.txt", Size: 5}))
	_, err := tw.Write([]byte("hello"))
	must(t, err)
	must(t, tw.Close())

	entries, err := listTar(bytes.NewReader(buf.Bytes()), "tar")
	must(t, err)
	got := paths(entries)
	want := "dir/,dir/file.txt"
	if strings.Join(got, ",") != want {
		t.Fatalf("paths = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestListCPIONewc(t *testing.T) {
	archive := newcEntry("etc/config", 0100644, []byte("x")) + newcEntry("TRAILER!!!", 0, nil)
	entries, err := ListCPIONewc(strings.NewReader(archive), "cpio")
	must(t, err)
	if len(entries) != 1 || entries[0].Path != "etc/config" || entries[0].Size != 1 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestISORecordRockRidgeName(t *testing.T) {
	rec := isoRecordBytes("README.TXT;1", "readme.txt", 23, 99, false)
	parsed, err := parseISORecord(rec)
	must(t, err)
	if parsed.name != "readme.txt" || parsed.extent != 23 || parsed.size != 99 || parsed.isDir {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestListISO(t *testing.T) {
	image := make([]byte, 24*isoBlockSize)
	pvd := image[16*isoBlockSize : 17*isoBlockSize]
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	copy(pvd[156:], isoRecordBytes("\x00", "", 20, isoBlockSize, true))

	dir := image[20*isoBlockSize : 21*isoBlockSize]
	pos := 0
	pos += copy(dir[pos:], isoRecordBytes("\x00", "", 20, isoBlockSize, true))
	pos += copy(dir[pos:], isoRecordBytes("\x01", "", 20, isoBlockSize, true))
	copy(dir[pos:], isoRecordBytes("HELLO.TXT;1", "hello.txt", 21, 5, false))

	entries, err := ListISO(bytes.NewReader(image), int64(len(image)), Options{ISOWorkers: 2})
	must(t, err)
	if len(entries) != 1 || entries[0].Path != "hello.txt" || entries[0].Format != "iso9660" {
		t.Fatalf("entries = %#v", entries)
	}
}

func paths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Path
	}
	return out
}

func newcEntry(name string, mode int, data []byte) string {
	nameBytes := append([]byte(name), 0)
	header := fmt.Sprintf("070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
		0, mode, 0, 0, 1, 0, len(data), 0, 0, 0, 0, len(nameBytes), 0)
	return header + string(nameBytes) + strings.Repeat("\x00", int(pad4(int64(110+len(nameBytes))))) + string(data) + strings.Repeat("\x00", int(pad4(int64(len(data)))))
}

func isoRecordBytes(isoName, rrName string, extent, size uint32, dir bool) []byte {
	name := []byte(isoName)
	systemUse := []byte(nil)
	if rrName != "" {
		systemUse = append(systemUse, 'N', 'M', byte(5+len(rrName)), 1, 0)
		systemUse = append(systemUse, rrName...)
	}
	length := 33 + len(name) + len(systemUse)
	if length%2 == 1 {
		length++
	}
	rec := make([]byte, length)
	rec[0] = byte(length)
	binary.LittleEndian.PutUint32(rec[2:6], extent)
	binary.BigEndian.PutUint32(rec[6:10], extent)
	binary.LittleEndian.PutUint32(rec[10:14], size)
	binary.BigEndian.PutUint32(rec[14:18], size)
	if dir {
		rec[25] = 2
	}
	rec[28] = 1
	rec[32] = byte(len(name))
	copy(rec[33:], name)
	copy(rec[33+len(name):], systemUse)
	return rec
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
