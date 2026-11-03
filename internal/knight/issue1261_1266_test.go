package knight

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNameSimilarShortAffixesRejected(t *testing.T) {

	falsePairs := [][2]string{
		{"go", "golang"},
		{"test", "latest"},
		{"api", "rapid"},
		{"ci", "circle-ci"},
	}
	for _, p := range falsePairs {
		if nameSimilar(p[0], p[1]) {
			t.Fatalf("#1264: %q vs %q must not be name-similar (short affix)", p[0], p[1])
		}
	}
}

func TestNameSimilarSubstantialAffixesKept(t *testing.T) {
	truePairs := [][2]string{
		{"golang", "golang-ci"},
		{"verify", "go-verify"},
		{"go-verify", "go-verify"},
		{"Deploy", "deploy"},
	}
	for _, p := range truePairs {
		if !nameSimilar(p[0], p[1]) {
			t.Fatalf("#1264: %q vs %q must remain name-similar (substantial affix)", p[0], p[1])
		}
	}
}

func TestGovernanceDuplicateRegistrationReported(t *testing.T) {
	k := &Knight{}
	active := []*SkillEntry{
		{Scope: "project", Name: "go-verify", Path: "/ws/foo/SKILL.md"},
		{Scope: "project", Name: "go-verify", Path: "/ws/bar/SKILL.md"},
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

func TestCountStatusDiff(t *testing.T) {
	before := " M a.go\n?? b.txt\n"
	after := " M a.go\n?? c.txt\n M d.go\n"
	if n := countStatusDiff(before, after); n != 3 {
		t.Fatalf("countStatusDiff = %d, want 3 (1 removed + 2 added)", n)
	}
	if n := countStatusDiff(before, before); n != 0 {
		t.Fatalf("identical snapshots must diff to 0, got %d", n)
	}
}

func TestEvalRetryCooldownConstant(t *testing.T) {
	if knightEvalRetryCooldown != 24*time.Hour {
		t.Fatalf("knightEvalRetryCooldown = %v, want 24h", knightEvalRetryCooldown)
	}
	k := New(config.KnightConfig{}, t.TempDir(), t.TempDir(), nil)
	if k.evalCooldownUntil == nil {
		t.Fatal("#1265: evalCooldownUntil map must be initialized")
	}
}

func TestFilterKnightBookkeeping(t *testing.T) {
	before := " M main.go\n?? .ggcode/\n"
	after := " M main.go\n?? .ggcode/\n?? .ggcode/knight/usage-2026-08-29.jsonl\n"

	if n := countStatusDiff(before, after); n != 0 {
		t.Fatalf("bookkeeping-only drift must not count as violation, got %d", n)
	}

	violating := after + " M src/app.go\n"
	if n := countStatusDiff(before, violating); n != 1 {
		t.Fatalf("project-file drift must count, got %d", n)
	}

	quoted := "?? \".ggcode/my dir/file\"\n"
	if filterKnightBookkeeping(quoted) != "" {
		t.Fatalf("quoted .ggcode path must be filtered, got %q", filterKnightBookkeeping(quoted))
	}
}
