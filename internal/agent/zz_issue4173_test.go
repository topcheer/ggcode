package agent

import (
	"strings"
	"testing"
)

// #4173: supersede must key on (toolName, argSummary), not the bare tool
// name. A successful grep for pattern B says nothing about an earlier
// no-match for pattern A - especially after a mutation changed the match
// surface - yet the old name-only supersede cleared A's observation, so
// checkStaleTrap went silent exactly when it exists to fire.
func TestReverifySupersedeIsQueryGranular(t *testing.T) {
	// Issue scenario verbatim: negative for TODO, mutation, then an
	// UNRELATED grep (func main) returns matches.
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"TODO"}`, "No matches found for pattern TODO", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 3)
	s.recordToolResult("grep", `{"pattern":"func main"}`, "main.go:12: func main() {", false, 5)
	// Aged (8-1 >= 6) + mutated after: the TODO observation must survive
	// the unrelated positive and the trap must fire.
	msg := s.checkStaleTrap("All TODOs are cleaned up, everything works.", 8)
	if msg == "" {
		t.Fatal("cross-query positive must not supersede; stale trap went silent")
	}
	for _, want := range []string{"grep", "TODO"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
}

// Control: the SAME query returning real content still supersedes its own
// negative observation (old semantics preserved where query identity matches).
func TestReverifySupersedeSameQueryStillSupersedes(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"TODO"}`, "No matches found for pattern TODO", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)
	s.recordToolResult("grep", `{"pattern":"TODO"}`, "main.go:4: // TODO fix", false, 5)
	if msg := s.checkStaleTrap("All TODOs are cleaned up, everything works.", 8); msg != "" {
		t.Fatalf("same-query positive must supersede, got: %s", msg)
	}
}

// Argless tools keep the old behavior: empty argSummary on both sides is a
// query match, so any success still clears the tool's stale negatives.
func TestReverifySupersedeArglessToolUnaffected(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("list_directory", "", "empty", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)
	s.recordToolResult("list_directory", "", "main.go\ngo.mod", false, 5)
	if msg := s.checkStaleTrap("the listing is complete now", 8); msg != "" {
		t.Fatalf("argless same-tool positive must supersede, got: %s", msg)
	}
}
