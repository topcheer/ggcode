package session

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

func TestLoadKeepsDistinctUsers(t *testing.T) {
	store, ses := newStoreSession2324(t, "distinct")
	append2324(t, store, ses, mkUser2324("question A"))
	append2324(t, store, ses, mkUser2324("question A?"))
	append2324(t, store, ses, mkAssistant2324("answers"))
	append2324(t, store, ses, mkUser2324("question A"))
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
