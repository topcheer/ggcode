package main

import (
	"encoding/json"
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

	scroll *container.Scroll
	vbox   *fyne.Container

	tabs         *container.AppTabs
	tabMap       map[string]*container.TabItem
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
			cv.updateStatusBar(working)
		})
	}
}

func (cv *ChatView) updateButtons(working bool) {
	if working {
		cv.cancelBtn.Show()
	} else {
		cv.cancelBtn.Hide()
	}
	cv.sendBtn.Show()
	cv.sendBtn.Enable()
}

func (cv *ChatView) onSend() {
	text := strings.TrimSpace(cv.entry.Text)
	if text == "" {
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

	if cv.bridge.IsWorking() {
		// Agent busy — queue as pending message (sent after current turn).
		cv.bridge.QueueMessage(text)
		cv.ui.AppendChat(ChatMessage{
			Role:    "system",
			Content: "(queued — will be sent after current response)",
			Time:    time.Now(),
		})
		return
	}

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

		tabName := truncateTabName(panel.Name, len(panels))
		if tabName == "" {
			tabName = truncateTabName(panel.ID, len(panels))
		}
		if panel.Status == "running" || panel.Status == "working" {
			tabName += "*"
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

	for id, item := range cv.tabMap {
		if !activeIDs[id] {
			cv.tabs.Remove(item)
			delete(cv.tabMap, id)
			delete(cv.agentScrolls, id)
		}
	}
	cv.tabs.Refresh()
}

// renderAgentPanel renders all events in an agent panel using same message style.
func (cv *ChatView) renderAgentPanel(panel AgentPanelData, vbox *fyne.Container) {
	objects := make([]fyne.CanvasObject, 0, len(panel.Events)+2)

	// Header with status.
	statusColor := theme.ColorNameSuccess
	if panel.Status == "running" || panel.Status == "working" {
		statusColor = theme.ColorNameWarning
	} else if panel.Status == "failed" {
		statusColor = theme.ColorNameError
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: panel.Task},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor}, Text: "  " + panel.Status},
	)
	objects = append(objects, cv.iconRow(theme.ComputerIcon(), header))

	var pendingTool *AgentEventEntry
	for i := range panel.Events {
		ev := &panel.Events[i]
		if pendingTool != nil && ev.Type == "tool_result" {
			objects = append(objects, cv.renderToolFromEvent(pendingTool, ev.Content))
			pendingTool = nil
			continue
		}
		if pendingTool != nil {
			objects = append(objects, cv.renderToolFromEvent(pendingTool, ""))
			pendingTool = nil
		}
		switch ev.Type {
		case "text":
			if ev.Content != "" {
				rt := widget.NewRichTextFromMarkdown(ev.Content)
				rt.Wrapping = fyne.TextWrapWord
				objects = append(objects, cv.iconRow(theme.ComputerIcon(), rt))
			}
		case "tool_call":
			pendingTool = ev
		case "error":
			text := canvas.NewText(ev.Content, theme.ErrorColor())
			text.TextSize = theme.TextSize()
			objects = append(objects, cv.iconRow(theme.CancelIcon(), text))
		}
	}
	if pendingTool != nil {
		objects = append(objects, cv.renderToolFromEvent(pendingTool, ""))
	}

	if panel.Result != "" && (panel.Status == "completed" || panel.Status == "failed" || panel.Status == "idle") {
		resultMd := "```\n" + panel.Result + "\n```"
		rt := widget.NewRichTextFromMarkdown(resultMd)
		rt.Wrapping = fyne.TextWrapWord
		objects = append(objects, cv.iconRow(theme.ComputerIcon(), rt))
	}

	vbox.Objects = objects
	vbox.Refresh()
}

func (cv *ChatView) renderToolFromEvent(toolEv *AgentEventEntry, result string) fyne.CanvasObject {
	msg := &ChatMessage{ToolName: toolEv.ToolName, ToolArgs: toolEv.ToolArgs, Content: result, ToolDesc: toolEv.Content}
	// Reuse the same toolItem renderer as the main chat for consistent styling.
	return cv.toolItem(msg)
}

// ── Shared helpers ───────────────────────────────────

// iconRow creates a compact row with an icon prefix and content.
func (cv *ChatView) iconRow(icon fyne.Resource, content fyne.CanvasObject) fyne.CanvasObject {
	ic := widget.NewIcon(icon)
	ic.Resize(fyne.NewSize(16, 16))
	row := container.NewBorder(nil, nil, ic, nil, content)
	row.Refresh()
	return row
}

