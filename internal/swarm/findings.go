package swarm

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/util"
)

// Dependency-chain blackboard: the team task board carries ordering
// (BlockedBy gating in allBlockersComplete) but, before this file, not
// content - a teammate claiming a dependent task received the dependency's
// subject line yet none of its findings, so the executing agent had to
// re-derive work its teammate had already done (the swarm-side twin of the
// spawn_agent findings-handoff gap). These helpers close that loop:
//
//   - persistTaskResult writes the executing teammate's final output onto
//     the board task (metadata["result"], bounded) when it completes.
//   - depFindingsPrompt renders the persisted results of a claimed task's
//     completed dependencies into the claim prompt, mirroring the
//     subagent findings-handoff bounds (6 entries / 800 runes each /
//     4800 runes total). Failed or cancelled runs never persisted a
//     result, so they are excluded by construction.

// resultMetaKey stores a completed board task's final output in metadata.
const resultMetaKey = "result"

// Bounds for the result persisted on the board. Richer than the injection
// bounds: the board snapshot is also read by the leader via swarm_task_list,
// so it keeps a bit more context than any single claim prompt needs.
const boardResultMaxRunes = 2000

// Bounds for depFindingsPrompt - same values as the subagent
// findings-handoff injection so both surfaces share one cost profile.
const (
	maxDepFindings      = 6
	depFindingMaxRunes  = 800
	depFindingsMaxTotal = 4800
)

// persistTaskResult records a completed board task's final output in its
// metadata (truncated, rune-safe). Empty results write nothing: there is
// no finding to share. Update merges metadata, so retry_attempts and
// other governance keys are preserved.
func persistTaskResult(board *task.Manager, taskID, result string) {
	if board == nil || taskID == "" {
		return
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return
	}
	if _, err := board.Update(taskID, task.UpdateOptions{
		Metadata: map[string]string{resultMetaKey: util.Truncate(trimmed, boardResultMaxRunes)},
	}); err != nil {
		// The task already completed successfully; losing the result
		// metadata is degraded sharing, not lost work.
		debug.Log("swarm", "persistTaskResult: task=%s update failed: %v", taskID, err)
	}
}

// depFindingsPrompt renders the persisted results of a claimed task's
// completed dependencies as a bounded prompt section. Returns "" when no
// dependency carried a finding (the common case: no deps, or deps
// completed without a persisted result).
func depFindingsPrompt(board *task.Manager, tk task.Task) string {
	if board == nil || len(tk.BlockedBy) == 0 {
		return ""
	}
	var sb strings.Builder
	total := 0
	entries := 0
	for _, depID := range tk.BlockedBy {
		if entries >= maxDepFindings || total >= depFindingsMaxTotal {
			break
		}
		dep, ok := board.Get(depID)
		if !ok || dep.Status != task.StatusCompleted {
			continue
		}
		result := strings.TrimSpace(dep.Metadata[resultMetaKey])
		if result == "" {
			continue
		}
		if entries == 0 {
			sb.WriteString("\n\nFindings from completed dependency tasks (team task board):\n")
		}
		entry := fmt.Sprintf("[%s] %s: %s\n", dep.ID, dep.Subject,
			util.Truncate(result, depFindingMaxRunes))
		if total+len(entry) > depFindingsMaxTotal {
			break
		}
		sb.WriteString(entry)
		total += len(entry)
		entries++
	}
	return sb.String()
}
