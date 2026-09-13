package agent

// #2179 regression: URL-shaped text was captured wholesale as an "error
// file" - never matching any edited file, but inflating errorFiles past
// the recency gate for a free attribution score.

import "testing"

func TestCausalErrorFilesSkipsURLs(t *testing.T) {
	files := extractErrorFiles("see https://x.com/a.go:1: for details")
	if len(files) != 0 {
		t.Fatalf("URL-only output must yield no error files, got %v", files)
	}
	files = extractErrorFiles("docs at http://cdn.example.com/src/lib.rs:12: more")
	if len(files) != 0 {
		t.Fatalf("http URL must be skipped, got %v", files)
	}
	// Mixed: the real file survives, the URL does not.
	files = extractErrorFiles("see https://x.com/a.go:1: and src/a.go:3:1: boom")
	found := false
	for _, f := range files {
		if f == "https://x.com/a.go" {
			t.Fatalf("URL capture leaked through: %v", files)
		}
		if f == "src/a.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("real file must survive next to a URL, got %v", files)
	}
}
