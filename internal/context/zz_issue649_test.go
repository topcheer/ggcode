package context

// #649: after #634 unfroze Vietnamese samples, their calibration residual
// was 100% attributed to asciiRatio (RecordSample ignored latinExtChars):
// estimated=chars/3.0 < actual pushed factor<1 on every sample until
// asciiRatio hit its 3.0 clamp, overestimating pure-ASCII tokens ~17% and
// firing auto-compact early. The residual belongs to the fixed latinExt
// tier — samples dominated by latinExt must not adjust any ratio.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Vietnamese-dominant samples (latinExt > 50% of covered composition) must
// leave asciiRatio at the default — pre-fix they drove it to the 3.0 clamp.
func TestIssue649_LatinExtDominantSamplesDoNotDragAsciiRatio(t *testing.T) {
	cal := NewTokenCalibrator()
	text := strings.Repeat("ậẹộộịụự ếềễểẫ", 60) +
		"func main() { println() }"
	a, c, le, cy, g, o := scriptTokenClasses(text)
	if cy != 0 || g != 0 || o != 0 || c != 0 {
		t.Fatalf("precondition: latinExt+ASCII probe only, got cy=%d g=%d o=%d cjk=%d", cy, g, o, c)
	}
	if float64(le)/float64(a+le) <= latinExtDominantShare {
		t.Fatalf("precondition: latinExt share must exceed %.0f%%, got %.1f%%",
			latinExtDominantShare*100, float64(le)/float64(a+le)*100)
	}

	// estimated = chars/3.0 tier (under-estimate → factor<1 → pre-fix pushed
	// asciiRatio down toward the 3.0 clamp on every adjustment sample).
	cal = NewTokenCalibrator() // fresh instance: the probes above were precondition-only
	for i := 0; i < 40; i++ {
		cal.RecordSample(1000, 1400, a, c, le)
	}
	if got := cal.ASCIICharsPerToken(); got != defaultASCIIRatio {
		t.Fatalf("latinExt-dominant residual must not touch asciiRatio (#649): got %.3f want %.3f (clamp was %.1f)",
			got, defaultASCIIRatio, asciiRatioMin)
	}
	if got := cal.CJKCharsPerToken(); got != defaultCJKRatio {
		t.Fatalf("latinExt-dominant residual must not touch cjkRatio (#649): got %v want %v", got, defaultCJKRatio)
	}
}

// End-to-end via Manager.RecordUsage: a Vietnamese-dominant context must not
// freeze the sample entirely (#634 keeps working) AND must not drag asciiRatio
// to its clamp (#649).
func TestIssue649_ManagerVietnameseResidualNotAttributedToAscii(t *testing.T) {
	text := strings.Repeat("ậẹộộịụự ếềễểẫ", 60) +
		"func main() { println() }"
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}})
	for i := 0; i < 40; i++ {
		cm.RecordUsage(provider.TokenUsage{InputTokens: 200, OutputTokens: 1})
	}
	if got := cm.calibrator.SampleCount(); got != 40 {
		t.Fatalf("Vietnamese samples must stay unfrozen (#634), got %d/40", got)
	}
	if got := cm.calibrator.ASCIICharsPerToken(); got != defaultASCIIRatio {
		t.Fatalf("asciiRatio must stay at default despite latinExt residual (#649): got %.3f want %.3f", got, defaultASCIIRatio)
	}
}

// A latinExt-minority sample (accents in mostly-ASCII code) must still
// calibrate asciiRatio — the fix must not freeze ordinary mixed code.
func TestIssue649_MinorityLatinExtStillCalibratesAscii(t *testing.T) {
	cal := NewTokenCalibrator()
	// 4000 ascii + 100 latinExt = 2.4% latinExt — well below the freeze share.
	for i := 0; i < 8; i++ {
		cal.RecordSample(2000, 1000, 4000, 0, 100)
	}
	if got := cal.ASCIICharsPerToken(); got <= defaultASCIIRatio {
		t.Fatalf("minority-latinExt sample must still adjust asciiRatio upward: got %v want > %v", got, defaultASCIIRatio)
	}
}
