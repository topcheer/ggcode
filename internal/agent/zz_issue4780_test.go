package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// #4780 (S-09): the argument-complexity gate scored chained commands by
// their whole-string bulk - individually-safe steps (gh pr merge + gh pr
// comment + patrol compound) aggregated pipes and length across the entire
// chain and crossed the threshold, rejecting the WHOLE command and forcing
// costly step-splitting. Scoring is now per-subcommand (max, not sum):
// a chain fires only when one of its subcommands is itself heavy.
func zz4780RunCmd(t *testing.T, cmd string) (int, bool) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": cmd})
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		t.Fatal(err)
	}
	score, _ := scoreArgComplexity("run_command", call)
	return score, score >= argComplexityThresholdFor("run_command")
}

func TestIssue4780_SafeChainNoLongerAggregates(t *testing.T) {
	// The observed incident shape: merge + comment + view compound, all
	// read-safe / individually light verbs, but a long total string with
	// several pipes inside segments.
	chain := strings.Join([]string{
		`gh pr merge 4859 --repo topcheer/ggcode --squash --delete-branch`,
		`gh pr comment 4779 --repo topcheer/ggcode --body "merged in 646720df9; thanks for the fake-gh end-to-end coverage and the once-per-branch cap work"`,
		`gh pr view 4859 --repo topcheer/ggcode --json state,mergedAt,mergeCommit -q '.state + " " + .mergedAt'`,
		`gh api 'repos/topcheer/ggcode/issues?state=open&per_page=100&sort=updated' -q '.[] | select(.pull_request == null) | .number'`,
		`git ls-remote --heads origin | grep -c 'issue-48'`,
	}, " && ")
	if len(chain) <= 400 {
		t.Fatalf("fixture must exceed the old length tier, got %d bytes", len(chain))
	}
	wholeScore := scoreShellComplexity(chain) // old whole-string view
	score, fires := zz4780RunCmd(t, chain)
	if fires {
		t.Fatalf("safe chain must not fire: per-sub score=%d (whole-string legacy score=%d)", score, wholeScore)
	}
	if score >= wholeScore {
		t.Logf("note: per-sub score %d vs legacy %d", score, wholeScore)
	}
}

func TestIssue4780_RiskySubcommandStillFires(t *testing.T) {
	// One destructive subcommand buried in an otherwise-safe chain.
	chain := `gh pr view 4859 --json state && rm -rf /tmp/scratch-dir && gh pr list --limit 5`
	if score, fires := zz4780RunCmd(t, chain); !fires {
		t.Fatalf("chain containing a hard-token subcommand must fire (score=%d)", score)
	}
	// Substitution-heavy single subcommand also still fires: >4 pipes,
	// 3 command substitutions, and >200 chars in ONE segment.
	heavy := `r=$(git log --oneline --decorate -30 origin/main) $(git status --porcelain=v1 --branch) $(git branch --list --format='%(refname:short) %(objectname:short)' 'fix-*' 'patrol-*') | grep -E 'issue|fix|patrol' | sort -u | uniq -c | sort -rn | head -10 | tee /tmp/ggcode-arg-complexity-fixture-output.txt`
	if score, fires := zz4780RunCmd(t, heavy); !fires {
		t.Fatalf("substitution-heavy subcommand must fire (score=%d)", score)
	}
}

func TestIssue4780_SubstitutionBoundaryKeptIntact(t *testing.T) {
	// A $() containing && must NOT be split: the substitution belongs to
	// its one subcommand. With the boundary honored, the segment keeps its
	// substitution weight and still fires.
	cmd := `v=$(gh pr list --state open && gh pr view 1) && echo "$v" | head -3`
	subs := splitShellSubcommands(cmd)
	if len(subs) != 2 {
		t.Fatalf("expected 2 subcommands ($() kept intact), got %d: %q", len(subs), subs)
	}
	if !strings.Contains(subs[0], "gh pr list --state open && gh pr view 1") {
		t.Fatalf("substitution body split across segments: %q", subs)
	}
}
