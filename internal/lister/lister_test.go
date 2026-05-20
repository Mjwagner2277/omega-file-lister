package lister

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestListMultipleArchiveTypes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		make func(t *testing.T, path string)
		want string
	}{
		{"zip", makeZipFixture, "docs/readme.txt"},
		{"tar", makeTarFixture, "etc/config.yaml"},
		{"tar.gz", makeTarGzipFixture, "usr/bin/tool"},
		{"gzip", makeGzipFixture, "content"},
		{"cpio", makeCPIOFixture, "var/lib/data"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "fixture."+strings.ReplaceAll(tc.name, "/", "-"))
			tc.make(t, path)
			entries, err := List(context.Background(), path, Options{})
			must(t, err)
			if !containsPath(entries, tc.want) {
				t.Fatalf("%s entries missing %q: %#v", tc.name, tc.want, entries)
			}
		})
	}
}

func TestListTarBzip2WhenHelperExists(t *testing.T) {
	if _, err := exec.LookPath("bzip2"); err != nil {
		t.Skip("bzip2 helper is not installed")
	}
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "fixture.tar")
	makeTarFixture(t, tarPath)
	cmd := exec.Command("bzip2", "-k", tarPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bzip2 fixture: %v\n%s", err, out)
	}
	entries, err := List(context.Background(), tarPath+".bz2", Options{})
	must(t, err)
	if !containsPath(entries, "etc/config.yaml") {
		t.Fatalf("tar.bz2 entries missing fixture file: %#v", entries)
	}
}

func TestDebianISOFromOtherProject(t *testing.T) {
	isoPath := os.Getenv("LFL_DEBIAN_ISO")
	if isoPath == "" {
		isoPath = "/private/tmp/debian-13.5.0-amd64-netinst.iso"
	}
	st, err := os.Stat(isoPath)
	if err != nil {
		t.Skipf("Debian ISO fixture not found: %s", isoPath)
	}
	entries, err := List(context.Background(), isoPath, Options{})
	must(t, err)
	if len(entries) < 1000 {
		t.Fatalf("Debian ISO listed %d entries, expected at least 1000", len(entries))
	}
	for _, want := range []string{"README.TXT", "install.amd/VMLINUZ", "dists/TRIXIE/MAIN/BINARY_A/Packages.gz"} {
		if !containsPath(entries, want) {
			t.Fatalf("Debian ISO missing %q among %d entries from %s (%d bytes)", want, len(entries), isoPath, st.Size())
		}
	}
}

func BenchmarkListDebianISO(b *testing.B) {
	isoPath := os.Getenv("LFL_DEBIAN_ISO")
	if isoPath == "" {
		isoPath = "/private/tmp/debian-13.5.0-amd64-netinst.iso"
	}
	if _, err := os.Stat(isoPath); err != nil {
		b.Skipf("Debian ISO fixture not found: %s", isoPath)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entries, err := List(context.Background(), isoPath, Options{})
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) == 0 {
			b.Fatal("no entries listed")
		}
	}
}

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

func makeZipFixture(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create("docs/readme.txt")
	must(t, err)
	_, err = w.Write([]byte("zip"))
	must(t, err)
	must(t, zw.Close())
	must(t, f.Close())
}

func makeTarFixture(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	tw := tar.NewWriter(f)
	writeTarFile(t, tw, "etc/config.yaml", []byte("debug: false\n"))
	must(t, tw.Close())
	must(t, f.Close())
}

func makeTarGzipFixture(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	writeTarFile(t, tw, "usr/bin/tool", []byte("#!/bin/sh\n"))
	must(t, tw.Close())
	must(t, gw.Close())
	must(t, f.Close())
}

func makeGzipFixture(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	gw := gzip.NewWriter(f)
	_, err = gw.Write([]byte("plain gzip stream"))
	must(t, err)
	must(t, gw.Close())
	must(t, f.Close())
}

func makeCPIOFixture(t *testing.T, path string) {
	t.Helper()
	archive := newcEntry("var/lib/data", 0100644, []byte("x")) + newcEntry("TRAILER!!!", 0, nil)
	must(t, os.WriteFile(path, []byte(archive), 0644))
}

func writeTarFile(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}))
	_, err := tw.Write(data)
	must(t, err)
}

func containsPath(entries []Entry, want string) bool {
	for _, entry := range entries {
		if entry.Path == want {
			return true
		}
	}
	return false
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
