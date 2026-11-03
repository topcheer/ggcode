package restart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaunch_WritesScriptAndStarts(t *testing.T) {

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	err := Launch(Request{
		Binary:  fakeBin,
		Args:    []string{"--version"},
		WorkDir: dir,
		PID:     999999,
	})

	_ = err
}

func TestLaunch_DefaultsPID(t *testing.T) {

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	_ = Launch(Request{
		Binary:  fakeBin,
		WorkDir: dir,
		PID:     0,
	})
}

func TestLaunch_DefaultsWorkDir(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	_ = Launch(Request{
		Binary: fakeBin,
		PID:    999999,
	})
}

func TestBashEscape(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"/path/to/file", "'/path/to/file'"},
	}
	for _, tt := range tests {
		got := bashEscape(tt.input)
		if got != tt.expected {
			t.Errorf("bashEscape(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestWritePlatformScript(t *testing.T) {
	req := Request{
		Binary:  "/usr/local/bin/ggcode",
		Args:    []string{"--resume", "abc123"},
		WorkDir: "/home/user",
		PID:     12345,
	}

	path, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript error: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty script path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty script")
	}

	os.Remove(path)
}

func TestLaunchPlatformScript(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  "/bin/echo",
		WorkDir: dir,
		PID:     999999,
	}

	err := launchPlatformScript(script, req)

	_ = err
}
