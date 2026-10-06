package task

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDigestEmptyBoardReturnsEmpty(t *testing.T) {
	m := NewManager()
	if got := m.Digest(20, 1200); got != "" {
		t.Fatalf("expected empty digest for empty board, got %q", got)
	}
}

func TestDigestAllCompletedReturnsEmpty(t *testing.T) {
	m := NewManager()
	tk := m.Create("done thing", "desc", "", nil)
	st := StatusCompleted
	if _, err := m.Update(tk.ID, UpdateOptions{Status: &st}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if got := m.Digest(20, 1200); got != "" {
		t.Fatalf("expected empty digest when only completed tasks exist, got %q", got)
	}
}

func TestDigestCountsCompletedButListsOpen(t *testing.T) {
	m := NewManager()
	done := m.Create("finished task", "desc", "", nil)
	st := StatusCompleted
	m.Update(done.ID, UpdateOptions{Status: &st})
	prog := m.Create("wip task", "desc", "", nil)
	ip := StatusInProgress
	m.Update(prog.ID, UpdateOptions{Status: &ip})
	m.Create("queued task", "desc", "", nil)

	got := m.Digest(20, 1200)
	if !strings.Contains(got, "1 pending, 1 in_progress, 1 completed") {
		t.Fatalf("expected counts in header, got %q", got)
	}
	if !strings.Contains(got, "[in_progress] wip task") {
		t.Fatalf("expected in_progress task listed, got %q", got)
	}
	if !strings.Contains(got, "[pending] queued task") {
		t.Fatalf("expected pending task listed, got %q", got)
	}
	if strings.Contains(got, "finished task") {
		t.Fatalf("completed tasks must not be listed, got %q", got)
	}
}

func TestDigestCapsListedTasks(t *testing.T) {
	m := NewManager()
	for i := 0; i < 5; i++ {
		m.Create("task subject", "desc", "", nil)
	}
	got := m.Digest(2, 0)
	if n := strings.Count(got, "- task-"); n != 2 {
		t.Fatalf("expected 2 listed tasks, got %d in %q", n, got)
	}
}

func TestDigestRuneSafeCap(t *testing.T) {
	m := NewManager()
	// CJK subject: byte cut would split runes and emit U+FFFD.
	m.Create(strings.Repeat("你好世界", 100), "desc", "", nil)
	got := m.Digest(20, 60)
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("rune split in digest output: %q", got)
	}
	if !strings.Contains(got, "... (truncated)") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestDigestSubjectTruncation(t *testing.T) {
	m := NewManager()
	m.Create(strings.Repeat("x", 200), "desc", "", nil)
	got := m.Digest(20, 0)
	if !strings.Contains(got, "xxx...") {
		t.Fatalf("expected subject truncated with ellipsis, got %q", got)
	}
}
