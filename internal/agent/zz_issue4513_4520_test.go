package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestSetSessionIDNilRegistrySafe (#4513): NewAgent documents nil-registry
// support, but SetSessionID called a.tools.Get unconditionally - a nil
// registry panicked. The guard makes nil mean "nothing to rebind".
func TestSetSessionIDNilRegistrySafe(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a := NewAgent(mp, nil, "", 1) // nil registry: the documented contract
	if a.tools != nil {
		t.Fatal("fixture must actually carry a nil registry")
	}
	a.SetSessionID("sess-4513") // must not panic
	if a.sessionID != "sess-4513" {
		t.Fatalf("sessionID must still be set, got %q", a.sessionID)
	}
	// And the non-nil path still binds.
	b := NewAgent(mp, tool.NewRegistry(), "", 1)
	b.SetSessionID("sess-b")
	if b.sessionID != "sess-b" {
		t.Fatalf("non-nil registry path regressed, got %q", b.sessionID)
	}
}

// TestToolEquivDetectWarnsOnLateDivergence (#4520): the core scenario this
// detector exists for - first N occurrences byte-identical (rawSeen=true,
// warning suppressed), occurrence N+1 diverges (reordered keys). With == the
// one comparison chance was consumed at N; the divergence at N+1 could never
// warn. >= keeps the gate open past the threshold.
func TestToolEquivDetectWarnsOnLateDivergence(t *testing.T) {
	s := newToolEquivDetectState()
	identical := `{"path":"/tmp/a.go","offset":1}`
	for i := 0; i < equivWarnThreshold; i++ {
		w := s.recordCall("read_file", []byte(identical), "raw-identical")
		if w != "" {
			t.Fatalf("byte-identical repeat %d must not warn (tool_redundancy owns it), got %q", i, w)
		}
	}
	// Diverge: identical payload PLUS a volatile timestamp key - the
	// normalized fingerprint matches (timestamp stripped for builtins),
	// but raw bytes differ so rawFp differs and rawSeen flips false.
	diverged := `{"timestamp":"2026-10-30T18:00:00Z","offset":1,"path":"/tmp/a.go"}`
	w := s.recordCall("read_file", []byte(diverged), "raw-diverged")
	if w == "" {
		t.Fatal("late divergence past the threshold must warn (#4520: == let this slip forever)")
	}
	if !strings.Contains(w, "read_file") {
		t.Fatalf("warning must name the tool, got %q", w)
	}
}

// TestToolEquivDetectEscalationReachable (#4640): #4520's >= on the soft
// warning made the old count==9 escalation branch unreachable - the
// stronger "wastes significant iteration budget" wording was dead code.
// With the per-fingerprint escalation latch it must fire once at >=9 even
// after the soft warning already fired at 3, and never twice.
func TestToolEquivDetectEscalationReachable(t *testing.T) {
	s := newToolEquivDetectState()
	escalated := 0
	for i := 1; i <= 12; i++ {
		// Same normalized fingerprint throughout; timestamp varies so raw
		// bytes differ (rawSeen false, this detector's jurisdiction).
		args := []byte(`{"timestamp":"2026-10-30T18:0` + string(rune('0'+i%10)) + `:00Z","offset":1,"path":"/tmp/a.go"}`)
		w := s.recordCall("read_file", args, fmt.Sprintf("raw-%d", i))
		if strings.Contains(w, "wastes significant iteration budget") {
			escalated++
			if i < equivWarnThreshold*3 {
				t.Fatalf("escalation fired too early at call %d", i)
			}
		}
	}
	if escalated != 1 {
		t.Fatalf("escalation must fire exactly once across calls 9-12, got %d", escalated)
	}
}
