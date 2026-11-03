package agent

import (
	"strings"
	"testing"
)

func TestClaimVerifyXargsFindExemptionNeedsRetriever1695(t *testing.T) {

	if isContentRetrievalCommand("xargs rm -rf /tmp/x") {
		t.Fatal("xargs rm -rf must not be exempt (pure mutator)")
	}

	if !isContentRetrievalCommand("xargs grep -l foo") {
		t.Fatal("xargs grep must stay exempt")
	}

	if isContentRetrievalCommand("find . -delete") {
		t.Fatal("find -delete must not be exempt (pure mutator)")
	}

	if !isContentRetrievalCommand("find . -exec grep -l foo {} +") {
		t.Fatal("find -exec grep must stay exempt")
	}
}

func TestClaimVerifyGitDashCExemption1695(t *testing.T) {
	if !isContentRetrievalCommand("git -C /repo grep -n \"fail:\"") {
		t.Fatal("git -C /path grep must be exempt like bare git grep")
	}
	if isContentRetrievalCommand("git -C /repo push origin main") {
		t.Fatal("git -C push is not content-bearing")
	}
}

func TestClaimVerifyMetaPrefixFirstLine1695(t *testing.T) {
	c := newClaimVerifyState()
	if msg := c.check("grep", "no matches are allowed in strict mode\nsrc line 1", false, "grep x y"); msg != "" {
		t.Fatalf("payload first line must not trip advisory: %q", msg)
	}
	c2 := newClaimVerifyState()
	if msg := c2.check("grep", "no matches found", false, "grep x y"); msg == "" {
		t.Fatal("true meta-status line must trigger advisory")
	}
	c3 := newClaimVerifyState()
	if msg := c3.check("grep", "no matches found for pattern: foo", false, "grep x y"); msg == "" {
		t.Fatal("qualified meta-status line must trigger advisory")
	}
}

var _ = strings.TrimSpace
