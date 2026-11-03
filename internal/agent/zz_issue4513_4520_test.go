package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestSetSessionIDNilRegistrySafe(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a := NewAgent(mp, nil, "", 1)
	if a.tools != nil {
		t.Fatal("fixture must actually carry a nil registry")
	}
	a.SetSessionID("sess-4513")
	if a.sessionID != "sess-4513" {
		t.Fatalf("sessionID must still be set, got %q", a.sessionID)
	}

	b := NewAgent(mp, tool.NewRegistry(), "", 1)
	b.SetSessionID("sess-b")
	if b.sessionID != "sess-b" {
		t.Fatalf("non-nil registry path regressed, got %q", b.sessionID)
	}
}

func TestToolEquivDetectWarnsOnLateDivergence(t *testing.T) {
	s := newToolEquivDetectState()
	identical := `{"path":"/tmp/a.go","offset":1}`
	for i := 0; i < equivWarnThreshold; i++ {
		w := s.recordCall("read_file", []byte(identical), "raw-identical")
		if w != "" {
			t.Fatalf("byte-identical repeat %d must not warn (tool_redundancy owns it), got %q", i, w)
		}
	}

	diverged := `{"timestamp":"2026-10-30T18:00:00Z","offset":1,"path":"/tmp/a.go"}`
	w := s.recordCall("read_file", []byte(diverged), "raw-diverged")
	if w == "" {
		t.Fatal("late divergence past the threshold must warn (#4520: == let this slip forever)")
	}
	if !strings.Contains(w, "read_file") {
		t.Fatalf("warning must name the tool, got %q", w)
	}
}

func TestToolEquivDetectEscalationReachable(t *testing.T) {
	s := newToolEquivDetectState()
	escalated := 0
	for i := 1; i <= 12; i++ {

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
