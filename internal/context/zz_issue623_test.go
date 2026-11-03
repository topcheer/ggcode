package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue623_SpaceCountedASCIIBypassesFreeze(t *testing.T) {
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

func TestIssue623_SpaceFreeCyrillicStillFrozen(t *testing.T) {
	text := strings.Repeat("Приветмир", 100)
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 8; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 0 {
		t.Fatalf("pure uncovered-script text must stay frozen (#605 G3 regression), got %d samples", got)
	}
}

func TestIssue623_DominantCoveredScriptsStillCalibrate(t *testing.T) {
	cm := NewManager(100000)
	text := strings.Repeat("hello world the quick brown fox jumps over the lazy dog ", 20) +
		strings.Repeat("Привет ", 5)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 3; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 3 {
		t.Fatalf("covered-script-dominant text must still record samples, got %d", got)
	}
}

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
