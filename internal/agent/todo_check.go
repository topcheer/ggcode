package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

func (a *Agent) checkIncompleteTodos() string {
	var incomplete []string
	total, doneCount := 0, 0

	// Source 1: todo_write todos.
	if t, ok := a.tools.Get("todo_write"); ok {
		if tw, ok := t.(*tool.TodoWrite); ok {
			todos, err := tw.ListTodos()
			if err != nil {
				debug.Log("agent", "checkIncompleteTodos: todo read error: %v", err)
			} else {
				for _, td := range todos {
					total++
					switch td.Status {
					case "pending":
						incomplete = append(incomplete, fmt.Sprintf("  [pending] %s: %s", td.ID, td.Content))
					case "in_progress":
						incomplete = append(incomplete, fmt.Sprintf("  [IN PROGRESS] %s: %s", td.ID, td.Content))
					default:
						doneCount++
					}
				}
			}
		}
	}

	// Source 2 (#5656): session task board. Sessions that never call
	// todo_write had NO pending-work signal at all - the todo-only check
	// was a blind spot for them.
	if t, ok := a.tools.Get("task_list"); ok {
		if tl, ok := t.(interface{ PendingSubjects() []string }); ok {
			for _, s := range tl.PendingSubjects() {
				incomplete = append(incomplete, fmt.Sprintf("  [task] %s", s))
			}
		}
	}

	if len(incomplete) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("You are ending your turn, but the session still has declared, unfinished work:\n\n")
	for _, line := range incomplete {
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nContinue working on it now in this same turn. Only end the turn when the work is complete or genuinely blocked; if a blocker exists, name it explicitly in your final message. Do not just describe the remaining steps - execute them. (If an item is truly obsolete, remove it with todo_write/task_update first, then finish the rest.)\n")
	return sb.String()
}

func codeChangedInRun(runStats *RunStats) bool {
	for name := range runStats.ToolCalls {
		// #5254: apply_patch edits are code changes for todo discipline too.
		if mutatingSourceEdit(name) {
			return true
		}
	}
	return false
}
