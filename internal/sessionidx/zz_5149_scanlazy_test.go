package sessionidx

import (
	"testing"
)

// #5149 scanlazy pins: plain-instance startup must NOT eagerly decode
// sibling indexes (pprof-verified 276MB live at boot before first user
// interaction); decode happens on first query via decodeInto.
func TestScanExternal_PlainLazy(t *testing.T) {
	dir := t.TempDir()
	const live = "scanlive"
	seedSelfCapSession(t, dir, live, 5)
	seedSelfCapSession(t, dir, "sib1", 5)
	seedSelfCapSession(t, dir, "sib2", 5)
	// Materialize sibling idx files (owner-style refresh).
	for _, id := range []string{"sib1", "sib2"} {
		ix, err := Load(dir, id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if err := ix.Refresh(); err != nil {
			t.Fatalf("refresh %s: %v", id, err)
		}
	}

	m := NewManager(dir, live)
	m.Start()
	defer m.Stop()

	// Startup: entries recorded, NOTHING decoded.
	m.mu.Lock()
	n := len(m.external)
	decoded := 0
	for _, ent := range m.external {
		if ent.decoded {
			decoded++
		}
	}
	m.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 external entries, got %d", n)
	}
	if decoded != 0 {
		t.Fatalf("startup decoded %d siblings - scanlazy violated", decoded)
	}

	// First query across sessions: lazy decode engages, results correct.
	res := m.Query([]string{"unicorns"}, "", 10)
	if len(res) == 0 {
		t.Fatal("lazy query must still find sibling hits")
	}
	m.mu.Lock()
	decoded = 0
	for _, ent := range m.external {
		if ent.decoded {
			decoded++
		}
	}
	m.mu.Unlock()
	if decoded == 0 {
		t.Fatal("query must decode hit siblings lazily")
	}

	// Targeted sibling query also works post-lazy.
	if res := m.Query([]string{"unicorns"}, "sib1", 10); len(res) == 0 {
		t.Fatal("targeted sibling query failed")
	}
}
