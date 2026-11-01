package agent

import "testing"

// Path-symmetry audit round 4: the same shellMutatesSources heuristic feeds
// four sibling consumers but not premature_success's edit counter, so a
// "sed -i ... ; done" run passed silently (editsSinceVerify==0 short-circuits
// the whole check).
func TestPrematureSuccessShellMutationCounts(t *testing.T) {
	p := newPrematureSuccessState()
	// A shell command that rewrites sources but is not a verify command.
	p.recordToolCall("run_command", map[string]interface{}{"command": "sed -i 's/x/y/' main.go"}, false, "")
	if p.editsSinceVerify == 0 {
		t.Fatal("sed -i must count as an edit (shellMutatesSources)")
	}
	// The premature-success check must no longer short-circuit after it.
	if w := p.checkSuccessClaim("done — the fix is complete."); w == "" {
		t.Fatal("mutation followed by a success claim must fire the warning")
	}

	// Non-mutating non-verify commands still count nothing (no false positives).
	q := newPrematureSuccessState()
	q.recordToolCall("run_command", map[string]interface{}{"command": "ls -la"}, false, "")
	if q.editsSinceVerify != 0 {
		t.Fatalf("ls must not count as an edit, got %d", q.editsSinceVerify)
	}
	if w := q.checkSuccessClaim("done."); w != "" {
		t.Fatalf("zero-edit no-verify claim wording unchanged path fired unexpectedly: %q", w)
	}
}
