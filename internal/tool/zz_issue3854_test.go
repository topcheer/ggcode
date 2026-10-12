package tool

// #3854 probes: adjustNewText must not strip digit+TAB content from
// new_text unless a majority of its non-empty lines carry line-number
// prefixes - the same gate the old_text side applies.

import "testing"

func TestIssue3854_TSVNewTextPreserved(t *testing.T) {
	// old_text matched via line-number stripping, but new_text is TSV data
	// (digit+TAB rows) with no majority of numbered lines.
	newText := "id\tname\n123\talpha\n456\tbeta\nnote line\n"
	got := adjustNewText("content", newText, matchResult{transform: "line-numbers-stripped"})
	if got != newText {
		t.Fatalf("minority-numbered new_text must stay verbatim, got: %q", got)
	}
}

func TestIssue3854_NumberedNewTextStillStripped(t *testing.T) {
	// The common case: new_text pasted back from read_file with PADDED
	// prefixes (readFileRange right-aligns) - prefixes must come off.
	newText := " 12\tfunc main() {\n 13\t\tprintln()\n 14\t}\n"
	got := adjustNewText("content", newText, matchResult{transform: "line-numbers-stripped"})
	want := "func main() {\n\tprintln()\n}\n"
	if got != want {
		t.Fatalf("padded-numbered new_text must be stripped, got: %q", got)
	}
}
