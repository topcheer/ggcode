package agent

import "github.com/topcheer/ggcode/internal/debug"

type postCompactNoteSetter interface {
	SetPostCompactNoteProvider(fn func() string)
}

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
