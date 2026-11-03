package agent

import "testing"

func TestIssue3717_MentionInContentDoesNotBlock(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl", refusalKindDirect)
	args := `{"file_path":"docs/guide.md","old_text":"The settings live in settings.ini","new_text":"config.yaml supports many options"}`
	if msg := l.checkBlocked("edit_file", args); msg != "" {
		t.Fatalf("cross-file edit whose TEXT mentions the refused file must not block: %s", msg)
	}
}

func TestIssue3717_TargetFieldStillBlocks(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl", refusalKindDirect)
	if msg := l.checkBlocked("edit_file", `{"file_path":"config.yaml","old_text":"a","new_text":"b"}`); msg == "" {
		t.Fatal("an edit whose TARGET is the refused file must still block")
	}

	if msg := l.checkBlocked("multi_file_edit", `{"files":[{"path":"pkg/config.yaml","old_text":"a","new_text":"b"}]}`); msg == "" {
		t.Fatal("nested files[].path must still block")
	}
	if msg := l.checkBlocked("file_ops", `{"operations":[{"action":"move","source":"config.yaml","destination":"renamed.yaml"}]}`); msg == "" {
		t.Fatal("file_ops source/destination must still block")
	}
}

func TestIssue3717_CommandDomainUnchanged(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl", refusalKindDirect)

	if msg := l.checkBlocked("run_command", `{"command":"echo x > config.yaml"}`); msg == "" {
		t.Fatal("run_command writing the refused target must still block")
	}

	if msg := l.checkBlocked("run_command", `{"command":"cat README.md | grep config.yaml"}`); msg != "" {
		t.Fatalf("read-only grep mentioning the target must stay exempt: %s", msg)
	}
}

func TestIssue3717_UnparseableArgsFailClosed(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl", refusalKindDirect)
	if msg := l.checkBlocked("edit_file", "not json config.yaml trailing"); msg == "" {
		t.Fatal("non-JSON args must fall back to whole-string matching (fail closed)")
	}
}
