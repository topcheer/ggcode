package session

// User-reported (confirmed against a real 566MB victim file): a session
// killed after an endless autopilot/retry loop loads back with the same
// user prompt up to 859 times. The dominant shape is NON-consecutive -
// the strategist budget notice and the autopilot cron prompt re-fire
// every cycle, separated by assistant runs - so only GLOBAL byte-exact
// identity dedup fixes the display AND the LLM context rebuild.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func mkUser2324(text string) provider.Message {
	return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func mkAssistant2324(text string) provider.Message {
	return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func append2324(t *testing.T, store *JSONLStore, ses *Session, msg provider.Message) {
	t.Helper()
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatal(err)
	}
}

func userTurnTexts(msgs []provider.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

// The real shape: the SAME prompt re-fired N times, each separated by
// assistant runs (autopilot cron / strategist loop). All copies must
// collapse to ONE in both render and context.
func TestLoadDedupesNonConsecutiveIdenticalUserTurns(t *testing.T) {
	store, ses := newStoreSession2324(t, "autopilot-victim")
	for round := 0; round < 4; round++ {
		append2324(t, store, ses, mkUser2324("自动推进 GGTerm 终端模拟器开发。不限方向。"))
		append2324(t, store, ses, mkAssistant2324("working on it..."))
		append2324(t, store, ses, mkUser2324("Strategist guidance budget is exhausted. Review the original goal."))
		append2324(t, store, ses, mkAssistant2324("retrying with fresh plan"))
	}
	loaded, err := store.LoadWithOptions("autopilot-victim", true)
	if err != nil {
		t.Fatal(err)
	}
	got := userTurnTexts(loaded.Messages)
	if len(got) != 2 {
		t.Fatalf("4+4 interleaved identical turns must collapse to 2 distinct, got %d: %v", len(got), got)
	}
	if got[0] != "自动推进 GGTerm 终端模拟器开发。不限方向。" {
		t.Fatalf("first kept turn wrong: %q", got[0])
	}
	if ctxGot := userTurnTexts(loaded.ContextMessages); len(ctxGot) != 2 {
		t.Fatalf("LLM context must dedupe identically, got %d user turns", len(ctxGot))
	}
}

// Distinct texts never collapse (one byte apart is a different turn).
func TestLoadKeepsDistinctUsers(t *testing.T) {
	store, ses := newStoreSession2324(t, "distinct")
	append2324(t, store, ses, mkUser2324("question A"))
	append2324(t, store, ses, mkUser2324("question A?")) // 1 byte off: keep
	append2324(t, store, ses, mkAssistant2324("answers"))
	append2324(t, store, ses, mkUser2324("question A")) // identical to FIRST: drop
	loaded, err := store.LoadWithOptions("distinct", true)
	if err != nil {
		t.Fatal(err)
	}
	got := userTurnTexts(loaded.Messages)
	if len(got) != 2 {
		t.Fatalf("A and A? survive, the A copy drops: got %v", got)
	}
}

func newStoreSession2324(t *testing.T, id string) (*JSONLStore, *Session) {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: id}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	return store, ses
}
