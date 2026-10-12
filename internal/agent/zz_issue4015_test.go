package agent

import "testing"

// #4015: the strategist completion-marker strip must slice the ORIGINAL
// summary, not a ToUpper copy whose byte offsets can drift (U+0250 ɐ→Ɐ +1B,
// ſ→S −1B) whenever case-expanding characters appear before the marker.

func TestIssue4015StripMarkerGrowthCharBeforeMarker(t *testing.T) {
	// ɐ (U+0250, 2B) uppercases to Ɐ (U+2C6F, 3B): +1 byte in the ToUpper
	// copy, so the old ToUpper-offset slice started one byte too late and
	// ate the first rune of the remainder.
	in := "注意 ɐ done <GOAL_ACHIEVED/> Shipping complete."
	want := "Shipping complete."
	if got := stripStrategistCompleteMarker(in); got != want {
		t.Errorf("growth case: got %q, want %q", got, want)
	}
}

func TestIssue4015StripMarkerShrinkCharBeforeMarker(t *testing.T) {
	// ſ (U+017F, 2B) uppercases to S (1B): −1 byte, so the old slice started
	// one byte too early and kept a stale prefix rune.
	in := "ļ ſ ok <GOAL_ACHIEVED/> Done, all tests green."
	want := "Done, all tests green."
	if got := stripStrategistCompleteMarker(in); got != want {
		t.Errorf("shrink case: got %q, want %q", got, want)
	}
}

func TestIssue4015StripMarkerCanonicalAndFolded(t *testing.T) {
	// Canonical uppercase marker (no length-changing chars): unchanged
	// behavior, original case of the remainder preserved.
	if got := stripStrategistCompleteMarker("intro <GOAL_ACHIEVED/> Shipped v2."); got != "Shipped v2." {
		t.Errorf("canonical: got %q", got)
	}
	// Case-folded marker form must still strip (parity with the old
	// ToUpper-based detection).
	if got := stripStrategistCompleteMarker("intro <goal_achieved/> Shipped v2."); got != "Shipped v2." {
		t.Errorf("folded: got %q", got)
	}
}

func TestIssue4015StripMarkerAbsentUnchanged(t *testing.T) {
	in := "ɐ plain summary without the marker, ſ kept as-is."
	if got := stripStrategistCompleteMarker(in); got != in {
		t.Errorf("absent: got %q, want unchanged %q", got, in)
	}
}

func TestIssue4015StripMarkerResultIsValidUTF8(t *testing.T) {
	// The old misaligned slice could land mid-rune and emit invalid UTF-8;
	// the fix must never do that.
	in := "ɐɐɐ <GOAL_ACHIEVED/> 完成了所有目标。"
	got := stripStrategistCompleteMarker(in)
	if got != "完成了所有目标。" {
		t.Errorf("utf8 case: got %q", got)
	}
}
