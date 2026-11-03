package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"time"
)

func (m Model) handleQqBindResultMsg(msg qqBindResultMsg) (Model, tea.Cmd) {
	if m.qqPanel != nil {
		if msg.err != nil {
			m.qqPanel.shareAdapter = ""
			m.qqPanel.shareLink = ""
			m.qqPanel.shareQRCode = ""
			m.qqPanel.message = msg.err.Error()
		} else {
			m.qqPanel.shareAdapter = msg.shareAdapter
			m.qqPanel.shareLink = msg.shareLink
			m.qqPanel.shareQRCode = msg.shareQRCode
			m.qqPanel.message = msg.message

			if msg.shareQRCode != "" || msg.shareLink != "" {
				m.openQROverlayDirect(
					"QQ — Share Link",
					"Scan to share",
					msg.shareQRCode,
					msg.shareLink,
				)
			}
		}
	}
	return m, nil

}

func (m Model) handleImPanelResultMsg(msg imPanelResultMsg) (Model, tea.Cmd) {
	msgText := msg.message
	if msg.err != nil {
		msgText = msg.err.Error()
	}
	if m.imPanel != nil {
		m.imPanel.message = msgText
	}

	if p := m.activeIMPanel(); p != nil {
		*p = msgText
	}
	return m, nil

}

