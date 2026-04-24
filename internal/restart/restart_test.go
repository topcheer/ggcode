package restart

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if !filepath.IsAbs(bin) {
		t.Fatalf("ResolveBinary returned relative path: %s", bin)
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
		{"arg with spaces", "'arg with spaces'"},
		{"$HOME", "'$HOME'"},
		{"back`tick", "'back`tick'"},
	}
	for _, tt := range tests {
		got := bashEscape(tt.input)
		if got != tt.want {
			t.Errorf("bashEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteScriptBasicContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	tmpBin := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  tmpBin,
		Args:    []string{"--resume", "sess-abc", "--config", "/tmp/gg.yaml"},
		WorkDir: t.TempDir(),
		PID:     12345,
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript: %v", err)
	}
	defer os.Remove(scriptPath)

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}

	s := string(content)

	// Verify PID is embedded.
	if !strings.Contains(s, "PARENT_PID=12345") {
		t.Error("script should contain PARENT_PID=12345")
	}

	// Verify binary path is embedded (bash-escaped).
	if !strings.Contains(s, tmpBin) {
		t.Errorf("script should contain binary path %q", tmpBin)
	}

	// Verify workdir is embedded.
	if !strings.Contains(s, req.WorkDir) {
		t.Errorf("script should contain workdir %q", req.WorkDir)
	}

	// Verify args are embedded.
	if !strings.Contains(s, "sess-abc") {
		t.Error("script should contain session ID")
	}
	if !strings.Contains(s, "gg.yaml") {
		t.Error("script should contain config path")
	}

	// Verify key safety mechanisms.
	if !strings.Contains(s, "kill -0") {
		t.Error("script should use kill -0 to poll parent")
	}
	if !strings.Contains(s, "trap cleanup EXIT") {
		t.Error("script should have cleanup trap")
	}
	if !strings.Contains(s, "exec") {
		t.Error("script should use exec to replace itself")
	}
}

func TestWriteScriptEmptyArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	tmpBin := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  tmpBin,
		Args:    nil,
		WorkDir: t.TempDir(),
		PID:     99,
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript: %v", err)
	}
	defer os.Remove(scriptPath)

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}

	s := string(content)
	if !strings.Contains(s, "ARGS=()") {
		t.Error("script should have empty ARGS array when no args provided")
	}
}

func TestWriteScriptSpecialCharsInPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	// Create a binary with special chars in directory name.
	dir := filepath.Join(t.TempDir(), "path with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpBin := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  tmpBin,
		Args:    []string{"--config", "/path/with spaces/gg.yaml"},
		WorkDir: dir,
		PID:     42,
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript with special chars: %v", err)
	}
	defer os.Remove(scriptPath)

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}

	// Verify the script was generated without errors.
	if len(content) == 0 {
		t.Fatal("script content is empty")
	}
}

func TestLaunchSetsDefaults(t *testing.T) {
	// Verify Launch fills in defaults for PID and WorkDir.
	// We can't actually test Launch (it starts a process) but we can
	// verify the Request is normalized correctly by checking writePlatformScript.
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	tmpBin := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// PID=0 should be treated as "use os.Getpid()" — but writePlatformScript
	// just takes whatever PID is passed. Launch() normalizes it.
	// Test that PID=0 doesn't crash.
	req := Request{
		Binary: tmpBin,
		PID:    0,
	}
	// Simulate what Launch does.
	if req.PID <= 0 {
		req.PID = os.Getpid()
	}

	_, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript with PID=0 (normalized to %d): %v", req.PID, err)
	}
}

func TestLaunchScriptIsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	tmpBin := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		Binary:  tmpBin,
		WorkDir: t.TempDir(),
		PID:     os.Getpid(),
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scriptPath)

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("script should be executable")
	}
}
