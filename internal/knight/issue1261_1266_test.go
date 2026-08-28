package knight

// Regression tests for GitHub issues #1261-#1266 (knight governance,
// overlap heuristics, scheduler error propagation, eval cooldown).

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- #1264: nameSimilar short-affix false positives ---

func TestNameSimilarShortAffixesRejected(t *testing.T) {
	// Coincidental short affixes must NOT count as similarity - they used to
	// hard-floor Jaccard at 0.6 and unconditionally block auto-promotion.
	falsePairs := [][2]string{
		{"go", "golang"},
		{"test", "latest"},  // 4-char shared suffix, still below the gate
		{"api", "rapid"},    // 3-char prefix
		{"ci", "circle-ci"}, // 2-char prefix
	}
	for _, p := range falsePairs {
		if nameSimilar(p[0], p[1]) {
			t.Fatalf("#1264: %q vs %q must not be name-similar (short affix)", p[0], p[1])
		}
	}
}

func TestNameSimilarSubstantialAffixesKept(t *testing.T) {
	truePairs := [][2]string{
		{"golang", "golang-ci"},    // shared prefix 6
		{"verify", "go-verify"},    // shared suffix 6
		{"go-verify", "go-verify"}, // exact
		{"Deploy", "deploy"},       // case-insensitive exact
	}
	for _, p := range truePairs {
		if !nameSimilar(p[0], p[1]) {
			t.Fatalf("#1264: %q vs %q must remain name-similar (substantial affix)", p[0], p[1])
		}
	}
}

// --- #1263: duplicate (scope, name) registration must be reported ---

func TestGovernanceDuplicateRegistrationReported(t *testing.T) {
	k := &Knight{}
	active := []*SkillEntry{
		{Scope: "project", Name: "go-verify", Path: "/ws/foo/SKILL.md"},
		{Scope: "project", Name: "go-verify", Path: "/ws/bar/SKILL.md"}, // same declared name, different dir
	}
	recs := k.findOverlapRecommendations(active)
	found := false
	for _, r := range recs {
		if strings.Contains(r.Reason, "duplicate registration") &&
			strings.Contains(r.Reason, "/ws/foo/SKILL.md") &&
			strings.Contains(r.Reason, "/ws/bar/SKILL.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("#1263: duplicate (scope,name) registration not reported: %+v", recs)
	}
}

func TestGovernanceDistinctNamesNoDupReport(t *testing.T) {
	k := &Knight{}
	active := []*SkillEntry{
		{Scope: "project", Name: "go-verify", Path: "/ws/a/SKILL.md"},
		{Scope: "project", Name: "go-deploy", Path: "/ws/b/SKILL.md"},
	}
	for _, r := range k.findOverlapRecommendations(active) {
		if strings.Contains(r.Reason, "duplicate registration") {
			t.Fatalf("#1263: distinct names must not be flagged as duplicate registration: %+v", r)
		}
	}
}

// --- #1261: proposal tasks get read-only system-prompt rules ---

func TestBuildKnightSystemPromptProposalReadOnly(t *testing.T) {
	proposal := buildKnightSystemPrompt("project-improvement-proposal")
	if !strings.Contains(proposal, "READ-ONLY TASK") {
		t.Fatal("#1261: proposal system prompt must carry the READ-ONLY rule block")
	}
	if !strings.Contains(proposal, "Do NOT use edit_file") {
		t.Fatal("#1261: proposal system prompt must name the forbidden write tools")
	}

	normal := buildKnightSystemPrompt("skill-generation")
	if strings.Contains(normal, "READ-ONLY TASK") {
		t.Fatal("#1261: non-proposal tasks must not get the read-only block")
	}
}

// --- #1261: working-tree diff counting ---

func TestCountStatusDiff(t *testing.T) {
	before := " M a.go\n?? b.txt\n"
	after := " M a.go\n?? c.txt\n M d.go\n" // b.txt gone, c.txt + d.go new
	if n := countStatusDiff(before, after); n != 3 {
		t.Fatalf("countStatusDiff = %d, want 3 (1 removed + 2 added)", n)
	}
	if n := countStatusDiff(before, before); n != 0 {
		t.Fatalf("identical snapshots must diff to 0, got %d", n)
	}
}

// --- #1265: eval cooldown constants wired ---

func TestEvalRetryCooldownConstant(t *testing.T) {
	if knightEvalRetryCooldown != 24*time.Hour {
		t.Fatalf("knightEvalRetryCooldown = %v, want 24h", knightEvalRetryCooldown)
	}
	k := New(config.KnightConfig{}, t.TempDir(), t.TempDir(), nil)
	if k.evalCooldownUntil == nil {
		t.Fatal("#1265: evalCooldownUntil map must be initialized")
	}
}
