package agent

import "testing"

func TestIssue2827EnvelopePseudoToken(t *testing.T) {

	snippet := `{"command":"python3 reproduce_bug.py"}`
	unrelated := `{"command":"python3 run_e2e.py"}`
	if reproCommandTokenOverlap(snippet, unrelated) {
		t.Errorf("#2827 envelope pseudo-token still shared: unrelated python3 run counted as re-run")
	}

	rerun := `{"command":"python3 reproduce_bug.py"}`
	if !reproCommandTokenOverlap(snippet, rerun) {
		t.Errorf("#2827 genuine envelope-form re-run no longer matches")
	}

	if !reproCommandTokenOverlap(snippet, "python3 reproduce_bug.py") {
		t.Errorf("#2827 envelope snippet vs bare re-run no longer matches")
	}
	if !reproCommandTokenOverlap("python3 reproduce_bug.py", rerun) {
		t.Errorf("#2827 bare snippet vs envelope re-run no longer matches")
	}

	if reproCommandTokenOverlap("python3 reproduce_bug.py", "python3 run_e2e.py") {
		t.Errorf("#2827 base #2802 behavior regressed: unrelated scripts overlap")
	}

	toks := reproCommandTokens(snippet)
	if toks[`{"command":"python3`] || toks["command"] {
		t.Errorf("#2827 envelope pseudo-token present in token set: %v", toks)
	}
	if !toks["reproduce_bug.py"] {
		t.Errorf("#2827 script token missing after unwrap: %v", toks)
	}

	toks2 := reproCommandTokens("python3 reproduce_bug.py")
	if !toks2["reproduce_bug.py"] || toks2["command"] {
		t.Errorf("#2827 bare command tokenization changed: %v", toks2)
	}

	toks3 := reproCommandTokens(`{"timeout": 30}`)
	if len(toks3) != 0 {
		t.Errorf("#2827 non-command envelope yielded distinctive tokens %v, want none (keys are shared pseudo-tokens)", toks3)
	}
}
