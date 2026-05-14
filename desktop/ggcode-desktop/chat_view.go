package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── sendEntry ────────────────────────────────────────

type sendEntry struct {
	widget.Entry
	onSend func()
	busy   bool
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
		if e.busy {
			return
		}
		if e.isShiftHeld() {
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

func (e *sendEntry) isShiftHeld() bool {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		m := d.CurrentKeyModifiers()
		return m&fyne.KeyModifierShift != 0
	}
	return false
}

// ── ChatView ─────────────────────────────────────────

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
}

func NewChatView(bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{
		bridge:       bridge,
		ui:           ui,
		tabMap:       make(map[string]*container.TabItem),
		agentScrolls: make(map[string]*container.Scroll),
	}

	cv.entry = newSendEntry()
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

	mainTab := container.NewTabItem("Main", cv.scroll)
	cv.tabs = container.NewAppTabs(mainTab)
	cv.tabs.SetTabLocation(container.TabLocationTop)

	go cv.pollRefresh()

	return container.NewBorder(nil, container.NewPadded(inputBar), nil, nil, cv.tabs)
}

// ── Poll loop ────────────────────────────────────────

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
			// Always scroll to bottom when agent is working.
			if working {
				cv.scroll.ScrollToBottom()
			}
			cv.updateButtons(working)
			cv.rebuildAgentTabs()
			cv.updateStatusBar(working)
		})
	}
}

const placeholderIdle = "Message ggcode... (Enter to send, Shift+Enter for newline)"
const placeholderBusy = "ggcode is working... (messages will be queued)"

func (cv *ChatView) updateButtons(working bool) {
	cv.entry.busy = working
	if working {
		cv.cancelBtn.Show()
		cv.entry.PlaceHolder = placeholderBusy
	} else {
		cv.cancelBtn.Hide()
		cv.entry.PlaceHolder = placeholderIdle
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
	cv.entry.Refresh()
	cv.ui.AppendChat(ChatMessage{Role: "user", Content: text, Time: time.Now()})
	if cv.bridge.IsWorking() {
		cv.bridge.QueueMessage(text)
		cv.ui.AppendChat(ChatMessage{Role: "system", Content: "(queued)", Time: time.Now()})
		return
	}
	if err := cv.bridge.Send(text); err != nil {
		cv.ui.AppendChat(ChatMessage{Role: "error", Content: err.Error(), Time: time.Now()})
	}
}

var lastStatusText string

func (cv *ChatView) updateStatusBar(working bool) {
	tc := cv.bridge.TokenCount()
	cw := cv.bridge.ContextWindow()
	info := fmt.Sprintf("%s / %s", humanizeTokens(tc), humanizeTokens(cw))
	var text string
	if working {
		text = fmt.Sprintf(">> Working (%s) | %s", cv.bridge.Elapsed().Round(time.Second), info)
	} else {
		text = info
	}
	if text != lastStatusText {
		lastStatusText = text
		// We're already on UI thread (inside fyne.Do from pollRefresh).
		cv.ui.SetStatusDirect(text)
	}
}

// ── Main chat rebuild ────────────────────────────────

func (cv *ChatView) rebuildMessages() {
	msgs := cv.ui.TakeMessages()
	// Merge consecutive assistant messages into one.
	merged := make([]ChatMessage, 0, len(msgs))
	for i := range msgs {
		if i > 0 && msgs[i].Role == "assistant" && merged[len(merged)-1].Role == "assistant" {
			merged[len(merged)-1].Content += "\n\n" + msgs[i].Content
			continue
		}
		merged = append(merged, msgs[i])
	}
	objs := make([]fyne.CanvasObject, 0, len(merged))
	for i := range merged {
		w := cv.renderMessage(&merged[i])
		if w != nil {
			objs = append(objs, w)
		}
	}
	cv.vbox.Objects = objs
	cv.vbox.Refresh()
}

// ── Message rendering ────────────────────────────────

func (cv *ChatView) renderMessage(msg *ChatMessage) fyne.CanvasObject {
	switch msg.Role {
	case "user":
		return cv.renderUser(msg)
	case "assistant":
		return cv.renderAssistant(msg)
	case "tool":
		return cv.renderTool(msg)
	case "system":
		return cv.renderSystem(msg)
	case "reasoning":
		return cv.renderReasoning(msg)
	case "error":
		return cv.renderError(msg)
	}
	return nil
}

func (cv *ChatView) renderUser(msg *ChatMessage) fyne.CanvasObject {
	rt := widget.NewRichTextFromMarkdown(msg.Content)
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.AccountIcon(), rt)
}

