package agent

import "testing"

// Pins #5339: #5269 made bare shell control keywords transparent, but the
// switch returned true for the WHOLE segment — same-line compound bodies
// ("do rm -rf x", "then git push ...") escaped classification entirely.
// After the fix: bare keyword segments stay transparent, keyword-leading
// segments are classified by the body command.
func TestIssue5339CompoundBodyNotTransparent(t *testing.T) {
	failing := []string{
		"for f in *.log; do rm important.txt; done",  // issue row 1 (non-destructor form kept: plain rm)
		"while true; do curl -X POST http://x; done", // issue row 2
		"if [ -d x ]; then git push origin main; fi", // issue row 3
		"do rm important.txt",                        // same-line body, standalone
		"then git push origin main",                  // same-line body, standalone
		"{ rm important.txt; }",                      // brace group
	}
	for _, cmd := range failing {
		if irrevIsReadOnlyCommand(cmd) {
			t.Errorf("compound body must NOT classify as read-only (escaped #5269 passthrough): %q", cmd)
		}
	}

	// Bare keyword segments stay transparent (#5269 contract preserved).
	bare := []string{"for n in 1 2", "do", "done", "while", "{", "}", "if [ -d x ]", "then", "fi"}
	for _, seg := range bare {
		if !irrevIsReadOnlyCommand(seg) {
			t.Errorf("bare keyword segment must stay transparent (#5269 contract): %q", seg)
		}
	}

	// The #5269 FP fix must not regress: quoted jq pipes stay intact.
	jq := "gh api repos/x/y -q '.[] | select(.x) | .number'"
	if !irrevIsReadOnlyCommand(jq) {
		t.Errorf("#5269 quoted-jq FP fix regressed: %q must classify read-only", jq)
	}
}
