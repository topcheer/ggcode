package tool

import "testing"

func TestIssue3854_TSVNewTextPreserved(t *testing.T) {

	newText := "id\tname\n123\talpha\n456\tbeta\nnote line\n"
	got := adjustNewText("content", newText, matchResult{transform: "line-numbers-stripped"})
	if got != newText {
		t.Fatalf("minority-numbered new_text must stay verbatim, got: %q", got)
	}
}

func TestIssue3854_NumberedNewTextStillStripped(t *testing.T) {

	newText := " 12\tfunc main() {\n 13\t\tprintln()\n 14\t}\n"
	got := adjustNewText("content", newText, matchResult{transform: "line-numbers-stripped"})
	want := "func main() {\n\tprintln()\n}\n"
	if got != want {
		t.Fatalf("padded-numbered new_text must be stripped, got: %q", got)
	}
}
