package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// handleRemoteInbound handles the remoteInboundMsg case.
func (m Model) handleRemoteInbound(msg remoteInboundMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
	// Track the originating adapter for per-channel echo suppression.
	m.remoteInboundAdapter = msg.Message.Envelope.Adapter
	prompt := buildRemoteInboundPrompt(msg.Message)
	route := im.RouteInboundText(prompt, m.pendingApproval != nil, m.pendingQuestionnaire != nil)

	if route.Kind == im.InboundRouteSlash {
		if response, handled := m.ExecuteRemoteSlashCommand(route.Text); handled {
			// Handle /restart: send IM confirmation first, then quit after delay.
			if response == "RESTART" || response == "RESTART:DEBUG" {
				if response == "RESTART:DEBUG" {
					m.emitIMText("\U0001f504 Restarting ggcode with debug mode enabled...")
				} else {
					m.emitIMText("\U0001f504 Restarting ggcode...")
				}
				if msg.Response != nil {
					msg.Response <- nil
				}
				return m, m.scheduleRemoteRestart()
			}
			// Handle /muteself: send warning first, then mute after delay.
			if strings.HasPrefix(response, "MUTES:") {
				adapter := strings.TrimPrefix(response, "MUTES:")
				m.emitIMText(im.DefaultMuteSelfWarning(adapter))
				if msg.Response != nil {
					msg.Response <- nil
				}
				return m, m.scheduleMuteSelf(adapter)
			}
			if strings.TrimSpace(response) != "" {
				m.emitIMText(response)
			}
			if msg.Response != nil {
				msg.Response <- nil
			}
			return m, nil
		}
	}

	if route.Kind == im.InboundRouteApproval {
		toolName := m.pendingApproval.ToolName
		decisionStr := "deny"
		var cmd tea.Cmd
		if route.Decision == permission.Allow && route.AlwaysAllow {
			cmd = m.handleApprovalAllowAlways()
			decisionStr = "always"
		} else {
			if route.Decision == permission.Allow {
				decisionStr = "allow"
			}
			cmd = m.handleApproval(route.Decision)
		}
		if msg.Response != nil {
			msg.Response <- nil
		}
		if m.approvalNotifiedIM {
			m.emitIMApprovalResult(toolName, decisionStr)
		}
		return m, cmd
	}

	if route.Kind == im.InboundRouteAskUser {
		completed, err := m.pendingQuestionnaire.applyRemoteAnswer(route.Text, m.currentLanguage())
		if msg.Response != nil {
			msg.Response <- nil
		}
		if err != nil {
			switch m.currentLanguage() {
			case LangZhCN:
				m.emitIMText("没有识别出有效的问卷答案，请直接回复选项编号或文本答案。")
			default:
				m.emitIMText("I couldn't parse that questionnaire answer. Reply with choice numbers or plain text.")
			}
			return m, nil
		}
		if completed {
			return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusSubmitted)
		}
		if nextIdx := m.pendingQuestionnaire.firstUnansweredQuestionIndex(); nextIdx >= 0 {
			q := m.pendingQuestionnaire.request.Questions[nextIdx]
			fallback := m.formatIMAskUserQuestion(m.pendingQuestionnaire.request.Title, q)
			if len(q.Choices) > 0 {
				m.emitIMAskUserInteractive(m.pendingQuestionnaire.request.Title, q, fallback)
			} else {
				m.emitIMAskUser(fallback)
			}
		}
		return m, nil
	}

	if route.Kind == im.InboundRouteEmpty {
		if msg.Response != nil {
			msg.Response <- fmt.Errorf("empty remote message")
		}
		return m, nil
	}
	if msg.Response != nil {
		msg.Response <- nil
	}
	// Echo user message to all channels EXCEPT the originating adapter,
	// so other IM users can see what was asked.
	m.emitIMLocalUserTextExcept(prompt, m.remoteInboundAdapter)
	if m.loading {
		m.queuePendingSubmission(prompt)
		return m, nil
	}
	return m, m.submitText(prompt, false)

}
