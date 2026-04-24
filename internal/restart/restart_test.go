package restart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinary(t *testing.T) {
	bin, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if bin == "" {
		t.Fatal("ResolveBinary returned empty string")
	}
	// Should be an absolute path.
	if !filepath.IsAbs(bin) {
		t.Fatalf("ResolveBinary returned relative path: %s", bin)
	}
}

func TestWriteScriptDoesNotError(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  tmpBin,
		Args:    []string{"--resume", "test-session-123", "--config", "/tmp/ggcode.yaml"},
		WorkDir: t.TempDir(),
		PID:     os.Getpid(),
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript: %v", err)
	}
	defer os.Remove(scriptPath)

	if scriptPath == "" {
		t.Fatal("expected non-empty script path")
	}

	// Verify the script file exists.
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("script file not found: %v", err)
	}
}

func TestBashEscape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"/path/to/file", "'/path/to/file'"},
	}
	for _, tt := range tests {
		got := bashEscape(tt.input)
		if got != tt.want {
			t.Errorf("bashEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
