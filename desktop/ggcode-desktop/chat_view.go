package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const autoScrollPause = 30 * time.Second

// sendEntry: Enter sends, Ctrl/Shift+Enter inserts newline.
type sendEntry struct {
	widget.Entry
	onSend func()
}

func newSendEntry() *sendEntry {
	e := &sendEntry{}
	e.MultiLine = true
	e.ExtendBaseWidget(e)
	return e
}

func (e *sendEntry) KeyDown(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.isCtrlOrShiftHeld() {
			e.Entry.KeyDown(key)
			return
		}
		if e.onSend != nil {
			e.onSend()
		}
		return
	}
	e.Entry.KeyDown(key)
}

func (e *sendEntry) isCtrlOrShiftHeld() bool {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		m := d.CurrentKeyModifiers()
		if m&fyne.KeyModifierControl != 0 || m&fyne.KeyModifierShift != 0 {
			return true
		}
	}
	return false
}

// ChatView renders the chat area with smart auto-scroll.
type ChatView struct {
	bridge *AgentBridge
	ui     *UIState

	entry     *sendEntry
	sendBtn   *widget.Button
	cancelBtn *widget.Button
	scroll    *container.Scroll
	vbox      *fyne.Container

	scrollMu    sync.Mutex
	autoScroll  bool
	lastUserAct time.Time
}

func NewChatView(bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{
		bridge:     bridge,
		ui:         ui,
		autoScroll: true,
	}

	cv.entry = newSendEntry()
	cv.entry.PlaceHolder = "Message ggcode... (Enter to send, Ctrl+Enter for newline)"
	cv.entry.Wrapping = fyne.TextWrapWord
	cv.entry.SetMinRowsVisible(2)
	cv.entry.onSend = cv.onSend

	cv.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), cv.onSend)
	cv.sendBtn.Importance = widget.HighImportance

	cv.cancelBtn = widget.NewButtonWithIcon("Stop", theme.CancelIcon(), func() {
		cv.bridge.Cancel()
	})
	cv.cancelBtn.Importance = widget.DangerImportance
	cv.cancelBtn.Hide()

	return cv
}

func (cv *ChatView) Render() fyne.CanvasObject {
	btnRow := container.NewHBox(cv.cancelBtn, cv.sendBtn)
	inputBar := container.NewBorder(nil, nil, nil, btnRow, cv.entry)

	cv.vbox = container.NewVBox()
	cv.scroll = container.NewVScroll(cv.vbox)

	cv.scroll.OnScrolled = func(_ fyne.Position) {
		cv.scrollMu.Lock()
		cv.autoScroll = false
		cv.lastUserAct = time.Now()
		cv.scrollMu.Unlock()
	}

	go cv.pollRefresh()

	return container.NewBorder(nil, container.NewPadded(inputBar), nil, nil, cv.scroll)
}

func (cv *ChatView) shouldAutoScroll() bool {
	cv.scrollMu.Lock()
	defer cv.scrollMu.Unlock()
	if cv.autoScroll {
		return true
	}
	if time.Since(cv.lastUserAct) >= autoScrollPause {
		cv.autoScroll = true
		return true
	}
	return false
}

func (cv *ChatView) pollRefresh() {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		working := cv.bridge.IsWorking()
		dirty := cv.ui.IsDirty()

		fyne.Do(func() {
			if dirty {
				cv.rebuildMessages()
			}
			if cv.shouldAutoScroll() {
				cv.scroll.ScrollToBottom()
			}
			cv.updateButtons(working)
		})
	}
}

func (cv *ChatView) updateButtons(working bool) {
	if working {
		cv.sendBtn.Hide()
		cv.cancelBtn.Show()
	} else {
		cv.sendBtn.Show()
		cv.sendBtn.Enable()
		cv.cancelBtn.Hide()
	}
}

func (cv *ChatView) onSend() {
	text := strings.TrimSpace(cv.entry.Text)
	if text == "" || cv.bridge.IsWorking() {
		return
	}
	cv.entry.SetText("")

	cv.scrollMu.Lock()
	cv.autoScroll = true
	cv.scrollMu.Unlock()

	cv.ui.AppendChat(ChatMessage{
		Role:    "user",
		Content: text,
		Time:    time.Now(),
	})

	if err := cv.bridge.Send(text); err != nil {
		cv.ui.AppendChat(ChatMessage{
			Role:    "error",
			Content: err.Error(),
			Time:    time.Now(),
		})
	}
}

// ── Message rebuild ──────────────────────────────────

