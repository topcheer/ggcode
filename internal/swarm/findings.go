package swarm

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/util"
)

const resultMetaKey = "result"

const boardResultMaxRunes = 2000

const (
	maxDepFindings      = 6
	depFindingMaxRunes  = 800
	depFindingsMaxTotal = 4800
)

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

		debug.Log("swarm", "persistTaskResult: task=%s update failed: %v", taskID, err)
	}
}

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

			continue
		}
		sb.WriteString(entry)
		total += len(entry)
		entries++
	}
	return sb.String()
}
