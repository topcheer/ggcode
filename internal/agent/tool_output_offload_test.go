package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpillBelowMinReturnsEmpty(t *testing.T) {
	o := newOutputOffloader()
	small := strings.Repeat("x", minSpillBytes-1)
	if path := o.spill("run_command", small); path != "" {
		t.Fatalf("content below minSpillBytes must not be spilled, got %q", path)
	}
}

func TestSpillWritesFullContent(t *testing.T) {
	o := newOutputOffloader()
	// 200KB content whose middle section contains a unique marker.
	head := strings.Repeat("head line\n", 2000)
	mid := "UNIQUE_MIDDLE_MARKER needle-42\n" + strings.Repeat("mid\n", 40000)
	tail := strings.Repeat("tail line\n", 2000)
	full := head + mid + tail

	path := o.spill("run_command", full)
	if path == "" {
		t.Fatal("spill must return a path for large content")
	}
	defer os.RemoveAll(filepath.Dir(path))

	if !filepath.IsAbs(path) {
		t.Fatalf("spill path must be absolute, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spilled file: %v", err)
	}
	if !strings.Contains(string(data), "UNIQUE_MIDDLE_MARKER needle-42") {
		t.Fatal("spilled file must contain the middle section dropped by head+tail truncation")
	}
	if len(data) != len(full) {
		t.Fatalf("spilled size mismatch: got %d want %d", len(data), len(full))
	}
}

func TestSpillNoticeContainsPath(t *testing.T) {
	notice := spillNotice("/tmp/spill/x.txt", 100*1024)
	if !strings.Contains(notice, "/tmp/spill/x.txt") {
		t.Fatal("notice must contain the spill path")
	}
	if !strings.Contains(notice, "read_file") {
		t.Fatal("notice must tell the agent how to recover the content")
	}
}

func TestSpillCapsOversizedContent(t *testing.T) {
	o := newOutputOffloader()
	huge := strings.Repeat("y", maxSpillBytes+1024)
	path := o.spill("browser", huge)
	if path == "" {
		t.Fatal("oversized content should still spill (capped)")
	}
	defer os.RemoveAll(filepath.Dir(path))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat spilled file: %v", err)
	}
	if info.Size() > maxSpillBytes+512 {
		t.Fatalf("spilled file exceeds cap: %d bytes", info.Size())
	}
}

func TestSpillPrunesOldestBeyondCap(t *testing.T) {
	o := newOutputOffloader()
	paths := make([]string, 0, maxSpillFiles+3)
	for i := 0; i < maxSpillFiles+3; i++ {
		p := o.spill("grep", strings.Repeat("z", minSpillBytes+1024))
		if p == "" {
			t.Fatalf("spill %d returned empty", i)
		}
		paths = append(paths, p)
	}
	dir := filepath.Dir(paths[0])
	defer os.RemoveAll(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	if len(entries) > maxSpillFiles {
		t.Fatalf("spill dir exceeds cap: %d files", len(entries))
	}
	// The first (oldest) spilled file must have been pruned.
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatal("oldest spill file must be pruned when cap exceeded")
	}
}

func TestSpillToolNameSanitized(t *testing.T) {
	o := newOutputOffloader()
	path := o.spill("mcp__weird/server__tool!name", strings.Repeat("q", minSpillBytes+512))
	if path == "" {
		t.Fatal("spill must succeed with unusual tool names")
	}
	defer os.RemoveAll(filepath.Dir(path))
	base := filepath.Base(path)
	if strings.ContainsAny(base, " /\\!") {
		t.Fatalf("file name contains unsafe characters: %q", base)
	}
}
