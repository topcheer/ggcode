package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// #3138 probes: the candidate-side dotfile guard (symmetric to the
// query-side #1684 case-3 fix).

// Dotfiles must no longer sail through the quick filter on the empty-stem
// HasPrefix(x, "") hole - only genuinely name-related candidates pass.
func TestIssue3138_DotfileCandidatesNotAllPass(t *testing.T) {
	// The contract: with the guard, a dotfile candidate's stem is its
	// full name, so unrelated dotfiles are NOT likely matches.
	if isLikelyMatch(".env", ".gitignore", ".env", ".gitignore") {
		t.Fatal("'.env' vs '.gitignore' (both guarded stems) must not be a likely match")
	}
	// A dotfile must still match ITSELF (exact stem equality path).
	if !isLikelyMatch(".env", ".env", ".env", ".env") {
		t.Fatal("dotfile self-match broken")
	}
	// Query-side guard interop: querying ".gitig" should keep ".gitignore"
	// as a plausible prefix-family candidate.
	if !isLikelyMatch(".gitig", ".gitignore", ".gitig", ".gitignore") {
		t.Fatal("'.gitig' vs '.gitignore' prefix family lost")
	}
}

// Walk-level probe: scanning a directory with one query dotfile must not
// return every OTHER dotfile in the directory.
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
