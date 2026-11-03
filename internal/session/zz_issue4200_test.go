package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func issue4200Msg(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func TestIssue4200_TombstoneRemovesOriginalFromLoad(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "s4200-a", Title: "issue4200"}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("user", "describe this [image-original]")); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("assistant", "vision attempt")); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUserMessageRemovalToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("user", "describe this [image omitted: no vision support]")); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("assistant", "text-only answer")); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range loaded.Messages {
		if m.Role == "user" && len(m.Content) > 0 && m.Content[0].Text == "describe this [image-original]" {
			t.Fatalf("resume replayed the removed original user turn: %+v", loaded.Messages)
		}
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("expected 3 surviving messages (assistant + projection user + assistant), got %d: %+v", len(loaded.Messages), loaded.Messages)
	}
	for _, m := range loaded.ContextMessages {
		if m.Role == "user" && len(m.Content) > 0 && m.Content[0].Text == "describe this [image-original]" {
			t.Fatalf("agent context still contains the removed original user turn: %+v", loaded.ContextMessages)
		}
	}
}

func TestIssue4200_TombstonePopsNewestUserOnly(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "s4200-b", Title: "issue4200"}
	for _, m := range []provider.Message{
		issue4200Msg("user", "earlier question"),
		issue4200Msg("assistant", "earlier answer"),
		issue4200Msg("user", "doomed image turn"),
		issue4200Msg("assistant", "vision error surface"),
	} {
		if err := store.AppendMessageToDisk(ses, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendUserMessageRemovalToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("user", "text-only retry")); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	var userTexts []string
	for _, m := range loaded.Messages {
		if m.Role == "user" {
			userTexts = append(userTexts, m.Content[0].Text)
		}
	}
	if len(userTexts) != 2 || userTexts[0] != "earlier question" || userTexts[1] != "text-only retry" {
		t.Fatalf("tombstone must remove only the newest user turn (doomed image turn), got %v", userTexts)
	}
}

func TestIssue4200_TombstoneNoUserAndDuplicateNoOp(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "s4200-c", Title: "issue4200"}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("assistant", "solo answer")); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUserMessageRemovalToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, issue4200Msg("user", "only user")); err != nil {
		t.Fatal(err)
	}

	if err := store.AppendUserMessageRemovalToDisk(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Role != "assistant" {
		t.Fatalf("expected only the solo assistant message after no-op tombstones, got %+v", loaded.Messages)
	}
}

func TestIssue4200_AppendRemovalNilSession(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUserMessageRemovalToDisk(nil); err != nil {
		t.Fatalf("nil session should be a no-op, got %v", err)
	}
}
