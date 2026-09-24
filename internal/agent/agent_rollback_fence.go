package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/rollback"
	"github.com/topcheer/ggcode/internal/tool"
)

// Rollback fence wiring (ACRFence-inspired, arXiv:2603.20625).
//
// A checkpoint restore rolls back files only. Commands executed before the
// restore keep their external effects, and after the restore the model often
// re-issues a semantically equal command with fresh arguments — duplicating
// irreversible side effects (pushes, PRs, publishes, messages). The fence
// records executed commands, and restore paths move those recorded since the
// restored checkpoint into a fenced set that is (a) disclosed in restore
// output and correction feedback, and (b) matched on re-execution to append a
// duplicate-effect warning to the tool result.

// commandRecordingTools are the tools whose executions can produce
// irreversible external effects and are therefore recorded in the fence.
var commandRecordingTools = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

// rollbackFence returns the agent's fence, lazily initializing it so the
// zero-value Agent (used pervasively in tests) keeps working.
func (a *Agent) rollbackFence() *rollback.Fence {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rbFence == nil {
		a.rbFence = rollback.New()
	}
	return a.rbFence
}

// fenceMatchFor checks a pending tool call against the fenced set. Only
// command-executing tools participate; other tools never match.
func (a *Agent) fenceMatchFor(tcName string, tcArguments []byte) (rollback.Action, bool) {
	if !commandRecordingTools[tcName] {
		return rollback.Action{}, false
	}
	cmd := extractCommandArgument(tcArguments)
	if cmd == "" {
		return rollback.Action{}, false
	}
	if f := a.rollbackFence(); f != nil {
		return f.Match(cmd)
	}
	return rollback.Action{}, false
}

// fenceObserve records a completed execution in the fence and, when the call
// matched a fenced action, appends a duplicate-effect warning to the result.
// Warning text travels inside the tool RESULT (never as a separate message) so
// the protocol constraint between tool_calls and tool_results is respected.
func (a *Agent) fenceObserve(tcName string, tcArguments []byte, res tool.Result, fenced bool) tool.Result {
	if fenced {
		res.Content += rollbackFenceWarning
	}
	if !res.IsError && commandRecordingTools[tcName] {
		if cmd := extractCommandArgument(tcArguments); cmd != "" {
			if f := a.rollbackFence(); f != nil {
				f.Record(tcName, cmd)
			}
		}
	}
	return res
}

// rollbackFenceWarning is appended to results of commands that re-execute
// after a checkpoint restore.
const rollbackFenceWarning = "\n\n[rollback-fence] This command already ran before a recent revert/undo, and its external effects were NOT rolled back. If it creates external resources (commits/pushes, PRs/issues, deployments, messages), verify the earlier effect exists before repeating it — re-running may create a duplicate."

// ActivateRollbackFence fences every recorded command execution at or after
// `at` (the timestamp of the checkpoint being restored) and returns them for
// caller-side disclosure. Called by the TUI restore paths and undo_edit.
func (a *Agent) ActivateRollbackFence(at time.Time) []rollback.Action {
	f := a.rollbackFence()
	if f == nil {
		return nil
	}
	return f.Activate(at)
}

// RollbackFenceEntries returns the current fenced set (commands whose
// external effects predate the latest restore).
func (a *Agent) RollbackFenceEntries() []rollback.Action {
	f := a.rollbackFence()
	if f == nil {
		return nil
	}
	return f.Fenced()
}

// activateRollbackFence is the agent-internal variant used by undo_edit after
// a successful self-restore.
func (a *Agent) activateRollbackFence(at time.Time) {
	if f := a.rollbackFence(); f != nil {
		f.Activate(at)
	}
}

// extractCommandArgument pulls the `command` field from a tool call's JSON
// arguments, defensively (malformed JSON yields "").
func extractCommandArgument(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

// rollbackFenceNote builds the fenced-actions disclosure appended to
// model-injected correction feedback. Returns "" when nothing is fenced.
func (a *Agent) rollbackFenceNote() string {
	acted := a.RollbackFenceEntries()
	if len(acted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nRollback advisory: the following command(s) ran BEFORE the revert/undo and their external effects were NOT rolled back:\n")
	for i, act := range acted {
		if i >= 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(acted)-5))
			break
		}
		b.WriteString("  - " + rollback.NormalizeCommand(act.Command) + "\n")
	}
	b.WriteString("Before re-running any of them, verify the earlier effect already exists (e.g. list PRs/issues/deployments) to avoid creating duplicates.\n")
	return b.String()
}

// injectRollbackFenceNote injects a standalone fence advisory as a user
// message — used when corrections are absent or agent-sourced (#1449-A) but
// fenced external effects exist. No-op when nothing is fenced.
func (a *Agent) injectRollbackFenceNote() {
	note := a.rollbackFenceNote()
	if note == "" {
		return
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: note},
		},
	})
	debug.Log("agent", "injected rollback fence note")
}
