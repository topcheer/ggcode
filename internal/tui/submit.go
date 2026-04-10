package tui

import (
	"context"
	"errors"

	"github.com/topcheer/ggcode/internal/session"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/provider"
)

func (m *Model) appendUserMessage(text string) {
	m.sessionMutex().Lock()
	defer m.sessionMutex().Unlock()
	if m.session == nil || m.sessionStore == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	// Auto-generate title from first user message
	if m.session.Title == "" || m.session.Title == "New session" {
		if len(text) > 60 {
			m.session.Title = text[:57] + "..."
		} else {
			m.session.Title = text
		}
	}
	if store, ok := m.sessionStore.(*session.JSONLStore); ok {
		_ = store.AppendMessage(m.session, msg)
	} else {
		m.session.Messages = append(m.session.Messages, msg)
		_ = m.sessionStore.Save(m.session)
	}
}

func (m *Model) startAgent(text string) tea.Cmd {
	debug.Log("tui", "startAgent called: text=%s", truncateStr(text, 200))
	// Capture and clear pending image
	img := m.pendingImage
	m.pendingImage = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeAgentRunID++
	runID := m.activeAgentRunID
	if m.agent != nil {
		m.agent.SetInterruptionHandler(func() string {
			return m.drainPendingInterrupt(runID)
		})
	}

	return func() tea.Msg {
		go func() {
			defer func() {
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

			if img != nil {
				streamErrSent := false
				content := []provider.ContentBlock{
					provider.TextBlock(text),
					provider.ImageBlock(img.img.MIME, image.EncodeBase64(img.img)),
				}
				err := m.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
					if m.program == nil {
						return
					}
					switch event.Type {
					case provider.StreamEventText:
						m.program.Send(agentStreamMsg{RunID: runID, Text: event.Text})
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity: m.t("status.writing"),
						}})
					case provider.StreamEventToolCallDone:
						present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
						if isSubAgentLifecycleTool(event.Tool.Name) {
							m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
								ToolName: event.Tool.Name,
								Activity: m.t("status.thinking"),
								Running:  true,
								RawArgs:  string(event.Tool.Arguments),
								Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
							}})
							break
						}
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity:  present.Activity,
							ToolName:  present.DisplayName,
							ToolArg:   present.Detail,
							ToolCount: m.statusToolCount + 1,
						}})
						m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
							ToolName:    event.Tool.Name,
							DisplayName: present.DisplayName,
							Detail:      present.Detail,
							Activity:    present.Activity,
							Running:     true,
							RawArgs:     string(event.Tool.Arguments),
							Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
						}})
					case provider.StreamEventToolResult:
						present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
						if isSubAgentLifecycleTool(event.Tool.Name) {
							m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
								ToolName: event.Tool.Name,
								Activity: m.t("status.thinking"),
								Running:  false,
								Result:   event.Result,
								RawArgs:  string(event.Tool.Arguments),
								Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
								IsError:  event.IsError,
							}})
							m.program.Send(subAgentUpdateMsg{})
							break
						}
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity: m.t("status.thinking"),
							ToolName: present.DisplayName,
							ToolArg:  present.Detail,
						}})
						m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
							ToolName:    event.Tool.Name,
							DisplayName: present.DisplayName,
							Detail:      present.Detail,
							Activity:    present.Activity,
							Running:     false,
							Result:      event.Result,
							RawArgs:     string(event.Tool.Arguments),
							Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
							IsError:     event.IsError,
						}})
					case provider.StreamEventError:
						if !errors.Is(event.Error, context.Canceled) {
							streamErrSent = true
							m.program.Send(agentErrMsg{RunID: runID, Err: event.Error})
						}
					}
				})
				if err != nil && !errors.Is(err, context.Canceled) && !streamErrSent && m.program != nil {
					m.program.Send(agentErrMsg{RunID: runID, Err: err})
				}
			} else {
				streamErrSent := false
				err := m.agent.RunStream(ctx, text, func(event provider.StreamEvent) {
					if m.program == nil {
						return
					}
					switch event.Type {
					case provider.StreamEventText:
						m.program.Send(agentStreamMsg{RunID: runID, Text: event.Text})
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity: m.t("status.writing"),
						}})
					case provider.StreamEventToolCallDone:
						present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
						if isSubAgentLifecycleTool(event.Tool.Name) {
							m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
								ToolName: event.Tool.Name,
								Activity: m.t("status.thinking"),
								Running:  true,
								RawArgs:  string(event.Tool.Arguments),
								Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
							}})
							break
						}
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity:  present.Activity,
							ToolName:  present.DisplayName,
							ToolArg:   present.Detail,
							ToolCount: m.statusToolCount + 1,
						}})
						m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
							ToolName:    event.Tool.Name,
							DisplayName: present.DisplayName,
							Detail:      present.Detail,
							Activity:    present.Activity,
							Running:     true,
							RawArgs:     string(event.Tool.Arguments),
							Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
						}})
					case provider.StreamEventToolResult:
						present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
						if isSubAgentLifecycleTool(event.Tool.Name) {
							m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
								ToolName: event.Tool.Name,
								Activity: m.t("status.thinking"),
								Running:  false,
								Result:   event.Result,
								RawArgs:  string(event.Tool.Arguments),
								Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
								IsError:  event.IsError,
							}})
							m.program.Send(subAgentUpdateMsg{})
							break
						}
						m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
							Activity: m.t("status.thinking"),
							ToolName: present.DisplayName,
							ToolArg:  present.Detail,
						}})
						m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
							ToolName:    event.Tool.Name,
							DisplayName: present.DisplayName,
							Detail:      present.Detail,
							Activity:    present.Activity,
							Running:     false,
							Result:      event.Result,
							RawArgs:     string(event.Tool.Arguments),
							Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
							IsError:     event.IsError,
						}})
					case provider.StreamEventError:
						if !errors.Is(event.Error, context.Canceled) {
							streamErrSent = true
							m.program.Send(agentErrMsg{RunID: runID, Err: event.Error})
						}
					}
				})
				if err != nil && !errors.Is(err, context.Canceled) && !streamErrSent && m.program != nil {
					m.program.Send(agentErrMsg{RunID: runID, Err: err})
				}
			}
		}()

		return nil
	}
}
