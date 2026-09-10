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

	// Shell passthrough ($ cmd / ! cmd): execute immediately even while a
	// turn is running, mirroring the daemon bridge and the local TUI escape.
	// Never queued as an agent submission - the user expects direct output.
	if route.Kind == im.InboundRouteShell {
		if msg.Response != nil {
			msg.Response <- nil
		}
		im.RunInboundShellAsync(route.Text, func(s string) error {
			m.emitIMText(s)
			return nil
		})
		return m, nil
	}

	if route.Kind == im.InboundRouteApproval {
		// #1833 case 1: mirror the questionnaire branch's stale guard (and
		// the daemon bridge's #569 A timeout drop). The route decision came
		// from a snapshot taken before handleRemoteInbound; if the approval
		// was already answered locally / timed out in between, the late IM
		// "y" must be dropped - leaking it further would crash on the nil
		// ToolName read below or, worse, resubmit "y" as a fresh prompt.
		if m.pendingApproval == nil {
			if msg.Response != nil {
				msg.Response <- nil
			}
			return m, nil
		}
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
		if m.pendingQuestionnaire == nil {
			// Questionnaire was already completed/cancelled; ignore stale IM reply.
			if msg.Response != nil {
				msg.Response <- nil
			}
			return m, nil
		}
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
	// #1762 case 2: match the local Enter gate (update_keys.go) - a
	// message arriving while project memory is still LOADING would run
	// its first turn without the project-memory system injection and
	// reorder behind locally queued messages.
	if m.loading || m.projectMemoryLoading {
		m.queuePendingSubmission(prompt)
		return m, nil
	}
	return m, m.submitText(prompt, false)

}
