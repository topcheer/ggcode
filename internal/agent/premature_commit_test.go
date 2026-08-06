package agent

import (
	"strings"
	"testing"
)

func TestPrematureCommit_SufficientExploration(t *testing.T) {
	s := newPrematureCommitState()

	// Simulate sufficient exploration: 3 reads + 1 search
	s.recordExploration("read_file", "/foo/bar.go")
	s.recordExploration("read_file", "/foo/baz.go")
	s.recordExploration("grep", "pattern")
	s.recordExploration("read_file", "/foo/main.go")

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg != "" {
		t.Errorf("expected no warning with sufficient exploration, got: %s", msg)
	}
}

func TestPrematureCommit_InsufficientExploration(t *testing.T) {
	s := newPrematureCommitState()

	// Only 1 read, no search
	s.recordExploration("read_file", "/foo/bar.go")

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg == "" {
		t.Fatal("expected warning with insufficient exploration, got none")
	}
	if !strings.Contains(msg, "exploratory action") {
		t.Errorf("expected message about exploration count, got: %s", msg)
	}
	if !strings.Contains(msg, "no repository-wide search") {
		t.Errorf("expected message about missing search, got: %s", msg)
	}
}

func TestPrematureCommit_NoSearchPerformed(t *testing.T) {
	s := newPrematureCommitState()

	// 2 reads (below 3-file threshold), no search
	s.recordExploration("read_file", "/foo/bar.go")
	s.recordExploration("read_file", "/foo/baz.go")

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg == "" {
		t.Fatal("expected warning when no search was performed")
	}
	if !strings.Contains(msg, "no repository-wide search") {
		t.Errorf("expected message about missing search, got: %s", msg)
	}
}

func TestPrematureCommit_ReadsBeyondEditTarget(t *testing.T) {
	s := newPrematureCommitState()

	// Read 3 files, one of which is not the edit target, plus a search
	s.recordExploration("read_file", "/foo/bar.go")
	s.recordExploration("read_file", "/foo/baz.go")
	s.recordExploration("read_file", "/foo/qux.go")
	s.recordExploration("code_search", "related code")

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg != "" {
		t.Errorf("expected no warning when reading 3+ files with search, got: %s", msg)
	}
}

func TestPrematureCommit_FiresOnlyOnce(t *testing.T) {
	s := newPrematureCommitState()

	msg1 := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg1 == "" {
		t.Fatal("expected warning on first edit")
	}

	// Second edit should not trigger again
	msg2 := s.checkFirstEdit([]string{"/foo/baz.go"})
	if msg2 != "" {
		t.Errorf("expected no warning on subsequent edit, got: %s", msg2)
	}
}

func TestPrematureCommit_Reset(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", "/foo/bar.go")
	_ = s.checkFirstEdit([]string{"/foo/bar.go"})

	if !s.warned {
		t.Fatal("expected warned=true before reset")
	}

	s.reset()

	if s.warned || s.firstEditDone || s.explorationCount != 0 {
		t.Errorf("reset did not clear state: warned=%v firstEditDone=%v explorationCount=%d",
			s.warned, s.firstEditDone, s.explorationCount)
	}
}

func TestPrematureCommit_RecordsOnlyBeforeFirstEdit(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", "/foo/bar.go")
	_ = s.checkFirstEdit([]string{"/foo/bar.go"})

	// After first edit, exploration recording should be ignored
	s.recordExploration("grep", "pattern")
	if s.explorationCount != 1 {
		t.Errorf("expected explorationCount=1 after first edit, got %d", s.explorationCount)
	}
}

func TestPrematureCommit_MinimalExplorationWithSearch(t *testing.T) {
	s := newPrematureCommitState()

	// Only 1 read + 1 search = explorationCount=2 < 3, but searchCount=1 >= 1
	// Should still warn because explorationCount is too low
	s.recordExploration("read_file", "/foo/bar.go")
	s.recordExploration("grep", "pattern")

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg == "" {
		t.Fatal("expected warning when explorationCount < 3 even with search")
	}
	if !strings.Contains(msg, "exploratory action") {
		t.Errorf("expected message about low exploration, got: %s", msg)
	}
}

func TestPrematureCommit_IgnoresNonExploratoryTools(t *testing.T) {
	s := newPrematureCommitState()

	// Non-exploratory tools should not count
	s.recordExploration("edit_file", "/foo/bar.go")
	s.recordExploration("run_command", "ls")

	if s.explorationCount != 0 || s.searchCount != 0 {
		t.Errorf("non-exploratory tools should not increment counters: exp=%d search=%d",
			s.explorationCount, s.searchCount)
	}
}

func TestPrematureCommit_LSPCountsAsSearch(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", "/foo/bar.go")
	s.recordExploration("read_file", "/foo/baz.go")
	s.recordExploration("lsp_references", "")
	s.recordExploration("read_file", "/foo/qux.go")

	// 4 exploration calls + 1 search = sufficient
	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg != "" {
		t.Errorf("expected no warning with LSP search, got: %s", msg)
	}
}
