package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue623_SpaceCountedASCIIBypassesFreeze verifies the #623 fix: the
// #605 G3 freeze used to require asciiChars+cjkChars == 0, but
// scriptTokenClasses counts spaces/punctuation as ASCII — Cyrillic prose
// with a single space returned asciiChars>0, bypassed the freeze, and fed
// RecordSample at asciiShare=1.0, driving asciiRatio 3.5→3.0 (clamp).
// The freeze must now trigger by uncovered-script SHARE, not absolute zero.
func TestIssue623_SpaceCountedASCIIBypassesFreeze(t *testing.T) {
	// Cyrillic prose + spaces + one ASCII identifier: reproduces the issue
	// probe (ascii count almost entirely space-derived, Cyrillic dominant).
	cyrillic := strings.Repeat("Привет мир эксперимент ", 52) + "identifier"

	a, c, le, cy, g, o := scriptTokenClasses(cyrillic)
	if a == 0 {
		t.Fatal("precondition: probe must contain space-derived ASCII chars")
	}
	if a+c == 0 {
		t.Fatal("precondition: old freeze boundary (ascii+cjk==0) not exercised — probe would have been frozen even pre-fix")
	}
	if le != 0 || g != 0 || o != 0 {
		t.Fatalf("precondition: probe should be Cyrillic+ASCII only, got latinExt=%d greek=%d other=%d", le, g, o)
	}
	if cy < a {
		t.Fatalf("precondition: Cyrillic must dominate the probe, cyrillic=%d ascii=%d", cy, a)
	}

	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: cyrillic}}})

	// Enough samples to pass warmup and land on adjustment steps. Pre-fix,
	// each adjustment drove asciiRatio down toward its 3.0 clamp.
	for i := 0; i < 16; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}

	if got := cm.calibrator.SampleCount(); got != 0 {
		t.Fatalf("calibration samples must be frozen for space-containing Cyrillic text, got %d samples", got)
	}
	if got := cm.calibrator.ASCIICharsPerToken(); got != defaultASCIIRatio {
		t.Fatalf("asciiRatio polluted by uncovered-script sample: got %v, want default %v", got, defaultASCIIRatio)
	}
	if got := cm.calibrator.CJKCharsPerToken(); got != defaultCJKRatio {
		t.Fatalf("cjkRatio polluted: got %v, want default %v", got, defaultCJKRatio)
	}
}

// TestIssue623_SpaceFreeCyrillicStillFrozen: #605 G3's original (0,0)
// boundary must keep working — Cyrillic text with NO ASCII at all.
func TestIssue623_SpaceFreeCyrillicStillFrozen(t *testing.T) {
	text := strings.Repeat("Приветмир", 100) // no spaces, no ASCII
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 8; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 0 {
		t.Fatalf("pure uncovered-script text must stay frozen (#605 G3 regression), got %d samples", got)
	}
}

// TestIssue623_DominantCoveredScriptsStillCalibrate: the freeze must not
// over-trigger — English text (optionally with a small Cyrillic quote) is
// dominated by covered scripts and must still record samples.
func TestIssue623_DominantCoveredScriptsStillCalibrate(t *testing.T) {
	cm := NewManager(100000)
	text := strings.Repeat("hello world the quick brown fox jumps over the lazy dog ", 20) +
		strings.Repeat("Привет ", 5) // ~4% uncovered — well under the 20% share
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 3; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 3 {
		t.Fatalf("covered-script-dominant text must still record samples, got %d", got)
	}
}

// TestIssue623_CJKDominantStillCalibrates: CJK is a covered script — a
// Chinese session must never be frozen by the share check.
func TestIssue623_CJKDominantStillCalibrates(t *testing.T) {
	cm := NewManager(100000)
	text := strings.Repeat("你好世界测试 ", 100)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 3; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 3 {
		t.Fatalf("CJK-dominant text must still record samples, got %d", got)
	}
}
