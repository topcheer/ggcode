package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Todo represents a single todo item.
type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // "pending", "in_progress", "done"
}

// TodoWrite implements the todo_write tool — manages a persistent todo list.
type TodoWrite struct {
	mu  sync.Mutex
	dir string // directory containing todos.json
}

// NewTodoWrite creates a TodoWrite tool. dir defaults to ~/.ggcode.
func NewTodoWrite(dir string) *TodoWrite {
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".ggcode")
	}
	return &TodoWrite{dir: dir}
}

func (t *TodoWrite) Name() string { return "todo_write" }
func (t *TodoWrite) Description() string {
	return "Create, update, or complete todo items. Persists to ~/.ggcode/todos.json."
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
				"description": "Array of todo items to write. Existing todos not in this list are removed."
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
	for _, td := range args.Todos {
		if td.ID == "" {
			return Result{IsError: true, Content: "each todo must have an id"}, nil
		}
		switch td.Status {
		case "pending", "in_progress", "done":
		default:
			return Result{IsError: true, Content: fmt.Sprintf("invalid status %q for todo %q (must be pending, in_progress, or done)", td.Status, td.ID)}, nil
		}
	}

	// Ensure directory exists
	if err := os.MkdirAll(t.dir, 0755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to create directory: %v", err)}, nil
	}

	path := filepath.Join(t.dir, "todos.json")
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

// ListTodos reads and returns all todos from disk.
func (t *TodoWrite) ListTodos() ([]Todo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	path := filepath.Join(t.dir, "todos.json")
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
