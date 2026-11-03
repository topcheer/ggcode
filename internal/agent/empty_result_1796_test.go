package agent

import "testing"

func Test1796EmptyResultAnchors(t *testing.T) {

	for _, out := range []string{
		`No files matched pattern "*.zzz" in "/tmp".`,
		`No files matched query "zzz". Try different keywords or broader search terms.`,
		"No differences found.",
		"No output.",
		"No matches found.",
		"No commits found.",
	} {
		if !isEmptyResult(out) {
			t.Errorf("zero-result emission not recognized: %q", out)
		}
	}

	for _, out := range []string{
		"Found 10 matches:\n\nsrc/a.go:1:x\nsrc/b.go:2:y",
		"Found 100 matches:\n\n" + repeatN("src/f.go:1:x\n", 20),
		"Showing 20 of 40 matches:\n\n" + repeatN("src/f.go:1:x\n", 20),
	} {
		if isEmptyResult(out) {
			t.Errorf("successful result flagged as empty (FP): %.40q", out)
		}
	}

	if isEmptyResult("Truncated: showing 0 of 0 buffer\nbut 3 files were read: a.go b.go c.go") {
		t.Error("phantom 'showing 0 of 0' still deciding on data-bearing output")
	}

	long := repeatN("the program produced no output because of flags\n", 30)
	if isEmptyResult(long) {

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
