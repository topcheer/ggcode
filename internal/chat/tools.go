package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"

	"github.com/topcheer/ggcode/internal/markdown"
)

// BaseToolItem provides shared rendering logic for all tool items.
// Concrete tool types embed this and override RenderBody/RenderParams.
type BaseToolItem struct {
	CachedItem
	id           string
	toolName     string
	status       ToolStatus
	input        string // raw JSON input
	result       string // result text (may contain error)
	isError      bool
	markdownBody bool // render result as markdown
	styles       Styles
}

// NewBaseToolItem creates a base tool item.
func NewBaseToolItem(id, toolName string, status ToolStatus, input string, styles Styles) *BaseToolItem {
	return &BaseToolItem{
		id:       id,
		toolName: toolName,
		status:   status,
		input:    input,
		styles:   styles,
	}
}

func (t *BaseToolItem) ID() string { return t.id }

// SetStatus updates the tool status and invalidates cache.
func (t *BaseToolItem) SetStatus(s ToolStatus) {
	if t.status != s {
		t.status = s
		t.Invalidate()
	}
}

// SetResult updates the tool result and invalidates cache.
func (t *BaseToolItem) SetResult(result string, isError bool) {
	t.result = result
	t.isError = isError
	t.Invalidate()
}

// ToolName returns the tool name.
func (t *BaseToolItem) ToolName() string { return t.toolName }

// Status returns the current tool status.
func (t *BaseToolItem) Status() ToolStatus { return t.status }

// Input returns the raw input JSON.
func (t *BaseToolItem) Input() string { return t.input }

// RenderParams extracts display parameters from the tool input.
// Override in concrete types for better param extraction.
func (t *BaseToolItem) RenderParams() string {
	// Default: try to extract a "path" or "command" field
	var m map[string]any
	if err := json.Unmarshal([]byte(t.input), &m); err == nil {
		if path, ok := m["path"].(string); ok && path != "" {
			return path
		}
		if fp, ok := m["file_path"].(string); ok && fp != "" {
			return fp
		}
		if cmd, ok := m["command"].(string); ok && cmd != "" {
			return cmd
		}
		if query, ok := m["query"].(string); ok && query != "" {
			return query
		}
		if pattern, ok := m["pattern"].(string); ok && pattern != "" {
			return pattern
		}
	}
	// Fallback: first N chars of input
	s := strings.TrimSpace(t.input)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:59] + "…"
	}
	return s
}

// RenderBody renders the tool output body.
// Override in concrete types for specialized body rendering.
func (t *BaseToolItem) RenderBody(width int) string {
	if t.result == "" {
		return ""
	}

	if t.isError {
		return t.styles.ErrorStyle.Render(t.result)
	}

	if t.markdownBody {
		rendered := markdown.Render(t.result, width)
		return t.styles.ToolBody.Render(strings.TrimSuffix(rendered, "\n"))
	}

	body, _ := FormatBody(t.result, width, ToolBodyMaxLines)
	return t.styles.ToolBody.Render(body)
}

