package swarm

import (
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/task"
)

func priorityRank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high", "critical", "urgent", "p0", "p1":
		return 100
	case "low", "background", "p3":
		return 0
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return 50
		}
		return 50
	}
}

func agingBonus(createdAt, now time.Time) int {
	if createdAt.IsZero() || now.Before(createdAt) {
		return 0
	}
	return int(now.Sub(createdAt).Minutes())
}

func effectivePriority(tk task.Task, now time.Time) int {
	return priorityRank(tk.Metadata["priority"]) + agingBonus(tk.CreatedAt, now)
}

var starvationThreshold = 30 * time.Minute

const starvedSinceKey = "starved_since"

func markStarved(tmMgr *task.Manager, tk task.Task, now time.Time) {
	if tk.CreatedAt.IsZero() || now.Sub(tk.CreatedAt) < starvationThreshold {
		return
	}
	if v, ok := tk.Metadata[starvedSinceKey]; ok && v != "" {
		return
	}
	stamp := strconv.FormatInt(now.Unix(), 10)
	if _, err := tmMgr.Update(tk.ID, task.UpdateOptions{Metadata: map[string]string{starvedSinceKey: stamp}}); err != nil {

		return
	}
	debug.Log("swarm", "[starvation] task=%s subject=%q pending since %v (priority=%q)", tk.ID, tk.Subject, tk.CreatedAt, tk.Metadata["priority"])
}

func sortClaimable(tasks []task.Task, now time.Time) {

	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0; j-- {
			if effectivePriority(tasks[j], now) <= effectivePriority(tasks[j-1], now) {
				break
			}
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
}
