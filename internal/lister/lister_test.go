package lister

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
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

func TestTopLevelArchivesExpandRecursively(t *testing.T) {
	dir := t.TempDir()

	zipPath := filepath.Join(dir, "outer.zip")
	writeZipFile(t, zipPath, map[string][]byte{
		"bundle.tgz": tarGzipBytes(t, "deep/file.txt", []byte("nested")),
	})
	zipEntries, err := List(context.Background(), zipPath, Options{})
	must(t, err)
	if !containsPath(zipEntries, "bundle.tgz!deep/file.txt") {
		t.Fatalf("zip recursion missing nested tar.gz entry: %#v", zipEntries)
	}

	tgzPath := filepath.Join(dir, "outer.tgz")
	writeTarGzipFile(t, tgzPath, map[string][]byte{
		"payload.zip": zipBytes(t, map[string][]byte{"inside.txt": []byte("zip")}),
	})
	tgzEntries, err := List(context.Background(), tgzPath, Options{})
	must(t, err)
	if !containsPath(tgzEntries, "payload.zip!inside.txt") {
		t.Fatalf("tar.gz recursion missing nested zip entry: %#v", tgzEntries)
	}
}

func TestNestedRPMExpandsWhenHelpersExist(t *testing.T) {
	if _, err := exec.LookPath("rpmbuild"); err != nil {
		t.Skip("rpmbuild helper is not installed")
	}
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		t.Skip("rpm2cpio helper is not installed")
	}
	dir := t.TempDir()
	rpmPath := filepath.Join(dir, "fixture.rpm")
	makeRPMFixture(t, rpmPath)
	rpmBytes, err := os.ReadFile(rpmPath)
	must(t, err)

	zipPath := filepath.Join(dir, "outer.zip")
	writeZipFile(t, zipPath, map[string][]byte{"package.rpm": rpmBytes})
	entries, err := List(context.Background(), zipPath, Options{})
	must(t, err)
	if !containsPath(entries, "package.rpm!./opt/lfl-fixture/rpm-file.txt") {
		t.Fatalf("nested RPM payload missing from zip entries: %#v", entries)
	}
}

func TestMountedISOExpandsNestedRPMWhenHelpersExist(t *testing.T) {
	if os.Getenv("LFL_RUN_MOUNT_ISO_TESTS") != "1" {
		t.Skip("mounted ISO integration test requires Linux mount privileges; set LFL_RUN_MOUNT_ISO_TESTS=1 to run")
	}
	if _, err := exec.LookPath("rpmbuild"); err != nil {
		t.Skip("rpmbuild helper is not installed")
	}
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		t.Skip("rpm2cpio helper is not installed")
	}

	dir := t.TempDir()
	root := filepath.Join(dir, "iso-root")
	must(t, os.MkdirAll(filepath.Join(root, "Packages"), 0755))
	makeRPMFixture(t, filepath.Join(root, "Packages", "package.rpm"))

	isoPath := filepath.Join(dir, "nested-rpm.iso")
	cmd := exec.Command(isoImageTool(t), "-quiet", "-o", isoPath, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make ISO fixture: %v\n%s", err, out)
	}

	entries, err := List(context.Background(), isoPath, Options{})
	must(t, err)
	if !containsPath(entries, "Packages/package.rpm!./opt/lfl-fixture/rpm-file.txt") {
		t.Fatalf("nested RPM payload missing from mounted ISO entries: %#v", entries)
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

func TestListSquashFSWhenHelperExists(t *testing.T) {
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("mksquashfs helper is not installed")
	}
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs helper is not installed")
	}
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "root.squashfs")
	makeSquashFSFixture(t, imagePath)

	entries, err := List(context.Background(), imagePath, Options{})
	must(t, err)
	if !containsPath(entries, "etc/example.conf") {
		t.Fatalf("squashfs entries missing fixture file: %#v", entries)
	}
}

