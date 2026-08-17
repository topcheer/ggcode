package context

// #634: the #623 uncovered-script freeze counted latinExt as uncovered.
// Vietnamese-style sessions (Latin Extended Additional, nearly every syllable
// carries a diacritic) pushed uncovered share over 20%, freezing EVERY
// calibration sample — asciiRatio never calibrated, regressing the #598
// drift family. latinExt must be treated as covered: the estimation side
// prices it on its own tier (3.0 chars/token, near ASCII's 3.5).

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Vietnamese-dominant text with code identifiers must record samples
// (pre-fix: frozen every time by the >20% uncovered share).
func TestIssue634_VietnameseSamplesNotFrozen(t *testing.T) {
	text := strings.Repeat("Xin chào, tôi là trợ lý thử nghiệm ngôn ngữ ", 60) +
		"func main() { println() }"

	a, c, le, cy, g, o := scriptTokenClasses(text)
	if le == 0 {
		t.Fatal("precondition: probe must contain Latin-Extended chars")
	}
	if cy != 0 || g != 0 || o != 0 {
		t.Fatalf("precondition: probe must be latinExt+ASCII only, got cy=%d g=%d other=%d", cy, g, o)
	}
	total := a + c + le + cy + g + o
	if float64(le)/float64(total) <= uncoveredScriptFreezeShare {
		t.Fatalf("precondition: latinExt share must exceed %.0f%% to exercise the old freeze, got %.1f%%",
			uncoveredScriptFreezeShare*100, float64(le)/float64(total)*100)
	}

	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 3; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 3 {
		t.Fatalf("Vietnamese-dominant samples must not be frozen (#634), got %d/3 samples", got)
	}
	// The freeze must not change #598: latinExt still does not feed the ratio
	// composition, so asciiRatio stays at default (nothing ASCII-dominant to
	// calibrate here beyond warmup anyway).
	if got := cm.calibrator.CJKCharsPerToken(); got != defaultCJKRatio {
		t.Fatalf("cjkRatio must stay at default (latinExt not in CJK bucket per #598), got %v", got)
	}
}

// Pure latinExt text (no ASCII at all) must also not be treated as
// "uncovered scripts only" — the (0,0) freeze branch requires latinExt==0.
func TestIssue634_PureLatinExtNotUncoveredOnly(t *testing.T) {
	text := strings.Repeat("ạậẹệịộợụựđ", 100) // Vietnamese Latin Extended Additional
	a, c, le, _, _, _ := scriptTokenClasses(text)
	if a != 0 || c != 0 || le == 0 {
		t.Fatalf("precondition: pure latinExt expected, got ascii=%d cjk=%d latinExt=%d", a, c, le)
	}
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 3; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 3 {
		t.Fatalf("pure latinExt samples must not hit the uncovered-only freeze (#634), got %d/3", got)
	}
}

// The freeze itself must keep working for genuinely uncovered scripts —
// Cyrillic prose stays frozen (#623 regression guard).
func TestIssue634_CyrillicStillFrozen(t *testing.T) {
	text := strings.Repeat("Привет мир эксперимент ", 52) + "identifier"
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 8; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 0 {
		t.Fatalf("Cyrillic-dominant samples must stay frozen (#623), got %d", got)
	}
}
