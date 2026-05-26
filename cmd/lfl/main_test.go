package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesDefaultOutputFile(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{archive}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty by default", stdout.String())
	}
	outPath := defaultOutputPath(archive)
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "alpha.txt") {
		t.Fatalf("output file missing listed file: %q", string(body))
	}
	for _, want := range []string{"processing", "opening input", "expanding archive entries", "writing 1 entries to", "done:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, stderr.String())
		}
	}
}

func TestRunStdoutKeepsPipeMode(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-stdout", archive}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "alpha.txt") {
		t.Fatalf("stdout missing listed file: %q", stdout.String())
	}
	if _, err := os.Stat(defaultOutputPath(archive)); !os.IsNotExist(err) {
		t.Fatalf("default output file exists with -stdout, stat err = %v", err)
	}
}

func TestRunQuietSuppressesProgress(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-quiet", archive}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("quiet stdout = %q, want empty by default", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("quiet stderr = %q, want empty", stderr.String())
	}
}

func TestHelpIsUserFriendly(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	help := stderr.String()
	for _, want := range []string{"Linux File Lister", "Usage:", "Examples:", "_files.txt", "-stdout", "-workers", "-quiet", "-json"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q: %s", want, help)
		}
	}
}

func TestDefaultOutputPath(t *testing.T) {
	got := defaultOutputPath(filepath.Join("tmp", "rocky.iso"))
	want := filepath.Join("tmp", "rocky_files.txt")
	if got != want {
		t.Fatalf("defaultOutputPath = %q, want %q", got, want)
	}
}

func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := t.TempDir() + "/fixture.zip"
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
