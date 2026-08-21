package tool

// Guard tests for the #839-#852 fix round. Each test pins the new semantics
// so a regression reintroduces a visible failure instead of silent drift.

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// TestKGPartialUpdatePreservesContent (#844): an add/update on an existing
// id carrying only status must not wipe accumulated Content/Title.
func TestKGPartialUpdatePreservesContent(t *testing.T) {
	tk := &KnowledgeGraphTool{}
	s := &kgStore{Nodes: map[string]*kgNode{
		"n1": {ID: "n1", Type: "decision", Title: "Old Title",
			Content: "Long accumulated content", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}}
	_, err := tk.doAdd(s, &kgParams{Action: "add", ID: "n1", Type: "decision",
		Title: "Updated Title", Content: "", Status: "superseded"})
	if err != nil {
		t.Fatalf("doAdd error: %v", err)
	}
	n := s.Nodes["n1"]
	if n.Content != "Long accumulated content" {
		t.Fatalf("content wiped by partial update: %q", n.Content)
	}
	if n.Title != "Updated Title" {
		t.Fatalf("provided title not applied: %q", n.Title)
	}
	if n.Status != "superseded" {
		t.Fatalf("status not applied: %q", n.Status)
	}
}

// TestKGRuneSafePreview (#850): 200-rune CJK preview must not split bytes.
func TestKGRuneSafePreview(t *testing.T) {
	cjk := strings.Repeat("世", 250) // 750 bytes, 250 runes
	n := &kgNode{Type: "note", Title: "t", Content: cjk}
	var sb strings.Builder
	formatNodeShort(&sb, n)
	out := strings.TrimRight(sb.String(), "\n")
	if got := strings.Count(out, "世"); got > 200 {
		t.Fatalf("preview not truncated to 200 runes: %d runes", got)
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("preview missing ellipsis suffix: %q", out[len(out)-10:])
	}
}

// TestKittyWindowLabelFocused (#852): omitted window_id shows "(focused)"
// instead of "<nil>".
func TestKittyWindowLabelFocused(t *testing.T) {
	if got := kittyWindowLabel(map[string]any{}); got != "window (focused)" {
		t.Fatalf("empty window_id label = %q", got)
	}
	if got := kittyWindowLabel(map[string]any{"window_id": float64(7)}); got != "window 7" {
		t.Fatalf("explicit window_id label = %q", got)
	}
}

// TestBroadcastRoleHumanDefault (#845): human-originated broadcasts must
// target RoleHuman; only as_agent flips to RoleAgent. Pin that the two
// roles are distinct so the branch stays meaningful.
func TestBroadcastRoleHumanDefault(t *testing.T) {
	if lanchat.RoleHuman == lanchat.RoleAgent {
		t.Fatal("RoleHuman and RoleAgent collapsed - broadcast targeting broken")
	}
}