func (cv *ChatView) rebuildMessages() {
	msgs := cv.ui.TakeMessages()
	objects := make([]fyne.CanvasObject, 0, len(msgs))

	for i := range msgs {
		w := cv.buildMessageWidget(&msgs[i])
		if w != nil {
			objects = append(objects, w)
		}
	}

	cv.vbox.Objects = objects
	cv.vbox.Refresh()
}

// ── Message widgets ──────────────────────────────────

func (cv *ChatView) buildMessageWidget(msg *ChatMessage) fyne.CanvasObject {
	switch msg.Role {
	case "user":
		return cv.userBubble(msg)
	case "assistant":
		return cv.assistantBubble(msg)
	case "tool":
		return cv.toolItem(msg)
	case "system":
		return cv.systemNotice(msg)
	case "reasoning":
		return cv.reasoningNotice(msg)
	case "error":
		return cv.errorNotice(msg)
	}
	return nil
}

// userBubble — user message, rendered as plain text.
func (cv *ChatView) userBubble(msg *ChatMessage) fyne.CanvasObject {
	rt := widget.NewRichTextWithText(msg.Content)
	rt.Wrapping = fyne.TextWrapWord
	return container.NewPadded(widget.NewCard("", "", container.NewPadded(rt)))
}

// assistantBubble — markdown rendered output.
func (cv *ChatView) assistantBubble(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}

	rt := widget.NewRichTextFromMarkdown(text)
	rt.Wrapping = fyne.TextWrapWord

	return container.NewPadded(widget.NewCard("", "", container.NewPadded(rt)))
}

// toolItem — command tools show code blocks; others show compact accordion.
func (cv *ChatView) toolItem(msg *ChatMessage) fyne.CanvasObject {
	// Agent/team tools get special rendering.
	switch msg.ToolName {
	case "spawn_agent":
		return cv.agentItem(msg)
	case "send_message":
		return cv.sendMessageItem(msg)
	case "wait_agent":
		return cv.waitAgentItem(msg)
	case "teammate_spawn":
		return cv.teammateSpawnItem(msg)
	case "teammate_list", "teammate_shutdown", "teammate_results":
		return cv.genericToolItem(msg, msg.ToolName, statusText(msg), statusColor(msg))
	}

	isCommand := msg.ToolName == "run_command" || msg.ToolName == "start_command"

	displayTitle := msg.ToolDesc
	if displayTitle == "" || displayTitle == msg.ToolName {
		displayTitle = msg.ToolName
	}

	if isCommand {
		return cv.commandToolItem(msg, displayTitle, statusText(msg), statusColor(msg))
	}
	return cv.genericToolItem(msg, displayTitle, statusText(msg), statusColor(msg))
}

// statusText returns the display status for a tool message.
func statusText(msg *ChatMessage) string {
	if msg.Content == "" {
		return "running..."
	}
	if msg.IsError {
		return "failed"
	}
	return "done"
}

// statusColor returns the theme color name for a tool status.
func statusColor(msg *ChatMessage) fyne.ThemeColorName {
	if msg.Content == "" {
		return theme.ColorNameWarning
	}
	if msg.IsError {
		return theme.ColorNameError
	}
	return theme.ColorNameSuccess
}

// agentItem renders spawn_agent as a card with agent name and result.
func (cv *ChatView) agentItem(msg *ChatMessage) fyne.CanvasObject {
	// Extract agent name from args.
	agentName := extractJSONField(msg.ToolArgs, "name")
	if agentName == "" {
		agentName = extractJSONField(msg.ToolArgs, "subagent_type")
	}
	if agentName == "" {
		agentName = "sub-agent"
	}
	taskDesc := extractJSONField(msg.ToolArgs, "task")
	if len(taskDesc) > 120 {
		taskDesc = taskDesc[:120] + "..."
	}

	headerText := "Agent: " + agentName
	status := statusText(msg)
	sc := statusColor(msg)

	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: sc},
			Text:  "  " + status,
		},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, container.NewPadded(header))

	if taskDesc != "" {
		taskLabel := widget.NewLabel(taskDesc)
		taskLabel.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(taskLabel))
	}

	if msg.Content != "" {
		result := msg.Content
		if len(result) > 2000 {
			result = result[:2000] + "\n...(truncated)"
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(resultBlock))
	} else {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		parts = append(parts, container.NewPadded(spinner))
	}

	return widget.NewCard("", "", container.NewVBox(parts...))
}

