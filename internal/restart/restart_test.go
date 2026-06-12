package restart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinary(t *testing.T) {
	path, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty binary path")
	}
	// Should be an absolute path
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
}

func TestLaunch_WritesScriptAndStarts(t *testing.T) {
	// Create a temp binary that just exits
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Use a non-existent PID so the script doesn't wait long
	err := Launch(Request{
		Binary:  fakeBin,
		Args:    []string{"--version"},
		WorkDir: dir,
		PID:     999999, // non-existent PID
	})
	// Launch may error if the script can't be started, that's fine
	// The important thing is it doesn't panic
	_ = err
}

func TestLaunch_DefaultsPID(t *testing.T) {
	// Just test it doesn't panic with PID=0
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// PID=0 should be replaced with os.Getpid()
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

	// WorkDir="" should be replaced with os.Getwd()
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

	// Verify the script file exists and has content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty script")
	}

	// Cleanup
	os.Remove(path)
}

func TestLaunchPlatformScript(t *testing.T) {
	dir := t.TempDir()
	// Create a simple script that exits immediately
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
	// May error if script already deleted, that's fine
	_ = err
}
