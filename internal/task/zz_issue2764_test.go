package task

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

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

func TestIssue2764_InProgressLeadsAndSurvivesCap(t *testing.T) {
	m := NewManager()

	for i := 0; i < 20; i++ {
		m.Create("old", "d", "af", nil)
	}

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

	if strings.Contains(d, "task-20 ") {
		t.Fatalf("newest pending task-20 should be dropped, digest:\n%s", d)
	}
}

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

func TestIssue2764_ListDeterministicOrder(t *testing.T) {
	m := NewManager()

	var created []Task
	for i := 0; i < 3; i++ {
		created = append(created, m.Create("s", "d", "af", nil))
	}
	same := time.Now()
	for _, tk := range created {
		m.tasks[tk.ID].CreatedAt = same
	}

	m.Create("newest", "d", "af", nil)

	got := m.List()
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	want := "task-1,task-2,task-3,task-4"
	if strings.Join(ids, ",") != want {
		t.Fatalf("List order = %v, want %v", ids, want)
	}
}
