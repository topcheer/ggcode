package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeGoalGraph writes a minimal goal-graph.json the way the goal_graph
// tool does, with the given node statuses.
func writeGoalGraph(t *testing.T, wd string, nodes map[string]*goalFinishNode) {
	t.Helper()
	dir := filepath.Join(wd, ".ggcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(goalFinishStore{Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal-graph.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func goalTestNode(status string, updated time.Time) *goalFinishNode {
	return &goalFinishNode{
		ID: "n1", Level: "task", Title: "wire the gate",
		Status: status, NextStep: "add tests", UpdatedAt: updated,
	}
}

// TestGoalFinishGateFiresOnFreshInProgress: a fresh in_progress leaf must
// produce the advisory naming the leaf and its next_step.
func TestGoalFinishGateFiresOnFreshInProgress(t *testing.T) {
	wd := t.TempDir()
	writeGoalGraph(t, wd, map[string]*goalFinishNode{
		"n1": goalTestNode("in_progress", time.Now()),
	})
	a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
	msg := a.checkGoalFinishGate()
	if msg == "" {
		t.Fatal("fresh in_progress leaf must fire the gate")
	}
	if !strings.Contains(msg, "wire the gate") || !strings.Contains(msg, "add tests") {
		t.Fatalf("advisory must name title and next_step, got:\n%s", msg)
	}
}

// TestGoalFinishGateFireOnce: second call in the same run returns empty.
func TestGoalFinishGateFireOnce(t *testing.T) {
	wd := t.TempDir()
	writeGoalGraph(t, wd, map[string]*goalFinishNode{
		"n1": goalTestNode("in_progress", time.Now()),
	})
	a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
	if a.checkGoalFinishGate() == "" {
		t.Fatal("first call must fire")
	}
	if a.checkGoalFinishGate() != "" {
		t.Fatal("second call must be silent (fire-once)")
	}
}

// TestGoalFinishGateSkipsWhenNoInProgress: done/dropped/pending trees and
// missing trees must stay silent - backlog must not nag completion runs.
func TestGoalFinishGateSkipsWhenNoInProgress(t *testing.T) {
	for _, status := range []string{"done", "dropped", "pending"} {
		wd := t.TempDir()
		writeGoalGraph(t, wd, map[string]*goalFinishNode{
			"n1": goalTestNode(status, time.Now()),
		})
		a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
		if msg := a.checkGoalFinishGate(); msg != "" {
			t.Fatalf("%s leaf must not fire, got:\n%s", status, msg)
		}
	}
	// No tree at all.
	a := &Agent{workingDir: t.TempDir(), goalFinish: newGoalFinishState()}
	if msg := a.checkGoalFinishGate(); msg != "" {
		t.Fatalf("missing goal-graph must stay silent, got:\n%s", msg)
	}
}

// TestGoalFinishGateStaleLeafSkipped: an in_progress leaf older than the
// 7-day freshness window is stall territory, not this gate's.
func TestGoalFinishGateStaleLeafSkipped(t *testing.T) {
	wd := t.TempDir()
	writeGoalGraph(t, wd, map[string]*goalFinishNode{
		"n1": goalTestNode("in_progress", time.Now().Add(-8*24*time.Hour)),
	})
	a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
	if msg := a.checkGoalFinishGate(); msg != "" {
		t.Fatalf("stale in_progress leaf must not fire, got:\n%s", msg)
	}
}
