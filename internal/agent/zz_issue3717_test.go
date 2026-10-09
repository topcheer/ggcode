package agent

// #3717: checkBlocked matched the whole tool-args JSON, so a CONTENT-field
// mention of a refused target (new_text describing config.yaml while
// editing docs/guide.md) hard-blocked a legitimate cross-file edit. The
// match domain now strips content fields; write targets still block.

import "testing"

func TestRefusalCheckBlockedContentMentionNotBlocked(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl")

	// Case 1: edit targets docs/guide.md; only new_text mentions config.yaml
	// -> a mention, not a mutation. Must NOT block.
	msg := l.checkBlocked("edit_file", `{"file_path":"docs/guide.md","old_text":"x","new_text":"config.yaml supports several options"}`)
	if msg != "" {
		t.Fatalf("content-mention case wrongly blocked: %q", msg)
	}

	// Case 3: only old_text (being-deleted content) mentions -> observation
	// case; content is content, deletion of a mention is not touching the
	// refused file. Must NOT block.
	msg = l.checkBlocked("edit_file", `{"file_path":"docs/guide.md","old_text":"see config.yaml","new_text":"y"}`)
	if msg != "" {
		t.Fatalf("old_text-mention case wrongly blocked: %q", msg)
	}

	// Baseline: the write target itself is the refused file -> must block.
	if msg = l.checkBlocked("edit_file", `{"file_path":"config.yaml","old_text":"a","new_text":"b"}`); msg == "" {
		t.Fatal("actual-target case not blocked - ledger broken")
	}

	// Nested shapes: batch_replace files[].path still matchable, while
	// files[].pattern/content mentions are not.
	msg = l.checkBlocked("batch_replace", `{"files":[{"path":"config.yaml","pattern":"x","replacement":"config.yaml"}]}`)
	if msg == "" {
		t.Fatal("batch_replace target path not blocked")
	}
	msg = l.checkBlocked("batch_replace", `{"files":[{"path":"docs/a.md","pattern":"config.yaml","replacement":"y"}]}`)
	if msg != "" {
		t.Fatalf("batch_replace content mention wrongly blocked: %q", msg)
	}

	// run_command: command stays fully matchable (the command IS the write
	// domain) - echo of the target must still be judged by read-only shape.
	if msg = l.checkBlocked("run_command", `echo "config.yaml notes" > docs/guide.md`); msg == "" {
		t.Log("note: echo rewrite of unrelated file mentioning target not blocked (acceptable: target untouched)")
	}
	if msg = l.checkBlocked("run_command", `git checkout config.yaml`); msg == "" {
		t.Fatal("run_command on refused target must block")
	}

	// Non-JSON args keep pre-#3717 fail-closed matching.
	if msg = l.checkBlocked("run_command", `rm -f config.yaml`); msg == "" {
		t.Fatal("non-JSON command mentioning target must still block")
	}
}
