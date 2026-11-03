package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQualityPromptRuneTruncation(t *testing.T) {
	cjk := strings.Repeat("中", 60)
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

func TestRegressionSeverityCoversAllSignals(t *testing.T) {

	bs := baselineStats{meanScore: 0.8, minScore: 0.75, meanIter: 1.0, meanErrRate: 0, count: 5}
	cur := QualityEntry{Score: 0.79, Signals: QualitySignals{IterationRatio: 1.8, ErrorRate: 0}}
	if s := classifyRegression(0.01, cur, bs); s != SeverityMinor {
		t.Errorf("1.8x iteration inflation should be Minor, got %s", s)
	}

	cur.Signals.IterationRatio = 2.2
	if s := classifyRegression(0.01, cur, bs); s != SeverityModerate {
		t.Errorf("2.2x iteration inflation should be Moderate, got %s", s)
	}

	cur.Signals.IterationRatio = 1.0
	cur.Signals.ErrorRate = 0.5
	if s := classifyRegression(0.01, cur, bs); s != SeverityMinor {
		t.Errorf("error regression should be at least Minor, got %s", s)
	}
}

func TestRegressionErrorThresholdContinuity(t *testing.T) {

	below := baselineStats{meanScore: 0.8, minScore: 0.75, meanIter: 1, meanErrRate: 0.149, count: 5}
	above := baselineStats{meanScore: 0.8, minScore: 0.75, meanIter: 1, meanErrRate: 0.151, count: 5}

	for name, bs := range map[string]baselineStats{"below": below, "above": above} {
		cur := QualityEntry{Score: 0.79, Signals: QualitySignals{IterationRatio: 1.0, ErrorRate: 0.30}}
		detect := cur.Signals.ErrorRate > maxF(regressionErrorFloor, bs.meanErrRate*regressionErrorMultiple)
		if !detect {
			t.Errorf("baseline %s: current 0.30 should trigger (threshold %.3f)", name, maxF(regressionErrorFloor, bs.meanErrRate*regressionErrorMultiple))
		}
	}

	thrBelow := maxF(regressionErrorFloor, below.meanErrRate*regressionErrorMultiple)
	thrAbove := maxF(regressionErrorFloor, above.meanErrRate*regressionErrorMultiple)
	if thrBelow > thrAbove+0.001 {
		t.Errorf("threshold discontinuity: below-floor baseline (%.3f) harder to trigger than above-floor (%.3f)", thrBelow, thrAbove)
	}

	cur := QualityEntry{Score: 0.79, Signals: QualitySignals{IterationRatio: 1.0, ErrorRate: 0.151}}
	if cur.Signals.ErrorRate > maxF(regressionErrorFloor, above.meanErrRate*regressionErrorMultiple) {
		t.Error("current == baseline (1.0x) must not trigger")
	}
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
