package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// SkillID constants — generic across all ggcode instances.
const (
	SkillCodeEdit    = "code-edit"
	SkillFileSearch  = "file-search"
	SkillCommandExec = "command-exec"
	SkillGitOps      = "git-ops"
	SkillCodeReview  = "code-review"
	SkillFullTask    = "full-task"
)

// DefaultSkills returns the fixed set of skills every ggcode instance advertises.
func DefaultSkills() []Skill {
	return []Skill{
		{ID: SkillCodeEdit, Name: "Code Editing", Description: "Read, write, and edit source files with diff support", Tags: []string{"code", "edit"}},
		{ID: SkillFileSearch, Name: "File Search", Description: "Search files by name pattern, content, or glob", Tags: []string{"search", "files"}},
		{ID: SkillCommandExec, Name: "Command Execution", Description: "Run shell commands with timeout and output capture", Tags: []string{"shell", "run"}},
		{ID: SkillGitOps, Name: "Git Operations", Description: "Git status, diff, log, and branch operations", Tags: []string{"git", "vcs"}},
		{ID: SkillCodeReview, Name: "Code Review", Description: "Review code changes and provide feedback", Tags: []string{"review", "quality"}},
		{ID: SkillFullTask, Name: "Full Task Execution", Description: "Execute a complete coding task end-to-end", Tags: []string{"task", "complete"}},
	}
}

// TaskHandler processes incoming A2A tasks.
type TaskHandler struct {
	mu        sync.Mutex
	workspace string
	agent     *agent.Agent
	registry  *tool.Registry
	tasks     map[string]*Task // active tasks by ID
	meta      WorkspaceMeta
}

// NewTaskHandler creates a handler bound to a specific workspace.
func NewTaskHandler(workspace string, a *agent.Agent, reg *tool.Registry) *TaskHandler {
	return &TaskHandler{
		workspace: workspace,
		agent:     a,
		registry:  reg,
		tasks:     make(map[string]*Task),
		meta:      detectWorkspaceMeta(workspace),
	}
}

// WorkspaceMeta holds dynamically detected workspace properties.
type WorkspaceMeta struct {
	Workspace  string   `json:"workspace"`
	Languages  []string `json:"languages,omitempty"`
	Frameworks []string `json:"frameworks,omitempty"`
	HasGit     bool     `json:"has_git"`
	HasTests   bool     `json:"has_tests"`
	ProjName   string   `json:"project_name"`
}

// Handle processes an incoming message as an A2A task.
func (h *TaskHandler) Handle(ctx context.Context, skill string, input Message) (*Task, error) {
	if skill == "" {
		skill = SkillFullTask
	}

	perm, ok := skillPermissions[skill]
	if !ok {
		return nil, fmt.Errorf("unknown skill: %s", skill)
	}

	// Validate skill availability at runtime.
	if skill == SkillGitOps && !h.meta.HasGit {
		return nil, fmt.Errorf("workspace has no git repository")
	}

	task := &Task{
		ID:        generateID(),
		ContextID: generateID(),
		Status:    TaskStateSubmitted,
		Skill:     skill,
		History:   []Message{input},
		CreatedAt: time.Now(),
	}

	h.mu.Lock()
	h.tasks[task.ID] = task
	h.mu.Unlock()

	// Execute asynchronously.
	go h.execute(context.Background(), task, perm)

	return task, nil
}

func (h *TaskHandler) execute(ctx context.Context, t *Task, perm *SkillPermission) {
	h.updateStatus(t, TaskStateWorking, "")

	var result string
	var err error

	switch t.Skill {
	case SkillFileSearch:
		result, err = h.executeDirectTool(ctx, perm, t.Skill, t.History[0])
	case SkillGitOps:
		result, err = h.executeDirectTool(ctx, perm, t.Skill, t.History[0])
	case SkillCommandExec:
		result, err = h.executeDirectTool(ctx, perm, t.Skill, t.History[0])
	case SkillCodeEdit, SkillCodeReview, SkillFullTask:
		result, err = h.executeAgent(ctx, perm, t.Skill, t.History[0])
	default:
		err = fmt.Errorf("unsupported skill: %s", t.Skill)
	}

	if err != nil {
		h.updateStatus(t, TaskStateFailed, err.Error())
		return
	}

	t.Artifacts = []Artifact{{
		ArtifactID: generateID(),
		Parts: []Part{{
			Kind: "text",
			Text: result,
		}},
	}}
	h.updateStatus(t, TaskStateCompleted, "")
}

// executeDirectTool runs a tool directly without spinning up a full agent loop.
func (h *TaskHandler) executeDirectTool(ctx context.Context, perm *SkillPermission, skill string, msg Message) (string, error) {
	text := extractText(msg)
	if text == "" {
		return "", fmt.Errorf("empty input")
	}

	// Pick the best tool for the skill.
	toolName := pickToolForSkill(skill, text)
	t, ok := h.registry.Get(toolName)
	if !ok {
		return "", fmt.Errorf("tool %s not found", toolName)
	}

	input := buildToolInput(toolName, text)
	result, err := t.Execute(ctx, input)
	if err != nil {
		return "", err
	}

	return result.Content, nil
}

