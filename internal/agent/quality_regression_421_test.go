package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #420: UserPrompt truncation must be rune-safe — 60 Chinese chars (180
// bytes) previously sliced at byte 100 producing invalid UTF-8 that JSON
// serialized into U+FFFD mojibake.
func TestQualityPromptRuneTruncation(t *testing.T) {
	cjk := strings.Repeat("中", 60) // 180 bytes, 60 runes
	got := truncateRunes(cjk, 100)
	if !utf8.ValidString(got) {
		t.Fatal("truncated prompt is not valid UTF-8")
	}
	if n := len([]rune(got)); n != 60 {
		t.Errorf("expected all 60 runes kept (under limit), got %d", n)
	}
	long := strings.Repeat("中", 80)
	got2 := truncateRunes(long, 100)
	if !utf8.ValidString(got2) {
		t.Fatal("truncated long prompt is not valid UTF-8")
	}
	if n := len([]rune(got2)); n != 80 {
		t.Errorf("expected exactly 80 runes (input shorter than 100+3 limit), got %d", n)
	}
	got3 := truncateRunes(strings.Repeat("中", 120), 100)
	if n := len([]rune(got3)); n != 100 {
		t.Errorf("expected exactly 100 runes from 120, got %d", n)
	}
	if !utf8.ValidString(got3) {
		t.Error("rune-truncated string must stay valid UTF-8")
	}
}

// #420: provider names containing "/" must not corrupt Compare() grouping.
func TestQualityCompareSlashProvider(t *testing.T) {
	s := NewResponseQualityScorer(10)
	stats := &RunStats{Success: true}
	s.ScoreRun(stats, "openrouter/anthropic", "claude-3.5")
	s.ScoreRun(stats, "openrouter/anthropic", "claude-3.5")
	s.ScoreRun(stats, "openrouter", "claude-3.5")

	comps := s.Compare()
	if len(comps) != 2 {
		t.Fatalf("expected 2 distinct provider/model groups, got %d: %+v", len(comps), comps)
	}
	for _, c := range comps {
		if c.Provider != "openrouter/anthropic" && c.Provider != "openrouter" {
			t.Errorf("provider mangled: %+v", c)
		}
		if c.Model != "claude-3.5" {
			t.Errorf("model mangled: %+v", c)
		}
	}
}

// #421/#422 regression-threshold tests were removed with the
// quality_regression detector itself (r156 consolidation into
// perf_baseline.go: see perf_baseline_score_test.go for the successor
// quality-score gating tests).
