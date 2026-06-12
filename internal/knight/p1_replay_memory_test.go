package knight

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- A/B replay -----------------------------------------------------------

func TestComputeABReplayScoreReturnsZeroWithoutScenarios(t *testing.T) {
	c := &SkillEntry{Name: "x", Meta: SkillMeta{Description: "y"}}
	got := computeABReplayScore(c, "body", "", nil)
	if got.ScenariosConsidered != 0 || got.CandidateScore != 0 {
		t.Fatalf("expected empty result, got %#v", got)
	}
}

func TestComputeABReplayScoreCandidateBeatsBaseline(t *testing.T) {
	cand := &SkillEntry{
		Name: "qq-passive-window",
		Meta: SkillMeta{Description: "Track QQ passive reply window per msg id"},
	}
	candBody := "Steps\n- record qq passive reply window\n- track msg id\n"
	baseBody := "Steps\n- run go build\n- verify\n"
	scenarios := []SkillScenarioLogEntry{
		{Task: "fix qq passive reply window not tracking msg id", Success: true},
		{Task: "qq adapter passive reply window expired", Success: true},
		{Task: "completely unrelated frontend bug", Success: true},
	}
	res := computeABReplayScore(cand, candBody, baseBody, scenarios)
	if res.CandidateScore <= res.BaselineScore {
		t.Fatalf("candidate should beat baseline, got %#v", res)
	}
	if res.Delta <= 0 {
		t.Fatalf("expected positive delta, got %#v", res)
	}
	if len(res.TopMatchedTasks) == 0 {
		t.Fatalf("expected top matched tasks, got %#v", res)
	}
}

func TestComputeABReplayScoreFailedScenariosArePenalized(t *testing.T) {
	cand := &SkillEntry{Name: "x", Meta: SkillMeta{Description: "task alpha beta"}}
	body := "alpha beta gamma"
	good := []SkillScenarioLogEntry{{Task: "alpha beta gamma", Success: true}}
	bad := []SkillScenarioLogEntry{{Task: "alpha beta gamma", Success: false}}
	a := computeABReplayScore(cand, body, "", good)
	b := computeABReplayScore(cand, body, "", bad)
	if !(a.CandidateScore > b.CandidateScore) {
		t.Fatalf("failed scenario should yield lower score: good=%v bad=%v", a.CandidateScore, b.CandidateScore)
	}
}