func (cv *ChatView) renderAssistant(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}
	if text == "" {
		return nil
	}
	// Pre-process: Fyne RichText doesn't support GFM tables.
	// Replace table blocks with formatted text representation.
	text = renderMarkdownTables(text)
	rt := widget.NewRichTextFromMarkdown(text)
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.ComputerIcon(), rt)
}

func (cv *ChatView) renderSystem(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText(msg.Content, theme.DisabledColor())
	t.TextStyle = fyne.TextStyle{Italic: true}
	t.TextSize = theme.Size(theme.SizeNameCaptionText)
	t.Alignment = fyne.TextAlignCenter
	return container.NewPadded(t)
}

func (cv *ChatView) renderReasoning(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText("Thinking: "+msg.Content, theme.DisabledColor())
	t.TextStyle = fyne.TextStyle{Italic: true}
	t.TextSize = theme.Size(theme.SizeNameCaptionText)
	return container.NewPadded(t)
}

func (cv *ChatView) renderError(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText("Error: "+msg.Content, theme.ErrorColor())
	t.TextSize = theme.TextSize()
	return cv.iconRow(theme.CancelIcon(), t)
}

// ── Tool rendering (mirrors TUI classifyTool logic) ──

// toolClass mirrors TUI's tool classification.
type toolClass int

const (
	tcBash     toolClass = iota // run_command
	tcFile                      // read/write/edit/notebook_edit
	tcSearch                    // grep/glob/search_files
	tcList                      // list_directory
	tcWeb                       // web_fetch/web_search
	tcGit                       // git_*
	tcCmd                       // start_command, read_command_output, wait_command, etc.
	tcLSP                       // lsp_*
	tcTodo                      // todo_write (special)
	tcAgent                     // spawn_agent
	tcMessage                   // send_message
	tcWait                      // wait_agent
	tcTeammate                  // teammate_spawn/shutdown/list/results
	tcSwarm                     // swarm_task_create/claim/complete/list
	tcSuppress                  // header-only tools (save_memory, config, skill, etc.)
	tcGeneric                   // fallback
)

func classifyToolGUI(name string) toolClass {
	switch name {
	case "run_command", "bash", "Bash":
		return tcBash
	case "read_file", "view", "write_file", "edit_file", "multi_edit_file", "notebook_edit":
		return tcFile
	case "search_files", "grep", "glob", "find":
		return tcSearch
	case "list_directory":
		return tcList
	case "web_fetch", "web_search":
		return tcWeb
	case "git_status", "git_diff", "git_log", "git_show", "git_blame",
		"git_branch_list", "git_remote", "git_stash_list", "git_add",
		"git_commit", "git_stash":
		return tcGit
	case "start_command", "read_command_output", "wait_command",
		"stop_command", "write_command_input", "list_commands":
		return tcCmd
	case "todo_write":
		return tcTodo
	case "spawn_agent":
		return tcAgent
	case "send_message":
		return tcMessage
	case "wait_agent", "list_agents":
		return tcWait
	case "teammate_spawn", "teammate_shutdown", "teammate_list", "teammate_results":
		return tcTeammate
	case "swarm_task_create", "swarm_task_claim", "swarm_task_complete", "swarm_task_list",
		"team_create", "team_delete":
		return tcSwarm
	case "save_memory", "config", "skill",
		"enter_plan_mode", "enter_worktree", "exit_worktree",
		"task_create", "task_get", "task_update", "task_list", "task_stop",
		"cron_create", "cron_delete", "cron_list",
		"list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource",
		"ask_user":
		return tcSuppress
	default:
		if strings.HasPrefix(name, "lsp_") {
			return tcLSP
		}
		if strings.HasPrefix(name, "mcp__") {
			return tcSuppress
		}
		return tcGeneric
	}
}

func (cv *ChatView) renderTool(msg *ChatMessage) fyne.CanvasObject {
	switch classifyToolGUI(msg.ToolName) {
	case tcBash:
		return cv.renderBashTool(msg)
	case tcFile:
		return cv.renderFileTool(msg)
	case tcSearch, tcList, tcWeb:
		return cv.renderHeaderOnlyTool(msg)
	case tcGit:
		return cv.renderGitTool(msg)
	case tcCmd:
		return cv.renderHeaderOnlyTool(msg)
	case tcLSP:
		return cv.renderHeaderOnlyTool(msg)
	case tcTodo:
		return cv.renderTodoTool(msg)
	case tcAgent:
		return cv.renderAgentTool(msg)
	case tcMessage:
		return cv.renderSendMessageTool(msg)
	case tcWait:
		return cv.renderHeaderOnlyTool(msg)
	case tcTeammate:
		return cv.renderHeaderOnlyTool(msg)
	case tcSwarm:
		return cv.renderSwarmTaskTool(msg)
	case tcSuppress:
		return cv.renderHeaderOnlyTool(msg)
	default:
		return cv.renderGenericTool(msg)
	}
}

