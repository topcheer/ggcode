package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newGoalGraphTestTool(t *testing.T) *GoalGraphTool {
	t.Helper()
	return &GoalGraphTool{WorkingDir: t.TempDir()}
}

func ggExec(t *testing.T, tool *GoalGraphTool, action string, extra map[string]any) Result {
	t.Helper()
	in := map[string]any{"action": action}
	for k, v := range extra {
		in[k] = v
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute %s: %v", action, err)
	}
	return res
}

// Full lifecycle: add hierarchy, rollup on update, tree render, resume
// (cross-session leaves + next steps), stats, cascade delete.
func TestGoalGraphLifecycle(t *testing.T) {
	tool := newGoalGraphTestTool(t)

	// Hierarchy enforcement: milestone cannot be a root-level goal child skip.
	if res := ggExec(t, tool, "add", map[string]any{"level": "task", "title": "orphan task", "parent": "nope"}); !res.IsError {
		t.Fatalf("add with missing parent should error, got %q", res.Content)
	}
	// goal <- milestone <- task must be strictly one rank apart.
	if res := ggExec(t, tool, "add", map[string]any{"level": "goal", "title": "Ship v2"}); res.IsError {
		t.Fatalf("add goal: %s", res.Content)
	}
	if res := ggExec(t, tool, "add", map[string]any{"level": "task", "title": "bad nesting", "parent": "ship-v2"}); !res.IsError {
		t.Fatalf("task under goal should be rejected (rank jump), got %q", res.Content)
	}
	if res := ggExec(t, tool, "add", map[string]any{"level": "milestone", "title": "Backend", "parent": "ship-v2"}); res.IsError {
		t.Fatalf("add milestone: %s", res.Content)
	}
	if res := ggExec(t, tool, "add", map[string]any{"level": "task", "title": "API done", "parent": "backend"}); res.IsError {
		t.Fatalf("add task: %s", res.Content)
	}
	if res := ggExec(t, tool, "add", map[string]any{"level": "task", "title": "Migrate DB", "parent": "backend", "next_step": "run scripts/migrate.sh on staging"}); res.IsError {
		t.Fatalf("add task 2: %s", res.Content)
	}

	// Rollup appears on parent update and in tree.
	res := ggExec(t, tool, "update", map[string]any{"id": "api-done", "status": "done"})
	if res.IsError || !strings.Contains(res.Content, "status=done") {
		t.Fatalf("update: %s", res.Content)
	}
	res = ggExec(t, tool, "tree", nil)
	if !strings.Contains(res.Content, "1/2 children done (50%)") {
		t.Fatalf("tree should show 50%% rollup, got:\n%s", res.Content)
	}

	// Resume: only open leaves, with next steps.
	res = ggExec(t, tool, "resume", nil)
	if !strings.Contains(res.Content, "Migrate DB") || !strings.Contains(res.Content, "run scripts/migrate.sh on staging") {
		t.Fatalf("resume should surface open leaf + next step, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "API done") || strings.Contains(res.Content, "Backend") {
		t.Fatalf("resume should only list open leaves, got:\n%s", res.Content)
	}

	// Stats: counts + per-goal rollup (direct children: 1 milestone, not done).
	res = ggExec(t, tool, "stats", nil)
	if !strings.Contains(res.Content, "goals=1 milestones=1 tasks=2") || !strings.Contains(res.Content, `goal "Ship v2": 0/1 children done (0%)`) {
		t.Fatalf("stats mismatch, got:\n%s", res.Content)
	}

	// Cascade delete removes the subtree.
	res = ggExec(t, tool, "delete", map[string]any{"id": "ship-v2"})
	if res.IsError || !strings.Contains(res.Content, "3 descendant") {
		t.Fatalf("delete: %s", res.Content)
	}
	res = ggExec(t, tool, "stats", nil)
	if !strings.Contains(res.Content, "goals=0") {
		t.Fatalf("graph should be empty after cascade delete, got:\n%s", res.Content)
	}
}

// Cross-session durability: a NEW tool instance over the same workingDir
// (fresh process simulation) must load the persisted graph and resume.
func TestGoalGraphCrossSessionPersistence(t *testing.T) {
	dir := t.TempDir()
	first := &GoalGraphTool{WorkingDir: dir}
	ggExec(t, first, "add", map[string]any{"level": "goal", "title": "Refactor auth"})
	ggExec(t, first, "add", map[string]any{"level": "milestone", "title": "JWT", "parent": "refactor-auth"})
	ggExec(t, first, "add", map[string]any{"level": "task", "title": "Rotate keys", "parent": "jwt", "status": "in_progress", "next_step": "check KMS rotation policy"})

	// File exists on disk with the expected shape.
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "goal-graph.json")); err != nil {
		t.Fatalf("goal graph not persisted: %v", err)
	}

	second := &GoalGraphTool{WorkingDir: dir} // new "session"
	res := ggExec(t, second, "resume", nil)
	if !strings.Contains(res.Content, "Rotate keys") || !strings.Contains(res.Content, "check KMS rotation policy") {
		t.Fatalf("new session must resume persisted in_progress leaf with next step, got:\n%s", res.Content)
	}
}

// Slug-collision disambiguation mirrors #1692: same slug from a different
// title must not silently collide.
func TestGoalGraphSlugCollision(t *testing.T) {
	tool := newGoalGraphTestTool(t)
	ggExec(t, tool, "add", map[string]any{"level": "goal", "title": "C++ API"})
	res := ggExec(t, tool, "add", map[string]any{"level": "goal", "title": "c api"})
	if res.IsError {
		t.Fatalf("second add: %s", res.Content)
	}
	if !strings.Contains(res.Content, "id=c-api-2") {
		t.Fatalf("distinct title colliding on slug should disambiguate, got %q", res.Content)
	}
}

// Stall detection: in_progress nodes untouched for >7d are flagged.
func TestGoalGraphStallDetection(t *testing.T) {
	tool := newGoalGraphTestTool(t)
	ggExec(t, tool, "add", map[string]any{"level": "goal", "title": "Slow", "status": "in_progress"})
	// Age the node past the stall threshold.
	tool.mu.Lock()
	tool.cache.Nodes["slow"].UpdatedAt = tool.cache.Nodes["slow"].UpdatedAt.AddDate(0, 0, -10)
	tool.mu.Unlock()
	res := ggExec(t, tool, "stats", nil)
	if !strings.Contains(res.Content, "Stalled") || !strings.Contains(res.Content, `goal "Slow"`) {
		t.Fatalf("stalled node should be flagged, got:\n%s", res.Content)
	}
}
