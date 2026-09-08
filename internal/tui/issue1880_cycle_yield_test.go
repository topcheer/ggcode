package tui

import "testing"

// Regression for #1880 case 1 (residual of #1772): the cycle-session keys
// (alt+up/alt+down) must yield to EVERY panel the dispatch chain handles,
// not just the nine states the old hand-enumerated anyPanelOpen listed.
// For each diff-set panel the old guard read false, cycleSession ran
// before the dispatch chain, and the open panel leaked onto the newly
// selected session - the exact bug #1772 fixed for its nine states.
// The guard now delegates to hasActivePanel (audited against
// closeActivePanel in #904) plus the four states it does not model.
func TestSessionCycleKeysYieldToDiffSetPanels(t *testing.T) {
	openers := map[string]func(m *Model){
		"modelPanel":      func(m *Model) { m.modelPanel = &modelPanelState{} },
		"knightPanel":     func(m *Model) { m.knightPanel = &knightPanelState{} },
		"statsPanel":      func(m *Model) { m.statsPanel = &statsPanelState{} },
		"skillsPanel":     func(m *Model) { m.skillsPanel = &skillsPanelState{} },
		"hooksPanel":      func(m *Model) { m.hooksPanel = &hooksPanelState{} },
		"inspectorPanel":  func(m *Model) { m.inspectorPanel = &inspectorPanelState{} },
		"lanChatPanel":    func(m *Model) { m.lanChatPanel = &lanChatPanelState{} },
		"qrOverlay":       func(m *Model) { m.qrOverlay = &qrOverlayState{} },
		"initPrompt":      func(m *Model) { m.initPromptActive = true },
		"imPanel":         func(m *Model) { m.imPanel = &imPanelState{} },
		"tgPanel":         func(m *Model) { m.tgPanel = &tgPanelState{} },
		"discordPanel":    func(m *Model) { m.discordPanel = &discordPanelState{} },
		"feishuPanel":     func(m *Model) { m.feishuPanel = &feishuPanelState{} },
		"slackPanel":      func(m *Model) { m.slackPanel = &slackPanelState{} },
		"dingtalkPanel":   func(m *Model) { m.dingtalkPanel = &dingtalkPanelState{} },
		"wechatPanel":     func(m *Model) { m.wechatPanel = &wechatPanelState{} },
		"wecomPanel":      func(m *Model) { m.wecomPanel = &wecomPanelState{} },
		"mattermostPanel": func(m *Model) { m.mattermostPanel = &mattermostPanelState{} },
		"matrixPanel":     func(m *Model) { m.matrixPanel = &matrixPanelState{} },
		"signalPanel":     func(m *Model) { m.signalPanel = &signalPanelState{} },
		"ircPanel":        func(m *Model) { m.ircPanel = &ircPanelState{} },
		"nostrPanel":      func(m *Model) { m.nostrPanel = &nostrPanelState{} },
		"twitchPanel":     func(m *Model) { m.twitchPanel = &twitchPanelState{} },
		"whatsappPanel":   func(m *Model) { m.whatsappPanel = &whatsappPanelState{} },
	}
	for name, open := range openers {
		m := newTestModel()
		open(&m)
		if !anyPanelOpen(m) {
			t.Errorf("anyPanelOpen must be true when %s is open (#1880: guard must cover every dispatched panel)", name)
		}
	}
}

// Sanity: the guard reads false when nothing is open, so the cycle keys
// keep working in the plain state.
func TestSessionCycleKeysFreeWhenNoPanel(t *testing.T) {
	m := newTestModel()
	if anyPanelOpen(m) {
		t.Error("anyPanelOpen must be false with no panels open")
	}
}
