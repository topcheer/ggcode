package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const maxTodoItems = 50

type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

func todosDir() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "todos")
}

func SweepStaleTodoFiles(olderThan time.Duration) int {
	entries, err := os.ReadDir(todosDir())
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(todosDir(), entry.Name())); err != nil {
			debug.Log("todo-write", "sweep: removing stale todo file %s: %v", entry.Name(), err)
			continue
		}
		removed++
	}
	if removed > 0 {
		debug.Log("todo-write", "sweep: removed %d stale todo file(s) older than %s", removed, olderThan)
	}
	return removed
}

func TodoFilePath(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		id = "_default"
	}
	return filepath.Join(todosDir(), id+".json")
}

type TodoWrite struct {
	mu        sync.Mutex
	sessionID string
}

func NewTodoWrite(sessionID string) *TodoWrite {
	return &TodoWrite{sessionID: sessionID}
}

func (t *TodoWrite) SetSessionID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}

func (t *TodoWrite) Clone() Tool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &TodoWrite{sessionID: t.sessionID}
}

func (t *TodoWrite) ClearTodos() {
	t.mu.Lock()
	defer t.mu.Unlock()
	path := t.currentPath()
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (t *TodoWrite) currentPath() string {
	if strings.TrimSpace(t.sessionID) == "" {
		return ""
	}
	return TodoFilePath(t.sessionID)
}

func (t *TodoWrite) Name() string { return "todo_write" }
func (t *TodoWrite) Description() string {
	return "Track work progress with a persistent todo list for genuinely multi-step work. " +
		"IMPORTANT: Mark tasks in_progress when starting and done when completed. Keep the list current at every milestone."
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

	if len(args.Todos) > maxTodoItems {
		return Result{IsError: true, Content: fmt.Sprintf("too many todo items: got %d, max %d. Consolidate or remove completed items.", len(args.Todos), maxTodoItems)}, nil
	}
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