// sendMessageItem renders send_message to teammate/sub-agent.
func (cv *ChatView) sendMessageItem(msg *ChatMessage) fyne.CanvasObject {
	to := extractJSONField(msg.ToolArgs, "to")
	summary := extractJSONField(msg.ToolArgs, "summary")

	headerText := "Send to: " + to
	if summary != "" {
		headerText = summary + "  →  " + to
	}
	status := statusText(msg)
	sc := statusColor(msg)

	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: sc},
			Text:  "  " + status,
		},
	)

	return widget.NewCard("", "", container.NewVBox(container.NewPadded(header)))
}

// waitAgentItem renders wait_agent results.
func (cv *ChatView) waitAgentItem(msg *ChatMessage) fyne.CanvasObject {
	agentID := extractJSONField(msg.ToolArgs, "agent_id")
	headerText := "Waiting for: " + agentID
	status := statusText(msg)
	sc := statusColor(msg)

	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: sc},
			Text:  "  " + status,
		},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, container.NewPadded(header))

	if msg.Content != "" {
		result := msg.Content
		if len(result) > 2000 {
			result = result[:2000] + "\n...(truncated)"
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(resultBlock))
	}

	return widget.NewCard("", "", container.NewVBox(parts...))
}

// teammateSpawnItem renders teammate_spawn.
func (cv *ChatView) teammateSpawnItem(msg *ChatMessage) fyne.CanvasObject {
	name := extractJSONField(msg.ToolArgs, "name")
	if name == "" {
		name = "teammate"
	}
	teamID := extractJSONField(msg.ToolArgs, "team_id")

	headerText := "Teammate: " + name
	if teamID != "" {
		headerText += "  (team: " + teamID[:8] + ")"
	}
	status := statusText(msg)
	sc := statusColor(msg)

	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: sc},
			Text:  "  " + status,
		},
	)

	return widget.NewCard("", "", container.NewVBox(container.NewPadded(header)))
}

// commandToolItem renders run_command with markdown code blocks.
// Layout: [header] → [command code block] → [result code block]
func (cv *ChatView) commandToolItem(msg *ChatMessage, displayTitle, status string, statusColor fyne.ThemeColorName) fyne.CanvasObject {
	// Header: tool description + status.
	headerLabel := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  displayTitle,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor},
			Text:  "  " + status,
		},
	)

	// Command code block from ToolArgs.
	var parts []fyne.CanvasObject
	parts = append(parts, container.NewPadded(headerLabel))

	if msg.ToolArgs != "" {
		cmdMd := "```bash\n" + msg.ToolArgs + "\n```"
		cmdBlock := widget.NewRichTextFromMarkdown(cmdMd)
		cmdBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(cmdBlock))
	}

	// Result code block.
	if msg.Content != "" {
		result := msg.Content
		if len(result) > 3000 {
			result = result[:3000] + "\n...(truncated)"
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(resultBlock))
	} else {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		parts = append(parts, container.NewPadded(spinner))
	}

	return widget.NewCard("", "", container.NewVBox(parts...))
}

// genericToolItem renders non-command tools as collapsible accordion.
func (cv *ChatView) genericToolItem(msg *ChatMessage, displayTitle, status string, statusColor fyne.ThemeColorName) fyne.CanvasObject {
	if msg.ToolArgs != "" {
		displayTitle = displayTitle + "  " + msg.ToolArgs
	}
	header := fmt.Sprintf("%s  (%s)", displayTitle, status)

	result := msg.Content
	if len(result) > 1000 {
		result = result[:1000] + "\n...(truncated)"
	}

	var detail fyne.CanvasObject
	if result == "" {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		detail = container.NewPadded(spinner)
	} else {
		resultLabel := widget.NewLabel(result)
		resultLabel.Wrapping = fyne.TextWrapWord
		resultLabel.TextStyle = fyne.TextStyle{Monospace: true}
		detail = container.NewPadded(resultLabel)
	}

	acc := widget.NewAccordion(widget.NewAccordionItem(header, detail))
	acc.MultiOpen = true

	return container.NewPadded(acc)
}

// systemNotice — subtle, centered.
func (cv *ChatView) systemNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText(msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	text.Alignment = fyne.TextAlignCenter
	return container.NewPadded(text)
}

// reasoningNotice — subtle, italic.
func (cv *ChatView) reasoningNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Thinking: "+msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	return container.NewPadded(text)
}

// errorNotice — red, prominent.
func (cv *ChatView) errorNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Error: "+msg.Content, theme.ErrorColor())
	text.TextSize = theme.TextSize()
	return container.NewPadded(text)
}
