package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleInspectorItemsLoaded(msg inspectorItemsLoadedMsg) (Model, tea.Cmd) {
	if m.inspectorPanel != nil && m.inspectorPanel.kind == msg.kind &&
		msg.seq == m.inspectorPanel.loadSeq {

		if msg.loadErr != nil {

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
