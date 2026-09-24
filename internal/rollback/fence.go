// Package rollback implements a side-effect-aware fence around the agent's
// checkpoint/restore workflow, inspired by ACRFence (arXiv:2603.20625,
// "Preventing Semantic Rollback Attacks in Agent Checkpoint-Restore").
//
// Checkpoint restore in a coding agent rolls back FILE state only. Commands
// the agent ran before the restore (git push, gh pr create, npm publish,
// messages sent, deployments) keep their external effects. After the restore,
// the model frequently re-derives its plan and re-issues a semantically equal
// command with fresh arguments (new IDs, timestamps, flags) — traditional
// byte-exact idempotency checks do not catch it, and the external effect is
// duplicated.
//
// The Fence records executed commands, and when a restore happens, moves the
// commands executed since the restored checkpoint into a "fenced" set. Two
// consumers use the fence:
//
//  1. Disclosure: the TUI restore paths (/undo, /undo-run, checkpoint panel
//     revert) and the agent's own undo_edit surface the fenced set so users
//     and the model know which effects were NOT rolled back.
//
//  2. Re-execution warning: when the agent runs a command that matches a
//     fenced entry (whitespace-normalized comparison), a warning is appended
//     to the tool result telling the model to verify the earlier effect
//     before repeating it.
//
// This is a deterministic, no-LLM variant of ACRFence's intent-aware
// analyzer: exact-after-normalization matching catches the common
// retry-after-restore case without adding latency or cost to every call.
package rollback

import (
	"strings"
	"sync"
	"time"
)

const (
	// maxRecent bounds the rolling window of recorded command executions.
	maxRecent = 64
	// maxFenced bounds the fenced set produced by a single restore.
	maxFenced = 16
)

// Action is one recorded execution of an external-effect tool.
type Action struct {
	Time    time.Time `json:"time"`
	Tool    string    `json:"tool"`
	Command string    `json:"command"`
}

// Fence tracks executed commands and the subset fenced by the latest restore.
// Safe for concurrent use.
type Fence struct {
	mu     sync.Mutex
	recent []Action // rolling window of executions since the last restore
	fenced []Action // executions that predate the latest restore (not rolled back)
}

// New creates an empty Fence.
func New() *Fence {
	return &Fence{}
}

// Record appends a successful execution to the rolling window.
// Commands that normalize to empty are ignored.
func (f *Fence) Record(tool, command string) {
	norm := NormalizeCommand(command)
	if norm == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recent = append(f.recent, Action{Time: time.Now(), Tool: tool, Command: command})
	if len(f.recent) > maxRecent {
		f.recent = f.recent[len(f.recent)-maxRecent:]
	}
}

// Activate fences every recorded execution that happened at or after `at`
// (typically the timestamp of the checkpoint being restored). It REPLACES the
// previous fenced set: each restore describes the effects of that restore
// only. Returns the newly fenced actions (most recent last).
func (f *Fence) Activate(at time.Time) []Action {
	f.mu.Lock()
	defer f.mu.Unlock()

	var acted []Action
	kept := f.recent[:0]
	for _, a := range f.recent {
		if !a.Time.Before(at) {
			acted = append(acted, a)
		} else {
			kept = append(kept, a)
		}
	}
	if kept == nil {
		kept = []Action{}
	}
	f.recent = kept

	// Keep the oldest entries first for display; cap to the most recent ones.
	if len(acted) > maxFenced {
		acted = acted[len(acted)-maxFenced:]
	}
	f.fenced = acted
	out := make([]Action, len(acted))
	copy(out, acted)
	return out
}

// Match reports whether `command` normalizes to a fenced action.
func (f *Fence) Match(command string) (Action, bool) {
	norm := NormalizeCommand(command)
	if norm == "" {
		return Action{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.fenced) - 1; i >= 0; i-- {
		if NormalizeCommand(f.fenced[i].Command) == norm {
			return f.fenced[i], true
		}
	}
	return Action{}, false
}

// Fenced returns a copy of the current fenced set.
func (f *Fence) Fenced() []Action {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Action, len(f.fenced))
	copy(out, f.fenced)
	return out
}

// NormalizeCommand collapses all whitespace runs to single spaces and trims,
// so a model that re-emits the same command with different indentation or
// quoting artifacts still matches the fenced entry.
func NormalizeCommand(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