func TestABReplayVerdictMapping(t *testing.T) {
	cases := []struct {
		name string
		in   ABReplayResult
		want string
	}{
		{"empty", ABReplayResult{}, "no recent scenarios"},
		{"no coverage", ABReplayResult{ScenariosConsidered: 5, CandidateScore: 0.01}, "no observed coverage"},
		{"better", ABReplayResult{ScenariosConsidered: 5, CandidateScore: 0.5, BaselineScore: 0.2, Delta: 0.3}, "covers recent tasks better than baseline"},
		{"worse", ABReplayResult{ScenariosConsidered: 5, CandidateScore: 0.2, BaselineScore: 0.5, Delta: -0.3}, "baseline already covers these tasks"},
		{"modest", ABReplayResult{ScenariosConsidered: 5, CandidateScore: 0.2, BaselineScore: 0.2, Delta: 0}, "modest overlap with recent tasks"},
	}
	for _, tc := range cases {
		if got := abReplayVerdict(tc.in); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// --- Semantic memory ------------------------------------------------------

func TestSemanticMemoryAppendAndRecent(t *testing.T) {
	dir := t.TempDir()
	store := newSemanticMemoryStore(filepath.Join(dir, "mem.jsonl"))
	for i, s := range []string{"first lesson", "second lesson", "third lesson"} {
		entry := SemanticMemoryEntry{Kind: "lesson", Summary: s, Time: time.Now().Add(time.Duration(i) * time.Second)}
		if err := store.Append(entry); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	out, err := store.Recent(0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(out))
	}
	if out[0].Summary != "third lesson" || out[2].Summary != "first lesson" {
		t.Fatalf("expected newest first, got %#v", out)
	}
}

func TestSemanticMemoryRejectsEmptySummary(t *testing.T) {
	dir := t.TempDir()
	store := newSemanticMemoryStore(filepath.Join(dir, "mem.jsonl"))
	if err := store.Append(SemanticMemoryEntry{Summary: "   "}); err == nil {
		t.Fatal("expected error for blank summary")
	}
}

func TestSemanticMemoryAutoTruncates(t *testing.T) {
	dir := t.TempDir()
	store := newSemanticMemoryStore(filepath.Join(dir, "mem.jsonl"))
	long := strings.Repeat("x", 5000)
	if err := store.Append(SemanticMemoryEntry{Summary: long}); err != nil {
		t.Fatalf("append: %v", err)
	}
	out, _ := store.Recent(1)
	if len([]rune(out[0].Summary)) > maxSemanticSummaryRunes+10 {
		t.Fatalf("expected truncation, got %d runes", len([]rune(out[0].Summary)))
	}
}

func TestSemanticMemoryCappedAtMaxEntries(t *testing.T) {
	dir := t.TempDir()
	store := newSemanticMemoryStore(filepath.Join(dir, "mem.jsonl"))
	for i := 0; i < maxSemanticMemoryEntries+25; i++ {
		_ = store.Append(SemanticMemoryEntry{Summary: "lesson", Time: time.Now().Add(time.Duration(i) * time.Microsecond)})
	}
	out, _ := store.Recent(0)
	if len(out) > maxSemanticMemoryEntries {
		t.Fatalf("expected ≤%d, got %d", maxSemanticMemoryEntries, len(out))
	}
}

// --- Knight integration ---------------------------------------------------

func TestKnightApproveProposalRecordsSemanticMemory(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)
	prop, err := k.writeProjectImprovementProposal("speed up CI", "# Speed up CI\n## Summary\nuse cache\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := k.ApproveProposal(prop.ID, "yes"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	mem, err := k.RecentSemanticMemory(10)
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	if len(mem) == 0 {
		t.Fatal("expected semantic memory entry after approving a proposal")
	}
	if !strings.Contains(mem[0].Summary, "Speed up CI") {
		t.Fatalf("memory should reference proposal title, got %q", mem[0].Summary)
	}
	if mem[0].Kind != "project-proposal-approved" {
		t.Fatalf("expected kind=project-proposal-approved, got %q", mem[0].Kind)
	}
}

func TestRecentRejectFeedbackEndToEnd(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)
	if err := k.rejects.Append(rejectFeedbackEntry{Name: "noisy-skill", Scope: "project", Action: "reject", Reason: "duplicate"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	lines := k.RecentRejectFeedback(5)
	if len(lines) != 1 || !strings.Contains(lines[0], "noisy-skill") {
		t.Fatalf("expected reject entry, got %#v", lines)
	}
	if err := k.ClearRejectFeedback(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := k.RecentRejectFeedback(5); len(got) != 0 {
		t.Fatalf("expected empty after clear, got %#v", got)
	}
}

func TestFormatRecentSemanticMemoryForEval(t *testing.T) {
	dir := t.TempDir()
	k := &Knight{projDir: dir}
	if got := k.formatRecentSemanticMemoryForEval(5); got != "" {
		t.Fatalf("expected empty when no memory exists, got %q", got)
	}
	if err := k.RecordSemanticMemory("lesson", "always run gofmt before commit", []string{"project:fmt"}, "test"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := k.RecordSemanticMemory("proposal", "prefer explicit error wrapping", nil, "test"); err != nil {
		t.Fatalf("record: %v", err)
	}
	got := k.formatRecentSemanticMemoryForEval(5)
	if !strings.Contains(got, "[lesson]") || !strings.Contains(got, "always run gofmt") {
		t.Fatalf("expected lesson rendered, got: %s", got)
	}
	if !strings.Contains(got, "[proposal]") || !strings.Contains(got, "explicit error wrapping") {
		t.Fatalf("expected proposal rendered, got: %s", got)
	}
}
