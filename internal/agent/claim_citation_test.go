package agent

// sa-114 acceptance tests: claim-level evidence citation for the final
// assistant answer (deterministic /evidence backend).

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func citeMsgs() []provider.Message {
	// Tool call turn: read_file on agent.go, result contains the anchor.
	toolTurn := provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "reading the file"},
		{Type: "tool_use", ToolID: "tu-1", ToolName: "read_file", Input: []byte(`{"path":"/repo/internal/agent/agent.go"}`)},
	}}
	toolRes := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "tu-1", Output: "42:\tfunc (a *Agent) streamChatResponse() error {\n43:\t\t// handles the streaming loop"},
	}}
	answer := provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "The root cause is in internal/agent/agent.go:42 - `streamChatResponse` mishandles the empty stream. Fixed."},
	}}
	return []provider.Message{toolTurn, toolRes, answer}
}

// Acceptance 1: a claim anchor backed by a recorded tool result yields a
// citation linking answer excerpt to tool evidence.
func TestEvidenceCitations_AnchoredClaim(t *testing.T) {
	cites := EvidenceCitations(citeMsgs())
	if len(cites) == 0 {
		t.Fatal("expected citations for anchored claims, got none")
	}
	found := false
	for _, c := range cites {
		if strings.Contains(c.Anchor, "agent.go:42") || c.Anchor == "streamChatResponse" {
			found = true
			if c.Tool != "read_file" {
				t.Errorf("anchor %s must cite read_file, got %s", c.Anchor, c.Tool)
			}
			if !strings.Contains(c.Evidence, "streamChatResponse") {
				t.Errorf("evidence excerpt must contain the anchor occurrence, got %q", c.Evidence)
			}
			if !strings.Contains(c.Claim, c.Anchor) {
				t.Errorf("claim excerpt should contain the anchor %q, got %q", c.Anchor, c.Claim)
			}
		}
	}
	if !found {
		t.Fatalf("no citation for agent.go:42 / streamChatResponse: %+v", cites)
	}
}

// Acceptance 2: anchors with no recorded backing produce no citation
// (and no panic) - absence is the signal.
func TestEvidenceCitations_UnsupportedAnchorOmitted(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "tu-9", ToolName: "grep", Input: []byte(`{"pattern":"unrelated"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tu-9", Output: "no matches here"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "The bug lives in internal/agent/missing.go:7 and nowhere else."},
		}},
	}
	cites := EvidenceCitations(msgs)
	for _, c := range cites {
		if strings.Contains(c.Anchor, "missing.go:7") {
			t.Fatalf("unsupported anchor must not be cited: %+v", c)
		}
	}
}

// Acceptance 3: no final answer yet (last assistant message carries tool
// calls) → nil.
func TestEvidenceCitations_NoFinalAnswer(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "tu-2", ToolName: "read_file", Input: []byte(`{"path":"x.go"}`)},
		}},
	}
	if cites := EvidenceCitations(msgs); cites != nil {
		t.Fatalf("expected nil without a final answer, got %+v", cites)
	}
}

// Acceptance 4: nearest evidence wins - two read_file turns both contain
// the anchor; the later one is cited.
func TestEvidenceCitations_NearestEvidenceWins(t *testing.T) {
	mk := func(id, out string) []provider.Message {
		return []provider.Message{
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "tool_use", ToolID: id + "a", ToolName: "read_file", Input: []byte(`{"path":"/repo/a.go"}`)},
			}},
			{Role: "user", Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: id + "a", Output: out},
			}},
		}
	}
	msgs := append(mk("t1-", "OLD: shared_symbol here"), mk("t2-", "NEW: shared_symbol here")...)
	msgs = append(msgs, provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "Confirmed `shared_symbol` exists."},
	}})
	cites := EvidenceCitations(msgs)
	if len(cites) != 1 {
		t.Fatalf("expected 1 citation, got %d: %+v", len(cites), cites)
	}
	if !strings.Contains(cites[0].Evidence, "NEW:") {
		t.Fatalf("nearest (latest) evidence must win, got %q", cites[0].Evidence)
	}
}

// Acceptance 5: plain quoted words are not anchors; empty conversation is
// safe; formatter renders the linked list.
func TestEvidenceCitations_Guardrails(t *testing.T) {
	if cites := EvidenceCitations(nil); cites != nil {
		t.Fatal("nil msgs must yield nil")
	}
	// Backticked plain word (no code shape) must not anchor.
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "The `problem` is obvious; see internal/agent/x.go:1 for detail."},
		}},
	}
	if cites := EvidenceCitations(msgs); len(cites) != 0 {
		t.Fatalf("plain word must not anchor, got %+v", cites)
	}
	if s := FormatCitations(nil); s != "" {
		t.Fatalf("empty format must be empty, got %q", s)
	}
	cites := EvidenceCitations(citeMsgs())
	if s := FormatCitations(cites); !strings.Contains(s, "← read_file") {
		t.Fatalf("formatter must render tool attribution, got %q", s)
	}
}
