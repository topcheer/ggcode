package agent

import (
	"os"
	"strings"
	"testing"
)

// #4632: flag anchors must match as whole tokens. A refusal against the
// plain force-push flag must NOT block its safer lease variant (prefix
// pollution), while exact-token mentions must still block.
func TestIssue4632_FlagAnchorWholeTokenMatch(t *testing.T) {
	const anchor = "--force"

	// Exact token: must hit.
	if !refusalAnchorHit("git push --force origin main", anchor) {
		t.Fatal("exact flag token must match (#4632)")
	}
	// Token at string edges: must hit.
	if !refusalAnchorHit("--force", anchor) {
		t.Fatal("bare flag token must match (#4632)")
	}
	// The safer lease variant merely contains the anchor as a prefix:
	// must NOT hit (the filed scenario).
	if refusalAnchorHit("git push --force-with-lease origin main", anchor) {
		t.Fatal("prefix-polluted longer flag must NOT match (#4632)")
	}
	// Left-boundary: an anchor inside a longer token must NOT hit either.
	if refusalAnchorHit("run --xx--force-yy now", anchor) {
		t.Fatal("anchor embedded in a longer token must NOT match (#4632)")
	}
}

// Non-flag anchors keep plain Contains semantics (paths, words).
func TestIssue4632_NonFlagAnchorUnchanged(t *testing.T) {
	if !refusalAnchorHit("/api/v1/keys", "/api/v1/keys") {
		t.Fatal("non-flag anchor must keep Contains semantics (#4632)")
	}
	if refusalAnchorHit("no match here", "/api/v1/keys") {
		t.Fatal("unrelated text must not match (#4632)")
	}
}

// Source pin: the hard-block loop uses refusalAnchorHit, not bare Contains.
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
