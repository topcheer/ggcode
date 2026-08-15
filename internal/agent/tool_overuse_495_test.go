package agent

import "testing"

// #495 regression: a FAILED edit must not make the recovery read_file
// "suspicious". edit_fail_recovery explicitly recommends re-reading the
// file after a failed edit — the old pre-execution bookkeeping counted the
// failed edit as a write, so the recovery read received false-premise
// "Trust the content from your edit" guidance contradicting that
// recommendation.
func TestToolOveruse_FailedEditThenRecoveryReadIsClean(t *testing.T) {
	s := newToolOveruseState()

	// Stale successful write from an earlier iteration (e.g. a previous
	// edit to the same file) — the failed edit below must clear it.
	s.recordWrite("src/main.go", 1, true)

	// Failed edit at iteration 2.
	s.recordWriteResult("edit_file", `{"file_path":"src/main.go","old_text":"gone","new_text":"b"}`, 2, false)

	// Recovery read at iteration 3 (edit_fail_recovery's own step 1).
	msg := s.maybeWarn("read_file", `{"path":"src/main.go"}`, 3)
	if msg != "" {
		t.Fatalf("recovery read after a FAILED edit must not get false-premise guidance, got: %s", msg)
	}
}

// Failed edit with no prior entry: nothing to warn on either.
func TestToolOveruse_FailedEditAlone(t *testing.T) {
	s := newToolOveruseState()
	s.recordWriteResult("edit_file", `{"file_path":"new.go","old_text":"a","new_text":"b"}`, 1, false)
	if msg := s.maybeWarn("read_file", `{"path":"new.go"}`, 2); msg != "" {
		t.Fatalf("no successful write happened — read must be clean, got: %s", msg)
	}
}

// Successful edit keeps the read-after-write detection intact.
func TestToolOveruse_SuccessfulEditStillWarns(t *testing.T) {
	s := newToolOveruseState()
	s.recordWriteResult("edit_file", `{"file_path":"a.go","old_text":"a","new_text":"b"}`, 1, true)
	if msg := s.maybeWarn("read_file", `{"path":"a.go"}`, 2); msg == "" {
		t.Fatal("successful edit followed by read must still warn")
	}
}

// #495 minor: trivial command matching is whole-word, not substring.
func TestToolOveruse_TrivialCommandWholeWord(t *testing.T) {
	for _, cmd := range []string{"cat .pwd_history", "ls /tmp/uname-dir", "echo which python3-config"} {
		s := newToolOveruseState()
		if msg := s.maybeWarn("run_command", `{"command":"`+cmd+`"}`, 1); msg != "" {
			t.Errorf("%q embeds a trivial pattern as substring — must NOT match, got warning", cmd)
		}
	}
	for _, cmd := range []string{"go version", "uname -a", "cd /x && pwd", "which python"} {
		s := newToolOveruseState()
		if msg := s.maybeWarn("run_command", `{"command":"`+cmd+`"}`, 2); msg == "" {
			t.Errorf("%q is a genuine trivial command — must match", cmd)
		}
	}
}
