package agent

import (
	"strings"
	"testing"
)

// #1695 case 1: pure mutators behind xargs/find must NOT be exempt from
// the status scan - only segments with a downstream content retriever are.
func TestClaimVerifyXargsFindExemptionNeedsRetriever1695(t *testing.T) {
	// xargs rm -rf: NOT exempt -> scanned (soft failures visible).
	if isContentRetrievalCommand("xargs rm -rf /tmp/x") {
		t.Fatal("xargs rm -rf must not be exempt (pure mutator)")
	}
	// xargs grep: exempt (wrapper around content retrieval).
	if !isContentRetrievalCommand("xargs grep -l foo") {
		t.Fatal("xargs grep must stay exempt")
	}
	// find -delete: NOT exempt.
	if isContentRetrievalCommand("find . -delete") {
		t.Fatal("find -delete must not be exempt (pure mutator)")
	}
	// find -exec grep: exempt.
	if !isContentRetrievalCommand("find . -exec grep -l foo {} +") {
		t.Fatal("find -exec grep must stay exempt")
	}
}

// #1695 case 2: `git -C /path grep` reaches the content-subcommand
// exemption despite the leading global option.
func TestClaimVerifyGitDashCExemption1695(t *testing.T) {
	if !isContentRetrievalCommand("git -C /repo grep -n \"fail:\"") {
		t.Fatal("git -C /path grep must be exempt like bare git grep")
	}
	if isContentRetrievalCommand("git -C /repo push origin main") {
		t.Fatal("git -C push is not content-bearing")
	}
}

// #1695 case 3: a matched payload whose first LINE begins with the generic
// prefix ("no matches are allowed...") must not trip the zero-result
// advisory; a true meta-status line still must.
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
