package agentruntime

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func handoffTestSession(t *testing.T, store *session.JSONLStore, id, workspace string, msgs []provider.Message) *session.Session {
	t.Helper()
	ses := &session.Session{
		ID:        id,
		Title:     "title " + id,
		Workspace: workspace,
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, m := range msgs {
		if err := store.AppendMessage(ses, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}
	return ses
}

func humanMsg(text string) provider.Message {
	return provider.Message{Role: "user", Origin: "human", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func assistantMsg(text string) provider.Message {
	return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

// TestShiftHandoffBriefSameWorkspace pins the happy path: a fresh session in
// a workspace with a recent prior session gets a brief carrying goal + last
// state from that prior session.
func TestShiftHandoffBriefSameWorkspace(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ws := "/tmp/handoff-ws-a"
	handoffTestSession(t, store, "sess-a1", ws, []provider.Message{
		humanMsg("fix the login bug in user-service"),
		assistantMsg("root cause identified: stale token cache; fix drafted"),
	})
	brief := BuildShiftHandoffBrief(store, ws, "", DefaultShiftHandoffWindow)
	if brief == "" {
		t.Fatal("want non-empty brief")
	}
	for _, want := range []string{"Previous Session Handoff", "fix the login bug in user-service", "stale token cache", "title sess-a1"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

// TestShiftHandoffBriefOtherWorkspace pins workspace isolation: a session
// recorded under another workspace must not leak into this workspace's brief.
func TestShiftHandoffBriefOtherWorkspace(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	handoffTestSession(t, store, "sess-b1", "/tmp/handoff-ws-other", []provider.Message{
		humanMsg("unrelated workspace work"),
	})
	if got := BuildShiftHandoffBrief(store, "/tmp/handoff-ws-main", "", DefaultShiftHandoffWindow); got != "" {
		t.Fatalf("want empty brief for other workspace, got:\n%s", got)
	}
}

// TestShiftHandoffBriefStale pins the age window: with a maxAge smaller than
// the entry's age the scan stops immediately (sorted newest-first).
func TestShiftHandoffBriefStale(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ws := "/tmp/handoff-ws-c"
	handoffTestSession(t, store, "sess-c1", ws, []provider.Message{
		humanMsg("recent work"),
	})
	if got := BuildShiftHandoffBrief(store, ws, "", time.Nanosecond); got != "" {
		t.Fatalf("want empty brief past maxAge, got:\n%s", got)
	}
}

// TestShiftHandoffBriefSkipsCurrent pins skipSessionID: resuming the newest
// session must surface the one BEFORE it, not the resumed session itself.
func TestShiftHandoffBriefSkipsCurrent(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ws := "/tmp/handoff-ws-d"
	handoffTestSession(t, store, "sess-d1", ws, []provider.Message{
		humanMsg("older task one"),
	})
	handoffTestSession(t, store, "sess-d2", ws, []provider.Message{
		humanMsg("newer task two"),
	})
	brief := BuildShiftHandoffBrief(store, ws, "sess-d2", DefaultShiftHandoffWindow)
	if !strings.Contains(brief, "older task one") {
		t.Fatalf("brief should come from the older session:\n%s", brief)
	}
	if strings.Contains(brief, "newer task two") {
		t.Fatalf("brief must not contain the skipped session:\n%s", brief)
	}
}

// TestShiftHandoffBriefCapped pins the rune budget: oversized goal text is
// truncated and the brief never exceeds shiftHandoffMaxRunes.
func TestShiftHandoffBriefCapped(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ws := "/tmp/handoff-ws-e"
	handoffTestSession(t, store, "sess-e1", ws, []provider.Message{
		humanMsg(strings.Repeat("g", 2000)),
		assistantMsg(strings.Repeat("s", 2000)),
	})
	brief := BuildShiftHandoffBrief(store, ws, "", DefaultShiftHandoffWindow)
	if utf8.RuneCountInString(brief) > 800 {
		t.Fatalf("brief exceeds cap: %d runes", utf8.RuneCountInString(brief))
	}
	if !strings.Contains(brief, "\u2026") {
		t.Fatalf("want truncated tail marker in:\n%s", brief)
	}
}
