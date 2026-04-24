package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
)

// BaseToolItem provides shared rendering logic for all tool items.
// Concrete tool types embed this and override RenderBody/RenderParams.
type BaseToolItem struct {
	CachedItem
	id       string
	toolName string
	status   ToolStatus
	input    string // raw JSON input
	result   string // result text (may contain error)
	isError  bool
	styles   Styles
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

func NewBashToolItem(id, command string, status ToolStatus, styles Styles) *BashToolItem {
	b := NewBaseToolItem(id, "Bash", status, "", styles)
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
func NewFileToolItem(id, toolType, filePath string, status ToolStatus, styles Styles) *FileToolItem {
	b := NewBaseToolItem(id, toolType, status, "", styles)
	return &FileToolItem{
		BaseToolItem: *b,
		filePath:     filePath,
		toolType:     toolType,
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
func NewSearchToolItem(id, toolName, pattern string, status ToolStatus, styles Styles) *SearchToolItem {
	b := NewBaseToolItem(id, toolName, status, "", styles)
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

// GenericToolItem is a fallback for unrecognized tools.
type GenericToolItem struct {
	BaseToolItem
}

// NewGenericToolItem creates a generic tool item.
func NewGenericToolItem(id, toolName string, status ToolStatus, input string, styles Styles) *GenericToolItem {
	return &GenericToolItem{
		BaseToolItem: *NewBaseToolItem(id, toolName, status, input, styles),
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

func NewToolItem(id, toolName string, status ToolStatus, input string, styles Styles) Item {
	pretty := PrettifyToolName(toolName)

	switch {
	case toolName == "bash" || toolName == "Bash" || toolName == "run_command":
		cmd := parseToolInputArg(input, "command")
		return NewBashToolItem(id, cmd, status, styles)

	case toolName == "read" || toolName == "Read" || toolName == "view" || toolName == "View" || toolName == "read_file":
		path := parseToolInputArgAny(input, "path", "file_path")
		return NewFileToolItem(id, pretty, path, status, styles)

	case toolName == "write" || toolName == "Write" || toolName == "write_file":
		path := parseToolInputArgAny(input, "path", "file_path")
		return NewFileToolItem(id, pretty, path, status, styles)

	case toolName == "edit" || toolName == "Edit" || toolName == "multiEdit" || toolName == "MultiEdit" || toolName == "edit_file":
		path := parseToolInputArgAny(input, "path", "file_path")
		return NewFileToolItem(id, pretty, path, status, styles)

	case toolName == "grep" || toolName == "Grep" || toolName == "search_files":
		pattern := parseToolInputArg(input, "pattern")
		return NewSearchToolItem(id, pretty, pattern, status, styles)

	case toolName == "glob" || toolName == "Glob" || toolName == "find":
		pattern := parseToolInputArg(input, "pattern")
		return NewSearchToolItem(id, pretty, pattern, status, styles)

	default:
		return NewGenericToolItem(id, pretty, status, input, styles)
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
}

// NewTodoToolItem creates a new todo list tool item.
func NewTodoToolItem(id string, tasks []TodoTask, styles Styles) *TodoToolItem {
	return &TodoToolItem{
		id:     id,
		tasks:  tasks,
		styles: styles,
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

	header := fmt.Sprintf("%s %s  %d/%d", t.styles.ToolIconStyle(StatusRunning), "To-Do", done, total)
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

	// Task list
	for _, task := range t.tasks {
		sb.WriteString("\n  ")
		switch task.Status {
		case "done":
			sb.WriteString("✓ ")
		case "in_progress":
			sb.WriteString("→ ")
		default:
			sb.WriteString("○ ")
		}
		sb.WriteString(task.Content)
	}

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
