package tui

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

	m.openSearchPanel("deploy")
	gen2 := m.inspectorPanel.loadSeq
	if gen2 <= gen1 {
		t.Fatalf("second search must bump loadSeq: gen1=%d gen2=%d", gen1, gen2)
	}

	before := m.inspectorPanel.cachedItems
	updated, _ := m.Update(inspectorItemsLoadedMsg{kind: inspectorPanelSearch, seq: gen1, items: []inspectorPanelItem{{ID: "old", Title: "stale"}}})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if len(m2.inspectorPanel.cachedItems) != len(before) {
		t.Fatalf("stale search generation must be dropped, got %v", m2.inspectorPanel.cachedItems)
	}

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
