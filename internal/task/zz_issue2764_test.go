package task

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// #2764: Digest used to render tasks in Go map iteration order, so when open
// tasks exceeded maxTasks the dropped subset (and line order) changed between
// consecutive compactions - tasks flickered in and out of the re-injected
// board. The fix makes List deterministic (in-progress first, then
// oldest-first, ID tiebreak) and truncates maxChars at the last full-line
// boundary instead of mid-line.

// 25 open tasks (> default maxTasks 20): consecutive Digest calls must be
// byte-identical, and the kept subset must be the deterministic head (not a
// random sample).
func TestIssue2764_DigestDeterministicAcrossCalls(t *testing.T) {
	m := NewManager()
	for i := 0; i < 25; i++ {
		m.Create("task subject", "d", "af", nil)
	}
	a := m.Digest(20, 5000)
	b := m.Digest(20, 5000)
	if a == "" {
		t.Fatal("digest empty with 25 pending tasks")
	}
	if a != b {
		t.Fatalf("two digests of an unchanged board differ:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
	// Oldest tasks win: task-1..task-20 must be listed, task-21..25 dropped.
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("task-%d", i)
		if !strings.Contains(a, "- "+id+" [") {
			t.Fatalf("expected %s in digest (oldest-first head), digest:\n%s", id, a)
		}
	}
	if strings.Contains(a, "task-21 ") {
		t.Fatalf("task-21 should be dropped (beyond maxTasks 20), digest:\n%s", a)
	}
}

// In-progress tasks lead the list even when created later, so they are never
// the ones dropped by the maxTasks cap.
func TestIssue2764_InProgressLeadsAndSurvivesCap(t *testing.T) {
	m := NewManager()
	// 20 old pending tasks fill the cap...
	for i := 0; i < 20; i++ {
		m.Create("old", "d", "af", nil)
	}
	// ...then a NEW task is started in progress. It must be listed first.
	latest := m.Create("active work", "d", "af", nil)
	inProgress := StatusInProgress
	if _, err := m.Update(latest.ID, UpdateOptions{Status: &inProgress}); err != nil {
		t.Fatalf("update: %v", err)
	}
	d := m.Digest(20, 5000)
	var body []string
	for _, ln := range strings.Split(d, "\n") {
		if strings.HasPrefix(ln, "- ") {
			body = append(body, ln)
		}
	}
	if len(body) != 20 {
		t.Fatalf("listed %d tasks, want 20", len(body))
	}
	if !strings.Contains(body[0], latest.ID) {
		t.Fatalf("in-progress task %s not first in digest:\n%s", latest.ID, d)
	}
	// One old pending task gets dropped instead of the active one.
	if strings.Contains(d, "task-20 ") {
		t.Fatalf("newest pending task-20 should be dropped, digest:\n%s", d)
	}
}

// maxChars truncation lands on a full-line boundary: no half-cut task ID or
// subject, and the output is stable for a stable board.
func TestIssue2764_TruncationOnLineBoundary(t *testing.T) {
	m := NewManager()
	for i := 0; i < 30; i++ {
		m.Create(strings.Repeat("x", 60), "d", "af", nil)
	}
	a := m.Digest(20, 400)
	b := m.Digest(20, 400)
	if a != b {
		t.Fatalf("truncated digests unstable:\n%s\n---\n%s", a, b)
	}
	if !strings.HasSuffix(a, "... (truncated)") {
		t.Fatalf("missing truncation marker:\n%s", a)
	}
	for _, ln := range strings.Split(a, "\n") {
		if !strings.HasPrefix(ln, "- ") && !strings.HasPrefix(ln, "Task board") && ln != "... (truncated)" {
			t.Fatalf("mid-line cut detected: %q", ln)
		}
	}
}

// List itself is the ordering source for digestStats; pin it directly with
// same-timestamp tasks (ID tiebreak) and an older out-of-order task.
func TestIssue2764_ListDeterministicOrder(t *testing.T) {
	m := NewManager()
	// 3 tasks created in immediate succession; force identical CreatedAt to
	// exercise the ID tiebreak.
	var created []Task
	for i := 0; i < 3; i++ {
		created = append(created, m.Create("s", "d", "af", nil))
	}
	same := time.Now()
	for _, tk := range created {
		m.tasks[tk.ID].CreatedAt = same
	}
	// Newer pending task sorts after the same-timestamp block.
	m.Create("newest", "d", "af", nil)

	got := m.List()
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	want := "task-1,task-2,task-3,task-4" // tiebreak by ID, then newest last
	if strings.Join(ids, ",") != want {
		t.Fatalf("List order = %v, want %v", ids, want)
	}
}
