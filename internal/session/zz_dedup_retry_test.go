package session

// User-reported: a session killed mid endless-retry loaded back with the
// same user prompt duplicated N times (the retry loop re-Add()s the
// prompt; each Add gets a fresh message ID so nothing dedups on write).
// Loading now collapses consecutive identical plain-text user turns in
// BOTH the render slice and the rebuilt LLM context.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestLoadDedupesRetryDuplicatedUserTurns(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "dup-retry"}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	userMsg := func(text string) provider.Message {
		return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	}
	assistantMsg := func(text string) provider.Message {
		return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	}
	// The killed endless-retry shape: the SAME prompt persisted 5 times
	// back-to-back (each with a distinct fresh ID on the write side).
	for i := 0; i < 5; i++ {
		if err := store.AppendMessageToDisk(ses, userMsg("please fix the build")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendMessageToDisk(ses, assistantMsg("attempting...")); err != nil {
		t.Fatal(err)
	}
	// A DIFFERENT consecutive user message must survive.
	if err := store.AppendMessageToDisk(ses, userMsg("and now run the tests")); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadWithOptions("dup-retry", true)
	if err != nil {
		t.Fatal(err)
	}
	var userTurns []string
	for _, m := range loaded.Messages {
		if m.Role != "user" {
			continue
		}
		userTurns = append(userTurns, m.Content[0].Text)
	}
	if len(userTurns) != 2 {
		t.Fatalf("5 retry-duplicated turns must collapse to 1 (+ the distinct one): got %v", userTurns)
	}
	if userTurns[0] != "please fix the build" || userTurns[1] != "and now run the tests" {
		t.Fatalf("wrong turns survived: %v", userTurns)
	}
	// Context rebuild dedups identically.
	ctxUsers := 0
	for _, m := range loaded.ContextMessages {
		if m.Role == "user" {
			ctxUsers++
		}
	}
	if ctxUsers != 2 {
		t.Fatalf("LLM context must also dedupe retry loops: got %d user turns", ctxUsers)
	}
}

func TestLoadKeepsDistinctAndNonConsecutiveUsers(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "no-dedup"}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	mk := mkUser2324
	append2324(t, store, ses, mk("first question"))
	append2324(t, store, ses, mk("second question")) // different: keep
	append2324(t, store, ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "answer"}}})
	append2324(t, store, ses, mk("first question")) // NON-consecutive repeat: keep

	loaded, err := store.LoadWithOptions("no-dedup", true)
	if err != nil {
		t.Fatal(err)
	}
	users := 0
	for _, m := range loaded.Messages {
		if m.Role == "user" {
			users++
		}
	}
	if users != 3 {
		t.Fatalf("distinct/non-consecutive user turns must all survive, got %d", users)
	}
}

func mkUser2324(text string) provider.Message {
	return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func append2324(t *testing.T, store *JSONLStore, ses *Session, msg provider.Message) {
	t.Helper()
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatal(err)
	}
}
