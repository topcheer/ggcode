package session

import (
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Persistent teams (#5829): teammate journals (IDs carrying the "-tm-"
// infix) must never surface in user session listers or latest-resume
// selection.
func TestTeammateJournalsFilteredFromListers(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	user := &Session{ID: "20261119-120000-aaaa1111", Workspace: filepath.Join(dir, "ws")}
	if err := store.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := store.AppendMessage(user, testTextMessage("user", "hello")); err != nil {
		t.Fatalf("append user: %v", err)
	}

	mate := &Session{ID: "20261119-120001-bbbb2222-tm-team-ab12cd-alice", Workspace: filepath.Join(dir, "ws")}
	if err := store.Save(mate); err != nil {
		t.Fatalf("save teammate journal: %v", err)
	}
	if err := store.AppendMessage(mate, testTextMessage("user", "teammate work")); err != nil {
		t.Fatalf("append teammate: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, s := range list {
		if isTeammateJournalID(s.ID) {
			t.Fatalf("List() surfaced teammate journal %q", s.ID)
		}
	}

	wsList, err := store.ListForWorkspace(user.Workspace)
	if err != nil {
		t.Fatalf("listForWorkspace: %v", err)
	}
	if len(wsList) != 1 || wsList[0].ID != user.ID {
		t.Fatalf("ListForWorkspace = %+v, want only the user session", wsList)
	}

	latest, err := store.LatestForWorkspace(user.Workspace)
	if err != nil {
		t.Fatalf("latestForWorkspace: %v", err)
	}
	if latest == nil || latest.ID != user.ID {
		t.Fatalf("LatestForWorkspace = %+v, want the user session", latest)
	}
}

func testTextMessage(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}