// toolIcon returns the icon for a tool's current status.
func toolIcon(msg *ChatMessage) fyne.Resource {
	if msg.Content == "" {
		return theme.MediaRecordIcon() // running (red dot)
	}
	if msg.IsError {
		return theme.CancelIcon() // failed (X)
	}
	return theme.ConfirmIcon() // done (check)
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

func (cv *ChatView) userBubble(msg *ChatMessage) fyne.CanvasObject {
	rt := widget.NewRichTextFromMarkdown(msg.Content)
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.AccountIcon(), rt)
}

func (cv *ChatView) assistantBubble(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}
	rt := widget.NewRichTextFromMarkdown(text)
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.ComputerIcon(), rt)
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
	case "swarm_task_create":
		return cv.swarmTaskCreateItem(msg)
	case "todo_write":
		return cv.todoWriteItem(msg)
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

func (cv *ChatView) commandToolItem(msg *ChatMessage, displayTitle, status string, statusColor fyne.ThemeColorName) fyne.CanvasObject {
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: displayTitle},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor}, Text: "  " + status},
	)
	header.Wrapping = fyne.TextWrapBreak

	var parts []fyne.CanvasObject
	parts = append(parts, header)

	if msg.ToolArgs != "" {
		cmdMd := "```bash\n" + msg.ToolArgs + "\n```"
		cmdBlock := widget.NewRichTextFromMarkdown(cmdMd)
		cmdBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, cmdBlock)
	}

	if msg.Content != "" {
		result := msg.Content
		if len([]rune(result)) > 3000 {
			result = truncateRunes(result, 3000, "\n...(truncated)")
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, resultBlock)
	} else {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		parts = append(parts, spinner)
	}

	vbox := container.NewVBox(parts...)
	return cv.iconRow(toolIcon(msg), vbox)
}

func (cv *ChatView) genericToolItem(msg *ChatMessage, displayTitle, status string, statusColor fyne.ThemeColorName) fyne.CanvasObject {
	if msg.ToolArgs != "" {
		displayTitle = displayTitle + "  " + msg.ToolArgs
	}
	header := fmt.Sprintf("%s  (%s)", displayTitle, status)

	result := msg.Content
	if len([]rune(result)) > 1000 {
		result = truncateRunes(result, 1000, "\n...(truncated)")
	}

	var detail fyne.CanvasObject
	if result == "" {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		detail = spinner
	} else {
		resultLabel := widget.NewLabel(result)
		resultLabel.Wrapping = fyne.TextWrapWord
		resultLabel.TextStyle = fyne.TextStyle{Monospace: true}
		detail = resultLabel
	}

	acc := widget.NewAccordion(widget.NewAccordionItem(header, detail))
	acc.MultiOpen = true
	return cv.iconRow(toolIcon(msg), acc)
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
	if len([]rune(taskDesc)) > 120 {
		taskDesc = truncateRunes(taskDesc, 120, "...")
	}

	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: "Agent: " + agentName},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor(msg)}, Text: "  " + statusText(msg)},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, header)
	if taskDesc != "" {
		taskLabel := widget.NewLabel(taskDesc)
		taskLabel.Wrapping = fyne.TextWrapWord
		parts = append(parts, taskLabel)
	}
	if msg.Content != "" {
		result := msg.Content
		if len([]rune(result)) > 2000 {
			result = truncateRunes(result, 2000, "\n...(truncated)")
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, resultBlock)
	} else {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		parts = append(parts, spinner)
	}

	return cv.iconRow(toolIcon(msg), container.NewVBox(parts...))
}

func (cv *ChatView) sendMessageItem(msg *ChatMessage) fyne.CanvasObject {
	to := extractJSONField(msg.ToolArgs, "to")
	summary := extractJSONField(msg.ToolArgs, "summary")
	message := extractJSONField(msg.ToolArgs, "message")

	headerText := "Send to: " + to
	if summary != "" {
		headerText = summary
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: headerText},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor(msg)}, Text: "  " + statusText(msg)},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, header)
	if message != "" {
		desc := widget.NewRichTextFromMarkdown(message)
		desc.Wrapping = fyne.TextWrapWord
		parts = append(parts, desc)
	}

	return cv.iconRow(toolIcon(msg), container.NewVBox(parts...))
}