// Render produces the full tool output: header + optional body.
// This is the base implementation. Concrete types should call renderCore
// with their own params/body overrides.
func (t *BaseToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// renderCore builds the full tool output string from header params and body.
func (t *BaseToolItem) renderCore(width int, params string, body string) string {
	var sb strings.Builder
	sb.WriteString(t.styles.ToolHeader(t.status, t.toolName, width, params))
	if body != "" {
		sb.WriteString("\n")
		for _, line := range strings.Split(body, "\n") {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (t *BaseToolItem) Height(width int) int {
	if _, h, ok := t.GetCached(width); ok {
		return h
	}
	return measureHeight(t.Render(width))
}

// --- Specific Tool Types ---

// BashToolItem renders bash command execution.
type BashToolItem struct {
	BaseToolItem
	command string
}

// NewBashToolItem creates a new bash tool item.
// PrettifyToolName converts internal tool names to display names.
// e.g. "run_command" → "Bash", "read_file" → "Read", "search_files" → "Grep"
func PrettifyToolName(name string) string {
	m := map[string]string{
		"run_command":        "Bash",
		"bash":               "Bash",
		"start_command":      "Bash",
		"read_file":          "Read",
		"view":               "Read",
		"write_file":         "Write",
		"edit_file":          "Edit",
		"multiEdit":          "Edit",
		"multi_edit_file":    "Edit",
		"search_files":       "Grep",
		"find":               "Glob",
		"glob":               "Glob",
		"todo_write":         "To-Do",
		"lsp_definition":     "Definition",
		"lsp_references":     "References",
		"lsp_hover":          "Hover",
		"lsp_diagnostics":    "Diagnostics",
		"lsp_implementation": "Implementation",
		"lsp_rename":         "Rename",
		"web_search":         "Search",
		"web_fetch":          "Fetch",
		"task_dispatch":      "Agent",
	}
	if pretty, ok := m[name]; ok {
		return pretty
	}
	// Fallback: capitalize first letter
	if len(name) > 0 {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return name
}

func NewBashToolItem(id, displayName, command string, status ToolStatus, styles Styles) *BashToolItem {
	b := NewBaseToolItem(id, displayName, status, command, styles)
	result := &BashToolItem{BaseToolItem: *b, command: command}
	return result
}

func (t *BashToolItem) RenderParams() string {
	return t.command
}

// RenderBody uses BashBody style for command output.
func (t *BashToolItem) RenderBody(width int) string {
	if t.result == "" {
		return ""
	}
	if t.isError {
		return t.styles.ErrorStyle.Render(t.result)
	}
	body, _ := FormatBody(t.result, width, ToolBodyMaxLines)
	return t.styles.BashBody.Render(body)
}

func (t *BashToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// FileToolItem renders file read/write/edit operations.
type FileToolItem struct {
	BaseToolItem
	filePath string
	toolType string // "Read", "Write", "Edit", "MultiEdit"
}

// NewFileToolItem creates a new file operation tool item.
func NewFileToolItem(id, displayName, filePath string, status ToolStatus, styles Styles) *FileToolItem {
	b := NewBaseToolItem(id, displayName, status, filePath, styles)
	return &FileToolItem{
		BaseToolItem: *b,
		filePath:     filePath,
		toolType:     displayName,
	}
}

func (t *FileToolItem) RenderParams() string {
	return t.filePath
}

func (t *FileToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// SearchToolItem renders grep/glob/ls operations.
type SearchToolItem struct {
	BaseToolItem
	pattern string
}

// NewSearchToolItem creates a new search tool item.
func NewSearchToolItem(id, displayName, pattern string, status ToolStatus, styles Styles) *SearchToolItem {
	b := NewBaseToolItem(id, displayName, status, pattern, styles)
	return &SearchToolItem{BaseToolItem: *b, pattern: pattern}
}

func (t *SearchToolItem) RenderParams() string {
	return t.pattern
}

func (t *SearchToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// --- ListToolItem (list_directory) ---

// ListToolItem renders directory listing operations.
type ListToolItem struct {
	BaseToolItem
	path string
}

func newListToolItem(id, displayName, path string, status ToolStatus, styles Styles) *ListToolItem {
	b := NewBaseToolItem(id, displayName, status, path, styles)
	return &ListToolItem{BaseToolItem: *b, path: path}
}

func (t *ListToolItem) RenderParams() string { return t.path }

func (t *ListToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// --- WebToolItem (web_fetch, web_search) ---

// WebToolItem renders web fetch/search operations.
type WebToolItem struct {
	BaseToolItem
	url string // or query
}

func newWebToolItem(id, displayName, url string, status ToolStatus, styles Styles) *WebToolItem {
	b := NewBaseToolItem(id, displayName, status, url, styles)
	return &WebToolItem{BaseToolItem: *b, url: url}
}

func (t *WebToolItem) RenderParams() string { return t.url }

func (t *WebToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	// WebToolItem: suppress body — only show header
	rendered := t.renderCore(width, t.RenderParams(), "")
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// --- GitToolItem (git_status, git_diff, git_log) ---

// GitToolItem renders git operations with command-output body.
type GitToolItem struct {
	BaseToolItem
	subCmd string // "status", "diff", "log"
}

func newGitToolItem(id, displayName, subCmd string, status ToolStatus, styles Styles) *GitToolItem {
	b := NewBaseToolItem(id, displayName, status, subCmd, styles)
	return &GitToolItem{BaseToolItem: *b, subCmd: subCmd}
}

func (t *GitToolItem) RenderParams() string { return t.subCmd }

// RenderBody uses BashBody style for git output (same dark background).
func (t *GitToolItem) RenderBody(width int) string {
	if t.result == "" {
		return ""
	}
	if t.isError {
		return t.styles.ErrorStyle.Render(t.result)
	}
	body, _ := FormatBody(t.result, width, ToolBodyMaxLines)
	return t.styles.BashBody.Render(body)
}

func (t *GitToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// --- CmdToolItem (background command management) ---

// CmdToolItem renders background command lifecycle operations.
// The detail string comes pre-formatted from describeTool, e.g.:
//   - start_command: "go build ./..."
//   - write_command_input: "[abc12345] → y\n" (input text, most important)
//   - read_command_output: "abc12345"
//   - wait_command: "abc12345 (30s)"
//   - stop_command: "abc12345"
//   - list_commands: "" (no params)
type CmdToolItem struct {
	BaseToolItem
	detail string // pre-formatted from describeTool
}

func newCmdToolItem(id, displayName, detail string, status ToolStatus, styles Styles) *CmdToolItem {
	b := NewBaseToolItem(id, displayName, status, detail, styles)
	return &CmdToolItem{BaseToolItem: *b, detail: detail}
}

func (t *CmdToolItem) RenderParams() string { return t.detail }

// RenderBody uses BashBody style for command output.
func (t *CmdToolItem) RenderBody(width int) string {
	if t.result == "" {
		return ""
	}
	if t.isError {
		return t.styles.ErrorStyle.Render(t.result)
	}
	body, _ := FormatBody(t.result, width, ToolBodyMaxLines)
	return t.styles.BashBody.Render(body)
}

func (t *CmdToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// --- LspToolItem (language server protocol) ---

// LspToolItem renders LSP operations (hover, definition, references, etc.)
type LspToolItem struct {
	BaseToolItem
	location string // "file:line" or "file"
}

func newLspToolItem(id, displayName, location string, status ToolStatus, styles Styles) *LspToolItem {
	b := NewBaseToolItem(id, displayName, status, location, styles)
	return &LspToolItem{BaseToolItem: *b, location: location}
}

func (t *LspToolItem) RenderParams() string { return t.location }

func (t *LspToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}
	rendered := t.renderCore(width, t.RenderParams(), t.RenderBody(width-4))
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

// GenericToolItem is a fallback for unrecognized tools.
type GenericToolItem struct {
	BaseToolItem
}

// NewGenericToolItem creates a generic tool item.
func NewGenericToolItem(id, displayName string, status ToolStatus, detail string, styles Styles) *GenericToolItem {
	b := NewBaseToolItem(id, displayName, status, detail, styles)
	return &GenericToolItem{
		BaseToolItem: *b,
	}
}

// NewMarkdownToolItem creates a tool item that renders its result as markdown.
func NewMarkdownToolItem(id, displayName string, status ToolStatus, detail string, styles Styles) *GenericToolItem {
	b := NewBaseToolItem(id, displayName, status, detail, styles)
	b.markdownBody = true
	return &GenericToolItem{
		BaseToolItem: *b,
	}
}

// NewToolItem creates the appropriate tool item type based on tool name.
// parseToolInputArg extracts a single string argument from raw JSON input.
// Uses map[string]any to correctly handle mixed-type JSON objects
// (e.g. {"command":"ls","timeout":30} where timeout is a number).
func parseToolInputArg(input, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(input), &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// parseToolInputArgAny tries multiple keys and returns the first non-empty match.
func parseToolInputArgAny(input string, keys ...string) string {
	for _, key := range keys {
		if v := parseToolInputArg(input, key); v != "" {
			return v
		}
	}
	return ""
}

// ToolContext carries pre-resolved display information from the caller
// (typically describeTool in the TUI layer). It is the primary data source
// for tool rendering — the caller is responsible for extracting the right
// detail from rawArgs, so NewToolItem no longer needs to do JSON parsing.
type ToolContext struct {
	ToolName    string // original tool name, e.g. "run_command"
	DisplayName string // prettified name, e.g. "Bash"
	Detail      string // extracted detail, e.g. "go build ./..."
	RawArgs     string // raw JSON input (for body rendering / fallback)
}

// toolCategory classifies a tool for rendering purposes.
type toolCategory int

const (
	catBash    toolCategory = iota // command execution
	catFile                        // file read/write/edit
	catSearch                      // grep/glob/find
	catList                        // list_directory
	catWeb                         // web_fetch/web_search
	catGit                         // git_*
	catCmd                         // background command management
	catLSP                         // language server protocol tools
	catGeneric                     // everything else
)

// classifyTool returns the rendering category for a tool name.
func classifyTool(name string) toolCategory {
	switch name {
	case "run_command", "bash", "Bash":
		return catBash
	case "read_file", "Read", "view", "View",
		"write_file", "Write",
		"edit_file", "Edit", "multiEdit", "MultiEdit",
		"multi_edit_file", "notebook_edit":
		return catFile
	case "search_files", "grep", "Grep", "glob", "Glob", "find":
		return catSearch
	case "list_directory":
		return catList
	case "web_fetch", "web_search":
		return catWeb
	case "git_status", "git_diff", "git_log", "git_show",
		"git_blame", "git_branch_list", "git_remote",
		"git_stash_list", "git_add", "git_commit", "git_stash":
		return catGit
	case "start_command", "read_command_output", "wait_command",
		"stop_command", "write_command_input", "list_commands":
		return catCmd
	default:
		// LSP tools (lsp_hover, lsp_definition, etc.)
		if strings.HasPrefix(name, "lsp_") {
			return catLSP
		}
		return catGeneric
	}
}

// NewToolItem creates the appropriate tool item type based on the ToolContext.
// The caller (chatStartTool) is responsible for filling Detail from describeTool;
// NewToolItem uses it directly instead of re-parsing RawArgs.
func NewToolItem(id string, ctx ToolContext, status ToolStatus, styles Styles) Item {
	displayName := ctx.DisplayName
	if displayName == "" {
		displayName = PrettifyToolName(ctx.ToolName)
	}

	switch classifyTool(ctx.ToolName) {
	case catBash:
		return NewBashToolItem(id, displayName, ctx.Detail, status, styles)
	case catFile:
		return NewFileToolItem(id, displayName, ctx.Detail, status, styles)
	case catSearch:
		return NewSearchToolItem(id, displayName, ctx.Detail, status, styles)
	case catList:
		return newListToolItem(id, displayName, ctx.Detail, status, styles)
	case catWeb:
		return newWebToolItem(id, displayName, ctx.Detail, status, styles)
	case catGit:
		return newGitToolItem(id, displayName, ctx.Detail, status, styles)
	case catCmd:
		return newCmdToolItem(id, displayName, ctx.Detail, status, styles)
	case catLSP:
		return newLspToolItem(id, displayName, ctx.Detail, status, styles)
	default:
		if ctx.ToolName == "wait_agent" {
			return NewMarkdownToolItem(id, displayName, status, ctx.Detail, styles)
		}
		return NewGenericToolItem(id, displayName, status, ctx.Detail, styles)
	}
}

// TodoTask represents a single todo/task item.
type TodoTask struct {
	ID      string
	Content string
	Status  string // "done", "in_progress", "pending"
}

// TodoToolItem renders a todo/task list.
type TodoToolItem struct {
		CachedItem
	id     string
	tasks  []TodoTask
	styles Styles
	lang   string
}

// NewTodoToolItem creates a new todo list tool item.
func NewTodoToolItem(id string, tasks []TodoTask, styles Styles, lang string) *TodoToolItem {
	return &TodoToolItem{
		id:     id,
		tasks:  tasks,
		styles: styles,
		lang:   lang,
	}
}

func (t *TodoToolItem) ID() string { return t.id }

// SetTasks updates the task list.
func (t *TodoToolItem) SetTasks(tasks []TodoTask) {
	t.tasks = tasks
	t.Invalidate()
}

func (t *TodoToolItem) Render(width int) string {
	if cached, _, ok := t.GetCached(width); ok {
		return cached
	}

	done, inProgress, pending := 0, 0, 0
	for _, task := range t.tasks {
		switch task.Status {
		case "done":
			done++
		case "in_progress":
			inProgress++
		default:
			pending++
		}
	}

	// Header: ratio + active task
	total := len(t.tasks)
	var active string
	for _, task := range t.tasks {
		if task.Status == "in_progress" {
			active = task.Content
			break
		}
	}

	icon := StatusSuccess
	if done < total {
		icon = StatusRunning
	}
	label := "Todo Progress Update"
	if t.lang == "zh-CN" {
		label = "更新待办事项"
	}
	header := t.styles.ToolHeader(icon, label, width)
	if active != "" {
		maxActive := width - len(header) - 5
		if maxActive < 10 {
			maxActive = 10
		}
		if len(active) > maxActive {
			active = active[:maxActive-1] + "…"
		}
		header += fmt.Sprintf(" · %s", active)
	}

	var sb strings.Builder
	sb.WriteString(header)

	rendered := sb.String()
	t.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

func (t *TodoToolItem) Height(width int) int {
	if _, h, ok := t.GetCached(width); ok {
		return h
	}
	return measureHeight(t.Render(width))
}

// --- AgentToolItem ---

// AgentToolItem renders a subagent with nested tool calls.
type AgentToolItem struct {
	CachedItem
	id          string
	task        string
	status      ToolStatus
	nestedItems []Item
	result      string
	styles      Styles
}

// NewAgentToolItem creates a new agent tool item.
func NewAgentToolItem(id, task string, status ToolStatus, styles Styles) *AgentToolItem {
	return &AgentToolItem{
		id:     id,
		task:   task,
		status: status,
		styles: styles,
	}
}

func (a *AgentToolItem) ID() string { return a.id }

// SetStatus updates the agent status.
func (a *AgentToolItem) SetStatus(s ToolStatus) {
	a.status = s
	a.Invalidate()
}

// SetResult updates the agent result.
func (a *AgentToolItem) SetResult(result string) {
	a.result = result
	a.Invalidate()
}

// AppendNested adds a nested tool item.
func (a *AgentToolItem) AppendNested(item Item) {
	a.nestedItems = append(a.nestedItems, item)
	a.Invalidate()
}

// UpdateNested updates a nested item by ID.
func (a *AgentToolItem) UpdateNested(id string, item Item) {
	for i, it := range a.nestedItems {
		if it.ID() == id {
			a.nestedItems[i] = item
			a.Invalidate()
			return
		}
	}
	a.nestedItems = append(a.nestedItems, item)
	a.Invalidate()
}

func (a *AgentToolItem) Render(width int) string {
	if cached, _, ok := a.GetCached(width); ok {
		return cached
	}

	// Header
	taskDisplay := a.task
	if len(taskDisplay) > width-15 {
		taskDisplay = taskDisplay[:width-16] + "…"
	}
	header := a.styles.ToolHeader(a.status, "Agent", width, taskDisplay)

	if len(a.nestedItems) == 0 {
		rendered := header
		a.SetCached(rendered, width, measureHeight(rendered))
		return rendered
	}

	// Build tree with nested tool calls
	innerWidth := width - 4
	t := tree.Root(header)
	for _, item := range a.nestedItems {
		t.Child(item.Render(innerWidth))
	}

	// Style the tree with rounded enumerator
	enumStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	t.EnumeratorStyle(enumStyle)

	rendered := t.String()
	a.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

func (a *AgentToolItem) Height(width int) int {
	if _, h, ok := a.GetCached(width); ok {
		return h
	}
	return measureHeight(a.Render(width))
}