// executeAgent runs a full agent loop with restricted permissions.
func (h *TaskHandler) executeAgent(ctx context.Context, perm *SkillPermission, skill string, msg Message) (string, error) {
	text := extractText(msg)
	if text == "" {
		return "", fmt.Errorf("empty input")
	}

	prompt := buildAgentPrompt(skill, text)

	var buf strings.Builder
	err := h.agent.RunStream(ctx, prompt, func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			buf.WriteString(event.Text)
		}
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (h *TaskHandler) updateStatus(t *Task, state TaskState, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t.Status = state
	t.UpdatedAt = time.Now()
	if message != "" {
		t.History = append(t.History, Message{
			Role: "agent",
			Parts: []Part{{
				Kind: "text",
				Text: message,
			}},
		})
	}
	debug.Log("a2a", "task %s → %s", t.ID, state)
}

// GetTask returns a task by ID.
func (h *TaskHandler) GetTask(id string) (*Task, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	return t, ok
}

// CancelTask cancels a running task.
func (h *TaskHandler) CancelTask(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	if t.Status.IsTerminal() {
		return fmt.Errorf("task already in terminal state: %s", t.Status)
	}
	t.Status = TaskStateCanceled
	t.UpdatedAt = time.Now()
	return nil
}

// ActiveTaskCount returns the number of non-terminal tasks.
func (h *TaskHandler) ActiveTaskCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, t := range h.tasks {
		if !t.Status.IsTerminal() {
			count++
		}
	}
	return count
}

// WorkspaceMetadata returns the detected workspace metadata.
func (h *TaskHandler) WorkspaceMetadata() WorkspaceMeta {
	return h.meta
}

// ---------------------------------------------------------------------------
// Skill permissions
// ---------------------------------------------------------------------------

// SkillPermission defines what a skill can do.
type SkillPermission struct {
	AllowedTools  []string
	ReadOnly      bool
	MaxIterations int
}

var skillPermissions = map[string]*SkillPermission{
	SkillFileSearch:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "glob"}, ReadOnly: true, MaxIterations: 3},
	SkillGitOps:      {AllowedTools: []string{"git_status", "git_diff", "git_log"}, ReadOnly: true, MaxIterations: 3},
	SkillCommandExec: {AllowedTools: []string{"run_command"}, ReadOnly: false, MaxIterations: 1},
	SkillCodeEdit:    {AllowedTools: []string{"read_file", "write_file", "edit_file", "search_files"}, ReadOnly: false, MaxIterations: 5},
	SkillCodeReview:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "git_diff"}, ReadOnly: true, MaxIterations: 5},
	SkillFullTask:    {AllowedTools: nil, ReadOnly: false, MaxIterations: 0}, // nil = all tools, 0 = unlimited
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractText(msg Message) string {
	var parts []string
	for _, p := range msg.Parts {
		if p.Kind == "text" {
			parts = append(parts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func pickToolForSkill(skill string, input string) string {
	switch skill {
	case SkillFileSearch:
		if strings.Contains(input, "*") || strings.Contains(input, ".") {
			return "glob"
		}
		return "search_files"
	case SkillGitOps:
		input = strings.ToLower(input)
		if strings.Contains(input, "diff") {
			return "git_diff"
		}
		if strings.Contains(input, "log") {
			return "git_log"
		}
		return "git_status"
	case SkillCommandExec:
		return "run_command"
	default:
		return "search_files"
	}
}

func buildToolInput(toolName, text string) json.RawMessage {
	switch toolName {
	case "search_files":
		input, _ := json.Marshal(map[string]interface{}{"pattern": text, "max_results": 50})
		return input
	case "glob":
		input, _ := json.Marshal(map[string]interface{}{"pattern": text})
		return input
	case "git_status", "git_diff", "git_log":
		input, _ := json.Marshal(map[string]interface{}{})
		return input
	case "run_command":
		input, _ := json.Marshal(map[string]interface{}{"command": text})
		return input
	case "list_directory":
		input, _ := json.Marshal(map[string]interface{}{"path": "."})
		return input
	case "read_file":
		input, _ := json.Marshal(map[string]interface{}{"path": text})
		return input
	default:
		input, _ := json.Marshal(map[string]interface{}{"query": text})
		return input
	}
}

func buildAgentPrompt(skill string, text string) string {
	switch skill {
	case SkillCodeReview:
		return "Review the following code and provide detailed feedback:\n\n" + text
	case SkillCodeEdit:
		return "Make the following code changes:\n\n" + text
	case SkillFullTask:
		return text
	default:
		return text
	}
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
