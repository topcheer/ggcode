package tui

// #2422 knife F (misc long-tail): extracted verbatim from Model.Update's
// inline case bodies - zero behavior change.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (m Model) handleWebuiReadyMsg(msg webuiReadyMsg) (tea.Model, tea.Cmd) {
	// Display webui URL as a subtle system message in the chat area
	if msg.Addr != "" {
		url := "http://" + msg.Addr
		if msg.Token != "" {
			url += "#token=" + msg.Token
		}
		m.chatWriteSystem(nextSystemID(), "\u2B21 WebUI: "+url)
		m.chatListScrollToBottom()
	}
	return m, nil
}

func (m Model) handlePcResultMsg(msg pcResultMsg) (tea.Model, tea.Cmd) {
	// #1386-A: pcResultMsg had NO Update case - every PC panel
	// operation result (create/QR/renew/close/bind) was silently
	// dropped by bubbletea: QR never shown, success/error feedback
	// never updated. Routed to the panel fields like signal's results.
	if m.pcPanel != nil {
		if msg.err != nil {
			m.pcPanel.message = fmt.Sprintf("Error: %v", msg.err)
			m.pcPanel.showQR = false
		} else {
			m.pcPanel.message = msg.message
			m.pcPanel.showQR = msg.showQR
			m.pcPanel.qrCode = msg.qrCode
			if msg.inviteURI != "" {
				m.pcPanel.inviteURI = msg.inviteURI
			}
		}
	}
	return m, nil
}

func (m Model) handleRestorePendingImagesMsg(msg restorePendingImagesMsg) (tea.Model, tea.Cmd) {
	// #1393-A: the aborted run's captured attachments come home.
	if msg.RunID != m.activeAgentRunID {
		return m, nil // a newer run started; don't interleave its state
	}
	m.pendingImages = append(m.pendingImages, msg.Images...)
	debug.Log("tui", "restored %d pending image(s) from aborted submit (expand failure)", len(msg.Images))
	return m, nil
}

func (m Model) handleProjectMemoryLoadedMsg(msg projectMemoryLoadedMsg) (tea.Model, tea.Cmd) {
	m.projectMemoryLoading = false
	if msg.Err != nil {
		debug.Log("tui", "project memory load failed: %v", msg.Err)
		if m.pendingSubmissionCount() > 0 && !m.loading {
			return m, m.submitPendingSubmissionCmd()
		}
		return m, nil
	}
	m.projMemFiles = append([]string(nil), msg.Files...)
	if m.agent != nil && strings.TrimSpace(msg.Content) != "" {
		m.agent.SetProjectMemoryFiles(msg.Files)
		m.agent.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: msg.Content}},
		})
	}
	if m.pendingSubmissionCount() > 0 && !m.loading {
		if m.shellMode {
			return m, m.submitShellCommand(m.consumePendingSubmission(), false)
		}
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil
}

func (m Model) handleSystemNotifyMsg(msg systemNotifyMsg) (tea.Model, tea.Cmd) {
	if msg.ItemID != "" {
		if item := m.chatList.FindByID(msg.ItemID); item != nil {
			if sys, ok := item.(*chat.SystemItem); ok {
				if msg.Replace {
					sys.SetText(msg.Text)
				} else {
					sys.AppendText(msg.Text)
				}
				m.chatListScrollToBottom()
				return m, nil
			}
		}
		// Item not found yet — create it with the provided ItemID so
		// subsequent retries with the same ItemID can find and append.
		m.chatWriteSystem(msg.ItemID, msg.Text)
		m.chatListFollowOutput()
		return m, nil
	}
	m.chatWriteSystem(nextSystemID(), msg.Text)
	m.chatListFollowOutput()
	return m, nil
}

func (m Model) handleGitBranchTickMsg(msg gitBranchTickMsg) (tea.Model, tea.Cmd) {
	m.refreshCachedGitBranch()
	return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return gitBranchTickMsg{}
	})
}

func (m Model) handleWechatPollDueMsg(msg wechatPollDueMsg) (tea.Model, tea.Cmd) {
	// #1792 case 2: the paced re-poll fired - run the real poll.
	if m.wechatPanel != nil {
		return m, m.pollWechatQRStatus(msg.token)
	}
	return m, nil
}
