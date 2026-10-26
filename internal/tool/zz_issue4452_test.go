package tool

// Regression tests for GitHub issue #4452:
//  1. knowledge_graph over-long explicit ids must truncate on a rune boundary
//     (byte slicing produced invalid UTF-8 that json.Marshal persisted as
//     U+FFFD, making the node unaddressable after a restart roundtrip).
//  2. link over an existing edge whose valid_from is in the future must be
//     refused instead of appending a parallel edge that goes doubly-live.
//  3. lanchat send must not report phantom dropped recipients when several
//     input identifiers dedup onto one node.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestZZ4452IDTruncationRuneBoundary pins bug 1.
func TestZZ4452IDTruncationRuneBoundary(t *testing.T) {
	tool, _ := newKGTool(t)
	// 40 CJK runes = 120 bytes; the 80-byte cut lands mid-rune in the old code.
	longID := strings.Repeat("节", 40)
	r := kgMustOK(t, tool, `{"action":"add","id":"`+longID+`","type":"entity","title":"long id node"}`)

	// The id echoed back must be valid UTF-8.
	line := r.Content
	idx := strings.Index(line, "'")
	if idx < 0 {
		t.Fatalf("unexpected add output: %s", line)
	}
	id := line[idx+1:]
	if j := strings.Index(id, "'"); j >= 0 {
		id = id[:j]
	}
	if !utf8.ValidString(id) {
		t.Fatalf("truncated id is not valid UTF-8: %q", id)
	}
	if len(id) > 80 {
		t.Fatalf("id exceeds 80 bytes: %d", len(id))
	}

	// Marshal roundtrip must not alter the key (the old invalid-UTF-8 id was
	// persisted as U+FFFD, breaking addressability after restart).
	data, err := json.Marshal(map[string]string{"k": id})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["k"] != id {
		t.Fatalf("roundtrip broke: persisted %q != returned %q", m["k"], id)
	}

	// The node must be addressable by the returned id (delete path).
	dr := kgMustOK(t, tool, `{"action":"delete","id":"`+id+`"}`)
	if strings.Contains(dr.Content, "not found") {
		t.Fatalf("delete by returned id failed: %s", dr.Content)
	}
}

// TestZZ4452LinkFutureValidFromNoDuplicate pins bug 2.
func TestZZ4452LinkFutureValidFromNoDuplicate(t *testing.T) {
	tool := kgSetup(t)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	first := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to","valid_from":"`+future+`"}`)
	if strings.Contains(first.Content, "already exists") {
		t.Fatalf("first future-dated link should succeed, got: %s", first.Content)
	}

	// A second link with no valid_from must NOT append a parallel edge.
	second := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)
	if !strings.Contains(second.Content, "already exists") {
		t.Fatalf("second link over future-valid edge must be refused, got: %s", second.Content)
	}

	// Exactly one edge of that type may exist in the store.
	s, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range s.Edges {
		if e.From == "a" && e.To == "b" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 a->b edge, found %d", n)
	}
}

// TestZZ4452LanchatDedupNotDropped pins bug 3: two input identifiers that
// resolve to the same node must not be counted as "could not be resolved".
func TestZZ4452LanchatDedupNotDropped(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "ggcode")

	resolved, ambiguous, unresolved := tool.resolveRecipientsFull([]string{"node-a", "node-a"})
	if len(ambiguous) != 0 {
		t.Fatalf("unexpected ambiguity: %v", ambiguous)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 deduped node, got %v", resolved)
	}
	if len(unresolved) != 0 {
		t.Fatalf("deduped duplicate identifier misreported as unresolved: %v", unresolved)
	}
}

// TestZZ4452LanchatUnresolvedReported pins the honest reporting side of bug
// 3: an identifier that resolves to nothing must be surfaced.
func TestZZ4452LanchatUnresolvedReported(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "ggcode")

	_, _, unresolved := tool.resolveRecipientsFull([]string{"node-a", "no-such-peer"})
	if len(unresolved) != 1 || unresolved[0] != "no-such-peer" {
		t.Fatalf("expected [no-such-peer] unresolved, got %v", unresolved)
	}
}
