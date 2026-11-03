package tui

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func (m Model) remoteShellAllowed() bool {
	return m.config != nil && m.config.IM.RemoteDangerousCommands
}

func injectRemoteImageAttachments(m *Model, msg im.InboundMessage) {
	for _, att := range msg.Attachments {
		if att.DataBase64 == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(att.DataBase64)
		if err != nil {
			debug.Log("tui", "#4818 remote IM attachment %q: base64 decode failed: %v", att.Name, err)
			continue
		}
		img, err := image.Decode(data)
		if err != nil {
			debug.Log("tui", "#4818 remote IM attachment %q: image decode failed: %v", att.Name, err)
			continue
		}
		name := att.Name
		if name == "" {
			name = "remote-image.png"
		}
		m.pendingImages = append(m.pendingImages, imageAttachedMsg{
			placeholder: image.Placeholder(name, img),
			img:         img,
			filename:    filepath.Base(name),
		})
	}
}

func (m Model) handleRemoteInbound(msg remoteInboundMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {

	m.remoteInboundAdapter = msg.Message.Envelope.Adapter

	injectRemoteImageAttachments(&m, msg.Message)
	prompt := buildRemoteInboundPrompt(msg.Message)
	route := im.RouteInboundText(prompt, m.pendingApproval != nil, m.pendingQuestionnaire != nil)

	if route.Kind == im.InboundRouteSlash {
		if response, handled := m.ExecuteRemoteSlashCommand(route.Text); handled {

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

	if route.Kind == im.InboundRouteShell {
		if !m.remoteShellAllowed() {
			if msg.Response != nil {
				msg.Response <- nil
			}
			m.emitIMText("shell passthrough ($/! commands) over IM requires the im.remote_dangerous_commands opt-in - refused (#2185)")
			return m, nil
		}
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

	m.emitIMLocalUserTextExcept(prompt, m.remoteInboundAdapter)

	if m.loading || m.projectMemoryLoading {

		m.queuePendingSubmissionRemote(prompt)
		return m, nil
	}

	m.agent.InhibitNextRefusalLedgerWrite()
	return m, m.submitText(prompt, false)

}
