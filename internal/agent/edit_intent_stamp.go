package agent

import (
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

func (a *Agent) stampEditIntent() {
	if a.checkpoints == nil {
		return
	}
	intent := checkpoint.EditIntent{}
	if t, ok := a.tools.Get("todo_write"); ok {
		if tw, ok := t.(*tool.TodoWrite); ok {
			if todos, err := tw.ListTodos(); err == nil {
				for _, td := range todos {
					if td.Status == "in_progress" {
						intent = checkpoint.EditIntent{
							TaskID:   td.ID,
							TaskDesc: td.Content,
						}
						break
					}
				}
			} else {
				debug.Log("agent", "stampEditIntent: todo read error: %v", err)
			}
		}
	}
	a.checkpoints.SetActiveIntent(intent)
}
