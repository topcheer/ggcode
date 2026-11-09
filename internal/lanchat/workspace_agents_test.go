package lanchat

import (
	"strings"
	"testing"
)

func wsPeer(nodeID, nick, team, workspace string, online, busy bool) Participant {
	return Participant{
		NodeID:    nodeID,
		AgentNick: nick,
		Team:      team,
		Workspace: workspace,
		Online:    online,
		AgentBusy: busy,
	}
}

// Same participant set, different slice order, must render byte-identical
// output: this is the prompt-cache stability contract.
func TestFormatWorkspaceAgents_DeterministicOrder(t *testing.T) {
	peers := []Participant{
		wsPeer("n-3", "zeta_dev", "ggcode", "/w/repo", true, false),
		wsPeer("n-1", "alpha_reviewer", "ggcode", "/w/repo/.ggcode/worktrees/x", true, true),
		wsPeer("n-2", "beta_dev", "dev-team", "/w/repo", true, false),
		wsPeer("n-2b", "beta_dev", "ggcode", "/w/repo", true, true), // same nick, tie-break by NodeID
	}
	forward := FormatWorkspaceAgents(peers, "/w/repo", "self")
	reversed := FormatWorkspaceAgents([]Participant{peers[2], peers[0], peers[3], peers[1]}, "/w/repo", "self")
	if forward == "" {
		t.Fatal("expected non-empty section")
	}
	if forward != reversed {
		t.Fatalf("order-dependent rendering:\n---\n%s\n---\n%s", forward, reversed)
	}
	// sorted by nick asc, then node id
	lines := nonEmptyLines(forward)
	if len(lines) < 4 {
		t.Fatalf("expected 4 peer lines, got %d:\n%s", len(lines), forward)
	}
	wantFirst := "- alpha_reviewer (ggcode): busy"
	if lines[0] != wantFirst {
		t.Fatalf("first line = %q, want %q", lines[0], wantFirst)
	}
	if lines[1] != "- beta_dev (dev-team): idle" {
		t.Fatalf("nick tie not broken by NodeID: %q", lines[1])
	}
}

func TestFormatWorkspaceAgents_Filters(t *testing.T) {
	peers := []Participant{
		wsPeer("self", "me_coord", "ggcode", "/w/repo", true, false),                            // self: excluded
		wsPeer("n-off", "gone_dev", "ggcode", "/w/repo", false, false),                          // offline: excluded
		wsPeer("n-other", "other_ws", "ggcode", "/other/repo", true, false),                     // different workspace: excluded
		wsPeer("n-empty", "", "ggcode", "/w/repo", true, false),                                 // no agent nick: excluded
		wsPeer("n-wt", "worktree_dev", "ggcode", "/w/repo/.ggcode/worktrees/fix-1", true, true), // worktree under repo: included
	}
	got := FormatWorkspaceAgents(peers, "/w/repo", "self")
	if !strings.Contains(got, "worktree_dev") {
		t.Fatalf("worktree peer missing:\n%s", got)
	}
	for _, unwanted := range []string{"me_coord", "gone_dev", "other_ws"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("%s should be filtered:\n%s", unwanted, got)
		}
	}
	if n := len(nonEmptyLines(got)); n != 1 {
		t.Fatalf("expected exactly 1 peer line, got %d:\n%s", n, got)
	}
}

func TestFormatWorkspaceAgents_CapAndOverflow(t *testing.T) {
	peers := make([]Participant, 0, 20)
	for i := 0; i < 20; i++ {
		peers = append(peers, wsPeer(
			strings.Repeat("a", 3)+string(rune('a'+i)),
			"peer_"+string(rune('a'+i)),
			"ggcode", "/w/repo", true, false))
	}
	got := FormatWorkspaceAgents(peers, "/w/repo", "self")
	lines := nonEmptyLines(got)
	peerLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- peer_") {
			peerLines++
		}
	}
	if peerLines != maxWorkspaceAgentLines {
		t.Fatalf("expected %d peer lines, got %d", maxWorkspaceAgentLines, peerLines)
	}
	if !strings.Contains(got, "(+4 more; see lanchat list)") {
		t.Fatalf("overflow summary missing:\n%s", got)
	}
}

func TestFormatWorkspaceAgents_EmptyWhenNoPeers(t *testing.T) {
	if got := FormatWorkspaceAgents(nil, "/w/repo", "self"); got != "" {
		t.Fatalf("nil participants should render empty, got:\n%s", got)
	}
	onlySelf := []Participant{wsPeer("self", "me", "ggcode", "/w/repo", true, false)}
	if got := FormatWorkspaceAgents(onlySelf, "/w/repo", "self"); got != "" {
		t.Fatalf("self-only should render empty, got:\n%s", got)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "- ") {
			out = append(out, l)
		}
	}
	return out
}
