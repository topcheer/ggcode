package tui

import "testing"

func TestSessionCycleKeysYieldToDiffSetPanels(t *testing.T) {
	openers := map[string]func(m *Model){
		"modelPanel":      func(m *Model) { m.modelPanel = &modelPanelState{} },
		"knightPanel":     func(m *Model) { m.knightPanel = &knightPanelState{} },
		"statsPanel":      func(m *Model) { m.statsPanel = &statsPanelState{} },
		"usagePanel":      func(m *Model) { m.usagePanel = &usagePanelState{} },
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

func TestSessionCycleKeysFreeWhenNoPanel(t *testing.T) {
	m := newTestModel()
	if anyPanelOpen(m) {
		t.Error("anyPanelOpen must be false with no panels open")
	}
}
