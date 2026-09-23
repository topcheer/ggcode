package agent

import (
	"strings"
	"testing"
)

// #2684: the git_commit→git_revert annihilation pair ignored the revert
// target. Within the 20-call window, ANY successful git_revert after ANY
// git_commit fired a "net-zero state change / work was wasted" warning --
// including the first-class workflow of reverting an unrelated historical
// commit to fix a bug (the tool description itself guides git_log first).
// The fix records each commit's resulting hash (wiring parses the
// "[branch hash] summary" line) and only warns on a hash-prefix match.

func TestIssue2684_RevertUnrelatedHistoricalCommitSilent(t *testing.T) {
	s := newActionAnnihilateState()
	// Agent commits its own fix (wiring records the new hash).
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "fix A"}), 1)
	s.recordCommitHash(1, "5c2b1a3")
	// Normal intermediate work (reads/tests) -- not barriers, intentionally.
	s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "x.go"}), 2)
	s.recordToolCall("run_command", rawJSON(t, map[string]interface{}{"command": "go test ./..."}), 3)
	// Revert an unrelated pre-existing historical commit.
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "999dead"}), 4)

	if warn != "" {
		t.Fatalf("revert of unrelated historical commit must stay silent, got: %s", warn)
	}
}

func TestIssue2684_RevertOwnCommitStillWarns(t *testing.T) {
	cases := []struct {
		name     string
		recorded string // hash parsed from the commit result
		arg      string // commit arg of the revert
	}{
		{"abbrev-recorded-full-arg", "5c2b1a3", "5c2b1a3f9e2d4c6b8a0f1e2d3c4b5a697890d1e2"},
		{"full-recorded-abbrev-arg", "5c2b1a3f9e2d4c6b8a0f1e2d3c4b5a697890d1e2", "5c2b1a3"},
		{"exact-match", "5c2b1a3", "5c2b1a3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newActionAnnihilateState()
			s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "m"}), 1)
			s.recordCommitHash(1, c.recorded)
			warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": c.arg}), 2)
			if warn == "" {
				t.Fatal("revert of the agent's own commit must still warn (true positive)")
			}
		})
	}
}

func TestIssue2684_RevertOwnCommitCaseInsensitive(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "m"}), 1)
	s.recordCommitHash(1, "5C2B1A3")
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "5c2b1a3"}), 2)
	if warn == "" {
		t.Fatal("hash comparison must be case-insensitive (git hex)")
	}
}

func TestIssue2684_NoHashRecordedFailsSafe(t *testing.T) {
	s := newActionAnnihilateState()
	// Commit whose result could not be parsed (non-git VCS, unexpected
	// output): no hash recorded. A revert must stay silent rather than
	// fire a factually false net-zero claim we cannot verify.
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "m"}), 1)
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "abc1234"}), 2)
	if warn != "" {
		t.Fatalf("unverifiable commit target must fail safe (silent), got: %s", warn)
	}
}

func TestIssue2684_MultipleCommitsRevertOlderOwn(t *testing.T) {
	// Two own commits; the agent reverts the FIRST one. The backward scan
	// must attribute to the matching older commit, not the most recent.
	s := newActionAnnihilateState()
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "A"}), 1)
	s.recordCommitHash(1, "aaa1111")
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "B"}), 2)
	s.recordCommitHash(2, "bbb2222")
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "aaa1111"}), 3)
	if warn == "" {
		t.Fatal("revert of an older own commit (hash match) is still an annihilation")
	}
	if !strings.Contains(warn, "Iterations 1-3") {
		t.Fatalf("warning must attribute to the reverted commit's iteration 1, got: %s", warn)
	}
}

func TestExtractNewCommitHash(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"standard-commit-line",
			"[main 5c2b1a3] fix A\n 3 files changed, 10 insertions(+)",
			"5c2b1a3",
		},
		{
			"long-abbrev",
			"[feat/x 9f8e7d6a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e] work\n 1 file changed",
			"9f8e7d6a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e",
		},
		{
			"prefixed-by-advisory-output",
			"Committed successfully.\n\n[main 1a2b3c4] msg\n 2 files changed",
			"1a2b3c4",
		},
		{"nothing-to-commit", "On branch main\nnothing to commit, working tree clean", ""},
		{"bare-success", "Committed successfully.", ""},
		{"empty", "", ""},
		{"non-git-vcs", "Committed successfully. (jj)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractNewCommitHash(c.content)
			if got != c.want {
				t.Fatalf("extractNewCommitHash(%q) = %q, want %q", c.content, got, c.want)
			}
		})
	}
}

func TestIssue2684_WindowPruneDropsHashes(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordToolCall("git_commit", rawJSON(t, map[string]interface{}{"message": "m"}), 1)
	s.recordCommitHash(1, "aaa1111")
	// Fill the 20-action window so the commit leaves it.
	for i := 2; i <= s.lookback+1; i++ {
		s.recordToolCall("read_file", rawJSON(t, map[string]interface{}{"path": "f.go"}), i)
	}
	s.mu.Lock()
	n := len(s.commitHashes)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("commit hash for pruned iteration must be dropped, map size = %d", n)
	}
	// And a later revert of the pruned hash must stay silent.
	warn := s.recordToolCall("git_revert", rawJSON(t, map[string]interface{}{"commit": "aaa1111"}), s.lookback+2)
	if warn != "" {
		t.Fatalf("revert after the commit left the window must stay silent, got: %s", warn)
	}
}

func TestIssue2684_ResetClearsHashes(t *testing.T) {
	s := newActionAnnihilateState()
	s.recordCommitHash(1, "aaa1111")
	s.reset()
	s.mu.Lock()
	n := len(s.commitHashes)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("reset must clear commitHashes, map size = %d", n)
	}
}
