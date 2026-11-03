package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	a := &Agent{workingDir: t.TempDir(), goalFinish: newGoalFinishState()}
	if msg := a.checkGoalFinishGate(); msg != "" {
		t.Fatalf("missing goal-graph must stay silent, got:\n%s", msg)
	}
}

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

func TestGoalFinishGateParentNotLeaf(t *testing.T) {
	wd := t.TempDir()
	now := time.Now()
	writeGoalGraph(t, wd, map[string]*goalFinishNode{
		"g": {ID: "g", Level: "goal", Title: "ship feature", Status: "in_progress", UpdatedAt: now},
		"m": {ID: "m", Level: "milestone", Title: "backend", Parent: "g", Status: "in_progress", UpdatedAt: now},
		"t": {ID: "t", Level: "task", Title: "write tests", Parent: "m", Status: "in_progress", NextStep: "cover gate", UpdatedAt: now},
	})
	a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
	msg := a.checkGoalFinishGate()
	if msg == "" {
		t.Fatal("real in_progress leaf must still fire")
	}
	if strings.Contains(msg, "ship feature") || strings.Contains(msg, "backend") {
		t.Errorf("parents must not be listed as dangling leaves, got:\n%s", msg)
	}
	if !strings.Contains(msg, "write tests") {
		t.Errorf("the actual task leaf must be listed, got:\n%s", msg)
	}
}

func TestGoalFinishGateAllTasksDoneParentStaysOpen(t *testing.T) {
	wd := t.TempDir()
	now := time.Now()
	writeGoalGraph(t, wd, map[string]*goalFinishNode{
		"g": {ID: "g", Level: "goal", Title: "old goal", Status: "in_progress", UpdatedAt: now},
		"t": {ID: "t", Level: "task", Title: "done thing", Parent: "g", Status: "done", UpdatedAt: now},
	})
	a := &Agent{workingDir: wd, goalFinish: newGoalFinishState()}
	if msg := a.checkGoalFinishGate(); msg != "" {
		t.Errorf("goal parent with all tasks done must not fire, got:\n%s", msg)
	}
}

func TestGoalFinishGateFiredOnlyOnWarning(t *testing.T) {
	a := &Agent{workingDir: t.TempDir(), goalFinish: newGoalFinishState()}
	if msg := a.checkGoalFinishGate(); msg != "" {
		t.Fatalf("no tree yet must stay silent, got:\n%s", msg)
	}
	if a.goalFinish.fired {
		t.Fatal("missing tree must NOT latch fired (read failure is not a warning)")
	}

	writeGoalGraph(t, a.workingDir, map[string]*goalFinishNode{
		"n1": goalTestNode("in_progress", time.Now()),
	})
	if msg := a.checkGoalFinishGate(); msg == "" {
		t.Fatal("gate must fire once the tree exists - fired was wrongly latched")
	}
	if !a.goalFinish.fired {
		t.Fatal("warning must latch fired")
	}
}
