package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

func (a *Agent) checkIncompleteTodos() string {
	t, ok := a.tools.Get("todo_write")
	if !ok {
		return ""
	}
	tw, ok := t.(*tool.TodoWrite)
	if !ok {
		return ""
	}

	todos, err := tw.ListTodos()
	if err != nil {
		debug.Log("agent", "checkIncompleteTodos: read error: %v", err)
		return ""
	}
	if len(todos) == 0 {
		return ""
	}

	var incomplete []tool.Todo
	for _, td := range todos {
		if td.Status == "pending" || td.Status == "in_progress" {
			incomplete = append(incomplete, td)
		}
	}

	if len(incomplete) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("You have incomplete todo items that were not marked as done:\n\n")
	for _, td := range incomplete {
		mark := "pending"
		if td.Status == "in_progress" {
			mark = "IN PROGRESS"
		}
		fmt.Fprintf(&sb, "  [%s] %s: %s\n", mark, td.ID, td.Content)
	}
	sb.WriteString("\n")
	if len(incomplete) == len(todos) {
		sb.WriteString("None of your todos are complete. Either complete them or remove them with todo_write if they are no longer relevant.")
	} else {
		fmt.Fprintf(&sb, "%d of %d todos are still incomplete. Complete them or remove them with todo_write if they are no longer relevant.", len(incomplete), len(todos))
	}
	return sb.String()
}

func codeChangedInRun(runStats *RunStats) bool {
	for name := range runStats.ToolCalls {
		if fileEditingTools[name] {
			return true
		}
	}
	return false
}
