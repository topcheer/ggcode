package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/topcheer/ggcode/internal/session"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
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

			if err := m.runAgentSubmission(ctx, runID, text, img); err != nil && !errors.Is(err, context.Canceled) && m.program != nil {
				m.program.Send(agentErrMsg{RunID: runID, Err: err})
			}
		}()

		return nil
	}
}

func (m *Model) runAgentSubmission(ctx context.Context, runID int, text string, img *imageAttachedMsg) error {
	content := buildAgentSubmissionContent(text, img, false)
	if img == nil {
		_, err := m.runAgentWithContent(ctx, runID, content)
		return err
	}

	if !m.activeEndpointSupportsVision() {
		_, err := m.runAgentWithContent(ctx, runID, content)
		return err
	}

	streamErrSent, err := m.runAgentWithContent(ctx, runID, buildAgentSubmissionContent(text, img, true))
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if streamErrSent || !provider.IsImageBlockFallbackCandidate(err) {
		return err
	}

	debug.Log("tui", "image block rejected, retrying without image block: %v", err)
	_, retryErr := m.runAgentWithContent(ctx, runID, content)
	if retryErr == nil {
		return nil
	}
	if errors.Is(retryErr, context.Canceled) {
		return retryErr
	}
	return err
}

func (m *Model) runAgentWithContent(ctx context.Context, runID int, content []provider.ContentBlock) (bool, error) {
	streamErrSent := false
	writingStatusSent := false
	round := agentIMRoundState{}
	err := m.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
		if m.program == nil {
			return
		}
		switch event.Type {
		case provider.StreamEventText:
			round.AppendText(event.Text)
			m.program.Send(agentStreamMsg{RunID: runID, Text: event.Text})
			if !writingStatusSent {
				writingStatusSent = true
				m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
					Activity: m.t("status.writing"),
				}})
			}
		case provider.StreamEventToolCallDone:
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if event.Tool.Name == "ask_user" {
				round.SetAskUser(m.formatIMAskUserPrompt(string(event.Tool.Arguments)))
			}
			if isSubAgentLifecycleTool(event.Tool.Name) {
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:   event.Tool.ID,
					ToolName: event.Tool.Name,
					Activity: m.t("status.thinking"),
					Running:  true,
					RawArgs:  string(event.Tool.Arguments),
					Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
				}})
				return
			}
			round.ToolCalls++
			m.emitIMEvent(im.OutboundEvent{
				Kind: im.OutboundEventToolCall,
				ToolCall: &im.ToolCallInfo{
					ToolName: event.Tool.Name,
					Args:     string(event.Tool.Arguments),
					Detail:   present.Detail,
				},
			})
			m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
				Activity: present.Activity,
				ToolName: present.DisplayName,
				ToolArg:  present.Detail,
			}})
			m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
				ToolID:      event.Tool.ID,
				ToolName:    event.Tool.Name,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				Activity:    present.Activity,
				Running:     true,
				RawArgs:     string(event.Tool.Arguments),
				Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
			}})
		case provider.StreamEventToolResult:
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if isSubAgentLifecycleTool(event.Tool.Name) {
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:   event.Tool.ID,
					ToolName: event.Tool.Name,
					Activity: m.t("status.thinking"),
					Running:  false,
					Result:   event.Result,
					RawArgs:  string(event.Tool.Arguments),
					Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
					IsError:  event.IsError,
				}})
				m.program.Send(subAgentUpdateMsg{})
				return
			}
			if event.IsError {
				round.ToolFailures++
			} else {
				round.ToolSuccesses++
			}
			m.emitIMEvent(im.OutboundEvent{
				Kind: im.OutboundEventToolResult,
				ToolRes: &im.ToolResultInfo{
					ToolName: event.Tool.Name,
					Args:     string(event.Tool.Arguments),
					Result:   event.Result,
					IsError:  event.IsError,
				},
			})
			m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
				Activity: m.t("status.thinking"),
				ToolName: present.DisplayName,
				ToolArg:  present.Detail,
			}})
			m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
				ToolID:      event.Tool.ID,
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
		case provider.StreamEventDone:
			writingStatusSent = false
			switch {
			case round.AskUserText != "":
				m.program.Send(agentAskUserMsg{RunID: runID, Text: round.AskUserText})
			case round.HasVisibleOutput():
				m.program.Send(agentRoundSummaryMsg{
					RunID:         runID,
					Text:          round.Text(),
					ToolCalls:     round.ToolCalls,
					ToolSuccesses: round.ToolSuccesses,
					ToolFailures:  round.ToolFailures,
				})
			}
			round.Reset()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) && !streamErrSent {
		return false, err
	}
	return streamErrSent, err
}

type agentIMRoundState struct {
	text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
	AskUserText   string
}

func (s *agentIMRoundState) AppendText(text string) {
	s.text.WriteString(text)
}

func (s *agentIMRoundState) Text() string {
	return s.text.String()
}

func (s *agentIMRoundState) SetAskUser(text string) {
	s.AskUserText = strings.TrimSpace(text)
}

func (s *agentIMRoundState) HasVisibleOutput() bool {
	return strings.TrimSpace(s.Text()) != ""
}

func (s *agentIMRoundState) Reset() {
	s.text.Reset()
	s.ToolCalls = 0
	s.ToolSuccesses = 0
	s.ToolFailures = 0
	s.AskUserText = ""
}

func buildAgentSubmissionContent(text string, img *imageAttachedMsg, includeImage bool) []provider.ContentBlock {
	prompt := strings.TrimSpace(text)
	if img == nil {
		return []provider.ContentBlock{provider.TextBlock(prompt)}
	}

	imagePath := strings.TrimSpace(img.sourcePath)
	if imagePath == "" {
		imagePath = strings.TrimSpace(img.filename)
	}
	if imagePath != "" {
		pathHint := "\n\n[Attached image path: " + imagePath + "]"
		if includeImage {
			pathHint += "\nAn image is attached directly to this message. Prefer native vision understanding first. Only use external image-analysis tools if direct image understanding is unavailable or clearly insufficient."
		} else {
			pathHint += "\nIf direct multimodal image input is unavailable, inspect this local image path with available tools."
		}
		prompt = strings.TrimSpace(prompt + pathHint)
	}

	content := []provider.ContentBlock{provider.TextBlock(prompt)}
	if includeImage {
		content = append(content, provider.ImageBlock(img.img.MIME, image.EncodeBase64(img.img)))
	}
	return content
}

func (m *Model) activeEndpointSupportsVision() bool {
	if m.config == nil {
		return false
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil || resolved == nil {
		return false
	}
	return resolved.SupportsVision
}
