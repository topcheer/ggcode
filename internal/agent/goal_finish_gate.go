package agent

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
		return ""
	}
	var store goalFinishStore
	if err := json.Unmarshal(data, &store); err != nil {
		return ""
	}

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
			continue
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

	a.goalFinish.fired = true
	return "[goal-finish-gate] goal-graph still has in_progress leaves while you are about to declare completion:\n" +
		strings.Join(leaves, "\n") +
		"\nFinish them, or explicitly update each to done/dropped via goal_graph (dropping IS a legitimate terminal state), then restate completion."
}
