package agent

import (
	"strings"
	"testing"
)

func TestPrematureCommit_SufficientExploration(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	s.recordExploration("read_file", []string{"/foo/baz.go"})
	s.recordExploration("grep", []string{"pattern"})
	s.recordExploration("read_file", []string{"/foo/main.go"})

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg != "" {
		t.Errorf("expected no warning with sufficient exploration, got: %s", msg)
	}
}

func TestPrematureCommit_InsufficientExploration(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", []string{"/foo/bar.go"})

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

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	s.recordExploration("read_file", []string{"/foo/baz.go"})

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

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	s.recordExploration("read_file", []string{"/foo/baz.go"})
	s.recordExploration("read_file", []string{"/foo/qux.go"})
	s.recordExploration("code_search", []string{"related code"})

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

	msg2 := s.checkFirstEdit([]string{"/foo/baz.go"})
	if msg2 != "" {
		t.Errorf("expected no warning on subsequent edit, got: %s", msg2)
	}
}

func TestPrematureCommit_Reset(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", []string{"/foo/bar.go"})
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

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	_ = s.checkFirstEdit([]string{"/foo/bar.go"})

	s.recordExploration("grep", []string{"pattern"})
	if s.explorationCount != 1 {
		t.Errorf("expected explorationCount=1 after first edit, got %d", s.explorationCount)
	}
}

func TestPrematureCommit_MinimalExplorationWithSearch(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	s.recordExploration("grep", []string{"pattern"})

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

	s.recordExploration("edit_file", []string{"/foo/bar.go"})
	s.recordExploration("run_command", []string{"ls"})

	if s.explorationCount != 0 || s.searchCount != 0 {
		t.Errorf("non-exploratory tools should not increment counters: exp=%d search=%d",
			s.explorationCount, s.searchCount)
	}
}

func TestPrematureCommit_LSPCountsAsSearch(t *testing.T) {
	s := newPrematureCommitState()

	s.recordExploration("read_file", []string{"/foo/bar.go"})
	s.recordExploration("read_file", []string{"/foo/baz.go"})
	s.recordExploration("lsp_references", []string{""})
	s.recordExploration("read_file", []string{"/foo/qux.go"})

	msg := s.checkFirstEdit([]string{"/foo/bar.go"})
	if msg != "" {
		t.Errorf("expected no warning with LSP search, got: %s", msg)
	}
}

func TestPrematureCommit_MultiFileReadCountsAllFiles(t *testing.T) {
	s := newPrematureCommitState()
	args := []byte(`{"files":[{"path":"/a/target.go"},{"path":"/b/dep.go"},{"path":"/c/util.go"},{"path":"/d/helper.go"}]}`)
	s.recordExploration("multi_file_read", extractFileHints("multi_file_read", args))

	s.recordExploration("grep", []string{"pattern"})

	got := s.checkFirstEdit([]string{"/a/target.go"})
	if got != "" {
		t.Errorf("batch-read file set must satisfy the reads-beyond-target exemption, got: %s", got)
	}

	for _, p := range []string{"/a/target.go", "/b/dep.go", "/c/util.go", "/d/helper.go"} {
		if !s.filesRead[normalizeFilePath(p)] {
			t.Errorf("file %s not recorded from multi_file_read batch", p)
		}
	}
}
