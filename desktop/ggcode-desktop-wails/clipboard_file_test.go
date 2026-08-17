package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClipboardPathOutputSplitsOnlyByLines(t *testing.T) {
	output := "/tmp/report, final.txt\n/tmp/other.txt\n"
	paths := parseClipboardPathOutput(output)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/tmp/report, final.txt" {
		t.Fatalf("expected comma-containing file name preserved, got %q", paths[0])
	}
	if paths[1] != "/tmp/other.txt" {
		t.Fatalf("expected %q, got %q", "/tmp/other.txt", paths[1])
	}
}

func TestParseClipboardPathOutputCRHandling(t *testing.T) {
	// #579: the file:// URI branch was removed as dead code (upstream
	// AppleScript reads |path|(), never URIs). This test now covers the
	// surviving behavior: CR/LF normalization and literal passthrough.
	output := "file:///tmp/a%20b.txt\r\n/tmp/c.txt"
	paths := parseClipboardPathOutput(output)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	// Dead branch removed: the first item passes through literally — it
	// is a phantom path only if upstream ever produced URIs (it cannot).
	if paths[0] != "file:///tmp/a%20b.txt" {
		t.Fatalf("expected literal passthrough after dead-code removal, got %q", paths[0])
	}
	if paths[1] != "/tmp/c.txt" {
		t.Fatalf("expected %q, got %q", "/tmp/c.txt", paths[1])
	}
}

func TestReadFileAsBase64SizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	// 150MB would be too slow to write fully; create a sparse file instead
	// by seeking past the limit and writing one byte.
	f, err := os.OpenFile(big, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(maxReadFileBase64Bytes+1, 0); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	app := &App{workDir: dir}
	_, err = app.ReadFileAsBase64(big)
	if err == nil {
		t.Fatal("expected size-limit error for oversized file")
	}
	if !strings.Contains(err.Error(), "150MB") {
		t.Fatalf("expected error to mention 150MB limit, got %q", err.Error())
	}
}

func TestReadFileAsBase64SmallFileOK(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	app := &App{workDir: dir}
	data, err := app.ReadFileAsBase64(small)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if data.MimeType != "application/octet-stream" {
		t.Fatalf("unexpected mime %q", data.MimeType)
	}
	if data.Data != "aGVsbG8=" {
		t.Fatalf("expected base64 'aGVsbG8=', got %q", data.Data)
	}
}

// #329: App.ReadFileContent must enforce workspace-root containment.
func TestAppReadFileContent_Containment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	app := &App{workDir: dir}

	content, err := app.ReadFileContent(filepath.Join(dir, "note.txt"))
	if err != nil || content != "hi" {
		t.Fatalf("expected in-workspace read to succeed, got %q err=%v", content, err)
	}

	other := t.TempDir()
	outside := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(outside, []byte("pw"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFileContent(outside); err == nil {
		t.Fatal("expected access denied for file outside workspace root")
	}
}

// #329: App.ListFiles must enforce workspace-root containment while keeping
// the []map[string]interface{} shape the frontend expects.
func TestAppListFiles_Containment(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644)
	app := &App{workDir: dir}

	entries := app.ListFiles(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e["name"] != "a.txt" || e["isDir"] != false {
		t.Fatalf("unexpected entry shape: %+v", e)
	}

	if got := app.ListFiles(t.TempDir()); got != nil {
		t.Fatalf("expected nil for directory outside workspace root, got %v", got)
	}
}
