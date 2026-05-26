package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mjwagner2277/omega-file-lister/internal/lister"
)

func TestRunWritesDefaultOutputFile(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	cleanupDefaultOutput(t, archive)
	var stderr bytes.Buffer

	code := run([]string{archive}, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	body, err := os.ReadFile(defaultOutputPath(archive, false))
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

func TestRunJSONWritesJSONOutputFile(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	cleanupDefaultOutput(t, archive)
	var stderr bytes.Buffer

	code := run([]string{"-json", archive}, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	body, err := os.ReadFile(defaultOutputPath(archive, true))
	if err != nil {
		t.Fatal(err)
	}
	var entry lister.Entry
	if err := json.Unmarshal(bytes.TrimSpace(body), &entry); err != nil {
		t.Fatalf("json output is invalid: %v; body=%q", err, string(body))
	}
	if entry.Path != "alpha.txt" {
		t.Fatalf("json path = %q, want alpha.txt", entry.Path)
	}
}

func TestRunQuietSuppressesProgress(t *testing.T) {
	archive := makeZip(t, map[string]string{"alpha.txt": "alpha"})
	cleanupDefaultOutput(t, archive)
	var stderr bytes.Buffer

	code := run([]string{"-quiet", archive}, &stderr)
	if code != 0 {
		t.Fatalf("run exit code = %d, stderr = %s", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("quiet stderr = %q, want empty", stderr.String())
	}
}

func TestHelpIsUserFriendly(t *testing.T) {
	var stderr bytes.Buffer

	code := run([]string{"-help"}, &stderr)
	if code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	help := stderr.String()
	for _, want := range []string{"Linux File Lister", "Usage:", "Examples:", "_files", "_files.json", "-workers", "-quiet", "-json"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q: %s", want, help)
		}
	}
	if strings.Contains(help, "stdout") {
		t.Fatalf("help should not mention stdout: %s", help)
	}
}

func TestDefaultOutputPath(t *testing.T) {
	got := defaultOutputPath(filepath.Join("tmp", "some_thing.rpm"), false)
	want := "some_thing_rpm_files"
	if got != want {
		t.Fatalf("defaultOutputPath = %q, want %q", got, want)
	}
	got = defaultOutputPath(filepath.Join("tmp", "some_thing.rpm"), true)
	want = "some_thing_rpm_files.json"
	if got != want {
		t.Fatalf("defaultOutputPath json = %q, want %q", got, want)
	}
}

func cleanupDefaultOutput(t *testing.T, input string) {
	t.Helper()
	for _, jsonOut := range []bool{false, true} {
		out := defaultOutputPath(input, jsonOut)
		_ = os.Remove(out)
		t.Cleanup(func() { _ = os.Remove(out) })
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
