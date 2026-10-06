package agent

import "github.com/topcheer/ggcode/internal/debug"

// Post-Compaction Task-Board Rehydration (r53).
//
// Frontier harnesses all re-materialize structured task state after a
// context reset: Claude Code re-injects the plan-mode todo list after
// compaction, LangChain Deep Agents ships todo-state in its summary
// fields, and Manus recites todo.md each turn. ggcode's task board
// (task_create/task_list/task_update) lives only in tool_result blocks
// that compaction summarizes away: after a compaction the model has no
// signal that pending tasks exist, and the task IDs needed by task_update
// survive only by summarizer luck (the summary prompt has no task-board
// section, and tool results are truncated to 500 chars in the payload).
//
// This file wires a live task-board renderer into the context manager's
// post-compaction note injection point: after every successful compaction
// a "[Session State Note]" system message is inserted right after the
// summary, telling the model the board exists, what is still open, and
// that the IDs remain valid. pipe.go (non-interactive) registers no task
// tools, so it needs no wiring - same TUI-only layout as the task tools.

// postCompactNoteSetter is the optional capability of the context manager
// (same optional-interface pattern as promptBudgeter /
// oldestGroupTruncater in agent_compact.go).
type postCompactNoteSetter interface {
	SetPostCompactNoteProvider(fn func() string)
}

// SetTaskBoardSnapshotter registers fn as the live task-board renderer.
// fn is invoked after each successful compaction; its (non-empty) result
// is re-injected as a durable system note right after the compaction
// summary. No-op when the context manager lacks the post-compaction note
// capability.
func (a *Agent) SetTaskBoardSnapshotter(fn func() string) {
	if a == nil || fn == nil {
		return
	}
	setter, ok := a.contextManager.(postCompactNoteSetter)
	if !ok {
		debug.Log("agent", "SetTaskBoardSnapshotter: context manager lacks post-compaction note capability; skipping task-board rehydration")
		return
	}
	setter.SetPostCompactNoteProvider(fn)
}
