package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

func newResolveTestStore(t *testing.T, sessions ...*session.Session) session.Store {
	t.Helper()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	for _, s := range sessions {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s): %v", s.ID, err)
		}
	}
	return store
}

func TestResolveSessionIDExact(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "first"},
		&session.Session{ID: "20260728-110000-eeeeffff00001111", Title: "second"},
	)
	id, err := resolveSessionID(store, "20260728-100000-aaaabbbbccccdddd")
	if err != nil {
		t.Fatalf("resolve exact: %v", err)
	}
	if id != "20260728-100000-aaaabbbbccccdddd" {
		t.Fatalf("got %q", id)
	}
}

func TestResolveSessionIDUniquePrefix(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "first"},
		&session.Session{ID: "20260727-110000-eeeeffff00001111", Title: "second"},
	)
	id, err := resolveSessionID(store, "20260728")
	if err != nil {
		t.Fatalf("resolve prefix: %v", err)
	}
	if id != "20260728-100000-aaaabbbbccccdddd" {
		t.Fatalf("got %q", id)
	}
}

func TestResolveSessionIDUniqueSubstring(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "first"},
		&session.Session{ID: "20260727-110000-eeeeffff00001111", Title: "second"},
	)
	id, err := resolveSessionID(store, "eeeeffff")
	if err != nil {
		t.Fatalf("resolve substring: %v", err)
	}
	if id != "20260727-110000-eeeeffff00001111" {
		t.Fatalf("got %q", id)
	}
}

func TestResolveSessionIDTitleSubstring(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "Fix login bug"},
		&session.Session{ID: "20260727-110000-eeeeffff00001111", Title: "Refactor store"},
	)
	id, err := resolveSessionID(store, "login")
	if err != nil {
		t.Fatalf("resolve title substring: %v", err)
	}
	if id != "20260728-100000-aaaabbbbccccdddd" {
		t.Fatalf("got %q", id)
	}
}

func TestResolveSessionIDAmbiguous(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "first"},
		&session.Session{ID: "20260728-110000-eeeeffff00001111", Title: "second"},
	)
	_, err := resolveSessionID(store, "20260728")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	// Error should list candidate IDs so the user can pick one.
	if !strings.Contains(err.Error(), "aaaabbbb") || !strings.Contains(err.Error(), "eeeeffff") {
		t.Fatalf("error should list candidates, got %v", err)
	}
}

func TestResolveSessionIDNoMatch(t *testing.T) {
	store := newResolveTestStore(t,
		&session.Session{ID: "20260728-100000-aaaabbbbccccdddd", Title: "first"},
	)
	_, err := resolveSessionID(store, "zzzz")
	if err == nil {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(err.Error(), "no session matching") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSessionIDExactWinsOverAmbiguousPrefix(t *testing.T) {
	// An exact ID must resolve even if it is also a prefix of another ID.
	store := newResolveTestStore(t,
		&session.Session{ID: "abcd", Title: "short"},
		&session.Session{ID: "abcdef", Title: "long"},
	)
	id, err := resolveSessionID(store, "abcd")
	if err != nil {
		t.Fatalf("resolve exact-over-prefix: %v", err)
	}
	if id != "abcd" {
		t.Fatalf("got %q", id)
	}
}
