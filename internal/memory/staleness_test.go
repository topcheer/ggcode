package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStalenessBrokenPath(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Create a memory entry that references a non-existent file path.
	content := "Use the config loader from internal/config/loader.go to parse settings."
	am.SaveMemory("test-broken-path", content)

	// Use a workingDir where the path doesn't exist.
	report := am.ScanStaleness("/nonexistent/project/root")
	if report.Scanned != 1 {
		t.Fatalf("expected 1 scanned, got %d", report.Scanned)
	}
	if report.BrokenPaths == 0 {
		t.Error("expected broken path detection, got 0")
	}
}

func TestStalenessValidPath(t *testing.T) {
	dir := t.TempDir()
	workingDir := t.TempDir()

	// Create an actual file in workingDir.
	realFile := filepath.Join(workingDir, "src", "main.go")
	os.MkdirAll(filepath.Dir(realFile), 0755)
	os.WriteFile(realFile, []byte("package main"), 0644)

	am := &AutoMemory{dir: dir}
	content := "Entry point is src/main.go"
	am.SaveMemory("test-valid-path", content)

	report := am.ScanStaleness(workingDir)
	if report.BrokenPaths > 0 {
		t.Errorf("expected 0 broken paths, got %d", report.BrokenPaths)
	}
}

func TestStalenessOversized(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Create an entry exceeding maxInlineBytes.
	big := make([]byte, maxInlineBytes+100)
	for i := range big {
		big[i] = 'x'
	}
	am.SaveMemory("test-oversized", string(big))

	report := am.ScanStaleness("")
	if report.Oversized != 1 {
		t.Errorf("expected 1 oversized, got %d", report.Oversized)
	}
}

func TestStalenessAncient(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Create a persistent entry and set its modtime to 200 days ago.
	am.SaveMemory("ancient-impl", "old architecture note")
	path := filepath.Join(dir, "ancient-impl.md")
	oldTime := time.Now().Add(-200 * 24 * time.Hour)
	os.Chtimes(path, oldTime, oldTime)

	report := am.ScanStaleness("")
	if report.Ancient != 1 {
		t.Errorf("expected 1 ancient, got %d", report.Ancient)
	}
}

func TestStalenessEmptyDir(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	report := am.ScanStaleness("/some/path")
	if report.Scanned != 0 {
		t.Errorf("expected 0 scanned, got %d", report.Scanned)
	}
	if report.HasFindings() {
		t.Error("expected no findings for empty dir")
	}
}

func TestIsGenericPath(t *testing.T) {
	tests := []struct {
		path    string
		generic bool
	}{
		{"README.md", true},
		{"main.go", true},
		{"internal/agent/foo.go", false},
		{"example/example.go", true},
		{"cmd/ggcode/root.go", false},
	}
	for _, tt := range tests {
		if got := isGenericPath(tt.path); got != tt.generic {
			t.Errorf("isGenericPath(%q) = %v, want %v", tt.path, got, tt.generic)
		}
	}
}

func TestFormatByteSize(t *testing.T) {
	if got := formatByteSize(500); got != "500B" {
		t.Errorf("formatByteSize(500) = %q, want %q", got, "500B")
	}
	if got := formatByteSize(2048); got != "2KB" {
		t.Errorf("formatByteSize(2048) = %q, want %q", got, "2KB")
	}
}

func TestFormatDuration(t *testing.T) {
	d := 30 * 24 * time.Hour
	if got := formatDuration(d); got != "30d" {
		t.Errorf("formatDuration(%v) = %q, want %q", d, got, "30d")
	}
	d = 400 * 24 * time.Hour
	if got := formatDuration(d); got != "1y35d" {
		t.Errorf("formatDuration(%v) = %q, want %q", d, got, "1y35d")
	}
}
