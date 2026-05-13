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

// ChatView renders the chat area with tabs for sub-agents/teammates.
type ChatView struct {
	bridge *AgentBridge
	ui     *UIState

	entry     *sendEntry
	sendBtn   *widget.Button
	cancelBtn *widget.Button

	// Main chat tab content.
	scroll *container.Scroll
	vbox   *fyne.Container

	// Tab container.
	tabs   *container.AppTabs
	tabMap map[string]*container.TabItem // agentID -> tab item

	// Per-agent scroll containers.
	agentScrolls map[string]*container.Scroll

	scrollMu    sync.Mutex
	autoScroll  bool
	lastUserAct time.Time
}

func NewChatView(bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{
		bridge:       bridge,
		ui:           ui,
		autoScroll:   true,
		tabMap:       make(map[string]*container.TabItem),
		agentScrolls: make(map[string]*container.Scroll),
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

	// Main chat tab.
	cv.vbox = container.NewVBox()
	cv.scroll = container.NewVScroll(cv.vbox)
	cv.scroll.OnScrolled = func(_ fyne.Position) {
		cv.scrollMu.Lock()
		cv.autoScroll = false
		cv.lastUserAct = time.Now()
		cv.scrollMu.Unlock()
	}

	mainTab := container.NewTabItem("Main", cv.scroll)

	cv.tabs = container.NewAppTabs(mainTab)
	cv.tabs.SetTabLocation(container.TabLocationTop)

	go cv.pollRefresh()

	return container.NewBorder(nil, container.NewPadded(inputBar), nil, nil, cv.tabs)
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
			cv.rebuildAgentTabs()
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

// ── Main chat messages ───────────────────────────────

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

// ── Agent tabs ───────────────────────────────────────

func (cv *ChatView) rebuildAgentTabs() {
	cv.ui.RemoveStalePanels()

	if !cv.ui.IsAgentDirty() {
		return
	}
	cv.ui.ClearAgentDirty()

	panels := cv.ui.GetAgentPanels()
	activeIDs := make(map[string]bool)

	for _, panel := range panels {
		activeIDs[panel.ID] = true

		// Build panel content.
		vbox := container.NewVBox()
		cv.renderAgentPanel(panel, vbox)

		scr, exists := cv.agentScrolls[panel.ID]
		if !exists {
			scr = container.NewVScroll(vbox)
			cv.agentScrolls[panel.ID] = scr
		} else {
			scr.Content = vbox
			scr.Refresh()
		}

		tabName := panel.Name
		if tabName == "" {
			tabName = panel.ID
		}
		// Add status indicator.
		switch panel.Status {
		case "running", "working":
			tabName += " *"
		}

		item, exists := cv.tabMap[panel.ID]
		if !exists {
			item = container.NewTabItem(tabName, scr)
			cv.tabMap[panel.ID] = item
			cv.tabs.Append(item)
		} else {
			item.Text = tabName
			item.Content = scr
		}

		scr.Refresh()
	}

	// Remove tabs for stale panels.
	for id, item := range cv.tabMap {
		if !activeIDs[id] {
			cv.tabs.Remove(item)
			delete(cv.tabMap, id)
			delete(cv.agentScrolls, id)
		}
	}

	cv.tabs.Refresh()
}

// renderAgentPanel renders all events in an agent panel into the vbox.
// Uses the same message rendering style as the main chat.
func (cv *ChatView) renderAgentPanel(panel AgentPanelData, vbox *fyne.Container) {
	objects := make([]fyne.CanvasObject, 0, len(panel.Events)+2)

	// Header: name + status.
	statusColor := theme.ColorNameSuccess
	switch panel.Status {
	case "running", "working":
		statusColor = theme.ColorNameWarning
	case "failed":
		statusColor = theme.ColorNameError
	case "idle":
		if panel.Result != "" {
			statusColor = theme.ColorNameSuccess
		} else {
			statusColor = theme.ColorNameDisabled
		}
	}

	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  panel.Task,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor},
			Text:  "  " + panel.Status,
		},
	)
	objects = append(objects, container.NewPadded(header))

	// Render events using same style as main chat.
	var pendingToolEvent *AgentEventEntry

	for i := range panel.Events {
		ev := &panel.Events[i]

		// Flush pending tool result.
		if pendingToolEvent != nil && ev.Type == "tool_result" {
			objects = append(objects, cv.renderToolFromEvent(pendingToolEvent, ev.Content))
			pendingToolEvent = nil
			continue
		}
		if pendingToolEvent != nil {
			// Tool had no matching result — show as running.
			objects = append(objects, cv.renderToolFromEvent(pendingToolEvent, ""))
			pendingToolEvent = nil
		}

		switch ev.Type {
		case "text":
			if ev.Content != "" {
				rt := widget.NewRichTextFromMarkdown(ev.Content)
				rt.Wrapping = fyne.TextWrapWord
				objects = append(objects, container.NewPadded(widget.NewCard("", "", container.NewPadded(rt))))
			}
		case "tool_call":
			pendingToolEvent = ev
		case "error":
			text := canvas.NewText("Error: "+ev.Content, theme.ErrorColor())
			text.TextSize = theme.TextSize()
			objects = append(objects, container.NewPadded(text))
		}
	}
	if pendingToolEvent != nil {
		objects = append(objects, cv.renderToolFromEvent(pendingToolEvent, ""))
	}

	// Final result if completed.
	if panel.Result != "" && (panel.Status == "completed" || panel.Status == "failed" || panel.Status == "idle") {
		resultMd := "```\n" + panel.Result + "\n```"
		rt := widget.NewRichTextFromMarkdown(resultMd)
		rt.Wrapping = fyne.TextWrapWord
		objects = append(objects, container.NewPadded(widget.NewCard("Result", "", container.NewPadded(rt))))
	}

	vbox.Objects = objects
	vbox.Refresh()
}

