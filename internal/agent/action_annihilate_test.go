package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawJSON(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestActionAnnihilate_GitAddThenReset_SameFiles(t *testing.T) {
	s := newActionAnnihilateState()
	addArgs := rawJSON(t, map[string]interface{}{"files": []string{"a.go", "b.go"}})
	resetArgs := rawJSON(t, map[string]interface{}{"files": []string{"b.go"}})

	s.recordToolCall("git_add", addArgs, 1)
	warn := s.recordToolCall("git_reset", resetArgs, 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for git_add→git_reset")
	}
	if s.cancelCount != 1 {
		t.Errorf("expected cancelCount=1, got %d", s.cancelCount)
	}

	if strings.Contains(warn, "net-zero state change") {
		t.Errorf("partial reset mislabeled as net-zero: %q", warn)
	}
	if !strings.Contains(warn, "PART of the staged set") {
		t.Errorf("partial-reset qualifier missing: %q", warn)
	}
}

func TestActionAnnihilate_GitAddThenReset_DifferentFiles(t *testing.T) {
	s := newActionAnnihilateState()
	addArgs := rawJSON(t, map[string]interface{}{"files": []string{"a.go"}})
	resetArgs := rawJSON(t, map[string]interface{}{"files": []string{"z.go"}})

	s.recordToolCall("git_add", addArgs, 1)
	warn := s.recordToolCall("git_reset", resetArgs, 2)

	if warn != "" {
		t.Fatal("expected no warning for non-overlapping files")
	}
}

func TestActionAnnihilate_GitAddDotThenReset(t *testing.T) {
	s := newActionAnnihilateState()
	addArgs := rawJSON(t, map[string]interface{}{"files": []string{"."}})
	resetArgs := rawJSON(t, map[string]interface{}{"files": []string{"a.go"}})

	s.recordToolCall("git_add", addArgs, 1)
	warn := s.recordToolCall("git_reset", resetArgs, 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for git_add . → git_reset")
	}

	if strings.Contains(warn, "net-zero state change") || strings.Contains(warn, "oscillating") {
		t.Errorf("stage-all + single-file unstage mislabeled as net-zero/oscillation: %q", warn)
	}
	if !strings.Contains(warn, "standard curation step") {
		t.Errorf("curation qualifier missing: %q", warn)
	}
}

func TestActionAnnihilate_GitAddDotThenFullReset_KeepsNetZeroWording(t *testing.T) {

	s := newActionAnnihilateState()
	addArgs := rawJSON(t, map[string]interface{}{"files": []string{"."}})
	resetArgs := rawJSON(t, map[string]interface{}{})

	s.recordToolCall("git_add", addArgs, 1)
	warn := s.recordToolCall("git_reset", resetArgs, 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for git_add . → full git_reset")
	}
	if !strings.Contains(warn, "net-zero state change") {
		t.Errorf("full reset must keep the net-zero wording: %q", warn)
	}
}

func TestActionAnnihilate_GitAddThenReset_ExactSet_KeepsNetZeroWording(t *testing.T) {

	s := newActionAnnihilateState()
	addArgs := rawJSON(t, map[string]interface{}{"files": []string{"a.go"}})
	resetArgs := rawJSON(t, map[string]interface{}{"files": []string{"a.go"}})

	s.recordToolCall("git_add", addArgs, 1)
	warn := s.recordToolCall("git_reset", resetArgs, 2)

	if warn == "" {
		t.Fatal("expected annihilation warning")
	}
	if !strings.Contains(warn, "net-zero state change") {
		t.Errorf("exact-set reset must keep the net-zero wording: %q", warn)
	}
}

func TestActionAnnihilate_EditThenUndo(t *testing.T) {
	s := newActionAnnihilateState()
	editArgs := rawJSON(t, map[string]interface{}{"file_path": "/tmp/x.go"})

	s.recordToolCall("edit_file", editArgs, 1)
	warn := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for edit_file→undo_edit")
	}
}

func TestActionAnnihilate_WriteThenUndo(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("write_file", rawJSON(t, map[string]interface{}{"path": "/tmp/y.go"}), 1)
	warn := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for write_file→undo_edit")
	}
}

func TestActionAnnihilate_MultiEditThenUndo(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("multi_edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/z.go"}), 1)
	warn := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for multi_edit_file→undo_edit")
	}
}

func TestActionAnnihilate_GitCommitThenRevert(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "msg"}), 1)

	s.recordCommitHash(1, "abc1234")
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "abc123"}), 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for git_commit→git_revert")
	}
}

func TestActionAnnihilate_FileOpsMkdirThenDelete(t *testing.T) {
	s := newActionAnnihilateState()
	mkdirArgs := rawJSON(t, map[string]interface{}{
		"operations": []map[string]interface{}{
			{"action": "mkdir", "source": "/tmp/newdir"},
		},
	})
	deleteArgs := rawJSON(t, map[string]interface{}{
		"operations": []map[string]interface{}{
			{"action": "delete", "source": "/tmp/newdir"},
		},
	})

	s.recordToolCall("file_ops", mkdirArgs, 1)
	warn := s.recordToolCall("file_ops", deleteArgs, 2)

	if warn == "" {
		t.Fatal("expected annihilation warning for file_ops mkdir→delete")
	}
}

