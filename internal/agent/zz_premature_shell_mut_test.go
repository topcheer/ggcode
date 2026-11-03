package agent

import "testing"

func TestPrematureSuccessShellMutationCounts(t *testing.T) {
	p := newPrematureSuccessState()

	p.recordToolCall("run_command", map[string]interface{}{"command": "sed -i 's/x/y/' main.go"}, false, "")
	if p.editsSinceVerify == 0 {
		t.Fatal("sed -i must count as an edit (shellMutatesSources)")
	}

	if w := p.checkSuccessClaim("done — the fix is complete."); w == "" {
		t.Fatal("mutation followed by a success claim must fire the warning")
	}

	q := newPrematureSuccessState()
	q.recordToolCall("run_command", map[string]interface{}{"command": "ls -la"}, false, "")
	if q.editsSinceVerify != 0 {
		t.Fatalf("ls must not count as an edit, got %d", q.editsSinceVerify)
	}
	if w := q.checkSuccessClaim("done."); w != "" {
		t.Fatalf("zero-edit no-verify claim wording unchanged path fired unexpectedly: %q", w)
	}
}