func TestDebianISOFromOtherProject(t *testing.T) {
	if os.Getenv("LFL_RUN_MOUNT_ISO_TESTS") != "1" {
		t.Skip("mounted ISO integration test requires Linux mount privileges; set LFL_RUN_MOUNT_ISO_TESTS=1 to run")
	}
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
	if len(entries) < 10000 {
		t.Fatalf("Debian ISO listed %d entries, expected nested compressed-file expansion", len(entries))
	}
	for _, want := range []string{"README.TXT", "install.amd/VMLINUZ", "dists/TRIXIE/MAIN/BINARY_A/Packages.gz", "dists/TRIXIE/MAIN/BINARY_A/Packages.gz!content", "doc/FAQ/debian-faq.en.html.tar.gz!index.html"} {
		if !containsPath(entries, want) {
			t.Fatalf("Debian ISO missing %q among %d entries from %s (%d bytes)", want, len(entries), isoPath, st.Size())
		}
	}
}

func BenchmarkListDebianISO(b *testing.B) {
	if os.Getenv("LFL_RUN_MOUNT_ISO_TESTS") != "1" {
		b.Skip("mounted ISO benchmark requires Linux mount privileges; set LFL_RUN_MOUNT_ISO_TESTS=1 to run")
	}
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

func tarGzipBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	writeTarFile(t, tw, name, data)
	must(t, tw.Close())
	must(t, gw.Close())
	return buf.Bytes()
}

func makeSquashFSFixture(t *testing.T, imagePath string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	must(t, os.MkdirAll(filepath.Join(root, "etc"), 0755))
	must(t, os.WriteFile(filepath.Join(root, "etc", "example.conf"), []byte("ok\n"), 0644))
	cmd := exec.Command("mksquashfs", root, imagePath, "-quiet", "-noappend")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mksquashfs fixture: %v\n%s", err, out)
	}
}

func writeZipFile(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		must(t, err)
		_, err = w.Write(data)
		must(t, err)
	}
	must(t, zw.Close())
	must(t, f.Close())
}

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		must(t, err)
		_, err = w.Write(data)
		must(t, err)
	}
	must(t, zw.Close())
	return buf.Bytes()
}

func writeTarGzipFile(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	must(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, data := range files {
		writeTarFile(t, tw, name, data)
	}
	must(t, tw.Close())
	must(t, gw.Close())
	must(t, f.Close())
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

func makeRPMFixture(t *testing.T, outPath string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "rpmbuild")
	for _, dir := range []string{"BUILD", "RPMS", "SOURCES", "SPECS", "SRPMS"} {
		must(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}
	spec := `Name: lfl-fixture
Version: 1.0
Release: 1
Summary: lfl fixture rpm
License: MIT
BuildArch: noarch
%description
lfl fixture rpm
%install
mkdir -p %{buildroot}/opt/lfl-fixture
printf rpm-fixture > %{buildroot}/opt/lfl-fixture/rpm-file.txt
%files
/opt/lfl-fixture/rpm-file.txt
`
	specPath := filepath.Join(root, "SPECS", "lfl-fixture.spec")
	must(t, os.WriteFile(specPath, []byte(spec), 0644))
	cmd := exec.Command("rpmbuild", "--define", "_topdir "+root, "-bb", specPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rpmbuild fixture: %v\n%s", err, out)
	}
	matches, err := filepath.Glob(filepath.Join(root, "RPMS", "noarch", "*.rpm"))
	must(t, err)
	if len(matches) != 1 {
		t.Fatalf("expected one built rpm, found %d: %#v", len(matches), matches)
	}
	rpmBytes, err := os.ReadFile(matches[0])
	must(t, err)
	must(t, os.WriteFile(outPath, rpmBytes, 0644))
}

func isoImageTool(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"xorrisofs", "genisoimage", "mkisofs"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	t.Skip("ISO image builder is not installed")
	return ""
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
