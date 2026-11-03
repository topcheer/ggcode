package im

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4645_DMLookupFailsOpen(t *testing.T) {
	data, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	fn := strings.Index(text, "func (a *matrixAdapter) checkIsDMViaAPI")
	if fn < 0 {
		t.Fatal("checkIsDMViaAPI not found")
	}
	end := fn + 2200
	if end > len(text) {
		end = len(text)
	}
	body := text[fn:end]

	for _, want := range []string{
		"m.direct lookup room=%s error (failing open)",
		"checkIsDMViaAPI room=%s error (failing open)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("fail-open error path missing: %q (#4645)", want)
		}
	}
	if strings.Count(body, "failing open") != 2 {
		t.Fatalf("expected exactly 2 fail-open error paths, found %d (#4645)", strings.Count(body, "failing open"))
	}

	if !strings.Contains(body, "len(members.Joined) == 2") {
		t.Fatal("2-member DM heuristic must be preserved (#961)")
	}
}

func TestIssue4646_ReplaceUsesNewContent(t *testing.T) {
	data, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "rel.Type == event.RelReplace") {
		t.Fatal("m.replace classification missing on the inbound path (#4646)")
	}
	if !strings.Contains(text, "content.NewContent.Body") {
		t.Fatal("edit handling must take the m.new_content body (#4646)")
	}
	if !strings.Contains(text, `strings.TrimPrefix(strings.TrimLeft(body, " "), "* ")`) {
		t.Fatal("malformed-edit fallback must strip the '* ' prefix (#4646)")
	}
}
