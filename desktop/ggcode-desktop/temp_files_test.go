package main

import (
	"os"
	"runtime"
	"testing"
)

func TestCreateTempPath(t *testing.T) {
	path, err := createTempPath("ggcode-test", ".png")
	if err != nil {
		t.Fatalf("createTempPath() error = %v", err)
	}
	defer os.Remove(path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
}

func TestWriteTempDataFile(t *testing.T) {
	path, err := writeTempDataFile("ggcode-test", ".png", []byte("hello"))
	if err != nil {
		t.Fatalf("writeTempDataFile() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("temp file content = %q, want %q", data, "hello")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != desktopTempFileMode {
			t.Fatalf("temp file mode = %o, want %o", got, desktopTempFileMode)
		}
	}
}
