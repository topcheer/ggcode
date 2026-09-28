package agent

import "testing"

// zz_issue2827_test.go - probe for #2827: run_command tool inputs arrive as a
// raw JSON envelope; reproCommandTokens must unwrap it before tokenizing.
// The old cutset had no braces, so `{"command":"python3 x.py"}` produced the
// pseudo-token `{"command":"python3` shared by every python3 command - any
// unrelated run discharged the re-run obligation (#2802 bypass).
func TestIssue2827EnvelopePseudoToken(t *testing.T) {
	// Unrelated command in envelope form must NOT overlap with an
	// envelope-form reproducer snippet.
	snippet := `{"command":"python3 reproduce_bug.py"}`
	unrelated := `{"command":"python3 run_e2e.py"}`
	if reproCommandTokenOverlap(snippet, unrelated) {
		t.Errorf("#2827 envelope pseudo-token still shared: unrelated python3 run counted as re-run")
	}

	// Genuine re-run in envelope form must still overlap.
	rerun := `{"command":"python3 reproduce_bug.py"}`
	if !reproCommandTokenOverlap(snippet, rerun) {
		t.Errorf("#2827 genuine envelope-form re-run no longer matches")
	}

	// Mixed forms: envelope snippet vs bare re-run command and vice versa.
	if !reproCommandTokenOverlap(snippet, "python3 reproduce_bug.py") {
		t.Errorf("#2827 envelope snippet vs bare re-run no longer matches")
	}
	if !reproCommandTokenOverlap("python3 reproduce_bug.py", rerun) {
		t.Errorf("#2827 bare snippet vs envelope re-run no longer matches")
	}

	// Unrelated bare commands still do not overlap (base #2802 behavior).
	if reproCommandTokenOverlap("python3 reproduce_bug.py", "python3 run_e2e.py") {
		t.Errorf("#2827 base #2802 behavior regressed: unrelated scripts overlap")
	}

	// Direct token checks: no brace pseudo-tokens, real script name kept.
	toks := reproCommandTokens(snippet)
	if toks[`{"command":"python3`] || toks["command"] {
		t.Errorf("#2827 envelope pseudo-token present in token set: %v", toks)
	}
	if !toks["reproduce_bug.py"] {
		t.Errorf("#2827 script token missing after unwrap: %v", toks)
	}

	// Non-envelope input passes through unchanged.
	toks2 := reproCommandTokens("python3 reproduce_bug.py")
	if !toks2["reproduce_bug.py"] || toks2["command"] {
		t.Errorf("#2827 bare command tokenization changed: %v", toks2)
	}

	// Non-command envelope: keys must not become distinctive tokens at all.
	toks3 := reproCommandTokens(`{"timeout": 30}`)
	if len(toks3) != 0 {
		t.Errorf("#2827 non-command envelope yielded distinctive tokens %v, want none (keys are shared pseudo-tokens)", toks3)
	}
}
