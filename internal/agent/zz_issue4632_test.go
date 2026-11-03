package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4632_FlagAnchorWholeTokenMatch(t *testing.T) {
	const anchor = "--force"

	if !refusalAnchorHit("git push --force origin main", anchor) {
		t.Fatal("exact flag token must match (#4632)")
	}

	if !refusalAnchorHit("--force", anchor) {
		t.Fatal("bare flag token must match (#4632)")
	}

	if refusalAnchorHit("git push --force-with-lease origin main", anchor) {
		t.Fatal("prefix-polluted longer flag must NOT match (#4632)")
	}

	if refusalAnchorHit("run --xx--force-yy now", anchor) {
		t.Fatal("anchor embedded in a longer token must NOT match (#4632)")
	}
}

func TestIssue4632_NonFlagAnchorUnchanged(t *testing.T) {
	if !refusalAnchorHit("/api/v1/keys", "/api/v1/keys") {
		t.Fatal("non-flag anchor must keep Contains semantics (#4632)")
	}
	if refusalAnchorHit("no match here", "/api/v1/keys") {
		t.Fatal("unrelated text must not match (#4632)")
	}
}

func TestIssue4632_HardBlockUsesBoundaryMatch(t *testing.T) {
	data, err := os.ReadFile("refusal_ledger.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	fn := strings.Index(text, "func (l *refusalLedger) checkBlocked")
	end := fn + 2000
	if end > len(text) {
		end = len(text)
	}
	body := text[fn:end]
	if strings.Contains(body, "strings.Contains(lower, t)") {
		t.Fatal("checkBlocked must use refusalAnchorHit, not bare Contains (#4632)")
	}
	if !strings.Contains(body, "refusalAnchorHit(lower, t)") {
		t.Fatal("checkBlocked wiring to refusalAnchorHit missing (#4632)")
	}
}
