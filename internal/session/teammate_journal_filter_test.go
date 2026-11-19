package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// Persistent teams (#5829): teammate journals (IDs carrying the "-tm-"
// infix) must never surface in user session listers or latest-resume
// selection.
//
// The store dir uses a retrying cleanup instead of t.TempDir's single-shot
// RemoveAll: on GH Actions' overlay filesystem, RemoveAll occasionally
// races with the kernel's directory listing (stale readdir → "directory
// not empty" on an already-removed entry), which fails the test even
// though every assertion passed (observed on main CI runs 44622469553).
// The store itself is fully synchronous (no background writers), so a
// bounded retry is strictly a filesystem-quirk accommodation.
func TestTeammateJournalsFilteredFromListers(t *testing.T) {
	dir := mustTempDirWithRetryCleanup(t)
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

// mustTempDirWithRetryCleanup returns a fresh temp dir whose t.Cleanup
// retries RemoveAll with a short backoff (3 attempts, 100ms apart) to
// absorb overlayfs stale-readdir races on CI runners.
func mustTempDirWithRetryCleanup(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tmjournal-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		for i := 0; i < 3; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			} else if i == 2 {
				t.Logf("RemoveAll(%s) still failing after retries: %v", dir, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}