// ── Tool renderers ───────────────────────────────────

// renderBashTool: description header + command + result in accordion (collapsed by default).
func (cv *ChatView) renderBashTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = "Bash"
	}
	header := cv.toolHeader(desc, msg)

	var accItems []*widget.AccordionItem

	// Extract command from raw JSON args.
	cmd := extractJSONField(raw(msg), "command")
	if cmd != "" {
		cmdBlock := widget.NewRichTextFromMarkdown("```bash\n" + cmd + "\n```")
		cmdBlock.Wrapping = fyne.TextWrapWord
		accItems = append(accItems, widget.NewAccordionItem("Command", cmdBlock))
	}

	if msg.Content != "" {
		result := truncateRunes(msg.Content, 3000, "\n...(truncated)")
		resultBlock := widget.NewRichTextFromMarkdown("```\n" + result + "\n```")
		resultBlock.Wrapping = fyne.TextWrapWord
		accItems = append(accItems, widget.NewAccordionItem("Output", resultBlock))
	}

	if len(accItems) > 0 {
		acc := widget.NewAccordion(accItems...)
		return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
	}
	return cv.iconRow(toolIcon(msg), header)
}

// renderFileTool: header + line count / edit summary + result in accordion.
func (cv *ChatView) renderFileTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), header)
	}

	// Show file result in accordion.
	result := truncateRunes(msg.Content, 3000, "\n...(truncated)")
	resultBlock := widget.NewRichTextFromMarkdown("```\n" + result + "\n```")
	resultBlock.Wrapping = fyne.TextWrapWord
	acc := widget.NewAccordion(widget.NewAccordionItem("Content", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderGitTool: header + result in accordion (for git_diff, git_log, git_status).
func (cv *ChatView) renderGitTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	// git_add, git_commit, git_stash — header only
	switch msg.ToolName {
	case "git_add", "git_commit", "git_stash":
		return cv.iconRow(toolIcon(msg), header)
	}

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), header)
	}

	result := truncateRunes(msg.Content, 2000, "\n...(truncated)")
	resultBlock := widget.NewRichTextFromMarkdown("```\n" + result + "\n```")
	resultBlock.Wrapping = fyne.TextWrapWord
	acc := widget.NewAccordion(widget.NewAccordionItem("Output", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderHeaderOnlyTool: just the header line, no body.
// For: suppress tools, teammate ops, task ops, search, list, web, cmd, LSP.
func (cv *ChatView) renderHeaderOnlyTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// renderGenericTool: header + result in accordion.
func (cv *ChatView) renderGenericTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), header)
	}

	result := truncateRunes(msg.Content, 1000, "\n...(truncated)")
	resultBlock := widget.NewLabel(result)
	resultBlock.Wrapping = fyne.TextWrapWord
	resultBlock.TextStyle = fyne.TextStyle{Monospace: true}
	acc := widget.NewAccordion(widget.NewAccordionItem("Result", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderTodoTool: checkbox list, no tool name header.
func (cv *ChatView) renderTodoTool(msg *ChatMessage) fyne.CanvasObject {
	var input struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	args := msg.ToolArgs
	if args == "" {
		args = msg.Content
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil || len(input.Todos) == 0 {
		return nil
	}
	var sb strings.Builder
	for _, t := range input.Todos {
		switch t.Status {
		case "done":
			sb.WriteString("- [x] " + t.Content)
		case "in_progress":
			sb.WriteString("- [ ] **" + t.Content + "** _(in progress)_")
		default:
			sb.WriteString("- [ ] " + t.Content)
		}
		sb.WriteString("\n")
	}
	rt := widget.NewRichTextFromMarkdown(sb.String())
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.CheckButtonCheckedIcon(), rt)
}

// renderAgentTool: agent name + task description.
func (cv *ChatView) renderAgentTool(msg *ChatMessage) fyne.CanvasObject {
	name := extractJSONField(raw(msg), "name")
	if name == "" {
		name = extractJSONField(raw(msg), "subagent_type")
	}
	if name == "" {
		name = "agent"
	}
	task := truncateRunes(extractJSONField(raw(msg), "task"), 100, "...")
	desc := "Agent: " + name
	if task != "" {
		desc += " — " + task
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// renderSendMessageTool: to + summary + message preview.
func (cv *ChatView) renderSendMessageTool(msg *ChatMessage) fyne.CanvasObject {
	to := extractJSONField(raw(msg), "to")
	summary := extractJSONField(raw(msg), "summary")
	desc := "Send to: " + to
	if summary != "" {
		desc = summary
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// renderSwarmTaskTool: subject + assignee + description.
func (cv *ChatView) renderSwarmTaskTool(msg *ChatMessage) fyne.CanvasObject {
	subject := extractJSONField(raw(msg), "subject")
	assignee := extractJSONField(raw(msg), "assignee")
	desc := "Task"
	if subject != "" {
		desc = subject
	}
	if assignee != "" {
		desc += " -> " + assignee
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// ── Shared helpers ───────────────────────────────────

func (cv *ChatView) iconRow(icon fyne.Resource, content fyne.CanvasObject) fyne.CanvasObject {
	ic := widget.NewIcon(icon)
	ic.Resize(fyne.NewSize(16, 16))
	return container.NewBorder(nil, nil, ic, nil, content)
}

func (cv *ChatView) toolHeader(desc string, msg *ChatMessage) *widget.RichText {
	badge := "done"
	if msg.Content == "" {
		badge = "running..."
	} else if msg.IsError {
		badge = "failed"
	}
	md := "**" + desc + "** `" + badge + "`"
	rt := widget.NewRichTextFromMarkdown(md)
	rt.Wrapping = fyne.TextWrapBreak
	return rt
}

func raw(msg *ChatMessage) string {
	if msg.ToolRaw != "" {
		return msg.ToolRaw
	}
	return msg.ToolArgs
}

func toolIcon(msg *ChatMessage) fyne.Resource {
	if msg.Content == "" {
		return theme.MediaRecordIcon()
	}
	if msg.IsError {
		return theme.CancelIcon()
	}
	return theme.ConfirmIcon()
}

func prettifyToolName(name string) string {
	m := map[string]string{
		"run_command": "Bash", "read_file": "Read", "write_file": "Write",
		"edit_file": "Edit", "multi_edit_file": "Edit", "search_files": "Grep",
		"glob": "Glob", "find": "Glob", "list_directory": "List",
		"web_search": "Search", "web_fetch": "Fetch",
		"start_command": "Bash", "stop_command": "Stop",
		"read_command_output": "Output", "wait_command": "Wait",
		"write_command_input": "Input", "list_commands": "Jobs",
		"todo_write": "To-Do", "spawn_agent": "Agent",
		"send_message": "Send", "wait_agent": "Wait",
		"list_agents": "Agents", "teammate_spawn": "Teammate",
		"teammate_shutdown": "Shutdown", "teammate_list": "Teammates",
		"teammate_results": "Results", "swarm_task_create": "Task",
		"swarm_task_claim": "Claim", "swarm_task_complete": "Complete",
		"swarm_task_list": "Tasks", "team_create": "Team",
		"save_memory": "Memory", "config": "Config", "skill": "Skill",
		"git_status": "Git Status", "git_diff": "Git Diff",
		"git_log": "Git Log", "git_show": "Git Show",
		"git_blame": "Git Blame", "git_add": "Git Add",
		"git_commit": "Git Commit", "git_stash": "Git Stash",
		"git_branch_list": "Branches", "git_remote": "Remote",
		"notebook_edit": "Notebook",
	}
	if v, ok := m[name]; ok {
		return v
	}
	if strings.HasPrefix(name, "lsp_") {
		return strings.Title(name[4:])
	}
	if len(name) > 0 {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return name
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

	// Auto-scroll all agent panels to bottom.
	for _, scr := range cv.agentScrolls {
		scr.ScrollToBottom()
	}
}

func (cv *ChatView) renderAgentPanel(panel AgentPanelData, vbox *fyne.Container) {
	objs := make([]fyne.CanvasObject, 0, len(panel.Events)+2)

	statusStr := panel.Status
	statusColor := theme.ColorNameSuccess
	if panel.Status == "running" || panel.Status == "working" {
		statusColor = theme.ColorNameWarning
	} else if panel.Status == "failed" {
		statusColor = theme.ColorNameError
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: panel.Task},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor}, Text: "  " + statusStr},
	)
	objs = append(objs, cv.iconRow(theme.ComputerIcon(), header))

	var pendingTool *AgentEventEntry
	for i := range panel.Events {
		ev := &panel.Events[i]
		if pendingTool != nil && ev.Type == "tool_result" {
			if w := cv.renderToolFromAgentEvent(pendingTool, ev.Content); w != nil {
				objs = append(objs, w)
			}
			pendingTool = nil
			continue
		}
		if pendingTool != nil {
			if w := cv.renderToolFromAgentEvent(pendingTool, ""); w != nil {
				objs = append(objs, w)
			}
			pendingTool = nil
		}
		switch ev.Type {
		case "text":
			if ev.Content != "" {
				rt := widget.NewRichTextFromMarkdown(ev.Content)
				rt.Wrapping = fyne.TextWrapWord
				objs = append(objs, cv.iconRow(theme.ComputerIcon(), rt))
			}
		case "tool_call":
			pendingTool = ev
		case "error":
			t := canvas.NewText(ev.Content, theme.ErrorColor())
			t.TextSize = theme.TextSize()
			objs = append(objs, cv.iconRow(theme.CancelIcon(), t))
		}
	}
	if pendingTool != nil {
		if w := cv.renderToolFromAgentEvent(pendingTool, ""); w != nil {
			objs = append(objs, w)
		}
	}

	if panel.Result != "" {
		rt := widget.NewRichTextFromMarkdown("```\n" + panel.Result + "\n```")
		rt.Wrapping = fyne.TextWrapWord
		objs = append(objs, cv.iconRow(theme.ComputerIcon(), rt))
	}

	vbox.Objects = objs
	vbox.Refresh()
}

func (cv *ChatView) renderToolFromAgentEvent(toolEv *AgentEventEntry, result string) fyne.CanvasObject {
	// Build ChatMessage exactly like the main panel does in agent_bridge.go.
	// Main panel: toolDescription(name, rawArgs) + toolArgSummary(name, rawArgs) + rawArgs
	rawArgs := toolEv.ToolArgs
	name := toolEv.ToolName
	msg := &ChatMessage{
		Role:     "tool",
		ToolName: name,
		ToolDesc: toolDescription(name, rawArgs),
		ToolArgs: toolArgSummary(name, rawArgs),
		ToolRaw:  rawArgs,
		Content:  result,
	}
	return cv.renderTool(msg)
}

// ── Tab name truncation ──────────────────────────────

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

// renderMarkdownTables converts GFM table blocks to formatted text
// since Fyne's RichText doesn't support table rendering.
func renderMarkdownTables(md string) string {
	lines := strings.Split(md, "\n")
	var result []string
	var tableLines []string
	inTable := false

	for _, line := range lines {
		if isTableLine(line) {
			if !inTable {
				inTable = true
				tableLines = []string{line}
			} else {
				tableLines = append(tableLines, line)
			}
		} else {
			if inTable {
				result = append(result, formatTable(tableLines)...)
				tableLines = nil
				inTable = false
			}
			result = append(result, line)
		}
	}
	if inTable {
		result = append(result, formatTable(tableLines)...)
	}
	return strings.Join(result, "\n")
}

func isTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Table rows start and end with |
	if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
		return true
	}
	// Separator line like |---|---|
	if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "---") {
		return true
	}
	return false
}

func formatTable(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}

	// Parse all rows (skip separator rows).
	var rows [][]string
	for _, line := range lines {
		if isSeparatorRow(line) {
			continue
		}
		cells := parseCells(line)
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	if len(rows) == 0 {
		return lines
	}

	// Calculate column widths (use rune width for CJK).
	numCols := 0
	for _, r := range rows {
		if len(r) > numCols {
			numCols = len(r)
		}
	}
	widths := make([]int, numCols)
	for _, r := range rows {
		for i, c := range r {
			w := runeWidth(c)
			if w > widths[i] {
				widths[i] = w
			}
		}
	}

	// Format rows as code block.
	var sb strings.Builder
	for ri, r := range rows {
		for i := 0; i < numCols; i++ {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			w := runeWidth(cell)
			pad := widths[i] - w
			if pad < 0 {
				pad = 0
			}
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(cell + strings.Repeat(" ", pad))
		}
		sb.WriteString("\n")

		// Add separator after header row.
		if ri == 0 {
			for i := 0; i < numCols; i++ {
				if i > 0 {
					sb.WriteString("  ")
				}
				sb.WriteString(strings.Repeat("─", widths[i]))
			}
			sb.WriteString("\n")
		}
	}

	return []string{"```\n" + sb.String() + "```"}
}

// runeWidth returns display width of string (CJK chars = 2).
func runeWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115F || r <= 0x11A2 ||
			(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE6F) ||
			(r >= 0xFF01 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x20000 && r <= 0x2FFFD) ||
			(r >= 0x30000 && r <= 0x3FFFD)) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isSeparatorRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	for _, ch := range trimmed {
		if ch != '-' && ch != ' ' && ch != ':' {
			return false
		}
	}
	return true
}

func parseCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		result = append(result, strings.TrimSpace(p))
	}
	return result
}
