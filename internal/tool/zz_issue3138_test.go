package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3138_DotfileCandidatesNotAllPass(t *testing.T) {

	if isLikelyMatch(".env", ".gitignore", ".env", ".gitignore") {
		t.Fatal("'.env' vs '.gitignore' (both guarded stems) must not be a likely match")
	}

	if !isLikelyMatch(".env", ".env", ".env", ".env") {
		t.Fatal("dotfile self-match broken")
	}

	if !isLikelyMatch(".gitig", ".gitignore", ".gitig", ".gitignore") {
		t.Fatal("'.gitig' vs '.gitignore' prefix family lost")
	}
}

func TestIssue3138_CollectCandidatesDotfileScan(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".env", ".gitignore", ".eslintrc", "normal.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := collectCandidates(dir, ".env", 1)
	for _, p := range got {
		base := filepath.Base(p)
		if base == ".gitignore" || base == ".eslintrc" {
			t.Fatalf("unrelated dotfile %s leaked into candidates for query .env: %v", base, got)
		}
	}
	found := false
	for _, p := range got {
		if filepath.Base(p) == ".env" {
			found = true
		}
	}
	if !found {
		t.Fatalf("exact dotfile .env missing from its own candidates: %v", got)
	}
}
