package tui

// #3383 probes: consecutive /search opens must not let a slower earlier
// query overwrite a newer result. openSearchPanel now stamps loadSeq++ and
// carries it, so the consumer's R248 seq guard drops the stale generation
// instead of matching it by the pre-fix seq=0 coincidence.

import (
	"testing"
)

func TestIssue3383_SearchSeqStampsGeneration(t *testing.T) {
	m := newTestModel()
	m.sessionStore = newTestSessionStore(t)
	m.openSearchPanel("refactor")
	if m.inspectorPanel == nil || m.inspectorPanel.kind != inspectorPanelSearch {
		t.Fatal("openSearchPanel must open the search panel")
	}
	gen1 := m.inspectorPanel.loadSeq

	// A second search bumps the generation before the first goroutine lands.
	m.openSearchPanel("deploy")
	gen2 := m.inspectorPanel.loadSeq
	if gen2 <= gen1 {
		t.Fatalf("second search must bump loadSeq: gen1=%d gen2=%d", gen1, gen2)
	}

	// The SLOWER first query arrives after generation 2 opened and (in the
	// headless sync fallback) already landed its result: the stale result
	// must NOT change what the panel shows (pre-fix both sent seq=0 and the
	// stale overwrite won).
	before := m.inspectorPanel.cachedItems
	updated, _ := m.Update(inspectorItemsLoadedMsg{kind: inspectorPanelSearch, seq: gen1, items: []inspectorPanelItem{{ID: "old", Title: "stale"}}})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if len(m2.inspectorPanel.cachedItems) != len(before) {
		t.Fatalf("stale search generation must be dropped, got %v", m2.inspectorPanel.cachedItems)
	}

	// The current generation lands.
	updated, _ = m2.Update(inspectorItemsLoadedMsg{kind: inspectorPanelSearch, seq: gen2, items: []inspectorPanelItem{{ID: "new", Title: "fresh"}}})
	m3, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if len(m3.inspectorPanel.cachedItems) != 1 || m3.inspectorPanel.cachedItems[0].ID != "new" {
		t.Fatalf("current search generation must land, got %v", m3.inspectorPanel.cachedItems)
	}
	if m3.inspectorPanel.loading {
		t.Fatal("current generation landing must clear loading")
	}
}
