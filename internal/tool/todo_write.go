package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
)

// Todo represents a single todo item.
type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // "pending", "in_progress", "done"
}

// todosDir returns the global directory for session-scoped todo files:
// ~/.ggcode/todos/
func todosDir() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "todos")
}

// TodoFilePath returns the path to the todo file for a given session ID.
// The path is ~/.ggcode/todos/<sessionID>.json — global, not per-workspace.
// If sessionID is empty, returns a "_default" fallback path. This is purely
// defensive — normal callers always pass a real session ID (or pipe pseudo-ID).
// The _default file should never be created in normal operation because
// TodoWrite.currentPath() returns "" when sessionID is empty, preventing
// Execute/ClearTodos from reaching TodoFilePath("").
func TodoFilePath(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		id = "_default"
	}
	return filepath.Join(todosDir(), id+".json")
}

// TodoWrite implements the todo_write tool — manages a session-scoped todo list.
type TodoWrite struct {
	mu        sync.Mutex
	sessionID string // current session ID; determines the todo file path
}

// NewTodoWrite creates a TodoWrite tool bound to the given session ID.
// If sessionID is empty, the tool is unbound until SetSessionID is called.
func NewTodoWrite(sessionID string) *TodoWrite {
	return &TodoWrite{sessionID: sessionID}
}

// SetSessionID updates the session ID, switching which todo file is used.
func (t *TodoWrite) SetSessionID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}

// ClearTodos removes the todo file for the current session (if any).
// This is called on agent stop to prevent permanent todo residue.
func (t *TodoWrite) ClearTodos() {
	t.mu.Lock()
	defer t.mu.Unlock()
	path := t.currentPath()
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// currentPath returns the todo file path for the current session.
// Returns empty string if sessionID is unset.
func (t *TodoWrite) currentPath() string {
	if strings.TrimSpace(t.sessionID) == "" {
		return ""
	}
	return TodoFilePath(t.sessionID)
}

func (t *TodoWrite) Name() string { return "todo_write" }
func (t *TodoWrite) Description() string {
	return "Track work progress with a persistent todo list. Use for genuinely multi-step work, not every micro-step. IMPORTANT: Once you create todos, you MUST update their status as work progresses — mark tasks `in_progress` when starting, and `done` when completed. Do NOT create todos and then forget to update them. Keep the list current at every meaningful milestone."
}

func (t *TodoWrite) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"todos": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "Unique identifier for the todo"
						},
						"content": {
							"type": "string",
							"description": "Description of the todo task"
						},
						"status": {
							"type": "string",
							"enum": ["pending", "in_progress", "done"],
							"description": "Status of the todo"
						}
					},
					"required": ["id", "content", "status"]
				},
				"description": "Array of todo items to write. Existing todos not in this list are removed; include the full desired current list. Use for multi-step work, not every micro-step."
			}
		},
		"required": ["todos"]
	}`)
}

func (t *TodoWrite) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Validate
	ids := make(map[string]struct{}, len(args.Todos))
	for _, td := range args.Todos {
		if td.ID == "" {
			return Result{IsError: true, Content: "each todo must have an id"}, nil
		}
		if _, exists := ids[td.ID]; exists {
			return Result{IsError: true, Content: fmt.Sprintf("duplicate todo id %q", td.ID)}, nil
		}
		ids[td.ID] = struct{}{}
		switch td.Status {
		case "pending", "in_progress", "done":
			// valid — multiple in_progress allowed for swarm/team workflows
		default:
			return Result{IsError: true, Content: fmt.Sprintf("invalid status %q for todo %q (must be pending, in_progress, or done)", td.Status, td.ID)}, nil
		}
	}

	path := t.currentPath()
	if path == "" {
		return Result{IsError: true, Content: "no active session — cannot persist todos"}, nil
	}

	if len(args.Todos) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return Result{IsError: true, Content: fmt.Sprintf("clear failed: %v", err)}, nil
		}
		return Result{Content: "Todos cleared\n"}, nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to create directory: %v", err)}, nil
	}

	data, err := json.MarshalIndent(args.Todos, "", "  ")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("marshal failed: %v", err)}, nil
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("write failed: %v", err)}, nil
	}

	// Build summary
	var sb strings.Builder
	pending, progress, done := 0, 0, 0
	for _, td := range args.Todos {
		switch td.Status {
		case "pending":
			pending++
		case "in_progress":
			progress++
		case "done":
			done++
		}
	}
	fmt.Fprintf(&sb, "Todos updated (%d total: %d pending, %d in_progress, %d done)\n", len(args.Todos), pending, progress, done)
	for _, td := range args.Todos {
		mark := " "
		switch td.Status {
		case "in_progress":
			mark = ">"
		case "done":
			mark = "x"
		}
		fmt.Fprintf(&sb, "  [%s] %s: %s\n", mark, td.ID, td.Content)
	}

	return Result{Content: sb.String()}, nil
}

// ListTodos reads and returns all todos from the current session's file.
func (t *TodoWrite) ListTodos() ([]Todo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	path := t.currentPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var todos []Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil, err
	}
	return todos, nil
}
