package agent

// sa-91: edit-provenance intent stamping (PROV-AGENT arXiv 2508.02866;
// agent-native version control). Before a checkpoint is saved for an
// edit-class tool call, the CURRENT in_progress todo item (if any) is stamped
// onto the checkpoint manager so every edit answers "which task was this
// for?". Source of truth is the real todo state machine (todo_write's
// persisted list) - never model free-typed arguments - so attribution cannot
// be gamed or drift from what the agent actually declared as working-on.

import (
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// stampEditIntent refreshes the checkpoint manager's active task attribution
// from the todo list. Called immediately before edit-class checkpoints are
// saved; cheap (one small file read) and failure-tolerant (no todo tool, read
// error, or empty list all degrade to zero attribution, never block edits).
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