// renderToolFromEvent renders a tool_call + optional result using main chat style.
func (cv *ChatView) renderToolFromEvent(toolEv *AgentEventEntry, result string) fyne.CanvasObject {
	msg := &ChatMessage{
		ToolName: toolEv.ToolName,
		ToolArgs: toolEv.ToolArgs,
		Content:  result,
		ToolDesc: toolEv.Content,
	}

	// Determine status.
	if result == "" {
		// running
	} else {
		// done — check for error content
	}

	switch msg.ToolName {
	case "run_command", "start_command":
		return cv.commandToolItem(msg, msg.ToolDesc, statusText(msg), statusColor(msg))
	default:
		displayTitle := msg.ToolDesc
		if displayTitle == "" || displayTitle == msg.ToolName {
			displayTitle = msg.ToolName
		}
		return cv.genericToolItem(msg, displayTitle, statusText(msg), statusColor(msg))
	}
}

// ── Message widgets (shared by main chat + agent panels) ──

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

func statusText(msg *ChatMessage) string {
	if msg.Content == "" {
		return "running..."
	}
	if msg.IsError {
		return "failed"
	}
	return "done"
}

func statusColor(msg *ChatMessage) fyne.ThemeColorName {
	if msg.Content == "" {
		return theme.ColorNameWarning
	}
	if msg.IsError {
		return theme.ColorNameError
	}
	return theme.ColorNameSuccess
}

func (cv *ChatView) userBubble(msg *ChatMessage) fyne.CanvasObject {
	rt := widget.NewRichTextWithText(msg.Content)
	rt.Wrapping = fyne.TextWrapWord
	return container.NewPadded(widget.NewCard("", "", container.NewPadded(rt)))
}

func (cv *ChatView) assistantBubble(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}
	rt := widget.NewRichTextFromMarkdown(text)
	rt.Wrapping = fyne.TextWrapWord
	return container.NewPadded(widget.NewCard("", "", container.NewPadded(rt)))
}

func (cv *ChatView) toolItem(msg *ChatMessage) fyne.CanvasObject {
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

// commandToolItem renders run_command with markdown code blocks.
func (cv *ChatView) commandToolItem(msg *ChatMessage, displayTitle, status string, statusColor fyne.ThemeColorName) fyne.CanvasObject {
	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  displayTitle,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor},
			Text:  "  " + status,
		},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, container.NewPadded(header))

	if msg.ToolArgs != "" {
		cmdMd := "```bash\n" + msg.ToolArgs + "\n```"
		cmdBlock := widget.NewRichTextFromMarkdown(cmdMd)
		cmdBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.NewPadded(cmdBlock))
	}

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

func (cv *ChatView) agentItem(msg *ChatMessage) fyne.CanvasObject {
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
	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor(msg)},
			Text:  "  " + statusText(msg),
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

func (cv *ChatView) sendMessageItem(msg *ChatMessage) fyne.CanvasObject {
	to := extractJSONField(msg.ToolArgs, "to")
	summary := extractJSONField(msg.ToolArgs, "summary")
	headerText := "Send to: " + to
	if summary != "" {
		headerText = summary + "  →  " + to
	}
	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor(msg)},
			Text:  "  " + statusText(msg),
		},
	)
	return widget.NewCard("", "", container.NewVBox(container.NewPadded(header)))
}

func (cv *ChatView) waitAgentItem(msg *ChatMessage) fyne.CanvasObject {
	agentID := extractJSONField(msg.ToolArgs, "agent_id")
	headerText := "Waiting for: " + agentID
	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor(msg)},
			Text:  "  " + statusText(msg),
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

func (cv *ChatView) teammateSpawnItem(msg *ChatMessage) fyne.CanvasObject {
	name := extractJSONField(msg.ToolArgs, "name")
	if name == "" {
		name = "teammate"
	}
	teamID := extractJSONField(msg.ToolArgs, "team_id")
	headerText := "Teammate: " + name
	if teamID != "" && len(teamID) > 8 {
		headerText += "  (team: " + teamID[:8] + ")"
	}
	header := widget.NewRichText(
		&widget.TextSegment{
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			Text:  headerText,
		},
		&widget.TextSegment{
			Style: widget.RichTextStyle{ColorName: statusColor(msg)},
			Text:  "  " + statusText(msg),
		},
	)
	return widget.NewCard("", "", container.NewVBox(container.NewPadded(header)))
}

func (cv *ChatView) systemNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText(msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	text.Alignment = fyne.TextAlignCenter
	return container.NewPadded(text)
}

func (cv *ChatView) reasoningNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Thinking: "+msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	return container.NewPadded(text)
}

func (cv *ChatView) errorNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Error: "+msg.Content, theme.ErrorColor())
	text.TextSize = theme.TextSize()
	return container.NewPadded(text)
}
