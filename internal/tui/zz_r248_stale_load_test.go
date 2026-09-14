package tui

import (
	"testing"
)

// R248: a late async load from a PREVIOUS generation (A-key toggled
// allWorkspaces and reloaded while the old goroutine was still in
// List()) must be dropped, not cached over the newer generation's items.

func TestR248_StaleGenerationDropped(t *testing.T) {
	m := newTestModel()
	m.inspectorPanel = &inspectorPanelState{kind: inspectorPanelSessions, loadSeq: 2, itemsLoaded: true, loading: true}
	fresh := []inspectorPanelItem{{ID: "new", Title: "fresh"}}

	// seq 1 arrives AFTER the panel already moved to generation 2.
	updated, cmd := m.Update(inspectorItemsLoadedMsg{kind: inspectorPanelSessions, seq: 1, items: []inspectorPanelItem{{ID: "old", Title: "stale"}}})
	_ = cmd
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if m2.inspectorPanel.cachedItems != nil {
		t.Fatalf("stale generation must be dropped, got %v", m2.inspectorPanel.cachedItems)
	}
	if !m2.inspectorPanel.loading {
		t.Fatal("stale drop must leave loading=true for the in-flight generation")
	}

	// The matching generation lands.
	updated, cmd = m2.Update(inspectorItemsLoadedMsg{kind: inspectorPanelSessions, seq: 2, items: fresh})
	_ = cmd
	m3, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if len(m3.inspectorPanel.cachedItems) != 1 || m3.inspectorPanel.cachedItems[0].ID != "new" {
		t.Fatalf("current generation must land, got %v", m3.inspectorPanel.cachedItems)
	}
	if m3.inspectorPanel.loading {
		t.Fatal("current generation landing must clear loading")
	}
}