func (cv *ChatView) waitAgentItem(msg *ChatMessage) fyne.CanvasObject {
	agentID := extractJSONField(msg.ToolArgs, "agent_id")
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: "Waiting for: " + agentID},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor(msg)}, Text: "  " + statusText(msg)},
	)
	var parts []fyne.CanvasObject
	parts = append(parts, header)
	if msg.Content != "" {
		result := msg.Content
		if len([]rune(result)) > 2000 {
			result = truncateRunes(result, 2000, "\n...(truncated)")
		}
		resultMd := "```\n" + result + "\n```"
		resultBlock := widget.NewRichTextFromMarkdown(resultMd)
		resultBlock.Wrapping = fyne.TextWrapWord
		parts = append(parts, resultBlock)
	}
	return cv.iconRow(toolIcon(msg), container.NewVBox(parts...))
}

func (cv *ChatView) teammateSpawnItem(msg *ChatMessage) fyne.CanvasObject {
	name := extractJSONField(msg.ToolArgs, "name")
	if name == "" {
		name = "teammate"
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: "Teammate: " + name},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor(msg)}, Text: "  " + statusText(msg)},
	)
	return cv.iconRow(toolIcon(msg), header)
}

func (cv *ChatView) swarmTaskCreateItem(msg *ChatMessage) fyne.CanvasObject {
	subject := extractJSONField(msg.ToolArgs, "subject")
	description := extractJSONField(msg.ToolArgs, "description")
	assignee := extractJSONField(msg.ToolArgs, "assignee")

	headerText := "Task"
	if subject != "" {
		headerText = subject
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: headerText},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor(msg)}, Text: "  " + statusText(msg)},
	)

	var parts []fyne.CanvasObject
	parts = append(parts, header)
	if assignee != "" {
		assigneeLabel := widget.NewRichText(
			&widget.TextSegment{Style: widget.RichTextStyle{ColorName: theme.ColorNamePrimary}, Text: "Assigned to: " + assignee},
		)
		parts = append(parts, assigneeLabel)
	}
	if description != "" {
		desc := widget.NewRichTextFromMarkdown(description)
		desc.Wrapping = fyne.TextWrapWord
		parts = append(parts, desc)
	}

	return cv.iconRow(toolIcon(msg), container.NewVBox(parts...))
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
	return cv.iconRow(theme.CancelIcon(), text)
}

// todoWriteItem renders todo_write as plain markdown — no tool name, no accordion.
func (cv *ChatView) todoWriteItem(msg *ChatMessage) fyne.CanvasObject {
	var input struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(msg.ToolArgs), &input); err != nil {
		if msg.Content != "" {
			rt := widget.NewRichTextFromMarkdown(msg.Content)
			rt.Wrapping = fyne.TextWrapWord
			return cv.iconRow(theme.ConfirmIcon(), rt)
		}
		return nil
	}

	var sb strings.Builder
	for _, t := range input.Todos {
		switch t.Status {
		case "done":
			sb.WriteString("- [x] ")
		case "in_progress":
			sb.WriteString("- [ ] **")
			sb.WriteString(t.Content)
			sb.WriteString("** _(in progress)_")
			sb.WriteString("\n")
			continue
		default:
			sb.WriteString("- [ ] ")
		}
		sb.WriteString(t.Content)
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		return nil
	}

	rt := widget.NewRichTextFromMarkdown(sb.String())
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.CheckButtonCheckedIcon(), rt)
}

// truncateTabName shortens a tab name based on total agent count.
// Uses rune counting to avoid breaking multi-byte characters (Chinese, etc.).
func truncateTabName(name string, totalAgents int) string {
	maxLen := 25
	switch {
	case totalAgents <= 3:
		maxLen = 25
	case totalAgents <= 6:
		maxLen = 18
	case totalAgents <= 10:
		maxLen = 12
	default:
		maxLen = 8
	}
	runes := []rune(name)
	if len(runes) <= maxLen {
		return name
	}
	return string(runes[:maxLen-1]) + "…"
}

// lastStatusText avoids unnecessary label updates (prevents flicker).
var lastStatusText string

func (cv *ChatView) updateStatusBar(working bool) {
	tc := cv.bridge.TokenCount()
	cw := cv.bridge.ContextWindow()
	tokenInfo := fmt.Sprintf("%s / %s", humanizeTokens(tc), humanizeTokens(cw))

	var text string
	if working {
		elapsed := cv.bridge.Elapsed()
		text = fmt.Sprintf(">> Working (%s) | %s", elapsed.Round(time.Second), tokenInfo)
	} else {
		text = tokenInfo
	}

	// Only update if text actually changed.
	if text != lastStatusText {
		lastStatusText = text
		// Update the status bar label directly (not via binding).
		// Find it through the bridge's app reference.
		cv.ui.SetStatus(text)
	}
}