func TestActionAnnihilate_FileOpsMkdirDeleteDifferentPath(t *testing.T) {
	s := newActionAnnihilateState()
	mkdirArgs := rawJSON(t, map[string]interface{}{
		"operations": []map[string]interface{}{
			{"action": "mkdir", "source": "/tmp/dirA"},
		},
	})
	deleteArgs := rawJSON(t, map[string]interface{}{
		"operations": []map[string]interface{}{
			{"action": "delete", "source": "/tmp/dirB"},
		},
	})

	s.recordToolCall("file_ops", mkdirArgs, 1)
	warn := s.recordToolCall("file_ops", deleteArgs, 2)

	if warn != "" {
		t.Fatal("expected no warning for mkdir A → delete B (different paths)")
	}
}

func TestActionAnnihilate_GitCheckoutRoundtrip(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 1)

	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 2)

	warn := s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 3)

	if warn == "" {
		t.Fatal("expected annihilation warning for git_checkout main→feature→main roundtrip")
	}
}

func TestActionAnnihilate_GitCheckoutDifferentBranches(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 1)
	warn := s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 2)

	if warn != "" {
		t.Fatal("expected no warning for git_checkout to different branches")
	}
}

func TestActionAnnihilate_GitCheckoutRoundtrip_ReferenceWorkflow(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 1)
	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"}), 2)
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "release-1.0"}), 3)
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/ref.go"}), 4)
	s.recordToolCall("grep", rawJSON(t, map[string]interface{}{"pattern": "foo"}), 5)
	warn := s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 6)

	if warn != "" {
		t.Fatal("expected no warning: substantive work between checkouts is legitimate branch navigation")
	}
	if s.cancelCount != 0 {
		t.Errorf("expected cancelCount=0, got %d", s.cancelCount)
	}
}

func TestActionAnnihilate_GitCheckoutRoundtrip_EditBetweenBarriers(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 1)
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 2)
	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/b.go"}), 3)
	warn := s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 4)

	if warn != "" {
		t.Fatal("expected no warning: edit between checkouts is a barrier")
	}
}

func TestActionAnnihilate_GitCheckoutRoundtrip_MultiHopThrash(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 1)
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "feature"}), 2)
	s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "hotfix"}), 3)
	warn := s.recordToolCall("git_checkout", rawJSON(t, map[string]interface{}{"branch": "main"}), 4)

	if warn == "" {
		t.Fatal("expected warning for all-checkout multi-hop roundtrip")
	}
	assertContains(t, warn, "branch thrashing")
}

func TestActionAnnihilate_NoAnnihilation(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/a.go"}), 1)
	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"}), 2)
	warn := s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/a.go"}), 3)

	if warn != "" {
		t.Fatal("expected no annihilation warning for read→edit→read")
	}
}

func TestActionAnnihilate_MaxWarns(t *testing.T) {
	s := newActionAnnihilateState()

	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"}), 1)
	warn1 := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 2)

	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/b.go"}), 3)
	warn2 := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 4)

	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/c.go"}), 5)
	warn3 := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 6)

	if warn1 == "" {
		t.Fatal("expected first warning")
	}
	if warn2 == "" {
		t.Fatal("expected second warning")
	}
	if warn3 != "" {
		t.Fatal("expected third warning to be suppressed (max 2)")
	}
	if s.cancelCount != 3 {
		t.Errorf("expected cancelCount=3, got %d", s.cancelCount)
	}
	if s.warnsIssued != 2 {
		t.Errorf("expected warnsIssued=2, got %d", s.warnsIssued)
	}
}

func TestActionAnnihilate_ResetClearsState(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"}), 1)
	s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 2)

	s.reset()

	if s.cancelCount != 0 || s.warnsIssued != 0 || len(s.actions) != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestActionAnnihilate_LookbackWindow(t *testing.T) {
	s := newActionAnnihilateState()
	s.lookback = 3

	s.recordToolCall("edit_file", rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"}), 1)
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/b.go"}), 2)
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/c.go"}), 3)
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "/tmp/d.go"}), 4)

	warn := s.recordToolCall("undo_edit", rawJSON(t, map[string]interface{}{"action": "undo"}), 5)

	if warn != "" {
		t.Fatal("expected no warning when prior action is outside lookback window")
	}
}

func TestActionAnnihilate_WarningContainsDescription(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "m"}), 1)

	s.recordCommitHash(1, "abc1234")
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "abc"}), 2)

	if warn == "" {
		t.Fatal("expected warning")
	}

	assertContains(t, warn, "git_commit then git_revert")

	assertContains(t, warn, "Reflect")
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Errorf("expected %q to contain %q", s, substr)
}
