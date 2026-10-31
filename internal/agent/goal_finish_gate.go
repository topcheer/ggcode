package agent

// Goal finish gate - long-horizon "premature completion" protection.
// Research basis: Anthropic "Effective harnesses for long-running agents",
// failure mode 2 - a later agent instance looks around, sees progress, and
// declares the job done while its own structured backlog still hangs.
//
// The goal_graph tool persists the agent-built todo tree to
// .ggcode/goal-graph.json, but no layer of the exit gate chain ever reads
// it back: in_progress leaves could stay dangling while the agent reports
// completion. This gate fires once per run, after diffSummary and before
// commitHint, listing in_progress leaves inside the freshness window.
//
// Noise floor: only in_progress (claimed) leaves are gated - a multi-day
// project's pending backlog must not nag every unrelated Q&A run. The
// 7-day window mirrors the tool package's ggStaleAfter; anything older is
// stall territory, not this gate's business.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	goalFinishStaleAfter = 7 * 24 * time.Hour
	goalFinishMaxLeaves  = 3
)

// goalFinishNode mirrors internal/tool/goal_graph.go's ggNode. Defined
// independently here to avoid a tool->agent import cycle; field drift is
// caught by the round-trip tests below.
type goalFinishNode struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Parent    string    `json:"parent,omitempty"`
	Status    string    `json:"status"`
	NextStep  string    `json:"next_step,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type goalFinishStore struct {
	Nodes map[string]*goalFinishNode `json:"nodes"`
}

type goalFinishState struct{ fired bool }

func newGoalFinishState() *goalFinishState { return &goalFinishState{} }
func (g *goalFinishState) reset()          { g.fired = false }

// checkGoalFinishGate returns a fire-once advisory when the agent's own
// goal-graph still has fresh in_progress leaves. Empty string otherwise.
func (a *Agent) checkGoalFinishGate() string {
	if a.goalFinish == nil || a.goalFinish.fired {
		return ""
	}
	wd := a.WorkingDir()
	if wd == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(wd, ".ggcode", "goal-graph.json"))
	if err != nil {
		return "" // no tree (or transient read failure): retry next round, do not latch
	}
	var store goalFinishStore
	if err := json.Unmarshal(data, &store); err != nil {
		return "" // corrupt tree: tool-layer validation owns reporting; retry later
	}
	// #4626: leaf check - parents stay legitimately in_progress while their
	// children run (goal_graph never auto-flips parent status), so a node
	// referenced as someone's Parent is NOT a dangling leaf. Without this,
	// a normal goal->milestone->task chain lists all three levels and the
	// max-3 budget drowns out the actual task leaves.
	parents := make(map[string]bool, len(store.Nodes))
	for _, n := range store.Nodes {
		if n != nil && n.Parent != "" {
			parents[n.Parent] = true
		}
	}
	now := time.Now()
	var leaves []string
	for _, n := range store.Nodes {
		if n == nil || n.Status != "in_progress" || parents[n.ID] {
			continue
		}
		if now.Sub(n.UpdatedAt) > goalFinishStaleAfter {
			continue // stale: stall detection's territory, not ours
		}
		if n.NextStep != "" {
			leaves = append(leaves, fmt.Sprintf("- %s: %s", n.Title, n.NextStep))
		} else {
			leaves = append(leaves, fmt.Sprintf("- %s (no next_step)", n.Title))
		}
		if len(leaves) >= goalFinishMaxLeaves {
			break
		}
	}
	if len(leaves) == 0 {
		return ""
	}
	// #4626: latch only when a warning actually fired - "fire-once" means at
	// most one WARNING per run, not at most one CHECK. A missing/half-written
	// goal-graph.json must not permanently disable the gate for the run.
	a.goalFinish.fired = true
	return "[goal-finish-gate] goal-graph still has in_progress leaves while you are about to declare completion:\n" +
		strings.Join(leaves, "\n") +
		"\nFinish them, or explicitly update each to done/dropped via goal_graph (dropping IS a legitimate terminal state), then restate completion."
}
