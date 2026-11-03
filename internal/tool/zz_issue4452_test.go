package tool

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestZZ4452IDTruncationRuneBoundary(t *testing.T) {
	tool, _ := newKGTool(t)

	longID := strings.Repeat("节", 40)
	r := kgMustOK(t, tool, `{"action":"add","id":"`+longID+`","type":"entity","title":"long id node"}`)

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

	dr := kgMustOK(t, tool, `{"action":"delete","id":"`+id+`"}`)
	if strings.Contains(dr.Content, "not found") {
		t.Fatalf("delete by returned id failed: %s", dr.Content)
	}
}

func TestZZ4452LinkFutureValidFromNoDuplicate(t *testing.T) {
	tool := kgSetup(t)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	first := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to","valid_from":"`+future+`"}`)
	if strings.Contains(first.Content, "already exists") {
		t.Fatalf("first future-dated link should succeed, got: %s", first.Content)
	}

	second := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)
	if !strings.Contains(second.Content, "already exists") {
		t.Fatalf("second link over future-valid edge must be refused, got: %s", second.Content)
	}

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

func TestZZ4452LanchatUnresolvedReported(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "ggcode")

	_, _, unresolved := tool.resolveRecipientsFull([]string{"node-a", "no-such-peer"})
	if len(unresolved) != 1 || unresolved[0] != "no-such-peer" {
		t.Fatalf("expected [no-such-peer] unresolved, got %v", unresolved)
	}
}
