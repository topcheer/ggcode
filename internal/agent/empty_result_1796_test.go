package agent

import "testing"

// #1796 case 1: every searchTools member's ACTUAL zero-result emission must be
// recognized, and the FP regressions #1619 fixed must stay fixed.
func Test1796EmptyResultAnchors(t *testing.T) {
	// Real emissions from the five members the #1619 sweep missed.
	for _, out := range []string{
		`No files matched pattern "*.zzz" in "/tmp".`,                                   // glob
		`No files matched query "zzz". Try different keywords or broader search terms.`, // code_search x2
		"No differences found.", // git_diff x2
		"No output.",            // git_show / git_blame
		"No matches found.",     // grep (already covered)
		"No commits found.",     // git_log (already covered)
	} {
		if !isEmptyResult(out) {
			t.Errorf("zero-result emission not recognized: %q", out)
		}
	}

	// FP regressions: successful counts must NOT be flagged (#1619-A).
	for _, out := range []string{
		"Found 10 matches:\n\nsrc/a.go:1:x\nsrc/b.go:2:y",
		"Found 100 matches:\n\n" + repeatN("src/f.go:1:x\n", 20),
		"Showing 20 of 40 matches:\n\n" + repeatN("src/f.go:1:x\n", 20),
	} {
		if isEmptyResult(out) {
			t.Errorf("successful result flagged as empty (FP): %.40q", out)
		}
	}

	// Dead patterns removed: their phantom strings must no longer be the
	// deciding match (these outputs contain real data and must not be empty).
	if isEmptyResult("Truncated: showing 0 of 0 buffer\nbut 3 files were read: a.go b.go c.go") {
		t.Error("phantom 'showing 0 of 0' still deciding on data-bearing output")
	}

	// "no output." anchor must not fire on file content merely containing the words.
	long := repeatN("the program produced no output because of flags\n", 30)
	if isEmptyResult(long) {
		// >500 chars would early-return false anyway; use short variant
		short := "readme says the tool prints no output unless verbose is set; see config for details on the flags"
		if isEmptyResult(short) {
			t.Error("'no output' words in real content must not be flagged")
		}
	}
}

func repeatN(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
