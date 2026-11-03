package im

import "strings"
import "testing"

func TestIssue3572_ExpandingFoldNoMisalignment(t *testing.T) {

	text := "İstanbul @alice harika"
	got := stripLocalPartMention(text, "alice")
	if got != "İstanbul  harika" {
		t.Fatalf("expanding-fold text mangled: got %q", got)
	}
}

func TestIssue3572_CaseInsensitiveAndBoundaryKept(t *testing.T) {
	if got := stripLocalPartMention("hey @Alice hi", "alice"); got != "hey  hi" {
		t.Fatalf("case-insensitive match regressed: %q", got)
	}

	if got := stripLocalPartMention("hey @alicebot hi", "alice"); got != "hey @alicebot hi" {
		t.Fatalf("boundary rule regressed: %q", got)
	}

	if got := stripLocalPartMention("hey @aliceé hi", "alice"); got != "hey é hi" {
		t.Fatalf("non-ASCII boundary should strip: %q", got)
	}
}

func TestIssue3572_MultibyteBeforeAndAfter(t *testing.T) {
	text := "中文 @bob 讯息 İmotion @bob end"
	got := stripLocalPartMention(text, "bob")
	if strings.Contains(got, "@bob") {
		t.Fatalf("mentions after multibyte runs not stripped: %q", got)
	}

	for _, want := range []string{"中文", "讯息", "İmotion", "end"} {
		if !strings.Contains(got, want) {
			t.Fatalf("multibyte content lost: %q missing %q", got, want)
		}
	}
}
