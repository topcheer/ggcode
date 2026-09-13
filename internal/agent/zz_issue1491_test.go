package agent

// #1491 regressions:
//   - A layer 1: a repo-wide grep with NO explicit path argument never
//     reached recordSearchResult (it sat inside the readPaths else-if);
//     recordSearchResult now runs for every search-whitelisted tool.
//   - A layer 2: grep files_with_matches (bare paths) and code_search
//     ("N. path (relevance: %d%%)") outputs yielded zero tracked files
//     because only the `:N` regex ran for them.
//   - A layer 3: search-side relative keys vs edit-side absolute keys
//     never met; both now anchor to the workspace root.
//   - D: dirSignature took top-2 segments of a deep absolute path, so
//     every directory in the workspace collapsed to the same signature
//     ("/Volumes/x/repo/..." -> "Volumes/x"); absolute paths now take
//     the LAST two segments.

import (
	"strings"
	"testing"
)

func new1491State() *searchInvalidationState {
	s := newSearchInvalidationState()
	s.setBaseDir("/repo")
	return s
}

// Layer 1+2: bare-path grep output (files_with_matches, no explicit
// path arg) must populate the map.
func TestIssue1491A_BareGrepOutputTracked(t *testing.T) {
	s := new1491State()
	s.recordSearchResult("grep", "internal/agent/agent.go\ninternal/tui/model.go\n")
	if len(s.searchResultFiles) != 2 {
		t.Fatalf("bare grep output must track 2 files, got %d", len(s.searchResultFiles))
	}
}

// Layer 2: code_search numbered format must populate the map.
func TestIssue1491A_CodeSearchOutputTracked(t *testing.T) {
	s := new1491State()
	s.recordSearchResult("code_search", "1. internal/agent/search_invalid.go (relevance: 92%)\n2. internal/agent/agent.go (relevance: 71%)\n")
	if len(s.searchResultFiles) != 2 {
		t.Fatalf("code_search output must track 2 files, got %d", len(s.searchResultFiles))
	}
}

// Layer 3: relative search hit + absolute edit target must collide.
func TestIssue1491A_RelativeSearchAbsoluteEditCollide(t *testing.T) {
	s := new1491State()
	s.recordSearchResult("grep", "internal/agent/agent.go\n")
	msg := s.checkEditInvalidation("/repo/internal/agent/agent.go")
	if msg == "" {
		t.Fatal("absolute edit of a relative-keyed search hit must warn")
	}
	if !strings.Contains(msg, "STALE") {
		t.Fatalf("warning must mention staleness, got %q", msg)
	}
}

// Layer 1 via the real call shape: the record call itself is now
// unconditional (whitelist inside) - mirrored here by calling with a
// tool whose ARGS carry no path keys at all (output-only).
func TestIssue1491A_NoPathArgToolsStillRecord(t *testing.T) {
	s := new1491State()
	// code_search args are {"query": "..."} - no path keys; output has them.
	s.recordSearchResult("code_search", "internal/util/path.go\n")
	if len(s.searchResultFiles) != 1 {
		t.Fatalf("query-only code_search must track its output file, got %d", len(s.searchResultFiles))
	}
}

// D: deep absolute paths keep discriminating directories.
func TestIssue1491D_DeepAbsoluteSignatureDiscriminates(t *testing.T) {
	if dirSignature("/Volumes/x/repo/internal/agent") == dirSignature("/Volumes/x/repo/internal/tui") {
		t.Fatal("sibling directories under a deep absolute root must not share a signature")
	}
	if got := dirSignature("/Volumes/x/repo/internal/agent"); got != "internal/agent" {
		t.Fatalf("absolute path should keep its last two segments, got %q", got)
	}
	// Relative paths keep the top-2 behavior (#473 contract).
	if got := dirSignature("src/components"); got != "src/components" {
		t.Fatalf("relative path keeps top-2, got %q", got)
	}
}