func (m Model) handleFeishuBindResultMsg(msg feishuBindResultMsg) (Model, tea.Cmd) {
	if m.feishuPanel != nil {
		if msg.err != nil {
			m.feishuPanel.message = msg.err.Error()
		} else {
			m.feishuPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleSlackBindResultMsg(msg slackBindResultMsg) (Model, tea.Cmd) {
	if m.slackPanel != nil {
		if msg.err != nil {
			m.slackPanel.message = msg.err.Error()
		} else {
			m.slackPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleDiscordBindResultMsg(msg discordBindResultMsg) (Model, tea.Cmd) {
	if m.discordPanel != nil {
		if msg.err != nil {
			m.discordPanel.message = msg.err.Error()
		} else {
			m.discordPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleWhatsappBindResultMsg(msg whatsappBindResultMsg) (Model, tea.Cmd) {
	if m.whatsappPanel != nil {
		if msg.err != nil {
			m.whatsappPanel.message = msg.err.Error()
		} else {
			m.whatsappPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleDingtalkBindResultMsg(msg dingtalkBindResultMsg) (Model, tea.Cmd) {
	if m.dingtalkPanel != nil {
		if msg.err != nil {
			m.dingtalkPanel.message = msg.err.Error()
		} else {
			m.dingtalkPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleWecomBindResultMsg(msg wecomBindResultMsg) (Model, tea.Cmd) {
	if m.wecomPanel != nil {
		if msg.err != nil {
			m.wecomPanel.message = msg.err.Error()
		} else {
			m.wecomPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleMattermostBindResultMsg(msg mattermostBindResultMsg) (Model, tea.Cmd) {
	if m.mattermostPanel != nil {
		if msg.err != nil {
			m.mattermostPanel.message = msg.err.Error()
		} else {
			m.mattermostPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleMatrixBindResultMsg(msg matrixBindResultMsg) (Model, tea.Cmd) {
	if m.matrixPanel != nil {
		if msg.err != nil {
			m.matrixPanel.message = msg.err.Error()
		} else {
			m.matrixPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleSignalBindResultMsg(msg signalBindResultMsg) (Model, tea.Cmd) {
	if m.signalPanel != nil {
		m.signalPanel.installing = false
		if msg.err != nil {
			m.signalPanel.message = msg.err.Error()
		} else {
			m.signalPanel.message = msg.message
		}

		if m.signalPanel.daemonOK != nil && !*m.signalPanel.daemonOK {
			return m, checkSignalDaemonCmd()
		}
	}
	return m, nil

}

func (m Model) handleSignalDaemonCheckMsg(msg signalDaemonCheckMsg) (Model, tea.Cmd) {
	if m.signalPanel != nil {
		m.signalPanel.daemonOK = &msg.ok
	}
	return m, nil

}

func (m Model) handleSignalQRCodeMsg(msg signalQRCodeMsg) (Model, tea.Cmd) {
	if m.signalPanel != nil {
		m.signalPanel.qrFetching = false
		if msg.err != nil {

			m.signalPanel.qrError = msg.err.Error()
			m.signalPanel.message = m.t("panel.signal.qr_fetch_failed") + ": " + msg.err.Error()
		} else {
			m.signalPanel.qrCode = msg.qr

			m.openQROverlayDirect(
				"Signal — "+m.t("panel.signal.qr_title"),
				m.t("panel.signal.qr_scan_hint"),
				msg.qr,
				"",
			)
		}
	}
	return m, nil

}

func (m Model) handleIrcBindResultMsg(msg ircBindResultMsg) (Model, tea.Cmd) {
	if m.ircPanel != nil {
		if msg.err != nil {
			m.ircPanel.message = msg.err.Error()
		} else {
			m.ircPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleNostrBindResultMsg(msg nostrBindResultMsg) (Model, tea.Cmd) {
	if m.nostrPanel != nil {
		if msg.err != nil {
			m.nostrPanel.message = msg.err.Error()
		} else {
			m.nostrPanel.message = msg.message
			m.nostrPanel.qrCode = msg.qrCode
			m.nostrPanel.generatedNpub = msg.npub

			if msg.qrCode != "" || msg.npub != "" {
				m.openQROverlayDirect(
					"Nostr — Public Key",
					m.t("panel.qr.scan_hint"),
					msg.qrCode,
					msg.npub,
				)
			}
		}
	}
	return m, nil

}

func (m Model) handleTwitchBindResultMsg(msg twitchBindResultMsg) (Model, tea.Cmd) {
	if m.twitchPanel != nil {
		if msg.err != nil {
			m.twitchPanel.message = msg.err.Error()
		} else {
			m.twitchPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleTgBindResultMsg(msg tgBindResultMsg) (Model, tea.Cmd) {
	if m.tgPanel != nil {
		if msg.err != nil {
			m.tgPanel.message = msg.err.Error()
		} else {
			m.tgPanel.message = msg.message
		}
	}
	return m, nil

}

func (m Model) handleImEditResultMsg(msg imEditResultMsg) (Model, tea.Cmd) {

	if m.qqPanel != nil && m.qqPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.qqPanel.editState, msg)
	} else if m.tgPanel != nil && m.tgPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.tgPanel.editState, msg)
	} else if m.discordPanel != nil && m.discordPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.discordPanel.editState, msg)
	} else if m.feishuPanel != nil && m.feishuPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.feishuPanel.editState, msg)
	} else if m.slackPanel != nil && m.slackPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.slackPanel.editState, msg)
	} else if m.dingtalkPanel != nil && m.dingtalkPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.dingtalkPanel.editState, msg)
	} else if m.pcPanel != nil && m.pcPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.pcPanel.editState, msg)
	} else if m.wechatPanel != nil {
		if m.wechatPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.wechatPanel.editState, msg)
		} else if msg.err != nil {
			m.wechatPanel.message = fmt.Sprintf("Error: %v", msg.err)
		} else if msg.adapterName != "" {

			switch msg.field {
			case "remove":
				m.wechatPanel.message = m.t("panel.wechat.removed", msg.adapterName)
			case "bind":
				m.wechatPanel.message = m.t("panel.wechat.auth_success") + " (" + msg.adapterName + ")"
			default:
				m.wechatPanel.message = m.t("panel.wechat.auth_success") + " (" + msg.adapterName + ")"
			}
		}
	} else if m.wecomPanel != nil && m.wecomPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.wecomPanel.editState, msg)
	} else if m.mattermostPanel != nil && m.mattermostPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.mattermostPanel.editState, msg)
	} else if m.matrixPanel != nil && m.matrixPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.matrixPanel.editState, msg)
	} else if m.signalPanel != nil && m.signalPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.signalPanel.editState, msg)
	} else if m.ircPanel != nil && m.ircPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.ircPanel.editState, msg)
	} else if m.nostrPanel != nil && m.nostrPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.nostrPanel.editState, msg)
	} else if m.twitchPanel != nil && m.twitchPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.twitchPanel.editState, msg)
	} else if m.whatsappPanel != nil && m.whatsappPanel.editState.mode != imEditNone {
		m.applyIMEditResult(&m.whatsappPanel.editState, msg)
	}
	return m, nil

}

func (m Model) handleImPanelRefreshMsg(msg imPanelRefreshMsg) (Model, tea.Cmd) {

	if m.whatsappPanel != nil {
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return imPanelRefreshMsg{}
		})
	}
	return m, nil

}
