package swarm

import (
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/task"
)

// r456: priority-aware claiming with starvation detection.
//
// Frontier basis: multi-agent task-allocation consensus is priority-aware
// claiming plus aging (low-priority tasks must not be deferred forever)
// and starvation alerts. The board's "priority" metadata field existed
// (e2e tests set it) but claiming walked the list in creation order —
// the declared semantic had no runtime effect: a later-arriving high
// task could be starved indefinitely by a stream of earlier low tasks.
//
// This file keeps it conservative (detection over preemption, r319
// precedent): claims are ORDERED by effective priority (base + aging
// bonus), and a pending task older than the starvation threshold gets a
// one-shot marker + debug log. No preemption: an in-flight task is never
// revoked.

// priorityRank maps the board's priority metadata to a base score.
// Higher = claimed sooner. Unknown/missing = medium.
func priorityRank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high", "critical", "urgent", "p0", "p1":
		return 100
	case "low", "background", "p3":
		return 0
	default: // "medium", numeric garbage, absent
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return 50 // a positive numeric priority ranks like medium
		}
		return 50
	}
}

// agingBonus: +1 per pending minute. After ~50 minutes a low task
// outranks a fresh high one (100 vs 50+50) — bounded anti-starvation
// without letting old junk pre-empt everything.
func agingBonus(createdAt, now time.Time) int {
	if createdAt.IsZero() || now.Before(createdAt) {
		return 0
	}
	return int(now.Sub(createdAt).Minutes())
}

// effectivePriority is the claim-order score: base rank + aging.
// Pure function — probes cover the boundary cases.
func effectivePriority(tk task.Task, now time.Time) int {
	return priorityRank(tk.Metadata["priority"]) + agingBonus(tk.CreatedAt, now)
}

// starvationThreshold is how long a pending (claimable) task may wait
// before the one-shot starvation marker fires. Var for tests.
var starvationThreshold = 30 * time.Minute

// starvedSinceKey is the one-shot marker stored in board metadata.
const starvedSinceKey = "starved_since"

// markStarved fires the one-shot starvation alert for claimable pending
// tasks older than the threshold: first hit writes the marker (epoch
// seconds) and logs; the marker suppresses repeats. Tasks whose
// dependencies are unmet or that are assigned elsewhere are not
// starved — they are parked by design.
func markStarved(tmMgr *task.Manager, tk task.Task, now time.Time) {
	if tk.CreatedAt.IsZero() || now.Sub(tk.CreatedAt) < starvationThreshold {
		return
	}
	if v, ok := tk.Metadata[starvedSinceKey]; ok && v != "" {
		return // already alerted
	}
	stamp := strconv.FormatInt(now.Unix(), 10)
	if _, err := tmMgr.Update(tk.ID, task.UpdateOptions{Metadata: map[string]string{starvedSinceKey: stamp}}); err != nil {
		// Board is gone or the task moved on — nothing to alert about.
		return
	}
	debug.Log("swarm", "[starvation] task=%s subject=%q pending since %v (priority=%q)", tk.ID, tk.Subject, tk.CreatedAt, tk.Metadata["priority"])
}

// sortClaimable orders eligible pending tasks for claiming: highest
// effective priority first; ties keep list order (stable) so behavior
// with no priority metadata is unchanged FIFO.
func sortClaimable(tasks []task.Task, now time.Time) {
	// Insertion sort: n is small (a board page), stable, allocation-free.
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0; j-- {
			if effectivePriority(tasks[j], now) <= effectivePriority(tasks[j-1], now) {
				break
			}
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
}
