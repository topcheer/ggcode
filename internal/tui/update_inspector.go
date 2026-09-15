package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleInspectorItemsLoaded caches async-loaded session items for the
// inspector panel (#2357 slice 5: extracted from Model.Update - body moved
// verbatim, zero behavior change). The seq guard drops late results from a
// PREVIOUS load; the error branch mirrors the sync path's error state.
func (m *Model) handleInspectorItemsLoaded(msg inspectorItemsLoadedMsg) (Model, tea.Cmd) {
	if m.inspectorPanel != nil && m.inspectorPanel.kind == msg.kind &&
		msg.seq == m.inspectorPanel.loadSeq {
		// R248: seq guard - a late result from a PREVIOUS load (A-key
		// toggled allWorkspaces and reloaded while the old goroutine was
		// still in List()) must not overwrite the newer generation's items.
		// The nil/kind guard below stays for panel-closed/kind-switched drops.
		// #1737 case 3: loadErr was sent but never consumed - a failed
		// load showed "no sessions yet" instead of the error, sending
		// the user down the wrong path (e.g. creating a session over a
		// transient read failure). Mirror the sync path's error state.
		if msg.loadErr != nil {
			// Mirror the sync path exactly (L92): an error item, not an
			// empty list.
			m.inspectorPanel.cachedItems = []inspectorPanelItem{{
				Title:    inspectorText(m.currentLanguage(), "sessions_error"),
				Detail:   msg.loadErr.Error(),
				Disabled: true,
			}}
			m.inspectorPanel.itemsLoaded = true
			m.inspectorPanel.loading = false
			return *m, nil
		}
		m.inspectorPanel.cachedItems = msg.items
		m.inspectorPanel.itemsLoaded = true
		m.inspectorPanel.loading = false
	}
	return *m, nil
}
