package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeProjectMemoryFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadProjectMemoryFiles_OversizedDegradesToIndex(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a", MaxInlineProjectMemoryChars+50)
	p := writeProjectMemoryFile(t, dir, "AGENTS.md", body)

	content, files, err := ReadProjectMemoryFiles([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "[project-memory-index]") {
		t.Fatalf("oversized file must degrade to index line, got: %q", content)
	}
	if !strings.Contains(content, p) {
		t.Fatalf("index line must carry the path so read_file can target it, got: %q", content)
	}
	if !strings.Contains(content, "read_file") {
		t.Fatalf("index line must instruct read_file retrieval, got: %q", content)
	}
	if strings.Contains(content, body) {
		t.Fatalf("oversized body must NOT be flat-injected")
	}
	if len(files) != 1 || files[0] != p {
		t.Fatalf("oversized file must still be reported in files list, got %v", files)
	}
}

func TestReadProjectMemoryFiles_WithinCapInlinesVerbatim(t *testing.T) {
	dir := t.TempDir()
	exact := strings.Repeat("b", MaxInlineProjectMemoryChars)
	p := writeProjectMemoryFile(t, dir, "AGENTS.md", exact)

	content, _, err := ReadProjectMemoryFiles([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "[project-memory-index]") {
		t.Fatalf("file exactly at cap must inline verbatim, got index line: %q", content)
	}
	if !strings.Contains(content, exact) {
		t.Fatalf("full body expected inline")
	}
}

func TestReadProjectMemoryFiles_CJKBoundary(t *testing.T) {
	dir := t.TempDir()

	atCap := strings.Repeat("中", MaxInlineProjectMemoryChars)
	p1 := writeProjectMemoryFile(t, dir, "AGENTS.md", atCap)
	content, _, err := ReadProjectMemoryFiles([]string{p1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "[project-memory-index]") {
		t.Fatalf("exactly-cap CJK file (6000 bytes, 2000 runes) must inline, got index line")
	}

	overCap := strings.Repeat("中", MaxInlineProjectMemoryChars+1)
	p2 := writeProjectMemoryFile(t, dir, "AGENTS.md", overCap)
	content2, _, err := ReadProjectMemoryFiles([]string{p2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content2, "[project-memory-index]") {
		t.Fatalf("2001-rune CJK file must degrade to index")
	}
	if !utf8.ValidString(content2) {
		t.Fatalf("degraded index line must be valid UTF-8 (no mid-rune slicing)")
	}
}

// P2 (#5446) supersedes the old preview contract: oversized entries index
// to path + size only; content discovery is read_file on the path.
func TestProjectMemoryIndexLine_NoPreviewBodyAnymore(t *testing.T) {
	data := "\n\n# Title\n\nsecond line\n\nthird\nfourth\nfifth\nsixth\n"
	line := projectMemoryIndexLine("/tmp/AGENTS.md", data)
	if !strings.Contains(line, "/tmp/AGENTS.md") {
		t.Fatalf("index line must keep the path: %q", line)
	}
	if !strings.Contains(line, "read_file") {
		t.Fatalf("index line must keep the read_file instruction: %q", line)
	}
	for _, gone := range []string{"# Title", "second line", "sixth"} {
		if strings.Contains(line, gone) {
			t.Fatalf("preview body %q must not be inlined anymore: %q", gone, line)
		}
	}
}
