package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/sessionidx"
)

func newTestRecallEnv(t *testing.T, liveID string) (SessionRecall, *sessionidx.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	// one live + one sibling session with distinct content
	for _, c := range []struct{ id, role, text string }{
		{"live", "user", "uniqueterm alpha decision in live session"},
		{"sibling", "assistant", "uniqueterm beta decision in sibling session"},
	} {
		var b string
		b += `{"type":"message","timestamp":"2026-11-04T10:00:00Z","message":{"role":"` + c.role + `","content":[{"type":"text","text":"` + c.text + `"}]}}` + "\n"
		os.WriteFile(filepath.Join(dir, c.id+".jsonl"), []byte(b), 0o600)
	}
	// sibling's owning instance built its idx
	oix, _ := sessionidx.Load(dir, "sibling")
	oix.Refresh()

	m := sessionidx.NewManager(dir, liveID)
	t.Cleanup(m.Stop)
	m.Start()
	reg := NewRegistry()
	reg.SetSessionIndex(m)
	return SessionRecall{Registry: reg}, m, dir
}

func TestSessionRecallLazyManagerAndQuery(t *testing.T) {
	tr, _, _ := newTestRecallEnv(t, "live")

	out, err := tr.Execute(context.Background(), json.RawMessage(`{"query":"uniqueterm alpha","description":"t"}`))
	if err != nil || out.IsError {
		t.Fatalf("execute failed: err=%v isError=%v", err, out.IsError)
	}
	if !strings.Contains(out.Content, "alpha") {
		t.Fatalf("expected live hit, got: %.200s", out.Content)
	}
}

func TestSessionRecallRoleFilter(t *testing.T) {
	tr, _, _ := newTestRecallEnv(t, "live")

	// sibling hit is assistant-role; filter user must exclude it
	out, _ := tr.Execute(context.Background(), json.RawMessage(`{"query":"uniqueterm beta","role":"user","description":"t"}`))
	if strings.Contains(out.Content, "session sib") || strings.Contains(out.Content, "beta decision") {
		t.Fatalf("role filter failed to exclude sibling assistant hit: %.200s", out.Content)
	}
}

func TestSessionRecallNoManager(t *testing.T) {
	tr := SessionRecall{} // no manager, no registry
	out, err := tr.Execute(context.Background(), json.RawMessage(`{"query":"x","description":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsError {
		t.Fatal("expected error result when manager unavailable")
	}
}

func TestSessionRecallNoMatch(t *testing.T) {
	tr, _, _ := newTestRecallEnv(t, "live")
	out, err := tr.Execute(context.Background(), json.RawMessage(`{"query":"zzznotfoundzzz","description":"t"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v %v", err, out.IsError)
	}
	if !strings.Contains(out.Content, "no matches") {
		t.Fatalf("expected no-matches notice, got: %.200s", out.Content)
	}
}
