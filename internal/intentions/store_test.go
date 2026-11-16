package intentions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "intentions.json"), "sess-1")
}

func validIntention() Intention {
	return Intention{
		Statement:     "continue the release",
		CueFamily:     CueFile,
		CuePattern:    "deploy/state.txt",
		OriginSession: "sess-1",
		OriginExcerpt: "when CI is green, continue the release",
	}
}

func TestPutValidation(t *testing.T) {
	s := newTestStore(t)

	cases := []struct {
		name string
		mut  func(*Intention)
	}{
		{"missing statement", func(in *Intention) { in.Statement = "  " }},
		{"missing excerpt", func(in *Intention) { in.OriginExcerpt = "" }},
		{"missing pattern", func(in *Intention) { in.CuePattern = "" }},
		{"unknown family", func(in *Intention) { in.CueFamily = "smell" }},
		{"bad cron", func(in *Intention) { in.CueFamily = CueTime; in.CuePattern = "not a cron" }},
		{"bad glob", func(in *Intention) { in.CuePattern = "[" }},
	}
	for _, tc := range cases {
		in := validIntention()
		tc.mut(&in)
		if _, err := s.Put(in); err == nil {
			t.Fatalf("%s: expected validation error, got nil", tc.name)
		}
	}
}

func TestPutDedup(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Put(validIntention())
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.Put(validIntention()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
	// Retiring the first frees the key for re-capture.
	if err := s.Retire(first.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := s.Put(validIntention()); err != nil {
		t.Fatalf("re-put after retire: %v", err)
	}
}

func TestLifecycle(t *testing.T) {
	s := newTestStore(t)
	in, _ := s.Put(validIntention())

	// realize from pending
	if err := s.Realize(in.ID); err != nil {
		t.Fatalf("realize: %v", err)
	}
	got, _ := s.FindByID(in.ID)
	if got.State != StateRealized {
		t.Fatalf("state = %s, want realized", got.State)
	}
	// realize again is rejected (already terminal)
	if err := s.Realize(in.ID); err == nil {
		t.Fatal("double realize should fail")
	}

	// retire from pending
	in2, _ := s.Put(func() Intention { x := validIntention(); x.Statement = "second task"; return x }())
	if err := s.Retire(in2.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if err := s.Retire(in2.ID); err == nil {
		t.Fatal("double retire should fail")
	}
}

func TestListHidesTerminal(t *testing.T) {
	s := newTestStore(t)
	in1, _ := s.Put(validIntention())
	in2, _ := s.Put(func() Intention { x := validIntention(); x.Statement = "second"; return x }())
	_ = s.Realize(in2.ID)

	if got := len(s.List(false)); got != 1 {
		t.Fatalf("List(false) = %d items, want 1", got)
	}
	if got := len(s.List(true)); got != 2 {
		t.Fatalf("List(true) = %d items, want 2", got)
	}
	_ = in1
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intentions.json")
	s1 := NewStore(path, "sess-1")
	in, err := s1.Put(validIntention())
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("store file not written: %v", err)
	}
	s2 := NewStore(path, "")
	got, ok := s2.FindByID(in.ID)
	if !ok {
		t.Fatal("intention lost after reopen")
	}
	if got.Statement != in.Statement || got.OriginExcerpt != in.OriginExcerpt {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, in)
	}
	// Namespace marker written for isolation checks (#5260 vs session.instructions).
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "memory.intentions") {
		t.Fatal("store file missing memory.intentions namespace marker")
	}
}

func TestOriginSessionDefault(t *testing.T) {
	s := newTestStore(t) // defaultOriginSession = sess-1
	in := validIntention()
	in.OriginSession = ""
	stored, err := s.Put(in)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if stored.OriginSession != "sess-1" {
		t.Fatalf("origin session = %q, want default sess-1", stored.OriginSession)
	}
}

func TestNilStoreSafety(t *testing.T) {
	var s *Store
	if got := s.List(false); got != nil {
		t.Fatalf("nil store List = %v, want nil", got)
	}
	if _, ok := s.FindByID("x"); ok {
		t.Fatal("nil store FindByID should not find")
	}
	if err := s.Realize("x"); err == nil {
		t.Fatal("nil store realize should error")
	}
}
